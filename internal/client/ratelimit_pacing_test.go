// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"maps"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// gatewayResponse builds a response carrying the gateway's rate-limit headers
// as observed on the wire: Date and x-ratelimit-reset both in whole seconds,
// reset resetIn after Date.
func gatewayResponse(status int, remaining int, date time.Time, resetIn time.Duration) *http.Response {
	date = date.Truncate(time.Second)
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Date":                  []string{date.UTC().Format(http.TimeFormat)},
			"X-Ratelimit-Limit":     []string{"200"},
			"X-Ratelimit-Remaining": []string{strconv.Itoa(remaining)},
			"X-Ratelimit-Reset":     []string{strconv.FormatInt(date.Add(resetIn).Unix(), 10)},
		},
	}
}

// observed returns a throttle with no interval gate whose first request,
// started at now, was answered with resp at now.
func observed(now time.Time, resp *http.Response) *requestThrottle {
	g := newRequestThrottle(0)
	_, seq := g.reserve(now)
	g.observe(resp, seq, now)
	return g
}

// TestBudget_NoHeadersNoPacing pins that a server sending no rate-limit
// headers — an httptest stub, or a gateway with limiting off — sees exactly the
// behaviour from before the budget gate existed.
func TestBudget_NoHeadersNoPacing(t *testing.T) {
	now := time.Now()
	g := observed(now, &http.Response{StatusCode: http.StatusOK, Header: http.Header{}})
	for i := range 50 {
		if start, _ := g.reserve(now); !start.Equal(now) {
			t.Fatalf("start %d delayed by %v with no rate-limit headers", i, start.Sub(now))
		}
	}
}

// TestBudget_PlentyLeftBarelySpaces pins the claim that the gate leaves
// concurrency alone below the limit: with 199 of 200 left, ten requests
// arriving together all start within a few tens of milliseconds.
func TestBudget_PlentyLeftBarelySpaces(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	g := observed(now, gatewayResponse(http.StatusOK, 199, now, time.Second))
	for i := range 10 {
		if start, _ := g.reserve(now); start.Sub(now) > 60*time.Millisecond {
			t.Fatalf("start %d delayed %v with 199 left; want <= 60ms", i, start.Sub(now))
		}
	}
}

// TestBudget_SpreadsRemainingThenStops pins the slope and the stop together:
// with 19 left in a one-second window, the next 19 starts are spread across
// that window, not bunched at its start, and the 20th waits for the window
// to end.
func TestBudget_SpreadsRemainingThenStops(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	end := now.Add(time.Second)
	g := observed(now, gatewayResponse(http.StatusOK, 19, now, time.Second))

	var prev time.Time
	for i := range 19 {
		start, _ := g.reserve(now)
		if start.After(end) {
			t.Fatalf("start %d at +%v, past the window end; the budget had room", i, start.Sub(now))
		}
		if i > 0 && start.Sub(prev) < 30*time.Millisecond {
			t.Fatalf("starts %d and %d only %v apart; want the budget spread across the window", i-1, i, start.Sub(prev))
		}
		prev = start
	}
	if start, _ := g.reserve(now); start.Before(end.Add(time.Second)) {
		t.Errorf("start with the budget spent at +%v; want >= +2s (window end, padded a second)", start.Sub(now))
	}
}

// TestBudget_CountsInFlightRequests pins that the estimate subtracts requests
// started after the observed one, not after its response arrived. Three
// requests left the client before the first answered "3 left", so the fifth
// start has nothing left and must wait.
func TestBudget_CountsInFlightRequests(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	g := newRequestThrottle(0)
	for range 4 {
		g.reserve(now)
	}
	g.observe(gatewayResponse(http.StatusOK, 3, now, time.Second), 1, now)
	if start, _ := g.reserve(now); start.Before(now.Add(2 * time.Second)) {
		t.Errorf("fifth start at +%v; want held to the window end, since three in-flight requests spent the 3 left", start.Sub(now))
	}
}

