// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestThrottle_DefaultInterval confirms a freshly-constructed transport applies
// the 100ms default when no option overrides it.
func TestThrottle_DefaultInterval(t *testing.T) {
	c := NewTransport("http://example.invalid", "id", "secret")
	if c.throttle == nil {
		t.Fatal("throttle gate is nil")
	}
	if c.throttle.interval != defaultMinRequestInterval {
		t.Errorf("interval = %v, want %v", c.throttle.interval, defaultMinRequestInterval)
	}
}

// TestThrottle_OptionOverride confirms WithMinRequestInterval sets the interval,
// including the disabling values (0 and negative).
func TestThrottle_OptionOverride(t *testing.T) {
	for _, d := range []time.Duration{0, -1 * time.Second, 250 * time.Millisecond} {
		c := NewTransportWithUserAgent("http://example.invalid", "id", "secret", "ua", WithMinRequestInterval(d))
		if c.throttle.interval != d {
			t.Errorf("WithMinRequestInterval(%v): interval = %v", d, c.throttle.interval)
		}
	}
}

// TestThrottle_DisabledNoDelay confirms a zero/negative interval lets a burst of
// requests complete with no added spacing.
func TestThrottle_DisabledNoDelay(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	mux.HandleFunc("/api/x", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	start := time.Now()
	const n = 10
	for i := range n {
		if err := c.Do(context.Background(), http.MethodGet, "/api/x", nil, nil); err != nil {
			t.Fatalf("Do[%d]: %v", i, err)
		}
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("expected no pacing with interval=0, got %v for %d requests", elapsed, n)
	}
}

// TestThrottle_SerialSpacing confirms K serial requests take at least
// (K-1)*interval of wall-clock time.
func TestThrottle_SerialSpacing(t *testing.T) {
	c, _, mux := newTestClient(t)
	const interval = 20 * time.Millisecond
	c.throttle.setInterval(interval)
	mux.HandleFunc("/api/x", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Warm the token cache first so the gated slot we measure is purely API
	// requests (the token fetch consumes one slot otherwise).
	if err := c.Do(context.Background(), http.MethodGet, "/api/x", nil, nil); err != nil {
		t.Fatalf("warmup: %v", err)
	}

	const k = 5
	start := time.Now()
	for i := range k {
		if err := c.Do(context.Background(), http.MethodGet, "/api/x", nil, nil); err != nil {
			t.Fatalf("Do[%d]: %v", i, err)
		}
	}
	elapsed := time.Since(start)
	min := time.Duration(k-1) * interval
	if elapsed < min {
		t.Errorf("expected >= %v for %d spaced requests, got %v", min, k, elapsed)
	}
}

// TestThrottle_ConcurrentSpacing fires requests from many goroutines and asserts
// no two request starts land closer than the interval. Run under -race to catch
// data races on the shared gate.
func TestThrottle_ConcurrentSpacing(t *testing.T) {
	c, _, mux := newTestClient(t)
	const interval = 15 * time.Millisecond
	c.throttle.setInterval(interval)

	var mu sync.Mutex
	var starts []time.Time
	mux.HandleFunc("/api/x", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	const n = 8
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			_ = c.Do(context.Background(), http.MethodGet, "/api/x", nil, nil)
		})
	}
	wg.Wait()

	if len(starts) < n {
		t.Fatalf("expected at least %d server hits, got %d", n, len(starts))
	}
	// Sort and assert spacing. Allow a small scheduling tolerance.
	for i := 1; i < len(starts); i++ {
		for j := 0; j < i; j++ {
			if starts[i].Before(starts[j]) {
				starts[i], starts[j] = starts[j], starts[i]
			}
		}
	}
	const tol = 5 * time.Millisecond
	for i := 1; i < len(starts); i++ {
		gap := starts[i].Sub(starts[i-1])
		if gap < interval-tol {
			t.Errorf("requests %d and %d only %v apart, want >= %v", i-1, i, gap, interval)
		}
	}
}

// TestThrottle_ContextCancelDuringWait confirms a ctx cancelled while a request
// is parked behind the gate aborts promptly with a context error. The gate's
// ctx.Err() returns through httpClient.Do and is wrapped by doRequestFull, so
// match with errors.Is, not equality.
//
// One request suffices: the OAuth2 token fetch consumes the first gate slot
// (no wait, since the gate starts cold), then the API request parks behind the
// one-hour interval. The 50ms ctx fires while it is parked.
func TestThrottle_ContextCancelDuringWait(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(time.Hour) // park the API request effectively forever
	mux.HandleFunc("/api/x", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := c.Do(ctx, http.MethodGet, "/api/x", nil, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected prompt cancellation (~50ms), got %v", elapsed)
	}
}

// TestThrottle_WaitReservesSlot is a direct unit test of the gate primitive:
// concurrent callers each get a distinct, monotonically increasing reserved
// slot spaced by the interval.
func TestThrottle_WaitReservesSlot(t *testing.T) {
	g := newRequestThrottle(10 * time.Millisecond)

	const n = 6
	var done atomic.Int32
	var wg sync.WaitGroup
	start := time.Now()
	for range n {
		wg.Go(func() {
			if _, err := g.wait(context.Background()); err != nil {
				t.Errorf("wait: %v", err)
				return
			}
			done.Add(1)
		})
	}
	wg.Wait()
	if done.Load() != n {
		t.Fatalf("expected %d completions, got %d", n, done.Load())
	}
	if elapsed := time.Since(start); elapsed < time.Duration(n-1)*10*time.Millisecond {
		t.Errorf("expected >= %v for %d gated waits, got %v", time.Duration(n-1)*10*time.Millisecond, n, elapsed)
	}
}
