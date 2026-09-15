// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// TeamViewer Remote Administration (the /preview/remote-administration-*
// family). Wire-probed 2026-08-31 against eu.api.jamfcloud.com: every path in
// the family is routed and reachable, and the tenant has zero configurations.
//
// Creating one is not safely automatable — ConnectionConfigurationCandidateRequest
// carries a real TeamViewer ScriptToken, and Jamf Pro validates it against
// TeamViewer's API, so a synthetic token cannot produce a usable configuration
// and a real one cannot be committed. The create/update/delete chain is therefore
// gated behind an opt-in that also supplies the token, and the default run
// exercises the read chain plus the path-parameter contract.
//
// The path parameters are typed `string` in the spec but the server requires a
// positive integer or -1 (probed: a UUID gives 400 INVALID_ID naming pathId,
// integrationID or configurationId depending on the operation). That is the
// assertion the bogus-id probes make — it proves the request reached the handler
// and that the SDK's URL shape is right, without needing a real configuration.

const teamViewerBogusID = "00000000-0000-0000-0000-000000000000"

// teamViewerScriptToken returns the TeamViewer script token to create a real
// configuration with, or "" when the opt-in is unset. It is an opt-in rather
// than a fixture because the token is a third-party credential this repo cannot
// hold and Jamf Pro validates it against TeamViewer before the configuration
// exists.
func teamViewerScriptToken() string {
	return accEnv("JAMFPLATFORM_ACC_PRO_TEAMVIEWER_SCRIPT_TOKEN")
}

// requireTeamViewerInvalidID asserts err is the server's own path-parameter
// rejection rather than a routing failure or an unexpected status. A nil error
// is a hard failure: it would mean the bogus identifier resolved to something.
func requireTeamViewerInvalidID(t *testing.T, method string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s(%s): want 400 INVALID_ID for a non-numeric id, got success", method, teamViewerBogusID)
	}
	skipOnServerError(t, err)
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("%s: non-API error, the request did not reach the gateway: %v", method, err)
	}
	if !apiErr.HasStatus(400) {
		t.Fatalf("%s: want 400 INVALID_ID, got status %d: %v", method, apiErr.StatusCode, err)
	}
	for _, d := range apiErr.Details() {
		if d.Code == "INVALID_ID" {
			t.Logf("%s: reached the handler, rejected the non-numeric id on field %q", method, d.Field)
			return
		}
	}
	t.Fatalf("%s: 400 but not INVALID_ID, so the failure is not the documented one: %v", method, err)
}

func TestAcceptance_Pro_RemoteAdministration_ListConfigurationsPreview(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	items, err := p.ListRemoteAdministrationConfigurationsPreview(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListRemoteAdministrationConfigurationsPreview: %v", err)
	}
	t.Logf("Remote administration configurations: %d", len(items))

	// When the tenant does have one, exercise the whole read chain against its
	// real id rather than settling for the bogus-id contract below.
	for _, cfg := range items {
		if cfg.Type != "team-viewer" {
			t.Logf("configuration %s has type %q, not team-viewer — skipping the TeamViewer sub-resources", cfg.ID, cfg.Type)
			continue
		}
		got, err := p.GetTeamViewerConfigurationPreview(ctx, cfg.ID)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("GetTeamViewerConfigurationPreview(%s): %v", cfg.ID, err)
		}
		if got.ID != cfg.ID {
			t.Errorf("GetTeamViewerConfigurationPreview(%s) returned ID %q", cfg.ID, got.ID)
		}
		t.Logf("Configuration %s: displayName=%q enabled=%v siteId=%q", got.ID, got.DisplayName, got.Enabled, got.SiteID)

		status, err := p.GetTeamViewerConfigurationStatusPreview(ctx, cfg.ID)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("GetTeamViewerConfigurationStatusPreview(%s): %v", cfg.ID, err)
		}
		t.Logf("Configuration %s connection verification: %q", cfg.ID, status.ConnectionVerificationResult)

		sessions, err := p.ListTeamViewerSessionsPreview(ctx, cfg.ID, "")
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("ListTeamViewerSessionsPreview(%s): %v", cfg.ID, err)
		}
		t.Logf("Configuration %s sessions: %d", cfg.ID, len(sessions))

		for _, s := range sessions {
			gotSession, err := p.GetTeamViewerSessionPreview(ctx, cfg.ID, s.ID)
			if err != nil {
				skipOnServerError(t, err)
				t.Fatalf("GetTeamViewerSessionPreview(%s, %s): %v", cfg.ID, s.ID, err)
			}
			if gotSession.ID != s.ID {
				t.Errorf("GetTeamViewerSessionPreview(%s, %s) returned ID %q", cfg.ID, s.ID, gotSession.ID)
			}
			sessionStatus, err := p.GetTeamViewerSessionStatusPreview(ctx, cfg.ID, s.ID)
			if err != nil {
				skipOnServerError(t, err)
				t.Fatalf("GetTeamViewerSessionStatusPreview(%s, %s): %v", cfg.ID, s.ID, err)
			}
			t.Logf("Session %s: state=%q online=%v", s.ID, sessionStatus.SessionState, sessionStatus.Online)
			break // one is enough to cover the shape
		}
		break
	}
}

