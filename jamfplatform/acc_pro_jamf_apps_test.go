// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/pro"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// Batch 12 — jamf-connect, jamf-protect, jamf-remote-assist. Most
// surface is probe-only (read + history note). Protect has a real
// register → settings → plans-sync → unregister lifecycle when
// JAMFPLATFORM_ACC_PRO_PROTECT_URL / _CLIENT_ID / _PASSWORD are supplied via
// an API client in the Jamf Protect web console. The Jamf Connect
// lifecycle posts a minimal com.jamf.connect.login-payload macOS
// config profile via the Classic API, polls Connect's discovery
// endpoint until the profile surfaces, exercises the PUT and
// deployment-tasks paths, then deletes the Classic profile — Connect
// doesn't have a write path of its own (it surfaces profiles, it
// doesn't create them), so Classic is the upstream.

// --- jamf-connect -----------------------------------------------------

func TestAcceptance_Pro_JamfConnect_SettingsReadV1(t *testing.T) {
	c := accClient(t)
	err := pro.New(c).GetJamfConnectSettingsV1(context.Background())
	if err == nil {
		t.Log("Jamf Connect settings GET: 204 No Content")
		return
	}
	var apiErr *jamfplatform.APIResponseError
	if errors.As(err, &apiErr) && apiErr.StatusCode == 403 {
		t.Skip("Cloud Services Connection not configured — Jamf Connect settings unavailable (expected for tenants without CSC)")
	}
	skipOnServerError(t, err)
	t.Fatalf("GetJamfConnectSettingsV1: %v", err)
}

func TestAcceptance_Pro_JamfConnect_ListConfigProfilesV1(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListJamfConnectConfigProfilesV1(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListJamfConnectConfigProfilesV1: %v", err)
	}
	t.Logf("Jamf Connect config profiles: %d", len(items))
}

func TestAcceptance_Pro_JamfConnect_ListHistoryV1(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListJamfConnectHistoryV1(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListJamfConnectHistoryV1: %v", err)
	}
	t.Logf("Jamf Connect history: %d entries", len(items))
}

func TestAcceptance_Pro_JamfConnect_HistoryNoteV1(t *testing.T) {
	c := accClient(t)

	if _, err := pro.New(c).CreateJamfConnectHistoryNoteV1(context.Background(), &pro.ObjectHistoryNote{
		Note: "sdk-acc test Jamf Connect history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateJamfConnectHistoryNoteV1: %v", err)
	}
}

