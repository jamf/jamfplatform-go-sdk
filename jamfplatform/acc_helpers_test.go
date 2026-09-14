// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/compliancebenchmarks"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/devicegroups"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/pro"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/securitycloud"
)

// runSuffix computes a unique suffix (epoch timestamp) once for the entire test run.
var runSuffix = sync.OnceValue(func() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
})

// accTraceOpts returns the diagnostic client options every acceptance client
// constructor spreads, so one variable turns each on for the whole suite:
//
//	JAMFPLATFORM_ACC_TRACE=1      print every request and response
//	JAMFPLATFORM_ACC_FAST_RETRY=1 shorten the retry backoff (see accRetryOpts)
//	JAMFPLATFORM_ACC_TRACE_MAX=n  per-body byte cap; 0 for none
//
//	JAMFPLATFORM_ACC_TRACE=1 JAMFPLATFORM_ACC_FAST_RETRY=1 \
//	  go test -v -tags acceptance -run TestAcceptance_Account ./jamfplatform/
//
// Off by default and free when unset: with no logger installed the transport
// skips the calls entirely rather than formatting and discarding.
//
// Output goes to stderr rather than t.Logf because the logger has no *testing.T
// to attach to, and because stderr streams as the requests happen — the point of
// tracing is watching a hang or a retry storm unfold, which buffered per-test
// output cannot show.
//
// -v is REQUIRED for that streaming: without it `go test` buffers the test
// binary's output and shows it only for a failing test, so a trace of a passing
// test is swallowed and a trace of a hanging one appears only once it ends.
func accTraceOpts() []jamfplatform.Option {
	var opts []jamfplatform.Option
	if accEnv("JAMFPLATFORM_ACC_TRACE") != "" {
		opts = append(opts, jamfplatform.WithLogger(&accTracer{max: traceBodyLimit()}))
	}
	opts = append(opts, accRetryOpts()...)
	return opts
}

// accRetryOpts shortens the retry backoff when JAMFPLATFORM_ACC_FAST_RETRY is
// set. It is opt-in rather than the default because the production timing is
// part of what the suite exercises.
//
// Why it is worth having: the production policy is RetryMax=4 with
// RetryWaitMin=1s and RetryWaitMax=60s, and retryablehttp's
// RateLimitLinearJitterBackoff treats those two as the *jitter range*, not as
// (initial, cap) — the wait is (1s + rand*59s) * attemptNum, capped at 60s. So
// the first retry alone waits a median of ~30s and four retries total a median
// of ~184s, up to 240s. An endpoint that returns a persistent 502 in 1.5s (which
// GET /api/sso/v1/connections currently does) therefore takes about three
// minutes to surface, with no output in between: retries happen inside the
// retryablehttp client, below the SDK's Logger, so a trace shows one request line
// and then silence. That reads as a hang, and it was reported as one.
//
// 50ms/500ms/2 keeps a retry in the loop — so a genuinely transient failure is
// still absorbed — while bounding the wait to about a second.
func accRetryOpts() []jamfplatform.Option {
	if accEnv("JAMFPLATFORM_ACC_FAST_RETRY") == "" {
		return nil
	}
	return []jamfplatform.Option{jamfplatform.WithRetryPolicy(50*time.Millisecond, 500*time.Millisecond, 2)}
}

// traceBodyLimit is the per-body byte cap, overridable with
// JAMFPLATFORM_ACC_TRACE_MAX. It is deliberately generous: truncation can hide
// the exact field a trace was turned on to find, so the default only guards
// against a genuinely huge list body (the account licence list is ~245 rows)
// rather than trying to keep output tidy. Set it to 0 for no cap.
func traceBodyLimit() int {
	if v := accEnv("JAMFPLATFORM_ACC_TRACE_MAX"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 16384
}

// accTracer prints each request and response as it happens.
//
// Two precautions, because a trace is routinely pasted into a ticket or a chat.
// Headers are printed from a fixed allowlist rather than filtered, so
// Authorization — which LogResponse receives in full, carrying a bearer token
// live for the next 900 seconds — can never be printed by accident, and a header
// added upstream tomorrow cannot leak either. Request and response bodies have
// credential-shaped members replaced by name, which is best-effort by nature: a
// secret under an unrecognised key still prints, so treat a trace as sensitive
// regardless.
type accTracer struct {
	max int
	mu  sync.Mutex // one request's two lines must not interleave with another's
}

// secretBodyKeys are JSON members whose values are replaced before printing.
// Matched case-insensitively on the key. clientSecret and password are the ones
// the suite actually sends — UEM Connect connector creation and the Pro
// credential minting it does — but the list covers the obvious neighbours so a
// new endpoint does not quietly start logging a secret.
var secretBodyKeys = []string{
	"clientsecret", "client_secret", "password", "secret", "privatekey",
	"private_key", "token", "accesstoken", "access_token", "refreshtoken",
	"refresh_token", "apikey", "api_key", "passphrase", "psk",
}

func (l *accTracer) LogRequest(_ context.Context, method, url string, body []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(os.Stderr, "\n--> %s %s\n", method, url)
	if len(body) > 0 {
		fmt.Fprintf(os.Stderr, "    body: %s\n", l.render(redactSecrets(body)))
	}
}

func (l *accTracer) LogResponse(_ context.Context, statusCode int, headers http.Header, body []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(os.Stderr, "<-- %d\n", statusCode)

	// Response headers carry the facts that explain a failure a body cannot:
	// the traceId to quote to Jamf Support, the Deprecation notice, and the
	// Content-Encoding that determines whether href survives (see CLAUDE.md).
	for _, h := range []string{"X-Tyk-Trace-Id", "X-B3-Traceid", "Deprecation", "Sunset", "Link", "Content-Type", "Content-Encoding", "Retry-After"} {
		if v := headers.Get(h); v != "" {
			fmt.Fprintf(os.Stderr, "    %s: %s\n", h, v)
		}
	}
	if len(body) > 0 {
		fmt.Fprintf(os.Stderr, "    body: %s\n", l.render(redactSecrets(body)))
	}
}

// render indents a JSON body so it is readable, falls back to the raw bytes when
// it is not JSON (Classic is XML, and a gateway or WAF error page is HTML), and
// applies the byte cap last so the cap is measured against what is printed.
func (l *accTracer) render(body []byte) string {
	out := body
	var buf bytes.Buffer
	if json.Indent(&buf, body, "    ", "  ") == nil {
		out = buf.Bytes()
	}
	if l.max > 0 && len(out) > l.max {
		return fmt.Sprintf("%s\n    … truncated at %d of %d bytes (raise JAMFPLATFORM_ACC_TRACE_MAX, or set it to 0)", out[:l.max], l.max, len(out))
	}
	return string(out)
}

// redactSecrets replaces the values of credential-shaped JSON members. It
// re-encodes through a generic map, so a body that is not JSON object-shaped is
// returned untouched — including Classic's XML, which the suite does send.
//
// Returning the original on any failure is deliberate: a redaction pass that
// silently dropped a body would make tracing useless exactly when it matters.
func redactSecrets(body []byte) []byte {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return body
	}
	if !redactInto(v) {
		return body
	}
	out, err := json.Marshal(v)
	if err != nil {
		return body
	}
	return out
}

