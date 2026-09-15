// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// Batch 7c — enrollment customization. The V2 resource CRUD flows
// around a single EnrollmentCustomizationV2 fixture per run; the V1
// preview endpoints mount panels (text / ldap / sso) underneath that
// customization. Each panel has its own create/get/update/delete;
// list-all reads them back by id.
//
// Image upload/download uses the PNG fixture at testdata/
// jamf-colour-dark.png — a small real image rather than synthesised
// bytes so the server's content-type validation accepts it.

// --- /v1 singleton -----------------------------------------------------

func TestAcceptance_Pro_EnrollmentCustomization_ParseMarkdownV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	src := "# sdk-acc\n\nThis is a **probe** paragraph."
	got, err := p.ParseEnrollmentCustomizationMarkdownV1(ctx, &pro.Markdown{Markdown: &src})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ParseEnrollmentCustomizationMarkdownV1: %v", err)
	}
	if got.Markdown == nil {
		t.Fatal("ParseEnrollmentCustomizationMarkdownV1: nil markdown in response")
	}
	t.Logf("Parsed markdown: %d bytes", len(*got.Markdown))
}

// --- /v2 customization CRUD + sub-resources ----------------------------

// newEnrollmentCustomization creates a fresh customization for a test.
// Server validates that color fields match ^$|[a-fA-F0-9]{6} — six-hex
// digits, no #. Registers cleanupDelete on the returned id.
func newEnrollmentCustomization(t *testing.T, p *pro.Client, label string) string {
	t.Helper()
	ctx := context.Background()

	body := &pro.EnrollmentCustomizationV2{
		DisplayName: "sdk-acc-" + label + "-" + runSuffix(),
		Description: "sdk-acc test",
		SiteID:      "-1",
		EnrollmentCustomizationBrandingSettings: pro.EnrollmentCustomizationBrandingSettings{
			BackgroundColor: "FFFFFF",
			ButtonColor:     "1A73E8",
			ButtonTextColor: "FFFFFF",
			TextColor:       "202124",
		},
	}
	created, err := p.CreateEnrollmentCustomizationV2(ctx, body)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateEnrollmentCustomizationV2: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateEnrollmentCustomizationV2 returned no ID")
	}
	cleanupDelete(t, "DeleteEnrollmentCustomizationV2", func() error { return p.DeleteEnrollmentCustomizationV2(ctx, created.ID) })
	return created.ID
}

func TestAcceptance_Pro_EnrollmentCustomization_ListV2(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListEnrollmentCustomizationsV2(context.Background(), nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListEnrollmentCustomizationsV2: %v", err)
	}
	t.Logf("Found %d enrollment customizations", len(items))
}

func TestAcceptance_Pro_EnrollmentCustomization_CRUDV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	id := newEnrollmentCustomization(t, p, "crud")
	t.Logf("Created customization %s", id)

	got, err := p.GetEnrollmentCustomizationV2(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetEnrollmentCustomizationV2(%s): %v", id, err)
	}

	got.Description = "sdk-acc updated"
	updated, err := p.UpdateEnrollmentCustomizationV2(ctx, id, got)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateEnrollmentCustomizationV2(%s): %v", id, err)
	}
	if updated.Description != "sdk-acc updated" {
		t.Errorf("Description = %q, want %q", updated.Description, "sdk-acc updated")
	}

	if _, err := p.CreateEnrollmentCustomizationHistoryNoteV2(ctx, id, &pro.ObjectHistoryNote{
		Note: "sdk-acc test history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Errorf("CreateEnrollmentCustomizationHistoryNoteV2(%s): %v", id, err)
	}

	hist, err := p.ListEnrollmentCustomizationHistoryV2(ctx, id, nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListEnrollmentCustomizationHistoryV2(%s): %v", id, err)
	} else {
		t.Logf("History: %d entries", len(hist))
	}

	deps, err := p.ListEnrollmentCustomizationPrestagesV2(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListEnrollmentCustomizationPrestagesV2(%s): %v", id, err)
	} else {
		t.Logf("Prestage dependencies: %d", len(deps.Dependencies))
	}

	if err := p.DeleteEnrollmentCustomizationV2(ctx, id); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteEnrollmentCustomizationV2(%s): %v", id, err)
	}

	_, err = p.GetEnrollmentCustomizationV2(ctx, id)
	if err == nil {
		t.Fatalf("GetEnrollmentCustomizationV2(%s) after delete should 404", id)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetEnrollmentCustomizationV2(%s) after delete: want 404, got %v", id, err)
	}
}

