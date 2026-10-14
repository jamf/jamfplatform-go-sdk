// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// maxReportedRemovals bounds the report. A handful of removals is a diff to read; hundreds means
// the generator read the wrong thing, and printing every one buries that under its own evidence.
const maxReportedRemovals = 25

// removals is what a regeneration takes away from the embedded tables: a name the SDK's tables
// recognise today and report as unknown afterwards, or a key they accept today and constrain more
// tightly afterwards. The tables track upstream rather than retaining a vocabulary Apple has
// retired, so a withdrawal or a tightening is carried through deliberately — but every finding the
// tables produce is an error, so it turns a configuration that validates today into one that fails.
// Reporting it is what keeps that visible, since a refresh changes several thousand lines and these
// are a handful among them.
type removals struct {
	Findings []string
}

// compareTables lists what the newly generated tables no longer carry or now constrain more
// tightly, relative to the tables they are about to replace. It only reports: the generated tables
// are built from the checkouts alone and nothing here feeds back into them. Both baselines are
// optional — a first generation has nothing to compare against — while an unreadable one is an
// error rather than agreement, because a baseline that silently decodes as empty makes every
// regeneration look safe.
func compareTables(profilesPath string, profiles *table, declarationsPath string, declarations *declarationTable) (*removals, error) {
	found := &removals{}

	previousProfiles, err := readPrevious[table](profilesPath)
	if err != nil {
		return nil, err
	}
	if previousProfiles != nil {
		comparePayloads(found, previousProfiles, profiles)
	}

	previousDeclarations, err := readPrevious[declarationTable](declarationsPath)
	if err != nil {
		return nil, err
	}
	if previousDeclarations != nil {
		compareDeclarations(found, previousDeclarations, declarations)
	}

	sort.Strings(found.Findings)
	return found, nil
}

// readPrevious decodes a committed table, returning nil when the file does not exist yet.
func readPrevious[T any](path string) (*T, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var decoded T
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &decoded, nil
}

// comparePayloads records every payload type and payload key the regenerated profile table drops,
// and every key it constrains more tightly than before. Enums and bounds are left out for payloads:
// the profile validator does not consult them, because Jamf accepts values outside them, so a
// narrowed one fails nothing and reporting it would put a false alarm in the refresh body.
func comparePayloads(found *removals, previous, current *table) {
	for payloadType, was := range previous.Payloads {
		is, kept := current.Payloads[payloadType]
		if !kept {
			found.record("payload type %s", payloadType)
			continue
		}
		owner := "payload " + payloadType
		compareKeys(found, owner, was.Keys, is.Keys, false)
		compareWildcard(found, owner, owner, was.Any, is.Any, false)
	}
}

// compareDeclarations records every declaration type, declaration key and status item the
// regenerated declaration table drops, and every key it constrains more tightly than before.
func compareDeclarations(found *removals, previous, current *declarationTable) {
	for declarationType, was := range previous.Declarations {
		is, kept := current.Declarations[declarationType]
		if !kept {
			found.record("declaration type %s", declarationType)
			continue
		}
		owner := "declaration " + declarationType
		compareKeys(found, owner, was.Keys, is.Keys, true)
		compareWildcard(found, owner, owner, was.Any, is.Any, true)
	}

	carried := make(map[string]bool, len(current.StatusItems))
	for _, item := range current.StatusItems {
		carried[item] = true
	}
	for _, item := range previous.StatusItems {
		if !carried[item] {
			found.record("status item %s", item)
		}
	}
}

// compareKeys walks two key maps to the same depth the tables record, so a key removed from a
// nested dictionary is caught as well as a top-level one.
func compareKeys(found *removals, owner string, previous, current map[string]*schema, values bool) {
	for name, was := range previous {
		is, kept := current[name]
		if !kept {
			found.record("%s key %s", owner, name)
			continue
		}
		compareSchema(found, owner, name, was, is, values)
	}
}

// compareSchema records what makes one key stricter than it was, then descends into the schemas
// beneath it: its named keys, its wildcard and its array element. A name can survive a regeneration
// and still reject a value it accepted yesterday — a changed type, a key newly required, an enum
// value withdrawn, a bound narrowed — and the validators report each of those as an error, exactly
// as they do a removed name. Only tightenings are recorded: a loosening cannot fail a configuration.
// values says whether the validator for this table enforces enums and bounds, which only the
// declaration validator does.
func compareSchema(found *removals, owner, name string, was, is *schema, values bool) {
	if was == nil || is == nil {
		return
	}
	where := owner + " key " + name

	// A key that became `any` accepts every value, so nothing beneath it can tighten either. A
	// type changed to anything else is the finding: the keys beneath a dictionary that became a
	// string are gone as a consequence, and listing each of them would bury the cause.
	if is.Type == typeAny {
		return
	}
	if typeTightened(was.Type, is.Type) {
		found.record("%s: type changed from %s to %s", where, was.Type, is.Type)
		return
	}

	if is.Required && !was.Required {
		found.record("%s: now required", where)
	}
	if values {
		compareEnum(found, where, was.Enum, is.Enum)
		compareBounds(found, where, was, is)
	}

	compareKeys(found, owner+"."+name, was.Keys, is.Keys, values)
	compareWildcard(found, where, owner+"."+name, was.Any, is.Any, values)
	compareSchema(found, owner, name+"[]", was.Item, is.Item, values)
}

