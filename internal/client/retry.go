// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

// retryWaitMin/retryWaitMax/retryMax bound the transport's automatic retry
// of transient failures (429/503, 500/502/504 on GET/DELETE/PUT/HEAD, and
// recoverable network errors — see isRetryableWriteStatus). Total attempts
// per request is retryMax+1.
//
// retryWaitMin seeds the exponential curve (see jamfBackoff), so these
// values bound a full retry sequence at 1+2+4+8 = 15s of waiting.
// retryWaitMax is not reached by that curve at retryMax=4; it exists as a
// ceiling for two other cases — a caller who raises maxRetries via
// WithRetryPolicy, and a server-supplied Retry-After or x-ratelimit-reset,
// which would otherwise be honored verbatim and could stall a request far
// longer than any caller-supplied context deadline is likely to allow for.
const (
	retryWaitMin = 1 * time.Second
	retryWaitMax = 60 * time.Second
	retryMax     = 4
)

// isRetryableWriteStatus reports whether a response with the given status,
// from a request using method, is safe to retry automatically.
//
// 429 and 503 are refusals, not partial writes, so retrying is safe regardless
// of method. Most come from the gateway before the request reaches a service,
// but a service can send its own 429 too — the blueprints report does when its
// data store is overloaded, and pro and Security Cloud declare 429s on some
// POST and PATCH operations. Those are retried on every method as well, on the
// premise that a service refusing for load has not acted on the request
// (docs/WIRE-FACTS.md#rate-limiting). 501
// ("Not Implemented") is permanent and never retried. Any other 5xx (500,
// 502, 504, or a malformed/zero status) may mean the application partially
// processed the request before failing, so retrying is restricted to
// methods that are idempotent by definition (GET/DELETE/PUT/HEAD) — retrying
// a POST or PATCH risks double-applying a write the server actually
// completed before the client saw the failure (e.g. creating a duplicate
// object).
//
// PUT's idempotency assumption has one important carve-out: it only holds
// when the server has no side-channel precondition that advances as a side
// effect of a successful write. A handful of Jamf Pro V3 endpoints (computer
// and mobile-device prestage enrollment — see jamfplatform/pro's
// version_lock_helpers.go and its two callers, the only VersionLock users in
// the whole SDK) require an optimistic-lock field in the PUT body, sourced
// from a GET taken before the write. Confirmed live: such a PUT can commit
// successfully server-side (bumping the lock) and still return a 500 if the
// response serializer then crashes; blindly retrying it replays the
// now-stale lock and gets a genuine 409 OPTIMISTIC_LOCK_FAILED instead of the
// original 500 — turning a successful write into a reported failure.
//
// Given that's a small, enumerable set today rather than an inherent
// property of PUT in general, the default here stays retry-on for PUT, and
// the known exceptions opt OUT individually via DoWithContentTypeNoRetry
// (see its doc). Any future endpoint that gains a similar precondition
// (VersionLock, ETag, If-Match, or equivalent) must route through that
// no-retry entrypoint too — this function has no way to know from a bare
// method+status that a given PUT carries one.
//
// All other statuses (2xx, and 4xx other than 429) are left alone: a
// byte-identical retry won't change a client-request problem, and this
// function is never consulted for them in the first place since
// DefaultRetryPolicy already excludes them upstream of this check.
func isRetryableWriteStatus(method string, status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return true
	case http.StatusNotImplemented:
		return false
	}
	if status == 0 || status >= 500 {
		return isIdempotentMethod(method)
	}
	return false
}

// isIdempotentMethod reports whether method is one the transport treats as safe
// to send twice: GET, HEAD, PUT and DELETE. See isRetryableWriteStatus for the
// carve-out PUT needs and for why POST and PATCH are not here.
func isIdempotentMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodDelete, http.MethodPut, http.MethodHead:
		return true
	default:
		return false
	}
}

