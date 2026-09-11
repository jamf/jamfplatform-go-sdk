// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/pro"
)

// Four pro operations that are correct as generated and structurally refused on
// a Jamf Cloud tenant. Each test pins the *refusal*, and each is written to fail
// the day it lifts — at which point replace it with the real assertion rather
// than deleting it.
//
// All four were classified on 2026-08-31 against eu.api.jamfcloud.com with a
// known-good control (GET /pro/v1/jamf-pro-version) in the same invocation.
//
// The two gateway cases are distinguishable from a privilege denial by response
// shape: the gateway emits compact JSON carrying a traceId and
// errors[].code == BAD_PERMISSIONS, byte-for-byte the same as a deliberately
// bogus path (GET /pro/v1/zzz-not-a-real-endpoint), whereas Jamf Pro's own
// responses are pretty-printed. Both were additionally shown *not* to be
// privilege denials by exercising a different, already-shipping operation that
// requires the same privilege and succeeds — see each test.

// gatewayUnrouted reports whether err is the gateway refusing to route a path
// at all, as opposed to Jamf Pro denying an authenticated request.
func gatewayUnrouted(t *testing.T, method string, err error) bool {
	t.Helper()
	if err == nil {
		return false
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("%s: non-API error, the request did not reach the gateway: %v", method, err)
	}
	if !apiErr.HasStatus(403) {
		t.Fatalf("%s: want the gateway's 403 BAD_PERMISSIONS, got status %d: %v", method, apiErr.StatusCode, err)
	}
	for _, d := range apiErr.Details() {
		if d.Code == "BAD_PERMISSIONS" {
			t.Logf("%s: still unrouted at the gateway (403 BAD_PERMISSIONS)", method)
			return true
		}
	}
	t.Fatalf("%s: 403 but not BAD_PERMISSIONS, so the failure is not the documented one: %v", method, err)
	return false
}

// TestAcceptance_Pro_DssDeclarationsUnroutedAtGateway pins
// GET /v1/dss-declarations/{declarationId}.
//
// routes.yaml declares it with declarations:read, and this credential holds that
// privilege — the ddmreport operations that require the same string
// (ListDeclarationReportClients, GetDeviceDeclarationReport) both answer 200 for
// it. So the 403 is the path, not the grant.
func TestAcceptance_Pro_DssDeclarationsUnroutedAtGateway(t *testing.T) {
	c := accClient(t)

	_, err := pro.New(c).GetDssDeclarationsV1(context.Background(), "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatal("GetDssDeclarationsV1 now answers — the gateway has started routing " +
			"GET /pro/v1/dss-declarations/{declarationId}. Replace this test with real coverage: " +
			"list declarations via the ddmreport package, then assert the returned Declarations payload.")
	}
	skipOnServerError(t, err)
	if !gatewayUnrouted(t, "GetDssDeclarationsV1", err) {
		t.Fatalf("GetDssDeclarationsV1 failed for an unexpected reason: %v", err)
	}
}

