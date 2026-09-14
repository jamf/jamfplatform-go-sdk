// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

// Package client provides the HTTP transport layer for the Jamf Platform API.
//
// This package handles authentication, request/response processing, error handling,
// logging, and pagination. It does not contain any resource-specific types or methods;
// those belong in the jamfplatform package.
//
// https://developer.jamf.com/platform-api/docs/getting-started-with-the-platform-api

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
)

// isClassicPath reports whether an endpoint targets Jamf's Classic XML API
// via the platform gateway. The path prefix sniff is the single source of
// truth for codec selection: Classic paths marshal/unmarshal XML, everything
// else uses JSON.
func isClassicPath(path string) bool {
	return strings.Contains(path, "/proclassic/")
}

// Logger is an interface for logging HTTP requests and responses.
type Logger interface {
	LogRequest(ctx context.Context, method, url string, body []byte)
	LogResponse(ctx context.Context, statusCode int, headers http.Header, body []byte)
}

// Transport represents the HTTP transport layer for the Jamf Platform API.
// Sub-packages in jamfplatform/ construct service clients that wrap a Transport.
type Transport struct {
	baseURL         string
	scopeKind       ScopeKind    // which request-context header carries the scope
	scopeID         string       // the tenant (or environment) ID that header sends
	httpClient      *http.Client // retry.StandardClient() — used by execute() for all JSON/XML Do* calls
	uploadClient    *http.Client // authed + paced, NOT retry-wrapped — used directly by multipart.go; see retry.go's newRetryClient doc
	baseClient      *http.Client
	oauthConfig     *clientcredentials.Config
	logger          Logger
	userAgent       string
	tokenCache      TokenCache
	cacheKey        string
	cookieJar       http.CookieJar
	deprecationSeen sync.Map // dedup runtime Deprecation header warnings
	throttle        *requestThrottle
	extraHeaders    http.Header           // caller-supplied headers, applied to token exchange and API calls alike
	authHeaderName  string                // when set, the OAuth2 bearer moves from Authorization to this header
	retry           *retryablehttp.Client // backs httpClient; mutating retry.HTTPClient (see SetHTTPClient/SetUserAgent) updates httpClient's behavior in place
}

// PaginatedResponseRepresentation captures pagination metadata shared by multiple endpoints.
type PaginatedResponseRepresentation struct {
	Page        int   `json:"page"`
	PageSize    int   `json:"pageSize"`
	TotalCount  int64 `json:"totalCount"`
	TotalPages  int   `json:"totalPages"`
	HasNext     bool  `json:"hasNext"`
	HasPrevious bool  `json:"hasPrevious"`
}

// Option configures a Client.
type Option func(*Transport)

// WithHTTPClient overrides the HTTP client used by the API client.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Transport) {
		if httpClient != nil {
			if httpClient.Jar == nil {
				httpClient.Jar = newCookieJar()
			}
			c.baseClient = httpClient
			c.uploadClient = wrapWithOAuth2(c.oauthConfig, httpClient, c.throttle)
			c.retry.HTTPClient = c.uploadClient
		}
	}
}

