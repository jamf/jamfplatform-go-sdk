// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/compliancebenchmarks"
)

func TestAcceptance_ListBaselines(t *testing.T) {
	c := accEnvClient(t)

	baselines, err := compliancebenchmarks.New(c).ListBaselines(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBaselines failed: %v", err)
	}
	if len(baselines.Baselines) == 0 {
		t.Log("No baselines found — CB Engine may not be enabled")
		return
	}
	t.Logf("Found %d baselines:", len(baselines.Baselines))
	for _, b := range baselines.Baselines {
		t.Logf("  %s (%s) — %d rules", b.Title, b.BaselineID, b.RuleCount)
	}
}

func TestAcceptance_GetBaselineRules(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	cb := compliancebenchmarks.New(c)

	baselines, err := cb.ListBaselines(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBaselines failed: %v", err)
	}
	if len(baselines.Baselines) == 0 {
		t.Skip("No baselines available — cannot fetch rules")
	}

	baseline := baselines.Baselines[0]
	rules, err := cb.GetBaselineRules(ctx, baseline.BaselineID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetBaselineRules failed for %q: %v", baseline.BaselineID, err)
	}

	t.Logf("Found %d rules for baseline %q", len(rules.Rules), baseline.Title)
	t.Logf("  Sources: %d", len(rules.Sources))

	rulesWithODV := 0
	for _, r := range rules.Rules {
		if r.ODV != nil {
			rulesWithODV++
		}
	}
	t.Logf("  Rules with ODV: %d / %d", rulesWithODV, len(rules.Rules))
}

func TestAcceptance_ListBenchmarks(t *testing.T) {
	c := accEnvClient(t)

	benchmarks, err := compliancebenchmarks.New(c).ListBenchmarks(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBenchmarks failed: %v", err)
	}
	t.Logf("Found %d benchmarks", len(benchmarks.Benchmarks))
	for _, b := range benchmarks.Benchmarks {
		t.Logf("  %s (%s) — sync: %s", b.Title, b.ID, b.SyncState)
	}
}

