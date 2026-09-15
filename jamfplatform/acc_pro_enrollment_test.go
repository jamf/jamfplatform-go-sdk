// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// Batch 7a — device-enrollments + computer-prestages + mobile-device-
// prestages. The prestage CRUD tests exercise Jamf Pro's optimistic
// locking: the response's versionLock must be round-tripped on PUT, and
// a stale value surfaces as HTTP 409 OPTIMISTIC_LOCK_FAILED. Consumers
// (including the upcoming Terraform provider) are responsible for the
// read/modify/write cycle — the SDK just threads versionLock as a
// plain integer field through the request and response structs.

// depTokenBytes returns the raw S/MIME envelope bytes from the
// JAMFPLATFORM_ACC_PRO_DEP_TOKEN env var (base64 of the entire smime.p7m file
// as downloaded from Apple Business Manager). Skips the calling test
// when the env var is unset or malformed.
func depTokenBytes(t *testing.T) []byte {
	t.Helper()
	raw := strings.Join(strings.Fields(accEnv("JAMFPLATFORM_ACC_PRO_DEP_TOKEN")), "")
	if raw == "" {
		t.Skip("JAMFPLATFORM_ACC_PRO_DEP_TOKEN not set — DEP-token-dependent test skipped")
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Skipf("JAMFPLATFORM_ACC_PRO_DEP_TOKEN not valid base64: %v", err)
	}
	return decoded
}

// createDepInstance uploads a fresh DEP enrollment instance from the
// token env var and returns its id, or skips on server error / missing
// token. Registers a t.Cleanup to tear the instance down. The tests
// that use this helper get an isolated DEP instance per run.
func createDepInstance(t *testing.T, name string) string {
	t.Helper()
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	raw := depTokenBytes(t)
	label := "sdk-acc-dep-" + name + "-" + runSuffix()

	resp, err := p.UploadDeviceEnrollmentTokenV1(ctx, &pro.DeviceEnrollmentToken{
		TokenFileName: &label,
		EncodedToken:  &raw,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UploadDeviceEnrollmentTokenV1: %v", err)
	}
	if resp.ID == "" {
		t.Fatalf("UploadDeviceEnrollmentTokenV1 returned no ID (href=%q)", resp.Href)
	}
	id := resp.ID
	cleanupDelete(t, "DeleteDeviceEnrollmentV1", func() error { return p.DeleteDeviceEnrollmentV1(ctx, id) })
	return id
}

// --- device-enrollments -------------------------------------------------

func TestAcceptance_Pro_Enrollment_ListDeviceEnrollmentsV1(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListDeviceEnrollmentsV1(context.Background(), nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDeviceEnrollmentsV1: %v", err)
	}
	t.Logf("Found %d DEP enrollment instances", len(items))
}

func TestAcceptance_Pro_Enrollment_GetPublicKeyV1(t *testing.T) {
	c := accClient(t)

	body, err := pro.New(c).GetDeviceEnrollmentPublicKeyV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDeviceEnrollmentPublicKeyV1: %v", err)
	}
	if !strings.Contains(string(body), "BEGIN") {
		t.Errorf("public key body does not look like a PEM block: %q", body)
	}
	t.Logf("Public key PEM: %d bytes", len(body))
}

func TestAcceptance_Pro_Enrollment_ListAllSyncsV1(t *testing.T) {
	c := accClient(t)

	syncs, err := pro.New(c).ListAllDeviceEnrollmentSyncsV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListAllDeviceEnrollmentSyncsV1: %v", err)
	}
	t.Logf("Found %d DEP sync records", len(syncs))
}

