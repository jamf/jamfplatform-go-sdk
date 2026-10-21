// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// The published permissions map at
// https://developer.jamf.com/platform-api/reference/jamf-pro-permissions-map.md
// is a third privilege oracle, and the only independent one.
//
// The specs' own `x-required-privileges` is where the SDK's registries and
// godoc come from. The GitOps bundle's `_permissions/routes.yaml` cannot
// corroborate that, because it is *generated from* those same extensions — its
// agreement is tautological. The published map is hand-authored from the
// gateway's capability model, so agreement between it and the specs is
// evidence, and a disagreement is a defect in one of them.
//
// This file parses a committed snapshot of it. `TestScopedPrivilegesUseGAVocabulary`
// is the enforcement point: it reads the generated registries and checks every
// identifier against what is parsed here.
//
// # Why a parsed snapshot rather than a transcribed table
//
// That test previously asserted against `gaCapabilityActions`, a hand-typed
// transcription of the same article. It caught real defects, but a
// transcription drifts silently from its source and every ingest that meets a
// new capability has to extend it by hand. Parsing the article removes both:
// `make permmap` re-fetches it, and the check is against what Jamf publishes
// rather than against what somebody last copied out of it.
//
// The snapshot is committed because the check must run in CI, which has no
// network and no access to the private specs.
//
// # What is checked, and what deliberately is not
//
// Checked: every `{capability}:{action}` the generated registries carry must be
// declared by the map — the capability present, and the action in its declared
// set — plus the identifier form itself, since a return to the retired
// three-part beta slug (`create:pro:buildings`) would silently change the
// meaning of every consumer's permissions table. None of that needs path
// semantics, so it is exact.
//
// **Not** checked: whether the map's capability for a *path* matches the spec's.
// The map's Endpoints column lists resource-root prefixes with version segments
// collapsed and longest-prefix-wins, which sounds mechanical, but the cells
// abbreviate continuation roots — `disk-encryption-recovery-key` reads
// "Pro `/computers-inventory/filevault`, `/{id}/filevault`", where the second
// entry means `/computers-inventory/{id}/filevault` and its prefix has to be
// inferred from the preceding entry in the same product chunk. Inferring it
// wrongly would silently attribute a path to the broader capability, either
// manufacturing a disagreement that is the parser's fault or hiding a real one.
// A checker that is quietly wrong is worse than no checker, so the path
// dimension is left to the reader.
//
// Nor is the map ever a *source* of privileges. Its actions are a set per
// capability rather than per operation, so deriving them would be less precise
// than the specs already are, and it would get the conjunctions wrong:
// `POST /mobiledevicecommands/command` needs `destructive-device-actions:{x}`
// **and** `devices:{c}`, while `DELETE /logflush` takes `flush-policy-logs:{x}`
// **or** `policies:{d}` — the alternatives-versus-conjunction trap in
// docs/STYLE.md#required-privileges.

// permissionsMapFile is the committed snapshot, relative to the generator's own
// directory (`make permmap` writes it there from the repo root).
const permissionsMapFile = "permissions-map.md"

// minDeclaredCapabilities guards against a snapshot that parsed to nothing — a
// redirect, a login page, or an upstream restructure that moved the tables. A
// parser that silently finds nothing reports perfect agreement, which is the
// one failure mode this check must not have. The published map declared 125
// capabilities on 2026-09-03; anything under this is a broken snapshot rather
// than a shrinking API.
const minDeclaredCapabilities = 100

// mapActionCodes translates the map's action-code shorthand, from its own
// legend: "c create, r read, u update, d delete, dep deploy, x execute".
var mapActionCodes = map[string]string{
	"c": "create", "r": "read", "u": "update",
	"d": "delete", "dep": "deploy", "x": "execute",
}

