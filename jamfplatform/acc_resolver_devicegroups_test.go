// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/devicegroups"
)

func createStaticTestGroup(t *testing.T, dg *devicegroups.Client, name string) string {
	t.Helper()
	ctx := context.Background()
	desc := "SDK acceptance test — safe to delete"
	empty := []string{}
	resp, err := dg.CreateDeviceGroup(ctx, &devicegroups.DeviceGroupCreateRepresentationV1{
		Name:        name,
		Description: &desc,
		DeviceType:  "COMPUTER",
		GroupType:   "STATIC",
		Members:     &empty,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateDeviceGroup(%q): %v", name, err)
	}
	cleanupDelete(t, "DeleteDeviceGroup", func() error { return dg.DeleteDeviceGroup(ctx, resp.ID) })
	return resp.ID
}

func TestAcceptance_ResolveDeviceGroupIDByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	dg := devicegroups.New(c)

	name := "sdk-acc-resolver-dg-id-" + runSuffix()
	wantID := createStaticTestGroup(t, dg, name)

	gotID, err := dg.ResolveDeviceGroupIDByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveDeviceGroupIDByName(%q): %v", name, err)
	}
	if gotID != wantID {
		t.Errorf("resolved ID = %q, want %q", gotID, wantID)
	}
	t.Logf("Resolved %q -> %s (filtered mode, server-side RSQL)", name, gotID)
}

func TestAcceptance_ResolveDeviceGroupByName(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	dg := devicegroups.New(c)

	name := "sdk-acc-resolver-dg-typed-" + runSuffix()
	wantID := createStaticTestGroup(t, dg, name)

	got, err := dg.ResolveDeviceGroupByName(ctx, name)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveDeviceGroupByName(%q): %v", name, err)
	}
	if got == nil {
		t.Fatal("ResolveDeviceGroupByName returned nil without error")
	}
	if got.ID != wantID {
		t.Errorf("typed result ID = %q, want %q", got.ID, wantID)
	}
	if got.Name != name {
		t.Errorf("typed result Name = %q, want %q", got.Name, name)
	}
	if got.GroupType != "STATIC" {
		t.Errorf("typed result GroupType = %q, want STATIC", got.GroupType)
	}
	t.Logf("Resolved typed %q -> ID %s (%s)", name, got.ID, got.GroupType)
}

func TestAcceptance_ResolveDeviceGroupIDByName_NotFound(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	dg := devicegroups.New(c)

	probe := "sdk-does-not-exist-dg-" + runSuffix()
	_, err := dg.ResolveDeviceGroupIDByName(ctx, probe)
	if err == nil {
		t.Fatalf("expected not-found error for %q, got nil", probe)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIResponseError, got %T: %v", err, err)
	}
	if !apiErr.HasStatus(http.StatusNotFound) {
		t.Fatalf("expected status 404, got %d: %v", apiErr.StatusCode, err)
	}
	t.Logf("Not-found probe surfaced APIResponseError(404) as expected")
}

