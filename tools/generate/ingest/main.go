// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

// Command ingest populates the SDK's private source specs (the gitignored
// testing/ directory) from a single Jamf Platform APIs GitOps archive (zip).
//
//	cd tools/generate && go run ./ingest -zip ~/Downloads/jamf-platform-apis-gitops-vNNNN-*.zip
//	make generate
//
// # Every spec is copied verbatim
//
// The archive member is written to testing/ byte for byte, with no re-emission
// and no YAML-to-JSON conversion. That is the whole point: a re-emitted spec
// makes the next build's diff hundreds of lines of indentation churn, and the
// churn hides the delta. Three specs shipped that way and each had to be
// repaired by hand (the account trio, AI Governance, uem-connect) before their
// bundle diffs became readable.
//
// Verbatim copying is why every config.json spec entry names a .yaml file even
// where the family was historically carried as JSON. The generator's loader
// dispatches on the extension and the YAML branch is a strict superset of the
// JSON one — it additionally strips external parameter $refs kin-openapi cannot
// resolve — so the rename is inert to the generated tree. It was verified so:
// converting all seven JSON specs to YAML produced an empty diff across
// jamfplatform/ and api/.
//
// # GA: every spec the SDK carries lives in external/
//
// The archive ships three environment trees (external/ = the production
// gateway, internal/stage/, internal/dev/). As of the GA builds every family
// the SDK carries is published to external/, so that is the default and the
// only tree a normal ingest should use:
//
//   - internal/dev carries a per-spec x-generated block (commitHash, runId,
//     timestamp), so every file reports as changed and a no-op build looks like
//     a bundle-wide rewrite. Never ingest from it. Comparing operation *sets*
//     across environments is what it is good for — that is how the v1942
//     146-operation publishing filter was caught.
//   - internal/stage declares a stage host and an "(STAGE)" title, both of
//     which leak into the published api/*.json a consumer reads.
//
// -env exists for the case where a family has not reached external/ yet. There
// is currently no such family; if one appears, prefer stage over dev and say so
// in the provenance table.
//
// # Holds
//
// A held spec is one the SDK deliberately keeps at an older build because the
// newer one withdraws something the wire still serves and the SDK still needs.
// Held rows are skipped unless -include-held is passed or -only names them, and
// the reason is printed on every run so a hold cannot rot silently. The prose
// authority is the holds table in CLAUDE.md; keep the two in step in the same
// change.
//
// # An unmapped external/ family is a hard error
//
// A newly published API is the single easiest thing to miss in a build: nothing
// in the diff of the specs the SDK already carries mentions it. So any
// external/ directory carrying an openapi.yaml that neither specs nor
// knownUnmapped accounts for fails the run.
//
// # Why CI never runs this
//
// CI regenerates Go from the committed api/*.json published specs via the
// generator's fallback path, never from testing/. This command only refreshes
// the private sources a maintainer uses to regenerate the public api/ surface.
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// specRow maps one testing/ destination filename to its archive family
// directory. dest must match the "file" value of the corresponding entry in
// tools/generate/config.json, minus the "testing/" prefix.
type specRow struct {
	dest string
	dir  string
	// title is a case-insensitive fragment of info.title that the archive
	// member must carry. It is the executable half of "map by title, not by
	// directory name": a renamed or swapped directory fails the run instead of
	// silently replacing a spec with an unrelated API.
	title string
	// heldAt is the build the SDK pins this spec to, empty when not held.
	heldAt string
	// why explains the hold in one line; required when heldAt is set.
	why string
}

