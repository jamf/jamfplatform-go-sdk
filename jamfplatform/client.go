// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package jamfplatform

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"slices"
	"time"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/internal/client"
	"golang.org/x/oauth2"
)

// Client provides typed methods for all Jamf Platform API operations.
type Client struct {
	transport *client.Transport
}

// NewClient creates a new Jamf Platform API client.
func NewClient(baseURL, clientID, clientSecret string, opts ...Option) *Client {
	cfg := &clientConfig{
		userAgent: "jamfplatform-go-sdk/dev",
	}
	for _, opt := range opts {
		opt(cfg)
	}

	var transportOpts []client.Option
	if cfg.httpClient != nil {
		transportOpts = append(transportOpts, client.WithHTTPClient(cfg.httpClient))
	}

	var cache client.TokenCache
	if cfg.tokenCache != nil {
		cache = cfg.tokenCache
	} else if cfg.cacheDir != "" {
		cache = client.NewFileTokenCache(cfg.cacheDir)
	}
	if cache != nil {
		transportOpts = append(transportOpts, client.WithTokenCache(cache, client.CacheKey(baseURL, clientID)))
	}
	if cfg.cookieJarDir != "" {
		jarPath := filepath.Join(cfg.cookieJarDir, "jamfplatform-cookies-"+client.CacheKey(baseURL, clientID))
		if jar, err := client.NewFileCookieJar(jarPath); err == nil {
			transportOpts = append(transportOpts, client.WithCookieJar(jar))
		} else {
			log.Printf("jamfplatform: WithFileCookieJar: failed to open %s: %v — falling back to in-memory jar", jarPath, err)
		}
	}
	if cfg.tenantID != "" {
		transportOpts = append(transportOpts, client.WithTenantID(cfg.tenantID))
	}
	if cfg.environmentID != "" {
		transportOpts = append(transportOpts, client.WithEnvironmentID(cfg.environmentID))
	}
	if cfg.minRequestIntervalSet {
		transportOpts = append(transportOpts, client.WithMinRequestInterval(cfg.minRequestInterval))
	}
	if len(cfg.headers) > 0 {
		transportOpts = append(transportOpts, client.WithHeaders(cfg.headers))
	}
	if cfg.authHeaderName != "" {
		transportOpts = append(transportOpts, client.WithAuthorizationHeaderName(cfg.authHeaderName))
	}
	if cfg.retryPolicySet {
		transportOpts = append(transportOpts, client.WithRetryPolicy(cfg.retryWaitMin, cfg.retryWaitMax, cfg.retryMax))
	}

	transport := client.NewTransportWithUserAgent(baseURL, clientID, clientSecret, cfg.userAgent, transportOpts...)
	if cfg.logger != nil {
		transport.SetLogger(cfg.logger)
	}

	return &Client{
		transport: transport,
	}
}

// BaseURL returns the base URL configured for the client.
func (c *Client) BaseURL() string {
	return c.transport.BaseURL()
}

// ValidateCredentials tests authentication by requesting an OAuth token.
func (c *Client) ValidateCredentials(ctx context.Context) error {
	return c.transport.ValidateCredentials(ctx)
}

// AccessToken returns a valid OAuth2 token from the client's credentials configuration.
func (c *Client) AccessToken(ctx context.Context) (*oauth2.Token, error) {
	return c.transport.AccessToken(ctx)
}

// ScopeKind identifies which kind of Jamf scope a client is bound to. It is an
// alias for the transport's type, so a consumer can name it, switch on it and
// call ScopeHeader on it without importing internal/client — which is what made
// the kind unreachable before.
type ScopeKind = client.ScopeKind

// Scope kinds. A zero value means no scope, which is how organization-scoped
// credentials work: the gateway resolves the context from the access token, so
// the client sends no scope header. That zero value is named
// ScopeOrganization for the benefit of the generated Privileges registry; it
// carries no header, confirmed absent across every published spec and the
// gateway configuration.
const (
	// ScopeOrganization is the zero value and sends no header: the gateway
	// resolves the organization from the access token. It exists so the
	// generated Privileges registry can name this scope instead of leaving an
	// empty slice, which would be indistinguishable from "the spec declared
	// nothing". There is no WithOrganizationID — an unset scope already means
	// organization.
	ScopeOrganization = client.ScopeOrganization
	// ScopeTenant scopes requests to a single product tenant, sent as
	// X-Tenant-Id. The legacy scope; prefer ScopeEnvironment.
	ScopeTenant = client.ScopeTenant
	// ScopeEnvironment scopes requests to a platform environment — a grouping
	// of tenants — sent as X-Environment-Id. The scope to prefer.
	ScopeEnvironment = client.ScopeEnvironment
)

