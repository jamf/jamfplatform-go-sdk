// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/pro"
)

// Batch 6 — computer-inventory, computer-inventory-collection-settings,
// inventory-preload, inventory-information.
//
// Destructive endpoints (EraseComputerV4, RemoveMdmProfileFromComputerV4)
// are permanently skipped here. They
// affect real managed computers — no automated probe is safe. Manual curl
// verification against a real target is the only path if needed.

// --- inventory-information ---------------------------------------------

func TestAcceptance_Pro_Inventory_GetInformationV1(t *testing.T) {
	c := accClient(t)

	info, err := pro.New(c).GetInventoryInformationV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetInventoryInformationV1: %v", err)
	}
	t.Logf("Inventory info: managedComputers=%d managedDevices=%d unmanagedComputers=%d unmanagedDevices=%d",
		info.ManagedComputers, info.ManagedDevices, info.UnmanagedComputers, info.UnmanagedDevices)
}

// --- computer-inventory V3 (read chain) --------------------------------
//
// v1942 withdrew V1, V2 and V3 from the published spec; v2082 brought V3 back
// (upstream's own restoration change) on the grounds that its 2026-07-14 deprecation left
// callers no reasonable window to reach V4. V1 and V2 stay withdrawn, so the
// two destructive V1 stubs that used to sit in this section are gone with
// them. Wire-confirmed 2026-09-04: GET /pro/v3/computers-inventory answers 200
// with a body byte-identical to V4's for the same tenant.

func TestAcceptance_Pro_Inventory_ListComputersV3(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListComputersInventoryV3(context.Background(), nil, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListComputersInventoryV3: %v", err)
	}
	t.Logf("Found %d computers", len(items))
}

func TestAcceptance_Pro_Inventory_ListComputerFileVaultsV3(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListComputerInventoryFileVaultsV3(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListComputerInventoryFileVaultsV3: %v", err)
	}
	t.Logf("Found %d computer FileVault records", len(items))
}

// TestAcceptance_Pro_Inventory_ComputerReadChainV3 exercises per-computer
// read endpoints (get, get-detail, filevault, view-device-lock-pin,
// view-recovery-lock-password) against the first computer the tenant has,
// if any. Sensitive reads (PIN / recovery password) fire but the values
// are not logged.
func TestAcceptance_Pro_Inventory_ComputerReadChainV3(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	computers, err := p.ListComputersInventoryV3(ctx, nil, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListComputersInventoryV3: %v", err)
	}
	if len(computers) == 0 {
		t.Skip("tenant has no computers — no read probes possible")
	}
	id := computers[0].ID

	if _, err := p.GetComputerInventoryV3(ctx, id, nil); err != nil {
		skipOnServerError(t, err)
		t.Errorf("GetComputerInventoryV3(%s): %v", id, err)
	}
	if _, err := p.GetComputerInventoryDetailV3(ctx, id); err != nil {
		skipOnServerError(t, err)
		t.Errorf("GetComputerInventoryDetailV3(%s): %v", id, err)
	}

	// Per-device FileVault — 404 if the machine isn't encrypted, plumbing ok.
	if _, err := p.GetComputerInventoryFileVaultV3(ctx, id); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Logf("GetComputerInventoryFileVaultV3(%s): 404 — no FileVault record, plumbing OK", id)
		} else {
			skipOnServerError(t, err)
			t.Errorf("GetComputerInventoryFileVaultV3(%s): %v", id, err)
		}
	}

	// Device lock PIN and recovery lock password — sensitive. Plumbing only;
	// do not log values. 4xx on no-PIN-set is acceptable.
	if _, err := p.GetComputerDeviceLockPinV3(ctx, id); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("GetComputerDeviceLockPinV3(%s): %d — no PIN set or not eligible, plumbing OK", id, apiErr.StatusCode)
		} else {
			skipOnServerError(t, err)
			t.Errorf("GetComputerDeviceLockPinV3(%s): %v", id, err)
		}
	} else {
		t.Logf("GetComputerDeviceLockPinV3(%s): ok (value not logged)", id)
	}

	if _, err := p.GetComputerRecoveryLockPasswordV3(ctx, id); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("GetComputerRecoveryLockPasswordV3(%s): %d — no password set or not eligible, plumbing OK", id, apiErr.StatusCode)
		} else {
			skipOnServerError(t, err)
			t.Errorf("GetComputerRecoveryLockPasswordV3(%s): %v", id, err)
		}
	} else {
		t.Logf("GetComputerRecoveryLockPasswordV3(%s): ok (value not logged)", id)
	}
}

