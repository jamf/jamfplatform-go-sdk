// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// Batch 11 — volume-purchasing-locations + volume-purchasing-
// subscriptions. Requires JAMFPLATFORM_ACC_PRO_VPP_TOKEN (base64 serviceToken
// from Apple Business Manager). Destructive ops (reclaim,
// revoke-licenses) are exercised against a self-created throwaway
// location registered from the env token — the location has no
// assignees so reclaim/revoke are API-level no-ops, but the endpoints
// get plumbing coverage without touching any productive VPP state.

func vppToken(t *testing.T) string {
	t.Helper()
	v := strings.Join(strings.Fields(accEnv("JAMFPLATFORM_ACC_PRO_VPP_TOKEN")), "")
	if v == "" {
		t.Skip("JAMFPLATFORM_ACC_PRO_VPP_TOKEN not set")
	}
	return v
}

// --- volume-purchasing-locations --------------------------------------

func TestAcceptance_Pro_Vpp_ListLocationsV1(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListVolumePurchasingLocationsV1(context.Background(), nil, "")
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListVolumePurchasingLocationsV1: %v", err)
	}
	t.Logf("VPP locations: %d", len(items))
}

// TestAcceptance_Pro_Vpp_LocationCRUDV1 creates a fresh VPP location
// from the env-var token, reads back, PATCHes a harmless display name,
// reads + notes history, and deletes. A fresh POST against the
// already-associated Apple Business Manager token is legitimate on the
// Jamf side (each POST registers a new location); Apple tracks the
// token's assignments per Jamf server identity so this creates a new
// place to receive sync'd content, not a second Apple-side account.
func TestAcceptance_Pro_Vpp_LocationCRUDV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	token := vppToken(t)

	name := "sdk-acc-vpp-loc-" + runSuffix()
	created, err := p.CreateVolumePurchasingLocationV1(ctx, &pro.VolumePurchasingLocationPost{
		Name:         &name,
		ServiceToken: token,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateVolumePurchasingLocationV1: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateVolumePurchasingLocationV1 returned no id")
	}
	id := created.ID
	cleanupDelete(t, "DeleteVolumePurchasingLocationV1", func() error { return p.DeleteVolumePurchasingLocationV1(ctx, id) })
	t.Logf("Created VPP location %s (%s)", id, name)

	got, err := p.GetVolumePurchasingLocationV1(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetVolumePurchasingLocationV1(%s): %v", id, err)
	}
	t.Logf("VPP location %s: locationName=%q countryCode=%q email=%q", id, got.LocationName, got.CountryCode, got.Email)

	// PATCH display name (merge-patch+json).
	newName := name + "-updated"
	if _, err := p.UpdateVolumePurchasingLocationV1(ctx, id, &pro.VolumePurchasingLocationPatch{Name: &newName}); err != nil {
		skipOnServerError(t, err)
		t.Errorf("UpdateVolumePurchasingLocationV1(%s): %v", id, err)
	}

	// Content list + history read.
	if content, err := p.ListVolumePurchasingLocationContentV1(ctx, id, nil, ""); err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListVolumePurchasingLocationContentV1(%s): %v", id, err)
	} else {
		t.Logf("VPP location %s content: %d items", id, len(content))
	}
	if _, err := p.CreateVolumePurchasingLocationHistoryNoteV1(ctx, id, &pro.ObjectHistoryNote{
		Note: "sdk-acc test VPP history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Errorf("CreateVolumePurchasingLocationHistoryNoteV1(%s): %v", id, err)
	}
	if hist, err := p.ListVolumePurchasingLocationHistoryV1(ctx, id, nil, ""); err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListVolumePurchasingLocationHistoryV1(%s): %v", id, err)
	} else {
		t.Logf("VPP location %s history: %d entries", id, len(hist))
	}

	// Reclaim + revoke-licenses against the throwaway location. No
	// assignees so these are no-ops server-side, but we prove the
	// plumbing. Running them here (rather than against a pre-existing
	// location the tenant cares about) keeps the destructive paths
	// isolated to state we own.
	if err := p.ReclaimVolumePurchasingLocationLicensesV1(ctx, id); err != nil {
		skipOnServerError(t, err)
		t.Errorf("ReclaimVolumePurchasingLocationLicensesV1(%s): %v", id, err)
	} else {
		t.Logf("Reclaim triggered on VPP location %s", id)
	}
	if err := p.RevokeVolumePurchasingLocationLicensesV1(ctx, id); err != nil {
		skipOnServerError(t, err)
		t.Errorf("RevokeVolumePurchasingLocationLicensesV1(%s): %v", id, err)
	} else {
		t.Logf("Revoke-licenses triggered on VPP location %s", id)
	}

	if err := p.DeleteVolumePurchasingLocationV1(ctx, id); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteVolumePurchasingLocationV1(%s): %v", id, err)
	}

	_, err = p.GetVolumePurchasingLocationV1(ctx, id)
	if err == nil {
		t.Fatalf("GetVolumePurchasingLocationV1(%s) after delete should 404", id)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetVolumePurchasingLocationV1(%s) after delete: want 404, got %v", id, err)
	}
}

