// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package appstore_test

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"testing"
	"time"

	"github.com/jamf/jamfplatform-go-sdk/appstore"
)

// The live suite checks the wire behaviour the package is built on against Apple's real lookup,
// which publishes no contract and can change without notice. It needs no credentials. Each test
// asserts a shape, never a value that moves with an app release (version, description, price),
// and every listing it reads is Apple-owned or long-lived so a test fails only when the shape does.
const (
	pagesID       = "361309726"
	xcodeBundleID = "com.apple.dt.Xcode"
	xcodeID       = "497799835"
	jamfParentID  = "1458797105"
	things3ID     = "904280696"
)

func accLookup(t *testing.T, q appstore.Query) (*appstore.App, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return appstore.New(appstore.WithUserAgent("jamfplatform-go-sdk-acceptance")).Lookup(ctx, q)
}

func accMustLookup(t *testing.T, q appstore.Query) *appstore.App {
	t.Helper()
	app, err := accLookup(t, q)
	if err != nil {
		t.Fatalf("Lookup(%+v): %v", q, err)
	}
	return app
}

// accFetchIcon downloads an artwork URL and decodes it as PNG.
func accFetchIcon(t *testing.T, url string) image.Image {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request for %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("fetch %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fetch %s: status %d", url, resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("fetch %s: content type %q, want image/png", url, ct)
	}
	img, err := png.Decode(resp.Body)
	if err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return img
}

func cornerAlpha(img image.Image) uint32 {
	b := img.Bounds()
	_, _, _, a := img.At(b.Min.X, b.Min.Y).RGBA()
	return a
}

func assertSize(t *testing.T, label string, img image.Image, want int) {
	t.Helper()
	if b := img.Bounds(); b.Dx() != want || b.Dy() != want {
		t.Errorf("%s: icon is %dx%d, want %dx%d", label, b.Dx(), b.Dy(), want, want)
	}
}

// TestAcceptance_AppStore_UniversalAppFlavours pins that one id answers with two different
// listings by platform, and that the iPhone and iPad artwork is an opaque square while the Mac
// artwork is shaped with transparent corners. Consumers choose artwork over iconArtwork on that
// basis, so a change here invalidates the choice.
//
// The iconArtwork template is checked to fill but its image is not fetched: for Pages it names a
// layered "Placeholder.mill" source that Apple's image service refused with 403 from
// GitHub-hosted runners on 2026-10-16, while serving it from elsewhere.
func TestAcceptance_AppStore_UniversalAppFlavours(t *testing.T) {
	mobile := accMustLookup(t, appstore.Query{ID: pagesID, Platform: appstore.PlatformMobile})
	mac := accMustLookup(t, appstore.Query{ID: pagesID, Platform: appstore.PlatformMac})

	if mobile.BundleID != "com.apple.Pages" || mac.BundleID != "com.apple.Pages" {
		t.Fatalf("bundle ids %q / %q, want com.apple.Pages for both", mobile.BundleID, mac.BundleID)
	}
	if mobile.ArtworkURLTemplate == mac.ArtworkURLTemplate {
		t.Errorf("both platforms returned the same artwork template %q; the Mac flavour is no longer distinct", mac.ArtworkURLTemplate)
	}

	for _, tc := range []struct {
		label  string
		app    *appstore.App
		opaque bool
	}{
		{"iPhone and iPad artwork", mobile, true},
		{"Mac artwork", mac, false},
	} {
		u, err := tc.app.ArtworkURL(512, 512, "png")
		if err != nil {
			t.Fatalf("%s: ArtworkURL: %v", tc.label, err)
		}
		img := accFetchIcon(t, u)
		assertSize(t, tc.label, img, 512)
		a := cornerAlpha(img)
		if tc.opaque && a != 0xffff {
			t.Errorf("%s: corner alpha %#x, want opaque", tc.label, a)
		}
		if !tc.opaque && a == 0xffff {
			t.Errorf("%s: corner is opaque, want transparent", tc.label)
		}
	}

	if _, err := mobile.IconArtworkURL(512, 512, "png"); err != nil {
		t.Fatalf("iPhone and iPad IconArtworkURL: %v", err)
	}
}

