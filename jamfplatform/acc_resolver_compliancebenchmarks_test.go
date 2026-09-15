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
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/compliancebenchmarks"
)

// createLightweightBenchmark provisions a minimal benchmark for resolver
// testing. Uses the first available baseline + 1 rule + the shared smart-group
// fixture so the server accepts the payload. Skips the sync wait — resolver
// cares only that the benchmark appears in ListBenchmarks, not that it has
// finished syncing.
func createLightweightBenchmark(t *testing.T, c *jamfplatform.Client, title string) string {
	t.Helper()
	ctx := context.Background()
	cb := compliancebenchmarks.New(c)

	baselines, err := cb.ListBaselines(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBaselines: %v", err)
	}
	if len(baselines.Baselines) == 0 {
		t.Skip("No baselines available — CB Engine not enabled")
	}
	baseline := baselines.Baselines[0]

	rules, err := cb.GetBaselineRules(ctx, baseline.BaselineID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetBaselineRules: %v", err)
	}
	if len(rules.Rules) == 0 {
		t.Skip("No rules available in baseline")
	}

	groupID := requireSmartGroupFixture(t)
	rr := compliancebenchmarks.RuleRequest{ID: rules.Rules[0].ID, Enabled: true}
	if rules.Rules[0].ODV != nil {
		rr.ODV = &compliancebenchmarks.ODVRequest{Value: rules.Rules[0].ODV.Value}
	}
	desc := "SDK acceptance test — safe to delete"
	resp, err := cb.CreateBenchmark(ctx, &compliancebenchmarks.BenchmarkRequestV2{
		Title:            title,
		Description:      &desc,
		SourceBaselineID: baseline.BaselineID,
		Rules:            []compliancebenchmarks.RuleRequest{rr},
		Target:           compliancebenchmarks.TargetV2{DeviceGroups: []string{groupID}},
		EnforcementMode:  compliancebenchmarks.BenchmarkRequestV2EnforcementModeMonitor,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateBenchmark(%q): %v", title, err)
	}
	t.Cleanup(func() { ensureBenchmarkDeletedByID(t, c, ctx, resp.BenchmarkID) })
	return resp.BenchmarkID
}

func TestAcceptance_ResolveBenchmarkIDByName(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	cb := compliancebenchmarks.New(c)

	title := "sdk-acc-resolver-bm-id-" + runSuffix()
	wantID := createLightweightBenchmark(t, c, title)

	gotID, err := cb.ResolveBenchmarkIDByName(ctx, title)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveBenchmarkIDByName(%q): %v", title, err)
	}
	if gotID != wantID {
		t.Errorf("resolved ID = %q, want %q", gotID, wantID)
	}
	t.Logf("Resolved benchmark %q -> %s (clientFilter mode over 'benchmarks' wrapper)", title, gotID)
}

func TestAcceptance_ResolveBenchmarkByName(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	cb := compliancebenchmarks.New(c)

	title := "sdk-acc-resolver-bm-typed-" + runSuffix()
	wantID := createLightweightBenchmark(t, c, title)

	got, err := cb.ResolveBenchmarkByName(ctx, title)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveBenchmarkByName(%q): %v", title, err)
	}
	if got == nil {
		t.Fatal("ResolveBenchmarkByName returned nil without error")
	}
	if got.ID != wantID {
		t.Errorf("typed result ID = %q, want %q", got.ID, wantID)
	}
	if got.Title != title {
		t.Errorf("typed result Title = %q, want %q", got.Title, title)
	}
	t.Logf("Resolved typed benchmark %q -> ID %s", title, got.ID)
}

