// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// buildArchive writes a zip whose members are the given name -> content pairs
// and opens it as the command would.
func buildArchive(t *testing.T, members map[string]string) *archive {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bundle.zip")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	a, err := openArchive(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.close)
	return a
}

// specDoc is the minimum a member needs for summarize and the title check.
func specDoc(title string, paths ...string) string {
	var b strings.Builder
	b.WriteString("openapi: 3.0.1\ninfo:\n  title: " + title + "\n  version: 1.0.0\npaths:\n")
	for _, p := range paths {
		b.WriteString("  " + p + ":\n    get:\n      operationId: x\n")
	}
	return b.String()
}

// withHeldRow swaps the provenance table for a copy in which one row is held,
// and returns that row's destination.
//
// Every hold is temporary by construction, so no spec being held is the normal
// resting state — it is what the table looks like the moment the last hold
// lifts. The tests below have to exercise the held path anyway, and keying them
// off whichever real row happens to carry heldAt makes them stop testing it
// silently on exactly the build that most needs the machinery to still work.
// Both v1865 account holds lifting at v2176 is what turned that into three
// failures rather than three quiet no-ops.
func withHeldRow(t *testing.T) string {
	t.Helper()
	if len(specs) < 2 {
		t.Fatalf("provenance table carries %d rows; need at least 2 to hold one", len(specs))
	}
	original := specs
	t.Cleanup(func() { specs = original })
	swapped := make([]specRow, len(original))
	copy(swapped, original)
	swapped[0].heldAt = "v1"
	swapped[0].why = "synthetic hold, so the held path stays covered when no spec is really held."
	specs = swapped
	return swapped[0].dest
}

// firstUnheldDest is the counterpart: a destination that is definitely
// selectable, named off the table rather than hardcoded, since which row sits
// where changes whenever a spec is added.
func firstUnheldDest(t *testing.T) string {
	t.Helper()
	for _, s := range specs {
		if s.heldAt == "" {
			return s.dest
		}
	}
	t.Fatal("every row in the provenance table is held")
	return ""
}

func TestOpenArchiveReadsBuildFromManifest(t *testing.T) {
	a := buildArchive(t, map[string]string{
		"MANIFEST.md": "# GitOps Platform API Archive - Build 2056\n\n**GitOps Build**: v2056\n",
	})
	if a.build != "v2056" {
		t.Fatalf("build = %q, want v2056", a.build)
	}
	if a.prefix != "" {
		t.Fatalf("prefix = %q, want empty", a.prefix)
	}
	if a.sha256 == "" {
		t.Fatal("sha256 not computed")
	}
}

// A wrapped archive is one whose members all sit under a single top-level
// directory. The bundles are unwrapped today, so this is only tolerance —
// but tolerance that silently failed would look like "no changes".
func TestOpenArchiveToleratesWrapperDirectory(t *testing.T) {
	a := buildArchive(t, map[string]string{
		"bundle-v9/MANIFEST.md":                    "**GitOps Build**: v9\n",
		"bundle-v9/external/jpapi/openapi.yaml":    specDoc("Jamf Pro API", "/a"),
		"bundle-v9/external/nonesuch/openapi.yaml": specDoc("Nonesuch API", "/b"),
	})
	if a.prefix != "bundle-v9/" {
		t.Fatalf("prefix = %q, want bundle-v9/", a.prefix)
	}
	if a.build != "v9" {
		t.Fatalf("build = %q, want v9", a.build)
	}
	if got := a.families("external"); len(got) != 2 || got[0] != "jpapi" || got[1] != "nonesuch" {
		t.Fatalf("families = %v", got)
	}
}

