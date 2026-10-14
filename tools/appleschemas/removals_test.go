// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBaseline writes a table to a temporary file, standing in for the committed artefact a
// regeneration is about to replace.
func writeBaseline(t *testing.T, name string, value any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encoding baseline: %v", err)
	}
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatalf("writing baseline: %v", err)
	}
	return path
}

func TestCompareTablesReportsWhatARegenerationDrops(t *testing.T) {
	previousProfiles := &table{Payloads: map[string]*payload{
		"com.apple.gone": {Keys: map[string]*schema{"Anything": {Type: "string"}}},
		"com.apple.kept": {Keys: map[string]*schema{
			"Stays": {Type: "string"},
			"Goes":  {Type: "string"},
			"Nested": {Type: typeDictionary, Keys: map[string]*schema{
				"Inner": {Type: "string"},
			}},
			"Items": {Type: typeArray, Item: &schema{Type: typeDictionary, Keys: map[string]*schema{
				"Element": {Type: "string"},
			}}},
		}},
	}}
	currentProfiles := &table{Payloads: map[string]*payload{
		"com.apple.kept": {Keys: map[string]*schema{
			"Stays":  {Type: "string"},
			"Nested": {Type: typeDictionary, Keys: map[string]*schema{}},
			"Items":  {Type: typeArray, Item: &schema{Type: typeDictionary, Keys: map[string]*schema{}}},
		}},
	}}

	previousDeclarations := &declarationTable{
		Declarations: map[string]*declaration{
			"com.apple.configuration.gone": {Keys: map[string]*schema{}},
			"com.apple.configuration.kept": {Keys: map[string]*schema{"Dropped": {Type: "string"}}},
		},
		StatusItems: []string{"device.identifier.serial-number", "device.gone"},
	}
	currentDeclarations := &declarationTable{
		Declarations: map[string]*declaration{
			"com.apple.configuration.kept": {Keys: map[string]*schema{}},
		},
		StatusItems: []string{"device.identifier.serial-number"},
	}

	found, err := compareTables(
		writeBaseline(t, "profiles.json", previousProfiles), currentProfiles,
		writeBaseline(t, "declarations.json", previousDeclarations), currentDeclarations,
	)
	if err != nil {
		t.Fatalf("compareTables: %v", err)
	}

	want := []string{
		"declaration com.apple.configuration.kept key Dropped",
		"declaration type com.apple.configuration.gone",
		"payload com.apple.kept key Goes",
		"payload com.apple.kept.Items[] key Element",
		"payload com.apple.kept.Nested key Inner",
		"payload type com.apple.gone",
		"status item device.gone",
	}
	if strings.Join(found.Findings, "\n") != strings.Join(want, "\n") {
		t.Fatalf("findings:\n%s\nwant:\n%s", strings.Join(found.Findings, "\n"), strings.Join(want, "\n"))
	}
}

// A payload type that disappears is reported once, rather than once per key it carried: the type
// itself is the finding, and enumerating its keys would bury it.
func TestCompareTablesReportsARemovedTypeOnce(t *testing.T) {
	previous := &table{Payloads: map[string]*payload{
		"com.apple.gone": {Keys: map[string]*schema{"One": {Type: "string"}, "Two": {Type: "string"}}},
	}}

	found, err := compareTables(
		writeBaseline(t, "profiles.json", previous), &table{Payloads: map[string]*payload{}},
		filepath.Join(t.TempDir(), "absent.json"), &declarationTable{},
	)
	if err != nil {
		t.Fatalf("compareTables: %v", err)
	}
	if len(found.Findings) != 1 || found.Findings[0] != "payload type com.apple.gone" {
		t.Fatalf("findings: %v", found.Findings)
	}
}

// A first generation has no baseline on disk, which is not a removal.
func TestCompareTablesAcceptsAMissingBaseline(t *testing.T) {
	directory := t.TempDir()
	found, err := compareTables(
		filepath.Join(directory, "profiles.json"), &table{Payloads: map[string]*payload{}},
		filepath.Join(directory, "declarations.json"), &declarationTable{},
	)
	if err != nil {
		t.Fatalf("compareTables: %v", err)
	}
	if len(found.Findings) != 0 {
		t.Fatalf("findings: %v", found.Findings)
	}
}