// TestAcceptance_Pro_JamfProServerURLHistoryNoteRefusedOnHostedInstance pins
// POST /v1/jamf-pro-server-url/history.
//
// Renamed from ...UnroutedAtGateway on 2026-09-03, because the old name and the
// old assertion were both wrong about WHERE the refusal happens. The gateway
// routes this path fine; Jamf Pro itself refuses it, with
// 403 [HOSTED_ENVIRONMENT] Forbidden — which makes sense, since the server URL of
// a Jamf-hosted instance is not the customer's to change, and the history note is
// part of that surface.
//
// Wire-verified on two instances in different regions, on different tenants, with
// different credentials and different scope kinds (tenant and environment): both
// answer HOSTED_ENVIRONMENT, neither has ever answered BAD_PERMISSIONS. The old
// test asserted the gateway's BAD_PERMISSIONS via gatewayUnrouted, so it failed
// with "403 but not BAD_PERMISSIONS" the moment it met a real instance.
//
// GET on the very same path is routed and answers 200 (see
// TestAcceptance_Pro_Settings_JamfProServerURLHistoryV1), so reads are allowed
// and only the write is refused. This is therefore a hosting restriction rather
// than a gateway gap, and it will not lift by adding a privilege: the privilege
// is jss-url:update and this credential holds it — PUT /pro/v1/jamf-pro-server-url
// reaches Jamf Pro and returns 415 for an unsupported media type, which could not
// happen if the request had been refused before Pro saw it.
func TestAcceptance_Pro_JamfProServerURLHistoryNoteRefusedOnHostedInstance(t *testing.T) {
	c := accClient(t)

	_, err := pro.New(c).CreateJamfProServerURLHistoryNoteV1(context.Background(), &pro.ObjectHistoryNote{
		Note: "sdk-acc jamf-pro-server-url history probe",
	})
	if err == nil {
		t.Fatal("CreateJamfProServerURLHistoryNoteV1 now answers — this instance permits writing the " +
			"server-URL history, so it is either self-hosted or the restriction has been lifted. Replace " +
			"this test with a real round-trip: create the note, then find it via ListJamfProServerURLHistoryV1.")
	}
	skipOnServerError(t, err)

	apiErr := jamfplatform.AsAPIError(err)
	if apiErr == nil {
		t.Fatalf("CreateJamfProServerURLHistoryNoteV1: non-API error, the request did not reach the gateway: %v", err)
	}
	if !apiErr.HasStatus(403) {
		t.Fatalf("CreateJamfProServerURLHistoryNoteV1: want 403 HOSTED_ENVIRONMENT, got status %d: %v", apiErr.StatusCode, err)
	}
	for _, d := range apiErr.Details() {
		switch d.Code {
		case "HOSTED_ENVIRONMENT":
			t.Log("CreateJamfProServerURLHistoryNoteV1: still refused by Jamf Pro on a hosted instance (403 HOSTED_ENVIRONMENT)")
			return
		case "BAD_PERMISSIONS":
			// Worth distinguishing rather than accepting: BAD_PERMISSIONS would
			// mean the gateway had stopped routing a path it currently routes,
			// which is a different regression with a different owner.
			t.Fatalf("CreateJamfProServerURLHistoryNoteV1: the gateway now refuses this path (403 BAD_PERMISSIONS) " +
				"where it previously routed it to Jamf Pro — that is a routing change, not the hosting restriction this test pins")
		}
	}
	t.Fatalf("CreateJamfProServerURLHistoryNoteV1: 403 but neither HOSTED_ENVIRONMENT nor BAD_PERMISSIONS: %v", err)
}

// --- cache-settings ----------------------------------------------------

func TestAcceptance_Pro_CacheSettings_GetV1(t *testing.T) {
	c := accClient(t)

	settings, err := pro.New(c).GetCacheSettingsV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetCacheSettingsV1: %v", err)
	}
	if settings.CacheType == "" {
		t.Error("GetCacheSettingsV1 returned an empty cacheType")
	}
	t.Logf("Cache settings: cacheType=%q ttl=%ds directoryTtl=%v endpoints=%d",
		settings.CacheType, settings.TimeToLiveSeconds, settings.DirectoryTimeToLiveSeconds, len(settings.MemcachedEndpoints))
}