// The single easiest thing to miss in a build is a newly published API: it
// appears in no diff of the specs already carried.
func TestCheckUnmappedRejectsANewlyPublishedFamily(t *testing.T) {
	a := buildArchive(t, map[string]string{
		"MANIFEST.md":                         "**GitOps Build**: v1\n",
		"external/jpapi/openapi.yaml":         specDoc("Jamf Pro API", "/a"),
		"external/users/openapi.yaml":         specDoc("User Inventory API", "/b"),
		"external/brand-new-api/openapi.yaml": specDoc("Brand New API", "/c"),
		"external/_permissions/routes.yaml":   "routes: []\n",
		"external/unified-platform-api.yaml":  "openapi: 3.0.1\n",
		"internal/stage/jpapi-x/openapi.yaml": specDoc("Whatever", "/d"),
	})
	err := checkUnmapped(a, "external")
	if err == nil {
		t.Fatal("want an error for the unmapped family")
	}
	if !strings.Contains(err.Error(), "brand-new-api") {
		t.Fatalf("error does not name the family: %v", err)
	}
	// users is in knownUnmapped and a rollup at the tree root is not a family,
	// so neither may be reported.
	if strings.Contains(err.Error(), "users") || strings.Contains(err.Error(), "unified") {
		t.Fatalf("error names something it should tolerate: %v", err)
	}
}

func TestCheckUnmappedAcceptsAFullyAccountedArchive(t *testing.T) {
	members := map[string]string{"MANIFEST.md": "**GitOps Build**: v1\n"}
	for _, s := range specs {
		members["external/"+s.dir+"/openapi.yaml"] = specDoc(s.title, "/a")
	}
	members["external/users/openapi.yaml"] = specDoc("User Inventory API", "/a")
	if err := checkUnmapped(buildArchive(t, members), "external"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Directory names are not stable across builds, so a row that still resolves
// must additionally prove it points at the API it claims. The concrete trap:
// device-groups is the Platform API and securitycloud-devices is Security
// Cloud's, and copying one over the other silently replaces a spec.
func TestIngestRejectsAMisdirectedRow(t *testing.T) {
	members := map[string]string{"MANIFEST.md": "**GitOps Build**: v1\n"}
	for _, s := range specs {
		members["external/"+s.dir+"/openapi.yaml"] = specDoc(s.title, "/a")
	}
	members["external/securitycloud-devices/openapi.yaml"] = specDoc("Device Groups API", "/a")

	sel := map[string]bool{"securitycloud-device-groups-api.yaml": true}
	mf := &manifest{Entries: map[string]manifestEntry{}}
	_, _, err := ingest(buildArchive(t, members), "external", t.TempDir(), sel, mf)
	if err == nil {
		t.Fatal("want an error for the title mismatch")
	}
	for _, want := range []string{"Device Groups API", "Security Cloud Devices API", "securitycloud-device-groups-api.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error omits %q: %v", want, err)
		}
	}
}

// A held row that the archive predates is normal when reconstructing an older
// build; only a selected row has to resolve.
func TestIngestToleratesAnAbsentHeldFamilyButNotAnAbsentSelectedOne(t *testing.T) {
	held := withHeldRow(t)
	members := map[string]string{"MANIFEST.md": "**GitOps Build**: v1\n"}
	for _, s := range specs {
		if s.heldAt != "" {
			continue
		}
		members["external/"+s.dir+"/openapi.yaml"] = specDoc(s.title, "/a")
	}
	a := buildArchive(t, members)

	sel, err := selection("", false)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := ingest(a, "external", t.TempDir(), sel, &manifest{Entries: map[string]manifestEntry{}})
	if err != nil {
		t.Fatalf("unheld-only selection should succeed: %v", err)
	}
	var absent int
	for _, r := range rows {
		if r.status == statusHeld && strings.Contains(r.note, "absent from this archive") {
			absent++
		}
	}
	if absent == 0 {
		t.Fatal("no held row reported as absent")
	}

	_, _, err = ingest(a, "external", t.TempDir(), map[string]bool{held: true}, &manifest{Entries: map[string]manifestEntry{}})
	if err == nil {
		t.Fatalf("a selected family missing from the archive must fail (%s)", held)
	}
}