// redactInto walks maps and slices in place, reporting whether it changed
// anything so the caller can skip a pointless re-encode.
func redactInto(v any) bool {
	changed := false
	switch t := v.(type) {
	case map[string]any:
		for k, inner := range t {
			lk := strings.ToLower(k)
			if slices.Contains(secretBodyKeys, lk) {
				// A null stays null: replacing it would make a trace claim the
				// request carried a secret it did not send.
				if inner != nil {
					t[k] = "***REDACTED***"
					changed = true
				}
				continue
			}
			if redactInto(inner) {
				changed = true
			}
		}
	case []any:
		for _, inner := range t {
			if redactInto(inner) {
				changed = true
			}
		}
	}
	return changed
}

// errAccCredsUnset marks the one condition under which skipping the whole suite
// is legitimate: no tenant was configured, as on a local run or a fork PR.
//
// Every other error from initAcceptanceClient means credentials WERE supplied and
// the tenant refused them, which must fail. Conflating the two is how a total
// auth outage reports success: on 2026-08-04 a WAF block made all 146 scoped
// tests skip, the package still printed `ok`, and the acceptance check went green
// having executed zero assertions against the tenant.
var errAccCredsUnset = errors.New("acceptance credentials not configured")

// initAcceptanceClient creates and validates the singleton acceptance client
// once, preferring environment scope over tenant scope.
//
// Environment is becoming the predominant Jamf scope, with tenant the fallback,
// so the suite follows the same order: when a complete environment credential
// set is configured it is used, otherwise the tenant set is. A credential is
// minted against one scope and the header must match it, so this is a choice
// between two integrations rather than two IDs for one — see
// WithEnvironmentID.
//
// The consequence worth knowing before configuring the environment secrets:
// every test using accClient switches scope at that moment, and an environment
// credential generally reaches a different Jamf Pro instance with different
// data. Expect fixture-dependent tests to change behaviour, and read
// accScopeInUse in the log before concluding that the secrets broke CI.
var initAcceptanceClient = sync.OnceValues(func() (*jamfplatform.Client, error) {
	envBase, envID := accEnvironmentBaseURL(), accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_ID")
	envClient, envSecret := accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_ID"), accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_SECRET")
	if accEnvironmentSetComplete() {
		c := jamfplatform.NewClient(envBase, envClient, envSecret,
			append(accTraceOpts(), jamfplatform.WithEnvironmentID(envID))...)
		if err := c.ValidateCredentials(context.Background()); err != nil {
			return nil, fmt.Errorf("failed to validate environment-scoped credentials: %w", err)
		}
		accScopeInUse = "environment " + envID
		return c, nil
	}

	baseURL := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL")
	clientID := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_ID")
	clientSecret := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_SECRET")
	tenantID := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_ID")

	if baseURL == "" || clientID == "" || clientSecret == "" || tenantID == "" {
		return nil, fmt.Errorf("%w: set JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL, JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_ID, JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_SECRET, JAMFPLATFORM_ACC_PRO_TENANT_ID (or the JAMFPLATFORM_ACC_ENVIRONMENT_* set for environment scope)", errAccCredsUnset)
	}

	c := jamfplatform.NewClient(baseURL, clientID, clientSecret,
		append(accTraceOpts(), jamfplatform.WithTenantID(tenantID))...)
	if err := c.ValidateCredentials(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to validate credentials: %w", err)
	}
	accScopeInUse = "tenant " + tenantID
	return c, nil
})

// accScopeInUse records which scope initAcceptanceClient settled on, so a run
// says so out loud. A silent switch between scopes would be indistinguishable
// from the tenant's data changing underneath the suite.
var accScopeInUse = "(client not yet built)"

// errAccJSCCredsUnset marks a Security Cloud acceptance run with no Security
// Cloud credentials configured. It is deliberately separate from
// errAccCredsUnset: Security Cloud is a different product with its own tenant
// ID, and a Jamf Pro API client cannot reach it at all — probed 2026-08-17, a
// Security Cloud client answers 403 BAD_PERMISSIONS on /api/pro and
// /api/devices, and the reverse holds. So the suite needs a second credential
// set, not just a second tenant ID, and a tenant configured for Jamf Pro only
// must skip these tests rather than fail them.
var errAccJSCCredsUnset = errors.New("Security Cloud acceptance credentials not configured")

// initJSCAcceptanceClient creates and validates the singleton Security Cloud
// acceptance client. JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_BASE_URL is optional and defaults to the
// Jamf Pro base URL, since both products are served by the same regional
// gateway; the credentials and tenant are not optional.
// accJSCCredSource records which credential the Security Cloud client was built
// from, because the two reach different Security Cloud tenants with different
// fixtures and a silent switch would look like the tenant's data changing.
var accJSCCredSource = "(client not yet built)"

