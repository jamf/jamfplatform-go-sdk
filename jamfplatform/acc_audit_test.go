// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/audit"
)

// The Audit API is read-only — all four operations are GETs, so there is nothing
// here to mutate.
//
// **Audit became reachable on 2026-09-03.** For the whole life of this file it
// was not: four separate credentials resolved no request context or lacked
// audit:read, and the tests here pinned that limitation and were written to fail
// the day it lifted. They did exactly that, and this is the replacement the old
// comment asked for — the capability asserted at least as precisely as the block
// was.
//
// What works, wire-verified: environment scope, X-Environment-Id, a credential
// granted audit:read. ListAuditSources returns real sources (api-gateway,
// blueprints, ai-policy on the environment probed). The organization form is
// still refused — an organization credential sending no scope header answers
// 400 REQUEST_CONTEXT_NOT_PROVIDED — which is consistent with the gateway
// having removed organization scoping, and is why accEnvClient is the right
// factory here rather than accOrgClient.
//
// The Audit API is read-only: all four operations are GETs, so there is nothing
// here to mutate.

// auditWindowStart bounds every query here. `since` is required, and the cursor
// walker has no page cap by design, so the window is the only thing keeping a
// busy environment's walk finite.
const auditWindowStart = "2026-08-01T00:00:00Z"

// auditGrantMissing reports whether err is this credential lacking audit:read,
// which is a configuration fact about the credential rather than a defect.
//
// Kept as a skip rather than a failure because the grant is not universal: four
// credentials probed between 2026-08-29 and 2026-09-03 did not have it, and a
// caller cannot check its own grants — the token is opaque and the exchange
// returns an empty scope. Anything that is not a clean 403 fails, so a genuine
// regression cannot hide behind this.
func auditGrantMissing(t *testing.T, method string, err error) bool {
	t.Helper()
	if err == nil {
		return false
	}
	var apiErr *jamfplatform.APIResponseError
	if !errors.As(err, &apiErr) {
		t.Fatalf("%s: non-API error, the request did not reach the gateway: %v", method, err)
	}
	for _, d := range apiErr.Details() {
		if d.Code == "REQUEST_CONTEXT_NOT_PROVIDED" {
			t.Fatalf("%s: the gateway resolved no request context. That was audit's documented state until "+
				"2026-09-03 and is now a regression, not a limitation — re-read the header/scope notes in CLAUDE.md: %v", method, err)
		}
	}
	if apiErr.HasStatus(403) {
		t.Logf("%s: context resolved, authorization denied (403) — this credential lacks audit:read", method)
		return true
	}
	t.Fatalf("%s: unexpected failure, neither a missing grant nor success: %v", method, err)
	return false
}

// TestAcceptance_AuditReachableUnderEnvironmentScope replaces the old
// TestAcceptance_AuditBlockedByGatewayContextConfig, which failed on 2026-09-03
// because audit started answering.
//
// It asserts the capability and pins the query contract, which is the part that
// can regress quietly. ListAuditEvents has TWO independent required inputs and
// the generated signature expresses neither — every parameter is an optional
// string:
//
//	since alone                     -> 400 MISSING_REQUIRED_FILTER
//	a filter alone, no since         -> 400 MISSING_REQUIRED_PARAMETER, field since
//	since + one filter               -> 200
//
// So a caller who reads only the signature writes a call that cannot succeed.
// Asserting both refusals here is what makes that discoverable from the suite.
func TestAcceptance_AuditReachableUnderEnvironmentScope(t *testing.T) {
	a := audit.New(accEnvClient(t))
	ctx := context.Background()
	since := auditWindowStart

	sources, err := a.ListAuditSources(ctx, since)
	if err != nil {
		if auditGrantMissing(t, "ListAuditSources", err) {
			t.Skip("Skipping: this credential is not granted audit:read")
		}
		t.Fatalf("ListAuditSources: %v", err)
	}
	if len(sources) == 0 {
		t.Fatal("ListAuditSources returned no sources — audit is reachable but reports nothing, " +
			"so the events coverage below has nothing to filter on")
	}
	t.Logf("audit is reachable under environment scope: %d sources", len(sources))

	t.Run("since alone is refused", func(t *testing.T) {
		_, err := a.ListAuditEvents(ctx, since, "", "", nil, nil, nil)
		assertAuditError(t, "ListAuditEvents(since only)", err, 400, "MISSING_REQUIRED_FILTER")
	})

	t.Run("a filter without since is refused", func(t *testing.T) {
		_, err := a.ListAuditEvents(ctx, "", "", "", []string{sources[0].Source}, nil, nil)
		assertAuditError(t, "ListAuditEvents(filter only)", err, 400, "MISSING_REQUIRED_PARAMETER")
	})
}

// assertAuditError pins one refusal by status AND code. Status alone would pass
// for any 400, which is precisely what made the original ListAuditEvents call
// look like a working query.
func assertAuditError(t *testing.T, op string, err error, status int, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s succeeded — the required-parameter contract has changed, so update this test and "+
			"the note on ListAuditEvents", op)
	}
	apiErr := jamfplatform.AsAPIError(err)
	if apiErr == nil {
		t.Fatalf("%s: non-API error: %v", op, err)
	}
	if !apiErr.HasStatus(status) {
		t.Fatalf("%s: want %d, got %d: %s", op, status, apiErr.StatusCode, apiErr.Summary())
	}
	for _, d := range apiErr.Details() {
		if d.Code == code {
			t.Logf("%s: %d %s, as documented", op, status, code)
			return
		}
	}
	t.Fatalf("%s: got %d but not %s: %s", op, status, code, apiErr.Summary())
}

