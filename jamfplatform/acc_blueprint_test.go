// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/blueprints"
)

func createTestBlueprint(t *testing.T, c *jamfplatform.Client, name string, groupID string, steps []blueprints.BlueprintStep) *blueprints.BlueprintDetail {
	t.Helper()
	ctx := context.Background()
	bp := blueprints.New(c)

	desc := "SDK acceptance test — safe to delete"
	resp, err := bp.CreateBlueprint(ctx, &blueprints.CreateBlueprintRequest{
		Name:        name,
		Description: &desc,
		Scope:       blueprints.CreateScope{DeviceGroups: []string{groupID}},
		Steps:       steps,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateBlueprint failed for %q: %v", name, err)
	}
	cleanupDelete(t, "DeleteBlueprint", func() error { return bp.DeleteBlueprint(ctx, resp.ID) })

	got, err := bp.GetBlueprint(ctx, resp.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetBlueprint failed for %q: %v", name, err)
	}
	return got
}

func makeStep(identifier string, config any) []blueprints.BlueprintStep {
	configJSON, _ := json.Marshal(config)
	stepName := "Step 1"
	return []blueprints.BlueprintStep{
		{
			Name: &stepName,
			Components: []blueprints.Component{
				{
					Identifier:    identifier,
					Configuration: json.RawMessage(configJSON),
				},
			},
		},
	}
}

func TestAcceptance_ListBlueprints(t *testing.T) {
	c := accEnvClient(t)

	bps, err := blueprints.New(c).ListBlueprints(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBlueprints failed: %v", err)
	}
	t.Logf("Found %d blueprints", len(bps))
}

func TestAcceptance_ListBlueprintsWithSearch(t *testing.T) {
	c := accEnvClient(t)

	bps, err := blueprints.New(c).ListBlueprints(context.Background(), nil, "sdk-acc")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBlueprints with search failed: %v", err)
	}
	t.Logf("Found %d blueprints matching 'sdk-acc'", len(bps))
}

func TestAcceptance_ListBlueprintComponents(t *testing.T) {
	c := accEnvClient(t)

	comps, err := blueprints.New(c).ListBlueprintComponents(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBlueprintComponents failed: %v", err)
	}
	if len(comps) == 0 {
		t.Log("No blueprint components found")
		return
	}
	t.Logf("Found %d blueprint components", len(comps))
	for _, comp := range comps {
		t.Logf("  %s (%s)", comp.Name, comp.Identifier)
	}
}

func TestAcceptance_GetBlueprintComponent(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	bp := blueprints.New(c)

	comps, err := bp.ListBlueprintComponents(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBlueprintComponents failed: %v", err)
	}
	if len(comps) == 0 {
		t.Skip("No blueprint components available")
	}

	comp, err := bp.GetBlueprintComponent(ctx, comps[0].Identifier)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetBlueprintComponent failed for %q: %v", comps[0].Identifier, err)
	}
	if comp.Identifier != comps[0].Identifier {
		t.Errorf("expected %q, got %q", comps[0].Identifier, comp.Identifier)
	}
	t.Logf("Read component: %s (%s)", comp.Name, comp.Identifier)
}

func TestAcceptance_Blueprint_EmptyBlueprint(t *testing.T) {
	groupID := requireSmartGroupFixture(t)
	c := accEnvClient(t)

	name := "sdk-acc-empty-bp-" + runSuffix()
	bp := createTestBlueprint(t, c, name, groupID, []blueprints.BlueprintStep{})

	if bp.Name != name {
		t.Errorf("expected name %q, got %q", name, bp.Name)
	}
	if len(bp.Scope.DeviceGroups) != 1 || bp.Scope.DeviceGroups[0] != groupID {
		t.Errorf("unexpected scope: %v", bp.Scope.DeviceGroups)
	}
	t.Logf("Created empty blueprint ID: %s", bp.ID)
}

