// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/pro"
)

// Batch 13 — settings singletons. Each resource follows the GET / PUT /
// history pattern. Tests favour GET+round-trip-PUT where the request
// and response types match; when they diverge (login-customization,
// teacher-app) we construct the request from the response. Destructive
// singletons (activation-code, jamf-pro-server-url) are GET+history
// only — changing the live values would break the tenant.

// --- activation code ---------------------------------------------------

// Activation code PUT is intentionally not exercised — changing the live
// activation code would break licensing for the tenant. We cover the
// history surface only. UpdateActivationCodeOrganizationNameV1 is also
// skipped for the same reason (org name is tenant-wide, non-reversible).
func TestAcceptance_Pro_Settings_ActivationCodeHistoryV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	if _, err := p.CreateActivationCodeHistoryNoteV1(ctx, &pro.ObjectHistoryNote{
		Note: "sdk-acc test activation-code history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateActivationCodeHistoryNoteV1: %v", err)
	}

	hist, err := p.ListActivationCodeHistoryV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListActivationCodeHistoryV1: %v", err)
	}
	t.Logf("Activation-code history: %d entries", len(hist))

	body, err := p.ExportActivationCodeHistoryV1(ctx, &pro.ExportParameters{})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ExportActivationCodeHistoryV1: %v", err)
	}
	t.Logf("Activation-code history export: %d bytes", len(body))
}

// --- SMTP server v2 ----------------------------------------------------

// SMTP PUT is not round-tripped — echoing the current config back can
// trigger server-side validation on credentials the server redacts on
// GET. Read-only.
func TestAcceptance_Pro_Settings_SmtpServerV2Read(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	s, err := pro.New(c).GetSmtpServerV2(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSmtpServerV2: %v", err)
	}
	t.Logf("SMTP server: enabled=%v authType=%s", s.Enabled, s.AuthenticationType)
}

// The allowed set is gated at instance/knobs level and is independent of the
// current SMTP settings, so it can legitimately be narrower than the
// SmtpServerV2AuthenticationType constants.
//
// Reachable as of 2026-08-29, contrary to what this comment said for a fortnight.
// The path is routed and gated on `smtp-server:read` by the gateway's
// authorization policy; an environment-scoped credential returned 200 with
// all four values, while two tenant-scoped credentials — one EU, one US — were
// refused 403 against the same regional bundles. So the 403 is a capability the
// tenant credentials do not hold, not the unrouted path the earlier note claimed,
// and no amount of reading one credential's 403 was going to reveal that.
//
// Hence the asymmetry below: an environment-scoped client is proven to reach this,
// so a 403 there is a real failure and is fatal. A tenant-scoped client skips,
// naming the capability to grant. Nothing tolerates a 403 unconditionally.
func TestAcceptance_Pro_Settings_SmtpServerAllowedAuthTypesV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	l, err := pro.New(c).ListSmtpServerAllowedAuthTypesV2(ctx)
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(403) {
			if kind, _ := c.Scope(); kind == jamfplatform.ScopeEnvironment {
				t.Fatalf("allowed-auth-types: 403 on an environment-scoped client, which was wire-verified to reach this path on 2026-08-29 — the credential has lost smtp-server:read, or the policy changed: %v", err)
			}
			t.Skipf("allowed-auth-types: 403 on a tenant-scoped client while GetSmtpServerV2 200s on the same scope — this credential lacks smtp-server:read. An environment-scoped credential returns 200: %v", err)
		}
		t.Fatalf("ListSmtpServerAllowedAuthTypesV2: %v", err)
	}
	t.Logf("SMTP allowed auth types: %v", l.AllowedAuthenticationTypes)

	// Probed direct against an 11.31.0 sandbox instance (2026-08-16, bypassing the
	// gateway): the instance returned exactly the four values the spec
	// enumerates, so every value the server can send is nameable as a constant.
	// A value outside the set means the spec's enum has drifted.
	allowed := pro.SmtpAuthenticationTypeListAllowedAuthenticationTypesValues()
	for _, got := range l.AllowedAuthenticationTypes {
		if !slices.Contains(allowed, got) {
			t.Errorf("server returned auth type %q absent from the generated constants %v — spec enum has drifted", got, allowed)
		}
	}
}

func TestAcceptance_Pro_Settings_SmtpServerHistoryV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	if _, err := p.CreateSmtpServerHistoryNoteV1(ctx, &pro.ObjectHistoryNote{
		Note: "sdk-acc test smtp-server history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateSmtpServerHistoryNoteV1: %v", err)
	}

	hist, err := p.ListSmtpServerHistoryV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListSmtpServerHistoryV1: %v", err)
	}
	t.Logf("SMTP server history: %d entries", len(hist))
}