// TestAcceptance_AuditReads is the real coverage, held behind the block above. It
// exercises every operation and reports what each one says, so that the moment
// audit is reachable the suite already covers it — including the cursor-paginated
// walk, which is the part most likely to be wrong on first contact with real data.
func TestAcceptance_AuditReads(t *testing.T) {
	a := audit.New(accEnvClient(t))
	ctx := context.Background()

	since := auditWindowStart

	sources, err := a.ListAuditSources(ctx, since)
	if err != nil {
		if auditGrantMissing(t, "ListAuditSources", err) {
			t.Skip("Skipping: this credential is not granted audit:read")
		}
		t.Fatalf("ListAuditSources: %v", err)
	}
	if len(sources) == 0 {
		t.Skip("no audit sources on this environment, so there is no required filter value to query with")
	}
	t.Logf("%d audit sources", len(sources))
	for _, s := range sources {
		t.Logf("  %+v", s)
	}

	// The filter is not optional despite the signature saying so — see
	// TestAcceptance_AuditReachableUnderEnvironmentScope. Driving it from the
	// discovered sources rather than a hardcoded name keeps this working on any
	// environment, and means the events returned are guaranteed to have a
	// producer.
	//
	// ListAuditEvents walks nextCursor to exhaustion. On a busy environment that
	// is a lot of pages, so the window plus the single-source filter is what
	// bounds it — not a page cap, which the cursor walker deliberately lacks.
	source := sources[0].Source
	events, err := a.ListAuditEvents(ctx, since, "", "", []string{source}, nil, nil)
	if err != nil {
		if auditGrantMissing(t, "ListAuditEvents", err) {
			t.Skip("Skipping: this credential is not granted audit:read")
		}
		t.Fatalf("ListAuditEvents(source=%s): %v", source, err)
	}
	t.Logf("%d audit events from source %q since %s (cursor-paginated walk)", len(events), source, since)

	if len(events) == 0 {
		t.Log("no events in the window — nothing to drive the lineage and transaction probes from")
		return
	}

	// AuditEnvelope is a merged structural union: a gateway event carries Actor
	// and RequestContext, a service event carries Data, and the two never mix.
	// Asserting that is the only check that proves mergeOneOfVariants produced a
	// type that matches the wire rather than merely compiling.
	var gateway, service int
	for _, e := range events {
		isGateway := e.Actor != nil || e.RequestContext != nil
		isService := e.Data != nil
		if isGateway && isService {
			t.Errorf("event %s carries both actor/requestContext and data — the two variants are documented never to mix", e.AuditID)
		}
		switch {
		case isGateway:
			gateway++
		case isService:
			service++
		}
		if e.AuditID == "" || e.TxID == "" || e.AuditSource == "" {
			t.Errorf("event has an empty required base field: %+v", e)
		}
	}
	t.Logf("  %d gateway events, %d service events, %d neither", gateway, service, len(events)-gateway-service)

	first := events[0]
	timeline, err := a.GetTransactionTimeline(ctx, first.TxID)
	if err != nil {
		t.Errorf("GetTransactionTimeline(%s): %v", first.TxID, err)
	} else {
		t.Logf("transaction %s spans %d events", first.TxID, len(timeline))
	}

	// Scan for a resourceId rather than trusting events[0] to have one. It
	// generally does not: the api-gateway source emits http.request events, which
	// carry no resource, so a test that only looked at the first event left
	// GetResourceLineage permanently uncovered on the environment probed
	// 2026-09-03. Service sources (blueprints, ai-policy) are where resources
	// appear.
	var resourceID string
	for _, e := range events {
		if e.ResourceID != nil && *e.ResourceID != "" {
			resourceID = *e.ResourceID
			break
		}
	}
	if resourceID == "" {
		// Fall back to a source that emits resource-bearing events, so the
		// operation is covered even when the first source is the gateway.
		for _, src := range sources {
			if src.Source == source {
				continue
			}
			more, err := a.ListAuditEvents(ctx, since, "", "", []string{src.Source}, nil, nil)
			if err != nil {
				t.Logf("ListAuditEvents(source=%s): %v", src.Source, err)
				continue
			}
			for _, e := range more {
				if e.ResourceID != nil && *e.ResourceID != "" {
					resourceID = *e.ResourceID
					t.Logf("took a resourceId from source %q, since %q emits none", src.Source, source)
					break
				}
			}
			if resourceID != "" {
				break
			}
		}
	}
	if resourceID == "" {
		t.Log("no event in any source carries a resourceId, so GetResourceLineage is uncovered on this data")
		return
	}
	lineage, err := a.GetResourceLineage(ctx, resourceID, "", since, "")
	if err != nil {
		t.Errorf("GetResourceLineage(%s): %v", resourceID, err)
	} else {
		t.Logf("resource %s has %d transactions in its lineage", resourceID, len(lineage))
	}
}
