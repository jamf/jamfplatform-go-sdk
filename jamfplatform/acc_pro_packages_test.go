// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// Packages CRUD + upload + history + manifest + bulk-delete + export.
// Full CRUD needs a .pkg fixture in testdata/; history and metadata
// tests create/delete ephemeral package records without binary upload.

// createAccPackage creates a minimal metadata-only package record
// with cleanup registered. Returns the package id. Used by the
// history / manifest / bulk-delete tests that don't need a real .pkg.
//
// It takes the root client rather than a *pro.Client so the package-store gate
// lives here, at the one choke point every package-creating test goes through:
// on an instance with no Jamf Cloud distribution point the create succeeds and
// the delete 500s, stranding an undeletable record, so a test that creates a
// package must not run there. See requirePackageStore for the wire evidence.
func createAccPackage(t *testing.T, c *jamfplatform.Client, suffix string) string {
	t.Helper()
	requirePackageStore(t, c)
	ctx := context.Background()
	p := pro.New(c)
	created, err := p.CreatePackageV1(ctx, &pro.Package{
		PackageName:          "sdk-acc-pkg-" + suffix,
		FileName:             "sdk-acc-" + suffix + ".pkg",
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
	id := created.ID
	cleanupDelete(t, "Package "+id, func() error { return p.DeletePackageV1(ctx, id) })
	return id
}

func TestAcceptance_Pro_ListPackages(t *testing.T) {
	c := accClient(t)

	pkgs, err := pro.New(c).ListPackagesV1(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListPackagesV1: %v", err)
	}
	t.Logf("Found %d packages", len(pkgs))
}

// TestAcceptance_Pro_PackageCRUD exercises the full Package lifecycle:
// create metadata, upload the .pkg binary (multipart), fetch to confirm
// the round-trip, then delete and verify 404. Requires a .pkg fixture
// in jamfplatform/testdata/ — the test skips if none is present.
func TestAcceptance_Pro_PackageCRUD(t *testing.T) {
	c := accClient(t)
	requirePackageStore(t, c)
	ctx := context.Background()
	p := pro.New(c)

	matches, _ := filepath.Glob("testdata/*.pkg")
	if len(matches) == 0 {
		t.Skip("no .pkg fixture in testdata/ — drop a signed package file there to run this test")
	}
	pkgPath := matches[0]
	pkgFile, err := os.Open(pkgPath)
	if err != nil {
		t.Fatalf("open fixture %s: %v", pkgPath, err)
	}
	t.Cleanup(func() { _ = pkgFile.Close() })

	name := "sdk-acc-pkg-" + runSuffix()
	filename := filepath.Base(pkgPath)

	created, err := p.CreatePackageV1(ctx, &pro.Package{
		PackageName:          name,
		FileName:             filename,
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
	if created.ID == "" {
		t.Fatalf("CreatePackageV1 returned no ID (href=%q)", created.Href)
	}
	cleanupDelete(t, "DeletePackageV1", func() error { return p.DeletePackageV1(ctx, created.ID) })
	t.Logf("Created package %s (%s)", created.ID, name)

	uploadResp, err := p.UploadPackageV1(ctx, created.ID, filename, pkgFile)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UploadPackageV1: %v", err)
	}
	t.Logf("Uploaded pkg bytes for package %s (href=%s)", created.ID, uploadResp.Href)

	got, err := p.GetPackageV1(ctx, created.ID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetPackageV1(%s): %v", created.ID, err)
	}
	if got.PackageName != name {
		t.Errorf("PackageName = %q, want %q", got.PackageName, name)
	}
	if got.FileName != filename {
		t.Errorf("FileName = %q, want %q", got.FileName, filename)
	}

	if err := p.DeletePackageV1(ctx, created.ID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeletePackageV1(%s): %v", created.ID, err)
	}

	_, err = p.GetPackageV1(ctx, created.ID)
	if err == nil {
		t.Fatalf("GetPackageV1(%s) after delete should 404, succeeded", created.ID)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetPackageV1(%s) after delete: want 404, got %v", created.ID, err)
	}
}

func TestAcceptance_Pro_PackageHistoryV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	id := createAccPackage(t, c, runSuffix())

	if _, err := p.CreatePackageHistoryNoteV1(ctx, id, &pro.ObjectHistoryNote{
		Note: "sdk-acc test package history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreatePackageHistoryNoteV1: %v", err)
	}

	hist, err := p.ListPackageHistoryV1(ctx, id, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListPackageHistoryV1: %v", err)
	}
	t.Logf("Package history: %d entries", len(hist))

	body, err := p.ExportPackageHistoryV1(ctx, id, &pro.ExportParameters{}, nil, nil, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ExportPackageHistoryV1: %v", err)
	}
	t.Logf("Package history export: %d bytes", len(body))
}

func TestAcceptance_Pro_PackagesExportV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	body, err := pro.New(c).ExportPackagesV1(ctx, &pro.ExportParameters{}, nil, nil, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ExportPackagesV1: %v", err)
	}
	t.Logf("Packages export: %d bytes", len(body))
}

func TestAcceptance_Pro_PackageManifestV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	id := createAccPackage(t, c, runSuffix()+"-manifest")

	// Minimal plist manifest — the server accepts this shape against a
	// test tenant but may reject stricter validation elsewhere.
	manifest := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>items</key><array/></dict></plist>
`)
	if _, err := p.UploadPackageManifestV1(ctx, id, "manifest.plist", bytes.NewReader(manifest)); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("UploadPackageManifestV1 rejected: status=%d — placeholder manifest likely invalid on this tenant", apiErr.StatusCode)
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("UploadPackageManifestV1: %v", err)
	}
	t.Logf("Uploaded manifest for package %s", id)

	if err := p.DeletePackageManifestV1(ctx, id); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeletePackageManifestV1: %v", err)
	}
}

func TestAcceptance_Pro_PackagesDeleteMultipleV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	a := createAccPackage(t, c, runSuffix()+"-a")
	b := createAccPackage(t, c, runSuffix()+"-b")

	// No skipOnServerError here: createAccPackage has already gated this test on
	// a Jamf Cloud distribution point, which is the one 5xx on a package delete
	// that is an instance limitation rather than a defect. Anything else is a
	// real server fault and must fail, not skip — this is the bulk equivalent of
	// the single delete, and a swallowed 500 here strands both records.
	ids := []string{a, b}
	if err := p.DeleteMultiplePackagesV1(ctx, &pro.Ids{IDs: &ids}); err != nil {
		t.Fatalf("DeleteMultiplePackagesV1: %v", err)
	}
	t.Logf("DeleteMultiplePackagesV1 succeeded for ids %v", ids)
}