// TestAcceptance_Pro_Enrollment_DeviceEnrollmentInstanceLifecycle uploads a
// fresh DEP token, reads it back, lists devices + syncs, writes a history
// note, reads history, then tears down.
func TestAcceptance_Pro_Enrollment_DeviceEnrollmentInstanceLifecycle(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	id := createDepInstance(t, "lifecycle")
	t.Logf("Uploaded DEP instance %s", id)

	// Round-trip GET.
	got, err := p.GetDeviceEnrollmentV1(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDeviceEnrollmentV1(%s): %v", id, err)
	}
	t.Logf("DEP %s: name=%q", id, got.Name)

	// Listing devices — likely 0 on a test DEP token.
	devices, err := p.ListDeviceEnrollmentDevicesV1(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListDeviceEnrollmentDevicesV1(%s): %v", id, err)
	} else {
		t.Logf("DEP %s exposes %d devices", id, devices.TotalCount)
	}

	// Per-instance syncs.
	if _, err := p.ListDeviceEnrollmentSyncsV1(ctx, id); err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListDeviceEnrollmentSyncsV1(%s): %v", id, err)
	}
	if _, err := p.GetLatestDeviceEnrollmentSyncV1(ctx, id); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Logf("GetLatestDeviceEnrollmentSyncV1(%s): 404 — no sync yet, plumbing OK", id)
		} else {
			skipOnServerError(t, err)
			t.Errorf("GetLatestDeviceEnrollmentSyncV1(%s): %v", id, err)
		}
	}

	// History round-trip.
	if _, err := p.CreateDeviceEnrollmentHistoryNoteV1(ctx, id, &pro.ObjectHistoryNote{
		Note: "sdk-acc test history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Errorf("CreateDeviceEnrollmentHistoryNoteV1(%s): %v", id, err)
	}
	hist, err := p.ListDeviceEnrollmentHistoryV1(ctx, id, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListDeviceEnrollmentHistoryV1(%s): %v", id, err)
	} else {
		t.Logf("DEP %s history: %d entries", id, len(hist))
	}
}

// --- computer-prestages — optimistic locking focus ---------------------

func TestAcceptance_Pro_Enrollment_ListComputerPrestagesV3(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListComputerPrestagesV3(context.Background(), nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListComputerPrestagesV3: %v", err)
	}
	t.Logf("Found %d computer prestages", len(items))
}