// TestBudget_IgnoresOlderResponses pins the ordering rule. Under concurrency an
// older request's response can arrive after a newer one's, carrying a count
// taken earlier and therefore higher. Taking it would hand the budget back.
func TestBudget_IgnoresOlderResponses(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	g := newRequestThrottle(0)
	g.reserve(now)
	g.reserve(now)
	g.observe(gatewayResponse(http.StatusOK, 1, now, time.Second), 2, now)
	g.observe(gatewayResponse(http.StatusOK, 150, now, time.Second), 1, now)

	g.reserve(now) // the one left
	if start, _ := g.reserve(now); start.Before(now.Add(2 * time.Second)) {
		t.Errorf("start at +%v; want held — the late response from an older request must not restore the budget", start.Sub(now))
	}
}

// TestBudget_GatewayRefusalHoldsEveryone pins the hold. A limiter that counts
// refused requests keeps refusing a client that keeps sending, so after one
// 429 every goroutine must stop, and a success that arrives afterwards from a request started
// later must not release them.
func TestBudget_GatewayRefusalHoldsEveryone(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	g := newRequestThrottle(0)
	g.reserve(now)
	g.reserve(now)
	g.observe(gatewayResponse(http.StatusTooManyRequests, 0, now, 0), 1, now)
	g.observe(gatewayResponse(http.StatusOK, 150, now, time.Second), 2, now)

	for i := range 5 {
		start, _ := g.reserve(now)
		if start.Before(now.Add(time.Second)) {
			t.Fatalf("start %d at +%v after a gateway 429; want held to the window end, padded a second", i, start.Sub(now))
		}
		if start.After(now.Add(2 * time.Second)) {
			t.Fatalf("start %d at +%v; want released within a second of jitter after the hold", i, start.Sub(now))
		}
	}
}

// TestBudget_RefusalsSpreadOnRelease pins the jitter on release. Goroutines
// held together would otherwise all start at the same instant.
func TestBudget_RefusalsSpreadOnRelease(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	g := observed(now, gatewayResponse(http.StatusTooManyRequests, 0, now, 0))
	lo, hi := time.Duration(1<<62), time.Duration(0)
	for range 100 {
		start, _ := g.reserve(now)
		d := start.Sub(now)
		lo, hi = min(lo, d), max(hi, d)
	}
	if hi-lo < 500*time.Millisecond {
		t.Errorf("100 held starts released over %v; want spread over most of a second", hi-lo)
	}
}

// TestBudget_ServiceRefusalSetsNoHold replays the blueprints report's 429: the
// service's own refusal, carrying the gateway's headers with 18 left and a
// reset taken when the 10-second request started. The gateway has budget, so
// nothing is held; the refused request's own retry backs off exponentially.
func TestBudget_ServiceRefusalSetsNoHold(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	g := observed(now, gatewayResponse(http.StatusTooManyRequests, 18, now, -9*time.Second))
	if start, _ := g.reserve(now); start.Sub(now) > 100*time.Millisecond {
		t.Errorf("start delayed %v after a service 429; want no hold", start.Sub(now))
	}
}

// TestBudget_SuccessReportingZeroIgnored pins the guard against a limiter that
// reports remaining 0 on every response. Read as an exhausted budget, that
// would hold every request for a whole window.
func TestBudget_SuccessReportingZeroIgnored(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	g := observed(now, gatewayResponse(http.StatusOK, 0, now, time.Second))
	for i := range 10 {
		if start, _ := g.reserve(now); !start.Equal(now) {
			t.Fatalf("start %d delayed %v by a success reporting 0", i, start.Sub(now))
		}
	}
}

// TestBudget_ExpiredWindowIgnored pins that an observation stops applying once
// its window is over. The count it carried belonged to that window.
func TestBudget_ExpiredWindowIgnored(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	g := newRequestThrottle(0)
	_, seq := g.reserve(now.Add(-5 * time.Second))
	g.observe(gatewayResponse(http.StatusOK, 1, now.Add(-5*time.Second), time.Second), seq, now.Add(-5*time.Second))
	for i := range 10 {
		if start, _ := g.reserve(now); !start.Equal(now) {
			t.Fatalf("start %d delayed %v by a window that ended seconds ago", i, start.Sub(now))
		}
	}
}