// permissionsMapException records a `{capability}:{action}` the generated
// registries carry that the published map does not declare, with the reason it
// is tolerated.
//
// It is self-expiring in both directions, the same discipline as
// `schemaPatchesRequireAbsent`: an entry the map has since caught up with, and
// an entry for a permission the SDK no longer emits, both fail. Neither is a
// problem to work around — each means the exception has served its purpose.
type permissionsMapException struct {
	Permission string
	Why        string
}

// permissionsMapExceptions is empty, and that is the finding: as of the
// 2026-09-03 snapshot the published map declares every permission the SDK
// emits, action for action — including all eighteen `account` ones that no
// published spec carries and `config.json` supplies by hand.
//
// It was briefly populated with two `compliance-benchmarks` entries, from a
// throwaway parser that kept only the last row for a capability appearing in
// several rows — losing `compliance-benchmarks:{c,r,d}` over `/benchmarks` and
// keeping `{r}` over `/baselines`. Hence the union rule in
// parsePermissionsMap, and hence the note here: a capability is declared across
// as many rows as its resources need, and reading one row as the whole
// declaration manufactures a disagreement.
var permissionsMapExceptions = []permissionsMapException{
	{Permission: "platform-environments:read", Why: platformEnvironmentsWhy},
	{Permission: "platform-environments:create", Why: platformEnvironmentsWhy},
	{Permission: "platform-environments:update", Why: platformEnvironmentsWhy},
	{Permission: "platform-environments:delete", Why: platformEnvironmentsWhy},
}

// platformEnvironmentsWhy is the evidence for the account-organization
// operations (GitOps v2495). The capability is absent from the published map's
// 2026-10-21 snapshot, but the gateway's authorization policy gates exactly
// these six routes on it (rule dated 2026-09-29), the Account integrations UI
// offers it as a grantable Organization permission ("Platform Environments",
// create/read/update/delete, 2026-10-21), and an organization credential reads
// /organization/v1/environments and /tenants at 200 on the wire. Report the map's omission upstream; the entries expire the day the map
// declares the capability.
const platformEnvironmentsWhy = "gateway policy gates /organization/v1/{environments,tenants} on it; absent from the published map"

var capCellRe = regexp.MustCompile("^`([a-z0-9-]+)(?::\\{([a-z,\\s]+)\\})?`$")

// loadPermissionsMap reads and parses the committed snapshot.
func loadPermissionsMap() (map[string]map[string]bool, error) {
	raw, err := os.ReadFile(permissionsMapFile)
	if err != nil {
		return nil, fmt.Errorf("reading the published permissions map: %w (refresh it with `make permmap`)", err)
	}
	return parsePermissionsMap(string(raw))
}

// parsePermissionsMap reads the capability tables out of the published map,
// returning each declared capability's action set.
//
// Only the "Find the capability for an endpoint you already call" section is
// read. The tables before it are worked examples of the old-to-new conversion
// and would inject capabilities that section does not declare; the tables after
// it list resources with *no* capability — which is exactly why that closing
// heading is required, not merely preferred, to be present: an absent one is
// indistinguishable from "the rest of the document is more of the same
// section" and would silently fold the no-capability tables in as
// declarations.
func parsePermissionsMap(markdown string) (map[string]map[string]bool, error) {
	const (
		startAt = "## Find the capability for an endpoint you already call"
		endAt   = "## Endpoints with no permission"
	)
	_, body, found := strings.Cut(markdown, startAt)
	if !found {
		return nil, fmt.Errorf("heading %q not found — has the published map been restructured?", startAt)
	}
	before, _, found := strings.Cut(body, endAt)
	if !found {
		return nil, fmt.Errorf("heading %q not found — has the published map been restructured?", endAt)
	}
	body = before

	out := map[string]map[string]bool{}
	for line := range strings.SplitSeq(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) < 2 {
			continue
		}
		m := capCellRe.FindStringSubmatch(strings.TrimSpace(cells[1]))
		if m == nil {
			continue // header row, alignment row, or a cell shape this does not read
		}
		name := m[1]
		actions := map[string]bool{}
		for code := range strings.SplitSeq(m[2], ",") {
			code = strings.TrimSpace(code)
			if code == "" {
				continue
			}
			action, ok := mapActionCodes[code]
			if !ok {
				return nil, fmt.Errorf("capability %q declares unknown action code %q", name, code)
			}
			actions[action] = true
		}
		// A capability is declared across as many rows as its resources need,
		// so rows contribute the union of their action sets.
		for a := range out[name] {
			actions[a] = true
		}
		out[name] = actions
	}
	if len(out) < minDeclaredCapabilities {
		return nil, fmt.Errorf(
			"parsed only %d capabilities from %s, expected at least %d — the snapshot is "+
				"truncated or the published map's table shape changed",
			len(out), permissionsMapFile, minDeclaredCapabilities)
	}
	return out, nil
}