var initJSCAcceptanceClient = sync.OnceValues(func() (*jamfplatform.Client, error) {
	// An environment-scoped credential reaches Security Cloud: wire-verified
	// 2026-09-03, GET /securitycloud/v1/groups and /v2/groups both 200 with
	// X-Environment-Id, alongside 200 on pro, devices, blueprints,
	// compliance-benchmarks and ai/governance on the same token. That is why the
	// dedicated Security Cloud credential is no longer required — an environment
	// credential covers all four product spaces this suite touches.
	//
	// It is still preferred over the environment credential when configured,
	// because the two land on DIFFERENT Security Cloud tenants: the dedicated one
	// carries the suite's long-lived fixtures. Prefer-then-fall-back rather than
	// replace, so removing the secret is a deliberate migration rather than a
	// silent change of tenant. Note the environment credential is tenant-agnostic
	// here — Security Cloud is itself tenant-scoped, on the same X-Tenant-Id
	// header as Jamf Pro, so "environment vs Security Cloud" is a difference of
	// which credential, not of which scope kind.
	clientID := accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_ID")
	clientSecret := accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_SECRET")
	tenantID := accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_ID")

	if !accSecurityCloudSetComplete() {
		envBase := accEnvironmentBaseURL()
		envID := accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_ID")
		envClientID := accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_ID")
		envSecret := accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_SECRET")
		if envBase != "" && envID != "" && envClientID != "" && envSecret != "" {
			c := jamfplatform.NewClient(envBase, envClientID, envSecret,
				append(accTraceOpts(), jamfplatform.WithEnvironmentID(envID))...)
			if err := c.ValidateCredentials(context.Background()); err != nil {
				return nil, fmt.Errorf("failed to validate environment-scoped credentials for Security Cloud: %w", err)
			}
			accJSCCredSource = "environment " + envID
			return c, nil
		}
		return nil, fmt.Errorf("%w: set the JAMFPLATFORM_ACC_ENVIRONMENT_* set, or JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_ID, JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_SECRET and JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_ID", errAccJSCCredsUnset)
	}
	// The dedicated set being complete, accSecurityCloudBaseURL resolves to its
	// chain — the same value the failure report will name.
	baseURL := accSecurityCloudBaseURL()
	if baseURL == "" {
		return nil, fmt.Errorf("%w: JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_BASE_URL or JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL must be set", errAccJSCCredsUnset)
	}
	accJSCCredSource = "securitycloud tenant " + tenantID

	// Plain WithTenantID: this client is built from Security Cloud credentials
	// and only ever reaches /api/securitycloud, so there is nothing to scope
	// per-namespace. The per-namespace override this used to exercise went away
	// with header scoping — one credential set reaches one product (a Security
	// Cloud client answers 403 on /api/pro and vice versa), so a single Client
	// could never have served both regardless of how many tenant IDs it held.
	c := jamfplatform.NewClient(baseURL, clientID, clientSecret,
		append(accTraceOpts(), jamfplatform.WithTenantID(tenantID))...)
	if err := c.ValidateCredentials(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to validate Security Cloud credentials: %w", err)
	}

	return c, nil
})

// accSecurityCloudClient returns a live client for the securitycloud package.
// It skips when no Security Cloud credentials are configured and FAILS when
// they are configured but rejected — the same distinction accClient draws, for
// the same reason (see errAccCredsUnset).
func accSecurityCloudClient(t *testing.T) *securitycloud.Client {
	t.Helper()
	c, err := initJSCAcceptanceClient()
	switch {
	case errors.Is(err, errAccJSCCredsUnset):
		skipOrFailUnset(t, "securitycloud", err)
	case err != nil:
		t.Fatal(credentialRejectedMessage(accSecurityCloudBaseURL(), err))
	}
	logAccJSCCredSourceOnce()
	return securitycloud.New(c)
}

// logAccJSCCredSourceOnce announces which credential the Security Cloud client
// was built from, once per run. The dedicated Security Cloud set and the
// environment set land on DIFFERENT Security Cloud tenants with different
// fixtures, so a run that does not say which one it settled on leaves that
// switch looking like the tenant's data having changed. It matters now rather
// than in principle: the dedicated secrets are commented out in
// .github/workflows/acceptance.yml, so the environment fallback is the active
// path on every CI run.
//
// Logged from the accessor rather than from initJSCAcceptanceClient so the line
// appears whenever a run builds a Security Cloud client, independent of which
// tests select the credential. Same reasoning, same shape, as logAccScopeOnce.
var logAccJSCCredSourceOnce = sync.OnceFunc(func() {
	fmt.Fprintf(os.Stderr, "acceptance: Security Cloud built from %s credentials\n", accJSCCredSource)
})

// errAccTenantCredsUnset marks an absent tenant-scoped credential set.
var errAccTenantCredsUnset = errors.New("tenant-scoped acceptance credentials not configured")

// initTenantAcceptanceClient creates and validates a client that is FORCED onto
// tenant scope, with no environment fallback.
//
// It exists because accClient prefers environment scope whenever that credential
// set is complete — the right default, since environment is the scope Jamf
// intends new integrations to use. The consequence is that once the environment
// secrets are configured, nothing in the suite sends X-Tenant-Id any more, and
// WithTenantID stops being exercised. That option is public, supported, and used
// by terraform-provider-jamfplatform, and tenant is the LEGACY scope — precisely
// the kind of surface that breaks without anyone noticing. So one lane pins it.
//
// This is scope coverage, not data coverage: it deliberately reads only, and it
// asserts the scope the client actually settled on rather than trusting that the
// credential set it was handed implies it.
var initTenantAcceptanceClient = sync.OnceValues(func() (*jamfplatform.Client, error) {
	baseURL := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL")
	clientID := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_ID")
	clientSecret := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_SECRET")
	tenantID := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_ID")

	if baseURL == "" || clientID == "" || clientSecret == "" || tenantID == "" {
		return nil, fmt.Errorf("%w: set JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL, JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_ID, JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_SECRET, JAMFPLATFORM_ACC_PRO_TENANT_ID", errAccTenantCredsUnset)
	}

	c := jamfplatform.NewClient(baseURL, clientID, clientSecret,
		append(accTraceOpts(), jamfplatform.WithTenantID(tenantID))...)
	if err := c.ValidateCredentials(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to validate tenant-scoped credentials: %w", err)
	}
	return c, nil
})

// accTenantClient returns a live tenant-scoped client. Unset credentials skip;
// supplied-but-rejected credentials fail, the same distinction accClient draws.
func accTenantClient(t *testing.T) *jamfplatform.Client {
	t.Helper()
	c, err := initTenantAcceptanceClient()
	switch {
	case errors.Is(err, errAccTenantCredsUnset):
		skipOrFailUnset(t, "pro-tenant", err)
	case err != nil:
		t.Fatal(credentialRejectedMessage(accEnv("JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL"), err))
	}
	return c
}

// errAccEnvCredsUnset marks an absent environment-scoped credential set, the
// same way errAccCredsUnset marks an absent tenant one.
var errAccEnvCredsUnset = errors.New("environment-scoped acceptance credentials not configured")

// initEnvAcceptanceClient creates and validates the singleton environment-scoped
// acceptance client.
//
// This needs its own credential set, not just a second ID: a credential is
// minted against one scope, and the gateway refuses a header that disagrees with
// it — an environment-scoped integration sending X-Tenant-Id, or a tenant-scoped
// one sending X-Environment-Id, gets 403 OWNERSHIP_FORBIDDEN even when both IDs
// belong to the same customer. Wire-verified against securitycloud in prod on
// 2026-08-25, which is what TestAcceptance_EnvironmentScope pins.
var initEnvAcceptanceClient = sync.OnceValues(func() (*jamfplatform.Client, error) {
	baseURL := accEnvironmentBaseURL()
	clientID := accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_ID")
	clientSecret := accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_SECRET")
	environmentID := accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_ID")

	if baseURL == "" || clientID == "" || clientSecret == "" || environmentID == "" {
		return nil, fmt.Errorf("%w: set JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_ID, JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_SECRET, JAMFPLATFORM_ACC_ENVIRONMENT_ID (and JAMFPLATFORM_ACC_ENVIRONMENT_BASE_URL when it differs from JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL)", errAccEnvCredsUnset)
	}

	c := jamfplatform.NewClient(baseURL, clientID, clientSecret,
		append(accTraceOpts(), jamfplatform.WithEnvironmentID(environmentID))...)
	if err := c.ValidateCredentials(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to validate environment-scoped credentials: %w", err)
	}
	return c, nil
})