// An unreadable baseline is an error, never silent agreement: a baseline that decodes as empty
// makes every regeneration look safe.
func TestCompareTablesRefusesAnUnreadableBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("writing baseline: %v", err)
	}
	if _, err := compareTables(path, &table{}, filepath.Join(t.TempDir(), "absent.json"), &declarationTable{}); err == nil {
		t.Fatal("expected an error for an undecodable baseline")
	}
}

func TestRemovalReportElidesBeyondTheBound(t *testing.T) {
	found := &removals{}
	for i := range maxReportedRemovals + 3 {
		found.record("payload type com.apple.%d", i)
	}
	report := found.report()
	if strings.Count(report, "\n") != maxReportedRemovals+1 {
		t.Fatalf("report lines:\n%s", report)
	}
	if !strings.Contains(report, "and 3 more") {
		t.Fatalf("report does not say how many were elided:\n%s", report)
	}
}

// A name can survive a regeneration and still reject a value it accepted yesterday. Each case
// regenerates one key, Bar, of one declaration and lists what the report must say about it; the
// loosening cases must say nothing, because a loosening cannot fail a configuration. Declarations
// carry the cases because their validator enforces every constraint compared here.
func TestCompareTablesReportsWhatARegenerationTightens(t *testing.T) {
	cases := []struct {
		name     string
		was, is  *schema
		findings []string
	}{
		{
			name:     "type changed",
			was:      &schema{Type: "string"},
			is:       &schema{Type: "integer"},
			findings: []string{"declaration com.apple.configuration.foo key Bar: type changed from string to integer"},
		},
		{
			// The type is the finding: the keys a dictionary carried are gone because it is no
			// longer a dictionary, and listing them would bury that.
			name: "type changed away from a dictionary reports only the type",
			was: &schema{Type: typeDictionary, Keys: map[string]*schema{
				"Inner": {Type: "string"},
			}},
			is:       &schema{Type: "string"},
			findings: []string{"declaration com.apple.configuration.foo key Bar: type changed from dictionary to string"},
		},
		{
			name:     "newly required",
			was:      &schema{Type: "string"},
			is:       &schema{Type: "string", Required: true},
			findings: []string{"declaration com.apple.configuration.foo key Bar: now required"},
		},
		{
			name:     "enum value withdrawn",
			was:      &schema{Type: "string", Enum: []any{"X", "Y", "Z"}},
			is:       &schema{Type: "string", Enum: []any{"Y"}},
			findings: []string{`declaration com.apple.configuration.foo key Bar: enum no longer accepts "X", "Z"`},
		},
		{
			name:     "enum newly declared",
			was:      &schema{Type: "string"},
			is:       &schema{Type: "string", Enum: []any{"X", "Y"}},
			findings: []string{"declaration com.apple.configuration.foo key Bar: now restricted to an enum of 2 value(s)"},
		},
		{
			name:     "numeric enum value withdrawn",
			was:      &schema{Type: "integer", Enum: []any{float64(14), float64(19)}},
			is:       &schema{Type: "integer", Enum: []any{int64(19)}},
			findings: []string{"declaration com.apple.configuration.foo key Bar: enum no longer accepts 14"},
		},
		{
			name:     "min raised",
			was:      &schema{Type: "integer", Min: new(float64(1))},
			is:       &schema{Type: "integer", Min: new(float64(5))},
			findings: []string{"declaration com.apple.configuration.foo key Bar: min raised from 1 to 5"},
		},
		{
			name:     "min newly set",
			was:      &schema{Type: "integer"},
			is:       &schema{Type: "integer", Min: new(float64(0))},
			findings: []string{"declaration com.apple.configuration.foo key Bar: min newly set to 0"},
		},
		{
			name:     "max lowered",
			was:      &schema{Type: "real", Max: new(10.5)},
			is:       &schema{Type: "real", Max: new(float64(10))},
			findings: []string{"declaration com.apple.configuration.foo key Bar: max lowered from 10.5 to 10"},
		},
		{
			name:     "max newly set",
			was:      &schema{Type: "integer"},
			is:       &schema{Type: "integer", Max: new(float64(100))},
			findings: []string{"declaration com.apple.configuration.foo key Bar: max newly set to 100"},
		},
		{
			name: "beneath a dictionary's wildcard",
			was:  &schema{Type: typeDictionary, Any: &schema{Type: "string"}},
			is:   &schema{Type: typeDictionary, Any: &schema{Type: "string", Enum: []any{"on", "off"}}},
			findings: []string{
				"declaration com.apple.configuration.foo.Bar key ANY: now restricted to an enum of 2 value(s)",
			},
		},
		{
			// A free-form dictionary that gains named keys loses its wildcard, so every other key
			// name it accepted becomes unknown.
			name: "wildcard withdrawn",
			was:  &schema{Type: typeDictionary, Any: &schema{Type: typeAny}},
			is: &schema{Type: typeDictionary, Keys: map[string]*schema{
				"Named": {Type: "string"},
			}},
			findings: []string{"declaration com.apple.configuration.foo key Bar: no longer accepts arbitrary key names"},
		},
		{
			name:     "beneath an array's item",
			was:      &schema{Type: typeArray, Item: &schema{Type: "integer", Max: new(float64(8))}},
			is:       &schema{Type: typeArray, Item: &schema{Type: "integer", Max: new(float64(4))}},
			findings: []string{"declaration com.apple.configuration.foo key Bar[]: max lowered from 8 to 4"},
		},
		{
			name: "beneath an array's item dictionary",
			was: &schema{Type: typeArray, Item: &schema{Type: typeDictionary, Keys: map[string]*schema{
				"Element": {Type: "string"},
			}}},
			is: &schema{Type: typeArray, Item: &schema{Type: typeDictionary, Keys: map[string]*schema{
				"Element": {Type: "string", Required: true},
			}}},
			findings: []string{"declaration com.apple.configuration.foo.Bar[] key Element: now required"},
		},
		{
			name: "several tightenings at once",
			was:  &schema{Type: "integer", Min: new(float64(0)), Max: new(float64(10))},
			is:   &schema{Type: "integer", Required: true, Min: new(float64(1)), Max: new(float64(9))},
			findings: []string{
				"declaration com.apple.configuration.foo key Bar: max lowered from 10 to 9",
				"declaration com.apple.configuration.foo key Bar: min raised from 0 to 1",
				"declaration com.apple.configuration.foo key Bar: now required",
			},
		},
		{
			// 14 decoded from the committed JSON is float64, and 14 parsed from Apple's YAML is
			// int64: the same value, so the same enum.
			name: "numeric enum unchanged across representations",
			was:  &schema{Type: "integer", Enum: []any{float64(14), float64(19)}},
			is:   &schema{Type: "integer", Enum: []any{int64(14), int64(19)}},
		},
		{
			name: "type widened to any",
			was:  &schema{Type: typeDictionary, Keys: map[string]*schema{"Inner": {Type: "string"}}},
			is:   &schema{Type: typeAny},
		},
		{
			name: "required dropped",
			was:  &schema{Type: "string", Required: true},
			is:   &schema{Type: "string"},
		},
		{
			name: "enum widened",
			was:  &schema{Type: "string", Enum: []any{"X"}},
			is:   &schema{Type: "string", Enum: []any{"X", "Y"}},
		},
		{
			name: "enum dropped",
			was:  &schema{Type: "string", Enum: []any{"X"}},
			is:   &schema{Type: "string"},
		},
		{
			name: "bounds widened",
			was:  &schema{Type: "integer", Min: new(float64(1)), Max: new(float64(10))},
			is:   &schema{Type: "integer", Min: new(float64(0)), Max: new(float64(20))},
		},
		{
			name: "bounds dropped",
			was:  &schema{Type: "integer", Min: new(float64(1)), Max: new(float64(10))},
			is:   &schema{Type: "integer"},
		},
		{
			name: "wildcard loosened",
			was:  &schema{Type: typeDictionary, Any: &schema{Type: "string", Enum: []any{"on"}}},
			is:   &schema{Type: typeDictionary, Any: &schema{Type: typeAny}},
		},
		{
			// Both validators carry string, data and date alike as a JSON string.
			name: "type moved between string, data and date",
			was:  &schema{Type: "string"},
			is:   &schema{Type: "date"},
		},
		{
			// Both validators accept an integer where a real is declared.
			name: "type widened from integer to real",
			was:  &schema{Type: "integer"},
			is:   &schema{Type: "real"},
		},
		{
			name:     "type narrowed from real to integer",
			was:      &schema{Type: "real"},
			is:       &schema{Type: "integer"},
			findings: []string{"declaration com.apple.configuration.foo key Bar: type changed from real to integer"},
		},
		{
			name: "wildcard gained",
			was:  &schema{Type: typeDictionary, Keys: map[string]*schema{"Named": {Type: "string"}}},
			is: &schema{Type: typeDictionary, Keys: map[string]*schema{"Named": {Type: "string"}},
				Any: &schema{Type: typeAny}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			previous := &declarationTable{Declarations: map[string]*declaration{
				"com.apple.configuration.foo": {Keys: map[string]*schema{"Bar": tc.was}},
			}}
			current := &declarationTable{Declarations: map[string]*declaration{
				"com.apple.configuration.foo": {Keys: map[string]*schema{"Bar": tc.is}},
			}}
			found, err := compareTables(
				filepath.Join(t.TempDir(), "absent.json"), &table{},
				writeBaseline(t, "declarations.json", previous), current,
			)
			if err != nil {
				t.Fatalf("compareTables: %v", err)
			}
			if strings.Join(found.Findings, "\n") != strings.Join(tc.findings, "\n") {
				t.Fatalf("findings:\n%s\nwant:\n%s", strings.Join(found.Findings, "\n"), strings.Join(tc.findings, "\n"))
			}
		})
	}
}