// jamfCheckRetry extends retryablehttp.DefaultRetryPolicy with the
// method-awareness it doesn't have: DefaultRetryPolicy alone would retry a
// 500 on a POST just as readily as one on a GET. See isRetryableWriteStatus
// for the policy.
//
// A transport error with no response needs the same care, and DefaultRetryPolicy
// does not give it. It retries any such error that is not a redirect, scheme,
// header or certificate failure, on every method, on the premise that nothing
// reached the server. That holds for a refused connection or a failed dial. It
// does not hold for a connection reset after the request was written, an EOF
// before the response, a GOAWAY, a failed h2 health-check ping, or a response
// header timeout: the server may have committed the write, and a retried POST
// creates a second object. So an error with no response is retried freely only
// if the request never finished being written, and after that only for an
// idempotent method and never for a timeout — see retryableTransportError.
//
// Deliberately does NOT extend this to endpoint-specific 4xx semantics
// (e.g. a classic API's misleading "400" on an accepted-async delete, or a
// transient "409 problem matching limitation user group" during a
// from-scratch apply). The transport has no way to tell a permanent 4xx
// from a transient one without knowing what the specific endpoint's status
// code means for that specific payload — that eventual-consistency
// judgement is a caller concern, not a transport one. This mirrors the
// reasoning that removed the SDK's old blanket 4xx retry.
func jamfCheckRetry(ctx context.Context, resp *http.Response, err error) (bool, error) {
	shouldRetry, checkErr := retryablehttp.DefaultRetryPolicy(ctx, resp, err)
	if !shouldRetry || checkErr != nil {
		return shouldRetry, checkErr
	}
	if resp == nil {
		return retryableTransportError(sendStateFrom(ctx), err), nil
	}
	return isRetryableWriteStatus(resp.Request.Method, resp.StatusCode), nil
}

// jamfBackoff is retryablehttp.DefaultBackoff with two additions: a 429 with
// no Retry-After waits for x-ratelimit-reset (see rateLimitResetWait), and the
// wait is always clamped to maxWait.
//
// The order of precedence is Retry-After, then x-ratelimit-reset, then the
// exponential curve. DefaultBackoff supplies the first and the last —
// exponential backoff of minWait*2^attemptNum capped at maxWait,
// overflow-guarded, plus Retry-After parsing (seconds and date form) for 429
// and 503. The clamp is needed because the Retry-After and reset paths both
// take the server's value uncapped, which would let a misconfigured or hostile
// header stall a request far past what maxWait promises callers. For the
// exponential path the clamp is a no-op, since DefaultBackoff already caps
// there.
//
// This deliberately does NOT use retryablehttp.RateLimitLinearJitterBackoff,
// which the SDK previously selected — presumably for its Retry-After
// handling, which DefaultBackoff also has. That function samples uniformly
// over the *entire* [minWait, maxWait] window and multiplies by attempt
// number, so under this SDK's 1s/60s window the very first retry waited ~30s
// on average and a four-retry sequence averaged over three minutes — all of
// it inside a single httpClient.Do emitting no log output (see
// logRetryAttempt), which is indistinguishable from a hang. DefaultBackoff
// bounds the same sequence at 1+2+4+8 = 15s.
//
// One property is given up in the swap: DefaultBackoff is deterministic, so
// concurrent requests that fail together wake together and collide again.
// The gateway's rate limit is the case where that matters, and the reset path
// adds its own jitter. The exponential curve stays deterministic, because it
// now only serves 5xx and a 429 that carries neither header.
func jamfBackoff(minWait, maxWait time.Duration, attemptNum int, resp *http.Response) time.Duration {
	if wait, ok := rateLimitResetWait(resp, maxWait); ok {
		return wait
	}
	return min(retryablehttp.DefaultBackoff(minWait, maxWait, attemptNum, resp), maxWait)
}