// --- volume-purchasing-subscriptions ----------------------------------

func TestAcceptance_Pro_Vpp_ListSubscriptionsV1(t *testing.T) {
	c := accClient(t)

	items, err := pro.New(c).ListVolumePurchasingSubscriptionsV1(context.Background(), nil)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListVolumePurchasingSubscriptionsV1: %v", err)
	}
	t.Logf("VPP subscriptions: %d", len(items))
}

// TestAcceptance_Pro_Vpp_SubscriptionCRUDV1 creates a throwaway
// subscription with no recipients or locations (noop notifier),
// updates via PUT, history round-trip, deletes, verifies 404.
func TestAcceptance_Pro_Vpp_SubscriptionCRUDV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	name := "sdk-acc-vpp-sub-" + runSuffix()
	disabled := false
	created, err := p.CreateVolumePurchasingSubscriptionV1(ctx, &pro.VolumePurchasingSubscriptionBase{
		Name:    name,
		Enabled: &disabled,
	})
	if err != nil {
		skipOnServerError(t, err)
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Skipf("CreateVolumePurchasingSubscriptionV1 rejected: status=%d — server may require recipients/locations", apiErr.StatusCode)
		}
		t.Fatalf("CreateVolumePurchasingSubscriptionV1: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("CreateVolumePurchasingSubscriptionV1 returned no id")
	}
	id := created.ID
	cleanupDelete(t, "DeleteVolumePurchasingSubscriptionV1", func() error { return p.DeleteVolumePurchasingSubscriptionV1(ctx, id) })
	t.Logf("Created VPP subscription %s", id)

	got, err := p.GetVolumePurchasingSubscriptionV1(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetVolumePurchasingSubscriptionV1(%s): %v", id, err)
	}
	if got.Name != name {
		t.Errorf("Name = %q, want %q", got.Name, name)
	}

	newName := name + "-updated"
	if _, err := p.UpdateVolumePurchasingSubscriptionV1(ctx, id, &pro.VolumePurchasingSubscriptionBase{
		Name:    newName,
		Enabled: &disabled,
	}); err != nil {
		skipOnServerError(t, err)
		t.Errorf("UpdateVolumePurchasingSubscriptionV1(%s): %v", id, err)
	}

	if _, err := p.CreateVolumePurchasingSubscriptionHistoryNoteV1(ctx, id, &pro.ObjectHistoryNote{
		Note: "sdk-acc test VPP subscription history entry",
	}); err != nil {
		skipOnServerError(t, err)
		t.Errorf("CreateVolumePurchasingSubscriptionHistoryNoteV1(%s): %v", id, err)
	}
	if hist, err := p.ListVolumePurchasingSubscriptionHistoryV1(ctx, id, nil, ""); err != nil {
		skipOnServerError(t, err)
		t.Errorf("ListVolumePurchasingSubscriptionHistoryV1(%s): %v", id, err)
	} else {
		t.Logf("VPP subscription %s history: %d entries", id, len(hist))
	}

	if err := p.DeleteVolumePurchasingSubscriptionV1(ctx, id); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeleteVolumePurchasingSubscriptionV1(%s): %v", id, err)
	}

	_, err = p.GetVolumePurchasingSubscriptionV1(ctx, id)
	if err == nil {
		t.Fatalf("GetVolumePurchasingSubscriptionV1(%s) after delete should 404", id)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(404) {
		t.Fatalf("GetVolumePurchasingSubscriptionV1(%s) after delete: want 404, got %v", id, err)
	}
}
