// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// Batch 16 — self-service + branding + icon. The branding endpoints
// reference iconId and brandingHeaderImageId by numeric id, so each
// test uploads fresh assets before configuring a branding record. The
// PNG fixtures under testdata/ are intentionally small so repeated
// uploads don't bloat the tenant.

const (
	fixtureIconPNG   = "testdata/jamf-icon-greyscale-dark.png"
	fixtureBannerPNG = "testdata/self-service-plus-color-dark.png"
)

// --- icon upload + GET + download ------------------------------------

func TestAcceptance_Pro_IconV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	f, err := os.Open(fixtureIconPNG)
	if err != nil {
		t.Skipf("fixture %s unavailable: %v", fixtureIconPNG, err)
	}
	t.Cleanup(func() { _ = f.Close() })

	uploaded, err := p.UploadIconV1(ctx, "sdk-acc-icon-"+runSuffix()+".png", f)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UploadIconV1: %v", err)
	}
	t.Logf("Uploaded icon id=%d url=%s", uploaded.ID, uploaded.URL)

	id := strconv.Itoa(uploaded.ID)
	got, err := p.GetIconV1(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetIconV1(%s): %v", id, err)
	}
	if got.ID != uploaded.ID {
		t.Errorf("GetIconV1 id mismatch: got %d, want %d", got.ID, uploaded.ID)
	}

	body, err := p.DownloadIconV1(ctx, id, 0, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DownloadIconV1: %v", err)
	}
	t.Logf("DownloadIconV1: %d bytes", len(body))
}

// --- branding-image upload + download --------------------------------

func TestAcceptance_Pro_BrandingImageV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	f, err := os.Open(fixtureBannerPNG)
	if err != nil {
		t.Skipf("fixture %s unavailable: %v", fixtureBannerPNG, err)
	}
	t.Cleanup(func() { _ = f.Close() })

	uploaded, err := p.UploadBrandingImageV1(ctx, "sdk-acc-banner-"+runSuffix()+".png", f)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UploadBrandingImageV1: %v", err)
	}
	t.Logf("Uploaded branding image url=%s", uploaded.URL)

	// URL contains the image id as the last path segment.
	// e.g. /api/.../branding-images/download/123
	id := extractLastPathSegment(uploaded.URL)
	if id == "" {
		t.Fatalf("could not extract branding image id from URL %q", uploaded.URL)
	}
	body, err := p.DownloadBrandingImageV1(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DownloadBrandingImageV1(%s): %v", id, err)
	}
	t.Logf("DownloadBrandingImageV1: %d bytes", len(body))
}

func extractLastPathSegment(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return s[i+1:]
		}
	}
	return s
}

// --- iOS branding CRUD -----------------------------------------------

func TestAcceptance_Pro_IOSBrandingV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	existing, err := p.ListIOSBrandingConfigurationsV1(ctx, nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListIOSBrandingConfigurationsV1: %v", err)
	}
	t.Logf("iOS branding configs: %d existing", len(existing))

	iconID := uploadFixtureIcon(t, p, fixtureIconPNG)

	cfg := &pro.IosBrandingConfiguration{
		BrandingName:              "sdk-acc-ios-" + runSuffix(),
		BrandingNameColorCode:     "FFFFFF",
		HeaderBackgroundColorCode: "000000",
		MenuIconColorCode:         "FFFFFF",
		StatusBarTextColor:        "LIGHT",
		IconID:                    &iconID,
	}
	created, err := p.CreateIOSBrandingConfigurationV1(ctx, cfg)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateIOSBrandingConfigurationV1: %v", err)
	}
	id := created.ID
	t.Logf("Created iOS branding id=%s", id)
	cleanupDelete(t, "IOSBranding "+id, func() error { return p.DeleteIOSBrandingConfigurationV1(ctx, id) })

	got, err := p.GetIOSBrandingConfigurationV1(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetIOSBrandingConfigurationV1: %v", err)
	}
	if got.BrandingName != cfg.BrandingName {
		t.Errorf("branding name round-trip mismatch: got %q, want %q", got.BrandingName, cfg.BrandingName)
	}

	got.BrandingName = cfg.BrandingName + "-upd"
	if _, err := p.UpdateIOSBrandingConfigurationV1(ctx, id, got); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateIOSBrandingConfigurationV1: %v", err)
	}
}

