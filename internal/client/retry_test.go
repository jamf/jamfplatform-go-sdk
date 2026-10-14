// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- no-retry-on-4xx regression tests ---
//
// The transport must NOT retry ordinary 4xx responses (everything except
// 429, which is a standardized "slow down" signal, not a client mistake).
// Eventual-consistency handling of what a *specific* 4xx means for a
// specific endpoint is a caller concern — see jamfCheckRetry's doc. These
// guards assert a single dispatch and an immediately-surfaced
// *APIResponseError.

func TestNoRetryOn4xx_DeleteSingleCall(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0) // isolate retry behavior from request pacing
	var calls atomic.Int32
	mux.HandleFunc("/api/eventual", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	})

	start := time.Now()
	err := c.DoExpect(context.Background(), http.MethodDelete, "/api/eventual", nil, http.StatusNoContent, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusBadRequest) {
		t.Fatalf("expected APIResponseError(400), got %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("expected exactly 1 call (no 4xx retry), got %d", calls.Load())
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected immediate surface, got %v (looks like a backoff loop)", elapsed)
	}
}

func TestNoRetryOn4xx_GetSingleCall(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	var calls atomic.Int32
	mux.HandleFunc("/api/missing", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	})

	start := time.Now()
	err := c.Do(context.Background(), http.MethodGet, "/api/missing", nil, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusNotFound) {
		t.Fatalf("expected APIResponseError(404), got %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("expected exactly 1 call (no 4xx retry), got %d", calls.Load())
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected immediate surface, got %v", elapsed)
	}
}

// TestNoRetryOn4xx_Delete404IsError pins the deliberate behavior change: a 404
// on DELETE now surfaces as *APIResponseError(404) rather than being swallowed
// as idempotent-delete success. The "404-on-DELETE == success" judgement is a
// lifecycle semantic the consumer owns.
func TestNoRetryOn4xx_Delete404IsError(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	var calls atomic.Int32
	mux.HandleFunc("/api/gone", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
	})

	err := c.DoExpect(context.Background(), http.MethodDelete, "/api/gone", nil, http.StatusNoContent, nil)
	if err == nil {
		t.Fatal("expected APIResponseError(404), got nil (404-on-DELETE must no longer be success)")
	}
	var apiErr *APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusNotFound) {
		t.Fatalf("expected APIResponseError(404), got %v", err)
	}
	if calls.Load() != 1 {
		t.Errorf("expected exactly 1 call, got %d", calls.Load())
	}
}

// shrinkRetryWaits narrows a test client's backoff bounds so retry tests run
// in milliseconds instead of the production 1s-60s window. Mirrors the
// existing c.throttle.setInterval(0) pattern used to isolate pacing from
// retry behavior.
func shrinkRetryWaits(c *Transport, minWait, maxWait time.Duration) {
	c.retry.RetryWaitMin = minWait
	c.retry.RetryWaitMax = maxWait
}

// --- retry policy tests (transport-level automatic retry) ---

func TestIsRetryableWriteStatus(t *testing.T) {
	cases := []struct {
		method string
		status int
		want   bool
	}{
		{http.MethodPost, http.StatusTooManyRequests, true},      // 429 always retryable, method-agnostic
		{http.MethodPost, http.StatusServiceUnavailable, true},   // 503 always retryable, method-agnostic
		{http.MethodPost, http.StatusNotImplemented, false},      // 501 never retryable
		{http.MethodGet, http.StatusInternalServerError, true},   // 500 on idempotent method
		{http.MethodDelete, http.StatusBadGateway, true},         // 502 on idempotent method
		{http.MethodPut, http.StatusGatewayTimeout, true},        // 504 on idempotent method — see DoWithContentTypeNoRetry for the small set of PUT endpoints that must opt out instead
		{http.MethodHead, http.StatusInternalServerError, true},  // 500 on idempotent method
		{http.MethodPost, http.StatusInternalServerError, false}, // 500 on non-idempotent method — don't risk double-apply
		{http.MethodPatch, http.StatusBadGateway, false},         // 502 on non-idempotent method
		{http.MethodPost, http.StatusBadRequest, false},          // ordinary 4xx never retryable
		{http.MethodGet, http.StatusNotFound, false},             // ordinary 4xx never retryable
		{http.MethodGet, 0, true},                                // malformed/zero status treated like a 5xx on idempotent method
	}
	for _, tc := range cases {
		if got := isRetryableWriteStatus(tc.method, tc.status); got != tc.want {
			t.Errorf("isRetryableWriteStatus(%s, %d) = %v, want %v", tc.method, tc.status, got, tc.want)
		}
	}
}