// TestAcceptance_Pro_CacheSettings_UpdateV1RefusedOnHostedTenant pins the fact
// that PUT /v1/cache-settings is routed but permanently unavailable on Jamf
// Cloud: the endpoint itself answers 403 HOSTED_ENVIRONMENT ("PUT command is not
// available in hosted environments"), pretty-printed by Jamf Pro rather than the
// gateway. The body sent is the tenant's current settings read back unchanged,
// so the call cannot alter anything even on an on-premise tenant where it works.
func TestAcceptance_Pro_CacheSettings_UpdateV1RefusedOnHostedTenant(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetCacheSettingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetCacheSettingsV1: %v", err)
	}

	_, err = p.UpdateCacheSettingsV1(ctx, current)
	if err == nil {
		t.Log("UpdateCacheSettingsV1 succeeded — this tenant is not a hosted environment, " +
			"so the write-back round-trip is the real assertion here")
		return
	}
	skipOnServerError(t, err)
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("UpdateCacheSettingsV1: non-API error: %v", err)
	}
	if !apiErr.HasStatus(403) {
		t.Fatalf("UpdateCacheSettingsV1: want 403 HOSTED_ENVIRONMENT on a hosted tenant, got status %d: %v", apiErr.StatusCode, err)
	}
	for _, d := range apiErr.Details() {
		if d.Code == "HOSTED_ENVIRONMENT" {
			t.Logf("UpdateCacheSettingsV1: refused by Jamf Pro as a hosted environment, as documented")
			return
		}
	}
	t.Fatalf("UpdateCacheSettingsV1: 403 but not HOSTED_ENVIRONMENT: %v", err)
}

// --- macos-managed-software-updates (deprecated) -----------------------

// TestAcceptance_Pro_SendMacOsManagedSoftwareUpdatesV1SupersededByPlans pins
// POST /v1/macos-managed-software-updates/send-updates, the deprecated
// predecessor of the managed-software-updates plans API.
//
// The empty request body is deliberate and is what makes this safe to run: the
// tenant-level toggle check runs before field validation, so a tenant with
// Managed Software Update Plans enabled answers 503 without looking at the body,
// and a tenant with it disabled rejects the empty body on field validation
// before any device is targeted. Neither path can send an update to a real
// device.
func TestAcceptance_Pro_SendMacOsManagedSoftwareUpdatesV1SupersededByPlans(t *testing.T) {
	c := accClient(t)

	_, err := pro.New(c).SendMacOsManagedSoftwareUpdatesV1(context.Background(), &pro.MacOsManagedSoftwareUpdate{})
	if err == nil {
		t.Fatal("SendMacOsManagedSoftwareUpdatesV1 accepted an empty request body — it must be rejected, " +
			"either by the Managed Software Update Plans toggle (503) or by field validation (400)")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("SendMacOsManagedSoftwareUpdatesV1: non-API error: %v", err)
	}
	switch {
	case apiErr.HasStatus(503):
		t.Logf("SendMacOsManagedSoftwareUpdatesV1: refused because Managed Software Update Plans is enabled, " +
			"which is the expected state for a modern tenant — use CreateManagedSoftwareUpdatePlanV1 instead")
	case apiErr.HasStatus(400):
		t.Logf("SendMacOsManagedSoftwareUpdatesV1: Managed Software Update Plans is disabled on this tenant and " +
			"the empty body was rejected by field validation, which is the other documented outcome")
	default:
		t.Fatalf("SendMacOsManagedSoftwareUpdatesV1: want 503 (plans toggle on) or 400 (field validation), got status %d: %v",
			apiErr.StatusCode, err)
	}
}

// The three operations v2154 (Jamf Pro API 11.32.0) added are published and
// unrouted. `jamf/authorization-policies` carried no allow rule for any of them
// on 2026-09-10 — `jamf_pro_dismiss_notifications.rego` covered only the
// `{type}/{id}` variant, and `jamf_pro_sso_settings.rego` had a rule for every
// other `/v3/sso/*` sibling but not `oidc-broker-config` — and PR #283
// ("API-396: Add authz rules for three new jpapi 11.32 endpoints") is open with
// that same reading in its own body. So the SDK reaches them and the gateway
// does not, which the three tests below pin.
//
// Wire-classified 2026-09-10 against eu.api.jamfcloud.com under environment
// scope, with `GET /pro/v1/jamf-pro-version` at 200 and a bogus path in the
// same namespace at 403 BAD_PERMISSIONS as controls in the same invocation, and
// each 403 reproduced on a second round. The credential is NOT short of either
// capability, which is what makes this a routing gap rather than a grant:
// `GET /pro/v3/sso/dependencies` (sso-settings:read) answers 200, and the
// routed item-level `DELETE /pro/v1/notifications/{type}/{id}`
// (dismiss-notifications:execute) answers 204.