// TestAcceptance_Pro_Inventory_ComputerCRUDV3 exercises full CRUD against a
// synthetic computer inventory record seeded with a sdk-acc-* UDID. Server
// accepts a minimal create body; no real managed computer is touched.
// Covers create, get, detail update (PATCH), attachment upload, attachment
// download, attachment delete, and record delete.
func TestAcceptance_Pro_Inventory_ComputerCRUDV3(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	udid := "sdk-acc-udid-" + runSuffix()
	name := "sdk-acc-mac-" + runSuffix()

	created, err := p.CreateComputerInventoryV3(ctx, &pro.ComputerInventoryCreateRequestV2{
		UDID: &udid,
		General: &pro.ComputerGeneralCreate{
			Name: name,
		},
		Hardware: &pro.ComputerHardwareCreate{
			Make:            ptr("Apple"),
			Model:           ptr("SDK Acceptance Virtual"),
			ModelIdentifier: ptr("SDKAcc1,1"),
		},
		OperatingSystem: &pro.ComputerOperatingSystemCreate{
			Name:    ptr("macOS"),
			Version: ptr("14.0"),
			Build:   ptr("23A344"),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateComputerInventoryV3: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateComputerInventoryV3 returned no ID (href=%q)", created.Href)
	}
	cleanupDelete(t, "DeleteComputerInventoryV3", func() error { return p.DeleteComputerInventoryV3(ctx, created.ID) })
	t.Logf("Created computer inventory record %s (udid=%s)", created.ID, udid)

	// Round-trip verify.
	got, err := p.GetComputerInventoryV3(ctx, created.ID, []string{"GENERAL", "HARDWARE"})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerInventoryV3(%s): %v", created.ID, err)
	}
	if got.UDID != udid {
		t.Errorf("UDID = %q, want %q", got.UDID, udid)
	}

	// PATCH detail — returns 204.
	assetTag := "sdk-acc-tag-" + runSuffix()
	if err := p.UpdateComputerInventoryDetailV3(ctx, created.ID, &pro.ComputerInventoryUpdateRequest{
		General: &pro.ComputerGeneralUpdate{AssetTag: &assetTag},
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateComputerInventoryDetailV3(%s): %v", created.ID, err)
	}

	// Upload attachment (inline bytes).
	body := "sdk-acc attachment probe payload " + runSuffix()
	att, err := p.UploadComputerInventoryAttachmentV3(ctx, created.ID, "probe.txt", strings.NewReader(body))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UploadComputerInventoryAttachmentV3(%s): %v", created.ID, err)
	}
	t.Logf("Uploaded attachment %s", att.ID)

	// Download the attachment — returns text/plain bytes.
	downloaded, err := p.DownloadComputerInventoryAttachmentV3(ctx, created.ID, att.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DownloadComputerInventoryAttachmentV3(%s, %s): %v", created.ID, att.ID, err)
	}
	if !strings.Contains(string(downloaded), "sdk-acc attachment probe") {
		t.Errorf("Download body %q does not contain expected probe string", downloaded)
	}

	// Delete attachment.
	if err := p.DeleteComputerInventoryAttachmentV3(ctx, created.ID, att.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteComputerInventoryAttachmentV3(%s, %s): %v", created.ID, att.ID, err)
	}

	// Delete record.
	if err := p.DeleteComputerInventoryV3(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteComputerInventoryV3(%s): %v", created.ID, err)
	}

	// Verify gone.
	_, err = p.GetComputerInventoryV3(ctx, created.ID, nil)
	if err == nil {
		t.Fatalf("GetComputerInventoryV3(%s) after delete should 404", created.ID)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetComputerInventoryV3(%s) after delete: want 404, got %v", created.ID, err)
	}
}

// --- computer-inventory V4 (read chain) --------------------------------
//
// V4 is the successor to the V1/V2/V3 computers-inventory surface, all of
// which Jamf marked deprecated with a 2026-07-14 date in the 11.30.0 spec
// (issue #50). The V4 tests below are deliberate duplicates of the V3 ones:
// the point of retaining both is that a consumer can diff them to see the
// migration is shape-for-shape, and a V4 regression is caught even while
// the V3 tests still pass.

func TestAcceptance_Pro_Inventory_ListComputersV4(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListComputersInventoryV4(context.Background(), nil, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListComputersInventoryV4: %v", err)
	}
	t.Logf("Found %d computers", len(items))
}

func TestAcceptance_Pro_Inventory_ListComputerFileVaultsV4(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListComputerInventoryFileVaultsV4(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListComputerInventoryFileVaultsV4: %v", err)
	}
	t.Logf("Found %d computer FileVault records", len(items))
}

// TestAcceptance_Pro_Inventory_ComputerReadChainV4 mirrors the V3 read chain
// against V4. Sensitive reads (PIN / recovery password) fire but the values
// are not logged.
func TestAcceptance_Pro_Inventory_ComputerReadChainV4(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	computers, err := p.ListComputersInventoryV4(ctx, nil, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListComputersInventoryV4: %v", err)
	}
	if len(computers) == 0 {
		t.Skip("tenant has no computers — no read probes possible")
	}
	id := computers[0].ID

	if _, err := p.GetComputerInventoryV4(ctx, id, nil); err != nil {
		skipOnServerError(t, err)
		t.Errorf("GetComputerInventoryV4(%s): %v", id, err)
	}
	if _, err := p.GetComputerInventoryDetailV4(ctx, id); err != nil {
		skipOnServerError(t, err)
		t.Errorf("GetComputerInventoryDetailV4(%s): %v", id, err)
	}

	// Per-device FileVault — 404 if the machine isn't encrypted, plumbing ok.
	if _, err := p.GetComputerInventoryFileVaultV4(ctx, id); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Logf("GetComputerInventoryFileVaultV4(%s): 404 — no FileVault record, plumbing OK", id)
		} else {
			skipOnServerError(t, err)
			t.Errorf("GetComputerInventoryFileVaultV4(%s): %v", id, err)
		}
	}

	// Device lock PIN and recovery lock password — sensitive. Plumbing only;
	// do not log values. 4xx on no-PIN-set is acceptable.
	if _, err := p.GetComputerDeviceLockPinV4(ctx, id); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("GetComputerDeviceLockPinV4(%s): %d — no PIN set or not eligible, plumbing OK", id, apiErr.StatusCode)
		} else {
			skipOnServerError(t, err)
			t.Errorf("GetComputerDeviceLockPinV4(%s): %v", id, err)
		}
	} else {
		t.Logf("GetComputerDeviceLockPinV4(%s): ok (value not logged)", id)
	}

	if _, err := p.GetComputerRecoveryLockPasswordV4(ctx, id); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("GetComputerRecoveryLockPasswordV4(%s): %d — no password set or not eligible, plumbing OK", id, apiErr.StatusCode)
		} else {
			skipOnServerError(t, err)
			t.Errorf("GetComputerRecoveryLockPasswordV4(%s): %v", id, err)
		}
	} else {
		t.Logf("GetComputerRecoveryLockPasswordV4(%s): ok (value not logged)", id)
	}
}

