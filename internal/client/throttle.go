// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// defaultMinRequestInterval is the minimum wall-clock spacing applied between
// the start of consecutive outbound requests unless overridden via
// WithMinRequestInterval. 100ms paces the parallel request burst Terraform
// fans out (default -parallelism=10) without materially slowing a serial
// workload.
//
// It was added, with the removal of the transport's eventual-consistency retry
// loop, as a cushion between a write and the requests that follow it, not as
// a rate-limit control. Keeping a client under the gateway's limit is the
// ceiling gate's job, which works with the interval at 0.
const defaultMinRequestInterval = 100 * time.Millisecond

// requestThrottle decides when each outbound request may start. It is shared by
// every goroutine dispatching through the Transport, so it must be
// goroutine-safe; every caller reserves its start time under one lock, in
// arrival order, then sleeps outside it.
//
// Three independent gates feed the reservation, and a start must satisfy all
// of them:
//
//   - The minimum interval (WithMinRequestInterval): a fixed spacing between
//     consecutive starts, whatever the server says.
//   - The ceiling: once a response has carried x-ratelimit-limit, a spacing of
//     limitSpacing(limit) between consecutive starts, so a client running flat
//     out stays a fifth under the limit. See limitSpacing.
//   - The gateway's rate-limit budget, read from the x-ratelimit-remaining and
//     x-ratelimit-reset headers of earlier responses. Always on, and
//     unaffected by the interval. It does nothing until a response has carried
//     those headers, so a server that sends none (an httptest stub, or a
//     gateway with limiting off) sees no change.
//
// The budget gate spreads the requests the gateway says are left across the
// time left in its window: each start comes at least
// (time until reset) / (estimated remaining) after the previous one. With
// plenty left that is a few milliseconds, which leaves concurrency alone;
// as the budget drains the spacing grows, and when the estimate reaches zero
// starts wait for the window to end. That is the published guidance — slow
// down before the limit rather than waiting for a 429 — and nothing in it
// encodes the limit or the window length, which are gateway configuration.
// See docs/WIRE-FACTS.md#rate-limiting.
//
// The budget gate alone does not keep an interval-0 client under a one-second
// window, for two wire reasons. x-ratelimit-reset and Date are whole seconds,
// so the time until reset reads as 0 for most of the window, and at 0 there is
// nothing to spread the budget across. And x-ratelimit-remaining is not the
// global budget: across one window of 100, each value from 19 down to 0
// appears five times. The ceiling covers both.
//
// Each gate is a gap measured from the same base, the start after any hold,
// and the stricter one wins. Measured from each other's result instead, their
// gaps would add.
//
// The estimate is the remaining count from the response to the most recently
// *started* request that has answered, minus every request started after it.
// Counting from the start rather than from the response's arrival is what
// covers the caller's own in-flight requests: those were counted by the
// gateway after that remaining was taken, and arrive later than it. Responses
// from older requests, which arrive out of order under concurrency, are
// ignored for the same reason.
//
// A gateway 429 — remaining 0 on the refusal itself — also sets a hold that no
// later response lifts, so every goroutine stops until the window ends. The
// published guidance's first step on a 429 is to stop sending from that
// client, and a limiter that counts refused requests in its window would keep
// refusing a client whose other goroutines kept sending after a refusal.
type requestThrottle struct {
	mu       sync.Mutex
	interval time.Duration // <= 0 disables the interval gate
	last     time.Time     // start time reserved for the most recent caller
	seq      uint64        // number of starts reserved so far
	limit    int64         // last x-ratelimit-limit seen; 0 until one is

	budgetKnown bool      // a response has carried the headers
	budgetSeq   uint64    // seq of the request that response answered
	budgetLeft  int64     // its x-ratelimit-remaining
	windowEnd   time.Time // local time its x-ratelimit-reset falls at
	hold        time.Time // set by a gateway 429; no start before it
}

// newRequestThrottle returns a throttle with the given minimum interval.
func newRequestThrottle(interval time.Duration) *requestThrottle {
	return &requestThrottle{interval: interval}
}

// setInterval updates the minimum interval. Only called during client
// construction (single-threaded), before any request is dispatched, so it
// needs no synchronisation against wait.
func (t *requestThrottle) setInterval(d time.Duration) {
	t.interval = d
}

// wait blocks until the caller's reserved request-start slot is reached, then
// returns the sequence number of the start, which the caller hands back to
// observe with the response. A cancelled ctx aborts the wait and returns
// ctx.Err(); the reserved slot is not rolled back, which at worst spaces the
// next caller slightly further out.
func (t *requestThrottle) wait(ctx context.Context) (uint64, error) {
	now := time.Now()
	start, seq := t.reserve(now)
	wait := start.Sub(now)
	if wait <= 0 {
		return seq, nil
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-timer.C:
		return seq, nil
	case <-ctx.Done():
		return seq, ctx.Err()
	}
}