// TestBudget_HoldClamped pins the ceiling on a hostile or misconfigured reset:
// a reset an hour out holds for at most retryWaitMax plus the pad and jitter.
func TestBudget_HoldClamped(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	g := observed(now, gatewayResponse(http.StatusTooManyRequests, 0, now, time.Hour))
	if start, _ := g.reserve(now); start.Sub(now) > retryWaitMax+2*time.Second {
		t.Errorf("held %v for a reset an hour out; want <= %v", start.Sub(now), retryWaitMax+2*time.Second)
	}
}

// TestBudget_IntervalStillApplies pins that the two gates are independent:
// plenty of budget does not shorten WithMinRequestInterval's spacing.
func TestBudget_IntervalStillApplies(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	g := newRequestThrottle(100 * time.Millisecond)
	_, seq := g.reserve(now)
	g.observe(gatewayResponse(http.StatusOK, 199, now, time.Second), seq, now)
	if start, _ := g.reserve(now); start.Sub(now) < 100*time.Millisecond {
		t.Errorf("start at +%v; want the 100ms interval to apply", start.Sub(now))
	}
}

// TestThrottle_GatewayRefusalHoldsOtherRequests runs the hold through the
// transport: after a gateway 429, a different request issued straight away
// reaches the server only once the window has ended.
func TestThrottle_GatewayRefusalHoldsOtherRequests(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	c.retry.RetryMax = 0

	var refusedAt time.Time
	mux.HandleFunc("/api/refused", func(w http.ResponseWriter, _ *http.Request) {
		refusedAt = time.Now()
		resp := gatewayResponse(http.StatusTooManyRequests, 0, refusedAt, 0)
		maps.Copy(w.Header(), resp.Header)
		w.WriteHeader(http.StatusTooManyRequests)
	})
	var arrivedAt time.Time
	mux.HandleFunc("/api/other", func(w http.ResponseWriter, _ *http.Request) {
		arrivedAt = time.Now()
		w.WriteHeader(http.StatusOK)
	})

	if err := c.Do(context.Background(), http.MethodGet, "/api/refused", nil, nil); err == nil {
		t.Fatal("expected the 429 to surface with retries off")
	}
	if err := c.Do(context.Background(), http.MethodGet, "/api/other", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if held := arrivedAt.Sub(refusedAt); held < 900*time.Millisecond || held > 3*time.Second {
		t.Errorf("next request reached the server %v after the 429; want held about 1-2s for the window", held)
	}
}

// TestThrottle_BudgetLeavesConcurrencyAlone is the consumer-facing guarantee:
// fifteen goroutines against a gateway reporting plenty left all run at once.
// Fifteen is Terraform's default parallelism plus the provider's five-worker
// impact sweep.
func TestThrottle_BudgetLeavesConcurrencyAlone(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)

	var inFlight, peak atomic.Int32
	mux.HandleFunc("/api/x", func(w http.ResponseWriter, _ *http.Request) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
		inFlight.Add(-1)
		resp := gatewayResponse(http.StatusOK, 150, time.Now(), time.Second)
		maps.Copy(w.Header(), resp.Header)
		w.WriteHeader(http.StatusOK)
	})

	// One request first, so the budget is known before the burst.
	if err := c.Do(context.Background(), http.MethodGet, "/api/x", nil, nil); err != nil {
		t.Fatalf("warmup: %v", err)
	}
	peak.Store(0)

	const n = 15
	var wg sync.WaitGroup
	start := time.Now()
	for range n {
		wg.Go(func() {
			if err := c.Do(context.Background(), http.MethodGet, "/api/x", nil, nil); err != nil {
				t.Errorf("Do: %v", err)
			}
		})
	}
	wg.Wait()
	if got := peak.Load(); got < 12 {
		t.Errorf("peak concurrency %d of %d with 150 left; want the requests to overlap", got, n)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("%d concurrent 200ms requests took %v with 150 left; want well under 1s", n, elapsed)
	}
}