// TestAcceptance_AppStore_BundleIDLookup pins bundleId as a lookup key, case-insensitively.
func TestAcceptance_AppStore_BundleIDLookup(t *testing.T) {
	for _, bid := range []string{xcodeBundleID, "COM.APPLE.DT.XCODE"} {
		app := accMustLookup(t, appstore.Query{BundleID: bid, Platform: appstore.PlatformMac})
		if app.ID != xcodeID {
			t.Errorf("bundle id %q resolved to id %q, want %s", bid, app.ID, xcodeID)
		}
	}
}

// TestAcceptance_AppStore_StoreURLRoundTrip pins that the URL a listing returns parses back to its
// own id and storefront.
func TestAcceptance_AppStore_StoreURLRoundTrip(t *testing.T) {
	app := accMustLookup(t, appstore.Query{ID: pagesID, Platform: appstore.PlatformMobile, Country: "GB"})
	parsed, err := appstore.ParseStoreURL(app.URL)
	if err != nil {
		t.Fatalf("ParseStoreURL(%q): %v", app.URL, err)
	}
	if parsed.ID != pagesID || parsed.Country != "GB" {
		t.Errorf("ParseStoreURL(%q) = %+v, want id %s country GB", app.URL, parsed, pagesID)
	}
}

// TestAcceptance_AppStore_Misses pins that Apple answers every miss with an empty 200, which the
// package must turn into a NotFoundError rather than a zero-value listing.
func TestAcceptance_AppStore_Misses(t *testing.T) {
	for _, tc := range []struct {
		label string
		q     appstore.Query
	}{
		{"iOS-only app asked for Mac", appstore.Query{ID: jamfParentID, Platform: appstore.PlatformMac}},
		{"unassigned id", appstore.Query{ID: "1", Platform: appstore.PlatformMobile}},
		{"unknown bundle id", appstore.Query{BundleID: "com.example.does-not-exist.acceptance", Platform: appstore.PlatformMobile}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			_, err := accLookup(t, tc.q)
			var nf *appstore.NotFoundError
			if !errors.As(err, &nf) {
				t.Fatalf("Lookup(%+v) error = %v, want *NotFoundError", tc.q, err)
			}
		})
	}
}

// TestAcceptance_AppStore_UnknownCountryFallsBack pins Apple's silent US fallback for a country it
// does not recognise, which the package must surface as a StorefrontMismatchError.
func TestAcceptance_AppStore_UnknownCountryFallsBack(t *testing.T) {
	_, err := accLookup(t, appstore.Query{ID: pagesID, Platform: appstore.PlatformMobile, Country: "XX"})
	var mm *appstore.StorefrontMismatchError
	if !errors.As(err, &mm) {
		t.Fatalf("error = %v, want *StorefrontMismatchError", err)
	}
	if mm.Requested != "XX" || mm.Answered != "US" {
		t.Errorf("mismatch = %+v, want requested XX answered US", mm)
	}
}

// TestAcceptance_AppStore_PaidApp pins the paid/free decode on a long-lived paid listing.
func TestAcceptance_AppStore_PaidApp(t *testing.T) {
	app := accMustLookup(t, appstore.Query{ID: things3ID, Platform: appstore.PlatformMac})
	if app.Free || app.Price <= 0 {
		t.Errorf("Things 3: Free=%v Price=%v, want a paid listing", app.Free, app.Price)
	}
	free := accMustLookup(t, appstore.Query{ID: xcodeID, Platform: appstore.PlatformMac})
	if !free.Free || free.Price != 0 {
		t.Errorf("Xcode: Free=%v Price=%v, want a free listing", free.Free, free.Price)
	}
	if app.Name == "" || app.Version == "" || app.URL == "" || app.BundleID == "" {
		t.Errorf("Things 3 listing missing a core field: %s", fmt.Sprintf("%+v", *app))
	}
}
