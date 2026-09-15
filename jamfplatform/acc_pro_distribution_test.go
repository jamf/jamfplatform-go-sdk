// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// Batch 10 — distribution (cloud + classic file-server distribution
// points). Jamf Cloud tenants ship with a single Jamf-managed cloud
// distribution point (cdnType=JAMF_CLOUD) already configured, so the
// CDP POST/DELETE paths are probe-only on this tenant. CDP GET,
// test-connection, upload-capability, file listing and history read
// fine; the traditional file-server DistributionPoint surface gets
// full CRUD against a fresh sdk-acc-* fixture.

// --- cloud-distribution-point -----------------------------------------

func TestAcceptance_Pro_Distribution_CloudDistributionPointReadsV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	cdp, err := p.GetCloudDistributionPointV1(ctx)
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Skip("no cloud distribution point configured on tenant")
		}
		skipOnServerError(t, err)
		t.Fatalf("GetCloudDistributionPointV1: %v", err)
	}
	t.Logf("CDP: cdnType=%s master=%v connectionOK=%v", cdp.CdnType, cdp.Master, cdp.HasConnectionSucceeded)

	if status, err := p.TestCloudDistributionPointConnectionV1(ctx); err != nil {
		skipOnServerError(t, err)
		t.Errorf("TestCloudDistributionPointConnectionV1: %v", err)
	} else {
		t.Logf("CDP test-connection: %+v", status)
	}

	if cap, err := p.GetCloudDistributionPointUploadCapabilityV1(ctx); err != nil {
		skipOnServerError(t, err)
		t.Errorf("GetCloudDistributionPointUploadCapabilityV1: %v", err)
	} else {
		t.Logf("CDP upload capability: %+v", cap)
	}

	if files, err := p.ListCloudDistributionPointFilesV1(ctx, nil, ""); err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListCloudDistributionPointFilesV1: %v", err)
	} else {
		t.Logf("CDP files: %d", len(files))
	}

	if hist, err := p.ListCloudDistributionPointHistoryV1(ctx, nil, ""); err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListCloudDistributionPointHistoryV1: %v", err)
	} else {
		t.Logf("CDP history: %d entries", len(hist))
	}
}

func TestAcceptance_Pro_Distribution_CloudDistributionPointHistoryNoteV1(t *testing.T) {
	c := accClient(t)

	if _, err := pro.New(c).CreateCloudDistributionPointHistoryNoteV1(context.Background(), &pro.ObjectHistoryNote{
		Note: "sdk-acc test CDP history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateCloudDistributionPointHistoryNoteV1: %v", err)
	}
}