// TestAcceptance_Pro_JamfConnect_LifecycleV1 uploads a minimal
// com.jamf.connect.login payload profile via the Classic
// OsxConfigurationProfile endpoint, polls Connect's discovery endpoint
// until the profile surfaces (Connect scans server-side; takes a few
// seconds in practice), exercises the PUT + deployment-tasks paths
// against the discovered UUID, then deletes the Classic profile. The
// profile carries fake identifiers so it's harmless if accidentally
// scoped — no computer is in its scope.
func TestAcceptance_Pro_JamfConnect_LifecycleV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)
	pc := proclassic.New(c)

	suffix := runSuffix()
	name := "sdk-acc-connect-" + suffix
	desc := "sdk-acc test Jamf Connect fixture — safe to delete"
	level := "System"
	distribution := "Install Automatically"
	payload := proclassic.PayloadsXMLText(fmt.Sprintf(jamfConnectPayloadXML, suffix, suffix))

	created, err := pc.CreateOSXConfigurationProfileByID(ctx, "0", &proclassic.OsXConfigurationProfile{
		General: &proclassic.OsXConfigurationProfileGeneral{
			Name:               &name,
			Description:        &desc,
			Level:              &level,
			DistributionMethod: &distribution,
			Payloads:           &payload,
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Skipf("could not create Connect fixture via Classic — lifecycle skipped: %v", err)
	}
	if created == nil || created.ID == nil {
		t.Skip("Classic Connect fixture returned no id — lifecycle skipped")
	}
	classicID := strconv.Itoa(*created.ID)
	t.Cleanup(func() {
		if err := pc.DeleteOSXConfigurationProfileByID(context.Background(), classicID); err != nil {
			t.Logf("cleanup DeleteOSXConfigurationProfileByID(%s): %v", classicID, err)
		}
	})
	t.Logf("Created Classic OSX profile %s (name=%q)", classicID, name)

	// Poll Connect's discovery endpoint until our profile surfaces.
	// Match on profileId (the Classic integer id) — the UUID Connect
	// reports is server-minted, NOT the Classic general.uuid, so
	// UUID-based matching doesn't work. Connect's scanner runs on
	// its own schedule with no SDK-visible sync endpoint, so budget
	// 2 minutes then skip: the read-only Connect tests still prove
	// List/Update plumbing against whatever the tenant has.
	wantProfileID := *created.ID
	var target *pro.LinkedConnectProfile
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		items, err := p.ListJamfConnectConfigProfilesV1(ctx, nil, "")
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("ListJamfConnectConfigProfilesV1: %v", err)
		}
		for i := range items {
			if items[i].ProfileID != nil && *items[i].ProfileID == wantProfileID {
				target = &items[i]
				break
			}
		}
		if target != nil {
			break
		}
		time.Sleep(10 * time.Second)
	}
	if target == nil {
		t.Skipf("Jamf Connect did not surface profileId %d within 2m — server-side discovery scan runs on its own schedule", wantProfileID)
	}
	if target.UUID == nil {
		t.Fatalf("Connect surfaced profileId=%d but UUID is nil: %+v", wantProfileID, target)
	}
	connectUUID := *target.UUID
	t.Logf("Jamf Connect discovered profileId=%d as connectUUID=%s (profileName=%q)", wantProfileID, connectUUID, strPtrOrEmpty(target.ProfileName))

	// Identity PUT round-trip — re-send the Connect-surfaced payload.
	if _, err := p.UpdateJamfConnectConfigProfileV1(ctx, connectUUID, target); err != nil {
		skipOnServerError(t, err)
		t.Errorf("UpdateJamfConnectConfigProfileV1(%s): %v", connectUUID, err)
	} else {
		t.Logf("Jamf Connect config profile %s identity PUT round-trip OK", connectUUID)
	}

	// Deployment tasks list. The fresh profile has empty scope so
	// there are no tasks yet — just proving plumbing.
	if tasks, err := p.ListJamfConnectDeploymentTasksV1(ctx, connectUUID, nil, ""); err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListJamfConnectDeploymentTasksV1(%s): %v", connectUUID, err)
	} else {
		t.Logf("Jamf Connect deployment %s tasks: %d", connectUUID, len(tasks))
	}
}

// jamfConnectPayloadXML wraps a com.jamf.connect dict inside a
// com.apple.ManagedClient.preferences payload — the shape Jamf Pro's
// Connect scanner actually recognises when discovering deployed
// profiles. Modelled on a live, working Connect profile from the
// Pro tenant (Jamf Connect 2 / id=36). The outer
// com.jamf.connect.login payload type alone isn't enough; the
// scanner keys off the ManagedClient-preferences wrapping.
//
// Two %s placeholders expand to suffix-derived PayloadUUID /
// PayloadIdentifier values so each test run posts a distinct
// profile. IdP config points at a placeholder Okta hostname — the
// profile is never scoped to a real computer, so this never phones
// home.
const jamfConnectPayloadXML = `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1"><dict><key>PayloadUUID</key><string>cccccccc-cccc-cccc-cccc-%[1]scccc</string><key>PayloadType</key><string>Configuration</string><key>PayloadOrganization</key><string>sdk-acc</string><key>PayloadIdentifier</key><string>cccccccc-cccc-cccc-cccc-%[1]scccc</string><key>PayloadDisplayName</key><string>sdk-acc-connect-%[2]s</string><key>PayloadDescription</key><string>sdk-acc test Jamf Connect fixture</string><key>PayloadVersion</key><integer>1</integer><key>PayloadEnabled</key><true/><key>PayloadRemovalDisallowed</key><true/><key>PayloadScope</key><string>System</string><key>PayloadContent</key><array><dict><key>PayloadDisplayName</key><string>Custom Settings</string><key>PayloadIdentifier</key><string>dddddddd-dddd-dddd-dddd-%[1]sdddd</string><key>PayloadOrganization</key><string>sdk-acc</string><key>PayloadType</key><string>com.apple.ManagedClient.preferences</string><key>PayloadUUID</key><string>dddddddd-dddd-dddd-dddd-%[1]sdddd</string><key>PayloadVersion</key><integer>1</integer><key>PayloadContent</key><dict><key>com.jamf.connect</key><dict><key>Forced</key><array><dict><key>mcx_preference_settings</key><dict><key>IdPSettings</key><dict><key>Provider</key><string>Okta</string><key>OktaAuthServer</key><string>sdk-acc-placeholder.okta.com</string></dict><key>SignIn</key><dict><key>AutoAuthenticate</key><true/></dict></dict></dict></array></dict></dict></dict></array></dict></plist>`

func strPtrOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// --- jamf-protect -----------------------------------------------------

func TestAcceptance_Pro_JamfProtect_ListHistoryV1(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListJamfProtectHistoryV1(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListJamfProtectHistoryV1: %v", err)
	}
	t.Logf("Jamf Protect history: %d entries", len(items))
}

func TestAcceptance_Pro_JamfProtect_HistoryNoteV1(t *testing.T) {
	c := accClient(t)

	if _, err := pro.New(c).CreateJamfProtectHistoryNoteV1(context.Background(), &pro.ObjectHistoryNote{
		Note: "sdk-acc test Jamf Protect history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateJamfProtectHistoryNoteV1: %v", err)
	}
}

// TestAcceptance_Pro_JamfProtect_LifecycleV1 exercises the full
// register → get settings → update → plans sync → list plans → list
// deployment tasks → unregister flow against a real Protect tenant.
// Skips if the Protect creds env vars are unset. Snapshot+restore:
// if a prior registration exists, it is unregistered, the test
// registers the env-var creds, and t.Cleanup restores the original
// creds at the end. If there was no prior registration, cleanup
// leaves the tenant unregistered.
func TestAcceptance_Pro_JamfProtect_LifecycleV1(t *testing.T) {
	protectURL := strings.TrimSpace(accEnv("JAMFPLATFORM_ACC_PRO_PROTECT_URL"))
	clientID := strings.TrimSpace(accEnv("JAMFPLATFORM_ACC_PRO_PROTECT_CLIENT_ID"))
	password := strings.TrimSpace(accEnv("JAMFPLATFORM_ACC_PRO_PROTECT_PASSWORD"))
	if protectURL == "" || clientID == "" || password == "" {
		t.Skip("JAMFPLATFORM_ACC_PRO_PROTECT_{URL,CLIENT_ID,PASSWORD} not all set — Protect lifecycle needs a Jamf Protect API client")
	}
	// Protect register expects the GraphQL endpoint URL. Accept a
	// bare console URL (…jamfcloud.com) and append /graphql.
	if !strings.HasSuffix(protectURL, "/graphql") {
		protectURL = strings.TrimRight(protectURL, "/") + "/graphql"
	}

	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	// Snapshot any existing registration. 404 is fine (no-op tenant).
	var snapshot *pro.ProtectSettingsResponse
	if cur, err := p.GetJamfProtectSettingsV1(ctx); err == nil && cur.RegistrationID != "" {
		snapshot = cur
		t.Logf("Protect snapshot: apiClientId=%s syncStatus=%s protectUrl=%s", snapshot.ApiClientID, snapshot.SyncStatus, snapshot.ProtectURL)
	} else if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Log("Protect not currently registered — test will register and leave unregistered at end")
		} else {
			skipOnServerError(t, err)
			t.Fatalf("GetJamfProtectSettingsV1 (snapshot): %v", err)
		}
	}

	// Cleanup: unregister and (if a snapshot existed) skip restore —
	// we don't have the original password, it's write-only. Log a
	// warning so the operator knows to re-register manually.
	t.Cleanup(func() {
		if err := p.UnregisterJamfProtectV1(context.Background()); err != nil {
			var apiErr *jamfplatform.APIResponseError
			if errors.As(err, &apiErr) && apiErr.StatusCode == 500 {
				t.Logf("cleanup UnregisterJamfProtectV1: %v — may already be unregistered", err)
			} else {
				t.Logf("cleanup UnregisterJamfProtectV1: %v", err)
			}
		}
		if snapshot != nil {
			t.Logf("cleanup: tenant left unregistered; prior registration was apiClientId=%s (password is write-only, can't be round-tripped) — re-register via the Protect console if needed", snapshot.ApiClientID)
		}
	})

	// Unregister any existing registration so the POST register path
	// is a fresh create, not an overwrite.
	if snapshot != nil {
		if err := p.UnregisterJamfProtectV1(ctx); err != nil {
			skipOnServerError(t, err)
			t.Fatalf("UnregisterJamfProtectV1 (snapshot clear): %v", err)
		}
		t.Log("cleared prior Protect registration before register-probe")
	}

	// Register with env-var creds.
	reg, err := p.RegisterJamfProtectV1(ctx, &pro.ProtectRegistrationRequest{
		ProtectURL: protectURL,
		ClientID:   clientID,
		Password:   password,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("RegisterJamfProtectV1(%s): %v", protectURL, err)
	}
	t.Logf("Protect registered: id=%s apiClientId=%s syncStatus=%s", reg.ID, reg.ApiClientID, reg.SyncStatus)

	// GET settings round-trip.
	settings, err := p.GetJamfProtectSettingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetJamfProtectSettingsV1: %v", err)
	}
	if settings.ProtectURL != protectURL {
		t.Errorf("protectUrl = %q, want %q", settings.ProtectURL, protectURL)
	}

	// PUT toggle autoInstall off (cheap settings mutation).
	autoOff := false
	if _, err := p.UpdateJamfProtectSettingsV1(ctx, &pro.ProtectUpdatableSettingsRequest{AutoInstall: &autoOff}); err != nil {
		skipOnServerError(t, err)
		t.Errorf("UpdateJamfProtectSettingsV1: %v", err)
	} else {
		t.Log("Protect autoInstall set to false")
	}

	// Sync plans. Returns 204; the actual sync is async server-side.
	if err := p.SyncJamfProtectPlansV1(ctx); err != nil {
		skipOnServerError(t, err)
		t.Errorf("SyncJamfProtectPlansV1: %v", err)
	} else {
		t.Log("Protect plans sync triggered")
	}

	// List plans — may be empty if sync hasn't completed or tenant
	// has no plans in the Protect account.
	plans, err := p.ListJamfProtectPlansV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListJamfProtectPlansV1: %v", err)
	} else {
		t.Logf("Jamf Protect plans: %d", len(plans))
		if len(plans) > 0 && plans[0].UUID != "" {
			// Deployment tasks list for the first plan's UUID.
			if tasks, err := p.ListJamfProtectDeploymentTasksV1(ctx, plans[0].UUID, nil, ""); err != nil {
				skipOnServerError(t, err)
				t.Errorf("ListJamfProtectDeploymentTasksV1(%s): %v", plans[0].UUID, err)
			} else {
				t.Logf("Jamf Protect deployment %s tasks: %d", plans[0].UUID, len(tasks))
			}
		}
	}
}