// accEnvClient returns a live environment-scoped client. Unset credentials skip;
// supplied-but-rejected credentials fail, the same distinction accClient draws.
func accEnvClient(t *testing.T) *jamfplatform.Client {
	t.Helper()
	c, err := initEnvAcceptanceClient()
	switch {
	case errors.Is(err, errAccEnvCredsUnset):
		skipOrFailUnset(t, "environment", err)
	case err != nil:
		t.Fatal(credentialRejectedMessage(accEnvironmentBaseURL(), err))
	}
	return c
}

// errAccOrgCredsUnset marks an absent organization-scoped credential set.
var errAccOrgCredsUnset = errors.New("organization-scoped acceptance credentials not configured")

// initOrgAcceptanceClient creates and validates the singleton organization-scoped
// client used by the Jamf Account tests.
//
// Organization scope is the *absence* of a scope, not a header: the gateway
// derives the organization from the token itself, so no WithTenantID or
// WithEnvironmentID is applied and Client.Scope() reports the zero kind with an
// empty ID. Passing either option would be actively wrong — it would make the
// gateway resolve a different context type and refuse the request.
//
// JAMFPLATFORM_ACC_ORGANIZATION_BASE_URL is separate from JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL because the
// Jamf Account api-product ships only use1 api-definitions: an EU base URL
// cannot reach these endpoints at all, and the failure does not look regional.
var initOrgAcceptanceClient = sync.OnceValues(func() (*jamfplatform.Client, error) {
	baseURL := accOrgBaseURL()
	clientID := accEnv("JAMFPLATFORM_ACC_ORGANIZATION_CLIENT_ID")
	clientSecret := accEnv("JAMFPLATFORM_ACC_ORGANIZATION_CLIENT_SECRET")

	if baseURL == "" || clientID == "" || clientSecret == "" {
		return nil, fmt.Errorf("%w: set JAMFPLATFORM_ACC_ORGANIZATION_CLIENT_ID, JAMFPLATFORM_ACC_ORGANIZATION_CLIENT_SECRET (and JAMFPLATFORM_ACC_ORGANIZATION_BASE_URL — must be the US gateway)", errAccOrgCredsUnset)
	}

	c := jamfplatform.NewClient(baseURL, clientID, clientSecret, accTraceOpts()...)
	if err := c.ValidateCredentials(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to validate organization-scoped credentials: %w", err)
	}
	return c, nil
})

// accOrgClient returns a live organization-scoped client. Unset credentials skip;
// supplied-but-rejected credentials fail, the same distinction accClient draws.
func accOrgClient(t *testing.T) *jamfplatform.Client {
	t.Helper()
	c, err := initOrgAcceptanceClient()
	switch {
	case errors.Is(err, errAccOrgCredsUnset):
		skipOrFailUnset(t, "organization", err)
	case err != nil:
		t.Fatal(credentialRejectedMessage(accOrgBaseURL(), err))
	}
	return c
}

// requirePackageStore skips unless this instance has a working Jamf Cloud
// distribution point, which is what packages are stored in.
//
// Without one, package CRUD is not merely unsupported, it is BROKEN in a way that
// looks like an SDK fault. Wire-verified 2026-09-03 across two instances running
// the identical build 11.31.1-t1787060595569:
//
//	                        cdnType      directUploadCapable  /v1/jcds/files
//	no distribution point   NONE         false                404 "JCDS Settings record not found"
//	Jamf Cloud              JAMF_CLOUD   true                 200, real files
//
// On the instance with cdnType NONE, POST /v1/packages returns 201 with an id and
// the package reads back — then DELETE answers 500 and half-deletes it, removing
// the Pro row while stranding an undeletable Classic record. Because DELETE+5xx is
// retryable (correctly: delete is idempotent), the transport retries and the
// caller sees only the retry's 404 INVALID_ID. So four package tests failed on a
// 404 whose real cause was a 500 two hops earlier, and each run leaked a package
// record that cannot be removed.
//
// Gated on cdnType rather than on directUploadCapable because cdnType names the
// backing store, which is the thing packages need; directUploadCapable is about
// how bytes get there. Both agree on both instances, so either would do today —
// cdnType is the one that will still mean the right thing if a non-Jamf-Cloud CDN
// gains direct upload.
func requirePackageStore(t *testing.T, c *jamfplatform.Client) {
	t.Helper()
	cdp, err := pro.New(c).GetCloudDistributionPointV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetCloudDistributionPointV1: cannot determine whether this instance can store packages: %v", err)
	}
	if cdp.CdnType != string(pro.CloudDistributionPointCdnTypeJamfCloud) {
		t.Skipf("packages need a Jamf Cloud distribution point; this instance reports cdnType=%q, so creates succeed but deletes 500 and strand an undeletable record", cdp.CdnType)
	}
}

// requireWriteOptIn skips unless the named environment variable is set. Used for
// mutations that touch shared organization or environment state, where the
// blast radius is a real customer record rather than a throwaway tenant object.
// The message names the variable so the skip is actionable rather than a dead end.
func requireWriteOptIn(t *testing.T, envVar, why string) {
	t.Helper()
	if os.Getenv(envVar) == "" {
		t.Skipf("Skipping: set %s to opt in. %s", envVar, why)
	}
}

// egressIP reports this host's public egress IP, resolved once per run because a
// rejected credential fails every test in the suite and a per-test lookup would
// add three seconds each. An empty result is itself a signal: a host that cannot
// reach checkip.amazonaws.com probably cannot reach Jamf either.
var egressIP = sync.OnceValue(func() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://checkip.amazonaws.com", nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
})