// TestAcceptance_Blueprint_EmptyStepsCannotBePatched pins a spec/wire
// contradiction that makes one SDK call sequence unreachable:
// CreateBlueprint with no steps, then UpdateBlueprint.
//
// CreateBlueprintRequest.steps declares minItems: 0 and the create accepts an
// empty array — TestAcceptance_Blueprint_EmptyBlueprint above depends on that.
// But PATCH is application/merge-patch+json, the server validates the *merged*
// entity, and it enforces steps size 1..100 on the result. So a blueprint
// stored with no steps rejects every patch, including one that does not mention
// steps at all, with 400 Size on the field `steps`. UpdateBlueprintRequest.steps
// declares no bounds, so the constraint is undeclared on the operation that
// applies it.
//
// Wire-established 2026-09-11 by contrast rather than by reading the error:
// TestAcceptance_Blueprint_PartialUpdatePreservesSteps sends the identical
// description-only patch to a blueprint that has one step and gets 204 with its
// steps intact, so the rejection is the stored state and not the patch body.
//
// Report upstream: either the create should refuse an empty steps array, or the
// patch should not apply minItems to a field the request does not carry.
func TestAcceptance_Blueprint_EmptyStepsCannotBePatched(t *testing.T) {
	groupID := requireSmartGroupFixture(t)
	c := accEnvClient(t)
	ctx := context.Background()
	bpClient := blueprints.New(c)

	bp := createTestBlueprint(t, c, "sdk-acc-empty-steps-patch-"+runSuffix(), groupID, []blueprints.BlueprintStep{})
	if len(bp.Steps) != 0 {
		t.Fatalf("fixture is not an empty-steps blueprint: %d steps", len(bp.Steps))
	}

	// Description-only, so nothing here can be what the server objects to.
	desc := "patched after creation with no steps"
	err := bpClient.UpdateBlueprint(ctx, bp.ID, &blueprints.UpdateBlueprintRequest{Description: &desc})
	if err == nil {
		t.Fatal("UpdateBlueprint succeeded on a blueprint stored with no steps — the contradiction " +
			"has been resolved upstream. Replace this assertion with a real round-trip and drop the " +
			"note above; check whether CreateBlueprint still accepts an empty steps array too.")
	}
	skipOnServerError(t, err)

	apiErr := jamfplatform.AsAPIError(err)
	if apiErr == nil {
		t.Fatalf("UpdateBlueprint: non-API error, the request did not reach the gateway: %v", err)
	}
	if !apiErr.HasStatus(400) {
		t.Fatalf("UpdateBlueprint: want the recorded 400 on steps, got %d: %s", apiErr.StatusCode, apiErr.Summary())
	}
	if msgs := apiErr.FieldErrors()["steps"]; len(msgs) == 0 {
		t.Fatalf("UpdateBlueprint: 400 but not attributed to `steps`, so the refusal is not the one "+
			"this test pins: %s", apiErr.Summary())
	}
	t.Logf("UpdateBlueprint on an empty-steps blueprint: 400 as recorded (%s)", apiErr.Summary())
}