// TestAcceptance_Pro_Distribution_CloudDistributionPointLifecycleV1
// exercises the full CDP lifecycle end-to-end: snapshot → delete →
// re-create (JCDS mode) → PATCH round-trip → test-connection →
// create+upload a package → refresh-inventory for that package →
// delete package → delete CDP → restore snapshot.
//
// A t.Cleanup re-asserts the original CDP snapshot unconditionally,
// so the tenant is left in its starting state even if an assertion
// fails mid-flight. The CDP is briefly absent during the test —
// package installs queued against the tenant during this window
// would fail, so don't run while live devices are actively
// installing packages.
//
// Requires the Jamf CLI .pkg fixture at testdata/jamf-cli-1.12.0.pkg
// (already checked in for package-upload tests); skips if missing.
func TestAcceptance_Pro_Distribution_CloudDistributionPointLifecycleV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	pkgMatches, _ := filepath.Glob("testdata/*.pkg")
	if len(pkgMatches) == 0 {
		t.Skip("no .pkg fixture in testdata/ — CDP lifecycle requires a package to upload")
	}
	pkgPath := pkgMatches[0]

	// Snapshot the current CDP before anything else. If the GET 404s,
	// or the snapshot is a post-DELETE stub (cdnType empty/NONE), we
	// have no real baseline to restore to — skip so we don't leave the
	// tenant in an unknown state. The singleton endpoint's POST-on-NONE
	// rejects cdnType=NONE, so a NONE snapshot is functionally unusable.
	snapshot, err := p.GetCloudDistributionPointV1(ctx)
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Skip("tenant has no CDP — nothing to snapshot/restore; configure a CDP and re-run")
		}
		skipOnServerError(t, err)
		t.Fatalf("GetCloudDistributionPointV1 (snapshot): %v", err)
	}
	if snapshot.CdnType == "" || snapshot.CdnType == "NONE" {
		t.Skipf("tenant CDP is a stub (cdnType=%q master=%v) — no real baseline to restore; configure a live CDP and re-run", snapshot.CdnType, snapshot.Master)
	}
	t.Logf("CDP snapshot: cdnType=%s master=%v", snapshot.CdnType, snapshot.Master)

	// Restore on exit. GET current state first — only POST when the
	// CDP is genuinely gone (404) or is a post-DELETE stub (cdnType
	// empty or NONE). POSTing on top of a real, live CDP produces
	// duplicate records that wedge the singleton endpoint on subsequent
	// runs (409 "Multiple cloud distribution points are configured"
	// with no SDK path to disambiguate — requires Jamf support).
	t.Cleanup(func() {
		cur, ge := p.GetCloudDistributionPointV1(context.Background())
		if ge == nil {
			if cur.CdnType != "" && cur.CdnType != "NONE" {
				t.Logf("cleanup: real CDP present (cdnType=%s master=%v) — no restore needed", cur.CdnType, cur.Master)
				return
			}
			t.Logf("cleanup: CDP stub present (cdnType=%q master=%v) — restoring snapshot", cur.CdnType, cur.Master)
		} else {
			var apiErr *jamfplatform.APIResponseError
			if !errors.As(ge, &apiErr) || !apiErr.HasStatus(404) {
				t.Logf("cleanup GET CDP: %v — skipping restore (won't POST on uncertain state)", ge)
				return
			}
			t.Logf("cleanup: CDP 404 — restoring snapshot")
		}
		restore := *snapshot
		restore.InventoryID = new("")
		if _, err := p.CreateCloudDistributionPointV1(context.Background(), &restore); err != nil {
			t.Logf("cleanup restore CDP: %v", err)
			return
		}
		t.Logf("cleanup: CDP restored (cdnType=%s)", snapshot.CdnType)
	})

	// 1. Delete current CDP. The singleton DELETE surfaces 409 for two
	// different latent states — "Multiple cloud distribution points are
	// configured" (too many, can't disambiguate) and "Cloud distribution
	// point is not configured" (NONE stub). Neither is recoverable via
	// the SDK; skip so the tenant is untouched and CI passes.
	if err := p.DeleteCloudDistributionPointV1(ctx); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 409 {
			t.Skipf("DeleteCloudDistributionPointV1: 409 — singleton CDP endpoint in an unrecoverable state (multiple configured or not configured): %v", err)
		}
		skipOnServerError(t, err)
		t.Fatalf("DeleteCloudDistributionPointV1: %v", err)
	}
	t.Log("CDP deleted")

	// 2. Re-create CDP from the snapshot (JCDS mode preserved).
	recreate := *snapshot
	recreate.InventoryID = new("")
	recreated, err := p.CreateCloudDistributionPointV1(ctx, &recreate)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateCloudDistributionPointV1: %v", err)
	}
	t.Logf("CDP re-created: cdnType=%s master=%v inventoryId=%s", recreated.CdnType, recreated.Master, derefStrPtr(recreated.InventoryID))

	// 3. PATCH identity — re-send the current body. JAMF_CLOUD mode
	// rejects most patch payloads ("not applicable for this CDN type")
	// so an identity PATCH is the portable round-trip probe.
	if _, err := p.UpdateCloudDistributionPointV1(ctx, recreated); err != nil {
		skipOnServerError(t, err)
		t.Errorf("UpdateCloudDistributionPointV1 (identity PATCH): %v", err)
	} else {
		t.Logf("CDP PATCH identity round-trip OK")
	}

	// 4. Test connection.
	if status, err := p.TestCloudDistributionPointConnectionV1(ctx); err != nil {
		skipOnServerError(t, err)
		t.Errorf("TestCloudDistributionPointConnectionV1: %v", err)
	} else {
		t.Logf("CDP test-connection: succeeded=%v", status.HasConnectionSucceeded)
	}

	// 5. Upload a package (metadata then multipart .pkg).
	pkgFilename := filepath.Base(pkgPath)
	pkgName := "sdk-acc-cdp-pkg-" + runSuffix()
	pkgCreate, err := p.CreatePackageV1(ctx, &pro.Package{
		PackageName:          pkgName,
		FileName:             pkgFilename,
		CategoryID:           "-1",
		Priority:             10,
		FillUserTemplate:     false,
		OsInstall:            false,
		RebootRequired:       false,
		SuppressEula:         false,
		SuppressFromDock:     false,
		SuppressRegistration: false,
		SuppressUpdates:      false,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreatePackageV1: %v", err)
	}
	pkgID := pkgCreate.ID
	t.Logf("Created package %s", pkgID)

	// Ensure the package record is deleted regardless of upload outcome.
	t.Cleanup(func() {
		if err := p.DeletePackageV1(context.Background(), pkgID); err != nil {
			t.Logf("cleanup DeletePackageV1(%s): %v", pkgID, err)
		}
	})

	pkgFile, err := os.Open(pkgPath)
	if err != nil {
		t.Fatalf("open fixture %s: %v", pkgPath, err)
	}
	t.Cleanup(func() { _ = pkgFile.Close() })

	if _, err := p.UploadPackageV1(ctx, pkgID, pkgFilename, pkgFile); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UploadPackageV1(%s): %v", pkgID, err)
	}
	t.Logf("Uploaded package bytes for %s (%s)", pkgID, pkgFilename)

	// 6. Refresh-inventory for the uploaded file.
	if err := p.RefreshCloudDistributionPointInventoryV1(ctx, pkgFilename); err != nil {
		skipOnServerError(t, err)
		// refresh-inventory surfaces server scan errors as 500 sometimes —
		// log but don't fail.
		t.Logf("RefreshCloudDistributionPointInventoryV1(%s): %v", pkgFilename, err)
	} else {
		t.Logf("CDP refresh-inventory queued for %s", pkgFilename)
	}

	// 7. Delete package record (the pkg binary in the CDP goes with it).
	if err := p.DeletePackageV1(ctx, pkgID); err != nil {
		skipOnServerError(t, err)
		t.Errorf("DeletePackageV1(%s): %v", pkgID, err)
	}

	// 8. Delete CDP — cleanup restores on exit.
	if err := p.DeleteCloudDistributionPointV1(ctx); err != nil {
		skipOnServerError(t, err)
		t.Errorf("DeleteCloudDistributionPointV1 (final): %v", err)
	}
	t.Log("CDP deleted (cleanup will restore)")
}