// TestAcceptance_Pro_RemoteAdministration_ConfigurationPathContract covers the
// four configuration-scoped operations that need an id, using the server's own
// path-parameter rejection as the assertion. Deleting is included: DELETE on a
// non-numeric id is refused before anything is looked up, so it cannot remove a
// real configuration.
func TestAcceptance_Pro_RemoteAdministration_ConfigurationPathContract(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	_, err := p.GetTeamViewerConfigurationPreview(ctx, teamViewerBogusID)
	requireTeamViewerInvalidID(t, "GetTeamViewerConfigurationPreview", err)

	_, err = p.GetTeamViewerConfigurationStatusPreview(ctx, teamViewerBogusID)
	requireTeamViewerInvalidID(t, "GetTeamViewerConfigurationStatusPreview", err)

	enabled := false
	_, err = p.UpdateTeamViewerConfigurationPreview(ctx, teamViewerBogusID, &pro.ConnectionConfigurationUpdateRequest{Enabled: &enabled})
	requireTeamViewerInvalidID(t, "UpdateTeamViewerConfigurationPreview", err)

	err = p.DeleteTeamViewerConfigurationPreview(ctx, teamViewerBogusID)
	requireTeamViewerInvalidID(t, "DeleteTeamViewerConfigurationPreview", err)
}

// TestAcceptance_Pro_RemoteAdministration_SessionPathContract does the same for
// the five session-scoped operations.
func TestAcceptance_Pro_RemoteAdministration_SessionPathContract(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	_, err := p.ListTeamViewerSessionsPreview(ctx, teamViewerBogusID, "")
	requireTeamViewerInvalidID(t, "ListTeamViewerSessionsPreview", err)

	_, err = p.GetTeamViewerSessionPreview(ctx, teamViewerBogusID, "1")
	requireTeamViewerInvalidID(t, "GetTeamViewerSessionPreview", err)

	_, err = p.GetTeamViewerSessionStatusPreview(ctx, teamViewerBogusID, "1")
	requireTeamViewerInvalidID(t, "GetTeamViewerSessionStatusPreview", err)

	_, err = p.CreateTeamViewerSessionPreview(ctx, teamViewerBogusID, &pro.SessionCandidateRequest{
		Description: "sdk-acc path contract probe",
		DeviceID:    "1",
		DeviceType:  "COMPUTER",
	})
	requireTeamViewerInvalidID(t, "CreateTeamViewerSessionPreview", err)

	err = p.CloseTeamViewerSessionPreview(ctx, teamViewerBogusID, "1")
	requireTeamViewerInvalidID(t, "CloseTeamViewerSessionPreview", err)

	err = p.ResendTeamViewerSessionNotificationPreview(ctx, teamViewerBogusID, "1")
	requireTeamViewerInvalidID(t, "ResendTeamViewerSessionNotificationPreview", err)
}