func TestAcceptance_Blueprint_UpdateAndRead(t *testing.T) {
	groupID := requireSmartGroupFixture(t)
	c := accEnvClient(t)
	ctx := context.Background()
	suffix := runSuffix()
	bpClient := blueprints.New(c)

	boolTrue := true
	minLen := 6
	maxFailed := 3
	reuseLimit := 1

	steps := makeStep("com.jamf.ddm.passcode-settings", blueprints.PasscodeSettingsConfiguration{
		RequirePasscode: &blueprints.RequirePasscode{
			Included: &boolTrue,
			Value:    &boolTrue,
		},
		MinimumLength: &blueprints.MinimumLength{
			Included: &boolTrue,
			Value:    &minLen,
		},
		MaximumFailedAttempts: &blueprints.MaximumFailedAttempts{
			Included: &boolTrue,
			Value:    &maxFailed,
		},
		PasscodeReuseLimit: &blueprints.PasscodeReuseLimit{
			Included: &boolTrue,
			Value:    &reuseLimit,
		},
		Version: 2,
	})
	bp := createTestBlueprint(t, c, "sdk-acc-update-test-"+suffix, groupID, steps)

	renamedName := "sdk-acc-update-renamed-" + suffix
	updatedDesc := "Updated description"
	err := bpClient.UpdateBlueprint(ctx, bp.ID, &blueprints.UpdateBlueprintRequest{
		Name:        &renamedName,
		Description: &updatedDesc,
		Scope:       &blueprints.BlueprintScope{DeviceGroups: []string{groupID}},
		Steps:       &steps,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateBlueprint failed: %v", err)
	}

	updated, err := bpClient.GetBlueprint(ctx, bp.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetBlueprint after update failed: %v", err)
	}
	if updated.Name != renamedName {
		t.Errorf("expected name %q, got %q", renamedName, updated.Name)
	}
	if updated.Description == nil || *updated.Description != "Updated description" {
		t.Errorf("expected updated description, got %v", updated.Description)
	}
	t.Logf("Updated blueprint ID: %s", bp.ID)
}

func TestAcceptance_Blueprint_PartialUpdatePreservesSteps(t *testing.T) {
	groupID := requireSmartGroupFixture(t)
	c := accEnvClient(t)
	ctx := context.Background()
	suffix := runSuffix()
	bpClient := blueprints.New(c)

	boolTrue := true
	minLen := 6
	maxFailed := 3
	reuseLimit := 1

	steps := makeStep("com.jamf.ddm.passcode-settings", blueprints.PasscodeSettingsConfiguration{
		RequirePasscode: &blueprints.RequirePasscode{
			Included: &boolTrue,
			Value:    &boolTrue,
		},
		MinimumLength: &blueprints.MinimumLength{
			Included: &boolTrue,
			Value:    &minLen,
		},
		MaximumFailedAttempts: &blueprints.MaximumFailedAttempts{
			Included: &boolTrue,
			Value:    &maxFailed,
		},
		PasscodeReuseLimit: &blueprints.PasscodeReuseLimit{
			Included: &boolTrue,
			Value:    &reuseLimit,
		},
		Version: 2,
	})
	bp := createTestBlueprint(t, c, "sdk-acc-partial-update-"+suffix, groupID, steps)

	if len(bp.Steps) == 0 {
		t.Fatal("expected blueprint to have steps after creation")
	}

	// Update only the name — omit Steps entirely.
	// Before the fix, this would serialize "steps":[] and wipe them.
	renamedName := "sdk-acc-partial-renamed-" + suffix
	err := bpClient.UpdateBlueprint(ctx, bp.ID, &blueprints.UpdateBlueprintRequest{
		Name: &renamedName,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateBlueprint (partial) failed: %v", err)
	}

	updated, err := bpClient.GetBlueprint(ctx, bp.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetBlueprint after partial update failed: %v", err)
	}
	if updated.Name != renamedName {
		t.Errorf("expected name %q, got %q", renamedName, updated.Name)
	}
	if len(updated.Steps) != len(bp.Steps) {
		t.Errorf("steps were lost: expected %d steps, got %d", len(bp.Steps), len(updated.Steps))
	}
	t.Logf("Partial update preserved %d steps on blueprint %s", len(updated.Steps), bp.ID)
}

func TestAcceptance_Blueprint_Report(t *testing.T) {
	groupID := requireSmartGroupFixture(t)
	c := accEnvClient(t)
	ctx := context.Background()
	bpClient := blueprints.New(c)

	boolTrue := true
	minLen := 6
	maxFailed := 3
	reuseLimit := 1

	steps := makeStep("com.jamf.ddm.passcode-settings", blueprints.PasscodeSettingsConfiguration{
		RequirePasscode: &blueprints.RequirePasscode{
			Included: &boolTrue,
			Value:    &boolTrue,
		},
		MinimumLength: &blueprints.MinimumLength{
			Included: &boolTrue,
			Value:    &minLen,
		},
		MaximumFailedAttempts: &blueprints.MaximumFailedAttempts{
			Included: &boolTrue,
			Value:    &maxFailed,
		},
		PasscodeReuseLimit: &blueprints.PasscodeReuseLimit{
			Included: &boolTrue,
			Value:    &reuseLimit,
		},
		Version: 2,
	})
	bp := createTestBlueprint(t, c, "sdk-acc-report-"+runSuffix(), groupID, steps)

	if err := bpClient.DeployBlueprint(ctx, bp.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeployBlueprint failed: %v", err)
	}
	t.Cleanup(func() { _ = bpClient.UndeployBlueprint(ctx, bp.ID) })

	pollCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	err := jamfplatform.PollUntil(pollCtx, 2*time.Second, func(ctx context.Context) (bool, error) {
		got, err := bpClient.GetBlueprint(ctx, bp.ID)
		if err != nil {
			return false, err
		}
		return got.DeploymentState.State != "NOT_DEPLOYED", nil
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("Timed out waiting for deployment: %v", err)
	}

	report, err := bpClient.GetBlueprintReport(ctx, bp.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetBlueprintReport failed: %v", err)
	}
	t.Logf("Report for %s: succeeded=%d failed=%d pending=%d", bp.ID, report.Succeeded, report.Failed, report.Pending)
}

// TestAcceptance_Blueprint_TypedComponents creates a blueprint for each
// component type using the generated typed configuration structs. This proves
// that typed configs marshal correctly and are accepted by the Jamf API.
// Components not available on the tenant are skipped automatically.
func TestAcceptance_Blueprint_TypedComponents(t *testing.T) {
	groupID := requireSmartGroupFixture(t)
	c := accEnvClient(t)

	// Query available components so we can skip identifiers not present on this tenant.
	available, err := blueprints.New(c).ListBlueprintComponents(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBlueprintComponents failed: %v", err)
	}
	enabledIDs := make(map[string]bool, len(available))
	for _, comp := range available {
		enabledIDs[comp.Identifier] = true
	}

	boolTrue := true
	boolFalse := false
	extState := "Allowed"
	minLen := 8
	maxFailed := 5
	reuseLimit := 2
	maxInactivity := 15
	maxGrace := 5
	maxAge := 90
	acceptCookies := "Always"
	unpairingHour := 17
	extStorage := "Disallowed"
	deferral2 := 2
	deferral3 := 3
	deferral4 := 4
	deferral5 := 5

	cases := []struct {
		name       string
		identifier string
		config     any
	}{
		{
			name:       "PasscodeSettings",
			identifier: "com.jamf.ddm.passcode-settings",
			config: blueprints.PasscodeSettingsConfiguration{
				RequirePasscode: &blueprints.RequirePasscode{
					Included: &boolTrue,
					Value:    &boolTrue,
				},
				MinimumLength: &blueprints.MinimumLength{
					Included: &boolTrue,
					Value:    &minLen,
				},
				MaximumFailedAttempts: &blueprints.MaximumFailedAttempts{
					Included: &boolTrue,
					Value:    &maxFailed,
				},
				PasscodeReuseLimit: &blueprints.PasscodeReuseLimit{
					Included: &boolTrue,
					Value:    &reuseLimit,
				},
				MaximumInactivityInMinutes: &blueprints.MaximumInactivityInMinutes{
					Included: &boolTrue,
					Value:    &maxInactivity,
				},
				MaximumGracePeriodInMinutes: &blueprints.MaximumGracePeriodInMinutes{
					Included: &boolTrue,
					Value:    &maxGrace,
				},
				MaximumPasscodeAgeInDays: &blueprints.MaximumPasscodeAgeInDays{
					Included: &boolTrue,
					Value:    &maxAge,
				},
				RequireAlphanumericPasscode: &blueprints.RequireAlphanumericPasscode{
					Included: &boolTrue,
					Value:    &boolFalse,
				},
				Version: 2,
			},
		},
		{
			name:       "SoftwareUpdateSettings",
			identifier: "com.jamf.ddm.software-update-settings",
			config: blueprints.SoftwareUpdateSettingsConfiguration{
				AllowStandardUserOSUpdates: &blueprints.OptionallyEnabled{
					Included: &boolTrue,
					Enabled:  true,
				},
				AutomaticActions: &blueprints.AutomaticActions{
					Download: &blueprints.AutomaticAction{
						Included: &boolTrue,
						Value:    blueprints.AutomaticActionValueAlwaysOn,
					},
					InstallOSUpdates: &blueprints.AutomaticAction{
						Included: &boolTrue,
						Value:    blueprints.AutomaticActionValueAlwaysOn,
					},
					InstallSecurityUpdate: &blueprints.AutomaticAction{
						Included: &boolTrue,
						Value:    blueprints.AutomaticActionValueAlwaysOn,
					},
				},
				Beta: &blueprints.Beta{
					Included: &boolTrue,
					Value: &blueprints.BetaSettings{
						ProgramEnrollment: blueprints.BetaSettingsProgramEnrollmentAllowed,
						OfferPrograms: &[]blueprints.BetaProgram{
							{Token: new("test"), Description: new("test")},
						},
					},
				},
				Deferrals: &blueprints.Deferrals{
					CombinedPeriodInDays: &blueprints.OptionalPeriodInDays{Included: &boolTrue, Value: &deferral2},
					MajorPeriodInDays:    &blueprints.OptionalPeriodInDays{Included: &boolTrue, Value: &deferral3},
					MinorPeriodInDays:    &blueprints.OptionalPeriodInDays{Included: &boolTrue, Value: &deferral4},
					SystemPeriodInDays:   &blueprints.OptionalPeriodInDays{Included: &boolTrue, Value: &deferral5},
				},
				Notifications: &blueprints.OptionallyEnabled{
					Included: &boolTrue,
					Enabled:  false,
				},
				RapidSecurityResponse: &blueprints.RapidSecurityResponse{
					Enable:         &blueprints.OptionallyEnabled{Included: &boolTrue, Enabled: true},
					EnableRollback: &blueprints.OptionallyEnabled{Included: &boolTrue, Enabled: false},
				},
				RecommendedCadence: &blueprints.RecommendedCadence{
					Included: &boolTrue,
					Value:    blueprints.RecommendedCadenceValueNewest,
				},
			},
		},
		{
			name:       "SafariSettings",
			identifier: "com.jamf.ddm.safari-settings",
			config: blueprints.SafariSettingsConfiguration{
				AllowPopups: &blueprints.AllowPopups{
					Included: &boolTrue,
					Value:    &boolFalse,
				},
				AllowJavaScript: &blueprints.AllowJavaScript{
					Included: &boolTrue,
					Value:    &boolTrue,
				},
				AllowPrivateBrowsing: &blueprints.AllowPrivateBrowsing{
					Included: &boolTrue,
					Value:    &boolTrue,
				},
				AcceptCookies: &blueprints.AcceptCookies{
					Included: &boolTrue,
					Value:    &acceptCookies,
				},
			},
		},
		{
			name:       "SafariExtensions",
			identifier: "com.jamf.ddm.safari-extensions",
			config: blueprints.SafariExtensionsConfiguration{
				ManagedExtensions: map[string]blueprints.ManagedExtension{
					"com.example.test-extension": {State: &extState},
				},
			},
		},
		{
			name:       "SafariBookmarks",
			identifier: "com.jamf.ddm.safari-bookmarks",
			config: blueprints.SafariBookmarksConfiguration{
				ManagedBookmarks: []blueprints.BookmarkGroup{
					{
						Title:           "SDK Test Bookmarks",
						GroupIdentifier: "sdk-acc-test-group",
						Bookmarks: []blueprints.BookmarkItem{
							{Type: blueprints.BookmarkItemTypeBookmark, BOOKMARK: &blueprints.URLBookmarkItem{Type: blueprints.BookmarkItemTypeBookmark, Title: "Jamf", URL: "https://www.jamf.com"}},
						},
					},
				},
			},
		},
		{
			name:       "DiskManagement",
			identifier: "com.jamf.ddm.disk-management",
			config: blueprints.DiskManagementSettingsConfiguration{
				Restrictions: &blueprints.Restrictions{
					ExternalStorage: &blueprints.StorageMode{
						Included: &boolTrue,
						Value:    extStorage},
					NetworkStorage: &blueprints.StorageMode{
						Included: &boolTrue,
						Value:    extStorage},
				},
				Version: 2,
			},
		},
		{
			name:       "AudioAccessorySettings",
			identifier: "com.jamf.ddm.audio-accessory-settings",
			config: blueprints.AudioAccessorySettingsConfiguration{
				TemporaryPairing: &blueprints.TemporaryPairing{
					Included: &boolTrue,
					Disabled: &boolFalse,
					Configuration: &blueprints.TemporaryPairingConfig{
						UnpairingTime: blueprints.UnpairingTime{
							Policy: blueprints.UnpairingTimePolicyHour,
							Hour:   &unpairingHour,
						},
					},
				},
			},
		},
		{
			name:       "MathSettings",
			identifier: "com.jamf.ddm.math-settings",
			config: blueprints.MathSettingsConfiguration{
				Calculator: &blueprints.Calculator{
					BasicMode: &blueprints.BasicMode{
						Included:      &boolTrue,
						AddSquareRoot: true,
					},
					InputModes: &blueprints.InputModes{
						Included:       &boolTrue,
						RPN:            false,
						UnitConversion: true,
					},
				},
				SystemBehavior: &blueprints.SystemBehavior{
					Included:            &boolTrue,
					KeyboardSuggestions: true,
					MathNotes:           true,
				},
			},
		},
		{
			name:       "ConfigurationProfile",
			identifier: "com.jamf.ddm-configuration-profile",
			config: blueprints.ConfigurationProfileConfiguration{
				PayloadDisplayName: "SDK Test Profile",
				PayloadContent: []json.RawMessage{
					json.RawMessage(`{"payloadType":"com.apple.applicationaccess","payloadIdentifier":"9b924c9d-2f91-45e9-8c02-44bc11c61ce6","safariAllowJavaScript":false}`),
					json.RawMessage(`{"payloadType":"com.apple.preference.security","payloadIdentifier":"637b91c1-4e11-41b1-ba76-9a909485d919","dontAllowFireWallUI":true,"dontAllowPasswordResetUI":true}`),
				},
			},
		},
		{
			name:       "SwUpdate",
			identifier: "com.jamf.ddm.sw-updates",
			config: blueprints.SwUpdateAutomaticConfiguration{
				Strategy: blueprints.SwUpdateAutomaticConfigurationStrategySemantic,
				SEMANTIC: &blueprints.SwUpdateSemanticConfiguration{
					EnforcementType: "AUTOMATIC",
					Strategy:        blueprints.SwUpdateAutomaticConfigurationStrategySemantic,
					Rules: blueprints.UpdateRules{
						Minor: blueprints.UpdateRule{
							DeploymentTime:   "13:10",
							EnforceAfterDays: 0,
						},
					},
					DetailsURL: &blueprints.DetailsURL{
						Included: &boolFalse,
						Value:    new(""),
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !enabledIDs[tc.identifier] {
				t.Skipf("component %q not available on this tenant", tc.identifier)
			}

			steps := makeStep(tc.identifier, tc.config)
			bpName := "sdk-acc-typed-" + tc.name + "-" + runSuffix()
			bp := createTestBlueprint(t, c, bpName, groupID, steps)

			if len(bp.Steps) == 0 || len(bp.Steps[0].Components) == 0 {
				t.Fatal("expected at least one step with one component")
			}
			got := bp.Steps[0].Components[0].Identifier
			if got != tc.identifier {
				t.Errorf("expected identifier %q, got %q", tc.identifier, got)
			}
			t.Logf("Created %s blueprint ID: %s", tc.name, bp.ID)
		})
	}
}