func TestSelection(t *testing.T) {
	withHeldRow(t)
	unheld, err := selection("", false)
	if err != nil {
		t.Fatal(err)
	}
	all, err := selection("", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(specs) {
		t.Fatalf("-include-held selected %d of %d", len(all), len(specs))
	}
	if len(unheld) >= len(all) {
		t.Fatal("the default selection must exclude the held specs")
	}
	for _, s := range specs {
		if s.heldAt != "" && unheld[s.dest] {
			t.Fatalf("%s is held but selected by default", s.dest)
		}
	}
	// Naming a spec explicitly is the act -include-held performs in bulk. Read
	// the destination off the table rather than hardcoding one, so adding or
	// reordering a spec cannot quietly turn this into a no-op.
	target := firstUnheldDest(t)
	one, err := selection(target, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || !one[target] {
		t.Fatalf("-only %s selected %v", target, one)
	}
	if _, err := selection("no-such-spec.yaml", false); err == nil {
		t.Fatal("-only with an unknown destination must fail")
	}
}

// -only selects a strict subset; every unheld spec it does not name must
// report as skipped, not held. A held row's heldAt and why are always
// non-empty; an unheld, unselected row has neither, so sharing statusHeld's
// rendering prints "held at  — " — a hold with a blank build and a blank
// reason, in the exact report whose job is naming real ones.
func TestOnlyReportsUnselectedUnheldSpecsAsSkipped(t *testing.T) {
	withHeldRow(t)
	members := map[string]string{"MANIFEST.md": "**GitOps Build**: v1\n"}
	for _, s := range specs {
		members["external/"+s.dir+"/openapi.yaml"] = specDoc(s.title, "/a")
	}
	a := buildArchive(t, members)

	var target string
	for _, s := range specs {
		if s.heldAt == "" {
			target = s.dest
			break
		}
	}
	sel, err := selection(target, false)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := ingest(a, "external", t.TempDir(), sel, &manifest{Entries: map[string]manifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	var skipped, held int
	for _, r := range rows {
		if strings.Contains(r.note, "held at  ") {
			t.Errorf("%s: note names an empty hold: %q", r.dest, r.note)
		}
		if r.dest == target {
			continue
		}
		switch r.status {
		case statusSkipped:
			skipped++
			if !strings.Contains(r.note, "not selected by -only") {
				t.Errorf("%s: skipped note = %q", r.dest, r.note)
			}
		case statusHeld:
			held++
			if r.note == "" {
				t.Errorf("%s: held row has no note", r.dest)
			}
		}
	}
	if skipped == 0 {
		t.Fatal("no unheld, unselected spec reported as skipped")
	}
	if held == 0 {
		t.Fatal("no genuinely held spec reported as held")
	}
}

// The same split applies when the unselected row is additionally absent from
// the archive: an unheld, unselected, absent family must report skipped, not
// a hold with a blank build and a blank reason.
func TestOnlySkipsAnAbsentUnheldUnselectedFamily(t *testing.T) {
	var target, missing string
	for _, s := range specs {
		if s.heldAt != "" {
			continue
		}
		if target == "" {
			target = s.dest
			continue
		}
		if missing == "" {
			missing = s.dest
			break
		}
	}
	if target == "" || missing == "" {
		t.Fatal("need at least two unheld specs to run this test")
	}
	members := map[string]string{"MANIFEST.md": "**GitOps Build**: v1\n"}
	for _, s := range specs {
		if s.dest == missing {
			continue // simulate an archive that predates this family
		}
		members["external/"+s.dir+"/openapi.yaml"] = specDoc(s.title, "/a")
	}
	a := buildArchive(t, members)

	sel, err := selection(target, false)
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := ingest(a, "external", t.TempDir(), sel, &manifest{Entries: map[string]manifestEntry{}})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range rows {
		if r.dest != missing {
			continue
		}
		found = true
		if r.status != statusSkipped {
			t.Errorf("status = %v, want skipped", r.status)
		}
		if strings.Contains(r.note, "held at") {
			t.Errorf("note names a hold that does not exist: %q", r.note)
		}
	}
	if !found {
		t.Fatal("missing row not present in output")
	}
}

// A token that trims to empty — "-only ','" or an unset shell variable pasted
// into a wrapper script — must be a hard error, not a selection of nothing
// that exits 0 having ingested nothing.
func TestSelectionRejectsATrimmedToEmptyOnly(t *testing.T) {
	for _, only := range []string{",", " , ", ",,", " "} {
		if _, err := selection(only, false); err == nil {
			t.Errorf("-only %q must fail, not resolve to an empty selection", only)
		}
	}
}

// An -only typo means opening main.go to find the right name unless the
// error itself lists the valid destinations.
func TestOnlyUnknownDestinationListsValidDestinations(t *testing.T) {
	_, err := selection("no-such-spec.yaml", false)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, s := range specs {
		if !strings.Contains(err.Error(), s.dest) {
			t.Errorf("error does not list %s as a valid destination: %v", s.dest, err)
		}
	}
}

// ingest must never write, even partway, when a later selected row refuses:
// not the rows that passed before it, and not the manifest. ingest computes
// pending writes and returns them; committing is the caller's job, done only
// once every row — spec and permission alike — has resolved cleanly. This
// pins that split so a refusal on row 16 of 19 cannot leave rows 1-15 on disk
// with a manifest that never rewrites to describe them.
func TestIngestRefusalWritesNothing(t *testing.T) {
	members := map[string]string{"MANIFEST.md": "**GitOps Build**: v1\n"}
	for _, s := range specs {
		members["external/"+s.dir+"/openapi.yaml"] = specDoc(s.title, "/a")
	}
	// A row well after the first several carries the wrong title.
	members["external/audit/openapi.yaml"] = specDoc("Wrong Title Entirely", "/a")
	a := buildArchive(t, members)

	sel, err := selection("", true) // include held so every row is selected
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	mf := &manifest{Entries: map[string]manifestEntry{}}
	if _, _, err := ingest(a, "external", dest, sel, mf); err == nil {
		t.Fatal("want an error for the title mismatch")
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("ingest wrote files despite a later refusal: %v", entries)
	}
	if len(mf.Entries) != 0 {
		t.Fatalf("ingest mutated the manifest despite a later refusal: %+v", mf.Entries)
	}
}

// The atomicity crosses both calls, not just one: ingest's pending writes for
// a clean spec pass must not be committed if ingestPermissions then fails to
// resolve. This mirrors what main() does — call both, and invoke commit only
// once both have returned clean.
func TestIngestPermissionsRefusalLeavesSpecWritesUncommitted(t *testing.T) {
	members := map[string]string{"MANIFEST.md": "**GitOps Build**: v1\n"}
	for _, s := range specs {
		members["external/"+s.dir+"/openapi.yaml"] = specDoc(s.title, "/a")
	}
	// scopes.yaml is deliberately omitted, so ingestPermissions fails.
	members["external/_permissions/routes.yaml"] = "routes: []\n"
	a := buildArchive(t, members)

	sel, err := selection("", false)
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	mf := &manifest{Entries: map[string]manifestEntry{}}
	_, specWrites, err := ingest(a, "external", dest, sel, mf)
	if err != nil {
		t.Fatalf("spec pass should succeed: %v", err)
	}
	if len(specWrites) == 0 {
		t.Fatal("expected pending spec writes")
	}
	if _, _, err := ingestPermissions(a, "external", dest, mf, true); err == nil {
		t.Fatal("want an error for the missing scopes.yaml")
	}
	// main() bails here without ever calling commit. Confirm the pending
	// spec writes alone changed nothing on disk.
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("spec writes must stay uncommitted when permissions fails to resolve: %v", entries)
	}
}

// ingestPermissions is the reordering note's only caller-facing home;
// TestSortedSHA256IsBlindToLineOrderOnly exercises the helper in isolation
// and cannot reach it. Disabling the sortedSHA256 comparison inside
// ingestPermissions must fail this test even though the helper itself still
// works correctly.
func TestIngestPermissionsReportsAPureReordering(t *testing.T) {
	routes := "a: 1\nb: 2\nc: 3\n"
	reordered := "c: 3\na: 1\nb: 2\n"
	scopes := "x: 1\n"
	members := map[string]string{
		"MANIFEST.md":                       "**GitOps Build**: v2\n",
		"external/_permissions/routes.yaml": reordered,
		"external/_permissions/scopes.yaml": scopes,
	}
	a := buildArchive(t, members)

	dest := t.TempDir()
	outDir := filepath.Join(dest, "_permissions")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "routes.yaml"), []byte(routes), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "scopes.yaml"), []byte(scopes), 0o644); err != nil {
		t.Fatal(err)
	}
	mf := &manifest{Entries: map[string]manifestEntry{
		"_permissions/routes.yaml": {Build: "v1", Source: "external/_permissions", SHA256: sha256Bytes([]byte(routes))},
		"_permissions/scopes.yaml": {Build: "v1", Source: "external/_permissions", SHA256: sha256Bytes([]byte(scopes))},
	}}

	rows, writes, err := ingestPermissions(a, "external", dest, mf, true)
	if err != nil {
		t.Fatal(err)
	}
	var foundRoutes bool
	for _, r := range rows {
		switch r.name {
		case "_permissions/routes.yaml":
			foundRoutes = true
			if r.status != statusUpdated {
				t.Errorf("routes.yaml status = %v, want updated", r.status)
			}
			if !strings.Contains(r.note, "PURE REORDERING") {
				t.Errorf("routes.yaml note = %q, want it to flag a pure reordering", r.note)
			}
		case "_permissions/scopes.yaml":
			if r.status != statusUnchanged {
				t.Errorf("scopes.yaml status = %v, want unchanged", r.status)
			}
		}
	}
	if !foundRoutes {
		t.Fatal("routes.yaml row not present")
	}

	// The write itself must actually land, and the manifest must move on.
	if err := commit(writes, mf); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(outDir, "routes.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != reordered {
		t.Fatalf("routes.yaml on disk = %q, want the archive's (reordered) content", got)
	}
	if mf.Entries["_permissions/routes.yaml"].Build != "v2" {
		t.Fatalf("manifest not updated for routes.yaml: %+v", mf.Entries["_permissions/routes.yaml"])
	}
}