// TestJamfBackoff_ClampsUncappedRetryAfter pins the deliberate deviation
// from retryablehttp.RateLimitLinearJitterBackoff: a server-supplied
// Retry-After that exceeds max must be clamped, not honored verbatim.
func TestJamfBackoff_ClampsUncappedRetryAfter(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"120"}},
	}
	got := jamfBackoff(1*time.Second, 5*time.Second, 0, resp)
	if got != 5*time.Second {
		t.Errorf("jamfBackoff with Retry-After=120s and max=5s = %v, want 5s (clamped)", got)
	}
}

func TestJamfBackoff_HonorsRetryAfterUnderCap(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     http.Header{"Retry-After": []string{"2"}},
	}
	got := jamfBackoff(1*time.Second, 60*time.Second, 0, resp)
	if got != 2*time.Second {
		t.Errorf("jamfBackoff with Retry-After=2s and max=60s = %v, want 2s (unclamped)", got)
	}
}

// TestRetryOn500_GetSucceedsAfterRetry pins the new method-aware 5xx retry:
// a GET (idempotent) may be safely retried on a transient 500.
func TestRetryOn500_GetSucceedsAfterRetry(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	shrinkRetryWaits(c, 5*time.Millisecond, 20*time.Millisecond)
	var calls atomic.Int32
	mux.HandleFunc("/api/flaky", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	err := c.Do(context.Background(), http.MethodGet, "/api/flaky", nil, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 calls (initial 500 + retry), got %d", got)
	}
}

// TestRetryOn500_PostDoesNotRetry pins the non-idempotency guard: a POST
// must NOT be retried on a bare 500, since the server may have already
// applied it before the client saw the failure.
func TestRetryOn500_PostDoesNotRetry(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	shrinkRetryWaits(c, 5*time.Millisecond, 20*time.Millisecond)
	var calls atomic.Int32
	mux.HandleFunc("/api/create", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})

	err := c.Do(context.Background(), http.MethodPost, "/api/create", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusInternalServerError) {
		t.Fatalf("expected APIResponseError(500), got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 call (no retry on POST+500), got %d", got)
	}
}

// TestDoWithContentTypeNoRetry_SkipsAutoRetryOnPut pins the opt-out
// mechanism itself: a PUT through DoWithContentTypeNoRetry must dispatch
// exactly once on a 500, even though the same call through DoWithContentType
// would retry (see TestRetryOn500_GetSucceedsAfterRetry for the retrying
// case on a different idempotent method).
func TestDoWithContentTypeNoRetry_SkipsAutoRetryOnPut(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	shrinkRetryWaits(c, 2*time.Millisecond, 5*time.Millisecond)
	var calls atomic.Int32
	mux.HandleFunc("/api/versionlocked", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})

	err := c.DoWithContentTypeNoRetry(context.Background(), http.MethodPut, "/api/versionlocked", nil, "application/json", http.StatusOK, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusInternalServerError) {
		t.Fatalf("expected APIResponseError(500), got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly 1 call (DoWithContentTypeNoRetry must not auto-retry), got %d", got)
	}
}

