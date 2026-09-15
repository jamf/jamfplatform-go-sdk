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
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/blueprints"
)

func TestAcceptance_ResolveBlueprintIDByName(t *testing.T) {
	groupID := requireSmartGroupFixture(t)
	c := accEnvClient(t)
	ctx := context.Background()
	bp := blueprints.New(c)

	name := "sdk-acc-resolver-id-" + runSuffix()
	fixture := createTestBlueprint(t, c, name, groupID, []blueprints.BlueprintStep{})

	got, err := bp.ResolveBlueprintIDByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveBlueprintIDByName(%q): %v", name, err)
	}
	if got != fixture.ID {
		t.Errorf("resolved ID = %q, want %q", got, fixture.ID)
	}
	t.Logf("Resolved %q -> %s", name, got)
}

func TestAcceptance_ResolveBlueprintByName(t *testing.T) {
	groupID := requireSmartGroupFixture(t)
	c := accEnvClient(t)
	ctx := context.Background()
	bp := blueprints.New(c)

	name := "sdk-acc-resolver-typed-" + runSuffix()
	fixture := createTestBlueprint(t, c, name, groupID, []blueprints.BlueprintStep{})

	got, err := bp.ResolveBlueprintByName(ctx, name)
	if err != nil {
		t.Fatalf("ResolveBlueprintByName(%q): %v", name, err)
	}
	if got == nil {
		t.Fatal("ResolveBlueprintByName returned nil result without error")
	}
	if got.ID != fixture.ID {
		t.Errorf("typed result ID = %q, want %q", got.ID, fixture.ID)
	}
	if got.Name != name {
		t.Errorf("typed result Name = %q, want %q", got.Name, name)
	}
	t.Logf("Resolved typed %q -> ID %s", name, got.ID)
}

func TestAcceptance_ResolveBlueprintIDByName_NotFound(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	bp := blueprints.New(c)

	probe := "sdk-does-not-exist-" + runSuffix()
	_, err := bp.ResolveBlueprintIDByName(ctx, probe)
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

// TestAcceptance_ResolveBlueprint_SimilarNames guards against the
// clientFilter mode's server-side search=name narrowing producing false
// positives. Blueprints' ?search= matches substrings, so creating two
// blueprints whose names share a prefix should still resolve only the one
// with the exact name match.
func TestAcceptance_ResolveBlueprint_SimilarNames(t *testing.T) {
	groupID := requireSmartGroupFixture(t)
	c := accEnvClient(t)
	ctx := context.Background()
	bp := blueprints.New(c)
	suffix := runSuffix()

	baseName := "sdk-acc-resolver-prefix-" + suffix
	extraName := baseName + "-extra"
	longerName := baseName + "-longer-suffix"

	base := createTestBlueprint(t, c, baseName, groupID, []blueprints.BlueprintStep{})
	createTestBlueprint(t, c, extraName, groupID, []blueprints.BlueprintStep{})
	createTestBlueprint(t, c, longerName, groupID, []blueprints.BlueprintStep{})

	// Exact match must resolve only the base blueprint, not the two that
	// share the prefix.
	id, err := bp.ResolveBlueprintIDByName(ctx, baseName)
	if err != nil {
		t.Fatalf("ResolveBlueprintIDByName(%q): %v", baseName, err)
	}
	if id != base.ID {
		t.Errorf("resolved prefix-matching name to wrong blueprint: got %q, want %q", id, base.ID)
	}
	t.Logf("Prefix-safe resolve: %q -> %s (two siblings with same prefix ignored)", baseName, id)
}

// TestAcceptance_ResolveBlueprint_Ambiguous verifies the resolver surfaces
// *AmbiguousMatchError when two resources legitimately share the same
// name. If the server rejects duplicate names at create-time (server-side
// uniqueness constraint), the test skips with the rejection status logged
// — that's server behaviour worth recording, not a resolver defect.
func TestAcceptance_ResolveBlueprint_Ambiguous(t *testing.T) {
	groupID := requireSmartGroupFixture(t)
	c := accEnvClient(t)
	ctx := context.Background()
	bp := blueprints.New(c)

	shared := "sdk-acc-resolver-dup-" + runSuffix()
	first := createTestBlueprint(t, c, shared, groupID, []blueprints.BlueprintStep{})

	// Try to create a second blueprint with the same name. Use the raw
	// create path rather than the helper so a 4xx from the server doesn't
	// t.Fatal us — we need to interpret the response.
	desc := "SDK acceptance test — duplicate-name probe"
	resp, err := bp.CreateBlueprint(ctx, &blueprints.CreateBlueprintRequest{
		Name:        shared,
		Description: &desc,
		Scope:       blueprints.CreateScope{DeviceGroups: []string{groupID}},
		Steps:       []blueprints.BlueprintStep{},
	})
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Skipf("server rejects duplicate blueprint names (%d) — uniqueness enforced server-side, nothing for resolver to disambiguate: %v", apiErr.StatusCode, apiErr.Summary())
		}
		t.Fatalf("CreateBlueprint (duplicate) failed unexpectedly: %v", err)
	}
	cleanupDelete(t, "DeleteBlueprint", func() error { return bp.DeleteBlueprint(ctx, resp.ID) })

	_, err = bp.ResolveBlueprintIDByName(ctx, shared)
	if err == nil {
		t.Fatalf("expected ambiguous match error for duplicate name %q, got nil", shared)
	}
	var amErr *jamfplatform.AmbiguousMatchError
	if !errors.As(err, &amErr) {
		t.Fatalf("expected *AmbiguousMatchError, got %T: %v", err, err)
	}
	if amErr.Name != shared {
		t.Errorf("AmbiguousMatchError.Name = %q, want %q", amErr.Name, shared)
	}
	if len(amErr.Matches) < 2 {
		t.Errorf("AmbiguousMatchError.Matches = %v, want at least 2 entries", amErr.Matches)
	}
	// Sanity: the two IDs we created should both be present.
	foundFirst, foundSecond := false, false
	for _, m := range amErr.Matches {
		if m == first.ID {
			foundFirst = true
		}
		if m == resp.ID {
			foundSecond = true
		}
	}
	if !foundFirst || !foundSecond {
		t.Errorf("AmbiguousMatchError.Matches = %v, want to contain both %q and %q", amErr.Matches, first.ID, resp.ID)
	}
	t.Logf("Ambiguous resolve surfaced %d matches as expected: %v", len(amErr.Matches), amErr.Matches)
}