// rateLimitResetWait returns how long to wait before retrying a 429, read
// from the x-ratelimit-reset header, and false when that header does not
// apply.
//
// The gateway's 429 has no Retry-After. Its only timing signal is
// x-ratelimit-reset, the epoch second at which the window ends (see
// docs/WIRE-FACTS.md#rate-limiting). This is the same reactive handling the
// Okta and GitHub SDKs use for their X-Rate-Limit-Reset headers. Nothing here
// assumes the limit, the window length or the counter layout. Those are
// gateway configuration that can change at any time, and the header carries
// whatever they currently are.
//
// Four details, each deliberate:
//
//   - The wait is measured against the response's Date header, not the local
//     clock. Both values come from the same server, so the client's clock
//     skew cancels out. The local clock is the fallback when Date is absent.
//   - One second is added, because both values are whole seconds. On the 429
//     observed, reset was the same second as Date, so the window ends some
//     time in the second after reset. Without the second, a retry can land
//     inside the window that refused it and use up a retry for nothing.
//   - Up to one more second of random jitter is added. Requests that were
//     refused together would otherwise all retry at the same instant at the
//     start of the next window.
//   - x-ratelimit-remaining is used here only to tell whose 429 this is.
//     Holding requests back before the limit, and holding every other
//     goroutine after a refusal, is requestThrottle's job; this decides only
//     when the refused request itself is retried, and the retry then passes
//     through that same gate.
//
// The reset applies only when remaining is 0, which is the gateway's own
// refusal. A service behind the gateway can answer 429 too — the blueprints
// report does when its data store is overloaded — and the gateway stamps its
// x-ratelimit-* headers on that response as well, with remaining above 0 and
// a reset taken when the request started. Waiting for that reset would retry
// an overloaded service every second or so with no backoff, so such a 429
// falls back to the exponential curve. So does a reset that cannot be parsed
// or is not positive.
//
// The wait is clamped to maxWait here rather than by the caller, and
// parseRateLimitHeaders counts the seconds as integers, so a reset sent in
// epoch milliseconds cannot wrap the wait negative, which retryablehttp would
// treat as no wait at all.
func rateLimitResetWait(resp *http.Response, maxWait time.Duration) (time.Duration, bool) {
	if resp == nil || resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "" {
		return 0, false
	}
	remaining, untilReset, ok := parseRateLimitHeaders(resp.Header, maxWait)
	if !ok || remaining != 0 {
		return 0, false
	}
	wait := untilReset + time.Second + rand.N(time.Second)
	return min(wait, maxWait), true
}

// parseRateLimitHeaders reads the gateway's x-ratelimit-remaining and
// x-ratelimit-reset from h, and returns the remaining count and how long until
// the reset, measured against h's Date (the local clock when Date is absent)
// and clamped to [0, maxWait]. It reports false when either header is absent
// or unusable: a remaining that is not a non-negative integer, or a reset
// that is not a positive one.
//
// The seconds until reset are counted as integers before they become a
// Duration. A reset centuries away, such as one sent in epoch milliseconds,
// saturates time.Time.Sub at the largest Duration, and adding to that wraps
// it negative. Shared by rateLimitResetWait and requestThrottle.observe, so
// the retry wait and the pacing read the headers the same way.
func parseRateLimitHeaders(h http.Header, maxWait time.Duration) (int64, time.Duration, bool) {
	remaining, err := strconv.ParseInt(h.Get("X-Ratelimit-Remaining"), 10, 64)
	if err != nil || remaining < 0 {
		return 0, 0, false
	}
	reset, err := strconv.ParseInt(h.Get("X-Ratelimit-Reset"), 10, 64)
	if err != nil || reset <= 0 {
		return 0, 0, false
	}
	now, err := http.ParseTime(h.Get("Date"))
	if err != nil {
		now = time.Now()
	}
	secs := min(max(reset-now.Unix(), 0), int64(maxWait/time.Second))
	return remaining, time.Duration(secs) * time.Second, true
}

// logRetryAttempt is the RequestLogHook: retryablehttp calls it immediately
// before every attempt, with attemptNum 0 for the first.
//
// It exists because the retry loop is otherwise invisible from outside the
// transport. execute() logs one request, hands the whole loop to a single
// httpClient.Do, and handleResponse logs only the response that survives it
// — so a caller watching the log sees exactly one request and one response
// no matter how many attempts ran or how long the backoff waited. A
// four-retry sequence and a single slow call produce identical output, which
// is what made a multi-minute retry sequence read as a hang.
//
// Attempt 0 is skipped because execute() has already logged it; without that
// guard every request would log twice. The body is deliberately nil rather
// than a replay of the original: the bytes are identical across attempts, so
// re-dumping them multiplies a large request body through the log for no
// diagnostic gain.
//
// The retryablehttp.Logger argument is always nil here — newRetryClient sets
// rc.Logger = nil, and retryablehttp's hook dispatch passes nil through its
// default branch rather than skipping the hook. The SDK's own Logger is read
// from the Transport at call time, so a logger installed after construction
// (SetLogger, WithLogger) is picked up.
func (c *Transport) logRetryAttempt(_ retryablehttp.Logger, req *http.Request, attemptNum int) {
	if c.logger == nil || attemptNum == 0 {
		return
	}
	c.logger.LogRequest(req.Context(), req.Method, req.URL.String(), nil)
}