// TestAcceptance_Pro_Enrollment_ComputerPrestageCRUDAndOptimisticLock covers:
// 1. Upload DEP token, create prestage tied to the fresh instance.
// 2. Get — capture versionLock.
// 3. Update with fresh versionLock — expect 200, new versionLock > old.
// 4. Update with STALE versionLock — expect 409 OPTIMISTIC_LOCK_FAILED.
// 5. Read scope (empty), add a throwaway serial, read again, remove.
// 6. Delete prestage, verify 404.
//
// Cleanup chain tears down scope additions (via delete-multiple), the
// prestage itself, and the DEP instance.
func TestAcceptance_Pro_Enrollment_ComputerPrestageCRUDAndOptimisticLock(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	depID := createDepInstance(t, "computer-prestage")

	name := "sdk-acc-computer-prestage-" + runSuffix()

	// AccountSettings must be populated with server-side defaults — the
	// server 500s when the field is absent and rejects payloadConfigured
	// interactions when non-default parameters are supplied without it.
	// These values mirror what GET returns on an existing prestage with
	// default account settings.
	emptyStr := ""
	falseVal := false
	zeroStr := "0"
	zeroVersion := 0
	prefillCustom := "CUSTOM"
	userAccountAdmin := "ADMINISTRATOR"

	created, err := p.CreateComputerPrestageV3(ctx, &pro.PostComputerPrestageV3{
		DisplayName:                       name,
		DeviceEnrollmentProgramInstanceID: depID,
		AccountSettings: &pro.AccountSettingsRequest{
			ID:                                      &zeroStr,
			VersionLock:                             &zeroVersion,
			PayloadConfigured:                       &falseVal,
			HiddenAdminAccount:                      &falseVal,
			LocalAdminAccountEnabled:                &falseVal,
			LocalUserManaged:                        &falseVal,
			AdminUsername:                           &emptyStr,
			PrefillAccountFullName:                  &emptyStr,
			PrefillAccountUserName:                  &emptyStr,
			PrefillPrimaryAccountInfoFeatureEnabled: &falseVal,
			PrefillType:                             &prefillCustom,
			PreventPrefillInfoFromModification:      &falseVal,
			UserAccountType:                         &userAccountAdmin,
		},
		DefaultPrestage:                    false,
		Mandatory:                          false,
		MDMRemovable:                       true,
		AutoAdvanceSetup:                   false,
		PreventActivationLock:              false,
		EnableDeviceBasedActivationLock:    false,
		InstallProfilesDuringSetup:         false,
		KeepExistingSiteMembership:         false,
		KeepExistingLocationInformation:    false,
		RequireAuthentication:              false,
		EnableRecoveryLock:                 &falseVal,
		RotateRecoveryLockPassword:         &falseVal,
		RecoveryLockPasswordType:           ptr(pro.PostComputerPrestageV3RecoveryLockPasswordTypeManual),
		EnrollmentSiteID:                   "-1",
		CustomPackageDistributionPointID:   "-1",
		EnrollmentCustomizationID:          &zeroStr,
		PrestageMinimumOsTargetVersionType: ptr(pro.PostComputerPrestageV3PrestageMinimumOsTargetVersionTypeNoEnforcement),
		SkipSetupItems:                     &map[string]bool{},
		AnchorCertificates:                 &[]string{},
		CustomPackageIds:                   []string{},
		PrestageInstalledProfileIds:        []string{},
		LocationInformation: pro.LocationInformationV2{
			ID:           "-1",
			VersionLock:  0,
			DepartmentID: "-1",
			BuildingID:   "-1",
		},
		PurchasingInformation: pro.PrestagePurchasingInformationV2{
			ID:           "-1",
			VersionLock:  0,
			Purchased:    true,
			LeaseDate:    "1970-01-01",
			PoDate:       "1970-01-01",
			WarrantyDate: "1970-01-01",
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateComputerPrestageV3: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateComputerPrestageV3 returned no ID")
	}
	cleanupDelete(t, "DeleteComputerPrestageV3", func() error { return p.DeleteComputerPrestageV3(ctx, created.ID) })
	t.Logf("Created computer prestage %s", created.ID)

	// Optimistic locking: read → modify → put.
	got, err := p.GetComputerPrestageV3(ctx, created.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerPrestageV3(%s): %v", created.ID, err)
	}
	initialVersion := got.VersionLock
	t.Logf("Prestage %s initial versionLock=%d", created.ID, initialVersion)

	put := putFromGetComputerPrestage(t, got)
	put.DisplayName = name + "-updated"

	updated, err := p.UpdateComputerPrestageV3(ctx, created.ID, put)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateComputerPrestageV3(%s) with fresh versionLock: %v", created.ID, err)
	}
	if updated.VersionLock <= initialVersion {
		t.Errorf("versionLock did not increment after update: was %d, now %d", initialVersion, updated.VersionLock)
	}
	t.Logf("After update: versionLock=%d", updated.VersionLock)

	// Stale versionLock — expect 409 OPTIMISTIC_LOCK_FAILED.
	stale := putFromGetComputerPrestage(t, got) // still carries OLD versionLock
	stale.DisplayName = name + "-stale-attempt"
	_, err = p.UpdateComputerPrestageV3(ctx, created.ID, stale)
	if err == nil {
		t.Fatalf("UpdateComputerPrestageV3(%s) with stale versionLock=%d should have failed with 409", created.ID, got.VersionLock)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(409) {
		t.Fatalf("UpdateComputerPrestageV3 stale: want 409, got %v", err)
	}
	t.Logf("Stale versionLock rejected as expected: status=%d", apiErr.StatusCode)

	// Scope sub-resource — exercise add + remove against versionLock.
	scope, err := p.GetComputerPrestageScopeV2(ctx, created.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerPrestageScopeV2(%s): %v", created.ID, err)
	}
	scopeVersion := scope.VersionLock

	// Scope add probe. A real device serial must exist on the DEP token
	// for the server to accept the addition; a fabricated serial gets a
	// 400 DEVICE_DOES_NOT_EXIST_ON_TOKEN which still confirms the
	// transport + versionLock plumbing. If the tenant happens to have an
	// assigned serial via the existing scope map, use that; otherwise
	// treat 400 as a successful probe.
	probeSerial := "SDKACC" + runSuffix()
	if all, err := p.GetAllComputerPrestageScopeV2(ctx); err == nil {
		for _, v := range all.SerialsByPrestageID {
			if v != "" {
				probeSerial = v
				break
			}
		}
	}
	addResp, err := p.AddToComputerPrestageScopeV2(ctx, created.ID, &pro.PrestageScopeUpdate{
		SerialNumbers: []string{probeSerial},
		VersionLock:   scopeVersion,
	})
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(400) && strings.Contains(err.Error(), "DEVICE_DOES_NOT_EXIST_ON_TOKEN") {
			t.Logf("Scope add rejected — serial %q not on the DEP token (expected for fabricated serial); transport + versionLock plumbing exercised", probeSerial)
		} else {
			skipOnServerError(t, err)
			t.Fatalf("AddToComputerPrestageScopeV2(%s): %v", created.ID, err)
		}
	} else {
		if addResp.VersionLock <= scopeVersion {
			t.Errorf("scope versionLock did not advance after add: was %d, now %d", scopeVersion, addResp.VersionLock)
		}
		// Remove (delete-multiple) — uses the new versionLock from add.
		if _, err := p.RemoveFromComputerPrestageScopeV2(ctx, created.ID, &pro.PrestageScopeUpdate{
			SerialNumbers: []string{probeSerial},
			VersionLock:   addResp.VersionLock,
		}); err != nil {
			skipOnServerError(t, err)
			t.Errorf("RemoveFromComputerPrestageScopeV2(%s): %v", created.ID, err)
		}
	}

	// Delete prestage → verify 404.
	if err := p.DeleteComputerPrestageV3(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteComputerPrestageV3(%s): %v", created.ID, err)
	}
	_, err = p.GetComputerPrestageV3(ctx, created.ID)
	if err == nil {
		t.Fatalf("GetComputerPrestageV3(%s) after delete should 404", created.ID)
	}
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetComputerPrestageV3(%s) after delete: want 404, got %v", created.ID, err)
	}
}