// accLegacyEnvNames maps each current acceptance variable to the name it had
// before the 2026-09-03 rename, so an existing local .env keeps working.
//
// The rename gave every credential set the shape
// JAMFPLATFORM_ACC_<PRODUCT>_<SCOPE>_<FIELD>, with the product token present only
// where the scope is product-bound. Tenant is per-product; environment and
// organization span a customer's tenants, so they carry no product token — which
// is why the previous scheme was misleading: it listed ENV/ORG/JSC as if they
// were three scope kinds when Security Cloud is itself tenant-scoped, on the same
// X-Tenant-Id header as Jamf Pro. The ACC_ prefix also frees
// JAMFPLATFORM_CLIENT_ID and friends, which jamfplatform/doc.go documents as the
// *consumer* convention and which the suite was quietly borrowing.
var accLegacyEnvNames = map[string]string{
	"JAMFPLATFORM_ACC_AIGOVERNANCE_WRITE_OK":              "JAMFPLATFORM_AIGOV_WRITE_OK",
	"JAMFPLATFORM_ACC_PROCLASSIC_COMPUTER_ID":             "JAMFPLATFORM_CLASSIC_COMPUTER_ID",
	"JAMFPLATFORM_ACC_PROCLASSIC_MOBILE_DEVICE_ID":        "JAMFPLATFORM_CLASSIC_MOBILE_DEVICE_ID",
	"JAMFPLATFORM_ACC_PRO_DEP_TOKEN":                      "JAMFPLATFORM_DEP_TOKEN",
	"JAMFPLATFORM_ACC_PRO_DEVICE_GROUP_ID":                "JAMFPLATFORM_DEVICE_GROUP_ID",
	"JAMFPLATFORM_ACC_ENVIRONMENT_BASE_URL":               "JAMFPLATFORM_ENV_BASE_URL",
	"JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_ID":              "JAMFPLATFORM_ENV_CLIENT_ID",
	"JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_SECRET":          "JAMFPLATFORM_ENV_CLIENT_SECRET",
	"JAMFPLATFORM_ACC_ENVIRONMENT_ID":                     "JAMFPLATFORM_ENVIRONMENT_ID",
	"JAMFPLATFORM_ACC_ORGANIZATION_BASE_URL":              "JAMFPLATFORM_ORG_BASE_URL",
	"JAMFPLATFORM_ACC_ORGANIZATION_CLIENT_ID":             "JAMFPLATFORM_ORG_CLIENT_ID",
	"JAMFPLATFORM_ACC_ORGANIZATION_CLIENT_SECRET":         "JAMFPLATFORM_ORG_CLIENT_SECRET",
	"JAMFPLATFORM_ACC_ORGANIZATION_WRITE_OK":              "JAMFPLATFORM_ORG_WRITE_OK",
	"JAMFPLATFORM_ACC_PRO_PRELOAD_WIPE_OK":                "JAMFPLATFORM_PRELOAD_WIPE_OK",
	"JAMFPLATFORM_ACC_PRO_PROTECT_CLIENT_ID":              "JAMFPLATFORM_PROTECT_CLIENT_ID",
	"JAMFPLATFORM_ACC_PRO_PROTECT_PASSWORD":               "JAMFPLATFORM_PROTECT_PASSWORD",
	"JAMFPLATFORM_ACC_PRO_PROTECT_URL":                    "JAMFPLATFORM_PROTECT_URL",
	"JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL":                "JAMFPLATFORM_BASE_URL",
	"JAMFPLATFORM_ACC_SECURITYCLOUD_GATEWAY_WRITE_OK":     "JAMFPLATFORM_JSC_GATEWAY_WRITE_OK",
	"JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_BASE_URL":      "JAMFPLATFORM_JSC_BASE_URL",
	"JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_ID":     "JAMFPLATFORM_JSC_CLIENT_ID",
	"JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_SECRET": "JAMFPLATFORM_JSC_CLIENT_SECRET",
	"JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_ID":            "JAMFPLATFORM_JSC_TENANT_ID",
	"JAMFPLATFORM_ACC_SECURITYCLOUD_UEM_PRO_TENANT_ID":    "JAMFPLATFORM_JSC_UEM_PRO_TENANT_ID",
	"JAMFPLATFORM_ACC_PRO_USER_WRITE_OK":                  "JAMFPLATFORM_USER_WRITE_OK",
	"JAMFPLATFORM_ACC_PRO_VPP_INVITATION_ID":              "JAMFPLATFORM_VPP_INVITATION_ID",
	"JAMFPLATFORM_ACC_PRO_VPP_TOKEN":                      "JAMFPLATFORM_VPP_TOKEN",
	"JAMFPLATFORM_ACC_PRO_WIFI_PROFILE_ID":                "JAMFPLATFORM_WIFI_PROFILE_ID",

	// Deliberately NOT shimmed — do not add them back:
	// JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_ID  <- JAMFPLATFORM_CLIENT_ID
	// JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_SECRET  <- JAMFPLATFORM_CLIENT_SECRET
	// JAMFPLATFORM_ACC_PRO_TENANT_ID  <- JAMFPLATFORM_TENANT_ID
	//
	// Those three bare names are the published CONSUMER convention —
	// jamfplatform/doc.go uses them verbatim in the example a consumer copies —
	// so a fallback is the very borrowing the rename existed to stop. It would
	// point ~650 mutating tests at whatever tenant a consumer credential
	// sitting in the same shell happens to name, announced only by one
	// deduplicated log line. An unrenamed .env must fail its completeness check
	// and skip, which is loud and recoverable; silently mutating the wrong
	// tenant is neither.
}

// accLegacyEnvUsed remembers which legacy names this run fell back to, so the
// warning is emitted once per name rather than once per lookup.
var accLegacyEnvUsed sync.Map

// accEnv reads an acceptance variable, falling back to its pre-rename name.
//
// Dual-read rather than a hard cutover, deliberately: a stale .env carrying only
// the old names would otherwise not error but silently drop a credential set
// below its completeness check, and the suite would fall back to a different
// scope or skip the lane. That failure is invisible, which is the whole class of
// bug JAMFPLATFORM_ACC_REQUIRE exists to close. Remove this shim once the
// secrets and every local .env have moved.
//
// The Pro TENANT CREDENTIAL is the deliberate exception: its client ID, secret
// and tenant ID have no fallback, because their old names are the consumer
// convention doc.go publishes — see accLegacyEnvNames. A .env still carrying
// only those three loses the tenant lane to a skip (or, under
// JAMFPLATFORM_ACC_REQUIRE, to a failure) and must be renamed.
func accEnv(name string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	old, ok := accLegacyEnvNames[name]
	if !ok {
		return ""
	}
	v := os.Getenv(old)
	if v != "" {
		if _, seen := accLegacyEnvUsed.LoadOrStore(old, true); !seen {
			log.Printf("acceptance: %s is deprecated, rename it to %s", old, name)
		}
	}
	return v
}

// firstAccEnv returns the first of names that is set. It exists so a base-URL
// fallback chain is written once and read identically by the client that dials
// it and by the failure report that names it.
func firstAccEnv(names ...string) string {
	for _, name := range names {
		if v := accEnv(name); v != "" {
			return v
		}
	}
	return ""
}