// TestAcceptance_Pro_DismissAllNotificationsUnroutedAtGateway pins
// DELETE /v1/notifications.
func TestAcceptance_Pro_DismissAllNotificationsUnroutedAtGateway(t *testing.T) {
	c := accClient(t)

	err := pro.New(c).DismissAllNotificationsV1(context.Background())
	if err == nil {
		t.Fatal("DismissAllNotificationsV1 now answers — the gateway has started routing " +
			"DELETE /pro/v1/notifications. Replace this test with real coverage: dismiss the " +
			"collection, then assert ListNotificationsV1 returns no dismissible notification. " +
			"Note the call is destructive on a tenant that has notifications, so gate the " +
			"replacement behind JAMFPLATFORM_ACC_DESTRUCTIVE.")
	}
	skipOnServerError(t, err)
	if !gatewayUnrouted(t, "DismissAllNotificationsV1", err) {
		t.Fatalf("DismissAllNotificationsV1 failed for an unexpected reason: %v", err)
	}
}

// TestAcceptance_Pro_SsoOidcBrokerConfigUnroutedAtGateway pins
// GET and PUT /v3/sso/oidc-broker-config.
//
// Both verbs are asserted in one test because they share the single missing
// rego rule and will start routing together.
//
// The PUT body is the spec's six required fields with enabled:false and a
// throwaway client id, so that if the rule lands between now and the next run
// the test fails on the unexpected success rather than on a partial write —
// and any real replacement must read the current config first, since the
// operation is a full replacement that discards every omitted non-secret
// field.
func TestAcceptance_Pro_SsoOidcBrokerConfigUnroutedAtGateway(t *testing.T) {
	c := accClient(t)
	client := pro.New(c)

	_, err := client.GetSsoOidcBrokerConfigV3(context.Background())
	if err == nil {
		t.Fatal("GetSsoOidcBrokerConfigV3 now answers — the gateway has started routing " +
			"GET /pro/v3/sso/oidc-broker-config. Replace this test with real coverage: assert the " +
			"returned OidcBrokerConfig, and that no secret field is populated (the spec says " +
			"clientSecret and privateKeyJwt are never returned).")
	}
	skipOnServerError(t, err)
	if !gatewayUnrouted(t, "GetSsoOidcBrokerConfigV3", err) {
		t.Fatalf("GetSsoOidcBrokerConfigV3 failed for an unexpected reason: %v", err)
	}

	err = client.UpdateSsoOidcBrokerConfigV3(context.Background(), &pro.OidcBrokerConfigUpdate{
		ClientAuthMethod:   "CLIENT_SECRET",
		ClientID:           "sdk-acc-unrouted-probe",
		DiscoveryURL:       "https://example.invalid/.well-known/openid-configuration",
		Enabled:            false,
		ProductUserMapping: "EMAIL",
		Scopes:             []string{"openid"},
	})
	if err == nil {
		t.Fatal("UpdateSsoOidcBrokerConfigV3 accepted a write — the gateway has started routing " +
			"PUT /pro/v3/sso/oidc-broker-config AND this probe body was applied to the tenant's " +
			"broker configuration. Check the tenant's SSO settings, then replace this test with " +
			"coverage that reads the current config and round-trips it unchanged.")
	}
	skipOnServerError(t, err)
	if !gatewayUnrouted(t, "UpdateSsoOidcBrokerConfigV3", err) {
		t.Fatalf("UpdateSsoOidcBrokerConfigV3 failed for an unexpected reason: %v", err)
	}
}