// putFromGetComputerPrestage translates a Get response into a Put
// request, threading the versionLock verbatim. Mirrors the consumer
// pattern a Terraform provider uses: Read → transform to write body →
// send with the versionLock we just observed. Uses JSON round-trip
// because the generator flattens allOf compositions — Get and Put
// structs share JSON field names but aren't Go-assignable directly.
func putFromGetComputerPrestage(t *testing.T, g *pro.GetComputerPrestageV3) *pro.PutComputerPrestageV3 {
	t.Helper()
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal Get prestage: %v", err)
	}
	var put pro.PutComputerPrestageV3
	if err := json.Unmarshal(b, &put); err != nil {
		t.Fatalf("unmarshal into Put prestage: %v", err)
	}
	return &put
}

// --- mobile-device-prestages -------------------------------------------

func TestAcceptance_Pro_Enrollment_ListMobileDevicePrestagesV3(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListMobileDevicePrestagesV3(context.Background(), nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListMobileDevicePrestagesV3: %v", err)
	}
	t.Logf("Found %d mobile device prestages", len(items))
}

// TestAcceptance_Pro_Enrollment_ListAllScopes exercises the tenant-wide
// scope aggregation endpoints for both prestage families.
func TestAcceptance_Pro_Enrollment_ListAllScopes(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	cScope, err := p.GetAllComputerPrestageScopeV2(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetAllComputerPrestageScopeV2: %v", err)
	}
	t.Logf("Computer prestage scope map: %d prestages with assignments",
		len(cScope.SerialsByPrestageID))

	mScope, err := p.GetAllMobileDevicePrestageScopeV2(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetAllMobileDevicePrestageScopeV2: %v", err)
	}
	t.Logf("Mobile device prestage scope map: %d prestages with assignments",
		len(mScope.SerialsByPrestageID))

	syncs, err := p.ListAllMobileDevicePrestageSyncsV2(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListAllMobileDevicePrestageSyncsV2: %v", err)
	}
	t.Logf("Mobile device prestage sync records: %d", len(syncs))
}