// specs is the provenance table: testing/ filename <- external/ directory.
//
// Map by directory *and* verify info.title, never by name alone. Directory
// names are not stable across builds — v1401 renamed securitycloud-dns to
// jsc-dns, and v1865 dropped the -beta suffix from every directory while
// keeping the beta content — and a name-keyed match that finds nothing looks
// exactly like "no changes".
//
// Two rows are named for their tag rather than their API:
// securitycloud-device-groups-api.yaml is the Security Cloud *Devices* API
// (internal/stage/device-groups is the unrelated Platform Device Groups API,
// and copying that one silently replaces the spec), and
// securitycloud-uem-connect-api.yaml comes from uem-connect.
var specs = []specRow{
	{dest: "openapi-jpapi.yaml", dir: "jpapi", title: "Jamf Pro API"},
	{dest: "Classic-openapi.yaml", dir: "capi", title: "Classic API"},
	{dest: "blueprints-api.yaml", dir: "blueprints", title: "Blueprints API"},
	{dest: "device-groups-api.yaml", dir: "device-groups", title: "Device Groups API"},
	{dest: "device-inventory-api.yaml", dir: "devices", title: "Device Inventory API"},
	{dest: "device-management-actions-api.yaml", dir: "device-management-action", title: "Device Management Actions API"},
	{dest: "Declaration-reporting-openapi.yaml", dir: "declaration-reporting", title: "Declaration Reporting Service API"},
	{dest: "jamf-compliance-benchmark-engine-api.yaml", dir: "compliance-benchmarks", title: "Compliance Benchmarks API"},
	{dest: "securitycloud-dns-api.yaml", dir: "jsc-dns", title: "Security Cloud DNS API"},
	{dest: "securitycloud-ztna-api.yaml", dir: "jsc-ztna", title: "ZTNA Public API"},
	{dest: "securitycloud-categories-api.yaml", dir: "jsc-categories", title: "JSC Categories API"},
	{dest: "securitycloud-uem-connect-api.yaml", dir: "uem-connect", title: "UEM Connect API"},
	{dest: "securitycloud-enrollment-api.yaml", dir: "securitycloud-enrollment", title: "Security Cloud Enrollment API"},
	{dest: "securitycloud-device-groups-api.yaml", dir: "securitycloud-devices", title: "Security Cloud Devices API"},
	{dest: "ai-governance-api.yaml", dir: "ai-governance", title: "AI Governance Policies API"},
	{dest: "audit-api.yaml", dir: "audit", title: "Audit API"},
	{dest: "account-licensing-api.yaml", dir: "account-licensing", title: "Jamf Account Licensing API"},
	{dest: "account-partners-api.yaml", dir: "account-partners", title: "Jamf Account Partners API"},
	{dest: "account-sso-api.yaml", dir: "account-sso", title: "Jamf Account SSO API"},
}

// knownUnmapped are external/ families the SDK deliberately does not carry.
// An external/ directory that is in neither specs nor here fails the run: a
// newly published API is invisible in the diff of the specs already carried.
var knownUnmapped = map[string]string{
	"users": "User Inventory API — prod-published with real privileges, but every path 404s; " +
		"platform-users-directory is flag-gated to dev.",
}

// permissionFiles are the privilege oracle, copied alongside the specs. They
// are not generator inputs — routes.yaml is generated from the specs' own
// x-required-privileges, so its agreement is tautological — but they are read
// on every ingest, and routes.yaml has repeatedly changed as a pure reordering
// that a byte comparison alone reports as a change.
var permissionFiles = []string{"routes.yaml", "scopes.yaml"}

// manifestName records, inside the gitignored spec directory, which build each
// spec came from and the hash of the bytes written. It is what lets a run
// report "unchanged since v2051" for a spec rather than merely "differs", and
// it is the machine-readable half of CLAUDE.md's provenance and holds tables.
const manifestName = ".ingest-manifest.json"

type manifestEntry struct {
	Build  string `json:"build"`
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Entries map[string]manifestEntry `json:"entries"`
}

// status is what happened to one destination on this run.
type status string

const (
	statusNew       status = "new"
	statusUpdated   status = "updated"
	statusUnchanged status = "unchanged"
	statusHeld      status = "held"
	// statusSkipped is an unheld spec that -only simply did not name. It must
	// never share statusHeld's rendering: a skipped row's heldAt and why are
	// both empty, and printing "held at  — " invents a hold with a blank build
	// and a blank reason in the exact report whose job is naming real ones.
	statusSkipped status = "skipped"
)

type row struct {
	dest, source, version string
	npaths, nops, nprivs  int
	status                status
	note                  string
}

var (
	buildRe  = regexp.MustCompile(`(?m)^\*\*GitOps Build\*\*:\s*(\S+)`)
	httpVerb = map[string]bool{
		"get": true, "put": true, "post": true, "delete": true,
		"patch": true, "head": true, "options": true, "trace": true,
	}
)

