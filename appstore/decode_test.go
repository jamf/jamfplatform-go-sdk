// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package appstore

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestArtworkTemplate_Shapes(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want string
	}{
		"object":           {`{"url":"https://x/{w}x{h}bb.{f}","width":1024}`, "https://x/{w}x{h}bb.{f}"},
		"array":            {`[{"url":""},{"url":"https://y/{w}x{h}bb.{f}"}]`, "https://y/{w}x{h}bb.{f}"},
		"empty array":      {`[]`, ""},
		"absent":           {``, ""},
		"null":             {`null`, ""},
		"object, no url":   {`{"width":1}`, ""},
		"array, all empty": {`[{"url":""}]`, ""},
	}
	for name, tc := range cases {
		got, err := artworkTemplate(json.RawMessage(tc.raw))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", name, got, tc.want)
		}
	}
	if _, err := artworkTemplate(json.RawMessage(`"a string"`)); err == nil {
		t.Error("a string artwork decoded without error")
	}
}

func TestArtworkURL_FillsTemplate(t *testing.T) {
	app := &App{ArtworkURLTemplate: "https://is1-ssl.mzstatic.com/image/thumb/x/AppIcon.png/{w}x{h}bb.{f}"}
	got, err := app.ArtworkURL(512, 512, "png")
	if err != nil || got != "https://is1-ssl.mzstatic.com/image/thumb/x/AppIcon.png/512x512bb.png" {
		t.Errorf("ArtworkURL = %q, %v", got, err)
	}
	got, err = app.ArtworkURL(1024, 1024, "jpg")
	if err != nil || got != "https://is1-ssl.mzstatic.com/image/thumb/x/AppIcon.png/1024x1024bb.jpg" {
		t.Errorf("ArtworkURL 1024 jpg = %q, %v", got, err)
	}
}

func TestArtworkURL_FailsLoudly(t *testing.T) {
	iconArtwork := &App{ArtworkURLTemplate: "https://x/Placeholder.mill/{w}x{h}{c}.{f}"}
	if _, err := iconArtwork.ArtworkURL(512, 512, "png"); !errors.Is(err, ErrArtworkTemplate) {
		t.Errorf("iconArtwork-style template: err = %v, want ErrArtworkTemplate", err)
	}
	twice := &App{ArtworkURLTemplate: "https://x/{w}x{h}bb.{f}/{w}x{h}bb.{f}"}
	if _, err := twice.ArtworkURL(512, 512, "png"); !errors.Is(err, ErrArtworkTemplate) {
		t.Errorf("doubled token: err = %v, want ErrArtworkTemplate", err)
	}
	if _, err := (&App{}).ArtworkURL(512, 512, "png"); !errors.Is(err, ErrNoArtwork) {
		t.Errorf("no artwork: err = %v, want ErrNoArtwork", err)
	}
	var nilApp *App
	if _, err := nilApp.ArtworkURL(1, 1, "png"); !errors.Is(err, ErrNoArtwork) {
		t.Errorf("nil app: err = %v, want ErrNoArtwork", err)
	}
	ok := &App{ArtworkURLTemplate: "https://x/{w}x{h}bb.{f}"}
	for _, bad := range []struct {
		w, h int
		f    string
	}{{0, 512, "png"}, {512, -1, "png"}, {512, 512, ""}} {
		if _, err := ok.ArtworkURL(bad.w, bad.h, bad.f); err == nil {
			t.Errorf("ArtworkURL(%d, %d, %q) accepted", bad.w, bad.h, bad.f)
		}
	}
}

func TestIconArtworkURL(t *testing.T) {
	app := &App{IconArtworkURLTemplate: "https://is1-ssl.mzstatic.com/image/thumb/x/Placeholder.mill/{w}x{h}{c}.{f}"}
	got, err := app.IconArtworkURL(512, 512, "png")
	if err != nil || got != "https://is1-ssl.mzstatic.com/image/thumb/x/Placeholder.mill/512x512bb.png" {
		t.Errorf("IconArtworkURL = %q, %v", got, err)
	}
	artworkShaped := &App{IconArtworkURLTemplate: "https://x/AppIcon.png/{w}x{h}bb.{f}"}
	if _, err := artworkShaped.IconArtworkURL(512, 512, "png"); !errors.Is(err, ErrArtworkTemplate) {
		t.Errorf("artwork-token template under IconArtworkURL: err = %v, want ErrArtworkTemplate", err)
	}
	if _, err := (&App{ArtworkURLTemplate: "https://x/{w}x{h}bb.{f}"}).IconArtworkURL(512, 512, "png"); !errors.Is(err, ErrNoArtwork) {
		t.Errorf("absent icon artwork: err = %v, want ErrNoArtwork", err)
	}
	var nilApp *App
	if _, err := nilApp.IconArtworkURL(1, 1, "png"); !errors.Is(err, ErrNoArtwork) {
		t.Errorf("nil app: err = %v", err)
	}
}