// WithHeaders sets additional HTTP headers sent on every request this client
// makes, including the OAuth2 token exchange. Existing values for the same
// header name are replaced.
//
// Intended for callers whose traffic is fronted by a reverse proxy that requires
// headers of its own — a vaulted service-account credential, a routing tag —
// which the SDK cannot know about. Prefer this over WithHTTPClient for the
// purpose: supplying a client replaces the SDK's tuned *http.Transport and
// silently drops proxy-from-environment support, the per-phase timeouts, the
// connection-pool ceiling matched to Terraform's default parallelism, and the
// large write buffer that package upload depends on. This option layers onto
// that transport instead of replacing it, and composes with WithHTTPClient when
// a caller genuinely needs both.
//
// The scope headers (X-Tenant-Id, X-Environment-Id) are rejected: the gateway
// resolves the request context from them, and an override is refused with the
// same 403 OWNERSHIP_FORBIDDEN a mismatched credential gives. Set the scope with
// WithTenantID or WithEnvironmentID. Rejections are logged rather than silently
// dropped.
//
// Values replace rather than merge, which is worth knowing for one header:
// passing Cookie here replaces the sticky-session cookie Jamf Cloud uses to pin a
// client to a single app node, so a write may not be visible on the next read. It
// is allowed anyway, because a proxy may require a cookie of its own.
//
// Supplying an Authorization header here moves the OAuth2 client credential from
// the Authorization header into the request body on the token exchange (RFC 6749
// §2.3.1 permits both forms). Without that, the caller's header would overwrite
// the client credential and every token fetch would fail — or, under x/oauth2's
// auto-detection, succeed only after a wasted 401 round trip per fetch. This is
// what makes "proxy takes Authorization, Jamf credential rides in the body"
// work, which is the arrangement such proxies expect.
func WithHeaders(h http.Header) Option {
	return func(c *Transport) {
		if len(h) == 0 {
			return
		}
		if c.extraHeaders == nil {
			c.extraHeaders = make(http.Header, len(h))
		}
		for name, values := range h {
			canonical := http.CanonicalHeaderKey(name)
			if reservedHeaders[canonical] {
				log.Printf("jamfplatform: WithHeaders: refusing to set %s — the SDK owns the scope headers; use WithTenantID or WithEnvironmentID", canonical)
				continue
			}
			c.extraHeaders[canonical] = slices.Clone(values)
		}
	}
}

// WithAuthorizationHeaderName moves the OAuth2 bearer credential out of the
// Authorization header and into the named header on every API request.
//
// For callers behind a reverse proxy that consumes Authorization for its own
// credential and expects Jamf's bearer elsewhere. The relocation runs before the
// WithHeaders values are applied, so pairing the two — Authorization supplied by
// WithHeaders, the bearer relocated by this option — sends both credentials on
// the same request.
//
// Only a Bearer credential is moved, so the token exchange is unaffected:
// x/oauth2 writes the client credential there as Basic, and relocating it would
// leave the token endpoint with nothing to read while appearing to have worked.
//
// Two names are refused, both because accepting them breaks the request in a way
// no error message points at. "Authorization" is the header the bearer is already
// in, and relocating a header onto itself deletes it — RoundTrip sets the target
// and then deletes the source, which for one name are the same key, so every API
// call would go out with no credential at all and answer 401 exactly as a wrong
// client secret does. The scope headers are refused for the reason WithHeaders
// refuses them, reached the other way round: the bearer would overwrite the
// scope setScopeHeader stamped, and the gateway answers 403 OWNERSHIP_FORBIDDEN.
// Both rejections are logged.
func WithAuthorizationHeaderName(name string) Option {
	return func(c *Transport) {
		if name == "" {
			return
		}
		canonical := http.CanonicalHeaderKey(name)
		switch {
		case canonical == "Authorization":
			log.Printf("jamfplatform: WithAuthorizationHeaderName: ignoring %q — the bearer is already in Authorization, and relocating it onto itself would delete it; omit the option to leave it there", name)
		case reservedHeaders[canonical]:
			log.Printf("jamfplatform: WithAuthorizationHeaderName: refusing to relocate the bearer into %s — the SDK owns the scope headers, and overwriting one is refused with 403 OWNERSHIP_FORBIDDEN", canonical)
		default:
			c.authHeaderName = canonical
		}
	}
}

// WithTokenCache sets a persistent token cache and its lookup key.
func WithTokenCache(cache TokenCache, cacheKey string) Option {
	return func(c *Transport) {
		if cache != nil && cacheKey != "" {
			c.tokenCache = cache
			c.cacheKey = cacheKey
		}
	}
}

// WithTenantID sets the tenant this client is scoped to. It is sent as the
// X-Tenant-Id request header on every API call; see ScopeHeader.
//
// Tenant is the legacy scope; prefer WithEnvironmentID for new integrations.
func WithTenantID(id string) Option {
	return func(c *Transport) {
		c.scopeKind = ScopeTenant
		c.scopeID = id
	}
}

// WithEnvironmentID sets the platform environment this client is scoped to. It
// is sent as the X-Environment-Id request header on every API call.
//
// An environment groups a customer's tenants. A credential is minted against
// one scope or the other, and the header must match the credential: an
// environment-scoped integration sending X-Tenant-Id — or a tenant-scoped one
// sending X-Environment-Id — is refused with 403 OWNERSHIP_FORBIDDEN, even when
// both IDs belong to the same customer (wire-verified against securitycloud in
// prod, 2026-08-25). So this is not an alternative spelling of WithTenantID;
// pick the one your integration was created for.
func WithEnvironmentID(id string) Option {
	return func(c *Transport) {
		c.scopeKind = ScopeEnvironment
		c.scopeID = id
	}
}