func main() {
	log.SetFlags(0)

	zipPath := flag.String("zip", "", "path to the GitOps archive (.zip) [required]")
	env := flag.String("env", "external", "archive environment tree: external | internal/stage | internal/dev")
	dest := flag.String("dest", "testing", "destination spec directory (relative paths resolve against -root)")
	root := flag.String("root", "", "repo root (default: auto-detected from git)")
	only := flag.String("only", "", "comma-separated destination filenames to ingest; default is every unheld spec")
	includeHeld := flag.Bool("include-held", false, "also ingest specs the SDK holds at an older build")
	dryRun := flag.Bool("dry-run", false, "resolve and report but write nothing")
	flag.Parse()

	if *zipPath == "" {
		log.Fatal("-zip is required (make ingest ZIP=path/to/jamf-platform-apis-gitops-vNNNN-*.zip)")
	}
	switch *env {
	case "external", "internal/stage", "internal/dev":
	default:
		log.Fatalf("invalid -env %q: must be external, internal/stage or internal/dev", *env)
	}
	if *env == "internal/dev" {
		log.Print("WARNING: internal/dev carries a per-spec x-generated block, so every " +
			"file will report as changed even on a no-op build. Ingesting from it is almost " +
			"always a mistake.")
	}

	if *root == "" {
		out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			log.Fatal("cannot detect repo root (pass -root): ", err)
		}
		*root = strings.TrimSpace(string(out))
	}
	zipArg := abs(*root, *zipPath)
	destDir := abs(*root, *dest)

	selected, err := selection(*only, *includeHeld)
	if err != nil {
		log.Fatal(err)
	}

	arc, err := openArchive(zipArg)
	if err != nil {
		log.Fatal(err)
	}
	defer arc.close()

	fmt.Printf("archive %s\n  build   %s\n  sha256  %s\n  env     %s\n\n",
		filepath.Base(zipArg), arc.build, arc.sha256, *env)

	if err := checkUnmapped(arc, *env); err != nil {
		log.Fatal(err)
	}

	// Both phases resolve and validate fully, with no file written and no
	// manifest entry recorded, before either commits. That is what keeps a
	// refusal on spec 16 of 19 (or on either permission file) from leaving
	// spec 1-15 on disk with a manifest that never rewrites to describe them —
	// testing/ would otherwise become a mixture of two builds, and the
	// unchanged/updated report would be wrong for the already-landed rows on
	// the next run.
	mf := loadManifest(destDir)
	rows, specWrites, err := ingest(arc, *env, destDir, selected, mf)
	if err != nil {
		log.Fatal(err)
	}
	printSpecs(rows)

	permRows, permWrites, err := ingestPermissions(arc, *env, destDir, mf, *only == "")
	if err != nil {
		log.Fatal(err)
	}
	printPermissions(permRows)

	if *dryRun {
		fmt.Print("\nDry run — nothing written; re-run without -dry-run to write these files.\n")
		return
	}
	if err := commit(append(specWrites, permWrites...), mf); err != nil {
		log.Fatal(err)
	}
	if err := saveManifest(destDir, mf); err != nil {
		log.Fatal(err)
	}
	fmt.Print("\nNext: `make generate`, read the whole diff, then wire-probe every " +
		"behavioural change before believing it.\n")
}

// abs resolves a flag path. A leading ~/ is expanded because the Makefile
// quotes its arguments (paths may contain spaces), which suppresses the shell's
// own expansion — and a GitOps archive normally sits in ~/Downloads.
func abs(root, p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

// selection resolves -only / -include-held into the set of destinations to
// write. A held spec named by -only is ingested: naming it is the explicit act
// that -include-held otherwise provides in bulk.
func selection(only string, includeHeld bool) (map[string]bool, error) {
	known := map[string]bool{}
	var valid []string
	for _, s := range specs {
		known[s.dest] = true
		valid = append(valid, s.dest)
	}
	sort.Strings(valid)
	if only == "" {
		sel := map[string]bool{}
		for _, s := range specs {
			if s.heldAt == "" || includeHeld {
				sel[s.dest] = true
			}
		}
		return sel, nil
	}
	sel := map[string]bool{}
	for name := range strings.SplitSeq(only, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !known[name] {
			return nil, fmt.Errorf("-only names %q, which is not a destination in the provenance table.\n"+
				"Valid destinations: %s", name, strings.Join(valid, ", "))
		}
		sel[name] = true
	}
	// Every token trimmed to empty (",", " , ", an unset shell variable pasted
	// into a wrapper script) must not silently fall through to "ingest
	// nothing, exit 0" — that is indistinguishable from success.
	if len(sel) == 0 {
		return nil, fmt.Errorf("-only %q resolved to no destinations", only)
	}
	return sel, nil
}

type archive struct {
	rc     *zip.ReadCloser
	files  map[string]*zip.File
	names  []string
	prefix string // optional single top-level wrapper directory, "" when absent
	build  string
	sha256 string
}

func (a *archive) close() { _ = a.rc.Close() }

func openArchive(path string) (*archive, error) {
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return nil, fmt.Errorf("archive not found: %s", path)
	}
	sum, err := fileSHA256(path)
	if err != nil {
		return nil, err
	}
	rc, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("opening archive: %w", err)
	}
	a := &archive{rc: rc, files: map[string]*zip.File{}, sha256: sum}
	for _, f := range rc.File {
		a.files[f.Name] = f
		a.names = append(a.names, f.Name)
	}
	// Tolerate an archive that wraps everything in one top-level directory.
	for _, n := range a.names {
		if i := strings.Index(n, "MANIFEST.md"); i > 0 {
			a.prefix = n[:i]
			break
		}
	}
	if raw, err := a.read(a.prefix + "MANIFEST.md"); err == nil {
		if m := buildRe.FindSubmatch(raw); m != nil {
			a.build = string(m[1])
		}
	}
	if a.build == "" {
		a.build = "unknown"
	}
	return a, nil
}

