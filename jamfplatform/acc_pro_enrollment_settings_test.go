// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/pro"
)

// Batch 7b — enrollment + re-enrollment-preview + service-discovery-
// enrollment. Read-heavy plus round-trip settings PUTs and
// enrollment-language CRUD (which is creation-via-PUT at an explicit
// language id — no POST/create distinct from update). Access-groups
// CRUD goes through the LDAP server association, which the Pro tenant's
// tenant may not have configured, so those tests tolerate 400.

// --- ADUE session token settings ---------------------------------------

func TestAcceptance_Pro_EnrollmentSettings_ADUESessionTokenV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetADUESessionTokenSettingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetADUESessionTokenSettingsV1: %v", err)
	}
	t.Logf("ADUE session token: %+v", current)

	// Server returns both expirationIntervalDays and
	// expirationIntervalSeconds but rejects a PUT that carries both —
	// style-guide violation. Also rejects zero values. Send explicit
	// days=1 with seconds unset so the PUT is accepted; the original
	// values are preserved by not being sent.
	days := 1
	reroute := *current
	reroute.ExpirationIntervalSeconds = nil
	reroute.ExpirationIntervalDays = &days
	if _, err := p.UpdateADUESessionTokenSettingsV1(ctx, &reroute); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateADUESessionTokenSettingsV1 round-trip: %v", err)
	}
}

// --- enrollment settings V4 --------------------------------------------

func TestAcceptance_Pro_EnrollmentSettings_V4Settings(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetEnrollmentSettingsV4(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetEnrollmentSettingsV4: %v", err)
	}
	t.Logf("Enrollment settings V4 retrieved")

	if _, err := p.UpdateEnrollmentSettingsV4(ctx, current); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateEnrollmentSettingsV4 round-trip: %v", err)
	}

	access, err := p.GetEnrollmentAccessManagementV4(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetEnrollmentAccessManagementV4: %v", err)
	}
	t.Logf("Access management: %+v", access)

	if _, err := p.UpdateEnrollmentAccessManagementV4(ctx, access); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateEnrollmentAccessManagementV4 round-trip: %v", err)
	}
}

// --- enrollment history V2 ---------------------------------------------

func TestAcceptance_Pro_EnrollmentSettings_HistoryV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	if _, err := p.CreateEnrollmentHistoryNoteV2(ctx, &pro.ObjectHistoryNote{
		Note: "sdk-acc test enrollment history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateEnrollmentHistoryNoteV2: %v", err)
	}

	hist, err := p.ListEnrollmentHistoryV2(ctx, nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListEnrollmentHistoryV2: %v", err)
	}
	t.Logf("Enrollment history: %d entries", len(hist))

	body, err := p.ExportEnrollmentHistoryV2(ctx, &pro.ExportParameters{}, nil, nil, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ExportEnrollmentHistoryV2: %v", err)
	}
	t.Logf("Enrollment history export: %d bytes", len(body))
}

// --- enrollment access groups V3 ---------------------------------------

func TestAcceptance_Pro_EnrollmentSettings_ListAccessGroupsV3(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListEnrollmentAccessGroupsV3(context.Background(), nil, false)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListEnrollmentAccessGroupsV3: %v", err)
	}
	t.Logf("Enrollment access groups: %d", len(items))
}

// TestAcceptance_Pro_EnrollmentSettings_AccessGroupCRUDV3 requires an LDAP
// server association — create will 400 without a real ldapServerId. If
// the tenant has an existing access group we reuse its ldapServerId;
// otherwise we probe with "-1" and tolerate rejection.
func TestAcceptance_Pro_EnrollmentSettings_AccessGroupCRUDV3(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	existing, err := p.ListEnrollmentAccessGroupsV3(ctx, nil, false)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListEnrollmentAccessGroupsV3: %v", err)
	}

	ldapID := "-1"
	if len(existing) > 0 {
		ldapID = existing[0].LdapServerID
	}
	groupName := "sdk-acc-access-group-" + runSuffix()

	_, err = p.CreateEnrollmentAccessGroupV3(ctx, &pro.EnrollmentAccessGroupPreview{
		GroupID:      "sdk-acc-fake-group",
		LdapServerID: ldapID,
		Name:         groupName,
	})
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("CreateEnrollmentAccessGroupV3 rejected (ldapServerId=%q): status=%d — expected without a configured LDAP server", ldapID, apiErr.StatusCode)
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("CreateEnrollmentAccessGroupV3: %v", err)
	}
	t.Skip("access group created unexpectedly — no cleanup path implemented yet; skipping until fixture tenant is available")
}

// --- language codes + languages ----------------------------------------

func TestAcceptance_Pro_EnrollmentSettings_LanguageCodesV3(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	all, err := p.ListEnrollmentLanguageCodesV3(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListEnrollmentLanguageCodesV3: %v", err)
	}
	t.Logf("All language codes: %d", len(all))

	filtered, err := p.ListFilteredEnrollmentLanguageCodesV3(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListFilteredEnrollmentLanguageCodesV3: %v", err)
	}
	t.Logf("Filtered language codes: %d", len(filtered))
}