// fail-upload + refresh-inventory take upload-flow state that the
// SDK test harness doesn't know how to fake; probe only.
func TestAcceptance_Pro_Distribution_CloudDistributionPointFailUploadV1(t *testing.T) {
	c := accClient(t)

	err := pro.New(c).FailCloudDistributionPointUploadV1(context.Background(), "99999999", "sdk-acc-probe.pkg", "Package")
	if err == nil {
		t.Log("FailCloudDistributionPointUploadV1: unexpectedly succeeded against probe id")
		return
	}
	var apiErr *jamfplatform.APIResponseError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		t.Logf("FailCloudDistributionPointUploadV1(bogus): status=%d — plumbing OK", apiErr.StatusCode)
		return
	}
	skipOnServerError(t, err)
	t.Fatalf("FailCloudDistributionPointUploadV1: %v", err)
}

func TestAcceptance_Pro_Distribution_RefreshCloudDistributionPointInventoryV1(t *testing.T) {
	t.Skip("refresh-inventory triggers a server-side scan against real package storage — skip")
}

// --- distribution-points (traditional file-server DPs) ---------------

func TestAcceptance_Pro_Distribution_ListDistributionPointsV1(t *testing.T) {
	c := accClient(t)

	dps, err := pro.New(c).ListDistributionPointsV1(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListDistributionPointsV1: %v", err)
	}
	t.Logf("Distribution points: %d", len(dps))
}