// WithCookieJar overrides the default in-memory cookie jar. Typically used to
// install a persistent jar (e.g. FileCookieJar) so sticky-session cookies
// survive across process invocations.
func WithCookieJar(jar http.CookieJar) Option {
	return func(c *Transport) {
		c.cookieJar = jar
	}
}

// WithMinRequestInterval sets a client-level minimum elapsed wall-clock time
// between the start of consecutive outbound HTTP requests. It paces all traffic
// through the shared transport (which Terraform fans out across parallel
// goroutines), giving the server breathing room and reducing 429s. A value
// <= 0 disables the gate. The default is 100ms.
func WithMinRequestInterval(d time.Duration) Option {
	return func(c *Transport) {
		c.throttle.setInterval(d)
	}
}

// WithRetryPolicy overrides the transport's automatic-retry timing for
// transient failures (see retry.go's isRetryableWriteStatus for what gets
// retried, and jamfBackoff for the curve). waitMin seeds an exponential
// backoff and waitMax caps it; maxRetries follows retryablehttp's own
// semantics, so total attempts = maxRetries+1 and 0 disables retrying
// entirely.
//
// Two distinct uses, both legitimate:
//
//   - A test harness mocking a persistently-failing transient status (e.g.
//     an always-500 GET) wants the loop to run in milliseconds rather than
//     the production window. Pass a few milliseconds and a low maxRetries.
//   - An interactive caller (a CLI) wants a bound far tighter than the
//     production default, so a transient failure surfaces promptly instead
//     of appearing to hang.
//
// An overall time bound is better expressed as a context deadline, which
// bounds the whole call including every retry and needs no policy change.
func WithRetryPolicy(waitMin, waitMax time.Duration, maxRetries int) Option {
	return func(c *Transport) {
		c.retry.RetryWaitMin = waitMin
		c.retry.RetryWaitMax = waitMax
		c.retry.RetryMax = maxRetries
	}
}

// NewTransport creates a new Jamf Platform API transport.
func NewTransport(baseURL, clientID, clientSecret string) *Transport {
	return NewTransportWithUserAgent(baseURL, clientID, clientSecret, "jamfplatform-go-sdk/dev")
}

// NewTransportWithUserAgent creates a new Jamf Platform API transport with a custom user agent string.
func NewTransportWithUserAgent(baseURL, clientID, clientSecret, userAgent string, opts ...Option) *Transport {
	oauthConfig := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     baseURL + "/auth/token",
	}

	throttle := newRequestThrottle(defaultMinRequestInterval)
	uploadClient, baseClient := newOAuth2Client(oauthConfig, userAgent, throttle)
	retry := newRetryClient(uploadClient)

	c := &Transport{
		baseURL:      baseURL,
		uploadClient: uploadClient,
		httpClient:   retry.StandardClient(),
		baseClient:   baseClient,
		oauthConfig:  oauthConfig,
		userAgent:    userAgent,
		throttle:     throttle,
		retry:        retry,
	}
	// Installed here rather than in newRetryClient because both hooks are
	// methods on c, which does not exist yet at that point. Each reads
	// c.logger at call time, so they work regardless of when a logger is
	// installed.
	retry.RequestLogHook = c.logRetryAttempt
	retry.ResponseLogHook = c.logRetriedResponse
	for _, opt := range opts {
		opt(c)
	}
	c.installHeaderTransport()
	if c.tokenCache != nil {
		c.uploadClient = newCachingOAuth2Client(c.oauthConfig, c.baseClient, c.tokenCache, c.cacheKey)
		c.retry.HTTPClient = c.uploadClient
	}
	if c.cookieJar != nil {
		c.uploadClient.Jar = c.cookieJar
		c.baseClient.Jar = c.cookieJar
	}
	return c
}

// BaseURL returns the base URL configured for the client.
func (c *Transport) BaseURL() string {
	return c.baseURL
}