// TestAcceptance_Pro_EnrollmentCustomization_ImageV2 uploads the bundled
// jamf-colour-dark.png PNG fixture and round-trips the download. The
// returned URL carries a server-minted id path segment that we parse off
// to drive the download endpoint.
func TestAcceptance_Pro_EnrollmentCustomization_ImageV2(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	const fixturePath = "testdata/jamf-colour-dark.png"
	f, err := os.Open(fixturePath)
	if err != nil {
		t.Skipf("fixture %s unavailable: %v", fixturePath, err)
	}
	t.Cleanup(func() { _ = f.Close() })

	resp, err := p.UploadEnrollmentCustomizationImageV2(ctx, "jamf-colour-dark.png", f)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UploadEnrollmentCustomizationImageV2: %v", err)
	}
	if resp.URL == "" {
		t.Fatal("UploadEnrollmentCustomizationImageV2 returned empty URL")
	}
	t.Logf("Uploaded image → %s", resp.URL)

	// Server returns a URL like
	// https://<tenant>.jamfcloud.com/api/v2/enrollment-customizations/images/<id>
	// — the last path segment is the id used by the download endpoint.
	slash := strings.LastIndex(resp.URL, "/")
	if slash < 0 {
		t.Fatalf("unexpected URL shape: %q", resp.URL)
	}
	imageID := resp.URL[slash+1:]

	body, err := p.DownloadEnrollmentCustomizationImageV2(ctx, imageID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DownloadEnrollmentCustomizationImageV2(%s): %v", imageID, err)
	}
	if len(body) == 0 {
		t.Errorf("download returned empty body")
	} else if !strings.HasPrefix(string(body[:8]), "\x89PNG\r\n\x1a\n") {
		t.Errorf("download body does not look like a PNG (first bytes: %x)", body[:8])
	}
	t.Logf("Downloaded image: %d bytes", len(body))
}

// --- /v1 panel CRUD — text panels --------------------------------------

func TestAcceptance_Pro_EnrollmentCustomization_TextPanelCRUDV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	ecID := newEnrollmentCustomization(t, p, "text-panel")

	created, err := p.CreateEnrollmentCustomizationTextPanelV1(ctx, ecID, &pro.EnrollmentCustomizationPanelText{
		DisplayName:        "sdk-acc text panel",
		Title:              "Welcome",
		Body:               "sdk-acc test panel body",
		Subtext:            new(""),
		BackButtonText:     "Back",
		ContinueButtonText: "Continue",
		Rank:               1,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateEnrollmentCustomizationTextPanelV1: %v", err)
	}
	panelID := fmt.Sprintf("%d", created.ID)
	cleanupDelete(t, "DeleteEnrollmentCustomizationTextPanelV1", func() error {
		return p.DeleteEnrollmentCustomizationTextPanelV1(ctx, ecID, panelID)
	})
	t.Logf("Created text panel %s", panelID)

	got, err := p.GetEnrollmentCustomizationTextPanelV1(ctx, ecID, panelID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetEnrollmentCustomizationTextPanelV1(%s,%s): %v", ecID, panelID, err)
	}
	if got.Title != "Welcome" {
		t.Errorf("Title = %q, want Welcome", got.Title)
	}

	// Markdown sub-endpoint on the text panel.
	md, err := p.GetEnrollmentCustomizationTextPanelMarkdownV1(ctx, ecID, panelID)
	if err != nil {
		skipOnServerError(t, err)
		t.Errorf("GetEnrollmentCustomizationTextPanelMarkdownV1(%s,%s): %v", ecID, panelID, err)
	} else if md.Markdown == nil {
		t.Errorf("Markdown.Markdown nil")
	} else {
		t.Logf("Text panel markdown: %d bytes", len(*md.Markdown))
	}

	// Update via PUT — response type is the Get variant, shape matches.
	update := &pro.EnrollmentCustomizationPanelText{
		DisplayName:        "sdk-acc text panel (updated)",
		Title:              "Welcome updated",
		Body:               "updated body",
		Subtext:            new(""),
		BackButtonText:     "Back",
		ContinueButtonText: "Continue",
		Rank:               1,
	}
	if _, err := p.UpdateEnrollmentCustomizationTextPanelV1(ctx, ecID, panelID, update); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdateEnrollmentCustomizationTextPanelV1(%s,%s): %v", ecID, panelID, err)
	}

	// List all panels — should include our id.
	all, err := p.ListEnrollmentCustomizationPanelsV1(ctx, ecID)
	if err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListEnrollmentCustomizationPanelsV1(%s): %v", ecID, err)
	} else {
		found := false
		for _, panel := range all.Panels {
			if fmt.Sprintf("%d", panel.ID) == panelID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("panel %s not in list-all response", panelID)
		}
		t.Logf("Customization %s has %d panel(s)", ecID, len(all.Panels))
	}

	// Get via generic panel endpoint.
	generic, err := p.GetEnrollmentCustomizationPanelV1(ctx, ecID, panelID)
	if err != nil {
		skipOnServerError(t, err)
		t.Errorf("GetEnrollmentCustomizationPanelV1(%s,%s): %v", ecID, panelID, err)
	} else if generic.Type == "" {
		t.Errorf("generic panel get: Type empty, expected a discriminator")
	}

	if err := p.DeleteEnrollmentCustomizationTextPanelV1(ctx, ecID, panelID); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteEnrollmentCustomizationTextPanelV1(%s,%s): %v", ecID, panelID, err)
	}
}