// TestAcceptance_Pro_Settings_SmtpServerTestV1 sends a test email. Use an
// obviously-synthetic recipient so a misrouted send is easy to spot.
// Tolerate 4xx when the tenant's SMTP isn't configured to relay.
func TestAcceptance_Pro_Settings_SmtpServerTestV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	err := pro.New(c).TestSmtpServerV1(ctx, &pro.SmtpServerTest{
		RecipientEmail: "sdk-acc-discard@example.invalid",
	})
	if err == nil {
		t.Log("TestSmtpServerV1 accepted (202) — tenant SMTP relays outbound mail")
		return
	}
	var apiErr *jamfplatform.APIResponseError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		t.Logf("TestSmtpServerV1 rejected: status=%d — expected when SMTP relay blocks the test domain", apiErr.StatusCode)
		return
	}
	skipOnServerError(t, err)
	t.Fatalf("TestSmtpServerV1: %v", err)
}

// --- Jamf Pro server URL ----------------------------------------------

// Changing the Jamf Pro server URL would point clients at a different
// host. Read only.
func TestAcceptance_Pro_Settings_JamfProServerURLV1Read(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	url, err := p.GetJamfProServerURLV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetJamfProServerURLV1: %v", err)
	}
	t.Logf("Jamf Pro server URL: %s", url.URL)
}

// The history sub-resource is read-only from the SDK's point of view: GET is
// routed and answers 200, but POST is not routed at the gateway even though the
// credential holds jss-url:update — pinned by
// TestAcceptance_Pro_JamfProServerURLHistoryNoteUnroutedAtGateway.
func TestAcceptance_Pro_Settings_JamfProServerURLHistoryV1(t *testing.T) {
	c := accClient(t)

	entries, err := pro.New(c).ListJamfProServerURLHistoryV1(context.Background(), nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListJamfProServerURLHistoryV1: %v", err)
	}
	t.Logf("Jamf Pro server URL history: %d entries", len(entries))
}

// --- device-communication settings ------------------------------------

func TestAcceptance_Pro_Settings_DeviceCommunicationV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetDeviceCommunicationSettingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDeviceCommunicationSettingsV1: %v", err)
	}
	t.Logf("Device-communication settings retrieved")

	if _, err := p.UpdateDeviceCommunicationSettingsV1(ctx, current); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateDeviceCommunicationSettingsV1 round-trip: %v", err)
	}

	if _, err := p.CreateDeviceCommunicationSettingsHistoryNoteV1(ctx, &pro.ObjectHistoryNote{
		Note: "sdk-acc test device-communication history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDeviceCommunicationSettingsHistoryNoteV1: %v", err)
	}

	hist, err := p.ListDeviceCommunicationSettingsHistoryV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeviceCommunicationSettingsHistoryV1: %v", err)
	}
	t.Logf("Device-communication history: %d entries", len(hist))
}

// --- check-in v3 ------------------------------------------------------

func TestAcceptance_Pro_Settings_CheckInV3(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetCheckInSettingsV3(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetCheckInSettingsV3: %v", err)
	}
	t.Logf("Check-in settings retrieved")

	if _, err := p.UpdateCheckInSettingsV3(ctx, current); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateCheckInSettingsV3 round-trip: %v", err)
	}

	if _, err := p.CreateCheckInHistoryNoteV3(ctx, &pro.ObjectHistoryNote{
		Note: "sdk-acc test check-in history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateCheckInHistoryNoteV3: %v", err)
	}

	hist, err := p.ListCheckInHistoryV3(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListCheckInHistoryV3: %v", err)
	}
	t.Logf("Check-in history: %d entries", len(hist))
}

// --- login customization ----------------------------------------------

func TestAcceptance_Pro_Settings_LoginCustomizationV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetLoginCustomizationV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetLoginCustomizationV1: %v", err)
	}
	t.Logf("Login customization retrieved")

	// Request type is LoginContentPut; GET returns LoginContent. Map
	// the four shared fields (rampInstance is read-only). Server
	// rejects empty required fields even on round-trip, so skip the
	// PUT when the tenant has never populated disclaimer text — the
	// GET surface is still validated.
	if current.ActionText == "" || current.DisclaimerHeading == "" || current.DisclaimerMainText == "" {
		t.Logf("UpdateLoginCustomizationV1 skipped: tenant has empty required fields (server rejects empties)")
		return
	}
	put := &pro.LoginContentPut{
		ActionText:              &current.ActionText,
		DisclaimerHeading:       &current.DisclaimerHeading,
		DisclaimerMainText:      &current.DisclaimerMainText,
		IncludeCustomDisclaimer: current.IncludeCustomDisclaimer,
	}
	if _, err := p.UpdateLoginCustomizationV1(ctx, put); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateLoginCustomizationV1 round-trip: %v", err)
	}
}

// --- parent-app + teacher-app -----------------------------------------