// ScopeKind identifies which kind of Jamf scope a client is bound to. The
// gateway calls this the request context, and each kind travels in its own
// request header.
type ScopeKind int

// Every value is written out rather than run off iota, and that is not a style
// preference. iota counts the ConstSpec's position in the block, not the
// constants declared before it, so naming the zero value first and then opening
// an `iota + 1` run underneath it numbered ScopeTenant 2 and ScopeEnvironment 3
// and left 1 unreachable — the three printed `0 2 3`. Nothing observable broke,
// because every comparison in the SDK is symbolic and a ScopeKind is never
// serialised or persisted, but the block read as though ScopeTenant were 1,
// these constants are exported, and the next named zero value someone inserts
// at the top would have renumbered them again just as silently.
// TestScopeKindValues pins all three.
const (
	// ScopeOrganization scopes requests to a Jamf Account organization. It is
	// the zero value, and it sends no header at all: the gateway resolves the
	// organization from the access token
	// (request-context-allowed-sources is `token` for the account
	// api-products, in every environment).
	//
	// The constant exists so the generated Privileges registry can *name* this
	// scope rather than leaving an empty slice, which would be
	// indistinguishable from "the spec declared nothing". Client code has no
	// use for it: an unset scope already means organization, so there is no
	// WithOrganizationID option and never will be.
	ScopeOrganization ScopeKind = 0
	// ScopeTenant scopes requests to a single product tenant, sent as
	// X-Tenant-Id. This is the legacy scope: every spec still declares this
	// header, but Jamf intends new integrations to be environment-scoped.
	ScopeTenant ScopeKind = 1
	// ScopeEnvironment scopes requests to a platform environment — a grouping
	// of tenants — sent as X-Environment-Id, set by WithEnvironmentID. This is
	// the scope to prefer, and as of GitOps v2082 the specs declare it: the
	// six Platform APIs declare it as their only scope, and jpapi, capi and
	// the Security Cloud specs declare it alongside tenant. Wire-verified
	// against blueprints, compliance-benchmarks, pro, proclassic, devices and
	// securitycloud.
	ScopeEnvironment ScopeKind = 2
)

// ScopeHeader returns the request header that carries this scope kind, or ""
// when the kind is unset.
//
// Organization scope deliberately has no entry: the gateway resolves it from
// the access token alone (request-context-allowed-sources is `token` for the
// account api-products, in every environment), so there is no header to send.
func (k ScopeKind) ScopeHeader() string {
	switch k {
	case ScopeTenant:
		return "X-Tenant-Id"
	case ScopeEnvironment:
		return "X-Environment-Id"
	default:
		return ""
	}
}

// String names the scope kind, for logs and diagnostics.
//
// The zero value reports "organization", not "none". There is no unset scope in
// this model: absence of a scope header *is* organization scope, because the
// gateway resolves the organization from the access token, so a client that
// sends no header is organization-scoped whether or not its author meant it to
// be. Reporting "none" would name a fourth state that does not exist, and a
// caller reading it could not tell that the client was in fact addressing an
// organization-scoped API correctly.
//
// The consequence worth stating: a client whose scope options never took effect
// also logs "organization", so this string cannot diagnose a missing
// WithEnvironmentID or WithTenantID. What diagnoses that is the gateway, which
// answers 400 REQUEST_CONTEXT_NOT_PROVIDED when a scoped API is called with no
// scope header. Read the refusal, not the log line.
//
// A value outside the three kinds reports "unknown" rather than falling back to
// a real scope name — that is a programming error, not a scope, and naming it
// as one would be the same conflation this method exists to avoid.
func (k ScopeKind) String() string {
	switch k {
	case ScopeOrganization:
		return "organization"
	case ScopeTenant:
		return "tenant"
	case ScopeEnvironment:
		return "environment"
	default:
		return "unknown"
	}
}

// setScopeHeader stamps the scope header on a request. It is applied before
// extraHeaders so an operation that needs to override the scope for one call
// still can, and it is a no-op when no scope was configured — the gateway then
// resolves the context from the access token, which is how organization-scoped
// products work.
func (c *Transport) setScopeHeader(req *http.Request) {
	if h := c.scopeKind.ScopeHeader(); h != "" && c.scopeID != "" {
		req.Header.Set(h, c.scopeID)
	}
}