// TestAcceptance_Pro_EnrollmentCustomization_LdapPanelCRUDV1 requires a
// real LDAP server association. Create 400s with ldapGroupAccess=[] or
// a bogus ldapServerId. Plumbing-only test: try with empty group access
// and tolerate the 400 rejection.
func TestAcceptance_Pro_EnrollmentCustomization_LdapPanelCRUDV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	ecID := newEnrollmentCustomization(t, p, "ldap-panel")

	_, err := p.CreateEnrollmentCustomizationLdapPanelV1(ctx, ecID, &pro.EnrollmentCustomizationPanelLdapAuth{
		DisplayName:        "sdk-acc ldap panel",
		Title:              "Sign in",
		BackButtonText:     "Back",
		ContinueButtonText: "Continue",
		UsernameLabel:      "Username",
		PasswordLabel:      "Password",
		Rank:               1,
		LdapGroupAccess:    &[]pro.EnrollmentCustomizationLdapGroupAccess{},
	})
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("CreateEnrollmentCustomizationLdapPanelV1 rejected: status=%d — expected without a configured LDAP server", apiErr.StatusCode)
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("CreateEnrollmentCustomizationLdapPanelV1: %v", err)
	}
	t.Skip("ldap panel created unexpectedly — add real group access + cleanup when a fixture LDAP server is available")
}

// TestAcceptance_Pro_EnrollmentCustomization_SsoPanelCRUDV1 requires SSO
// configured on the tenant. Empty group-enrollment-access + zero
// attributes will likely 400. Plumbing-only test.
func TestAcceptance_Pro_EnrollmentCustomization_SsoPanelCRUDV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	ecID := newEnrollmentCustomization(t, p, "sso-panel")

	_, err := p.CreateEnrollmentCustomizationSsoPanelV1(ctx, ecID, &pro.EnrollmentCustomizationPanelSsoAuth{
		DisplayName:                    "sdk-acc sso panel",
		LongNameAttribute:              "displayName",
		ShortNameAttribute:             "uid",
		Rank:                           1,
		IsGroupEnrollmentAccessEnabled: false,
		IsUseJamfConnect:               false,
	})
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("CreateEnrollmentCustomizationSsoPanelV1 rejected: status=%d — expected without SSO configured", apiErr.StatusCode)
			return
		}
		skipOnServerError(t, err)
		t.Fatalf("CreateEnrollmentCustomizationSsoPanelV1: %v", err)
	}
	t.Skip("sso panel created unexpectedly — add proper cleanup when a fixture SSO config is available")
}