func TestDecode_MacListingWithoutIconArtwork(t *testing.T) {
	app, err := decode(Query{ID: "803453959", Platform: PlatformMac, Country: "US"}, fixture(t, "slack-mac-no-iconartwork"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if app.IconArtworkURLTemplate != "" {
		t.Errorf("IconArtworkURLTemplate = %q, want empty (the fixture carries no iconArtwork)", app.IconArtworkURLTemplate)
	}
	if _, err := app.IconArtworkURL(512, 512, "png"); !errors.Is(err, ErrNoArtwork) {
		t.Errorf("IconArtworkURL: err = %v, want ErrNoArtwork", err)
	}
	if _, err := app.ArtworkURL(512, 512, "png"); err != nil {
		t.Errorf("ArtworkURL: %v", err)
	}
}

func TestArtworkURL_EveryPopulatedFixtureFills(t *testing.T) {
	for _, name := range []string{"pages-ios", "pages-mac", "xcode-mac", "things3-mac-paid", "volumestore-two-offer", "bundleid-case-variant", "pages-gb-lang-fr", "slack-mac-no-iconartwork"} {
		body := fixture(t, name)
		var resp wireResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for key, w := range resp.Results {
			artwork, err := artworkTemplate(w.Artwork)
			if err != nil {
				t.Fatalf("%s/%s: %v", name, key, err)
			}
			icon, err := artworkTemplate(w.IconArtwork)
			if err != nil {
				t.Fatalf("%s/%s: %v", name, key, err)
			}
			app := &App{ArtworkURLTemplate: artwork, IconArtworkURLTemplate: icon}
			if _, err := app.ArtworkURL(512, 512, "png"); err != nil {
				t.Errorf("%s/%s artwork: %v", name, key, err)
			}
			if _, err := app.IconArtworkURL(512, 512, "png"); err != nil && (icon != "" || !errors.Is(err, ErrNoArtwork)) {
				t.Errorf("%s/%s icon artwork: %v", name, key, err)
			}
		}
	}
}

func TestDecode_PickAcceptsALoneMatchingResultUnderAnotherKey(t *testing.T) {
	body := []byte(`{"meta":{"storefront":{"cc":"US"}},"results":{"361309726":{"id":"361309726","bundleId":"com.apple.Pages","name":"Pages"}}}`)
	app, err := decode(Query{BundleID: "com.apple.pages", Platform: PlatformMobile, Country: "US"}, body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if app.Name != "Pages" {
		t.Errorf("Name = %q", app.Name)
	}
	body = []byte(`{"meta":{"storefront":{"cc":"US"}},"results":{"5":{"id":"5","bundleId":"com.example.other"}}}`)
	var nf *NotFoundError
	if _, err := decode(Query{ID: "6", Platform: PlatformMac, Country: "US"}, body); !errors.As(err, &nf) {
		t.Errorf("an unrelated lone result was accepted: %v", err)
	}
}

func TestDecode_NoOffersLeavesVersionEmpty(t *testing.T) {
	body := []byte(`{"meta":{"storefront":{"cc":"US"}},"results":{"7":{"id":"7","name":"Pre-order"}}}`)
	app, err := decode(Query{ID: "7", Platform: PlatformMac, Country: "US"}, body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if app.Version != "" || app.Free {
		t.Errorf("Version = %q Free = %v, want empty and not free", app.Version, app.Free)
	}
}

func TestDecode_MissingStorefrontKeepsRequestedCountry(t *testing.T) {
	body := []byte(`{"results":{"7":{"id":"7"}}}`)
	app, err := decode(Query{ID: "7", Platform: PlatformMac, Country: "GB"}, body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if app.Country != "GB" {
		t.Errorf("Country = %q, want GB", app.Country)
	}
}