// Destructive: permanently skipped. V4 serves these two actions under
// /computers-inventory/{id}/… (plural); the singular
// /computer-inventory/{id}/… forms were withdrawn from the spec at v1942.
func TestAcceptance_Pro_Inventory_EraseComputerV4(t *testing.T) {
	t.Skip("destructive (wipes a real computer) — no automated probe is safe; verify manually via curl against a known disposable target if needed")
}

func TestAcceptance_Pro_Inventory_RemoveMdmProfileFromComputerV4(t *testing.T) {
	t.Skip("destructive (removes MDM from a real computer, requires re-enrollment) — verify manually via curl")
}

// TestAcceptance_Pro_Inventory_ComputerCRUDV4 exercises the full CRUD
// lifecycle against a synthetic sdk-acc-* record. V4 has its own suffixed
// create schema (ComputerInventoryCreateRequestV4 / ComputerGeneralCreateV4)
// and renamed two general fields against the withdrawn V1–V3 body —
// lastContactTime → lastContact, lastReportedIp → lastCheckIn — so migrating
// callers is not a drop-in type swap.
func TestAcceptance_Pro_Inventory_ComputerCRUDV4(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	udid := "sdk-acc-udid-v4-" + runSuffix()
	name := "sdk-acc-mac-v4-" + runSuffix()

	created, err := p.CreateComputerInventoryV4(ctx, &pro.ComputerInventoryCreateRequestV4{
		UDID: &udid,
		General: &pro.ComputerGeneralCreateV4{
			Name: name,
		},
		Hardware: &pro.ComputerHardwareCreate{
			Make:            new("Apple"),
			Model:           new("SDK Acceptance Virtual"),
			ModelIdentifier: new("SDKAcc1,1"),
		},
		OperatingSystem: &pro.ComputerOperatingSystemCreate{
			Name:    new("macOS"),
			Version: new("14.0"),
			Build:   new("23A344"),
		},
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateComputerInventoryV4: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateComputerInventoryV4 returned no ID (href=%q)", created.Href)
	}
	cleanupDelete(t, "DeleteComputerInventoryV4", func() error { return p.DeleteComputerInventoryV4(ctx, created.ID) })
	t.Logf("Created computer inventory record %s (udid=%s)", created.ID, udid)

	// Round-trip verify.
	got, err := p.GetComputerInventoryV4(ctx, created.ID, []string{"GENERAL", "HARDWARE"})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerInventoryV4(%s): %v", created.ID, err)
	}
	if got.UDID != udid {
		t.Errorf("UDID = %q, want %q", got.UDID, udid)
	}

	// PATCH detail — returns 204.
	assetTag := "sdk-acc-tag-" + runSuffix()
	if err := p.UpdateComputerInventoryDetailV4(ctx, created.ID, &pro.ComputerInventoryUpdateRequest{
		General: &pro.ComputerGeneralUpdate{AssetTag: &assetTag},
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateComputerInventoryDetailV4(%s): %v", created.ID, err)
	}

	// Upload attachment (inline bytes).
	body := "sdk-acc attachment probe payload " + runSuffix()
	att, err := p.UploadComputerInventoryAttachmentV4(ctx, created.ID, "probe.txt", strings.NewReader(body))
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UploadComputerInventoryAttachmentV4(%s): %v", created.ID, err)
	}
	t.Logf("Uploaded attachment %s", att.ID)

	// Download the attachment — returns text/plain bytes.
	downloaded, err := p.DownloadComputerInventoryAttachmentV4(ctx, created.ID, att.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DownloadComputerInventoryAttachmentV4(%s, %s): %v", created.ID, att.ID, err)
	}
	if !strings.Contains(string(downloaded), "sdk-acc attachment probe") {
		t.Errorf("Download body %q does not contain expected probe string", downloaded)
	}

	// Delete attachment.
	if err := p.DeleteComputerInventoryAttachmentV4(ctx, created.ID, att.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteComputerInventoryAttachmentV4(%s, %s): %v", created.ID, att.ID, err)
	}

	// Delete record.
	if err := p.DeleteComputerInventoryV4(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteComputerInventoryV4(%s): %v", created.ID, err)
	}

	// Verify gone.
	_, err = p.GetComputerInventoryV4(ctx, created.ID, nil)
	if err == nil {
		t.Fatalf("GetComputerInventoryV4(%s) after delete should 404", created.ID)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetComputerInventoryV4(%s) after delete: want 404, got %v", created.ID, err)
	}
}