// accEnvironmentBaseURL is the base URL an environment-scoped client dials:
// its own variable when set, the Pro tenant's otherwise, both products being
// served by the same regional gateway.
func accEnvironmentBaseURL() string {
	return firstAccEnv("JAMFPLATFORM_ACC_ENVIRONMENT_BASE_URL", "JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL")
}

// accOrgBaseURL is the base URL the organization-scoped client dials. The
// fallback is a convenience only: the Jamf Account api-product ships US
// api-definitions alone, so a non-US Pro base URL cannot serve it — which is
// exactly why the report must name this URL rather than the Pro one.
func accOrgBaseURL() string {
	return firstAccEnv("JAMFPLATFORM_ACC_ORGANIZATION_BASE_URL", "JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL")
}

// accEnvironmentSetComplete reports whether the environment credential set is
// fully configured. It is the condition initAcceptanceClient prefers
// environment scope on and initJSCAcceptanceClient falls back to, stated once
// so the base-URL resolvers below cannot drift from the branch they describe.
func accEnvironmentSetComplete() bool {
	return accEnvironmentBaseURL() != "" &&
		accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_ID") != "" &&
		accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_SECRET") != "" &&
		accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_ID") != ""
}

// accSecurityCloudSetComplete reports whether the dedicated Security Cloud
// credential set is fully configured. The base URL is not part of it: that one
// falls back to the Pro tenant's, the credentials do not.
func accSecurityCloudSetComplete() bool {
	return accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_ID") != "" &&
		accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_SECRET") != "" &&
		accEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_ID") != ""
}

// accPlatformBaseURL is the base URL initAcceptanceClient dials, mirroring its
// preference for environment scope over tenant scope.
func accPlatformBaseURL() string {
	if accEnvironmentSetComplete() {
		return accEnvironmentBaseURL()
	}
	return accEnv("JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL")
}

// accSecurityCloudBaseURL is the base URL initJSCAcceptanceClient dials,
// mirroring its preference for the dedicated Security Cloud credential over the
// environment one — the two can be on different regional gateways, so which
// credential was used decides which URL the rejected request went to.
func accSecurityCloudBaseURL() string {
	if accSecurityCloudSetComplete() {
		return firstAccEnv("JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_BASE_URL", "JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL")
	}
	return accEnvironmentBaseURL()
}

// accRequiredSets names the credential sets this run must have configured, read
// from JAMFPLATFORM_ACC_REQUIRE as a comma-separated list of
// platform | environment | organization | securitycloud.
//
// It exists because "unset credentials skip" is right locally and wrong in CI.
// A contributor with no tenant must be able to run `make testacc` and get skips
// rather than a wall of failures, so absence cannot be fatal by default. But in
// a pipeline that wires the secrets, absence means the secret is missing or
// misnamed — and a skip there is invisible: the package still prints `ok` and
// the check goes green having asserted nothing against the tenant. That is not
// hypothetical twice over. On 2026-08-04 a WAF block made all 146 scoped tests
// skip and the run reported success. And the organization set was wired in
// exactly zero places in .github/workflows/acceptance.yml from the day
// accOrgClient was written, so all ten Jamf Account tests skipped on every run
// for the whole life of the file without anyone seeing a red build.
//
// The unit is one set per lane rather than a single boolean, because the
// acceptance matrix runs lanes that legitimately need different credentials: the
// Pro lane must not fail for a missing organization secret it never uses.
var accRequiredSets = sync.OnceValue(func() map[string]bool {
	required := map[string]bool{}
	for name := range strings.SplitSeq(accEnv("JAMFPLATFORM_ACC_REQUIRE"), ",") {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			required[name] = true
		}
	}
	return required
})

// skipOrFailUnset ends the test for an unconfigured credential set: fatally when
// this run declared the set required, otherwise as a skip. Call it only for a
// genuinely-unset set — credentials that were supplied and refused must always
// fail, which is the distinction credentialRejectedMessage carries.
func skipOrFailUnset(t *testing.T, set string, err error) {
	t.Helper()
	if accRequiredSets()[set] {
		t.Fatalf("JAMFPLATFORM_ACC_REQUIRE names %q but its credentials are not configured, so these tests would silently skip: %v", set, err)
	}
	t.Skipf("Skipping %s acceptance test: %v", set, err)
}

// credentialRejectedMessage builds the report to hand Jamf Support when the tenant
// refuses credentials that were supplied, mirroring what
// terraform-provider-jamfprotect emits for the same failure. The first question
// support asks about a blocked request is always "from which IP, and when", and on
// a CI runner nobody can go and check afterwards — the egress IP is gone with the
// runner. Capturing it in the failure output is the only chance to have it.
//
// The base URL is passed in rather than read from the client, which was never
// successfully constructed — and it is passed in per credential set rather than
// read from one variable, because the sets do not share a gateway: an
// organization failure is always on the US gateway, an environment or Security
// Cloud one can be on any region. Naming the Pro tenant's URL for all of them
// sent support a URL the failing request never used. In CI it renders as ***
// because it is a secret; that is fine, whoever opens the ticket has it.
func credentialRejectedMessage(baseURL string, err error) string {
	ip := egressIP()
	if ip == "" {
		ip = "(unable to determine — run `curl -s https://checkip.amazonaws.com` on this host)"
	}
	return fmt.Sprintf(`acceptance credentials were supplied but the tenant rejected them.
Failing rather than skipping: a skipped suite reports success while verifying nothing.

If this is an edge or WAF block rather than a bad secret, give Jamf Support:
  - Timestamp:    %s
  - Instance URL: %s
  - Egress IP:    %s

Technical details: %v`,
		time.Now().UTC().Format(time.RFC3339),
		baseURL,
		ip,
		err)
}

// accClient returns a live Jamf Platform API client. It skips the test when no
// credentials are configured, and FAILS when credentials are configured but the
// tenant rejected them — see errAccCredsUnset for why that distinction matters.
func accClient(t *testing.T) *jamfplatform.Client {
	t.Helper()
	c, err := initAcceptanceClient()
	switch {
	case errors.Is(err, errAccCredsUnset):
		skipOrFailUnset(t, "platform", err)
	case err != nil:
		t.Fatal(credentialRejectedMessage(accPlatformBaseURL(), err))
	}
	logAccScopeOnce()
	return c
}

// logAccScopeOnce announces the scope the suite is running under, once per run.
// Which scope is active changes what data every accClient test sees, so a run
// that does not say so leaves a scope switch looking like the tenant's data
// having changed.
var logAccScopeOnce = sync.OnceFunc(func() {
	fmt.Fprintf(os.Stderr, "acceptance: running under %s scope\n", accScopeInUse)
})