func TestAcceptance_ResolveBenchmarkIDByName_NotFound(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	cb := compliancebenchmarks.New(c)

	probe := "sdk-does-not-exist-bm-" + runSuffix()
	_, err := cb.ResolveBenchmarkIDByName(ctx, probe)
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

func TestAcceptance_ResolveBenchmark_Ambiguous(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	cb := compliancebenchmarks.New(c)

	shared := "sdk-acc-resolver-bm-dup-" + runSuffix()
	firstID := createLightweightBenchmark(t, c, shared)

	// Attempt to provision a second benchmark with the same title. Re-using
	// createLightweightBenchmark here would register a second t.Cleanup; the
	// raw call below keeps cleanup routing explicit.
	baselines, err := cb.ListBaselines(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBaselines: %v", err)
	}
	baseline := baselines.Baselines[0]
	rules, err := cb.GetBaselineRules(ctx, baseline.BaselineID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetBaselineRules: %v", err)
	}
	groupID := requireSmartGroupFixture(t)
	rr := compliancebenchmarks.RuleRequest{ID: rules.Rules[0].ID, Enabled: true}
	if rules.Rules[0].ODV != nil {
		rr.ODV = &compliancebenchmarks.ODVRequest{Value: rules.Rules[0].ODV.Value}
	}
	desc := "SDK acceptance test — duplicate-title probe"
	resp, err := cb.CreateBenchmark(ctx, &compliancebenchmarks.BenchmarkRequestV2{
		Title:            shared,
		Description:      &desc,
		SourceBaselineID: baseline.BaselineID,
		Rules:            []compliancebenchmarks.RuleRequest{rr},
		Target:           compliancebenchmarks.TargetV2{DeviceGroups: []string{groupID}},
		EnforcementMode:  compliancebenchmarks.BenchmarkRequestV2EnforcementModeMonitor,
	})
	if err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Skipf("server rejects duplicate benchmark titles (%d) — nothing to disambiguate: %v", apiErr.StatusCode, apiErr.Summary())
		}
		skipOnServerError(t, err)
		t.Fatalf("CreateBenchmark (duplicate) failed unexpectedly: %v", err)
	}
	t.Cleanup(func() { ensureBenchmarkDeletedByID(t, c, ctx, resp.BenchmarkID) })

	_, err = cb.ResolveBenchmarkIDByName(ctx, shared)
	if err == nil {
		t.Fatalf("expected ambiguous match error for duplicate title %q, got nil", shared)
	}
	var amErr *jamfplatform.AmbiguousMatchError
	if !errors.As(err, &amErr) {
		t.Fatalf("expected *AmbiguousMatchError, got %T: %v", err, err)
	}
	foundFirst, foundSecond := false, false
	for _, m := range amErr.Matches {
		if m == firstID {
			foundFirst = true
		}
		if m == resp.BenchmarkID {
			foundSecond = true
		}
	}
	if !foundFirst || !foundSecond {
		t.Errorf("AmbiguousMatchError.Matches = %v, want to contain both %q and %q", amErr.Matches, firstID, resp.BenchmarkID)
	}
	t.Logf("Ambiguous benchmark resolve surfaced %d matches: %v", len(amErr.Matches), amErr.Matches)
}

func TestAcceptance_ResolveBaselineIDByName(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	cb := compliancebenchmarks.New(c)

	baselines, err := cb.ListBaselines(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBaselines: %v", err)
	}
	if len(baselines.Baselines) == 0 {
		t.Skip("No baselines available — CB Engine not enabled")
	}
	// Baselines are tenant-global CIS/STIG templates — can't provision; pick
	// the first one and resolve its title. Different tenants will find
	// different baselines, which is fine for this smoke check.
	want := baselines.Baselines[0]

	gotID, err := cb.ResolveBaselineIDByName(ctx, want.Title)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveBaselineIDByName(%q): %v", want.Title, err)
	}
	if gotID != want.ID {
		t.Errorf("resolved ID = %q, want %q", gotID, want.ID)
	}
	t.Logf("Resolved baseline %q -> %s (clientFilter over 'baselines' wrapper)", want.Title, gotID)
}

func TestAcceptance_ResolveBaselineByName(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	cb := compliancebenchmarks.New(c)

	baselines, err := cb.ListBaselines(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBaselines: %v", err)
	}
	if len(baselines.Baselines) == 0 {
		t.Skip("No baselines available — CB Engine not enabled")
	}
	want := baselines.Baselines[0]

	got, err := cb.ResolveBaselineByName(ctx, want.Title)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ResolveBaselineByName(%q): %v", want.Title, err)
	}
	if got == nil {
		t.Fatal("ResolveBaselineByName returned nil without error")
	}
	if got.ID != want.ID {
		t.Errorf("typed result ID = %q, want %q", got.ID, want.ID)
	}
	if got.Title != want.Title {
		t.Errorf("typed result Title = %q, want %q", got.Title, want.Title)
	}
	t.Logf("Resolved typed baseline %q -> ID %s (%d rules)", got.Title, got.ID, got.RuleCount)
}

func TestAcceptance_ResolveBaselineIDByName_NotFound(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	cb := compliancebenchmarks.New(c)

	probe := "sdk-does-not-exist-baseline-" + runSuffix()
	_, err := cb.ResolveBaselineIDByName(ctx, probe)
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