// --- computer-inventory-collection-settings V2 --------------------------

func TestAcceptance_Pro_Inventory_GetCollectionSettingsV2(t *testing.T) {
	c := accClient(t)

	settings, err := pro.New(c).GetComputerInventoryCollectionSettingsV2(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerInventoryCollectionSettingsV2: %v", err)
	}
	paths := 0
	if settings.ApplicationPaths != nil {
		paths = len(*settings.ApplicationPaths)
	}
	t.Logf("Collection settings: %d application paths", paths)
}

// TestAcceptance_Pro_Inventory_UpdateCollectionSettingsV2 round-trips the
// current settings back to the server, confirming the PATCH plumbing works
// without mutating state.
func TestAcceptance_Pro_Inventory_UpdateCollectionSettingsV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetComputerInventoryCollectionSettingsV2(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetComputerInventoryCollectionSettingsV2: %v", err)
	}
	if err := p.UpdateComputerInventoryCollectionSettingsV2(ctx, current); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateComputerInventoryCollectionSettingsV2: %v", err)
	}
}

// TestAcceptance_Pro_Inventory_CollectionCustomPathCRUDV2 creates a custom
// application-inventory path, then deletes it.
func TestAcceptance_Pro_Inventory_CollectionCustomPathCRUDV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	// scope accepts a single enum value: "APP". The spec comment mentions
	// "ALL" but server-side enforcement rejects anything but APP.
	created, err := p.CreateComputerInventoryCollectionCustomPathV2(ctx, &pro.CreatePathV2{
		Path:  "/Applications/sdk-acc-" + runSuffix() + ".app",
		Scope: "APP",
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateComputerInventoryCollectionCustomPathV2: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateComputerInventoryCollectionCustomPathV2 returned no ID")
	}
	cleanupDelete(t, "DeleteComputerInventoryCollectionCustomPathV2", func() error {
		return p.DeleteComputerInventoryCollectionCustomPathV2(ctx, created.ID)
	})
	t.Logf("Created custom path %s", created.ID)

	if err := p.DeleteComputerInventoryCollectionCustomPathV2(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteComputerInventoryCollectionCustomPathV2(%s): %v", created.ID, err)
	}
}

