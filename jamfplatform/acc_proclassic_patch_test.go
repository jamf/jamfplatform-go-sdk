// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

//go:build acceptance

package jamfplatform_test

// The Classic patch-management family, restored at v2082.
//
// v1942 deleted every operation on /patches, /patchpolicies (collection),
// /patchreports and four of the five /patchsoftwaretitles verbs from the
// published spec; the config took 31 of the 32 withdrawals at v1993 and held
// only POST /patchsoftwaretitles/id/{id}, because nothing else can mint a
// softwareTitleId for the Pro v3 configuration endpoints. v2082 published the
// whole family again, on upstream's stated grounds: "Patch management is where Classic
// API callers are most concentrated; that migration gets driven on its own
// schedule"), so the hold is gone and these fourteen operations are back.
//
// This file is new rather than recovered. Six of the fourteen never had an
// acceptance test even before the withdrawal — GetPatchByID, UpdatePatchByID,
// GetPatchSoftwareTitleByID, UpdatePatchSoftwareTitleByID,
// GetPatchReportByPatchSoftwareTitleID and
// ListPatchPoliciesBySoftwareTitleConfigID — and the old coverage for the
// other eight was a bare "did it 200" per call, with
// ListPatchPoliciesBySoftwareTitleConfigID carrying a comment that it "needs a
// patch software title config id fixture; skip". It does not: the fixture is
// mintable, seedPatchSoftwareTitleFixture mints it, so nothing here skips for
// want of one.
//
// Writing it against the wire rather than the spec found two operations that
// have been decoding into zero-valued structs since the day they shipped.
// Both are corrected in config.json and asserted below:
//
//   - GET /patchpolicies/softwaretitleconfig/id/{id} answers
//     <patch_policies><size>N</size>…, a collection, against a spec declaring
//     the singular patch_policy. responseType is now patch_policies.
//   - GET /patches/id/{id}/version/{version} answers <software_title> — the
//     title filtered to one version — and the spec declares no schema at all,
//     so the old "computers" override was the SDK's own guess and it was
//     wrong. responseType is now software_title, and the method is renamed
//     GetPatchByIDVersion because a name promising computers over a
//     SoftwareTitle return is worse than a rename.
//
// Neither could fail loudly: the generated types leave XMLName untagged, so a
// mismatched root element decodes to a zero struct and the call reports
// success.

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/proclassic"
)

// --- enumerations -------------------------------------------------------

// TestAcceptance_Classic_ListPatchSoftwareTitles asserts the enumeration
// reflects a write, not merely that it answers. The enumeration is what
// v1942 took away and its absence is what made the whole family unseedable,
// so "it lists the title we just created" is the property worth pinning.
func TestAcceptance_Classic_ListPatchSoftwareTitles(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	id := seedPatchSoftwareTitleFixture(t)
	cleanupDelete(t, "DeletePatchSoftwareTitleByID "+id, func() error {
		return p.DeletePatchSoftwareTitleByID(ctx, id)
	})

	settleUntilFound(t, "GetPatchSoftwareTitleByID "+id, func() error {
		_, err := p.GetPatchSoftwareTitleByID(ctx, id)
		return err
	})

	titles, err := p.ListPatchSoftwareTitles(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListPatchSoftwareTitles: %v", err)
	}
	var found bool
	for _, ti := range titles.PatchSoftwareTitles {
		if ti.ID != nil && strconv.Itoa(*ti.ID) == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListPatchSoftwareTitles: seeded title %s absent from %d entries", id, len(titles.PatchSoftwareTitles))
	}
	t.Logf("ListPatchSoftwareTitles: %d titles, seeded %s present", len(titles.PatchSoftwareTitles), id)
}

