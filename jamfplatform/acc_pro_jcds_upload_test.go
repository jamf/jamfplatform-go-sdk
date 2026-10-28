// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
	"github.com/jamf/jamfplatform-go-sdk/jcdsupload"
)

// uploadViaJCDS creates a package record, sends size bytes through jcdsupload.Upload, then
// refreshes by file name until the record reports READY at that size. It returns the package and
// the content, with cleanup of both the record and the JCDS file registered.
//
// The order is create record, upload, refresh. A record created after the upload and refresh also
// reaches READY, so the order is the Jamf Pro UI's rather than a requirement.
func uploadViaJCDS(t *testing.T, c *jamfplatform.Client, size int) (*pro.Package, []byte) {
	t.Helper()
	requirePackageStore(t, c)
	ctx := context.Background()
	p := pro.New(c)

	suffix := runSuffix() + "-" + strconv.Itoa(size)
	fileName := "sdk-acc-jcds-" + suffix + ".pkg"
	content := make([]byte, size)
	rng := rand.New(rand.NewPCG(1, uint64(size)))
	for i := range content {
		content[i] = byte(rng.Uint32())
	}

	created, err := p.CreatePackageV1(ctx, &pro.Package{
		PackageName: "sdk-acc-jcds-" + suffix,
		FileName:    fileName,
		CategoryID:  "-1",
		Priority:    10,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreatePackageV1: %v", err)
	}
	// Registered first so it runs last: the record goes before the file it points at.
	cleanupDelete(t, "JCDS file "+fileName, func() error { return p.DeleteJCDSFileV1(ctx, fileName) })
	cleanupDelete(t, "Package "+created.ID, func() error { return p.DeletePackageV1(ctx, created.ID) })

	if err := jcdsupload.Upload(ctx, p, fileName, bytes.NewReader(content)); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("jcdsupload.Upload: %v", err)
	}

	pollCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	var got *pro.Package
	err = jamfplatform.PollUntil(pollCtx, 3*time.Second, func(ctx context.Context) (bool, error) {
		// A 504 here is the edge giving up on a refresh that is still processing the file and
		// will finish anyway; see WIRE-FACTS.md. Anything else ends the poll.
		var apiErr *jamfplatform.APIResponseError
		if err := p.RefreshJCDSInventoryV1(ctx, fileName); errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusGatewayTimeout {
			t.Logf("RefreshJCDSInventoryV1(%s): 504, still processing", fileName)
		} else if err != nil {
			return false, fmt.Errorf("RefreshJCDSInventoryV1(%s): %w", fileName, err)
		}
		pkg, err := p.GetPackageV1(ctx, created.ID)
		if err != nil {
			return false, fmt.Errorf("GetPackageV1(%s): %w", created.ID, err)
		}
		got = pkg
		return derefStr(pkg.CloudTransferStatus) == "READY" && derefStr(pkg.Size) == strconv.Itoa(size), nil
	})
	if err != nil {
		if got != nil {
			t.Logf("last read: status=%q size=%q", derefStr(got.CloudTransferStatus), derefStr(got.Size))
		}
		t.Fatalf("package %s not READY at %d bytes: %v", created.ID, size, err)
	}
	return got, content
}

// TestAcceptance_Pro_JCDS_UploadSinglePart sends a file under one part through the S3 route and
// checks the record converges with the right size and MD5. A single-part object's S3 ETag is its
// MD5, which is why the digest is comparable here and not in the multipart test.
func TestAcceptance_Pro_JCDS_UploadSinglePart(t *testing.T) {
	c := accClient(t)

	got, content := uploadViaJCDS(t, c, 1<<20)

	sum := md5.Sum(content)
	if want := hex.EncodeToString(sum[:]); derefStr(got.Md5) != want {
		t.Errorf("Md5 = %q, want %q", derefStr(got.Md5), want)
	}
	t.Logf("package %s READY: size=%s hashType=%s", derefStr(got.ID), derefStr(got.Size), derefStr(got.HashType))
}

// TestAcceptance_Pro_JCDS_UploadMultipart sends a file that spans several parts, so the multipart
// path runs against the real bucket: create, parts under the issued prefix, complete.
func TestAcceptance_Pro_JCDS_UploadMultipart(t *testing.T) {
	c := accClient(t)

	got, _ := uploadViaJCDS(t, c, 20<<20)

	t.Logf("package %s READY: size=%s hashType=%s", derefStr(got.ID), derefStr(got.Size), derefStr(got.HashType))
}