// TestAcceptance_Pro_RemoteAdministration_CreateConfigurationPreview asserts the
// create body contract without a token, then runs the real create/update/delete
// chain when one is supplied.
//
// The no-token half is a real assertion, not an escape hatch: a well-formed
// request minus the token must come back 400 INVALID_FIELD naming the fields the
// server requires, which proves the SDK's request type and Content-Type reach
// Jamf Pro's own validator.
func TestAcceptance_Pro_RemoteAdministration_CreateConfigurationPreview(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	token := teamViewerScriptToken()
	if token == "" {
		_, err := p.CreateTeamViewerConfigurationPreview(ctx, &pro.ConnectionConfigurationCandidateRequest{})
		if err == nil {
			t.Fatal("CreateTeamViewerConfigurationPreview with an empty body succeeded — it must be rejected")
		}
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if !errors.As(err, &apiErr) {
			t.Fatalf("CreateTeamViewerConfigurationPreview: non-API error: %v", err)
		}
		if !apiErr.HasStatus(400) {
			t.Fatalf("CreateTeamViewerConfigurationPreview: want 400 INVALID_FIELD, got status %d: %v", apiErr.StatusCode, err)
		}
		var fields []string
		for _, d := range apiErr.Details() {
			if d.Code == "INVALID_FIELD" {
				fields = append(fields, d.Field)
			}
		}
		if len(fields) == 0 {
			t.Fatalf("CreateTeamViewerConfigurationPreview: 400 but no INVALID_FIELD detail: %v", err)
		}
		t.Logf("CreateTeamViewerConfigurationPreview reached Jamf Pro's validator; required fields: %v", fields)
		t.Logf("Set JAMFPLATFORM_ACC_PRO_TEAMVIEWER_SCRIPT_TOKEN to a real TeamViewer script token to run the full create/update/delete chain")
		return
	}

	created, err := p.CreateTeamViewerConfigurationPreview(ctx, &pro.ConnectionConfigurationCandidateRequest{
		DisplayName:    "sdk-acc-" + runSuffix(),
		Enabled:        false,
		ScriptToken:    token,
		SessionTimeout: 30,
		SiteID:         "-1",
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateTeamViewerConfigurationPreview: %v", err)
	}
	if created.ID == "" {
		t.Fatal("CreateTeamViewerConfigurationPreview returned no ID")
	}
	cleanupDelete(t, "DeleteTeamViewerConfigurationPreview", func() error {
		return p.DeleteTeamViewerConfigurationPreview(ctx, created.ID)
	})
	t.Logf("Created TeamViewer configuration %s (href %s)", created.ID, created.Href)

	got, err := p.GetTeamViewerConfigurationPreview(ctx, created.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetTeamViewerConfigurationPreview(%s): %v", created.ID, err)
	}
	if got.ID != created.ID {
		t.Errorf("GetTeamViewerConfigurationPreview(%s) returned ID %q", created.ID, got.ID)
	}

	newTimeout := 45
	updated, err := p.UpdateTeamViewerConfigurationPreview(ctx, created.ID, &pro.ConnectionConfigurationUpdateRequest{
		SessionTimeout: &newTimeout,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateTeamViewerConfigurationPreview(%s): %v", created.ID, err)
	}
	if updated.SessionTimeout == nil || *updated.SessionTimeout != newTimeout {
		t.Errorf("SessionTimeout = %v, want %d", updated.SessionTimeout, newTimeout)
	}

	if err := p.DeleteTeamViewerConfigurationPreview(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteTeamViewerConfigurationPreview(%s): %v", created.ID, err)
	}

	_, err = p.GetTeamViewerConfigurationPreview(ctx, created.ID)
	if err == nil {
		t.Fatalf("GetTeamViewerConfigurationPreview(%s) after delete should fail", created.ID)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetTeamViewerConfigurationPreview(%s) after delete: want 404, got %v", created.ID, err)
	}
}