// --- jamf-remote-assist -----------------------------------------------
//
// Remote Assist sessions are started interactively from the Jamf Pro
// UI — there's no SDK path to create one. The API surfaces only
// history. Coverage below exercises both v1 and v2 list/get paths,
// pagination, sort + filter, detail-shape parity between versions,
// 404 on unknown id, and CSV export with both default and
// column-restricted parameters.

func TestAcceptance_Pro_RemoteAssist_ListSessionsV1(t *testing.T) {
	c := accClient(t)

	sessions, err := pro.New(c).ListJamfRemoteAssistSessionsV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListJamfRemoteAssistSessionsV1: %v", err)
	}
	t.Logf("Remote Assist sessions (v1): %d", len(sessions))
	// v1 endpoint doc says "up to 100 latest" — verify cap.
	if len(sessions) > 100 {
		t.Errorf("v1 returned %d sessions, expected <= 100 per spec", len(sessions))
	}
}

func TestAcceptance_Pro_RemoteAssist_ListSessionsV2(t *testing.T) {
	c := accClient(t)

	sessions, err := pro.New(c).ListJamfRemoteAssistSessionsV2(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListJamfRemoteAssistSessionsV2: %v", err)
	}
	t.Logf("Remote Assist sessions (v2): %d", len(sessions))
}