// Neither ingest nor ingestPermissions ever touches disk: they only resolve,
// validate and hand back pending writes. commit is the sole place bytes land,
// and it is called (from main) only when -dry-run is not set. This exercises
// both directions over one temp dir: nothing written before commit runs,
// everything written and the manifest updated after, and a second pass over
// the same archive and manifest reads back unchanged.
func TestCommitIsTheOnlyThingThatWrites(t *testing.T) {
	members := map[string]string{"MANIFEST.md": "**GitOps Build**: v3\n"}
	for _, s := range specs {
		members["external/"+s.dir+"/openapi.yaml"] = specDoc(s.title, "/a")
	}
	members["external/_permissions/routes.yaml"] = "routes: []\n"
	members["external/_permissions/scopes.yaml"] = "scopes: []\n"
	a := buildArchive(t, members)

	sel, err := selection("", true)
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	mf := loadManifest(dest)

	rows, specWrites, err := ingest(a, "external", dest, sel, mf)
	if err != nil {
		t.Fatal(err)
	}
	_, permWrites, err := ingestPermissions(a, "external", dest, mf, true)
	if err != nil {
		t.Fatal(err)
	}

	// Before commit: resolved and reported, nothing on disk.
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Fatalf("resolving alone wrote %v", entries)
	}
	for _, r := range rows {
		if sel[r.dest] && r.status != statusNew {
			t.Errorf("%s: status = %v before any write, want new", r.dest, r.status)
		}
	}

	if err := commit(append(specWrites, permWrites...), mf); err != nil {
		t.Fatal(err)
	}
	if err := saveManifest(dest, mf); err != nil {
		t.Fatal(err)
	}
	for _, s := range specs {
		if !fileExists(filepath.Join(dest, s.dest)) {
			t.Errorf("%s not written after commit", s.dest)
		}
	}
	if !fileExists(filepath.Join(dest, "_permissions", "routes.yaml")) {
		t.Fatal("routes.yaml not written after commit")
	}
	if !fileExists(filepath.Join(dest, manifestName)) {
		t.Fatal("manifest not saved after commit")
	}

	// A second run reading the manifest just saved must report every
	// selected spec, and both permission files, unchanged.
	mf2 := loadManifest(dest)
	rows2, _, err := ingest(a, "external", dest, sel, mf2)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows2 {
		if !sel[r.dest] {
			continue
		}
		if r.status != statusUnchanged {
			t.Errorf("%s: second run status = %v, want unchanged", r.dest, r.status)
		}
		if !strings.HasPrefix(r.note, "since v3") {
			t.Errorf("%s: second run note = %q, want it prefixed \"since v3\"", r.dest, r.note)
		}
	}
	permRows2, _, err := ingestPermissions(a, "external", dest, mf2, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range permRows2 {
		if r.status != statusUnchanged {
			t.Errorf("%s: second run status = %v, want unchanged", r.name, r.status)
		}
	}
}