// The profile validator does not consult enums or bounds, because Jamf accepts a payload value
// outside them, so narrowing one fails no configuration and must not reach the refresh body as a
// finding. What the profile validator does enforce — the type, required — is still reported.
func TestCompareTablesIgnoresPayloadEnumsAndBounds(t *testing.T) {
	previous := &table{Payloads: map[string]*payload{
		"com.apple.foo": {Keys: map[string]*schema{
			"Narrowed": {Type: "integer", Enum: []any{float64(1), float64(2)}, Min: new(float64(0)), Max: new(float64(10))},
			"Required": {Type: "string"},
		}},
	}}
	current := &table{Payloads: map[string]*payload{
		"com.apple.foo": {Keys: map[string]*schema{
			"Narrowed": {Type: "integer", Enum: []any{int64(1)}, Min: new(float64(1)), Max: new(float64(5))},
			"Required": {Type: "string", Required: true},
		}},
	}}
	found, err := compareTables(
		writeBaseline(t, "profiles.json", previous), current,
		filepath.Join(t.TempDir(), "absent.json"), &declarationTable{},
	)
	if err != nil {
		t.Fatalf("compareTables: %v", err)
	}
	want := "payload com.apple.foo key Required: now required"
	if got := strings.Join(found.Findings, "\n"); got != want {
		t.Fatalf("findings:\n%s\nwant:\n%s", got, want)
	}
}