// TestRetryExhausted_StillDecodesAPIResponseError is the most important
// regression guard in this file: retryablehttp's own default behavior on
// retry exhaustion is to drain and discard the final response body and
// return a synthetic "giving up after N attempts" error instead — which
// would silently break every caller inspecting the concrete
// *APIResponseError (helpers.IsNotFoundError, IsServerError, AsAPIError,
// .TraceID, .Errors, …) the moment a persistent failure ever exhausted
// retries. newRetryClient's ErrorHandler (PassthroughErrorHandler) exists
// specifically to prevent this; this test would fail without it.
func TestRetryExhausted_StillDecodesAPIResponseError(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	shrinkRetryWaits(c, 2*time.Millisecond, 5*time.Millisecond)
	c.retry.RetryMax = 2
	var calls atomic.Int32
	mux.HandleFunc("/api/downhard", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("X-Traceid", "trace-persistent-500")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"httpStatus":500,"errors":[{"code":"SERVER_ERROR","description":"nope"}]}`))
	})

	err := c.Do(context.Background(), http.MethodGet, "/api/downhard", nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIResponseError to survive retry exhaustion, got %T: %v", err, err)
	}
	if !apiErr.HasStatus(http.StatusInternalServerError) {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if apiErr.TraceID != "trace-persistent-500" {
		t.Errorf("TraceID = %q, want %q (structured details must survive retry exhaustion)", apiErr.TraceID, "trace-persistent-500")
	}
	if len(apiErr.Errors) != 1 || apiErr.Errors[0].Code != "SERVER_ERROR" {
		t.Errorf("Errors = %+v, want the persistent 500's structured detail intact", apiErr.Errors)
	}
	if got := calls.Load(); got != 3 { // RetryMax=2 → 3 total attempts
		t.Errorf("expected 3 total attempts (RetryMax=2 retries + initial), got %d", got)
	}
}

// --- 429/Retry-After tests ---

func TestRetryAfter_IntegerSeconds(t *testing.T) {
	c, srv, mux := newTestClient(t)
	c.throttle.setInterval(0)
	c.retry.RetryMax = 1
	var calls atomic.Int32
	mux.HandleFunc("/api/rate", func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	start := time.Now()
	err := c.Do(context.Background(), http.MethodGet, "/api/rate", nil, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls (initial + retry), got %d", calls.Load())
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("expected ~1s wait before retry, got %v", elapsed)
	}
	_ = srv
}

// TestRetryAfter_MissingRetriesWithBackoff pins a deliberate behavior change
// from the old hand-rolled loop: previously a 429 with no Retry-After header
// surfaced immediately (1 call). retryablehttp's default policy retries 429
// unconditionally and falls back to jittered backoff when Retry-After is
// absent — a bare 429 still means "slow down," and blind backoff is the
// standard mitigation, so this is treated as a genuine improvement rather
// than a regression to guard against.
func TestRetryAfter_MissingRetriesWithBackoff(t *testing.T) {
	c, srv, mux := newTestClient(t)
	c.throttle.setInterval(0)
	shrinkRetryWaits(c, 2*time.Millisecond, 5*time.Millisecond)
	c.retry.RetryMax = 2
	var calls atomic.Int32
	mux.HandleFunc("/api/rate", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	})

	err := c.Do(context.Background(), http.MethodGet, "/api/rate", nil, nil)
	if err == nil {
		t.Fatalf("expected error on persistent 429, got nil")
	}
	var apiErr *APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusTooManyRequests) {
		t.Fatalf("expected APIResponseError(429), got %v", err)
	}
	if got := calls.Load(); got != 3 { // RetryMax=2 → 3 total attempts
		t.Errorf("expected 3 total attempts (retry now applies even without Retry-After), got %d", got)
	}
	_ = srv
}

// TestRetryAfter_OverCapIsClampedNotAbandoned pins a deliberate behavior
// change from the old hand-rolled loop: previously a Retry-After exceeding
// the 60s cap abandoned the retry entirely (1 call). The library honors any
// Retry-After verbatim and uncapped, so jamfBackoff clamps it to
// RetryWaitMax instead of giving up — retrying with a bounded wait is more
// useful than surfacing immediately just because the server asked for a
// wait longer than our ceiling.
func TestRetryAfter_OverCapIsClampedNotAbandoned(t *testing.T) {
	c, srv, mux := newTestClient(t)
	c.throttle.setInterval(0)
	shrinkRetryWaits(c, 2*time.Millisecond, 5*time.Millisecond) // clamp ceiling under test
	c.retry.RetryMax = 1
	var calls atomic.Int32
	mux.HandleFunc("/api/rate", func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "120") // far above the 5ms test ceiling
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	start := time.Now()
	err := c.Do(context.Background(), http.MethodGet, "/api/rate", nil, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 calls (retried despite over-cap Retry-After), got %d", got)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected the 120s Retry-After to be clamped to ~5ms, wait was %v", elapsed)
	}
	_ = srv
}

// TestJamfBackoff_Exponential pins the curve the SDK gets from
// retryablehttp.DefaultBackoff, and is the regression guard for the reason
// jamfBackoff no longer uses RateLimitLinearJitterBackoff: that function
// sampled uniformly over the entire [min,max] window and multiplied by
// attempt number, so with these same 1s/60s bounds the FIRST retry waited
// ~30s on average and a four-retry sequence averaged over three minutes. The
// exponential curve bounds the same sequence at 1+2+4+8 = 15s.
func TestJamfBackoff_Exponential(t *testing.T) {
	for attempt, want := range map[int]time.Duration{
		0: 1 * time.Second,
		1: 2 * time.Second,
		2: 4 * time.Second,
		3: 8 * time.Second,
	} {
		if got := jamfBackoff(1*time.Second, 60*time.Second, attempt, nil); got != want {
			t.Errorf("jamfBackoff(1s, 60s, %d, nil) = %v, want %v", attempt, got, want)
		}
	}
}

// TestJamfBackoff_ExponentialCappedAtMax covers a caller who raises
// maxRetries via WithRetryPolicy far enough that the doubling would overshoot
// the ceiling.
func TestJamfBackoff_ExponentialCappedAtMax(t *testing.T) {
	if got := jamfBackoff(1*time.Second, 10*time.Second, 20, nil); got != 10*time.Second {
		t.Errorf("jamfBackoff(1s, 10s, 20, nil) = %v, want 10s (capped)", got)
	}
}

// TestJamfBackoff_RetryAfterDateForm pins that the date form of Retry-After
// is honored, not just delay-seconds. A seconds-only parser treats a date as
// absent and silently discards the only backpressure the server sent.
func TestJamfBackoff_RetryAfterDateForm(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{"Retry-After": []string{
			time.Now().Add(3 * time.Second).UTC().Format(time.RFC1123),
		}},
	}
	got := jamfBackoff(1*time.Second, 60*time.Second, 0, resp)
	if got < 1*time.Second || got > 4*time.Second {
		t.Errorf("jamfBackoff with Retry-After date ~3s away = %v, want ~3s", got)
	}
}

// TestJamfBackoff_RetryAfterIgnoredOnOther5xx pins that Retry-After only
// steers the wait for 429/503. A 500 carrying the header stays on the
// exponential curve — the header is a rate-limit signal, and honoring it on
// an arbitrary 5xx would let one confused upstream dictate client timing.
func TestJamfBackoff_RetryAfterIgnoredOnOther5xx(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     http.Header{"Retry-After": []string{"30"}},
	}
	if got := jamfBackoff(1*time.Second, 60*time.Second, 0, resp); got != 1*time.Second {
		t.Errorf("jamfBackoff(500 + Retry-After: 30) = %v, want 1s (exponential curve)", got)
	}
}

// tyk429 builds the gateway's rate-limit refusal as observed on 2026-10-14
// (docs/WIRE-FACTS.md#rate-limiting): no Retry-After, and x-ratelimit-reset
// offset from the response's Date by resetIn.
func tyk429(date time.Time, resetIn time.Duration) *http.Response {
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header: http.Header{
			"Date":                  []string{date.UTC().Format(http.TimeFormat)},
			"X-Generator":           []string{"tyk.io"},
			"X-Ratelimit-Limit":     []string{"100"},
			"X-Ratelimit-Remaining": []string{"0"},
			"X-Ratelimit-Reset":     []string{strconv.FormatInt(date.Add(resetIn).Unix(), 10)},
		},
	}
}

// TestJamfBackoff_RateLimitResetWaitsForWindow pins the wait for a 429 that
// carries x-ratelimit-reset and no Retry-After: the time until reset, plus one
// second for the headers' whole-second resolution, plus up to one second of
// jitter. A reset already in the past waits the same as one in the current
// second.
func TestJamfBackoff_RateLimitResetWaitsForWindow(t *testing.T) {
	date := time.Now().Truncate(time.Second)
	for _, tc := range []struct {
		name    string
		resetIn time.Duration
		lo      time.Duration
	}{
		{"same second as Date (as observed)", 0, 1 * time.Second},
		{"three seconds ahead", 3 * time.Second, 4 * time.Second},
		{"already past", -10 * time.Second, 1 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := jamfBackoff(1*time.Second, 60*time.Second, 0, tyk429(date, tc.resetIn))
			if got < tc.lo || got >= tc.lo+time.Second {
				t.Errorf("jamfBackoff = %v, want in [%v, %v)", got, tc.lo, tc.lo+time.Second)
			}
		})
	}
}

// TestJamfBackoff_RateLimitResetUsesServerDate pins that the wait is measured
// against the response's Date, not the local clock. Date here is an hour
// behind the local clock. Measured locally, the reset would be long past and
// the wait would be the 1s floor.
func TestJamfBackoff_RateLimitResetUsesServerDate(t *testing.T) {
	date := time.Now().Add(-time.Hour).Truncate(time.Second)
	got := jamfBackoff(1*time.Second, 60*time.Second, 0, tyk429(date, 2*time.Second))
	if got < 3*time.Second || got >= 4*time.Second {
		t.Errorf("jamfBackoff = %v, want in [3s, 4s) measured from Date", got)
	}
}

// TestJamfBackoff_RateLimitResetFallsBackToLocalClock covers a 429 with no
// Date header.
func TestJamfBackoff_RateLimitResetFallsBackToLocalClock(t *testing.T) {
	resp := tyk429(time.Now(), 3*time.Second)
	resp.Header.Del("Date")
	got := jamfBackoff(1*time.Second, 60*time.Second, 0, resp)
	if got < 3*time.Second || got >= 5*time.Second {
		t.Errorf("jamfBackoff = %v, want in [3s, 5s)", got)
	}
}

// TestJamfBackoff_RateLimitResetClamped pins the maxWait ceiling on the reset
// path. A limit with a long window must not stall a request past what
// WithRetryPolicy promises.
func TestJamfBackoff_RateLimitResetClamped(t *testing.T) {
	got := jamfBackoff(1*time.Second, 5*time.Second, 0, tyk429(time.Now(), time.Hour))
	if got != 5*time.Second {
		t.Errorf("jamfBackoff with reset an hour away and max=5s = %v, want 5s (clamped)", got)
	}
}

// TestJamfBackoff_RetryAfterBeatsRateLimitReset pins the precedence. No Jamf
// source observed sends Retry-After, but if a 429 carries one, that header
// decides the wait.
func TestJamfBackoff_RetryAfterBeatsRateLimitReset(t *testing.T) {
	resp := tyk429(time.Now(), 0)
	resp.Header.Set("Retry-After", "5")
	if got := jamfBackoff(1*time.Second, 60*time.Second, 0, resp); got != 5*time.Second {
		t.Errorf("jamfBackoff with Retry-After=5 and reset now = %v, want 5s", got)
	}
}

// TestJamfBackoff_RateLimitResetIgnored pins the cases that stay on the
// exponential curve: a reset that cannot be used, and a status other than 429.
func TestJamfBackoff_RateLimitResetIgnored(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*http.Response)
	}{
		{"unparseable reset", func(r *http.Response) { r.Header.Set("X-Ratelimit-Reset", "soon") }},
		{"zero reset (Tyk with limiting disabled)", func(r *http.Response) { r.Header.Set("X-Ratelimit-Reset", "0") }},
		{"absent reset", func(r *http.Response) { r.Header.Del("X-Ratelimit-Reset") }},
		{"503 carrying a reset", func(r *http.Response) { r.StatusCode = http.StatusServiceUnavailable }},
		{"remaining above 0", func(r *http.Response) { r.Header.Set("X-Ratelimit-Remaining", "18") }},
		{"absent remaining", func(r *http.Response) { r.Header.Del("X-Ratelimit-Remaining") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := tyk429(time.Now(), 3*time.Second)
			tc.edit(resp)
			if got := jamfBackoff(1*time.Second, 60*time.Second, 0, resp); got != 1*time.Second {
				t.Errorf("jamfBackoff = %v, want 1s (exponential curve)", got)
			}
		})
	}
}

// TestJamfBackoff_ServiceOverload429Escalates replays the blueprints report's
// 429 as observed on 2026-10-14: the service's ApiError, with the gateway's
// headers stamped on it, remaining 18 and a reset taken when the 10-second
// request started, 9s before Date. Reading that reset would retry an
// overloaded data store every second or so. The wait must grow with each
// attempt instead.
func TestJamfBackoff_ServiceOverload429Escalates(t *testing.T) {
	date := time.Now().Truncate(time.Second)
	resp := tyk429(date, -9*time.Second)
	resp.Header.Del("X-Generator")
	resp.Header.Set("X-Ratelimit-Remaining", "18")
	for attempt, want := range map[int]time.Duration{0: 1 * time.Second, 1: 2 * time.Second, 2: 4 * time.Second} {
		if got := jamfBackoff(1*time.Second, 60*time.Second, attempt, resp); got != want {
			t.Errorf("attempt %d: jamfBackoff = %v, want %v (exponential curve)", attempt, got, want)
		}
	}
}

// TestJamfBackoff_RateLimitResetJitters pins the jitter. Without it, requests
// refused together all retry at the same instant at the start of the next
// window.
func TestJamfBackoff_RateLimitResetJitters(t *testing.T) {
	resp := tyk429(time.Now(), 0)
	first := jamfBackoff(1*time.Second, 60*time.Second, 0, resp)
	for range 50 {
		if jamfBackoff(1*time.Second, 60*time.Second, 0, resp) != first {
			return
		}
	}
	t.Errorf("51 waits for the same 429 were all %v; want jittered", first)
}

// TestRateLimitReset_GatewayRefusedPostIsRetried runs the observed gateway 429
// through the transport. The refusal comes from Tyk before the service sees
// the request, so even a POST is retried, and the retry gets through.
func TestRateLimitReset_GatewayRefusedPostIsRetried(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	shrinkRetryWaits(c, 2*time.Millisecond, 5*time.Millisecond) // clamp the reset wait under test
	c.retry.RetryMax = 1
	var calls atomic.Int32
	mux.HandleFunc("/api/rate", func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			maps.Copy(w.Header(), tyk429(time.Now(), 0).Header)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("{\n    \"error\": \"Rate Limit Exceeded\"\n}"))
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := c.Do(context.Background(), http.MethodPost, "/api/rate", map[string]string{"k": "v"}, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("expected 2 calls (refused, then retried), got %d", got)
	}
}

// TestRetryAttemptsAreLogged is the visibility guard. Before the
// RequestLogHook existed, execute() logged one request, handed the whole
// retry loop to a single httpClient.Do, and handleResponse logged only the
// surviving response — so a four-retry sequence and a single slow call
// produced identical output, which is what made a multi-minute retry
// sequence read as a hang.
func TestRetryAttemptsAreLogged(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	shrinkRetryWaits(c, 1*time.Millisecond, 2*time.Millisecond)

	var logged atomic.Int32
	c.SetLogger(&testLogger{onRequest: func() { logged.Add(1) }})

	var calls atomic.Int32
	mux.HandleFunc("/api/flaky-logged", func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	if err := c.Do(context.Background(), http.MethodGet, "/api/flaky-logged", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected 3 attempts (500, 500, 200), got %d", got)
	}
	// One line from execute() for attempt 1, one from the hook for each of
	// the two retries. The hook skips attempt 0 precisely so it does not
	// double-log the request execute() already emitted.
	if got := logged.Load(); got != 3 {
		t.Errorf("expected 3 logged requests (one per attempt), got %d", got)
	}
}

// retryStatusLogger records every status handed to LogResponse, plus a count
// of LogRequest calls, so a test can assert on the *shape* of a retry
// sequence in the log rather than just its length.
type retryStatusLogger struct {
	mu       sync.Mutex
	requests int
	statuses []int
}

func (l *retryStatusLogger) LogRequest(_ context.Context, _, _ string, _ []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests++
}

func (l *retryStatusLogger) LogResponse(_ context.Context, statusCode int, _ http.Header, _ []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.statuses = append(l.statuses, statusCode)
}

func (l *retryStatusLogger) snapshot() (int, []int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.requests, slices.Clone(l.statuses)
}

// TestRetriedStatusIsLogged is the other half of TestRetryAttemptsAreLogged,
// and it guards a diagnosis cost this SDK has already paid: a package DELETE
// that 500s and is retried can answer 404 on the retry, and with only the
// request half of the retry logging in place the log read "request, request,
// 404" — the 500 that caused the retry was nowhere, so four package tests
// failed on a 404 whose real cause took a wire probe to establish (see
// requirePackageStore in jamfplatform/acc_helpers_test.go). Both statuses
// must now be in the log.
func TestRetriedStatusIsLogged(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	shrinkRetryWaits(c, 1*time.Millisecond, 2*time.Millisecond)

	logger := &retryStatusLogger{}
	c.SetLogger(logger)

	var calls atomic.Int32
	mux.HandleFunc("/api/packages/1", func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	err := c.DoExpect(context.Background(), http.MethodDelete, "/api/packages/1", nil, http.StatusNoContent, nil)
	if err == nil {
		t.Fatal("expected the retry's 404 to surface as an error")
	}
	var apiErr *APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusNotFound) {
		t.Fatalf("expected APIResponseError(404), got %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 attempts (500 then 404), got %d", got)
	}

	requests, statuses := logger.snapshot()
	if requests != 2 {
		t.Errorf("expected 2 logged requests (execute() for attempt 1, the hook for the retry), got %d", requests)
	}
	// The 500 comes from the ResponseLogHook, the 404 from handleResponse.
	// Order matters: the log has to read 500-then-404 for the first failure to
	// be attributable to the attempt it belongs to.
	if !slices.Equal(statuses, []int{http.StatusInternalServerError, http.StatusNotFound}) {
		t.Errorf("logged statuses = %v, want [500 404] — the 500 that caused the retry must be in the log", statuses)
	}
}

// TestNonRetriedResponseIsLoggedOnce pins the gate on logRetriedResponse: the
// hook fires after *every* attempt's response, including the final successful
// one that handleResponse already logs, so an ungated hook would double-log
// every request the SDK makes.
func TestNonRetriedResponseIsLoggedOnce(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)

	logger := &retryStatusLogger{}
	c.SetLogger(logger)

	mux.HandleFunc("/api/fine", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	if err := c.Do(context.Background(), http.MethodGet, "/api/fine", nil, nil); err != nil {
		t.Fatalf("Do: %v", err)
	}

	requests, statuses := logger.snapshot()
	if requests != 1 {
		t.Errorf("expected 1 logged request, got %d", requests)
	}
	if !slices.Equal(statuses, []int{http.StatusOK}) {
		t.Errorf("logged statuses = %v, want exactly [200] (the hook must not double-log a response nothing retried)", statuses)
	}
}

// TestNonRetryablePostErrorIsLoggedOnce covers the same gate on the path where
// the status *is* a 5xx but the method makes it non-retryable: a POST 500 is
// surfaced immediately (see TestRetryOn500_PostDoesNotRetry), so it must
// appear once, from handleResponse. The gate reuses jamfCheckRetry precisely
// so it cannot disagree with that decision.
func TestNonRetryablePostErrorIsLoggedOnce(t *testing.T) {
	c, _, mux := newTestClient(t)
	c.throttle.setInterval(0)
	shrinkRetryWaits(c, 1*time.Millisecond, 2*time.Millisecond)

	logger := &retryStatusLogger{}
	c.SetLogger(logger)

	mux.HandleFunc("/api/create-logged", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if err := c.Do(context.Background(), http.MethodPost, "/api/create-logged", nil, nil); err == nil {
		t.Fatal("expected error, got nil")
	}

	_, statuses := logger.snapshot()
	if !slices.Equal(statuses, []int{http.StatusInternalServerError}) {
		t.Errorf("logged statuses = %v, want exactly [500] (a non-retried 500 must not be logged twice)", statuses)
	}
}