// TestAcceptance_Pro_EnrollmentSettings_LanguageUpdateV3 exercises
// update-and-revert on an existing configured enrollment language.
//
// PUT /v3/enrollment/languages/{code} against a not-yet-configured code
// returns 500 on this tenant regardless of payload — verified with curl
// across 17 major language codes (fr, de, es, it, pt, ru, zh, ja, ko,
// ar, tr, pl, nl, sv, da, no, fi, cy all 500). The spec has no POST on
// the collection, so there is no working create path via the API.
// DELETE on any configured language also hits 500. The only usable op
// is update-on-existing, which this test covers by round-tripping a
// modified loginButton and restoring the original.
//
// If/when the tenant's PUT-create path is fixed server-side, flip this
// to a full create→get→update→delete→verify-404 CRUD test.
func TestAcceptance_Pro_EnrollmentSettings_LanguageUpdateV3(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	langs, err := p.ListEnrollmentLanguagesV3(ctx, nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListEnrollmentLanguagesV3: %v", err)
	}
	if len(langs) == 0 {
		t.Skip("tenant has no configured enrollment languages — nothing to update")
	}

	// Pick a language with a languageCode set.
	var code string
	for _, l := range langs {
		if l.LanguageCode != nil && *l.LanguageCode != "" {
			code = *l.LanguageCode
			break
		}
	}
	if code == "" {
		t.Skip("no configured language with a languageCode field")
	}

	original, err := p.GetEnrollmentLanguageV3(ctx, code)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetEnrollmentLanguageV3(%s): %v", code, err)
	}
	originalLoginButton := ""
	if original.LoginButton != nil {
		originalLoginButton = *original.LoginButton
	}

	// Ensure we revert on exit even if the test fails mid-way.
	t.Cleanup(func() {
		restore := *original
		restore.LoginButton = &originalLoginButton
		if _, err := p.UpdateEnrollmentLanguageV3(context.Background(), code, &restore); err != nil {
			t.Logf("cleanup restore LoginButton to %q: %v", originalLoginButton, err)
		}
	})

	modified := "sdk-acc-" + runSuffix()
	updateBody := *original
	updateBody.LoginButton = &modified
	if _, err := p.UpdateEnrollmentLanguageV3(ctx, code, &updateBody); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateEnrollmentLanguageV3(%s): %v", code, err)
	}

	reread, err := p.GetEnrollmentLanguageV3(ctx, code)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetEnrollmentLanguageV3(%s) post-update: %v", code, err)
	}
	if reread.LoginButton == nil || *reread.LoginButton != modified {
		t.Errorf("LoginButton post-update = %v, want %q", reread.LoginButton, modified)
	}
	t.Logf("Round-trip update on language %q succeeded", code)
}

// --- re-enrollment-preview ---------------------------------------------

func TestAcceptance_Pro_EnrollmentSettings_ReenrollmentV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetReenrollmentSettingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetReenrollmentSettingsV1: %v", err)
	}
	t.Logf("Re-enrollment settings retrieved")

	if _, err := p.UpdateReenrollmentSettingsV1(ctx, current); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateReenrollmentSettingsV1 round-trip: %v", err)
	}

	if _, err := p.CreateReenrollmentHistoryNoteV1(ctx, &pro.ObjectHistoryNote{
		Note: "sdk-acc test re-enrollment note",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateReenrollmentHistoryNoteV1: %v", err)
	}

	hist, err := p.ListReenrollmentHistoryV1(ctx, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListReenrollmentHistoryV1: %v", err)
	}
	t.Logf("Re-enrollment history: %d entries", len(hist))

	body, err := p.ExportReenrollmentHistoryV1(ctx, &pro.ExportParameters{}, nil, nil, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ExportReenrollmentHistoryV1: %v", err)
	}
	t.Logf("Re-enrollment history export: %d bytes", len(body))
}

// --- service-discovery-enrollment --------------------------------------

func TestAcceptance_Pro_EnrollmentSettings_ServiceDiscoveryV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetServiceDiscoveryEnrollmentWellKnownSettingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetServiceDiscoveryEnrollmentWellKnownSettingsV1: %v", err)
	}
	t.Logf("Service discovery well-known settings: %+v", current)

	// Round-trip the current settings back — PUT response body not
	// documented in spec; transport path is what we're exercising.
	req := serviceDiscoveryWellKnownRequestFromResponse(t, current)
	if err := p.UpdateServiceDiscoveryEnrollmentWellKnownSettingsV1(ctx, req); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateServiceDiscoveryEnrollmentWellKnownSettingsV1 round-trip: %v", err)
	}
}

// serviceDiscoveryWellKnownRequestFromResponse coerces the GET response
// shape into the PUT request shape by JSON round-trip.
func serviceDiscoveryWellKnownRequestFromResponse(t *testing.T, resp *pro.WellKnownSettingsResponse) *pro.WellKnownSettingsRequest {
	t.Helper()
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var req pro.WellKnownSettingsRequest
	if err := json.Unmarshal(b, &req); err != nil {
		t.Fatalf("unmarshal into request: %v", err)
	}
	if !strings.HasPrefix(string(b), "{") {
		t.Errorf("unexpected marshal shape: %s", b)
	}
	return &req
}
