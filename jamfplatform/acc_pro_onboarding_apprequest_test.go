// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// Batch 17 — onboarding + app-request. Onboarding is a singleton
// configuration with eligible-items probes and history; app-request
// exposes form-input-fields CRUD + settings. Both are safe to
// round-trip against the live tenant.

// --- onboarding ------------------------------------------------------

func TestAcceptance_Pro_OnboardingV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetOnboardingV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetOnboardingV1: %v", err)
	}
	t.Logf("Onboarding: enabled=%v items=%d", current.Enabled, len(current.OnboardingItems))

	if _, err := p.UpdateOnboardingV1(ctx, current); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateOnboardingV1 round-trip: %v", err)
	}
}

func TestAcceptance_Pro_OnboardingEligibleV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	apps, err := p.ListOnboardingEligibleAppsV1(ctx, nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListOnboardingEligibleAppsV1: %v", err)
	}
	t.Logf("Onboarding eligible apps: %d", len(apps))

	profiles, err := p.ListOnboardingEligibleConfigurationProfilesV1(ctx, nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListOnboardingEligibleConfigurationProfilesV1: %v", err)
	}
	t.Logf("Onboarding eligible config profiles: %d", len(profiles))

	policies, err := p.ListOnboardingEligiblePoliciesV1(ctx, nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListOnboardingEligiblePoliciesV1: %v", err)
	}
	t.Logf("Onboarding eligible policies: %d", len(policies))
}

func TestAcceptance_Pro_OnboardingHistoryV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	if _, err := p.CreateOnboardingHistoryNoteV1(ctx, &pro.ObjectHistoryNote{
		Note: "sdk-acc test onboarding history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateOnboardingHistoryNoteV1: %v", err)
	}

	hist, err := p.ListOnboardingHistoryV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListOnboardingHistoryV1: %v", err)
	}
	t.Logf("Onboarding history: %d entries", len(hist))

	body, err := p.ExportOnboardingHistoryV1(ctx, &pro.ExportParameters{})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ExportOnboardingHistoryV1: %v", err)
	}
	t.Logf("Onboarding history export: %d bytes", len(body))
}

// --- app-request form-input-fields CRUD ------------------------------

func TestAcceptance_Pro_AppRequestFormInputFieldsV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	existing, err := p.ListAppRequestFormInputFieldsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListAppRequestFormInputFieldsV1: %v", err)
	}
	t.Logf("App-request form input fields: %d existing (totalCount=%d)", len(existing.Results), existing.TotalCount)

	title := "sdk-acc-field-" + runSuffix()
	desc := "sdk-acc test description"
	created, err := p.CreateAppRequestFormInputFieldV1(ctx, &pro.AppRequestFormInputField{
		Title:       title,
		Description: &desc,
		Priority:    1,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateAppRequestFormInputFieldV1: %v", err)
	}
	if created.ID == nil {
		t.Fatalf("CreateAppRequestFormInputFieldV1: created field has nil ID")
	}
	id := strconv.Itoa(*created.ID)
	t.Logf("Created form input field id=%s title=%s", id, created.Title)
	cleanupDelete(t, "AppRequestFormInputField "+id, func() error { return p.DeleteAppRequestFormInputFieldV1(ctx, id) })

	got, err := p.GetAppRequestFormInputFieldV1(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetAppRequestFormInputFieldV1: %v", err)
	}
	if got.Title != title {
		t.Errorf("title round-trip mismatch: got %q, want %q", got.Title, title)
	}

	got.Title = title + "-upd"
	if _, err := p.UpdateAppRequestFormInputFieldV1(ctx, id, got); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateAppRequestFormInputFieldV1: %v", err)
	}

	// Bulk reorder: echo the current list back. Server validates the
	// ids exist, so a round-trip of the live list is the safest probe.
	listed, err := p.ListAppRequestFormInputFieldsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListAppRequestFormInputFieldsV1 (re-list): %v", err)
	}
	if _, err := p.ReorderAppRequestFormInputFieldsV1(ctx, &listed.Results); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ReorderAppRequestFormInputFieldsV1: %v", err)
	}
}

// --- app-request settings --------------------------------------------

func TestAcceptance_Pro_AppRequestSettingsV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetAppRequestSettingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetAppRequestSettingsV1: %v", err)
	}
	t.Logf("App-request settings: enabled=%v", current.IsEnabled)

	if _, err := p.UpdateAppRequestSettingsV1(ctx, current); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateAppRequestSettingsV1 round-trip: %v", err)
	}
}