// TestAcceptance_Pro_Distribution_DistributionPointCRUDV1 creates an AFP
// file-server DP, reads/updates (PUT + PATCH) it, and deletes. No
// actual AFP server is contacted — this is a configuration record on
// the Jamf side.
func TestAcceptance_Pro_Distribution_DistributionPointCRUDV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	name := "sdk-acc-dp-" + runSuffix()
	readWriteUser := "sdkacc-rw"
	readWritePass := "sdkacc-pass-not-used"
	shareName := "CasperShare"
	ipAddress := "dp.example.invalid"
	created, err := p.CreateDistributionPointV1(ctx, &pro.DistributionPoint{
		Name:                      name,
		FileSharingConnectionType: pro.DistributionPointFileSharingConnectionTypeAfp,
		ServerName:                ipAddress,
		ShareName:                 &shareName,
		ReadWriteUsername:         &readWriteUser,
		ReadWritePassword:         &readWritePass,
	})
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("CreateDistributionPointV1 rejected: status=%d — tenant may not allow AFP; skipping CRUD probe", apiErr.StatusCode)
			return
		}
		t.Fatalf("CreateDistributionPointV1: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateDistributionPointV1 returned no id")
	}
	id := created.ID
	cleanupDelete(t, "DeleteDistributionPointV1", func() error { return p.DeleteDistributionPointV1(ctx, id) })
	t.Logf("Created distribution point %s (%s)", id, name)

	got, err := p.GetDistributionPointV1(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetDistributionPointV1(%s): %v", id, err)
	}
	if got.Name != name {
		t.Errorf("Name = %q, want %q", got.Name, name)
	}

	// PUT replaces — send the GET snapshot with tweaked share name.
	newShare := "CasperShareUpdated"
	updateBody := *got
	updateBody.ShareName = &newShare
	if _, err := p.UpdateDistributionPointV1(ctx, id, &updateBody); err != nil {
		skipOnServerError(t, err)
		t.Errorf("UpdateDistributionPointV1(%s): %v", id, err)
	}

	// PATCH updates — reuse the same shape.
	patchShare := "CasperSharePatched"
	patchBody := *got
	patchBody.ShareName = &patchShare
	if _, err := p.PatchDistributionPointV1(ctx, id, &patchBody); err != nil {
		skipOnServerError(t, err)
		t.Errorf("PatchDistributionPointV1(%s): %v", id, err)
	}

	if _, err := p.CreateDistributionPointHistoryNoteV1(ctx, id, &pro.ObjectHistoryNote{
		Note: "sdk-acc test history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Errorf("CreateDistributionPointHistoryNoteV1(%s): %v", id, err)
	}
	if hist, err := p.ListDistributionPointHistoryV1(ctx, id, nil, ""); err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListDistributionPointHistoryV1(%s): %v", id, err)
	} else {
		t.Logf("DP %s history: %d entries", id, len(hist))
	}

	if err := p.DeleteDistributionPointV1(ctx, id); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteDistributionPointV1(%s): %v", id, err)
	}

	_, err = p.GetDistributionPointV1(ctx, id)
	if err == nil {
		t.Fatalf("GetDistributionPointV1(%s) after delete should 404", id)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetDistributionPointV1(%s) after delete: want 404, got %v", id, err)
	}
}

// TestAcceptance_Pro_Distribution_DeleteMultipleDistributionPointsV1 creates
// two throwaway DPs, bulk-deletes them, confirms 404s.
func TestAcceptance_Pro_Distribution_DeleteMultipleDistributionPointsV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	suffix := runSuffix()
	shareName := "CasperShare"
	ipAddress := "dp.example.invalid"
	var ids []string
	for _, tag := range []string{"a", "b"} {
		name := "sdk-acc-dp-bulk-" + suffix + "-" + tag
		resp, err := p.CreateDistributionPointV1(ctx, &pro.DistributionPoint{
			Name:                      name,
			FileSharingConnectionType: pro.DistributionPointFileSharingConnectionTypeAfp,
			ServerName:                ipAddress,
			ShareName:                 &shareName,
		})
		if err != nil {
			skipOnServerError(t, err)
			var apiErr *jamfplatform.APIResponseError
			if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
				t.Logf("CreateDistributionPointV1[%s] rejected: status=%d — skipping bulk-delete probe", tag, apiErr.StatusCode)
				return
			}
			t.Fatalf("CreateDistributionPointV1[%s]: %v", tag, err)
		}
		ids = append(ids, resp.ID)
		id := resp.ID
		cleanupDelete(t, "DeleteDistributionPointV1(fallback)", func() error { return p.DeleteDistributionPointV1(ctx, id) })
	}

	if err := p.DeleteMultipleDistributionPointsV1(ctx, &pro.Ids{IDs: &ids}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteMultipleDistributionPointsV1: %v", err)
	}

	for _, id := range ids {
		if _, err := p.GetDistributionPointV1(ctx, id); err == nil {
			t.Errorf("GetDistributionPointV1(%s) after bulk delete should 404", id)
		}
	}
}