func TestAcceptance_Pro_Settings_ParentAppV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetParentAppSettingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetParentAppSettingsV1: %v", err)
	}
	t.Logf("Parent-app settings: enabled=%v", current.IsEnabled)

	if _, err := p.UpdateParentAppSettingsV1(ctx, current); err != nil {
		// Round-trip may 400 on tenants without a configured device
		// group — the server enforces deviceGroupId referential
		// integrity on PUT but not on GET. Tolerate client errors.
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("UpdateParentAppSettingsV1 rejected: status=%d — expected on tenants without parent-app fixture", apiErr.StatusCode)
		} else {
			skipOnServerError(t, err)
			t.Fatalf("UpdateParentAppSettingsV1 round-trip: %v", err)
		}
	}

	if _, err := p.CreateParentAppHistoryNoteV1(ctx, &pro.ObjectHistoryNote{
		Note: "sdk-acc test parent-app history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateParentAppHistoryNoteV1: %v", err)
	}

	hist, err := p.ListParentAppHistoryV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListParentAppHistoryV1: %v", err)
	}
	t.Logf("Parent-app history: %d entries", len(hist))
}

func TestAcceptance_Pro_Settings_TeacherAppV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetTeacherAppSettingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetTeacherAppSettingsV1: %v", err)
	}
	t.Logf("Teacher-app settings: enabled=%v", current.IsEnabled)

	// Request type differs from response — map the writable fields.
	put := &pro.TeacherSettingsRequest{
		AutoClear:                   &current.AutoClear,
		IsEnabled:                   &current.IsEnabled,
		MaxRestrictionLengthSeconds: &current.MaxRestrictionLengthSeconds,
		TimezoneID:                  &current.TimezoneID,
		SafelistedApps:              &current.SafelistedApps,
	}
	if _, err := p.UpdateTeacherAppSettingsV1(ctx, put); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateTeacherAppSettingsV1 round-trip: %v", err)
	}

	if _, err := p.CreateTeacherAppHistoryNoteV1(ctx, &pro.ObjectHistoryNote{
		Note: "sdk-acc test teacher-app history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateTeacherAppHistoryNoteV1: %v", err)
	}

	hist, err := p.ListTeacherAppHistoryV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListTeacherAppHistoryV1: %v", err)
	}
	t.Logf("Teacher-app history: %d entries", len(hist))
}

// --- GSX connection ---------------------------------------------------

// GSX is Apple's Global Service Exchange — tenants without a
// provisioned Apple service account will 400 on mutate endpoints.
// Read-only and history are safe.
func TestAcceptance_Pro_Settings_GSXConnectionV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetGSXConnectionV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetGSXConnectionV1: %v", err)
	}
	t.Logf("GSX connection: enabled=%v", current.Enabled)

	if _, err := p.CreateGSXConnectionHistoryNoteV1(ctx, &pro.ObjectHistoryNote{
		Note: "sdk-acc test gsx-connection history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateGSXConnectionHistoryNoteV1: %v", err)
	}

	hist, err := p.ListGSXConnectionHistoryV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListGSXConnectionHistoryV1: %v", err)
	}
	t.Logf("GSX connection history: %d entries", len(hist))

	// Test endpoint calls out to Apple. If no keystore is configured
	// the server returns 400 — that's the expected path on a clean
	// tenant; fail only on 5xx.
	if err := p.TestGSXConnectionV1(ctx); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("TestGSXConnectionV1 rejected: status=%d — expected on tenants without Apple GSX fixture", apiErr.StatusCode)
		} else {
			skipOnServerError(t, err)
			t.Fatalf("TestGSXConnectionV1: %v", err)
		}
	}
}

// --- impact-alert notification settings -------------------------------

func TestAcceptance_Pro_Settings_ImpactAlertNotificationV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetImpactAlertNotificationSettingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetImpactAlertNotificationSettingsV1: %v", err)
	}
	t.Logf("Impact-alert settings retrieved")

	if err := p.UpdateImpactAlertNotificationSettingsV1(ctx, current); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateImpactAlertNotificationSettingsV1 round-trip: %v", err)
	}
}

// --- self-service-plus ------------------------------------------------

func TestAcceptance_Pro_Settings_SelfServicePlusV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	// Feature toggle returns 204 when enabled, 404 when not — both are
	// acceptable. Any other error fails.
	if err := p.GetSelfServicePlusFeatureToggleEnabledV1(ctx); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			t.Logf("Self-service-plus feature toggle: disabled (404)")
		} else {
			skipOnServerError(t, err)
			t.Fatalf("GetSelfServicePlusFeatureToggleEnabledV1: %v", err)
		}
	} else {
		t.Logf("Self-service-plus feature toggle: enabled (204)")
	}

	current, err := p.GetSelfServicePlusSettingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSelfServicePlusSettingsV1: %v", err)
	}
	t.Logf("Self-service-plus settings retrieved")

	if err := p.UpdateSelfServicePlusSettingsV1(ctx, current); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateSelfServicePlusSettingsV1 round-trip: %v", err)
	}
}
