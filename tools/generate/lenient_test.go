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

	seeds, err := lenientScalarSeeds(doc, []string{"Component"})
	if err != nil {
		t.Fatalf("lenientScalarSeeds: %v", err)
	}
	seed := seeds["Component"]
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
	_, err := lenientScalarSeeds(doc, []string{"Component", "Renamed"})
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
	got, _, err := lenientScalarTypes(types, map[string]bool{"SoftwareUpdateSettingsConfiguration": true})
	if err != nil {
		t.Fatalf("lenientScalarTypes: %v", err)
	}

	var names []string
	for _, e := range got {
		names = append(names, e.Name)
	}
	// Deferrals declares no scalar of its own and still gets a decoder: a
	// failure inside one of its children arrives naming only the child's own
	// type, and Deferrals is where the field name lives. Elsewhere is outside
	// the closure and gets nothing.
	want := []string{"SoftwareUpdateSettingsConfiguration", "Deferrals", "OptionalPeriodInDays"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("types = %v, want %v", names, want)
	}
	if keys := got[1].Keys; len(keys) != 0 {
		t.Errorf("Deferrals keys = %+v, want none of its own", keys)
	}
	if keys := got[2].Keys; len(keys) != 2 ||
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
	if err := validateLenientScalars("package blueprints", nil, lenientDiagnostics{}); err != nil {
		t.Errorf("no roots and no entries should pass: %v", err)
	}
	live := map[string][]lenientType{"Component": {{Name: "T"}}}
	if err := validateLenientScalars("package blueprints", live, lenientDiagnostics{}); err != nil {
		t.Errorf("a root with entries should pass: %v", err)
	}
	err := validateLenientScalars("package blueprints",
		map[string][]lenientType{"Component": nil}, lenientDiagnostics{})
	if err == nil {
		t.Fatal("a root that reaches no scalar should fail generation")
	}
	if !strings.Contains(err.Error(), "Component") {
		t.Errorf("error = %v, want it to name the root", err)
	}
}

// The guard is a claim about one root, so a sibling root that still has
// entries must not stand in for one that has lost every scalar. Aggregating
// first is what makes the second root's expiry unreachable.
func TestValidateLenientScalarsIsPerRoot(t *testing.T) {
	err := validateLenientScalars("package blueprints", map[string][]lenientType{
		"Component": {{Name: "OptionalPeriodInDays"}},
		"Withdrawn": nil,
	}, lenientDiagnostics{})
	if err == nil {
		t.Fatal("a root that reaches no scalar should fail even when a sibling root has entries")
	}
	if !strings.Contains(err.Error(), "Withdrawn") {
		t.Errorf("error = %v, want it to name the dead root", err)
	}
	if strings.Contains(err.Error(), "Component") {
		t.Errorf("error = %v, want only the dead root named", err)
	}
}

// The coercion does not reach into a slice or a map, and the justification for
// that is an observation about writers rather than a guarantee. An observation
// needs a tripwire, so the plan reports every such field instead of dropping
// it silently.
func TestLenientScalarTypesReportsSkippedComposites(t *testing.T) {
	types := []GoType{
		{Name: "Root", Fields: []GoField{
			{Name: "Count", Type: "int", JSONTag: "count"},
			{Name: "Values", Type: "[]int", JSONTag: "values"},
			{Name: "Flags", Type: "map[string]bool", JSONTag: "flags"},
			{Name: "Names", Type: "[]string", JSONTag: "names"},
		}},
	}
	_, diags, err := lenientScalarTypes(types, map[string]bool{"Root": true})
	if err != nil {
		t.Fatalf("lenientScalarTypes: %v", err)
	}
	joined := strings.Join(diags.SkippedComposites, " ")
	for _, want := range []string{"Root.values ([]int)", "Root.flags (map[string]bool)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("diagnostics = %v, want it to report %s", diags.SkippedComposites, want)
		}
	}
	if strings.Contains(joined, "names") {
		t.Errorf("diagnostics = %v, want no report for a slice of strings", diags.SkippedComposites)
	}
}

// A type the generator already emits an UnmarshalJSON for cannot also carry a
// lenient one: two methods on one type do not compile. A union in the middle
// of the closure is reported, because attribution stops there — and a union
// that declares a coerced scalar of its own fails generation, because there
// the tolerance itself is what would be lost.
func TestLenientScalarTypesAndTypesThatOwnAnUnmarshalJSON(t *testing.T) {
	union := GoType{Name: "SwUpdateConfiguration", Discriminator: &GoDiscriminator{PropertyName: "enforcementType"},
		Fields: []GoField{
			{Name: "EnforcementType", Type: "string", JSONTag: "enforcementType"},
			{Name: "AUTOMATIC", Type: "*Leaf", JSONTag: "-"},
		}}
	leaf := GoType{Name: "Leaf", Fields: []GoField{{Name: "Days", Type: "int", JSONTag: "enforceAfterDays"}}}

	entries, diags, err := lenientScalarTypes([]GoType{union, leaf},
		map[string]bool{"SwUpdateConfiguration": true})
	if err != nil {
		t.Fatalf("lenientScalarTypes: %v", err)
	}
	for _, e := range entries {
		if e.Name == "SwUpdateConfiguration" {
			t.Fatal("a discriminated union must not get a second UnmarshalJSON")
		}
	}
	if len(diags.OwnDecoder) != 1 || diags.OwnDecoder[0] != "SwUpdateConfiguration" {
		t.Errorf("OwnDecoder = %v, want the union reported", diags.OwnDecoder)
	}

	union.Fields = append(union.Fields, GoField{Name: "Retries", Type: "int", JSONTag: "retries"})
	_, _, err = lenientScalarTypes([]GoType{union, leaf}, map[string]bool{"SwUpdateConfiguration": true})
	if err == nil {
		t.Fatal("a union declaring a coerced scalar should fail generation")
	}
	if !strings.Contains(err.Error(), "SwUpdateConfiguration") {
		t.Errorf("error = %v, want it to name the type", err)
	}
}

