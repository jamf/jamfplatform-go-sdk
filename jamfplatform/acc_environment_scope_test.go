// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/blueprints"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/devices"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// Environment-scope coverage.
//
// A Jamf integration is minted against one scope — a tenant or a platform
// environment — and the request header must match it. WithEnvironmentID sends
// X-Environment-Id where WithTenantID sends X-Tenant-Id, and the gateway refuses
// a mismatch with 403 OWNERSHIP_FORBIDDEN even when both IDs belong to the same
// customer. That refusal is the half worth pinning: it is what stops a consumer
// treating the two options as interchangeable spellings.
//
// Everything here is read-only. Environment scope is about which credential
// reaches an API, not what it may do, so nothing is gained by mutating.
//
// Security Cloud is deliberately absent: that package does not exist on main.
// It was where this behaviour was first wire-verified, and its read coverage
// belongs in whichever branch carries the securitycloud package.

// TestAcceptance_EnvironmentScope drives read-only operations across several
// namespaces with an environment-scoped credential.
//
// Every spec this SDK generates declares X-Tenant-Id, so no operation *requires*
// environment scope — this proves the gateway serves the same surface to an
// environment-scoped caller, which is what a consumer holding only such a
// credential depends on. Wire-verified on securitycloud in prod 2026-08-25
// before the option existed.
func TestAcceptance_EnvironmentScope(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()

	t.Run("blueprints reads", func(t *testing.T) {
		if bps, err := blueprints.New(c).ListBlueprints(ctx, nil, ""); err != nil {
			logScopeOutcome(t, "ListBlueprints", err)
		} else {
			t.Logf("blueprints: %d", len(bps))
		}
	})

	t.Run("devices reads", func(t *testing.T) {
		if d, err := devices.New(c).ListDevices(ctx, nil, ""); err != nil {
			logScopeOutcome(t, "ListDevices", err)
		} else {
			t.Logf("devices: %d", len(d))
		}
	})

	// Pro and Classic are reached with the same client. A 403 here is a
	// meaningful negative, not a gap in the SDK: it means the environment
	// credential is not entitled to that product, which is a permissions
	// question rather than a scoping one. It is logged rather than failed so
	// this test reports the shape of the entitlement instead of demanding one.
	t.Run("pro reads", func(t *testing.T) {
		p := pro.New(c)
		if u, err := p.GetJamfProServerURLV1(ctx); err != nil {
			logScopeOutcome(t, "GetJamfProServerURLV1", err)
		} else {
			t.Logf("jamf-pro-server-url = %q", u.URL)
		}
		if cats, err := p.ListCategoriesV1(ctx, nil, ""); err != nil {
			logScopeOutcome(t, "ListCategoriesV1", err)
		} else {
			t.Logf("pro categories: %d", len(cats))
		}
		if b, err := p.ListBuildingsV1(ctx, nil, ""); err != nil {
			logScopeOutcome(t, "ListBuildingsV1", err)
		} else {
			t.Logf("pro buildings: %d", len(b))
		}
	})

	t.Run("proclassic reads", func(t *testing.T) {
		pc := proclassic.New(c)
		if sites, err := pc.ListSites(ctx); err != nil {
			logScopeOutcome(t, "ListSites", err)
		} else {
			t.Logf("classic sites: %+v", sites != nil)
		}
	})
}

// TestAcceptance_EnvironmentScopeMismatch pins the rule that makes
// WithEnvironmentID and WithTenantID alternatives rather than aliases: the
// header must agree with the credential.
//
// Read-only, and it cannot mutate anything — every request is refused. It needs
// both credential sets, because the whole point is to cross them over.
func TestAcceptance_EnvironmentScopeMismatch(t *testing.T) {
	envID := accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_ID")
	tenantID := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_ID")
	if envID == "" || tenantID == "" {
		t.Skip("needs both JAMFPLATFORM_ACC_ENVIRONMENT_ID and JAMFPLATFORM_ACC_PRO_TENANT_ID to cross the scopes over")
	}
	ctx := context.Background()

	// An environment-scoped credential told to send a tenant header.
	t.Run("environment credential, tenant header", func(t *testing.T) {
		baseURL := accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_BASE_URL")
		if baseURL == "" {
			baseURL = accEnv("JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL")
		}
		id, secret := accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_ID"), accEnv("JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_SECRET")
		if baseURL == "" || id == "" || secret == "" {
			t.Skip("environment-scoped credentials not configured")
		}
		c := jamfplatform.NewClient(baseURL, id, secret, jamfplatform.WithTenantID(tenantID))
		_, err := pro.New(c).GetJamfProServerURLV1(ctx)
		assertScopeMismatch(t, err, "environment credential sending X-Tenant-Id")
	})

	// And the reverse: a tenant-scoped credential told to send an environment
	// header, using the main tenant credential set.
	t.Run("tenant credential, environment header", func(t *testing.T) {
		baseURL, id, secret := accEnv("JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL"), accEnv("JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_ID"), accEnv("JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_SECRET")
		if baseURL == "" || id == "" || secret == "" {
			t.Skip("tenant credentials not configured")
		}
		c := jamfplatform.NewClient(baseURL, id, secret, jamfplatform.WithEnvironmentID(envID))
		_, err := pro.New(c).GetJamfProServerURLV1(ctx)
		assertScopeMismatch(t, err, "tenant credential sending X-Environment-Id")
	})
}

// assertScopeMismatch requires a crossed-over scope to be refused, and to be
// refused as an ownership problem rather than an authentication one. The
// distinction matters for diagnostics: 403 OWNERSHIP_FORBIDDEN says "that ID is
// not yours", whereas a plain-text 401 means the gateway rejected the token
// before any context was resolved — a different bug with a different fix.
func assertScopeMismatch(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s succeeded — the header no longer has to agree with the credential, so WithEnvironmentID and WithTenantID are now interchangeable and their docs are wrong", what)
	}
	apiErr := jamfplatform.AsAPIError(err)
	if apiErr == nil {
		t.Fatalf("%s: want an API error, got %v", what, err)
	}
	if !apiErr.HasStatus(403) {
		t.Errorf("%s: want 403, got %d (%s)", what, apiErr.StatusCode, apiErr.Summary())
		return
	}
	t.Logf("%s -> %s", what, apiErr.Summary())
}

// logScopeOutcome records why a namespace was unreachable under environment
// scope without failing: a 403 is an entitlement fact about the credential, not
// an SDK defect. A non-403 is surfaced loudly because it is not explained by
// entitlement.
func logScopeOutcome(t *testing.T, op string, err error) {
	t.Helper()
	apiErr := jamfplatform.AsAPIError(err)
	switch {
	case apiErr == nil:
		t.Errorf("%s: non-API error under environment scope: %v", op, err)
	case apiErr.HasStatus(403):
		t.Logf("%s: 403 — this environment credential is not entitled to that product (%s)", op, apiErr.Summary())
	case apiErr.HasStatus(401):
		t.Errorf("%s: 401 — the gateway rejected the token before resolving context, which is not an entitlement problem: %s", op, apiErr.Summary())
	default:
		t.Errorf("%s: unexpected %d under environment scope: %s", op, apiErr.StatusCode, apiErr.Summary())
	}
}