// TestAcceptance_Classic_ListPatches covers the /patches enumeration and the
// per-title read chain hanging off it. /patches and /patchsoftwaretitles
// describe the same objects under different root elements — the ids match —
// which is why the seeded title is reachable through both.
func TestAcceptance_Classic_ListPatches(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	id := seedPatchSoftwareTitleFixture(t)
	cleanupDelete(t, "DeletePatchSoftwareTitleByID "+id, func() error {
		return p.DeletePatchSoftwareTitleByID(ctx, id)
	})

	settleUntilFound(t, "GetPatchByID "+id, func() error {
		_, err := p.GetPatchByID(ctx, id)
		return err
	})

	titles, err := p.ListPatches(ctx)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListPatches: %v", err)
	}
	var found bool
	for _, ti := range titles.PatchManagementSoftwareTitles {
		if ti.ID != nil && strconv.Itoa(*ti.ID) == id {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListPatches: seeded title %s absent from %d entries", id, len(titles.PatchManagementSoftwareTitles))
	}

	// GetPatchByID carries the version catalogue /patchsoftwaretitles does
	// not, and it is the only source of a software_version for the two
	// version-scoped reads below.
	full, err := p.GetPatchByID(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetPatchByID(%s): %v", id, err)
	}
	if full.Name == nil {
		t.Fatalf("GetPatchByID(%s): decoded a nameless title — check the response root element", id)
	}
	t.Logf("GetPatchByID(%s): %q, totalVersions=%s", id, *full.Name, intPtrStr(full.TotalVersions))

	version := firstPatchVersion(t, full)

	// GetPatchByIDVersion answers <software_title>, not <computers>. Assert a
	// title field so the old "computers" typing cannot come back unnoticed:
	// with it, this decoded to a zero struct and still reported success.
	oneVersion, err := p.GetPatchByIDVersion(ctx, id, version)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetPatchByIDVersion(%s, %s): %v", id, version, err)
	}
	if oneVersion.Name == nil || oneVersion.ID == nil {
		t.Fatalf("GetPatchByIDVersion(%s, %s) decoded to a zero SoftwareTitle — response root is not software_title", id, version)
	}
	if strconv.Itoa(*oneVersion.ID) != id {
		t.Errorf("GetPatchByIDVersion(%s, %s): got id %d", id, version, *oneVersion.ID)
	}
}

// TestAcceptance_Classic_ListPatchPolicies covers the collection enumeration.
// It cannot assert on the policies themselves, because a patch policy needs a
// package and a scope this suite has no business creating on a shared tenant,
// so the tenant may legitimately have none.
//
// What it can assert is that the collection root decoded, and it must: the
// generated Classic types leave XMLName untagged, so a response whose root is
// not <patch_policies> decodes into a zero-valued PatchPolicies and the call
// reports success — which reads here as "0 policies", indistinguishable from a
// tenant with no patch policies. <size> is the tell, the same one
// TestAcceptance_Classic_ListPatchPoliciesBySoftwareTitleConfigID uses to pin
// its corrected response type. A failure means the response root changed (or
// the responseType override did), not that the tenant is empty.
func TestAcceptance_Classic_ListPatchPolicies(t *testing.T) {
	c := accClient(t)

	policies, err := proclassic.New(c).ListPatchPolicies(context.Background())
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListPatchPolicies: %v", err)
	}
	if policies.Size == nil {
		t.Fatal("ListPatchPolicies: no <size> decoded — response root is not patch_policies")
	}
	t.Logf("ListPatchPolicies: %d policies, size=%s", len(policies.PatchPolicies), sizePtrStr(policies.Size))
}

// TestAcceptance_Classic_ListPatchPoliciesBySoftwareTitleConfigID pins the
// corrected response type. The spec declares the singular patch_policy; the
// wire sends <patch_policies><size>0</size></patch_policies> for a title with
// no policies (verified 2026-09-04). A title minted by the fixture has none,
// so the empty collection is the expected result and an error is a real
// failure — this must not be an "any 4xx is fine" probe.
func TestAcceptance_Classic_ListPatchPoliciesBySoftwareTitleConfigID(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	id := seedPatchSoftwareTitleFixture(t)
	cleanupDelete(t, "DeletePatchSoftwareTitleByID "+id, func() error {
		return p.DeletePatchSoftwareTitleByID(ctx, id)
	})

	settleUntilFound(t, "GetPatchSoftwareTitleByID "+id, func() error {
		_, err := p.GetPatchSoftwareTitleByID(ctx, id)
		return err
	})

	policies, err := p.ListPatchPoliciesBySoftwareTitleConfigID(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("ListPatchPoliciesBySoftwareTitleConfigID(%s): %v", id, err)
	}
	// Size is the tell that the collection root decoded. Before the fix this
	// call returned a *PatchPolicy and every field came back nil.
	if policies.Size == nil {
		t.Fatalf("ListPatchPoliciesBySoftwareTitleConfigID(%s): no <size> decoded — response root is not patch_policies", id)
	}
	if len(policies.PatchPolicies) != 0 {
		t.Logf("ListPatchPoliciesBySoftwareTitleConfigID(%s): %d policies on a freshly seeded title", id, len(policies.PatchPolicies))
	}
	t.Logf("ListPatchPoliciesBySoftwareTitleConfigID(%s): size=%s", id, sizePtrStr(policies.Size))
}