// Every coerced key has to name a field the type declares. The generated
// equality test cannot see a wrong key: encoding/json ignores it on the bare
// and the quoted side alike, so both decodes land on the same zero value.
func TestLenientScalarTypesKeysAreRealFieldTags(t *testing.T) {
	types := []GoType{
		{Name: "Root", Fields: []GoField{
			{Name: "Count", Type: "int", JSONTag: "count,omitempty"},
			{Name: "Hidden", Type: "int", JSONTag: "-"},
			{Name: "Dashed", Type: "int", JSONTag: "-,"},
		}},
	}
	entries, _, err := lenientScalarTypes(types, map[string]bool{"Root": true})
	if err != nil {
		t.Fatalf("lenientScalarTypes: %v", err)
	}
	declared := map[string]bool{"count": true, "-": true}
	for _, e := range entries {
		for _, k := range e.Keys {
			if !declared[k.JSON] {
				t.Errorf("%s: coerced key %q names no field tag", e.Name, k.JSON)
			}
		}
	}
	if len(entries) != 1 || len(entries[0].Keys) != 2 {
		t.Fatalf("entries = %+v, want Root carrying count and the dashed key", entries)
	}
}

// A union hands the same bytes to the variant it selects, so the whole path
// renders as one flat object. The case exists because nothing else decodes
// through the dispatch: the per-type table builds each leaf directly.
func TestLenientUnionCasesFollowsANestedChain(t *testing.T) {
	types := []GoType{
		{Name: "SwUpdateConfiguration", Discriminator: &GoDiscriminator{
			PropertyName: "enforcementType",
			Variants: []GoDiscriminatorVariant{
				{Values: []string{"AUTOMATIC"}, TypeName: "SwUpdateAutomaticConfiguration", FieldName: "AUTOMATIC"},
			},
		}},
		{Name: "SwUpdateAutomaticConfiguration", Discriminator: &GoDiscriminator{
			PropertyName: "strategy",
			Variants: []GoDiscriminatorVariant{
				{Values: []string{"LATEST"}, TypeName: "SwUpdateLatestConfiguration", FieldName: "LATEST"},
			},
		}},
		{Name: "SwUpdateLatestConfiguration", Fields: []GoField{
			{Name: "EnforceAfterDays", Type: "int", JSONTag: "enforceAfterDays"},
		}},
	}
	entries := []lenientType{{Name: "SwUpdateLatestConfiguration",
		Keys: []lenientKey{{JSON: "enforceAfterDays", Kind: jsonScalarKindNumber}}}}

	cases := lenientUnionCases(types, entries)
	// Two entry points: the outer union, carrying both discriminators, and the
	// inner one on its own, which a caller can also decode into directly.
	if len(cases) != 2 {
		t.Fatalf("cases = %+v, want the outer chain and the inner union", cases)
	}
	byUnion := make(map[string]lenientUnionCase, len(cases))
	for _, c := range cases {
		byUnion[c.UnionType] = c
	}
	outer, ok := byUnion["SwUpdateConfiguration"]
	if !ok {
		t.Fatalf("cases = %+v, want one decoding into the outer union", cases)
	}
	if outer.ScalarJSON != "enforceAfterDays" {
		t.Errorf("ScalarJSON = %q, want the leaf's coerced key", outer.ScalarJSON)
	}
	if len(outer.Discrim) != 2 ||
		outer.Discrim[0] != (lenientDiscrimValue{JSON: "enforcementType", Value: "AUTOMATIC"}) ||
		outer.Discrim[1] != (lenientDiscrimValue{JSON: "strategy", Value: "LATEST"}) {
		t.Errorf("Discrim = %+v, want both discriminators on the path", outer.Discrim)
	}
	inner, ok := byUnion["SwUpdateAutomaticConfiguration"]
	if !ok {
		t.Fatalf("cases = %+v, want one decoding into the inner union", cases)
	}
	if len(inner.Discrim) != 1 || inner.Discrim[0].JSON != "strategy" {
		t.Errorf("inner Discrim = %+v, want only its own discriminator", inner.Discrim)
	}
}

// The parent names the field a child decoder failed on, so the case has to
// pair a parent with a child that actually carries a coerced number.
func TestLenientParentCasesPairsAParentWithItsChild(t *testing.T) {
	types := []GoType{
		{Name: "Deferrals", Fields: []GoField{
			{Name: "MajorPeriodInDays", Type: "*OptionalPeriodInDays", JSONTag: "MajorPeriodInDays,omitempty"},
			{Name: "MinorPeriodInDays", Type: "*OptionalPeriodInDays", JSONTag: "MinorPeriodInDays,omitempty"},
		}},
		{Name: "OptionalPeriodInDays", Fields: []GoField{{Name: "Value", Type: "*int", JSONTag: "Value,omitempty"}}},
	}
	entries := []lenientType{
		{Name: "Deferrals"},
		{Name: "OptionalPeriodInDays", Keys: []lenientKey{{JSON: "Value", Kind: jsonScalarKindNumber}}},
	}
	cases := lenientParentCases(types, entries)
	if len(cases) != 1 {
		t.Fatalf("cases = %+v, want one parent case", cases)
	}
	if cases[0].Name != "Deferrals" || cases[0].ChildJSON != "MajorPeriodInDays" || cases[0].ChildKey != "Value" {
		t.Errorf("case = %+v, want Deferrals.MajorPeriodInDays/Value", cases[0])
	}
}