func (a *archive) read(name string) ([]byte, error) {
	f, ok := a.files[name]
	if !ok {
		return nil, fmt.Errorf("archive member not found: %s", name)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

// families lists the directories under env/ that carry an openapi.yaml.
func (a *archive) families(env string) []string {
	pfx := a.prefix + env + "/"
	seen := map[string]bool{}
	for _, n := range a.names {
		rest, ok := strings.CutPrefix(n, pfx)
		if !ok {
			continue
		}
		dir, file, ok := strings.Cut(rest, "/")
		if !ok || file != "openapi.yaml" {
			continue
		}
		seen[dir] = true
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// checkUnmapped fails when the archive publishes a family that neither the
// provenance table nor knownUnmapped accounts for.
func checkUnmapped(a *archive, env string) error {
	mapped := map[string]bool{}
	for _, s := range specs {
		mapped[s.dir] = true
	}
	var novel []string
	for _, dir := range a.families(env) {
		if mapped[dir] {
			continue
		}
		if _, ok := knownUnmapped[dir]; ok {
			continue
		}
		novel = append(novel, dir)
	}
	if len(novel) == 0 {
		return nil
	}
	return fmt.Errorf(
		"%s/ publishes %d family/families the SDK does not account for: %s.\n"+
			"A newly published API is invisible in the diff of the specs already carried, so this "+
			"is a hard error. Either add a row to the provenance table (and the matching entry to "+
			"tools/generate/config.json), or record it in knownUnmapped with the reason it is not "+
			"carried",
		env, len(novel), strings.Join(novel, ", "))
}

// ingest resolves and validates every spec row against the archive and
// reports what would happen to each, but never touches disk and never mutates
// mf: the pending writes it computes are returned for the caller to commit
// once every row here, and both permission files, have resolved cleanly. That
// two-phase split is deliberate. A refusal on row 16 of 19 must not leave rows
// 1-15 written with a manifest that never rewrites to describe them — testing/
// would then be a mixture of two builds, and the next run's
// unchanged/updated report would be wrong for exactly the rows that already
// landed.
func ingest(a *archive, env, destDir string, selected map[string]bool, mf *manifest) ([]row, []pendingWrite, error) {
	var rows []row
	var writes []pendingWrite
	for _, s := range specs {
		member := a.prefix + env + "/" + s.dir + "/openapi.yaml"
		raw, err := a.read(member)
		if err != nil {
			if selected[s.dest] {
				return nil, nil, fmt.Errorf("family %q: %w (did the archive layout change?)", s.dir, err)
			}
			// A row absent from this archive is normal when reconstructing an
			// older build (a held family published to external/ later than the
			// build being read simply is not there) or when -only excluded it.
			// Only a *selected* row must resolve. Which note applies follows
			// the same split as the selected-but-unheld case below: a held row
			// names its hold, an unheld-but-unselected one must not print a
			// hold it does not have.
			r := row{dest: s.dest, source: env + "/" + s.dir}
			if s.heldAt != "" {
				r.status = statusHeld
				r.note = "absent from this archive; held at " + s.heldAt
			} else {
				r.status = statusSkipped
				r.note = "absent from this archive; not selected by -only"
			}
			rows = append(rows, r)
			continue
		}
		title, version, npaths, nops, err := summarize(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("family %q: %w", s.dir, err)
		}
		if !strings.Contains(strings.ToLower(title), strings.ToLower(s.title)) {
			return nil, nil, fmt.Errorf(
				"family %q: info.title is %q, which does not contain %q.\n"+
					"Either the archive renamed the directory (find the family by title and fix "+
					"the provenance table, here and in CLAUDE.md) or this row now points at an "+
					"unrelated API — copying it would silently replace %s",
				s.dir, title, s.title, s.dest)
		}
		r := row{
			dest:    s.dest,
			source:  env + "/" + s.dir,
			version: version,
			npaths:  npaths,
			nops:    nops,
			nprivs:  strings.Count(string(raw), "x-required-privileges"),
		}
		if !selected[s.dest] {
			// Two distinct triggers land here and must not share one message:
			// a genuinely held spec (heldAt set) versus a spec -only simply
			// did not name. The latter has no heldAt and no why, so reporting
			// it as "held at  — " invents a hold with a blank build and a
			// blank reason in the exact report whose job is naming real ones.
			if s.heldAt != "" {
				r.status = statusHeld
				r.note = "held at " + s.heldAt + " — " + s.why
			} else {
				r.status = statusSkipped
				r.note = "not selected by -only"
			}
			rows = append(rows, r)
			continue
		}
		sum := sha256Bytes(raw)
		outPath := filepath.Join(destDir, s.dest)
		prev, had := mf.Entries[s.dest]
		switch {
		case !had && !fileExists(outPath):
			r.status = statusNew
		case had && prev.SHA256 == sum && fileExists(outPath):
			r.status = statusUnchanged
			r.note = "since " + prev.Build
		default:
			r.status = statusUpdated
			if had {
				r.note = "was " + prev.Build
			}
		}
		if s.heldAt != "" {
			r.note = strings.TrimSpace(r.note + " [hold overridden — update CLAUDE.md's holds table]")
		}
		writes = append(writes, pendingWrite{
			path:  outPath,
			raw:   raw,
			key:   s.dest,
			entry: manifestEntry{Build: a.build, Source: r.source, SHA256: sum},
		})
		rows = append(rows, r)
	}
	return rows, writes, nil
}

// summarize reads info.title, info.version and the path/operation counts.
func summarize(raw []byte) (title, version string, npaths, nops int, err error) {
	var doc struct {
		Info struct {
			Title   string `yaml:"title"`
			Version string `yaml:"version"`
		} `yaml:"info"`
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", "", 0, 0, fmt.Errorf("parsing yaml: %w", err)
	}
	for _, item := range doc.Paths {
		for k := range item {
			if httpVerb[strings.ToLower(k)] {
				nops++
			}
		}
	}
	return doc.Info.Title, doc.Info.Version, len(doc.Paths), nops, nil
}

type permRow struct {
	name   string
	status status
	note   string
}

// ingestPermissions resolves _permissions/{routes,scopes}.yaml and reports a
// change that is only a reordering as such — routes.yaml has been
// byte-different and sort-identical in five separate builds; reading that by
// hand every time is wasted effort, and mistaking it for a real change has
// cost a wrong conclusion. It is ingest's counterpart for these two files and
// shares its two-phase contract: resolve and validate both, write neither,
// and let the caller commit only once every spec row and both of these have
// come back clean.
//
// write is false whenever -only narrows the run. These two files are not
// spec-scoped, so a run selecting one spec would otherwise reset the whole
// privilege oracle to that invocation's build — which is how a `-only capi`
// restore from an older bundle silently reverted routes.yaml here. They are
// still resolved and reported, so the row is not hidden.
func ingestPermissions(a *archive, env, destDir string, mf *manifest, write bool) ([]permRow, []pendingWrite, error) {
	var rows []permRow
	var writes []pendingWrite
	outDir := filepath.Join(destDir, "_permissions")
	for _, name := range permissionFiles {
		member := a.prefix + env + "/_permissions/" + name
		raw, err := a.read(member)
		if err != nil {
			return nil, nil, fmt.Errorf("permissions: %w", err)
		}
		key := "_permissions/" + name
		outPath := filepath.Join(outDir, name)
		sum := sha256Bytes(raw)
		r := permRow{name: key}
		prev, had := mf.Entries[key]
		switch {
		case !had && !fileExists(outPath):
			r.status = statusNew
		case had && prev.SHA256 == sum && fileExists(outPath):
			r.status = statusUnchanged
			r.note = "since " + prev.Build
		default:
			r.status = statusUpdated
			if had {
				r.note = "was " + prev.Build
			}
			if old, err := os.ReadFile(outPath); err == nil {
				if sortedSHA256(old) == sortedSHA256(raw) {
					r.note = strings.TrimSpace(r.note + " — PURE REORDERING (sort-identical, no content change)")
				}
			}
		}
		if write {
			writes = append(writes, pendingWrite{
				path:  outPath,
				raw:   raw,
				key:   key,
				entry: manifestEntry{Build: a.build, Source: env + "/_permissions", SHA256: sum},
			})
		} else {
			const skipped = "reported only; -only narrows the run and these are not spec-scoped — refresh with a full run"
			if r.note == "" {
				r.note = skipped
			} else {
				r.note += " — " + skipped
			}
		}
		rows = append(rows, r)
	}
	return rows, writes, nil
}

// pendingWrite is one file ingest or ingestPermissions has resolved and
// validated but not yet written. key is the manifest entry key (a spec's
// dest, or "_permissions/<name>").
type pendingWrite struct {
	path  string
	raw   []byte
	key   string
	entry manifestEntry
}

// commit writes every pending write to disk and records it in mf. Callers
// must not call this until every selected spec row and both permission files
// have resolved and validated cleanly — that is what makes a refusal partway
// through land zero files instead of a partial build.
func commit(writes []pendingWrite, mf *manifest) error {
	for _, w := range writes {
		dir := filepath.Dir(w.path)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
		if err := os.WriteFile(w.path, w.raw, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", w.path, err)
		}
		mf.Entries[w.key] = w.entry
	}
	return nil
}

func printSpecs(rows []row) {
	dw, sw := len("destination"), len("source")
	for _, r := range rows {
		dw = max(dw, len(r.dest))
		sw = max(sw, len(r.source))
	}
	fmt.Printf("  %-*s  %-*s  %-12s  %5s  %5s  %5s  %s\n",
		dw, "destination", sw, "source", "info.version", "paths", "ops", "privs", "status")
	fmt.Printf("  %s  %s  %s  %s  %s  %s  %s\n",
		rep(dw), rep(sw), rep(12), rep(5), rep(5), rep(5), rep(6))
	for _, r := range rows {
		fmt.Printf("  %-*s  %-*s  %-12s  %5d  %5d  %5d  %s",
			dw, r.dest, sw, r.source, trunc(r.version, 12), r.npaths, r.nops, r.nprivs, r.status)
		if r.note != "" {
			fmt.Printf(" (%s)", r.note)
		}
		fmt.Println()
	}
	fmt.Print("\n  not carried:\n")
	for _, dir := range sortedKeys(knownUnmapped) {
		fmt.Printf("    %s — %s\n", dir, knownUnmapped[dir])
	}
}

func printPermissions(rows []permRow) {
	fmt.Print("\n  privilege oracle:\n")
	for _, r := range rows {
		fmt.Printf("    %-24s %s", r.name, r.status)
		if r.note != "" {
			fmt.Printf(" (%s)", r.note)
		}
		fmt.Println()
	}
}

func loadManifest(destDir string) *manifest {
	mf := &manifest{Entries: map[string]manifestEntry{}}
	raw, err := os.ReadFile(filepath.Join(destDir, manifestName))
	if err != nil {
		return mf
	}
	// A corrupt or older manifest degrades to "everything is new", which is
	// merely noisy: the bytes written do not depend on it.
	if err := json.Unmarshal(raw, mf); err != nil || mf.Entries == nil {
		return &manifest{Entries: map[string]manifestEntry{}}
	}
	return mf
}

func saveManifest(destDir string, mf *manifest) error {
	raw, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destDir, manifestName), append(raw, '\n'), 0o644)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sha256Bytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// sortedSHA256 hashes the file's lines in sorted order, so two files that
// differ only in line order hash the same.
func sortedSHA256(b []byte) string {
	lines := strings.Split(string(b), "\n")
	sort.Strings(lines)
	return sha256Bytes([]byte(strings.Join(lines, "\n")))
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func rep(n int) string { return strings.Repeat("-", n) }

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