// logRetriedResponse is the ResponseLogHook: retryablehttp calls it after
// every attempt that produced a response, before it decides whether to wait
// and try again.
//
// It is the other half of logRetryAttempt. Logging the retried *requests*
// alone leaves the log reading "request, request, 404" — the status that
// actually caused each retry never appears, because handleResponse only ever
// sees the response that survives the loop. That cost is documented in this
// SDK: requirePackageStore's doc in jamfplatform/acc_helpers_test.go records
// four package tests failing on a 404 whose real cause was a 500 two hops
// earlier, which took a wire probe to establish. With this hook the 500 is in
// the log next to the 404 that replaced it.
//
// Gating is the whole design problem: the hook fires for every attempt's
// response, including the last one, and handleResponse already logs that one.
// Logging unconditionally would double-log every single request in the SDK.
// So the same predicate that drives the retry decides whether to log —
// jamfCheckRetry (and through it isRetryableWriteStatus), consulted with a nil
// error exactly as retryablehttp consults it — which means the statuses logged
// here can never drift from the statuses actually retried. err is nil rather
// than resp's own error because retryablehttp does not call this hook at all
// when the attempt failed at the transport layer (resp would be nil), so a
// response reaching here always came back from the server.
//
// One duplicate survives that gate deliberately: when retries are exhausted,
// the final attempt is retryable by policy but not retried, because
// retryablehttp checks the remaining budget after this hook and passes no
// attempt number here to reconstruct it. That response is logged twice — once
// as retryable, once by handleResponse as the surviving response. One extra
// line at the end of an exhausted sequence is a better trade than either
// tracking per-request attempt state in a Transport shared by concurrent
// callers, or dropping the last (and most interesting) failure from the log.
//
// Headers and body are both nil, which is the same discipline as
// logRetryAttempt's nil body. The body especially must not be read here:
// retryablehttp drains it to reuse the connection when it retries, and on the
// not-retried path it is the body handleResponse decodes for the caller —
// consuming it would break decoding. A nil http.Header is safe for a consumer
// logger, since Get and range are both nil-safe. The status is the only thing
// a retry sequence adds: the method and URL are already on the LogRequest line
// this response answers, and the Logger interface's LogResponse carries no
// place for them anyway.
//
// As in logRetryAttempt, the retryablehttp.Logger argument is always nil
// (newRetryClient sets rc.Logger = nil, and retryablehttp's hook dispatch
// passes nil through its default branch), and the SDK's own Logger is read
// from the Transport at call time so a logger installed later via SetLogger or
// WithLogger is picked up. Installed next to logRetryAttempt in NewTransport
// rather than in newRetryClient, for the same reason: it is a method on the
// Transport, which does not exist when newRetryClient runs.
func (c *Transport) logRetriedResponse(_ retryablehttp.Logger, resp *http.Response) {
	if c.logger == nil || resp == nil {
		return
	}
	ctx := resp.Request.Context()
	if shouldRetry, err := jamfCheckRetry(ctx, resp, nil); err != nil || !shouldRetry {
		return
	}
	c.logger.LogResponse(ctx, resp.StatusCode, nil, nil)
}

// newRetryClient builds a retryablehttp.Client around authed — the
// oauth2+throttle-wrapped client also used directly (unwrapped) for
// multipart uploads, see multipart.go. authed is deliberately the
// *HTTPClient* field, not the retry client's own base transport: every
// retry attempt re-enters authed, so token refresh and request pacing both
// apply per attempt, not just to the first one.
//
// ErrorHandler is set to PassthroughErrorHandler, which is load-bearing and
// not a style choice: retryablehttp's default behavior on retry exhaustion
// is to drain and discard the final response body and return a synthetic
// "giving up after N attempts" error instead. That would break every caller
// that inspects the concrete *APIResponseError (helpers.IsNotFoundError,
// IsServerError, AsAPIError, .TraceID, .Errors, …) once retries run out on a
// persistent failure. PassthroughErrorHandler returns the underlying
// (resp, err) pair untouched — for a plain non-2xx response with no
// transport-level error, err is nil, so this call site's caller sees a
// final response exactly as if it had never been retried, and the existing
// handleResponse decoding path is unaffected.
func newRetryClient(authed *http.Client) *retryablehttp.Client {
	rc := retryablehttp.NewClient()
	rc.HTTPClient = authed
	rc.Logger = nil // the SDK has its own Logger interface (LogRequest/LogResponse); avoid a second, unrelated stderr logger.
	rc.CheckRetry = jamfCheckRetry
	rc.Backoff = jamfBackoff
	rc.RetryWaitMin = retryWaitMin
	rc.RetryWaitMax = retryWaitMax
	rc.RetryMax = retryMax
	rc.ErrorHandler = retryablehttp.PassthroughErrorHandler
	return rc
}
