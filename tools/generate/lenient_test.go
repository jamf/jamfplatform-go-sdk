// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// The classification decides which fields the emitted decoder will coerce, so
// it has to be exact in both directions: a missed scalar leaves the defect in
// place, and a wrongly-included container would have the rewrite unquote a
// value the type cannot hold.
func TestGoScalarKind(t *testing.T) {
	aliases := map[string]string{"ConfigVersion": "int", "Cadence": ""}
	cases := []struct {
		field string
		want  string
	}{
		{"int", jsonScalarKindNumber},
		{"*int", jsonScalarKindNumber},
		{"int64", jsonScalarKindNumber},
		{"*float64", jsonScalarKindNumber},
		{"uint8", jsonScalarKindNumber},
		{"bool", jsonScalarKindBool},
		{"*bool", jsonScalarKindBool},
		{"ConfigVersion", jsonScalarKindNumber},
		{"*ConfigVersion", jsonScalarKindNumber},
		{"string", ""},
		{"*string", ""},
		{"Cadence", ""},
		{"[]int", ""},
		{"*[]int", ""},
		{"[]bool", ""},
		{"map[string]int", ""},
		{"json.RawMessage", ""},
		{"time.Time", ""},
		{"OptionalPeriodInDays", ""},
	}
	for _, c := range cases {
		if got := goScalarKind(c.field, aliases); got != c.want {
			t.Errorf("goScalarKind(%q) = %q, want %q", c.field, got, c.want)
		}
	}
}

// Only a numeric enum counts: its wire form is the integer the coercion has to
// produce. A string enum already arrives as a JSON string, and a struct that
// happens to declare enum values is not a scalar at all.
func TestNumericEnumBases(t *testing.T) {
	bases := numericEnumBases([]GoType{
		{Name: "RefreshRate", EnumValues: []GoEnumConst{{Literal: "60"}}, EnumBaseType: "int"},
		{Name: "Threshold", EnumValues: []GoEnumConst{{Literal: "1"}}, EnumBaseType: "int64"},
		{Name: "Cadence", EnumValues: []GoEnumConst{{Literal: `"All"`}}, EnumBaseType: "string"},
		{Name: "Unset", EnumValues: []GoEnumConst{{Literal: `"x"`}}},
		{Name: "Struct", EnumValues: []GoEnumConst{{Literal: "1"}}, EnumBaseType: "int", Fields: []GoField{{Name: "X"}}},
		{Name: "Plain"},
	})
	want := map[string]string{"RefreshRate": "int", "Threshold": "int64"}
	if len(bases) != len(want) {
		t.Fatalf("bases = %v, want %v", bases, want)
	}
	for name, base := range want {
		if bases[name] != base {
			t.Errorf("bases[%q] = %q, want %q", name, bases[name], base)
		}
	}
}

// The root is the discriminated union, not the twelve configurations under it,
// so the walk has to follow oneOf and then each variant's own properties —
// that is what makes a component added upstream inherit the tolerance with no
// config change.
func TestLenientScalarSeedFollowsAUnionToItsConfigurations(t *testing.T) {
	doc := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
		"Component": {Value: &openapi3.Schema{
			Type: types("object"),
			OneOf: openapi3.SchemaRefs{
				schemaRef("PasscodeSettingsComponent"),
			},
			Properties: openapi3.Schemas{"identifier": {Value: openapi3.NewStringSchema()}},
		}},
		"PasscodeSettingsComponent": {Value: &openapi3.Schema{
			Type:       types("object"),
			Properties: openapi3.Schemas{"configuration": schemaRef("PasscodeSettingsConfiguration")},
		}},
		"PasscodeSettingsConfiguration": {Value: &openapi3.Schema{
			Type:       types("object"),
			Properties: openapi3.Schemas{"MinimumLength": schemaRef("minimum_length")},
		}},
		"minimum_length": {Value: openapi3.NewObjectSchema()},
		"Unreferenced":   {Value: openapi3.NewObjectSchema()},
	}}}
	// The walker reads nested refs off the document, so the variant and
	// configuration refs above have to resolve through Components rather than
	// through the placeholder value schemaRef carries.
	doc.Components.Schemas["PasscodeSettingsComponent"].Value.Properties["configuration"].Value =
		doc.Components.Schemas["PasscodeSettingsConfiguration"].Value

	seed, err := lenientScalarSeed(doc, []string{"Component"})
	if err != nil {
		t.Fatalf("lenientScalarSeed: %v", err)
	}
	for _, want := range []string{"PasscodeSettingsComponent", "PasscodeSettingsConfiguration", "MinimumLength"} {
		if !seed[want] {
			t.Errorf("seed is missing %s; got %v", want, sortedKeys(seed))
		}
	}
	if seed["Unreferenced"] {
		t.Errorf("seed reached a schema no root references: %v", sortedKeys(seed))
	}
}