// Scope reports which kind of scope this client carries and the ID it sends.
//
// Prefer this over reading a single ID: the scope is a three-valued property,
// and an accessor for one kind cannot express the others. A zero kind with an
// empty ID means no scope header is sent at all — the organization-scoped case
// — so a caller switching on the kind should handle it rather than assume a
// scope is always present.
func (c *Client) Scope() (ScopeKind, string) {
	return c.transport.Scope()
}

// Transport returns the underlying transport used by sub-package clients in
// jamfplatform/. Sub-package constructors (e.g. devices.New) call this to
// share the authenticated HTTP layer.
func (c *Client) Transport() *client.Transport {
	return c.transport
}

// clientConfig holds configuration applied via Option functions.
type clientConfig struct {
	userAgent     string
	httpClient    *http.Client
	logger        Logger
	tenantID      string
	environmentID string
	tokenCache    TokenCache
	cacheDir      string
	cookieJarDir  string

	headers        http.Header
	authHeaderName string

	minRequestInterval    time.Duration
	minRequestIntervalSet bool

	retryWaitMin   time.Duration
	retryWaitMax   time.Duration
	retryMax       int
	retryPolicySet bool
}

// Option configures a Client.
type Option func(*clientConfig)

// WithUserAgent sets a custom user agent string.
func WithUserAgent(userAgent string) Option {
	return func(cfg *clientConfig) {
		if userAgent != "" {
			cfg.userAgent = userAgent
		}
	}
}

// WithHTTPClient overrides the default HTTP client.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(cfg *clientConfig) {
		cfg.httpClient = httpClient
	}
}

// WithHeaders sets additional HTTP headers sent on every request the client
// makes, including the OAuth2 token exchange. Calling it more than once merges;
// a repeated header name takes the last value given.
//
// For callers whose traffic is fronted by a reverse proxy needing headers of its
// own. Prefer this over WithHTTPClient for that purpose — supplying a client
// replaces the SDK's tuned transport and drops proxy-from-environment support,
// the per-phase timeouts, the connection-pool ceiling matched to Terraform's
// default parallelism, and the write buffer package upload depends on. This
// layers onto that transport, and composes with WithHTTPClient when both are
// genuinely needed.
//
// The scope headers (X-Tenant-Id, X-Environment-Id) are rejected and logged; set
// the scope with WithTenantID or WithEnvironmentID. User-Agent set here is
// overridden by WithUserAgent. Cookie is allowed but replaces rather than merges,
// so it displaces the sticky-session cookie Jamf Cloud uses to pin a client to
// one app node.
func WithHeaders(h http.Header) Option {
	return func(cfg *clientConfig) {
		if len(h) == 0 {
			return
		}
		if cfg.headers == nil {
			cfg.headers = make(http.Header, len(h))
		}
		for name, values := range h {
			cfg.headers[http.CanonicalHeaderKey(name)] = slices.Clone(values)
		}
	}
}

// WithAuthorizationHeaderName moves the OAuth2 bearer credential out of the
// Authorization header into the named header on every API request, leaving
// Authorization free for a header supplied via WithHeaders.
//
// For callers behind a reverse proxy that consumes Authorization for its own
// service-account credential and expects Jamf's bearer under a different name.
// The token exchange is unaffected: the client-credential Basic header x/oauth2
// writes there is left in place, since relocating it would leave the token
// endpoint with nothing to authenticate.
//
// "Authorization" and the scope headers are refused and the refusal logged.
// Relocating the bearer onto Authorization would delete it, and relocating it
// onto a scope header would overwrite the scope; both fail with a status —
// 401 and 403 OWNERSHIP_FORBIDDEN — that names something else as the cause.
func WithAuthorizationHeaderName(name string) Option {
	return func(cfg *clientConfig) {
		cfg.authHeaderName = name
	}
}

// WithLogger sets a logger for HTTP request/response logging.
//
// The SDK logs nothing until you install a Logger, and it redacts nothing it
// hands over. LogRequest receives the raw request body; LogResponse receives
// the raw response body and headers. Request bodies carry whatever secrets a
// write sends: ClientSecret, AdminPassword, KeystorePassword, the escaped
// plist in a configuration profile's Payloads. A Logger that renders a body
// verbatim therefore writes those in plaintext wherever it writes, which is
// often a support ticket or a CI log. Redact inside the Logger, or log only
// the method, URL and status.
//
// LogRequest never sees the bearer token or the client credential: the SDK
// passes it no headers at all, and the OAuth2 token exchange runs on its own
// http.Client outside the logged path.
//
// LogResponse is different. It receives the response http.Header unfiltered,
// so filter headers there rather than rendering them wholesale — the SDK's own
// acceptance tracer prints response headers from a fixed allowlist for exactly
// this reason, so that a header carrying credential material cannot reach a
// trace by accident and a header added upstream later cannot either.
func WithLogger(logger Logger) Option {
	return func(cfg *clientConfig) {
		cfg.logger = logger
	}
}

// WithTokenCache sets a custom token cache for persisting tokens across process restarts.
func WithTokenCache(cache TokenCache) Option {
	return func(cfg *clientConfig) {
		cfg.tokenCache = cache
	}
}