// typeTightened reports whether a declared type changed in a way the validators accept less under.
// Both validators carry string, data and date alike as a JSON string, and accept an integer where a
// real is declared, so a change within the first group, or from integer to real, rejects nothing
// it accepted before and would only be a false alarm in the refresh body.
func typeTightened(was, is string) bool {
	if was == is {
		return false
	}
	carriedAsString := map[string]bool{"string": true, "data": true, "date": true}
	if carriedAsString[was] && carriedAsString[is] {
		return false
	}
	return was != "integer" || is != "real"
}

// compareWildcard compares the schema a dictionary applies to arbitrary key names. Losing the
// wildcard is itself a tightening, since every key name it accepted that the table does not now
// list by name becomes unknown: this is how a free-form dictionary that gains named keys upstream
// shows up. where names the dictionary in a finding, and nested is the owner its keys are reported
// under, which differ for a key's wildcard but not for a payload's or declaration's own.
func compareWildcard(found *removals, where, nested string, was, is *schema, values bool) {
	if was == nil {
		return
	}
	if is == nil {
		found.record("%s: no longer accepts arbitrary key names", where)
		return
	}
	compareSchema(found, nested, wildcardKey, was, is, values)
}

// compareEnum records a value the regenerated enum no longer accepts, or an enum declared where
// there was none. An absent enum means the key is unconstrained, so gaining one is a tightening
// and losing one is not.
func compareEnum(found *removals, where string, was, is []any) {
	if len(is) == 0 {
		return
	}
	if len(was) == 0 {
		found.record("%s: now restricted to an enum of %d value(s)", where, len(is))
		return
	}
	var withdrawn []string
	for _, value := range was {
		if !enumContains(is, value) {
			withdrawn = append(withdrawn, formatEnumValue(value))
		}
	}
	if len(withdrawn) > 0 {
		found.record("%s: enum no longer accepts %s", where, strings.Join(withdrawn, ", "))
	}
}

// compareBounds records a minimum raised or newly declared, and a maximum lowered or newly
// declared. formatBound comes from the union logic, so both reports spell a bound the same way.
func compareBounds(found *removals, where string, was, is *schema) {
	switch {
	case is.Min == nil:
	case was.Min == nil:
		found.record("%s: min newly set to %s", where, formatBound(is.Min))
	case *is.Min > *was.Min:
		found.record("%s: min raised from %s to %s", where, formatBound(was.Min), formatBound(is.Min))
	}
	switch {
	case is.Max == nil:
	case was.Max == nil:
		found.record("%s: max newly set to %s", where, formatBound(is.Max))
	case *is.Max < *was.Max:
		found.record("%s: max lowered from %s to %s", where, formatBound(was.Max), formatBound(is.Max))
	}
}

// enumContains reports whether an enum accepts a value, comparing numbers by value rather than by
// representation, as the validators do. The baseline is decoded from JSON, which yields float64,
// while the regenerated table carries what scalarValue parsed from YAML, which is int64 for an
// integer: compared as interface values, an unchanged enum of 14 would read as withdrawing 14.
func enumContains(allowed []any, value any) bool {
	if number, ok := enumNumber(value); ok {
		for _, candidate := range allowed {
			if other, ok := enumNumber(candidate); ok && other == number {
				return true
			}
		}
		return false
	}
	return slices.Contains(allowed, value)
}

// enumNumber returns an enum value as a float64 when it is a number of any representation the
// table can hold.
func enumNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int64:
		return float64(typed), true
	case int:
		return float64(typed), true
	default:
		return 0, false
	}
}

// formatEnumValue renders an enum value for the report: a string quoted, so a reviewer can see an
// empty or space-padded value, and a number in its shortest form, so 14 and 14.0 read the same.
func formatEnumValue(value any) string {
	if number, ok := enumNumber(value); ok {
		return formatBound(&number)
	}
	if text, ok := value.(string); ok {
		return strconv.Quote(text)
	}
	return fmt.Sprint(value)
}

// record adds one finding.
func (r *removals) record(format string, args ...any) {
	r.Findings = append(r.Findings, fmt.Sprintf(format, args...))
}

// report renders the findings for stderr, naming at most maxReportedRemovals of them.
func (r *removals) report() string {
	shown := r.Findings
	var elided int
	if len(shown) > maxReportedRemovals {
		elided = len(shown) - maxReportedRemovals
		shown = shown[:maxReportedRemovals]
	}

	var builder strings.Builder
	for _, finding := range shown {
		fmt.Fprintf(&builder, "  - %s\n", finding)
	}
	if elided > 0 {
		fmt.Fprintf(&builder, "  - ... and %d more\n", elided)
	}
	return builder.String()
}