// --- inventory-preload V2 -----------------------------------------------

func TestAcceptance_Pro_Inventory_PreloadMetaV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	// CSV template download.
	tpl, err := p.DownloadInventoryPreloadCsvTemplateV2(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DownloadInventoryPreloadCsvTemplateV2: %v", err)
	}
	if len(tpl) == 0 {
		t.Error("CSV template body empty")
	} else {
		firstLine := string(tpl)
		if nl := strings.IndexByte(firstLine, '\n'); nl >= 0 {
			firstLine = firstLine[:nl]
		}
		if !strings.Contains(strings.ToLower(firstLine), "serial") {
			t.Errorf("CSV template header %q does not contain 'serial'", firstLine)
		}
		t.Logf("CSV template: %d bytes; header: %s", len(tpl), firstLine)
	}

	// EA columns.
	eas, err := p.ListInventoryPreloadExtensionAttributeColumnsV2(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListInventoryPreloadExtensionAttributeColumnsV2: %v", err)
	}
	t.Logf("Inventory preload EA columns: %d (totalCount=%d)", len(eas.Results), eas.TotalCount)
}

func TestAcceptance_Pro_Inventory_PreloadCsvV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	// Download whatever's currently there (may be empty).
	body, err := p.DownloadInventoryPreloadCsvV2(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DownloadInventoryPreloadCsvV2: %v", err)
	}
	t.Logf("Current inventory-preload CSV: %d bytes", len(body))

	// Validate a minimal CSV — should succeed with a single valid row.
	csv := "EA Department,Serial Number,Device Type\n,sdk-acc-" + runSuffix() + ",Computer\n"
	if _, err := p.ValidateInventoryPreloadCsvV2(ctx, "probe.csv", strings.NewReader(csv)); err != nil {
		skipOnServerError(t, err)
		// Validation errors are expected in some tenant configs — log + move on.
		if apiErr, ok := errors.AsType[*jamfplatform.APIResponseError](err); ok {
			t.Logf("ValidateInventoryPreloadCsvV2 rejected probe row: status=%d", apiErr.StatusCode)
		} else {
			t.Errorf("ValidateInventoryPreloadCsvV2: %v", err)
		}
	} else {
		t.Logf("ValidateInventoryPreloadCsvV2: accepted")
	}
}

func TestAcceptance_Pro_Inventory_PreloadRecordCRUDV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	serial := "sdkacc" + runSuffix()
	dept := "SDK Acceptance"

	created, err := p.CreateInventoryPreloadRecordV2(ctx, &pro.InventoryPreloadRecordV2{
		SerialNumber: serial,
		DeviceType:   pro.InventoryPreloadRecordV2DeviceTypeComputer,
		Department:   &dept,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateInventoryPreloadRecordV2: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateInventoryPreloadRecordV2 returned no ID")
	}
	cleanupDelete(t, "DeleteInventoryPreloadRecordV2", func() error { return p.DeleteInventoryPreloadRecordV2(ctx, created.ID) })
	t.Logf("Created preload record %s (%s)", created.ID, serial)

	got, err := p.GetInventoryPreloadRecordV2(ctx, created.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetInventoryPreloadRecordV2(%s): %v", created.ID, err)
	}
	if got.SerialNumber != serial {
		t.Errorf("SerialNumber = %q, want %q", got.SerialNumber, serial)
	}

	newDept := dept + " (updated)"
	got.Department = &newDept
	updated, err := p.UpdateInventoryPreloadRecordV2(ctx, created.ID, got)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateInventoryPreloadRecordV2(%s): %v", created.ID, err)
	}
	if updated.Department == nil || *updated.Department != newDept {
		t.Errorf("Department = %v, want %q", updated.Department, newDept)
	}

	if err := p.DeleteInventoryPreloadRecordV2(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteInventoryPreloadRecordV2(%s): %v", created.ID, err)
	}

	_, err = p.GetInventoryPreloadRecordV2(ctx, created.ID)
	if err == nil {
		t.Fatalf("GetInventoryPreloadRecordV2(%s) after delete should 404", created.ID)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetInventoryPreloadRecordV2(%s) after delete: want 404, got %v", created.ID, err)
	}
}