// A -only run must not rewrite _permissions. Those two files are not
// spec-scoped, so selecting one spec from an older bundle — the exact shape of
// restoring a held spec — would otherwise reset the whole privilege oracle to
// that bundle's build. It happened in the v2154 ingest: a `-only
// Classic-openapi.yaml` restore from the v2121 archive silently took
// routes.yaml back to v2121 with it. The row must still be reported, so the
// narrowing is visible rather than hidden.
func TestOnlyRunReportsPermissionsWithoutWritingThem(t *testing.T) {
	routes := "a: 1\n"
	scopes := "x: 1\n"
	members := map[string]string{
		"MANIFEST.md":                       "**GitOps Build**: v1\n",
		"external/_permissions/routes.yaml": routes,
		"external/_permissions/scopes.yaml": scopes,
	}
	a := buildArchive(t, members)

	dest := t.TempDir()
	mf := &manifest{Entries: map[string]manifestEntry{}}
	rows, writes, err := ingestPermissions(a, "external", dest, mf, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(writes) != 0 {
		t.Fatalf("a narrowed run must queue no permission writes, got %d", len(writes))
	}
	if len(rows) != len(permissionFiles) {
		t.Fatalf("rows = %d, want one per permission file (%d)", len(rows), len(permissionFiles))
	}
	for _, r := range rows {
		if !strings.Contains(r.note, "reported only") {
			t.Errorf("%s note = %q, want it to say the row is reported only", r.name, r.note)
		}
	}
}

// The manifest is the machine-readable half of the unchanged/updated report;
// it had no test of its own before this.
func TestManifestRoundTrip(t *testing.T) {
	dest := t.TempDir()
	mf := &manifest{Entries: map[string]manifestEntry{
		"openapi-jpapi.yaml": {Build: "v42", Source: "external/jpapi", SHA256: "deadbeef"},
	}}
	if err := saveManifest(dest, mf); err != nil {
		t.Fatal(err)
	}
	got := loadManifest(dest)
	if len(got.Entries) != 1 || got.Entries["openapi-jpapi.yaml"].Build != "v42" {
		t.Fatalf("round trip = %+v, want the entry saved above", got.Entries)
	}
}

// A corrupt or unreadable manifest must degrade to "everything is new"
// rather than fail the whole ingest — the bytes written never depend on it.
func TestCorruptManifestDegradesToEverythingNew(t *testing.T) {
	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, manifestName), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	mf := loadManifest(dest)
	if mf.Entries == nil || len(mf.Entries) != 0 {
		t.Fatalf("corrupt manifest loaded as %+v, want an empty entries map", mf.Entries)
	}
}

