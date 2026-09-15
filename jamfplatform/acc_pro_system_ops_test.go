// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jamf/jamfplatform-go-sdk/jamfplatform"
	"github.com/jamf/jamfplatform-go-sdk/jamfplatform/pro"
)

// System-level ops that are tenant-wide rather than per-resource:
// log-flushing config + task probes, scheduler job / summary reads,
// SLASA (software-license agreement) read. Slasa accept is read-only
// — POST /v1/slasa is a tenant-wide EULA click that shouldn't auto-
// trigger from a smoke test.

// --- log-flushing ----------------------------------------------------

func TestAcceptance_Pro_LogFlushingV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	cfg, err := p.GetLogFlushingV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetLogFlushingV1: %v", err)
	}
	t.Logf("Log-flushing: hourOfDay=%d policies=%d", cfg.HourOfDay, len(cfg.RetentionPolicies))

	tasks, err := p.ListLogFlushingTasksV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListLogFlushingTasksV1: %v", err)
	}
	t.Logf("Log-flushing tasks: %d existing", len(tasks))
}

// Creating a flush task against a live tenant prunes log data, so
// attempt only with a clearly-invalid qualifier and tolerate 4xx.
func TestAcceptance_Pro_LogFlushingTaskProbeV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	_, err := p.CreateLogFlushingTaskV1(ctx, &pro.LogFlushingTaskV1{
		Qualifier:           "sdk-acc-nonexistent-qualifier",
		RetentionPeriod:     1,
		RetentionPeriodUnit: "DAYS",
	})
	if err == nil {
		t.Log("CreateLogFlushingTaskV1 unexpectedly succeeded — qualifier may have matched a real log stream")
		return
	}
	var apiErr *jamfplatform.APIResponseError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		t.Logf("CreateLogFlushingTaskV1 rejected: status=%d — expected for invalid qualifier", apiErr.StatusCode)
		return
	}
	skipOnServerError(t, err)
	t.Fatalf("CreateLogFlushingTaskV1: %v", err)
}

// --- scheduler -------------------------------------------------------

func TestAcceptance_Pro_SchedulerV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := pro.New(c)

	summary, err := p.GetSchedulerSummaryV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSchedulerSummaryV1: %v", err)
	}
	t.Logf("Scheduler summary: %+v", summary)

	if _, err := p.GetSchedulerJobsV1(ctx); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSchedulerJobsV1: %v", err)
	}
	t.Logf("Scheduler jobs retrieved")

	// Triggers lookup needs a real jobKey; probe with a bogus one.
	if _, err := p.GetSchedulerJobTriggersV1(ctx, "sdk-acc-fake-job", nil, ""); err != nil {
		var apiErr *jamfplatform.APIResponseError
		if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
			t.Logf("GetSchedulerJobTriggersV1 probe: status=%d — expected for bogus jobKey", apiErr.StatusCode)
		} else {
			skipOnServerError(t, err)
			t.Fatalf("GetSchedulerJobTriggersV1: %v", err)
		}
	}
}

// --- slasa -----------------------------------------------------------

// POST /v1/slasa accepts the Software License + Service Agreement for
// the whole tenant — not something a smoke test should trigger. Only
// exercise the read.
func TestAcceptance_Pro_SlasaReadV1(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()

	slasa, err := pro.New(c).GetSlasaAcceptanceV1(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetSlasaAcceptanceV1: %v", err)
	}
	t.Logf("SLASA acceptance: %+v", slasa)
}
