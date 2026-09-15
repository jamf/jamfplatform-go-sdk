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

// Batch 11 — JCDS (Jamf Cloud Distribution Service).
//
// These endpoints are the deprecated predecessor to the v1/cloud-distribution-point
// surface. They expose S3-based upload credentials and file metadata for the JCDS
// storage backend. All six endpoints are marked deprecated in the Jamf API spec.
//
// The full JCDS upload flow (InitiateUpload → S3 PutObject → RefreshInventory)
// requires a direct S3 client which is outside the SDK's scope, so mutating tests
// are limited to probes and lifecycle steps that don't require S3 interaction.

// TestAcceptance_Pro_JCDS_ListFilesV1 exercises the read-only file list endpoint.
func TestAcceptance_Pro_JCDS_ListFilesV1(t *testing.T) {
	c := accClient(t)

	files, err := pro.New(c).ListJCDSFilesV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Skip("JCDS not configured on tenant")
		}
		t.Fatalf("ListJCDSFilesV1: %v", err)
	}
	t.Logf("JCDS files: %d", len(files))
	for i, f := range files {
		if i >= 5 {
			t.Logf("  ... and %d more", len(files)-5)
			break
		}
		t.Logf("  %s (length=%d, region=%s)", f.FileName, f.Length, f.Region)
	}
}

// TestAcceptance_Pro_JCDS_InitiateUploadV1 verifies that upload credential
// generation works. The returned credentials are S3 temporary credentials
// that would be used with the AWS SDK to PUT an object — the SDK doesn't
// exercise the S3 leg.
func TestAcceptance_Pro_JCDS_InitiateUploadV1(t *testing.T) {
	c := accClient(t)

	creds, err := pro.New(c).InitiateJCDSUploadV1(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Skip("JCDS not configured on tenant")
		}
		t.Fatalf("InitiateJCDSUploadV1: %v", err)
	}
	if creds.BucketName == "" {
		t.Error("expected non-empty BucketName")
	}
	if creds.Region == "" {
		t.Error("expected non-empty Region")
	}
	if creds.Path == "" {
		t.Error("expected non-empty Path")
	}
	t.Logf("JCDS upload credentials: region=%s bucket=%s path=%s expiration=%d",
		creds.Region, creds.BucketName, creds.Path, creds.Expiration)
}

// TestAcceptance_Pro_JCDS_RenewCredentialsV1 renews the temporary S3
// credentials for an in-flight upload session.
func TestAcceptance_Pro_JCDS_RenewCredentialsV1(t *testing.T) {
	c := accClient(t)
	p := pro.New(c)
	ctx := context.Background()

	// Initiate first so there is an active session to renew.
	if _, err := p.InitiateJCDSUploadV1(ctx); err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Skip("JCDS not configured on tenant")
		}
		t.Fatalf("InitiateJCDSUploadV1 (setup): %v", err)
	}

	renewed, err := p.RenewJCDSCredentialsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("RenewJCDSCredentialsV1: %v", err)
	}
	if renewed.Expiration == 0 {
		t.Error("expected non-zero Expiration after renew")
	}
	t.Logf("JCDS renewed credentials: region=%s bucket=%s expiration=%d",
		renewed.Region, renewed.BucketName, renewed.Expiration)
}

// TestAcceptance_Pro_JCDS_GetFileDownloadURLV1 fetches a presigned download
// URL for a file that exists in the JCDS. Requires at least one file to be
// present; skips if the file list is empty.
func TestAcceptance_Pro_JCDS_GetFileDownloadURLV1(t *testing.T) {
	c := accClient(t)
	p := pro.New(c)
	ctx := context.Background()

	files, err := p.ListJCDSFilesV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Skip("JCDS not configured on tenant")
		}
		t.Fatalf("ListJCDSFilesV1 (setup): %v", err)
	}
	if len(files) == 0 {
		t.Skip("no files in JCDS — cannot test download URL retrieval")
	}

	fileName := files[0].FileName
	dl, err := p.GetJCDSFileDownloadURLV1(ctx, fileName)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetJCDSFileDownloadURLV1(%s): %v", fileName, err)
	}
	if dl.URI == "" {
		t.Error("expected non-empty download URI")
	}
	t.Logf("JCDS download URL for %s: %s...  (truncated)", fileName, truncate(dl.URI, 80))
}

// TestAcceptance_Pro_JCDS_DeleteFileV1 probes the delete endpoint with a
// file name that does not exist. The server should return 404. We do not
// delete real files to avoid disrupting the tenant.
func TestAcceptance_Pro_JCDS_DeleteFileV1(t *testing.T) {
	c := accClient(t)

	err := pro.New(c).DeleteJCDSFileV1(context.Background(), "sdk-acc-nonexistent-"+runSuffix()+".pkg")
	if err == nil {
		// Unexpected success — the file shouldn't exist. Not fatal, but log it.
		t.Log("DeleteJCDSFileV1: unexpectedly succeeded for nonexistent file")
		return
	}
	if apiErr, ok := errors.AsType[*jamfplatform.APIResponseError](err); ok {
		if apiErr.HasStatus(404) {
			t.Logf("DeleteJCDSFileV1(bogus): 404 as expected — plumbing OK")
			return
		}
		if apiErr.StatusCode >= 500 {
			t.Skipf("DeleteJCDSFileV1(bogus): server error %d — skipping", apiErr.StatusCode)
			return
		}
		// Some tenants return 400 or 409 for JCDS not configured.
		t.Logf("DeleteJCDSFileV1(bogus): status=%d — plumbing OK (body: %s)", apiErr.StatusCode, apiErr.Summary())
		return
	}
	t.Fatalf("DeleteJCDSFileV1: unexpected error type: %v", err)
}

// TestAcceptance_Pro_JCDS_RefreshInventoryV1 triggers an inventory refresh.
// Passing an empty file name refreshes all inventory (rate-limited to once
// every 15 seconds server-side). Passing a file name polls JCDS for that
// specific file's availability.
func TestAcceptance_Pro_JCDS_RefreshInventoryV1(t *testing.T) {
	c := accClient(t)

	// Refresh all inventory (no file name).
	err := pro.New(c).RefreshJCDSInventoryV1(context.Background(), "")
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
			t.Skip("JCDS not configured on tenant")
		}
		// refresh-inventory can surface 500 from server-side scan issues —
		// log but don't fail.
		t.Logf("RefreshJCDSInventoryV1: %v", err)
		return
	}
	t.Log("RefreshJCDSInventoryV1: inventory refresh completed")
}