// TestAcceptance_ResolveDeviceGroup_Ambiguous exercises the ambiguity path
// using the one dimension where the server permits duplicates: group name is
// unique *within* a (GroupType, DeviceType) combination, but the same name
// can exist on a SMART+COMPUTER group and a STATIC+COMPUTER group
// simultaneously. Same applies across DeviceType (COMPUTER vs MOBILE).
// Resolver only sees the name — so two groups sharing it must surface as
// *AmbiguousMatchError with both IDs.
func TestAcceptance_ResolveDeviceGroup_Ambiguous(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	dg := devicegroups.New(c)

	shared := "sdk-acc-resolver-dg-dup-" + runSuffix()

	// First: STATIC+COMPUTER.
	staticID := createStaticTestGroup(t, dg, shared)

	// Second: STATIC+MOBILE with the same name. Server enforces uniqueness
	// within DeviceType (confirmed: even STATIC vs SMART within COMPUTER is
	// rejected with 422), but a MOBILE group can coexist with a COMPUTER
	// group of the same name.
	desc := "SDK acceptance test — MOBILE duplicate-name probe"
	empty := []string{}
	resp, err := dg.CreateDeviceGroup(ctx, &devicegroups.DeviceGroupCreateRepresentationV1{
		Name:        shared,
		Description: &desc,
		DeviceType:  "MOBILE",
		GroupType:   "STATIC",
		Members:     &empty,
	})
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Skipf("server rejects same-name across DeviceType dimension too (%d) — uniqueness is fully global on this tenant: %v", apiErr.StatusCode, apiErr.Summary())
		}
		skipOnServerError(t, err)
		t.Fatalf("CreateDeviceGroup (MOBILE duplicate) failed unexpectedly: %v", err)
	}
	cleanupDelete(t, "DeleteDeviceGroup", func() error { return dg.DeleteDeviceGroup(ctx, resp.ID) })
	mobileID := resp.ID

	_, err = dg.ResolveDeviceGroupIDByName(ctx, shared)
	if err == nil {
		t.Fatalf("expected ambiguous match error for duplicate name %q, got nil", shared)
	}
	var amErr *jamfplatform.AmbiguousMatchError
	if !errors.As(err, &amErr) {
		t.Fatalf("expected *AmbiguousMatchError, got %T: %v", err, err)
	}
	if len(amErr.Matches) < 2 {
		t.Errorf("AmbiguousMatchError.Matches = %v, want at least 2", amErr.Matches)
	}
	foundComputer, foundMobile := false, false
	for _, m := range amErr.Matches {
		if m == staticID {
			foundComputer = true
		}
		if m == mobileID {
			foundMobile = true
		}
	}
	if !foundComputer || !foundMobile {
		t.Errorf("AmbiguousMatchError.Matches = %v, want to contain both %q (COMPUTER) and %q (MOBILE)", amErr.Matches, staticID, mobileID)
	}
	t.Logf("Ambiguous device-group resolve (COMPUTER + MOBILE) surfaced %d matches: %v", len(amErr.Matches), amErr.Matches)
}

// TestAcceptance_ResolveDeviceGroup_StaticUniqueWithinType confirms the
// server's uniqueness scope: two STATIC+COMPUTER groups with the same name
// are rejected, so ambiguity cannot arise from that dimension alone.
// Documents the server behaviour so future regressions are visible.
func TestAcceptance_ResolveDeviceGroup_StaticUniqueWithinType(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	dg := devicegroups.New(c)

	shared := "sdk-acc-resolver-dg-within-type-" + runSuffix()
	createStaticTestGroup(t, dg, shared)

	// The server's own uniqueness check reads the existing set, and group
	// reads lag group writes (see settleUntilFound) — so a duplicate submitted
	// immediately is intermittently accepted, which reads as the server not
	// enforcing uniqueness at all. Wait for the first group to be visible
	// before probing, or this test asserts a race rather than the rule.
	settleUntilFound(t, "ResolveDeviceGroupIDByName before duplicate probe", func() error {
		_, err := dg.ResolveDeviceGroupIDByName(ctx, shared)
		return err
	})

	desc := "SDK acceptance test — within-type uniqueness probe"
	empty := []string{}
	_, err := dg.CreateDeviceGroup(ctx, &devicegroups.DeviceGroupCreateRepresentationV1{
		Name:        shared,
		Description: &desc,
		DeviceType:  "COMPUTER",
		GroupType:   "STATIC",
		Members:     &empty,
	})
	if err == nil {
		t.Fatalf("expected server to reject duplicate STATIC+COMPUTER name %q, but it succeeded", shared)
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIResponseError for duplicate STATIC+COMPUTER, got %T: %v", err, err)
	}
	if apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		t.Fatalf("expected 4xx rejection, got %d: %v", apiErr.StatusCode, err)
	}
	t.Logf("Server rejects duplicate STATIC+COMPUTER name with %d as expected: %s", apiErr.StatusCode, apiErr.Summary())
}