// --- patch software title lifecycle -------------------------------------

// TestAcceptance_Classic_PatchSoftwareTitleLifecycle round-trips the four
// /patchsoftwaretitles/id/{id} verbs against a test-owned title: create (via
// the fixture), read, update, read back the update, delete, and 404 after
// delete. Only POST survived v1993, so this is the first time the other three
// have been exercised at all.
func TestAcceptance_Classic_PatchSoftwareTitleLifecycle(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	id := seedPatchSoftwareTitleFixture(t)
	deleted := false
	cleanupDelete(t, "DeletePatchSoftwareTitleByID "+id, func() error {
		if deleted {
			return nil
		}
		return p.DeletePatchSoftwareTitleByID(ctx, id)
	})

	settleUntilFound(t, "GetPatchSoftwareTitleByID "+id, func() error {
		_, err := p.GetPatchSoftwareTitleByID(ctx, id)
		return err
	})

	got, err := p.GetPatchSoftwareTitleByID(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetPatchSoftwareTitleByID(%s): %v", id, err)
	}
	if got.ID == nil || strconv.Itoa(*got.ID) != id {
		t.Fatalf("GetPatchSoftwareTitleByID(%s): got id %v", id, got.ID)
	}
	if got.NameID == nil {
		t.Errorf("GetPatchSoftwareTitleByID(%s): no name_id — the fixture set one", id)
	}

	// The name is the only field safe to change: source_id and name_id
	// address the upstream catalogue entry, and versions are server-owned.
	// Notifications round-trip, so flip one and read it back.
	want := got.Notifications == nil || got.Notifications.EmailNotification == nil || !*got.Notifications.EmailNotification
	if err := p.UpdatePatchSoftwareTitleByID(ctx, id, &proclassic.PatchSoftwareTitle{
		Name:   got.Name,
		NameID: got.NameID,
		Notifications: &proclassic.PatchSoftwareTitleNotifications{
			EmailNotification: &want,
		},
	}); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("UpdatePatchSoftwareTitleByID(%s): %v", id, err)
	}

	after, err := p.GetPatchSoftwareTitleByID(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetPatchSoftwareTitleByID(%s) after update: %v", id, err)
	}
	if after.Notifications == nil || after.Notifications.EmailNotification == nil {
		t.Errorf("UpdatePatchSoftwareTitleByID(%s): notifications absent after update", id)
	} else if *after.Notifications.EmailNotification != want {
		t.Errorf("UpdatePatchSoftwareTitleByID(%s): emailNotification is %v, wrote %v",
			id, *after.Notifications.EmailNotification, want)
	}

	if err := p.DeletePatchSoftwareTitleByID(ctx, id); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeletePatchSoftwareTitleByID(%s): %v", id, err)
	}
	deleted = true

	settleUntilGone(t, "GetPatchSoftwareTitleByID "+id+" after delete", func() error {
		_, err := p.GetPatchSoftwareTitleByID(ctx, id)
		return err
	})
}

// --- patch reports ------------------------------------------------------

// TestAcceptance_Classic_PatchReports covers both /patchreports forms against
// a test-owned title. The version-scoped form narrows total_versions to 1,
// which is the assertion that proves the version path segment is honoured
// rather than ignored.
func TestAcceptance_Classic_PatchReports(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	id := seedPatchSoftwareTitleFixture(t)
	cleanupDelete(t, "DeletePatchSoftwareTitleByID "+id, func() error {
		return p.DeletePatchSoftwareTitleByID(ctx, id)
	})

	settleUntilFound(t, "GetPatchReportByPatchSoftwareTitleID "+id, func() error {
		_, err := p.GetPatchReportByPatchSoftwareTitleID(ctx, id)
		return err
	})

	report, err := p.GetPatchReportByPatchSoftwareTitleID(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetPatchReportByPatchSoftwareTitleID(%s): %v", id, err)
	}
	if report.PatchSoftwareTitleID == nil || *report.PatchSoftwareTitleID != id {
		t.Fatalf("GetPatchReportByPatchSoftwareTitleID(%s): report names title %v", id, report.PatchSoftwareTitleID)
	}
	if report.TotalVersions == nil || *report.TotalVersions == 0 {
		t.Skipf("patch report for title %s carries no versions — nothing to narrow", id)
	}
	total := *report.TotalVersions
	t.Logf("GetPatchReportByPatchSoftwareTitleID(%s): totalVersions=%d totalComputers=%s",
		id, total, intPtrStr(report.TotalComputers))

	version := firstPatchReportVersion(t, report)

	narrowed, err := p.GetPatchReportByTitleIDVersion(ctx, id, version)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetPatchReportByTitleIDVersion(%s, %s): %v", id, version, err)
	}
	if narrowed.PatchSoftwareTitleID == nil || *narrowed.PatchSoftwareTitleID != id {
		t.Fatalf("GetPatchReportByTitleIDVersion(%s, %s): report names title %v", id, version, narrowed.PatchSoftwareTitleID)
	}
	if narrowed.TotalVersions == nil {
		t.Fatalf("GetPatchReportByTitleIDVersion(%s, %s): no total_versions decoded", id, version)
	}
	if *narrowed.TotalVersions != 1 {
		t.Errorf("GetPatchReportByTitleIDVersion(%s, %s): totalVersions=%d, want 1 — the version segment was ignored",
			id, version, *narrowed.TotalVersions)
	}
}