// skipOnServerError skips the test if err is an API 5xx response.
// Use instead of t.Fatalf for API calls that may hit transient server bugs.
func skipOnServerError(t *testing.T, err error) {
	t.Helper()
	var apiErr *jamfplatform.APIResponseError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 500 {
		t.Skipf("Skipping due to server error: %v", err)
	}
}

// cleanupDelete registers a best-effort delete in t.Cleanup. Unlike
// `_ = pc.DeleteXxx(...)`, it surfaces delete failures via t.Logf so a
// server-side 5xx on cleanup doesn't silently leak a tenant record.
// Errors are intentionally non-fatal: one failed cleanup must not
// prevent other registered Cleanup hooks from running.
func cleanupDelete(t *testing.T, label string, fn func() error) {
	t.Helper()
	t.Cleanup(func() {
		if err := fn(); err != nil {
			t.Logf("cleanup %s: %v", label, err)
		}
	})
}

// Smart group fixture — shared across all tests that need a device group scope.

var smartGroupFixtureOnce sync.Once
var smartGroupID string
var smartGroupErr error

func smartGroupFixtureName() string {
	return "sdk-acc-fixture-" + runSuffix()
}

func requireSmartGroupFixture(t *testing.T) string {
	t.Helper()

	smartGroupFixtureOnce.Do(func() {
		// If a device group ID is provided via env var, use it directly.
		// This is useful when the device groups API is not available with the
		// current credentials (e.g. tenant-scoped credentials for blueprints/benchmarks).
		if id := accEnv("JAMFPLATFORM_ACC_PRO_DEVICE_GROUP_ID"); id != "" {
			smartGroupID = id
			return
		}

		c := accClient(t)
		ctx := context.Background()
		dg := devicegroups.New(c)

		groups, err := dg.ListDeviceGroups(ctx, nil, fmt.Sprintf("name==%q", smartGroupFixtureName()))
		if err != nil {
			smartGroupErr = fmt.Errorf("failed to look up fixture smart group: %w", err)
			return
		}
		for _, g := range groups {
			if g.Name == smartGroupFixtureName() {
				smartGroupID = g.ID
				return
			}
		}

		fixtureDesc := "SDK acceptance test fixture — safe to delete"
		resp, err := dg.CreateDeviceGroup(ctx, &devicegroups.DeviceGroupCreateRepresentationV1{
			Name:        smartGroupFixtureName(),
			Description: &fixtureDesc,
			DeviceType:  "COMPUTER",
			GroupType:   "SMART",
			Criteria: &[]devicegroups.DeviceGroupCriteriaRepresentationV1{
				{
					Order:          0,
					AttributeName:  "Serial Number",
					Operator:       "LIKE",
					AttributeValue: "",
					JoinType:       "AND",
				},
			},
		})
		if err != nil {
			smartGroupErr = fmt.Errorf("failed to create fixture smart group: %w", err)
			return
		}
		smartGroupID = resp.ID
	})

	if smartGroupErr != nil {
		t.Fatalf("Smart group fixture failed: %v", smartGroupErr)
	}
	return smartGroupID
}

// cleanupSmartGroupFixture deletes the shared fixture. Call from TestMain.
// Skips cleanup when the group was provided via JAMFPLATFORM_ACC_PRO_DEVICE_GROUP_ID
// since we don't own that resource.
func cleanupSmartGroupFixture() {
	if smartGroupID == "" || accEnv("JAMFPLATFORM_ACC_PRO_DEVICE_GROUP_ID") != "" {
		return
	}
	if c, err := initAcceptanceClient(); err == nil {
		_ = devicegroups.New(c).DeleteDeviceGroup(context.Background(), smartGroupID)
	}
}

// Benchmark cleanup helpers — handle async sync states and stuck DELETING.

func ensureBenchmarkDeletedByID(t *testing.T, c *jamfplatform.Client, ctx context.Context, benchmarkID string) {
	t.Helper()
	cb := compliancebenchmarks.New(c)
	waitForBenchmarkSyncState(t, c, ctx, benchmarkID)

	if err := cb.DeleteBenchmark(ctx, benchmarkID); err != nil {
		t.Logf("Warning: failed to delete benchmark %s: %v", benchmarkID, err)
		return
	}
	t.Logf("Delete issued for benchmark %s", benchmarkID)

	deleteCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	lastDelete := time.Now()
	err := jamfplatform.PollUntil(deleteCtx, 2*time.Second, func(_ context.Context) (bool, error) {
		if _, found := benchmarkSyncState(c, ctx, benchmarkID); !found {
			t.Logf("Benchmark %s fully deleted", benchmarkID)
			return true, nil
		}
		if time.Since(lastDelete) > 20*time.Second {
			lastDelete = time.Now()
			t.Logf("Retrying delete for stuck benchmark %s", benchmarkID)
			_ = cb.DeleteBenchmark(ctx, benchmarkID)
		}
		return false, nil
	})
	if err != nil {
		t.Logf("Warning: benchmark %s still present after 2m", benchmarkID)
	}
}

func waitForBenchmarkSyncState(t *testing.T, c *jamfplatform.Client, ctx context.Context, benchmarkID string) {
	t.Helper()
	syncCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	err := jamfplatform.PollUntil(syncCtx, 3*time.Second, func(_ context.Context) (bool, error) {
		state, found := benchmarkSyncState(c, ctx, benchmarkID)
		if !found {
			t.Logf("Benchmark %s not found, may already be deleted", benchmarkID)
			return true, nil
		}
		if state == "SYNCED" || state == "FAILED" {
			t.Logf("Benchmark %s reached state %s", benchmarkID, state)
			return true, nil
		}
		t.Logf("Benchmark %s in state %q, waiting for SYNCED", benchmarkID, state)
		return false, nil
	})
	if err != nil {
		t.Logf("Warning: benchmark %s did not reach SYNCED after 2m", benchmarkID)
	}
}

func benchmarkSyncState(c *jamfplatform.Client, ctx context.Context, benchmarkID string) (string, bool) {
	cb := compliancebenchmarks.New(c)
	benchmarks, err := cb.ListBenchmarks(ctx)
	if err != nil {
		return "", false
	}
	for _, b := range benchmarks.Benchmarks {
		if b.ID == benchmarkID {
			return b.SyncState, true
		}
	}
	return "", false
}

// ---------------------------------------------------------------------------
// List-response shape
// ---------------------------------------------------------------------------