// A declaration's own wildcard, not just a key's, is compared: one that tightens is reported under
// the declaration, and one that disappears names the declaration as closed.
func TestCompareTablesReportsATopLevelWildcardTightening(t *testing.T) {
	previous := &declarationTable{Declarations: map[string]*declaration{
		"com.apple.configuration.tightened": {Keys: map[string]*schema{}, Any: &schema{Type: "string"}},
		"com.apple.configuration.closed":    {Keys: map[string]*schema{}, Any: &schema{Type: typeAny}},
	}}
	current := &declarationTable{Declarations: map[string]*declaration{
		"com.apple.configuration.tightened": {Keys: map[string]*schema{}, Any: &schema{Type: "integer"}},
		"com.apple.configuration.closed":    {Keys: map[string]*schema{"Named": {Type: "string"}}},
	}}

	found, err := compareTables(
		filepath.Join(t.TempDir(), "absent.json"), &table{},
		writeBaseline(t, "declarations.json", previous), current,
	)
	if err != nil {
		t.Fatalf("compareTables: %v", err)
	}
	want := []string{
		"declaration com.apple.configuration.closed: no longer accepts arbitrary key names",
		"declaration com.apple.configuration.tightened key ANY: type changed from string to integer",
	}
	if strings.Join(found.Findings, "\n") != strings.Join(want, "\n") {
		t.Fatalf("findings:\n%s\nwant:\n%s", strings.Join(found.Findings, "\n"), strings.Join(want, "\n"))
	}
}