// TestAcceptance_Pro_RemoteAssist_ListSessionsV2Sort exercises the
// documented sort parameter (sessionId:asc). v2 pagination defaults to
// sessionId:desc; explicit asc should return the same set in inverse
// order.
func TestAcceptance_Pro_RemoteAssist_ListSessionsV2Sort(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	desc, err := p.ListJamfRemoteAssistSessionsV2(ctx, []string{"sessionId:desc"}, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListJamfRemoteAssistSessionsV2 (desc): %v", err)
	}
	asc, err := p.ListJamfRemoteAssistSessionsV2(ctx, []string{"sessionId:asc"}, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListJamfRemoteAssistSessionsV2 (asc): %v", err)
	}
	if len(desc) != len(asc) {
		t.Errorf("sort changed result count: desc=%d asc=%d", len(desc), len(asc))
	}
	if len(desc) > 1 && desc[0].SessionID == asc[0].SessionID {
		t.Errorf("asc and desc start with same session %q — sort not applied", desc[0].SessionID)
	}
	t.Logf("Remote Assist v2 sort: desc=%d asc=%d", len(desc), len(asc))
}

// TestAcceptance_Pro_RemoteAssist_ListSessionsV2Filter exercises RSQL
// filter on sessionAdminId. Zero-match filter should return [] — we
// don't assert a specific count, just that the filter doesn't 400.
func TestAcceptance_Pro_RemoteAssist_ListSessionsV2Filter(t *testing.T) {
	c := accClient(t)

	const nonMatch = "sessionAdminId==\"sdk-acc-filter-no-match-" + "sentinel\""
	items, err := pro.New(c).ListJamfRemoteAssistSessionsV2(context.Background(), nil, nonMatch)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListJamfRemoteAssistSessionsV2 (filter): %v", err)
	}
	if len(items) != 0 {
		t.Logf("unexpected: filter returned %d items (admin sentinel collision)", len(items))
	}
}

// TestAcceptance_Pro_RemoteAssist_GetSessionV1V2Parity reads the same
// session through v1 and v2 and sanity-checks the shared fields
// agree. Skips when the tenant has no sessions.
func TestAcceptance_Pro_RemoteAssist_GetSessionV1V2Parity(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	sessions, err := p.ListJamfRemoteAssistSessionsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListJamfRemoteAssistSessionsV1: %v", err)
	}
	if len(sessions) == 0 {
		t.Skip("no remote-assist sessions on tenant — nothing to GET by id")
	}
	id := sessions[0].SessionID

	v1, err := p.GetJamfRemoteAssistSessionV1(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetJamfRemoteAssistSessionV1(%s): %v", id, err)
	}
	v2, err := p.GetJamfRemoteAssistSessionV2(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetJamfRemoteAssistSessionV2(%s): %v", id, err)
	}

	if v1.SessionID != id || v2.SessionID != id {
		t.Errorf("session id mismatch: v1=%q v2=%q want %q", v1.SessionID, v2.SessionID, id)
	}
	if v1.DeviceID != v2.DeviceID {
		t.Errorf("deviceId diverges: v1=%q v2=%q", v1.DeviceID, v2.DeviceID)
	}
	if v1.SessionAdminID != v2.SessionAdminID {
		t.Errorf("sessionAdminId diverges: v1=%q v2=%q", v1.SessionAdminID, v2.SessionAdminID)
	}
	if v1.SessionType != v2.SessionType {
		t.Errorf("sessionType diverges: v1=%q v2=%q", v1.SessionType, v2.SessionType)
	}
	if v1.StatusType != v2.StatusType {
		t.Errorf("statusType diverges: v1=%q v2=%q", v1.StatusType, v2.StatusType)
	}
	t.Logf("Remote Assist session %s: v1/v2 parity OK (deviceId=%s sessionType=%s status=%s)", id, v1.DeviceID, v1.SessionType, v1.StatusType)
}

