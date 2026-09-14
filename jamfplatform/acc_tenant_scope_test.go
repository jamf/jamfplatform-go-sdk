// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

// Tenant-scope coverage — the mirror of acc_environment_scope_test.go.
//
// # Why this exists
//
// accClient prefers environment scope whenever that credential set is complete,
// which is correct: environment is the scope Jamf intends new integrations to
// use, and tenant is legacy. But the consequence is that once the environment
// secrets are configured, NOTHING in the suite sends X-Tenant-Id any more.
// WithTenantID stays public, stays supported, and is what
// terraform-provider-jamfplatform uses — so it would be a live consumer surface
// with no CI coverage, which is exactly how a legacy path breaks unnoticed.
//
// So this lane pins the scope rather than the data: accTenantClient forces
// tenant scope with no environment fallback, and the assertions below check that
// the gateway accepts X-Tenant-Id across the namespaces the SDK reaches with it.
//
// Everything here is read-only. Tenant scope is about which credential reaches
// an API, not what it may do, so nothing is gained by mutating — and this lane
// generally points at a different tenant from the pro lane, where a write would
// leave litter nobody is looking after.

import (
	"context"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/ddmreport"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/deviceactions"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/devicegroups"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/devices"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/pro"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// TestAcceptance_TenantScope drives read-only operations with a credential
// pinned to tenant scope.
//
// Three of the four assertions are strict, because a tenant credential that
// cannot reach pro, Classic or devices is not an entitlement quirk — it means
// X-Tenant-Id stopped working, which is the whole point of the lane. Entitlement
// differences between products are logged instead, the same way the
// environment-scope test treats them: a 403 there says this credential is not
// granted that product, which is a permissions question rather than a scoping
// one.
func TestAcceptance_TenantScope(t *testing.T) {
	c := accTenantClient(t)
	ctx := context.Background()

	// Assert the scope the client actually settled on. Without this the test
	// could pass having silently been built from some other credential set —
	// the failure mode this lane was created to rule out.
	kind, id := c.Scope()
	if kind != jamfplatform.ScopeTenant {
		t.Fatalf("client scope is %v (id %q), want ScopeTenant — accTenantClient must never fall back to another scope", kind, id)
	}
	if id == "" {
		t.Fatal("tenant scope reported an empty ID, so no X-Tenant-Id would be sent")
	}
	t.Logf("tenant scope in use: %s", id)

	t.Run("pro reads", func(t *testing.T) {
		p := pro.New(c)
		v, err := p.GetJamfProVersionV1(ctx)
		if err != nil {
			t.Fatalf("GetJamfProVersionV1 under tenant scope: %v", err)
		}
		t.Logf("jamf-pro-version = %q", v.Version)

		if cats, err := p.ListCategoriesV1(ctx, nil, ""); err != nil {
			t.Errorf("ListCategoriesV1 under tenant scope: %v", err)
		} else {
			t.Logf("pro categories: %d", len(cats))
		}
	})

	// Classic shares the client and the header. It is included because
	// proclassic is XML end to end and reaches the gateway on a different
	// namespace, so a scoping regression could plausibly hit one and not the
	// other.
	t.Run("proclassic reads", func(t *testing.T) {
		if cats, err := proclassic.New(c).ListCategories(ctx); err != nil {
			t.Errorf("ListCategories under tenant scope: %v", err)
		} else {
			t.Logf("classic categories: %d", len(cats.Categories))
		}
	})

	// A Platform namespace rather than a Jamf Pro one: tenant scope has to work
	// across both, and devices is the one this credential is reliably granted.
	t.Run("devices reads", func(t *testing.T) {
		if d, err := devices.New(c).ListDevices(ctx, nil, ""); err != nil {
			t.Errorf("ListDevices under tenant scope: %v", err)
		} else {
			t.Logf("devices: %d", len(d))
		}
	})
}

// TestAcceptance_TenantScopePlatformSpecsStillServed pins a spec/wire disagreement
// that v2082 opened, and it is written to fail the day the wire catches up.
//
// v2082 moved six Platform specs from tenant to environment scope — blueprints,
// device-groups, devices, device-management-action, declaration-reporting and
// compliance-benchmarks (two upstream spec changes, on the grounds that
// "Platform endpoints are environment-scoped only"). Each now declares
// x-scope-types: [environment] with X-Environment-Id required and no
// X-Tenant-Id parameter at all.
//
// The gateway has not followed. Probed 2026-09-04 with a tenant credential,
// with GET /pro/v1/jamf-pro-version at 200 as the control in the same
// invocation and a bogus path in the same namespace returning the unrouted
// 403 BAD_PERMISSIONS: devices, device-groups, declaration-reporting and
// device-management-action all answer under X-Tenant-Id. So a caller migrating
// off tenant scope because the spec told them to is acting on a claim the
// server does not yet make, and a caller who does not migrate is not yet
// broken.
//
// The four below are asserted, not logged. A 403 here means the withdrawal has
// landed, which is a breaking change for every tenant-scoped consumer of these
// packages and must not pass silently — when it fires, flip this test to assert
// the refusal and say so in CLAUDE.md's package table.
//
// blueprints and compliance-benchmarks are deliberately absent. Both refused
// the tenant credential with 403 BAD_PERMISSIONS on the same run, and with one
// tenant credential that cannot be told apart from an ungranted capability —
// classifying a 403 needs two credentials, not two paths. It agrees with the
// separately recorded GA decision that those two are environment-only, so they
// are left unpinned rather than pinned on a guess.
func TestAcceptance_TenantScopePlatformSpecsStillServed(t *testing.T) {
	c := accTenantClient(t)
	ctx := context.Background()

	// The control. Without it a blanket 403 across the subtests reads as a
	// scope withdrawal when it is really a dead or unentitled credential.
	if _, err := pro.New(c).GetJamfProVersionV1(ctx); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("control GET /pro/v1/jamf-pro-version under tenant scope: %v", err)
	}

	t.Run("devices", func(t *testing.T) {
		d, err := devices.New(c).ListDevices(ctx, nil, "")
		assertTenantStillServed(t, "ListDevices", err)
		t.Logf("devices under tenant scope: %d", len(d))
	})

	t.Run("device-groups", func(t *testing.T) {
		g, err := devicegroups.New(c).ListDeviceGroups(ctx, nil, "")
		assertTenantStillServed(t, "ListDeviceGroups", err)
		t.Logf("device groups under tenant scope: %d", len(g))
	})

	// A device id that cannot exist: this proves the namespace is routed and
	// the credential accepted without needing a device, since a refusal at the
	// gateway is a 403 and a refusal at the service is a 404.
	t.Run("declaration-reporting", func(t *testing.T) {
		_, err := ddmreport.New(c).GetDeviceDeclarationReportFiltered(ctx, noSuchDeviceID, "", nil)
		assertTenantStillServedPastGateway(t, "GetDeviceDeclarationReportFiltered", err)
	})

	// Likewise, and it mutates nothing: check-in against a nonexistent device
	// is a no-op the service answers 404 for.
	t.Run("device-management-action", func(t *testing.T) {
		err := deviceactions.New(c).CheckInDevice(ctx, noSuchDeviceID)
		assertTenantStillServedPastGateway(t, "CheckInDevice", err)
	})
}