// --- /patches writes ----------------------------------------------------

// TestAcceptance_Classic_PatchByIDWrites covers the three write verbs on
// /patches/id/{id}, two of which do not work.
//
// Wire-established 2026-09-04 on Jamf Pro 11.31.1, with GET on the same path
// answering 200 in the same invocation: POST and PUT are refused with Jamf
// Pro's own HTML 400 "Error in XML file" whatever body they are given — the
// spec's software_title root, the Go type's own SoftwareTitle root, and a
// minimal one-field body all get it, and POST adds "Possible mismatch between
// resource specified in the URL and XML file". So this is not a marshalling
// problem the SDK can fix from its side, and it is not the body shape: the
// endpoints are refused. DELETE works.
//
// That leaves /patches as a read-and-delete surface whose spec declares four
// verbs, which is worth reporting upstream. Both refusals are asserted rather
// than tolerated, and both fail the day they start working — at which point
// this becomes a real create/update round-trip and the assertions flip.
//
// The old TestAcceptance_Classic_ProbeCreate_CreatePatchByID is deliberately
// not restored: probeCreateHandleErr treats any APIResponseError as "rejected
// as expected", which cannot tell this 400 from a 403 or a 422 and so records
// nothing about what the server actually does.
func TestAcceptance_Classic_PatchByIDWrites(t *testing.T) {
	c := accClient(t)
	ctx := context.Background()
	p := proclassic.New(c)

	id := seedPatchSoftwareTitleFixture(t)
	deleted := false
	cleanupDelete(t, "DeletePatchByID "+id, func() error {
		if deleted {
			return nil
		}
		return p.DeletePatchByID(ctx, id)
	})

	settleUntilFound(t, "GetPatchByID "+id, func() error {
		_, err := p.GetPatchByID(ctx, id)
		return err
	})

	before, err := p.GetPatchByID(ctx, id)
	if err != nil {
		skipOnServerError(t, err)
		t.Fatalf("GetPatchByID(%s): %v", id, err)
	}

	// PUT. SoftwareTitle's notifications are jss_notification /
	// email_notification — a different pair from PatchSoftwareTitle's, on the
	// same object — so this body is the one the spec asks for.
	flip := before.Notifications == nil || before.Notifications.EmailNotification == nil || !*before.Notifications.EmailNotification
	err = p.UpdatePatchByID(ctx, id, &proclassic.SoftwareTitle{
		Name: before.Name,
		Notifications: &proclassic.SoftwareTitleNotifications{
			EmailNotification: &flip,
		},
	})
	assertPatchesWriteRefused(t, "UpdatePatchByID", err)

	// POST, against the id-0 create form the rest of the Classic surface uses.
	//
	// The response is captured rather than discarded so the create can be
	// cleaned up on the day it starts working. Today it cannot: the POST is
	// refused, nothing is created, and this cleanup is dormant — the guard
	// makes it a no-op. But the assertion below fails by design the moment
	// the endpoint is fixed, and a failing test that leaves an unowned patch
	// software title behind on a shared tenant is worse than a failing test.
	// /patches and /patchsoftwaretitles describe the same objects with
	// matching ids, so DeletePatchByID removes what this POST would create.
	created, err := p.CreatePatchByID(ctx, "0", &proclassic.SoftwareTitle{Name: before.Name})
	if created != nil && created.ID != nil {
		createdID := strconv.Itoa(*created.ID)
		cleanupDelete(t, "DeletePatchByID "+createdID+" (CreatePatchByID)", func() error {
			return p.DeletePatchByID(ctx, createdID)
		})
	}
	assertPatchesWriteRefused(t, "CreatePatchByID", err)

	// DELETE does work, and it removes the same object /patchsoftwaretitles
	// created — absence from a later read is the assertion.
	if err := p.DeletePatchByID(ctx, id); err != nil {
		skipOnServerError(t, err)
		t.Fatalf("DeletePatchByID(%s): %v", id, err)
	}
	deleted = true

	settleUntilGone(t, "GetPatchByID "+id+" after delete", func() error {
		_, err := p.GetPatchByID(ctx, id)
		return err
	})
}