// CLAUDE.md's provenance table is the prose copy of specs, and the doc
// comment at the top of this file (and CLAUDE.md itself) both say the two
// must agree. Nothing enforced that before this test: it parses the
// "Provenance — the full table" section and checks every dest/dir pair and
// every held/unheld row against the code.
func TestCLAUDEMdProvenanceTableMatchesSpecs(t *testing.T) {
	raw, err := os.ReadFile("../../../CLAUDE.md")
	if err != nil {
		t.Skipf("CLAUDE.md unreadable: %v", err)
	}
	body := string(raw)
	tableStart := strings.Index(body, "| `testing/` file | bundle source | at |")
	if tableStart < 0 {
		t.Fatal("CLAUDE.md has no provenance table header")
	}
	section := body[tableStart:]
	if end := strings.Index(section, "\n\n"); end > 0 {
		section = section[:end]
	}

	rowRe := regexp.MustCompile("(?m)^\\| `([^`]+)` \\| `external/([^`]+)` \\| (.+) \\|$")
	rowMatches := rowRe.FindAllStringSubmatch(section, -1)
	if len(rowMatches) == 0 {
		t.Fatal("no provenance rows parsed out of CLAUDE.md")
	}

	type docRow struct {
		dir  string
		held bool
	}
	got := map[string]docRow{}
	for _, m := range rowMatches {
		got[m[1]] = docRow{dir: m[2], held: strings.Contains(m[3], "(**held**)")}
	}

	if len(got) != len(specs) {
		t.Fatalf("CLAUDE.md carries %d provenance rows, specs carries %d", len(got), len(specs))
	}
	for _, s := range specs {
		d, ok := got[s.dest]
		if !ok {
			t.Errorf("CLAUDE.md has no provenance row for %s", s.dest)
			continue
		}
		if d.dir != s.dir {
			t.Errorf("%s: CLAUDE.md names external/%s, specs names external/%s", s.dest, d.dir, s.dir)
		}
		if d.held != (s.heldAt != "") {
			t.Errorf("%s: CLAUDE.md marks held=%v, specs' heldAt=%q", s.dest, d.held, s.heldAt)
		}
	}
}