// TenantID returns the tenant ID configured on the transport, or "" when this
// client is scoped to something other than a tenant.
//
// This is a partial view of a three-valued property and cannot distinguish an
// environment-scoped client from an unscoped, organization-style one — both
// report "". Prefer Scope, which reports the kind alongside the ID.
func (c *Transport) TenantID() string {
	if c.scopeKind != ScopeTenant {
		return ""
	}
	return c.scopeID
}

// Scope reports which kind of scope this client carries and the ID it sends.
//
// ScopeOrganization with an empty ID means no scope header is sent at all,
// which is how organization-scoped credentials work: the gateway resolves the
// context from the access token, so there is nothing for the client to state.
// It is the zero value, so it is also what a client whose scope options never
// took effect reports — the two are the same state on the wire and this method
// cannot separate them. Callers switching on the kind must handle
// ScopeOrganization rather than assuming a header-bearing scope is always
// present.
func (c *Transport) Scope() (ScopeKind, string) {
	if c.scopeKind.ScopeHeader() == "" || c.scopeID == "" {
		return ScopeOrganization, ""
	}
	return c.scopeKind, c.scopeID
}

// APIPrefix returns the /{namespace}/{version} URL prefix for a namespace.
// An empty version collapses that segment, for the APIs that carry no version
// in the URL (proclassic, Pro preview paths).
//
// There is NO /api segment. The GA gateway at {region}.api.jamfcloud.com
// mounts each namespace at the root, and answers 404 "page not found" — the
// unknown-namespace tell — for anything under /api. The retired
// {region}.apigw.jamf.com gateway required that segment; it is gone at GA
// (2026-09-01), so callers must set a base URL of https://{region}.api.jamfcloud.com.
// Wire-verified 2026-08-28 against EU with both a tenant- and an
// environment-scoped credential: every namespace this SDK generates returns
// byte-identical statuses on the new host once the segment is dropped, and
// tokens minted at either host work on both.
//
// Dropped outright rather than selected per host, the same call as the scope
// migration below: a second code path nothing exercises is how an earlier
// URL-shape bug went unnoticed for weeks.
//
// The scope is NOT in the path either. Until 2026-08-25 every Jamf URL
// embedded it — /api/{namespace}/{version}/tenant/{tenantID} — and the
// gateway's Tyk config resolved the request context from `path`. `header` was
// added as an allowed source in prod on that date by a gateway
// API-definition change enabling header context support, and the
// published specs dropped the path segment in GitOps build v1495 in favour of
// a required X-Tenant-Id header.
func (c *Transport) APIPrefix(namespace, version string) string {
	if version == "" {
		return "/" + namespace
	}
	return "/" + namespace + "/" + version
}

// ValidateCredentials tests authentication by requesting an OAuth token.
func (c *Transport) ValidateCredentials(ctx context.Context) error {
	return validateCredentials(ctx, c.oauthConfig, c.baseClient)
}

// HTTPClient returns the underlying OAuth2-managed, retry-wrapped HTTP client for raw authenticated requests.
func (c *Transport) HTTPClient() *http.Client {
	return c.httpClient
}

// SetHTTPClient sets a custom base HTTP client (useful for testing).
//
// installHeaderTransport runs afterwards for the same reason it does in
// SetUserAgent: this replaces baseClient outright, so any headerTransport
// layered onto the previous one is gone and the caller's headers and bearer
// relocation would silently stop being applied.
func (c *Transport) SetHTTPClient(httpClient *http.Client) {
	c.baseClient = httpClient
	c.uploadClient = wrapWithOAuth2(c.oauthConfig, httpClient, c.throttle)
	c.retry.HTTPClient = c.uploadClient
	c.installHeaderTransport()
}

// SetLogger sets the logger for the client.
func (c *Transport) SetLogger(logger Logger) {
	c.logger = logger
}

// SetUserAgent sets the User-Agent header value used for token and API requests.
func (c *Transport) SetUserAgent(ua string) {
	c.userAgent = ua
	c.uploadClient, c.baseClient = newOAuth2Client(c.oauthConfig, ua, c.throttle)
	c.retry.HTTPClient = c.uploadClient
	c.installHeaderTransport()
}