// A root that resolves to nothing removes the tolerance from its whole subtree
// with no other signal, which is exactly the silent regression this key exists
// to prevent — so an unknown name is a build failure, not a skip.
func TestLenientScalarSeedRefusesAnUnknownRoot(t *testing.T) {
	doc := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
		"Component": {Value: openapi3.NewObjectSchema()},
	}}}
	_, err := lenientScalarSeed(doc, []string{"Component", "Renamed"})
	if err == nil {
		t.Fatal("an unknown root should fail generation")
	}
	if !strings.Contains(err.Error(), "Renamed") {
		t.Errorf("error = %v, want it to name the missing root", err)
	}
	if strings.Contains(err.Error(), "Component") {
		t.Errorf("error = %v, want only the missing root named", err)
	}
}

// The seed is schema names; the tolerance is emitted against Go types. A type
// reached only through another type's field — a hoisted inline object, or one
// the seed named under a different spelling — has to come in through the
// closure or its scalars go untreated.
func TestLenientScalarTypesClosesOverFieldReferences(t *testing.T) {
	types := []GoType{
		{Name: "SoftwareUpdateSettingsConfiguration", Fields: []GoField{
			{Name: "Deferrals", Type: "*Deferrals", JSONTag: "Deferrals,omitempty"},
			{Name: "Version", Type: "int", JSONTag: "version"},
		}},
		{Name: "Deferrals", Fields: []GoField{
			{Name: "MajorPeriodInDays", Type: "*OptionalPeriodInDays", JSONTag: "MajorPeriodInDays,omitempty"},
		}},
		{Name: "OptionalPeriodInDays", Fields: []GoField{
			{Name: "Included", Type: "*bool", JSONTag: "Included,omitempty"},
			{Name: "Value", Type: "*int", JSONTag: "Value,omitempty"},
		}},
		{Name: "Elsewhere", Fields: []GoField{
			{Name: "Count", Type: "int", JSONTag: "count"},
		}},
	}
	got := lenientScalarTypes(types, map[string]bool{"SoftwareUpdateSettingsConfiguration": true})

	var names []string
	for _, e := range got {
		names = append(names, e.Name)
	}
	// Deferrals declares no scalar of its own, so it carries the closure
	// without getting a decoder.
	want := []string{"SoftwareUpdateSettingsConfiguration", "OptionalPeriodInDays"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("types = %v, want %v", names, want)
	}
	if keys := got[1].Keys; len(keys) != 2 ||
		keys[0].JSON != "Included" || keys[0].Kind != jsonScalarKindBool ||
		keys[1].JSON != "Value" || keys[1].Kind != jsonScalarKindNumber {
		t.Errorf("OptionalPeriodInDays keys = %+v, want Included/bool then Value/number", keys)
	}
}

// The wire name is what the rewrite matches on, so the options after it have
// to come off — and a field that travels under no key at all cannot be found
// by a rewrite keyed on one.
func TestJSONTagName(t *testing.T) {
	cases := map[string]string{
		"Value,omitempty": "Value",
		"Value":           "Value",
		"-":               "",
		"-,":              "-",
		",omitempty":      "",
	}
	for tag, want := range cases {
		if got := jsonTagName(tag); got != want {
			t.Errorf("jsonTagName(%q) = %q, want %q", tag, got, want)
		}
	}
}

// A root that resolves but reaches no scalar is a claim about a store that no
// longer needs it. Failing is what deletes the config entry.
func TestValidateLenientScalars(t *testing.T) {
	if err := validateLenientScalars("package blueprints", nil, nil); err != nil {
		t.Errorf("no roots and no entries should pass: %v", err)
	}
	if err := validateLenientScalars("package blueprints", []string{"Component"},
		[]lenientType{{Name: "T"}}); err != nil {
		t.Errorf("roots with entries should pass: %v", err)
	}
	err := validateLenientScalars("package blueprints", []string{"Component"}, nil)
	if err == nil {
		t.Fatal("roots that reach no scalar should fail generation")
	}
	if !strings.Contains(err.Error(), "Component") {
		t.Errorf("error = %v, want it to name the root", err)
	}
}