// permissionsMapReport is the outcome of checking the generated registries
// against the map. Every slice is sorted, and an empty report is agreement.
type permissionsMapReport struct {
	// BadForm, UnknownCapability and UnknownAction are defects, each rendered
	// as "identifier (packages)".
	BadForm           []string
	UnknownCapability []string
	UnknownAction     []string
	// ExpiredExceptions are recorded exceptions that no longer fire.
	ExpiredExceptions []string
	// UnreachedCapabilities and UnusedActions are coverage, not defects: the
	// map declares them and the SDK's surface does not use them.
	UnreachedCapabilities []string
	UnusedActions         []string
	// Identifiers and Capabilities count what was checked.
	Identifiers, Capabilities int
}

// OK reports whether the check found no defect and no expired exception.
func (r permissionsMapReport) OK() bool {
	return len(r.BadForm) == 0 && len(r.UnknownCapability) == 0 &&
		len(r.UnknownAction) == 0 && len(r.ExpiredExceptions) == 0
}

// checkAgainstPermissionsMap compares generated identifiers with the map. ids
// maps each distinct `{capability}:{action}` to the packages carrying it.
func checkAgainstPermissionsMap(declared map[string]map[string]bool, ids map[string][]string) permissionsMapReport {
	allowed := map[string]bool{}
	for _, e := range permissionsMapExceptions {
		allowed[e.Permission] = true
	}
	hit := map[string]bool{}

	r := permissionsMapReport{Identifiers: len(ids)}
	used := map[string]map[string]bool{}
	for id, pkgs := range ids {
		where := id + " (" + strings.Join(slices.Compact(slices.Sorted(slices.Values(pkgs))), ",") + ")"
		capability, action, wellFormed := strings.Cut(id, ":")
		if !wellFormed || capability == "" || action == "" {
			r.BadForm = append(r.BadForm, where)
			continue
		}
		if used[capability] == nil {
			used[capability] = map[string]bool{}
		}
		used[capability][action] = true

		actions, isDeclared := declared[capability]
		switch {
		case !isDeclared:
			if allowed[id] {
				hit[id] = true
				continue
			}
			r.UnknownCapability = append(r.UnknownCapability, where)
		case !actions[action]:
			if allowed[id] {
				hit[id] = true
				continue
			}
			r.UnknownAction = append(r.UnknownAction,
				where+" — the map declares "+capability+" with {"+strings.Join(sortedKeys(actions), ",")+"}")
		}
	}
	r.Capabilities = len(used)

	for _, e := range permissionsMapExceptions {
		if !hit[e.Permission] {
			r.ExpiredExceptions = append(r.ExpiredExceptions, e.Permission)
		}
	}
	for name, actions := range declared {
		if used[name] == nil {
			r.UnreachedCapabilities = append(r.UnreachedCapabilities, name)
			continue
		}
		for action := range actions {
			if !used[name][action] {
				r.UnusedActions = append(r.UnusedActions, name+":"+action)
			}
		}
	}
	for _, s := range []*[]string{
		&r.BadForm, &r.UnknownCapability, &r.UnknownAction,
		&r.ExpiredExceptions, &r.UnreachedCapabilities, &r.UnusedActions,
	} {
		sort.Strings(*s)
	}
	return r
}