// noSuchDeviceID is a well-formed UUID no tenant can hold, so an operation
// taking it reaches the service and returns without touching anything.
const noSuchDeviceID = "00000000-0000-0000-0000-000000000000"

// assertTenantStillServed fails when a Platform read is refused under tenant
// scope. 403 gets its own message because it is the one that means the spec's
// withdrawal has reached the gateway.
func assertTenantStillServed(t *testing.T, op string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	skipOnServerError(t, err)
	if apiErr := jamfplatform.AsAPIError(err); apiErr != nil && apiErr.HasStatus(403) {
		t.Fatalf("%s: 403 under tenant scope — v2082's environment-only declaration has reached the gateway. "+
			"This is a breaking change for tenant-scoped consumers: flip this test to assert the refusal, "+
			"and update the scope column in CLAUDE.md's package table. (%s)", op, apiErr.Summary())
	}
	t.Fatalf("%s under tenant scope: %v", op, err)
}

// assertTenantStillServedPastGateway is assertTenantStillServed for an
// operation probed with an impossible id, where the service's own 404 is the
// success condition and 403 still means unrouted or refused.
func assertTenantStillServedPastGateway(t *testing.T, op string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: an impossible device id succeeded — the probe proves nothing", op)
	}
	skipOnServerError(t, err)
	apiErr := jamfplatform.AsAPIError(err)
	switch {
	case apiErr == nil:
		t.Fatalf("%s: non-API error under tenant scope: %v", op, err)
	case apiErr.HasStatus(404), apiErr.HasStatus(400):
		// Reached the service. 400 is the shape this endpoint takes when it
		// wants a filter it was not given, which is equally past the gateway.
		t.Logf("%s: %d under tenant scope — routed and accepted (%s)", op, apiErr.StatusCode, apiErr.Summary())
	case apiErr.HasStatus(403):
		t.Fatalf("%s: 403 under tenant scope — v2082's environment-only declaration has reached the gateway. "+
			"This is a breaking change for tenant-scoped consumers: flip this test to assert the refusal, "+
			"and update the scope column in CLAUDE.md's package table. (%s)", op, apiErr.Summary())
	default:
		t.Fatalf("%s: unexpected %d under tenant scope: %s", op, apiErr.StatusCode, apiErr.Summary())
	}
}