func TestAcceptance_Benchmark_CreateAndDelete(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	cb := compliancebenchmarks.New(c)

	baselines, err := cb.ListBaselines(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBaselines failed: %v", err)
	}
	if len(baselines.Baselines) == 0 {
		t.Skip("No baselines available — CB Engine may not be enabled")
	}

	groupID := requireSmartGroupFixture(t)
	baseline := baselines.Baselines[0]

	rules, err := cb.GetBaselineRules(ctx, baseline.BaselineID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetBaselineRules failed: %v", err)
	}
	if len(rules.Rules) == 0 {
		t.Skip("No rules available for baseline")
	}

	// Build rule requests — enable first 5 rules (or fewer), including ODV values
	var ruleRequests []compliancebenchmarks.RuleRequest
	limit := min(len(rules.Rules), 5)
	for _, r := range rules.Rules[:limit] {
		rr := compliancebenchmarks.RuleRequest{
			ID:      r.ID,
			Enabled: true,
		}
		if r.ODV != nil {
			rr.ODV = &compliancebenchmarks.ODVRequest{Value: r.ODV.Value}
		}
		ruleRequests = append(ruleRequests, rr)
	}

	title := "sdk-acc-benchmark-" + runSuffix()
	benchmarkDesc := "SDK acceptance test — safe to delete"

	resp, err := cb.CreateBenchmark(ctx, &compliancebenchmarks.BenchmarkRequestV2{
		Title:            title,
		Description:      &benchmarkDesc,
		SourceBaselineID: baseline.BaselineID,
		Rules:            ruleRequests,
		Target:           compliancebenchmarks.TargetV2{DeviceGroups: []string{groupID}},
		EnforcementMode:  compliancebenchmarks.BenchmarkRequestV2EnforcementModeMonitor,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateBenchmark failed: %v", err)
	}
	t.Logf("Created benchmark %q (ID: %s)", title, resp.BenchmarkID)

	t.Cleanup(func() {
		ensureBenchmarkDeletedByID(t, c, ctx, resp.BenchmarkID)
	})

	// Wait for sync then verify
	waitForBenchmarkSyncState(t, c, ctx, resp.BenchmarkID)

	bm, err := cb.GetBenchmark(ctx, resp.BenchmarkID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetBenchmark failed: %v", err)
	}
	if bm.Title != title {
		t.Errorf("expected title %q, got %q", title, bm.Title)
	}
	if bm.EnforcementMode != "MONITOR" {
		t.Errorf("expected MONITOR, got %q", bm.EnforcementMode)
	}
	t.Logf("Benchmark synced: %s, rules: %d", resp.BenchmarkID, len(bm.Rules))
}

func TestAcceptance_Benchmark_Reporting(t *testing.T) {
	c := accEnvClient(t)
	ctx := context.Background()
	cb := compliancebenchmarks.New(c)

	baselines, err := cb.ListBaselines(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListBaselines failed: %v", err)
	}
	if len(baselines.Baselines) == 0 {
		t.Skip("No baselines available — CB Engine may not be enabled")
	}

	groupID := requireSmartGroupFixture(t)
	baseline := baselines.Baselines[0]

	rules, err := cb.GetBaselineRules(ctx, baseline.BaselineID)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetBaselineRules failed: %v", err)
	}
	if len(rules.Rules) == 0 {
		t.Skip("No rules available for baseline")
	}

	var ruleRequests []compliancebenchmarks.RuleRequest
	limit := min(len(rules.Rules), 3)
	for _, r := range rules.Rules[:limit] {
		rr := compliancebenchmarks.RuleRequest{
			ID:      r.ID,
			Enabled: true,
		}
		if r.ODV != nil {
			rr.ODV = &compliancebenchmarks.ODVRequest{Value: r.ODV.Value}
		}
		ruleRequests = append(ruleRequests, rr)
	}

	title := "sdk-acc-reporting-" + runSuffix()
	reportingDesc := "SDK acceptance test — reporting endpoints"

	resp, err := cb.CreateBenchmark(ctx, &compliancebenchmarks.BenchmarkRequestV2{
		Title:            title,
		Description:      &reportingDesc,
		SourceBaselineID: baseline.BaselineID,
		Rules:            ruleRequests,
		Target:           compliancebenchmarks.TargetV2{DeviceGroups: []string{groupID}},
		EnforcementMode:  compliancebenchmarks.BenchmarkRequestV2EnforcementModeMonitor,
	})
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("CreateBenchmark failed: %v", err)
	}
	t.Cleanup(func() { ensureBenchmarkDeletedByID(t, c, ctx, resp.BenchmarkID) })

	waitForBenchmarkSyncState(t, c, ctx, resp.BenchmarkID)
	benchmarkID := resp.BenchmarkID

	t.Run("RulesStats", func(t *testing.T) {
		stats, err := cb.ListBenchmarkRulesStats(ctx, benchmarkID, "", "")
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("ListBenchmarkRulesStats failed: %v", err)
		}
		t.Logf("Found %d rule stats", len(stats))
		for _, s := range stats {
			t.Logf("  %s: passed=%d failed=%d unknown=%d (%.1f%%)", s.RuleTitle, s.Passed, s.Failed, s.Unknown, s.PassPercentage)
		}
	})

	t.Run("RuleDevices", func(t *testing.T) {
		stats, err := cb.ListBenchmarkRulesStats(ctx, benchmarkID, "", "")
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("ListBenchmarkRulesStats failed: %v", err)
		}
		if len(stats) == 0 {
			t.Skip("No rule stats — cannot query devices")
		}
		devices, err := cb.ListBenchmarkRuleDevices(ctx, benchmarkID, stats[0].RuleID, "", "", "")
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("ListBenchmarkRuleDevices failed: %v", err)
		}
		t.Logf("Found %d devices for rule %s", len(devices), stats[0].RuleTitle)
	})

	t.Run("CompliancePercentage", func(t *testing.T) {
		pct, err := cb.GetBenchmarkCompliancePercentage(ctx, benchmarkID)
		if err != nil {
			skipOnServerError(t, err)
			t.Fatalf("GetBenchmarkCompliancePercentage failed: %v", err)
		}
		t.Logf("Compliance percentage: %.1f%%", pct.CompliancePercentage)
	})
}