// installHeaderTransport layers the caller's headers onto the base client's
// transport and rebuilds the OAuth2 wrapper on top of the result.
//
// The rebuild is required, not defensive: oauth2.NewClient captures the base
// transport by value when it constructs oauth2.Transport, so mutating
// baseClient.Transport afterwards would leave the already-built API client
// pointing at the unwrapped chain and the headers would reach the token exchange
// only. Wrapping the base rather than the OAuth2 client is what puts this
// transport below oauth2 — see headerTransport for why that ordering matters.
//
// No throttle is passed to wrapWithOAuth2: the base chain already carries the
// throttle wrapper from newOAuth2Client (or from the WithHTTPClient path), and
// adding a second would double every request's pacing interval.
func (c *Transport) installHeaderTransport() {
	if len(c.extraHeaders) == 0 && c.authHeaderName == "" {
		return
	}
	if len(c.extraHeaders.Values("Authorization")) > 0 {
		c.oauthConfig.AuthStyle = oauth2.AuthStyleInParams
	}
	base := c.baseClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.baseClient.Transport = &headerTransport{
		base:       base,
		headers:    c.extraHeaders,
		authHeader: c.authHeaderName,
	}
	c.uploadClient = wrapWithOAuth2(c.oauthConfig, c.baseClient, nil)
	c.retry.HTTPClient = c.uploadClient
}

// Do performs an authenticated API request and decodes the response.
// It expects HTTP 200 OK as the success status.
func (c *Transport) Do(ctx context.Context, method, path string, body, result any) error {
	return c.DoExpect(ctx, method, path, body, http.StatusOK, result)
}

// DoExpect performs an authenticated API request expecting the given HTTP status.
func (c *Transport) DoExpect(ctx context.Context, method, path string, body any, expectedStatus int, result any) error {
	return c.execute(ctx, method, path, body, "", nil, expectedStatus, result, c.httpClient)
}

// DoWithContentType performs an authenticated API request with a custom Content-Type header.
// It expects HTTP 200 OK as the success status.
func (c *Transport) DoWithContentType(ctx context.Context, method, path string, body any, contentType string, expectedStatus int, result any) error {
	return c.execute(ctx, method, path, body, contentType, nil, expectedStatus, result, c.httpClient)
}

// DoWithContentTypeNoRetry is DoWithContentType without the transport's
// automatic 5xx retry (see isRetryableWriteStatus). It exists for the small,
// enumerable set of PUT/PATCH endpoints that carry a side-channel
// precondition — an optimistic-lock field sourced from a GET taken before
// the write — where a successful-but-500ing write, if blindly retried,
// replays a now-stale precondition and turns into a genuine conflict the
// caller's own 500-specific compensation never expects. Route through this
// method instead of DoWithContentType for any such endpoint; see
// isRetryableWriteStatus's doc for the mechanism and the current callers
// (computer/mobile-device prestage enrollment). 429/503 retry still applies
// — those are gateway-level rejections that never reached the precondition
// check in the first place, so they carry none of this risk.
//
// This is a workaround for an upstream response-serializer bug, not a
// permanent architectural split: once Jamf fixes it, this opt-out and its
// callers' own GET-diff compensation (e.g. isPutSerializerBug in
// terraform-provider-jamfplatform) should both be removed together.
func (c *Transport) DoWithContentTypeNoRetry(ctx context.Context, method, path string, body any, contentType string, expectedStatus int, result any) error {
	return c.execute(ctx, method, path, body, contentType, nil, expectedStatus, result, c.uploadClient)
}

// RequestOptions carries the per-request dimensions of a call. It exists
// because those dimensions are independent — expected status, Content-Type,
// extra headers, and whether the write may be retried — so naming a wrapper
// per combination doubles the surface every time one is added. The Do* methods
// above are the shorthands for the combinations the generated code reaches
// most; DoWithOptions is the general form and the only one that can carry
// headers.
//
// Headers are stamped after the scope header, so a caller could in principle
// overwrite X-Tenant-Id / X-Environment-Id with a wrong value and get an
// undiagnosable 403 OWNERSHIP_FORBIDDEN. That is why tools/generate refuses to
// emit a scope header as a method argument (generatorReservedHeaders) — the
// restriction lives at the generator rather than here so a legitimate
// reverse-proxy caller reaching Client.Transport() is not blocked.
type RequestOptions struct {
	// ExpectedStatus is the success status; zero means 200, which keeps a
	// plain read's options literal empty rather than restating the default.
	ExpectedStatus int
	// ContentType overrides the Content-Type the codec would pick. Only
	// consulted when the request carries a body.
	ContentType string
	// Headers are set on the request, replacing rather than appending.
	Headers http.Header
	// NoRetry opts out of the automatic 5xx retry — see
	// DoWithContentTypeNoRetry for the case it exists for.
	NoRetry bool
}