// reserve returns the earliest time a request arriving at now may start, and
// records it as reserved. See requestThrottle for the three gates.
//
// The budget's window end is padded by a second wherever it decides that a
// start must wait for the window, because x-ratelimit-reset and Date are both
// whole seconds and the window ends somewhere in the second after reset (the
// same pad rateLimitResetWait applies). Starts released at the end of a wait
// are jittered across a further second, so goroutines held together do not
// all start at the same instant. The spacing uses the unpadded end, so a
// client with budget left is not slowed by the rounding.
func (t *requestThrottle) reserve(now time.Time) (time.Time, uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.seq++
	seq := t.seq
	start := now
	if start.Before(t.hold) {
		start = t.hold.Add(rand.N(time.Second))
	}
	base := start
	if t.interval > 0 {
		start = later(start, t.last.Add(t.interval))
	}
	if t.limit > 0 {
		start = later(start, t.last.Add(limitSpacing(t.limit)))
	}
	if paddedEnd := t.windowEnd.Add(time.Second); t.budgetKnown && base.Before(paddedEnd) {
		left := t.budgetLeft - int64(seq-t.budgetSeq-1)
		if left <= 0 {
			start = later(start, paddedEnd.Add(rand.N(time.Second)))
		} else if from := later(base, t.last); from.Before(t.windowEnd) {
			// The time left after the previous start, shared between the
			// requests left, so the budget is spread evenly to the window
			// end rather than front-loaded.
			start = later(start, from.Add(t.windowEnd.Sub(from)/time.Duration(left)))
		}
	}
	t.last = start
	return start, seq
}

// observe records the rate-limit headers of the response to the request that
// started as seq, received at recv. A response without them changes nothing.
//
// x-ratelimit-limit is read on its own, whether or not remaining and reset
// parse, and the last positive value seen wins. Tyk's 0 means limiting is
// disabled and sets nothing.
//
// A success reporting remaining 0 is ignored rather than read as an exhausted
// budget. A limiter that reports 0 on every response would otherwise hold
// every request for a whole window. The cost of ignoring a genuine 0 is that
// the next request draws the 429 that sets the hold.
func (t *requestThrottle) observe(resp *http.Response, seq uint64, recv time.Time) {
	if limit, err := strconv.ParseInt(resp.Header.Get("X-Ratelimit-Limit"), 10, 64); err == nil && limit > 0 {
		t.mu.Lock()
		t.limit = limit
		t.mu.Unlock()
	}
	remaining, untilReset, ok := parseRateLimitHeaders(resp.Header, retryWaitMax)
	if !ok {
		return
	}
	refused := resp.StatusCode == http.StatusTooManyRequests && remaining == 0
	if remaining == 0 && !refused {
		return
	}
	end := recv.Add(untilReset)

	t.mu.Lock()
	defer t.mu.Unlock()
	if refused {
		t.hold = later(t.hold, end.Add(time.Second))
	}
	if !t.budgetKnown || seq > t.budgetSeq {
		t.budgetKnown, t.budgetSeq, t.budgetLeft, t.windowEnd = true, seq, remaining, end
	}
}

// limitSpacing is the minimum gap between starts once the gateway has reported
// its limit: the published one-second window shared across 80% of the limit,
// so a client running flat out stays a fifth under it (12.5ms, or 80 a second,
// at today's 100). The limit comes off the wire; only the window is assumed,
// and it is the one the platform documentation publishes.
func limitSpacing(limit int64) time.Duration {
	return (time.Second + time.Second/4) / time.Duration(limit)
}

// later returns whichever of a and b is later.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// throttleTransport is an http.RoundTripper that paces every outbound request
// through a shared requestThrottle before delegating to the base transport,
// and reports each response back to it. Installed at the base of the
// transport chain so it covers API calls, multipart uploads, OAuth2 token
// fetches and every retry attempt alike.
type throttleTransport struct {
	base     http.RoundTripper
	throttle *requestThrottle
}

// RoundTrip waits for the throttle, then delegates. A ctx cancelled during the
// wait surfaces as ctx.Err() without a network round-trip.
func (t *throttleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	seq, err := t.throttle.wait(req.Context())
	if err != nil {
		return nil, err
	}
	resp, err := t.base.RoundTrip(req)
	if err == nil {
		t.throttle.observe(resp, seq, time.Now())
	}
	return resp, err
}