// ─── Blueprint Components ──────────────────────────────────────────────────
// Components are platform-managed — no create/delete. Read-only probe.

func TestAcceptance_ResolveBlueprintComponentIDByName_NotFound(t *testing.T) {
	c := accEnvClient(t)
	bp := blueprints.New(c)
	_, err := bp.ResolveBlueprintComponentIDByName(context.Background(), "sdk-does-not-exist-bpc-"+runSuffix())
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) || !apiErr.HasStatus(http.StatusNotFound) {
		t.Fatalf("expected APIResponseError(404), got %T: %v", err, err)
	}
	t.Log("not-found surfaced 404 ✓")
}

func TestAcceptance_ResolveBlueprintComponentIDByName_Existing(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	bp := blueprints.New(c)

	components, err := bp.ListBlueprintComponents(ctx)
	if err != nil {
		t.Fatalf("ListBlueprintComponents: %v", err)
	}
	if len(components) == 0 {
		t.Skip("no blueprint components — skipping")
	}
	first := components[0]
	gotID, err := bp.ResolveBlueprintComponentIDByName(ctx, first.Name)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if gotID != first.Identifier {
		t.Errorf("resolved id = %q, want %q", gotID, first.Identifier)
	}
	t.Logf("resolved %q → %s ✓", first.Name, gotID)
}

func TestAcceptance_ResolveBlueprintComponentByName_Existing(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	bp := blueprints.New(c)

	components, err := bp.ListBlueprintComponents(ctx)
	if err != nil {
		t.Fatalf("ListBlueprintComponents: %v", err)
	}
	if len(components) == 0 {
		t.Skip("no blueprint components — skipping")
	}
	first := components[0]
	got, err := bp.ResolveBlueprintComponentByName(ctx, first.Name)
	if err != nil {
		t.Fatalf("resolve typed: %v", err)
	}
	if got == nil {
		t.Fatal("resolve returned nil")
	}
	if got.Identifier != first.Identifier {
		t.Errorf("typed Identifier = %q, want %q", got.Identifier, first.Identifier)
	}
	if got.Name != first.Name {
		t.Errorf("typed Name = %q, want %q", got.Name, first.Name)
	}
	t.Logf("resolved typed %q → %s ✓", first.Name, got.Identifier)
}