// DoWithOptions performs an authenticated API request with any combination of
// the per-request options. It is the general form of the Do* family.
func (c *Transport) DoWithOptions(ctx context.Context, method, path string, body any, opts RequestOptions, result any) error {
	expected := opts.ExpectedStatus
	if expected == 0 {
		expected = http.StatusOK
	}
	httpClient := c.httpClient
	if opts.NoRetry {
		httpClient = c.uploadClient
	}
	return c.execute(ctx, method, path, body, opts.ContentType, opts.Headers, expected, result, httpClient)
}

// execute funnels every Do* variant through one place so Deprecation-header
// logging lives in a single hook point. client selects whether transient
// failures (429, 503, and 500/502/504 on an idempotent method — see
// isRetryableWriteStatus) are retried one layer down: c.httpClient retries,
// c.uploadClient doesn't — see DoWithContentTypeNoRetry. Non-retried
// statuses surface immediately as an *APIResponseError —
// eventual-consistency handling of a specific endpoint's error semantics is
// a caller concern.
func (c *Transport) execute(ctx context.Context, method, path string, body any, contentType string, extraHeaders http.Header, expectedStatus int, result any, client *http.Client) error {
	resp, classic, err := c.doRequestFull(ctx, method, path, body, contentType, extraHeaders, client)
	if err != nil {
		return err
	}
	return c.handleResponse(ctx, resp, classic, expectedStatus, result)
}

// logDeprecation logs once per (method, path) when a server response includes
// a Deprecation header, so consumers see the notice even without a custom
// Logger installed. Runtime signal in addition to spec-level // Deprecated:
// godoc, which only catches endpoints marked in the spec at SDK build time.
func (c *Transport) logDeprecation(resp *http.Response) {
	if resp == nil || resp.Request == nil {
		return
	}
	v := resp.Header.Get("Deprecation")
	if v == "" {
		return
	}
	key := resp.Request.Method + " " + resp.Request.URL.Path
	if _, seen := c.deprecationSeen.LoadOrStore(key, struct{}{}); seen {
		return
	}
	log.Printf("jamfplatform: endpoint %s returned Deprecation header: %s — migrate callers", key, v)
}

// buildURL constructs the full API URL from a relative endpoint.
func (c *Transport) buildURL(endpoint string) string {
	if len(endpoint) > 0 && endpoint[0] == '/' {
		return c.baseURL + endpoint
	}
	return c.baseURL + "/" + endpoint
}