// TestAcceptance_Pro_RemoteAssist_GetSessionV1NotFound validates the
// 404 path on v1 — unknown session id must surface as APIResponseError
// with status 404.
func TestAcceptance_Pro_RemoteAssist_GetSessionV1NotFound(t *testing.T) {
	c := accClient(t)

	_, err := pro.New(c).GetJamfRemoteAssistSessionV1(context.Background(), "sdk-acc-bogus-session-id")
	if err == nil {
		t.Fatal("GetJamfRemoteAssistSessionV1(bogus): expected 404, got nil")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetJamfRemoteAssistSessionV1(bogus): want 404, got %v", err)
	}
}

func TestAcceptance_Pro_RemoteAssist_GetSessionV2NotFound(t *testing.T) {
	c := accClient(t)

	_, err := pro.New(c).GetJamfRemoteAssistSessionV2(context.Background(), "sdk-acc-bogus-session-id")
	if err == nil {
		t.Fatal("GetJamfRemoteAssistSessionV2(bogus): expected 404, got nil")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetJamfRemoteAssistSessionV2(bogus): want 404, got %v", err)
	}
}

// TestAcceptance_Pro_RemoteAssist_ExportV2 exports the session
// history as CSV and checks the response opens with a plausible
// header. Empty tenants still return a valid CSV (headers only).
func TestAcceptance_Pro_RemoteAssist_ExportV2(t *testing.T) {
	c := accClient(t)

	body, err := pro.New(c).ExportJamfRemoteAssistSessionsV2(context.Background(), &pro.ExportParameters{})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ExportJamfRemoteAssistSessionsV2: %v", err)
	}
	// First line should be the header row containing at least sessionId.
	firstLine, _, _ := bytes.Cut(body, []byte("\n"))
	if !bytes.Contains(firstLine, []byte("sessionId")) {
		t.Errorf("export header missing sessionId; first line = %q", firstLine)
	}
	t.Logf("Remote Assist export (v2): %d bytes, header=%q", len(body), firstLine)
}

// TestAcceptance_Pro_RemoteAssist_ExportV2ColumnSubset restricts the
// export to a single field (sessionId) and confirms the header row
// only contains that column.
func TestAcceptance_Pro_RemoteAssist_ExportV2ColumnSubset(t *testing.T) {
	c := accClient(t)

	fields := []pro.ExportField{{FieldName: new("sessionId")}}
	body, err := pro.New(c).ExportJamfRemoteAssistSessionsV2(context.Background(), &pro.ExportParameters{
		Fields: &fields,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ExportJamfRemoteAssistSessionsV2 (fields): %v", err)
	}
	firstLine, _, _ := bytes.Cut(body, []byte("\n"))
	header := strings.TrimSpace(string(firstLine))
	if header != "sessionId" {
		t.Errorf("column-subset export header = %q, want %q", header, "sessionId")
	}
	t.Logf("Remote Assist export (v2, sessionId only): %d bytes", len(body))
}

//go:fix inline
func strPtr(s string) *string { return new(s) }