// --- macOS branding CRUD ---------------------------------------------

func TestAcceptance_Pro_MacOSBrandingV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	existing, err := p.ListMacOSBrandingConfigurationsV1(ctx, nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListMacOSBrandingConfigurationsV1: %v", err)
	}
	t.Logf("macOS branding configs: %d existing", len(existing))

	iconID := uploadFixtureIcon(t, p, fixtureIconPNG)
	bannerID := uploadFixtureBanner(t, p, fixtureBannerPNG)

	name := "sdk-acc-macos-" + runSuffix()
	heading := "sdk-acc heading"
	sub := "sdk-acc subheading"
	cfg := &pro.MacOsBrandingConfiguration{
		ApplicationName:       &name,
		BrandingName:          &name,
		BrandingHeaderImageID: &bannerID,
		HomeHeading:           &heading,
		HomeSubheading:        &sub,
		IconID:                &iconID,
	}
	created, err := p.CreateMacOSBrandingConfigurationV1(ctx, cfg)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateMacOSBrandingConfigurationV1: %v", err)
	}
	id := created.ID
	t.Logf("Created macOS branding id=%s", id)
	cleanupDelete(t, "MacOSBranding "+id, func() error { return p.DeleteMacOSBrandingConfigurationV1(ctx, id) })

	got, err := p.GetMacOSBrandingConfigurationV1(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetMacOSBrandingConfigurationV1: %v", err)
	}
	if got.BrandingName == nil || *got.BrandingName != name {
		t.Errorf("branding name round-trip mismatch: got %v, want %q", got.BrandingName, name)
	}

	upd := *got
	updName := name + "-upd"
	upd.BrandingName = &updName
	if _, err := p.UpdateMacOSBrandingConfigurationV1(ctx, id, &upd); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateMacOSBrandingConfigurationV1: %v", err)
	}
}

// --- self-service settings + history ---------------------------------

func TestAcceptance_Pro_SelfServiceSettingsV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	current, err := p.GetSelfServiceSettingsV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSelfServiceSettingsV1: %v", err)
	}
	t.Logf("Self-service settings retrieved")

	if _, err := p.UpdateSelfServiceSettingsV1(ctx, current); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateSelfServiceSettingsV1 round-trip: %v", err)
	}

	if _, err := p.CreateSelfServiceSettingsHistoryNoteV1(ctx, &pro.ObjectHistoryNote{
		Note: "sdk-acc test self-service settings history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateSelfServiceSettingsHistoryNoteV1: %v", err)
	}

	hist, err := p.ListSelfServiceSettingsHistoryV1(ctx, nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListSelfServiceSettingsHistoryV1: %v", err)
	}
	t.Logf("Self-service settings history: %d entries", len(hist))
}

// --- shared fixture helpers ------------------------------------------

func uploadFixtureIcon(t *testing.T, p *pro.Client, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("fixture %s unavailable: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })

	resp, err := p.UploadIconV1(context.Background(), "sdk-acc-icon-"+runSuffix()+".png", f)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UploadIconV1: %v", err)
	}
	return resp.ID
}

func uploadFixtureBanner(t *testing.T, p *pro.Client, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("fixture %s unavailable: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })

	resp, err := p.UploadBrandingImageV1(context.Background(), "sdk-acc-banner-"+runSuffix()+".png", f)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UploadBrandingImageV1: %v", err)
	}
	// Banner URL path tail is the numeric id.
	idStr := extractLastPathSegment(resp.URL)
	idInt, err := strconv.Atoi(idStr)
	if err != nil {
		t.Fatalf("could not parse branding image id from URL %q: %v", resp.URL, err)
	}
	return idInt
}