// doRequestFull performs an authenticated API request with optional content
// type and extra headers, dispatching through client (c.httpClient or
// c.uploadClient — see execute). Returns the response, the classic-codec
// flag captured from the request URL (used by the caller to route XML vs
// JSON unmarshal — avoids re-sniffing the response URL, which may have
// mutated through redirects), and any transport error.
func (c *Transport) doRequestFull(ctx context.Context, method, endpoint string, body any, contentType string, extraHeaders http.Header, client *http.Client) (*http.Response, bool, error) {
	var requestBodyBytes []byte

	fullURL := c.buildURL(endpoint)

	if err := checkDeniedPath(method, fullURL); err != nil {
		return nil, false, err
	}

	classic := isClassicPath(fullURL)

	if body != nil {
		// Raw byte bodies (e.g. Classic XML assembled by the caller) bypass
		// codec selection — send verbatim.
		if b, ok := body.([]byte); ok {
			requestBodyBytes = b
		} else {
			var err error
			if classic {
				requestBodyBytes, err = xml.Marshal(body)
			} else {
				requestBodyBytes, err = json.Marshal(body)
			}
			if err != nil {
				return nil, false, fmt.Errorf("failed to marshal request body: %w", err)
			}
		}
	}

	if c.logger != nil {
		c.logger.LogRequest(ctx, method, fullURL, requestBodyBytes)
	}

	var bodyReader io.Reader
	if requestBodyBytes != nil {
		bodyReader = bytes.NewReader(requestBodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create request: %w", err)
	}

	c.setScopeHeader(req)

	for key, values := range extraHeaders {
		for _, v := range values {
			req.Header.Set(key, v)
		}
	}

	if classic {
		req.Header.Set("Accept", "application/xml")
	}
	if requestBodyBytes != nil {
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		} else if classic {
			req.Header.Set("Content-Type", "application/xml")
		} else if method == http.MethodPatch {
			req.Header.Set("Content-Type", "application/merge-patch+json")
		} else {
			req.Header.Set("Content-Type", "application/json")
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("API request failed: %w", err)
	}

	return resp, classic, nil
}

// handleResponse processes API responses and handles common error cases.
// classic comes from the request URL captured by doRequestFull so codec
// selection is stable across any redirect path rewriting.
func (c *Transport) handleResponse(ctx context.Context, resp *http.Response, classic bool, expectedStatus int, result any) error {
	defer func() { _ = resp.Body.Close() }()

	c.logDeprecation(resp)

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return fmt.Errorf("failed to read response body: %w", readErr)
	}

	if c.logger != nil {
		c.logger.LogResponse(ctx, resp.StatusCode, resp.Header, body)
	}

	if resp.StatusCode != expectedStatus {
		respErr := &APIResponseError{
			StatusCode: resp.StatusCode,
			Method:     resp.Request.Method,
			URL:        resp.Request.URL.String(),
			Body:       string(body),
		}

		var apiErr ApiError
		_ = json.Unmarshal(body, &apiErr) // best-effort; non-JSON bodies leave apiErr zero
		edge := false
		switch {
		case len(apiErr.Errors) > 0:
			if apiErr.HTTPStatus > 0 {
				respErr.StatusCode = apiErr.HTTPStatus
			}
			respErr.Errors = apiErr.Errors
		// Classic API errors are an HTML "Status page", not JSON. Lift the
		// message into a synthetic structured detail so it renders through
		// the same path as Pro errors (Error/Summary/Details/FieldErrors,
		// and traceId).
		case isClassicStatusPage(resp.Header, body):
			if msg, ok := parseClassicErrorMessage(resp.Header, body); ok {
				respErr.Errors = []Error{{Description: msg}}
			}
		// Any other HTML body is an edge or gateway error page carrying no Jamf
		// message. Condense it the same way, and mark the error so a consumer
		// can branch — the page means the request did not reach Jamf, which
		// calls for reporting the host's egress IP rather than reading a
		// message. Marked here and not only on the token exchange because the
		// same page arrives on either path; see nonjson_errors.go.
		case isHTMLErrorBody(resp.Header, body):
			edge = true
			respErr.Errors = []Error{{Description: describeNonJSONError(resp.Header, body)}}
		}
		respErr.TraceID = pickTraceID(apiErr.TraceID, resp.Header)

		// Both wrappers keep respErr in the chain, so errors.As, AsAPIError and
		// every accessor go on working; only Error() gains the annotation.
		if edge {
			return fmt.Errorf("%w: %w", ErrUnexpectedResponse, respErr)
		}
		if guidance := nonJSONAuthGuidance(resp.StatusCode, resp.Header, body); guidance != "" {
			return fmt.Errorf("%w\n%s", respErr, guidance)
		}
		return respErr
	}

	if result != nil {
		// Raw byte responses (e.g. text/csv exports) bypass decoding.
		if bp, ok := result.(*[]byte); ok {
			*bp = append((*bp)[:0], body...)
		} else if classic {
			// Classic endpoints sometimes return 200/201 with an empty body
			// or an XML prolog-only body (e.g. computercommands/command/{cmd}
			// when no results exist). xml.Unmarshal returns io.EOF when it
			// finds no root element; treat that as a successful zero-value
			// decode, identical to a fully empty body.
			if len(bytes.TrimSpace(body)) > 0 {
				if err := xml.Unmarshal(body, result); err != nil && err != io.EOF {
					return fmt.Errorf("failed to decode XML response: %w", err)
				}
			}
		} else if err := json.Unmarshal(body, result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}