// listBodyShape names which of the two list-response shapes a raw body is,
// deciding on the first non-space byte rather than decoding: "envelope" for
// the {totalCount, results} object the specs declare, "array" for a bare JSON
// array. Anything else is reported verbatim so the failure message carries it.
func listBodyShape(body []byte) string {
	trimmed := bytes.TrimSpace(body)
	switch {
	case len(trimmed) == 0:
		return "empty"
	case trimmed[0] == '{':
		return "envelope"
	case trimmed[0] == '[':
		return "array"
	default:
		return fmt.Sprintf("neither (starts %q)", string(trimmed[0]))
	}
}

// assertListBodyShape fetches a list endpoint outside the generated methods and
// asserts which shape it answers with.
//
// It exists because client.UnwrapResults deliberately accepts both. The four
// account list endpoints served a bare array until 2026-09-01 and the envelope
// their specs had declared all along after, which broke every caller with
// `json: cannot unmarshal object into Go value of type []account.License` on
// every call — and no unit test could see it, since the generated httptest
// stub serves whatever the method assumed. Accepting both shapes removes that
// outage but also removes the only signal that the server moved, so the signal
// lives here instead: this pins what the wire sends today, on the one layer
// that actually talks to the wire.
//
// A mismatch is an Errorf, not a Fatalf. The SDK still decodes correctly — the
// finding is that the server changed and WIRE-FACTS is now stale, which is
// worth a red test and is not worth stopping the run.
func assertListBodyShape(t *testing.T, c *jamfplatform.Client, namespace, version, path, want string) {
	t.Helper()

	tr := c.Transport()
	endpoint := tr.APIPrefix(namespace, version) + path

	var raw json.RawMessage
	if err := tr.Do(context.Background(), http.MethodGet, endpoint, nil, &raw); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GET %s: %v", endpoint, err)
	}

	got := listBodyShape(raw)
	t.Logf("GET %s answers the %s shape", endpoint, got)
	if got != want {
		t.Errorf(`GET %s answers the %s shape, want %s.

The SDK still decodes this correctly — client.UnwrapResults accepts both — so
nothing is broken for consumers and no code change is needed. What is needed:
record the new shape in docs/WIRE-FACTS.md and update the want here, since this
assertion is the only thing that dates the change.`, endpoint, got, want)
	}
}

// Read-after-write staleness on group resources.
//
// Jamf Pro serves Classic and Pro *group* reads from behind something that
// lags its own writes: a GET immediately after a POST can 404, and a GET
// immediately after a DELETE can return 200 with the full record — while a
// second DELETE on that same id returns 404, proving the object really is
// gone. Measured on 11.31.1 (EU), the lag clears on the *next* attempt every
// time: 2 attempts, 167-234ms.
//
// Spacing requests does not fix it and must not be used as the remedy. The
// transport already paces requests 100ms apart by default
// (defaultMinRequestInterval), and raising that to 250ms and 500ms still
// produced stale reads — the staleness is intermittent, not a fixed latency,
// so there is no interval that makes a bare assertion reliable. Retrying the
// read is what converges.
//
// These helpers therefore poll rather than sleep, and they still assert the
// real outcome: a timeout fails the test. They are deliberately narrow — use
// them only for the first read that follows a write on a resource with this
// behaviour, never to paper over an endpoint that is simply broken.
const (
	settleTimeout  = 15 * time.Second
	settlePollWait = 100 * time.Millisecond
)

// settleUntilFound polls read until it succeeds, tolerating the post-write
// staleness described above. Fails the test if the read never succeeds, and
// returns immediately on a 5xx via skipOnServerError.
func settleUntilFound(t *testing.T, label string, read func() error) {
	t.Helper()
	deadline := time.Now().Add(settleTimeout)
	attempts := 0
	for {
		attempts++
		err := read()
		if err == nil {
			if attempts > 1 {
				t.Logf("%s: read settled after %d attempts", label, attempts)
			}
			return
		}
		skipOnServerError(t, err)
		if apiErr := jamfplatform.AsAPIError(err); apiErr == nil || !apiErr.HasStatus(404) {
			t.Fatalf("%s: %v", label, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: still 404 after %s and %d attempts", label, settleTimeout, attempts)
		}
		time.Sleep(settlePollWait)
	}
}

// settleUntilGone polls read until it reports 404, the mirror of
// settleUntilFound for the read that follows a delete. A non-404 error fails
// immediately: only staleness is tolerated, not an unexpected status.
func settleUntilGone(t *testing.T, label string, read func() error) {
	t.Helper()
	deadline := time.Now().Add(settleTimeout)
	attempts := 0
	for {
		attempts++
		err := read()
		if err != nil {
			skipOnServerError(t, err)
			apiErr := jamfplatform.AsAPIError(err)
			if apiErr == nil || !apiErr.HasStatus(404) {
				t.Fatalf("%s: want 404, got %v", label, err)
			}
			if attempts > 1 {
				t.Logf("%s: read settled after %d attempts", label, attempts)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: still readable %s after delete (%d attempts) — not staleness", label, settleTimeout, attempts)
		}
		time.Sleep(settlePollWait)
	}
}

// proServerVersion returns the Jamf Pro server's major and minor version.
//
// For a test whose expected behaviour is version-gated, which happens because
// the SDK generates from a spec that can be ahead of the server it is called
// against: a property the spec declares can be rejected outright by a tenant
// that has not rolled forward. The CI matrix holds tenants at more than one
// version at a time, so a test asserting one side of that unconditionally fails
// on the other — and the failure looks like a defect rather than a rollout.
//
// Only the first two components are compared, since Jamf ships API surface on
// minors. Version strings carry a build suffix ("11.32.0-t1787580540993"), which
// is cut before parsing.
func proServerVersion(t *testing.T, c *jamfplatform.Client) (major, minor int) {
	t.Helper()

	v, err := pro.New(c).GetJamfProVersionV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetJamfProVersionV1: %v", err)
	}

	base, _, _ := strings.Cut(v.Version, "-")
	parts := strings.Split(base, ".")
	if len(parts) < 2 {
		t.Fatalf("GetJamfProVersionV1: cannot parse a major.minor from %q", v.Version)
	}
	major, err = strconv.Atoi(parts[0])
	if err != nil {
		t.Fatalf("GetJamfProVersionV1: major of %q: %v", v.Version, err)
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("GetJamfProVersionV1: minor of %q: %v", v.Version, err)
	}
	return major, minor
}

// proServerAtLeast reports whether the Jamf Pro server is at or past the given
// major.minor.
func proServerAtLeast(t *testing.T, c *jamfplatform.Client, major, minor int) bool {
	t.Helper()
	haveMajor, haveMinor := proServerVersion(t, c)
	return haveMajor > major || (haveMajor == major && haveMinor >= minor)
}