// WithFileTokenCache enables file-based token caching in the given directory.
func WithFileTokenCache(dir string) Option {
	return func(cfg *clientConfig) {
		cfg.cacheDir = dir
	}
}

// WithFileCookieJar enables file-based cookie jar persistence in the given
// directory. The cookie jar survives across process invocations so
// sticky-session cookies keep pointing a CLI-style caller at the same app
// node between runs.
func WithFileCookieJar(dir string) Option {
	return func(cfg *clientConfig) {
		cfg.cookieJarDir = dir
	}
}

// WithTenantID configures the tenant this client is scoped to. It is sent as
// the X-Tenant-Id request header on every API call.
//
// Tenant scoping is the legacy form. Prefer WithEnvironmentID: an environment
// groups a customer's tenants, and it is the scope Jamf intends integrations to
// be created with. Tenant scope remains supported — a tenant is a single
// product, and some surfaces are only reachable that way — but new integrations
// should not choose it by default.
//
// Mutually exclusive with WithEnvironmentID. If both are set, environment wins
// regardless of the order they are passed in; see WithEnvironmentID.
//
// The gateway used to take the tenant from the URL path
// (/api/{namespace}/{version}/tenant/{tenantID}); it moved to a header at the
// Platform API GA. When neither scope option is set, no scope header is sent
// and the gateway resolves the context from the access token instead.
func WithTenantID(id string) Option {
	return func(cfg *clientConfig) {
		cfg.tenantID = id
	}
}

// WithEnvironmentID configures the platform environment this client is scoped
// to. It is sent as the X-Environment-Id request header on every API call.
//
// This is the scope to prefer. An environment groups a customer's tenants, and
// it is what Jamf intends new integrations to be created with; WithTenantID is
// the legacy alternative. The Platform API GA invalidates every public-beta
// credential, so integrations have to be re-created regardless — which makes it
// the moment to create them environment-scoped rather than a migration to
// schedule later.
//
// The header must match the credential. An integration is minted against one
// scope, and crossing over is refused with 403 OWNERSHIP_FORBIDDEN even when
// both IDs belong to the same customer, so this is a choice between two
// integrations rather than two IDs for one.
//
// The two scopes are mutually exclusive: a client carries exactly one, and
// exactly one scope header is ever sent. Setting both this and WithTenantID is
// a configuration mistake rather than a combination — **environment takes
// precedence**, whichever order the options are passed in, because a client
// built from an environment-scoped credential cannot use a tenant header
// anyway. Callers that can validate their own input should reject the pair up
// front instead of relying on that precedence.
func WithEnvironmentID(id string) Option {
	return func(cfg *clientConfig) {
		cfg.environmentID = id
	}
}

// WithMinRequestInterval sets the minimum wall-clock time between the start of
// consecutive outbound HTTP requests, paced across the shared transport that
// the SDK fans out over parallel goroutines. It gives the server breathing room
// and reduces 429s. A value <= 0 disables the gate. When this option is not
// supplied, the SDK applies a 100ms default.
func WithMinRequestInterval(d time.Duration) Option {
	return func(cfg *clientConfig) {
		cfg.minRequestInterval = d
		cfg.minRequestIntervalSet = true
	}
}

// WithRetryPolicy overrides the SDK's automatic retry timing for transient
// failures (429, 503, and 500/502/504 on GET/DELETE/PUT/HEAD — see
// isRetryableWriteStatus's godoc in the SDK's internal transport for the
// exact policy).
//
// waitMin seeds an exponential backoff and waitMax caps it; maxRetries
// follows retryablehttp's own semantics, so total attempts per request =
// maxRetries+1 and 0 disables automatic retrying entirely. The production
// default is waitMin 1s, waitMax 60s, maxRetries 4 — bounding a full retry
// sequence at 1+2+4+8 = 15s of waiting.
//
// Two distinct uses, both supported:
//
//   - Tests. A unit test mocking a persistently-failing transient status
//     (e.g. an always-500 GET, to exercise a caller's error-handling path)
//     otherwise waits out the full production backoff on every run. Pass a
//     few milliseconds and a low maxRetries.
//   - Interactive callers. A CLI generally wants a tighter bound than the
//     default so a transient failure surfaces promptly rather than looking
//     like a hang — e.g. WithRetryPolicy(200*time.Millisecond,
//     2*time.Second, 2).
//
// To bound total time rather than retry timing, prefer a context deadline:
// it covers the whole call including every retry attempt and the waits
// between them, and needs no policy change.
func WithRetryPolicy(waitMin, waitMax time.Duration, maxRetries int) Option {
	return func(cfg *clientConfig) {
		cfg.retryWaitMin = waitMin
		cfg.retryWaitMax = waitMax
		cfg.retryMax = maxRetries
		cfg.retryPolicySet = true
	}
}