func TestSummarizeCountsOperationsNotPaths(t *testing.T) {
	doc := `openapi: 3.0.1
info:
  title: Example API
  version: 9.9.9
paths:
  /a:
    get: {operationId: a}
    delete: {operationId: b}
    parameters: []
  /b:
    post: {operationId: c}
`
	title, version, npaths, nops, err := summarize([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if title != "Example API" || version != "9.9.9" {
		t.Fatalf("title/version = %q/%q", title, version)
	}
	if npaths != 2 {
		t.Fatalf("npaths = %d, want 2", npaths)
	}
	// parameters is a path-item sibling of the verbs and must not be counted.
	if nops != 3 {
		t.Fatalf("nops = %d, want 3", nops)
	}
}

// routes.yaml has been byte-different and sort-identical in five builds. The
// reordering check is what stops that reading as a content change.
func TestSortedSHA256IsBlindToLineOrderOnly(t *testing.T) {
	a := []byte("alpha\nbeta\ngamma\n")
	reordered := []byte("gamma\nalpha\nbeta\n")
	changed := []byte("alpha\nbeta\ndelta\n")
	if sortedSHA256(a) != sortedSHA256(reordered) {
		t.Fatal("a pure reordering must hash the same")
	}
	if sortedSHA256(a) == sortedSHA256(changed) {
		t.Fatal("a content change must not hash the same")
	}
	if sha256Bytes(a) == sha256Bytes(reordered) {
		t.Fatal("the plain hash must still see the reordering")
	}
}

// Every row must correspond to a config.json spec entry, or the ingest writes
// a file the generator never reads.
func TestEverySpecRowIsWhitelistedInConfig(t *testing.T) {
	raw, err := os.ReadFile("../config.json")
	if err != nil {
		t.Skipf("config.json unreadable: %v", err)
	}
	cfg := string(raw)
	for _, s := range specs {
		if !strings.Contains(cfg, `"testing/`+s.dest+`"`) {
			t.Errorf("%s has no config.json spec entry naming testing/%s", s.dest, s.dest)
		}
		if s.title == "" {
			t.Errorf("%s has no title assertion", s.dest)
		}
		if (s.heldAt == "") != (s.why == "") {
			t.Errorf("%s: heldAt and why must be set together", s.dest)
		}
	}
}