// assertPatchesWriteRefused requires a /patches write to be refused with the
// 400 recorded above. A success means the server was fixed: say so loudly,
// because the fix turns this test from a limitation into coverage that has to
// be written. Any other status — 5xx included — is an unexpected change and
// equally worth failing on.
func assertPatchesWriteRefused(t *testing.T, op string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: succeeded. The /patches write surface has been fixed — replace this assertion with a real round-trip and drop the limitation note in this file's header", op)
	}
	// Deliberately no skipOnServerError here, unlike almost every other call
	// in this file. That helper skips unconditionally on any status >= 500,
	// which is the right convention for a transient fault and precisely wrong
	// for a permanent refusal: the 400 asserted below is wire-established as
	// unconditional (2026-09-04, three body shapes tried, GET on the same
	// path at 200 in the same invocation), so a 5xx here is not
	// infrastructure having a bad afternoon — it is the refusal changing
	// shape, which is exactly what this helper exists to report. Skipping on
	// it would leave the test unable ever to report the change, the same trap
	// TestAcceptance_Classic_PatchByName was corrected for, and a 5xx is
	// plausible on this family: GET /patches/name/{name} answers 500 for
	// every name today.
	apiErr := jamfplatform.AsAPIError(err)
	if apiErr == nil {
		t.Fatalf("%s: non-API error: %v", op, err)
	}
	if !apiErr.HasStatus(400) {
		t.Fatalf("%s: want the recorded 400 refusal, got %d — the refusal has changed shape, which is a change in the endpoint rather than a transient fault: %s", op, apiErr.StatusCode, apiErr.Summary())
	}
	t.Logf("%s: 400 as recorded — /patches writes are refused whatever the body (%s)", op, apiErr.Summary())
}

// --- helpers ------------------------------------------------------------

// firstPatchVersion returns a software_version from a SoftwareTitle, failing
// the test when the catalogue is empty: a title minted from a real patch
// source always carries versions, so an empty one is a defect rather than a
// tenant that cannot supply a fixture.
func firstPatchVersion(t *testing.T, title *proclassic.SoftwareTitle) string {
	t.Helper()
	if title.Versions == nil || title.Versions.Version == nil || len(*title.Versions.Version) == 0 {
		t.Fatal("software title carries no versions — cannot exercise the version-scoped reads")
	}
	for _, v := range *title.Versions.Version {
		if v.SoftwareVersion != nil && *v.SoftwareVersion != "" {
			return *v.SoftwareVersion
		}
	}
	t.Fatal("every version entry has an empty software_version")
	return ""
}

// firstPatchReportVersion is firstPatchVersion for the report shape, which
// declares its own version item type.
func firstPatchReportVersion(t *testing.T, report *proclassic.PatchReport) string {
	t.Helper()
	if report.Versions == nil || report.Versions.Version == nil || len(*report.Versions.Version) == 0 {
		t.Fatal("patch report carries no versions — cannot exercise the version-scoped read")
	}
	for _, v := range *report.Versions.Version {
		if v.SoftwareVersion != nil && *v.SoftwareVersion != "" {
			return *v.SoftwareVersion
		}
	}
	t.Fatal("every report version entry has an empty software_version")
	return ""
}

// intPtrStr renders an optional int for a log line. Printing the pointer with
// %v prints an address, which is worse than useless in a test log.
func intPtrStr(v *int) string {
	if v == nil {
		return "<nil>"
	}
	return strconv.Itoa(*v)
}

// sizePtrStr is intPtrStr for the Classic Size wrapper, which carries the
// count as its chardata.
func sizePtrStr(v *proclassic.Size) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%+v", *v)
}