func TestAcceptance_Pro_Inventory_PreloadHistoryV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	if _, err := p.CreateInventoryPreloadHistoryNoteV2(ctx, &pro.ObjectHistoryNote{
		Note: "sdk-acc test history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateInventoryPreloadHistoryNoteV2: %v", err)
	}

	hist, err := p.ListInventoryPreloadHistoryV2(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListInventoryPreloadHistoryV2: %v", err)
	}
	t.Logf("Inventory preload history: %d entries", len(hist))
}

// TestAcceptance_Pro_Inventory_PreloadUploadCsvV2 exercises the CSV upload
// endpoint — server will overwrite matching serial numbers. Uses a
// throwaway serial so existing tenant data is unaffected.
func TestAcceptance_Pro_Inventory_PreloadUploadCsvV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	serial := "sdkacc" + runSuffix()
	csv := "EA Department,Serial Number,Device Type\n,\"" + serial + "\",Computer\n"

	resp, err := p.UploadInventoryPreloadCsvV2(ctx, "probe.csv", strings.NewReader(csv))
	if err != nil {
		skipOnServerError(t, err)
		// CSV upload may 400 due to tenant-specific validation rules; plumbing
		// is the only thing exercised here.
		if apiErr, ok := errors.AsType[*jamfplatform.APIResponseError](err); ok {
			t.Logf("UploadInventoryPreloadCsvV2 rejected probe: status=%d", apiErr.StatusCode)
			return
		}
		t.Fatalf("UploadInventoryPreloadCsvV2: %v", err)
	}
	// Clean up whichever record the CSV produced. The endpoint returns an
	// array of HrefResponse with ids; delete each.
	for _, h := range *resp {
		id := h.ID
		cleanupDelete(t, "DeleteInventoryPreloadRecordV2(csv-upload)", func() error {
			return p.DeleteInventoryPreloadRecordV2(ctx, id)
		})
	}
	t.Logf("UploadInventoryPreloadCsvV2: created %d records", len(*resp))
}

// TestAcceptance_Pro_Inventory_PreloadExportV2 exercises the export endpoint;
// returns text/csv. Empty filter gives a full dump.
func TestAcceptance_Pro_Inventory_PreloadExportV2(t *testing.T) {
	c := accClient(t)

	body, err := pro.New(c).ExportInventoryPreloadV2(context.Background(), &pro.ExportParameters{}, nil, nil, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ExportInventoryPreloadV2: %v", err)
	}
	t.Logf("ExportInventoryPreloadV2: %d bytes", len(body))
}

// TestAcceptance_Pro_Inventory_PreloadDeleteAllV2 is gated — bulk-deletes
// every inventory-preload record on the tenant. Acceptable in a fresh test
// tenant but not in one with real data. Opt in with JAMFPLATFORM_ACC_PRO_PRELOAD_WIPE_OK.
func TestAcceptance_Pro_Inventory_PreloadDeleteAllV2(t *testing.T) {
	if accEnv("JAMFPLATFORM_ACC_PRO_PRELOAD_WIPE_OK") == "" {
		t.Skip("gated behind JAMFPLATFORM_ACC_PRO_PRELOAD_WIPE_OK — wipes every inventory-preload record on the tenant")
	}
	c := accClient(t)
	if err := pro.New(c).DeleteAllInventoryPreloadRecordsV2(context.Background()); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteAllInventoryPreloadRecordsV2: %v", err)
	}
}
