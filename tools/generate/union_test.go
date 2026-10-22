// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func schemaRef(name string) *openapi3.SchemaRef {
	return &openapi3.SchemaRef{Ref: "#/components/schemas/" + name, Value: openapi3.NewObjectSchema()}
}

// isRefUnion decides whether a property's `oneOf` becomes a typed union or
// keeps its old behaviour, and the old behaviour for a property was a bare
// `any` — see validateNoUntypedFields. Each rejection below is a shape that
// must keep going down its existing path, so a too-eager match here is a
// silent re-typing of something else's field.
func TestIsRefUnion(t *testing.T) {
	tests := []struct {
		name   string
		schema *openapi3.Schema
		want   bool
	}{
		{
			// account-sso's ConnectionRequest.connection.
			name:   "two or more bare $refs",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{schemaRef("A"), schemaRef("B")}},
			want:   true,
		},
		{
			name:   "four bare $refs",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{schemaRef("A"), schemaRef("B"), schemaRef("C"), schemaRef("D")}},
			want:   true,
		},
		{
			// 3.0's $ref-with-siblings idiom. isSingleRefAllOfWrapper and the
			// nullable collapse own this shape.
			name:   "single member is not a union",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{schemaRef("A")}},
			want:   false,
		},
		{
			// normalizeNullableUnions has already rewritten this to a nullable
			// $ref by the time hoisting runs; matching it would resurrect the
			// union and give the field a pointless one-variant wrapper.
			name: "nullable oneOf is not a union",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				schemaRef("A"),
				{Value: &openapi3.Schema{Type: types("null")}},
			}},
			want: false,
		},
		{
			// No named type to point a variant pointer at.
			name: "an inline member disqualifies the whole union",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				schemaRef("A"),
				{Value: openapi3.NewObjectSchema()},
			}},
			want: false,
		},
		{
			// blueprints' SwUpdateConfiguration: type + oneOf + its own
			// properties. It goes down the object branch and must stay there.
			name: "explicit type disqualifies",
			schema: &openapi3.Schema{
				Type:  types("object"),
				OneOf: openapi3.SchemaRefs{schemaRef("A"), schemaRef("B")},
			},
			want: false,
		},
		{
			name: "own properties disqualify",
			schema: &openapi3.Schema{
				OneOf:      openapi3.SchemaRefs{schemaRef("A"), schemaRef("B")},
				Properties: openapi3.Schemas{"enforcementType": {Value: openapi3.NewStringSchema()}},
			},
			want: false,
		},
		{
			// A discriminated union is schemaToDiscriminatorType's job: it
			// produces a strictly better type, with dispatch on decode.
			name: "a discriminator disqualifies",
			schema: &openapi3.Schema{
				OneOf:         openapi3.SchemaRefs{schemaRef("A"), schemaRef("B")},
				Discriminator: &openapi3.Discriminator{PropertyName: "kind"},
			},
			want: false,
		},
		{
			name:   "no oneOf",
			schema: openapi3.NewObjectSchema(),
			want:   false,
		},
		{
			name:   "nil",
			schema: nil,
			want:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRefUnion(tc.schema); got != tc.want {
				t.Fatalf("isRefUnion = %v, want %v", got, tc.want)
			}
		})
	}
}

// The hoist is what gives a property-reached union a name at all. Without it
// the schema is never a component, extractTypes never sees it, and
// schemaRefToGoType answers `any` for the containing field.
func TestHoistInlineObjectsLiftsRefUnionProperty(t *testing.T) {
	doc := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
		"ConnectionRequest": {Value: &openapi3.Schema{
			Type: types("object"),
			Properties: openapi3.Schemas{
				"connection": {Value: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
					schemaRef("OidcConnectionSettings"),
					schemaRef("OktaConnectionSettings"),
				}}},
			},
		}},
		"OidcConnectionSettings": {Value: openapi3.NewObjectSchema()},
		"OktaConnectionSettings": {Value: openapi3.NewObjectSchema()},
	}}}

	hoistInlineObjects(doc, "json")

	hoisted, ok := doc.Components.Schemas["ConnectionRequestConnection"]
	if !ok {
		t.Fatal("union property was not hoisted into a named schema")
	}
	if !isRefUnion(hoisted.Value) {
		t.Fatal("hoisted schema is no longer a ref union")
	}
	prop := doc.Components.Schemas["ConnectionRequest"].Value.Properties["connection"]
	if prop.Ref != "#/components/schemas/ConnectionRequestConnection" {
		t.Fatalf("property $ref = %q, want the hoisted schema", prop.Ref)
	}
	if got := schemaRefToGoType(prop); got != "ConnectionRequestConnection" {
		t.Fatalf("field type = %q, want ConnectionRequestConnection (an %q here is the bug this fixes)", got, "any")
	}
}

// One pointer field per variant, named after the variant's own type, and the
// contract stated in the godoc — the spec's own description says only what the
// payload is, never that setting two is an error.
func TestSchemaToUnionType(t *testing.T) {
	schema := &openapi3.Schema{
		Description: "Provider settings, in the shape matching `connectionType`.",
		OneOf: openapi3.SchemaRefs{
			schemaRef("OidcConnectionSettings"),
			schemaRef("EntraConnectionSettings"),
			// A repeated $ref must not produce a duplicate field: the struct
			// would not compile.
			schemaRef("OidcConnectionSettings"),
		},
	}

	gt := schemaToUnionType("ConnectionRequestConnection", schema)
	if gt.Union == nil {
		t.Fatal("no union emitted")
	}
	want := []string{"OidcConnectionSettings", "EntraConnectionSettings"}
	if len(gt.Union.Variants) != len(want) {
		t.Fatalf("variants = %v, want %v", gt.Union.Variants, want)
	}
	for i, v := range gt.Union.Variants {
		if v.TypeName != want[i] || v.FieldName != want[i] {
			t.Errorf("variant %d = %+v, want type and field %q", i, v, want[i])
		}
	}
	if !strings.Contains(gt.Comment, "Provider settings") {
		t.Errorf("comment dropped the spec's description: %q", gt.Comment)
	}
	if !strings.Contains(gt.Comment, "Set exactly one variant pointer") {
		t.Errorf("comment does not state the contract: %q", gt.Comment)
	}
}

// A value the server produces and the spec omits is a broken validator, not a
// documentation gap: RegionValues() feeds stringvalidator.OneOf, so the
// consumer rejects the server's own output. Region gained RAMP this way.
func TestApplyEnumAdditions(t *testing.T) {
	doc := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
		"Region": {Value: &openapi3.Schema{Type: types("string"), Enum: []any{"US", "EU"}}},
	}}}

	applyEnumAdditions(doc, map[string][]string{"Region": {"RAMP"}})

	got := doc.Components.Schemas["Region"].Value.Enum
	if len(got) != 3 || got[2] != "RAMP" {
		t.Fatalf("enum = %v, want the addition appended after the spec's own values", got)
	}
}

// The entry has to expire when upstream publishes the value, and a duplicate
// enum member is invisible: two constants with the same value compile fine, so
// nothing else would ever notice.
func TestApplyEnumAdditionsPanicsWhenAlreadyDeclared(t *testing.T) {
	cases := map[string]openapi3.Schemas{
		"value already declared": {
			"Region": {Value: &openapi3.Schema{Type: types("string"), Enum: []any{"US", "RAMP"}}},
		},
		"schema declares no enum": {
			"Region": {Value: &openapi3.Schema{Type: types("string")}},
		},
		"no such schema": {},
	}
	for name, schemas := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("applyEnumAdditions returned without panicking")
				}
			}()
			applyEnumAdditions(&openapi3.T{Components: &openapi3.Components{Schemas: schemas}},
				map[string][]string{"Region": {"RAMP"}})
		})
	}
}

// A bare `any` field is the tell of a schema construct with no branch: it
// type-checks nothing and, the transport setting no DisallowUnknownFields,
// decodes anything. Container `any`s are what a genuinely freeform schema
// should produce and must not trip this.
func TestValidateNoUntypedFields(t *testing.T) {
	ok := []GoType{{Name: "T", Fields: []GoField{
		{Name: "Freeform", Type: "map[string]any", JSONTag: "freeform"},
		{Name: "Items", Type: "[]any", JSONTag: "items"},
		{Name: "Typed", Type: "*Settings", JSONTag: "typed"},
	}}}
	if err := validateNoUntypedFields("pkg", ok); err != nil {
		t.Fatalf("clean types rejected: %v", err)
	}

	for _, bad := range []string{"any", "*any"} {
		t.Run(bad, func(t *testing.T) {
			types := []GoType{{Name: "ConnectionRequest", Fields: []GoField{
				{Name: "Connection", Type: bad, JSONTag: "connection"},
			}}}
			err := validateNoUntypedFields("pkg", types)
			if err == nil {
				t.Fatalf("field typed %s was accepted", bad)
			}
			if !strings.Contains(err.Error(), "ConnectionRequest.Connection") {
				t.Errorf("error does not name the field: %v", err)
			}
		})
	}
}

// variantRef builds a `$ref` whose target pins prop to one enum value, the
// shape every SwUpdate variant uses.
func variantRef(name, prop, value string) *openapi3.SchemaRef {
	s := openapi3.NewObjectSchema()
	s.Properties = openapi3.Schemas{
		prop: {Value: &openapi3.Schema{Type: types("string"), Enum: []any{value}}},
	}
	s.Required = []string{prop}
	return &openapi3.SchemaRef{Ref: "#/components/schemas/" + name, Value: s}
}

// tagProp is the parent's own declaration of the discriminator property — an
// unconstrained string, which is what makes the schema eligible.
func tagProp(prop string) openapi3.Schemas {
	return openapi3.Schemas{prop: {Value: openapi3.NewStringSchema()}}
}

// blueprints' SwUpdateConfiguration declares `type: object`, a `oneOf` and the
// tag property, but no `discriminator` — so it emitted that one field and
// dropped the union whole, leaving a type through which no software-update
// configuration could be expressed at all. Its sibling
// SwUpdateAutomaticConfiguration declares the identical shape *with* a
// discriminator, so the omission is an authoring slip and the mapping is
// readable off the variants' own single-value enums.
func TestInferDiscriminator(t *testing.T) {
	schema := &openapi3.Schema{
		Type:       types("object"),
		Properties: tagProp("enforcementType"),
		OneOf: openapi3.SchemaRefs{
			variantRef("SwUpdateManualConfiguration", "enforcementType", "MANUAL"),
			variantRef("SwUpdateAutomaticConfiguration", "enforcementType", "AUTOMATIC"),
		},
	}

	d := inferDiscriminator("SwUpdateConfiguration", schema)
	if d == nil {
		t.Fatal("no discriminator inferred")
	}
	if d.PropertyName != "enforcementType" {
		t.Fatalf("PropertyName = %q, want enforcementType", d.PropertyName)
	}
	want := map[string]string{
		"MANUAL":    "#/components/schemas/SwUpdateManualConfiguration",
		"AUTOMATIC": "#/components/schemas/SwUpdateAutomaticConfiguration",
	}
	if len(d.Mapping) != len(want) {
		t.Fatalf("mapping = %v, want %v", d.Mapping, want)
	}
	for value, ref := range want {
		if d.Mapping[value].Ref != ref {
			t.Errorf("mapping[%q] = %q, want %q", value, d.Mapping[value].Ref, ref)
		}
	}
}

// The real SwUpdateConfiguration resolves AUTOMATIC one level down: the
// automatic variant declares only `strategy` of its own, and both of *its*
// variants pin enforcementType to AUTOMATIC. Requiring the nested members to
// agree is what makes following the `oneOf` safe.
func TestInferDiscriminatorResolvesThroughANestedOneOf(t *testing.T) {
	automatic := &openapi3.Schema{
		Type:       types("object"),
		Properties: tagProp("strategy"),
		OneOf: openapi3.SchemaRefs{
			variantRef("SwUpdateLatestConfiguration", "enforcementType", "AUTOMATIC"),
			variantRef("SwUpdateSemanticConfiguration", "enforcementType", "AUTOMATIC"),
		},
	}
	schema := &openapi3.Schema{
		Type:       types("object"),
		Properties: tagProp("enforcementType"),
		OneOf: openapi3.SchemaRefs{
			variantRef("SwUpdateManualConfiguration", "enforcementType", "MANUAL"),
			{Ref: "#/components/schemas/SwUpdateAutomaticConfiguration", Value: automatic},
		},
	}

	d := inferDiscriminator("SwUpdateConfiguration", schema)
	if d == nil {
		t.Fatal("no discriminator inferred through the nested oneOf")
	}
	if d.Mapping["AUTOMATIC"].Ref != "#/components/schemas/SwUpdateAutomaticConfiguration" {
		t.Fatalf("AUTOMATIC mapped to %q", d.Mapping["AUTOMATIC"].Ref)
	}

	// Members that disagree identify nothing, and picking either would route
	// half the payloads to the wrong type.
	automatic.OneOf[1] = variantRef("SwUpdateSemanticConfiguration", "enforcementType", "SOMETHING_ELSE")
	if d := inferDiscriminator("SwUpdateConfiguration", schema); d != nil {
		t.Fatalf("inferred %q from disagreeing nested members", d.PropertyName)
	}
}

// A wrong tag is worse than no tag: it routes a payload to the wrong variant in
// silence. Every rejection below is a shape where the property identifies
// nothing, or where the choice is not the generator's to make.
func TestInferDiscriminatorRefusals(t *testing.T) {
	tests := []struct {
		name   string
		schema *openapi3.Schema
	}{
		{
			// audit's AuditEnvelope: no properties of its own, so nothing is
			// eligible and the structural merge still applies.
			name: "no parent property to be the tag",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				variantRef("GatewayEnvelope", "auditSource", "api-gateway"),
				variantRef("ServiceEnvelope", "auditSource", "blueprints"),
			}},
		},
		{
			name: "a variant leaves the property open",
			schema: &openapi3.Schema{
				Type:       types("object"),
				Properties: tagProp("enforcementType"),
				OneOf: openapi3.SchemaRefs{
					variantRef("A", "enforcementType", "MANUAL"),
					{Ref: "#/components/schemas/B", Value: openapi3.NewObjectSchema()},
				},
			},
		},
		{
			name: "two variants claim the same value",
			schema: &openapi3.Schema{
				Type:       types("object"),
				Properties: tagProp("enforcementType"),
				OneOf: openapi3.SchemaRefs{
					variantRef("A", "enforcementType", "MANUAL"),
					variantRef("B", "enforcementType", "MANUAL"),
				},
			},
		},
		{
			name: "a variant is not a $ref, so it has no type to map to",
			schema: &openapi3.Schema{
				Type:       types("object"),
				Properties: tagProp("enforcementType"),
				OneOf: openapi3.SchemaRefs{
					variantRef("A", "enforcementType", "MANUAL"),
					{Value: variantRef("", "enforcementType", "AUTOMATIC").Value},
				},
			},
		},
		{
			name: "the property has more than one enum value on a variant",
			schema: &openapi3.Schema{
				Type:       types("object"),
				Properties: tagProp("enforcementType"),
				OneOf: openapi3.SchemaRefs{
					variantRef("A", "enforcementType", "MANUAL"),
					{Ref: "#/components/schemas/B", Value: &openapi3.Schema{
						Type: types("object"),
						Properties: openapi3.Schemas{"enforcementType": {Value: &openapi3.Schema{
							Type: types("string"), Enum: []any{"AUTOMATIC", "OTHER"},
						}}},
					}},
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if d := inferDiscriminator("Probe", tc.schema); d != nil {
				t.Fatalf("inferred %q from a shape that identifies nothing", d.PropertyName)
			}
		})
	}
}

// Two properties that both discriminate is refused rather than resolved by
// taking the first: either works on the wire, but they produce different Go
// field names and a different emitted enum, so the choice is visible and
// belongs to a human.
func TestInferDiscriminatorRefusesAnAmbiguousChoice(t *testing.T) {
	manual := variantRef("A", "enforcementType", "MANUAL")
	manual.Value.Properties["kind"] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{Type: types("string"), Enum: []any{"K1"}},
	}
	automatic := variantRef("B", "enforcementType", "AUTOMATIC")
	automatic.Value.Properties["kind"] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{Type: types("string"), Enum: []any{"K2"}},
	}
	schema := &openapi3.Schema{
		Type: types("object"),
		Properties: openapi3.Schemas{
			"enforcementType": {Value: openapi3.NewStringSchema()},
			"kind":            {Value: openapi3.NewStringSchema()},
		},
		OneOf: openapi3.SchemaRefs{manual, automatic},
	}

	if d := inferDiscriminator("Probe", schema); d != nil {
		t.Fatalf("inferred %q when two properties both discriminate", d.PropertyName)
	}
}

// unionSchema is the single gate both call sites go through: a declared
// discriminator wins, a missing one is inferred, and a content-addressed
// mapping is refused either way — "com.jamf.ddm.*" keys are routing
// identifiers, not Go-safe type tags.
func TestUnionSchemaRefusesContentAddressedMappings(t *testing.T) {
	schema := &openapi3.Schema{
		Type:       types("object"),
		Properties: tagProp("identifier"),
		OneOf: openapi3.SchemaRefs{
			variantRef("A", "identifier", "com.jamf.ddm.sw-updates"),
			variantRef("B", "identifier", "com.jamf.ddm.custom-declarations"),
		},
	}
	if u := unionSchema("Probe", schema); u != nil {
		t.Fatal("a content-addressed mapping was accepted as a discriminator")
	}
}

// A property whose inline `oneOf` carries a discriminator is hoisted like an
// undiscriminated one. isRefUnion refuses the discriminator on purpose, so the
// hoist condition is a different one and this pins it: left inline the field was
// `any`, which jpapi's MdmCommandRequest.commandData (v2508) was.
func TestHoistInlineObjectsLiftsDiscriminatedUnionProperty(t *testing.T) {
	doc := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
		"Request": {Value: &openapi3.Schema{
			Type: types("object"),
			Properties: openapi3.Schemas{
				"data": {Value: &openapi3.Schema{
					Discriminator: &openapi3.Discriminator{PropertyName: "kind"},
					OneOf:         openapi3.SchemaRefs{schemaRef("A"), schemaRef("B")},
				}},
			},
		}},
		"A": {Value: openapi3.NewObjectSchema()},
		"B": {Value: openapi3.NewObjectSchema()},
	}}}

	hoistInlineObjects(doc, "json")

	prop := doc.Components.Schemas["Request"].Value.Properties["data"]
	if prop.Ref != "#/components/schemas/RequestData" {
		t.Fatalf("property $ref = %q, want the hoisted schema", prop.Ref)
	}
	if got := schemaRefToGoType(prop); got != "RequestData" {
		t.Fatalf("field type = %q, want RequestData", got)
	}
	if unionSchema("RequestData", doc.Components.Schemas["RequestData"].Value) == nil {
		t.Fatal("hoisted schema is not built as a discriminated union")
	}
}

// A later allOf member that only annotates a property — an example, a nullable
// flag — must not replace the base's typed definition with a type-less one.
// DisableRemoteDesktopCommand narrows commandType this way, and the replacement
// came out as `any`. A member that does retype the property still wins.
func TestFlattenAllOfKeepsBaseTypeUnderAnnotationOnlyOverlay(t *testing.T) {
	typed := &openapi3.SchemaRef{Ref: "#/components/schemas/Kind", Value: openapi3.NewStringSchema()}
	base := &openapi3.Schema{Type: types("object"), Properties: openapi3.Schemas{"kind": typed}}
	annotate := &openapi3.Schema{Properties: openapi3.Schemas{"kind": {Value: &openapi3.Schema{Example: "X"}}}}
	props, _ := flattenAllOf(&openapi3.Schema{AllOf: openapi3.SchemaRefs{{Value: base}, {Value: annotate}}})
	if props["kind"] != typed {
		t.Fatalf("kind = %+v, want the base's typed definition", props["kind"])
	}

	retype := &openapi3.Schema{Properties: openapi3.Schemas{"kind": {Value: openapi3.NewIntegerSchema()}}}
	props, _ = flattenAllOf(&openapi3.Schema{AllOf: openapi3.SchemaRefs{{Value: base}, {Value: retype}}})
	if props["kind"] == typed {
		t.Fatal("an overlay that retypes the property must still win")
	}
}

// A discriminated union refuses a discriminator that names a variant which is
// nil, and a variant set beside a discriminator that selects another. Only an
// empty or unrecognised discriminator with nothing set marshals to the
// discriminator alone. The behaviour itself is executed by the generated
// unions_test.go in each package; this pins that the template emits the guards
// those tests exercise.
func TestDiscriminatedUnionRefusesMismatchedVariants(t *testing.T) {
	// registerDiscriminatorEnum writes to a package-level registry that extractTypes
	// owns for the duration of one spec; stand one up for this call.
	currentPropertyEnums = map[string]GoType{}
	t.Cleanup(func() { currentPropertyEnums = nil })
	gt := schemaToDiscriminatorType("Request", &openapi3.Schema{
		Discriminator: &openapi3.Discriminator{PropertyName: "kind", Mapping: map[string]openapi3.MappingRef{"A": {Ref: "#/components/schemas/A"}, "B": {Ref: "#/components/schemas/B"}}},
		OneOf:         openapi3.SchemaRefs{schemaRef("A"), schemaRef("B")},
	})
	var buf strings.Builder
	if err := sourceTmpl.Execute(&buf, GeneratedFile{Package: "x", Module: "m", Format: "json", Types: []GoType{gt}}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"if m.A == nil {",
		`kind %q set but A is nil`,
		"if len(set) > 1 {",
		`kind %q selects no variant, but %s set`,
		`map[string]string{"kind": m.Kind}`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("template output lacks %q:\n%s", want, out)
		}
	}
}

func TestResolveRequiredParams(t *testing.T) {
	m := GoMethod{QueryParams: []ExtraParam{{Spec: "filter", Go: "filter", Type: "string"}, {Spec: "sort", Go: "sort", Type: "[]string"}}}

	if err := resolveRequiredParams(&m, OperationDef{Pagination: "totalCount", RequiredParams: []string{"filter"}}); err != nil {
		t.Fatal(err)
	}
	if len(m.RequiredParams) != 1 || m.RequiredParams[0].Spec != "filter" {
		t.Fatalf("RequiredParams = %+v", m.RequiredParams)
	}

	refusals := map[string]struct {
		op   OperationDef
		want string
	}{
		"not paginated": {OperationDef{RequiredParams: []string{"filter"}}, "paginated operations only"},
		"unknown param": {OperationDef{Pagination: "totalCount", RequiredParams: []string{"nope"}}, "not one of the operation's configured query params"},
	}
	for name, tc := range refusals {
		err := resolveRequiredParams(&GoMethod{QueryParams: m.QueryParams}, tc.op)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, tc.want)
		}
	}

	// The spec has since declared it required: the config entry has expired.
	expired := GoMethod{QueryParams: []ExtraParam{{Spec: "filter", Go: "filter", Type: "string", AlwaysSend: true}}}
	if err := resolveRequiredParams(&expired, OperationDef{Pagination: "totalCount", RequiredParams: []string{"filter"}}); err == nil ||
		!strings.Contains(err.Error(), "now declares required") {
		t.Errorf("expired entry: err = %v", err)
	}
}

func TestApplyPrivilegeAlternatives(t *testing.T) {
	const conj = "Required privileges: a:execute, b:execute.\n// All of them are required, not alternatives."
	methods := []GoMethod{{Name: "Send", ScopedPrivileges: []string{"a:execute", "b:execute"}, Comment: conj}}

	if err := applyPrivilegeAlternatives(methods, []string{"Send"}); err != nil {
		t.Fatal(err)
	}
	if !methods[0].PrivilegeAlternatives || strings.Contains(methods[0].Comment, "All of them are required") ||
		!strings.Contains(methods[0].Comment, "Any one of them suffices") {
		t.Fatalf("not applied: %+v", methods[0])
	}

	one := []GoMethod{{Name: "Send", ScopedPrivileges: []string{"a:execute"}, Comment: "x"}}
	if err := applyPrivilegeAlternatives(one, []string{"Send"}); err == nil || !strings.Contains(err.Error(), "nothing to choose between") {
		t.Errorf("single privilege: err = %v", err)
	}
	if err := applyPrivilegeAlternatives(methods, []string{"Nope"}); err == nil || !strings.Contains(err.Error(), "names no emitted method") {
		t.Errorf("unknown method: err = %v", err)
	}
}

// isAnnotationOnly decides whether an allOf overlay may be skipped, so each
// clause is pinned on its own: an overlay carrying any one of these is a real
// redefinition and must win.
func TestIsAnnotationOnly(t *testing.T) {
	item := openapi3.NewStringSchema()
	cases := map[string]struct {
		ref  *openapi3.SchemaRef
		want bool
	}{
		"nil ref":          {nil, false},
		"nil value":        {&openapi3.SchemaRef{}, false},
		"$ref":             {&openapi3.SchemaRef{Ref: "#/components/schemas/X", Value: &openapi3.Schema{}}, false},
		"example only":     {&openapi3.SchemaRef{Value: &openapi3.Schema{Example: "x"}}, true},
		"description only": {&openapi3.SchemaRef{Value: &openapi3.Schema{Description: "d"}}, true},
		"type":             {&openapi3.SchemaRef{Value: openapi3.NewIntegerSchema()}, false},
		"format":           {&openapi3.SchemaRef{Value: &openapi3.Schema{Format: "date-time"}}, false},
		"enum":             {&openapi3.SchemaRef{Value: &openapi3.Schema{Enum: []any{"A"}}}, false},
		"items":            {&openapi3.SchemaRef{Value: &openapi3.Schema{Items: &openapi3.SchemaRef{Value: item}}}, false},
		"properties":       {&openapi3.SchemaRef{Value: &openapi3.Schema{Properties: openapi3.Schemas{"a": {Value: item}}}}, false},
		"allOf":            {&openapi3.SchemaRef{Value: &openapi3.Schema{AllOf: openapi3.SchemaRefs{{Value: item}}}}, false},
		"oneOf":            {&openapi3.SchemaRef{Value: &openapi3.Schema{OneOf: openapi3.SchemaRefs{{Value: item}}}}, false},
		"anyOf":            {&openapi3.SchemaRef{Value: &openapi3.Schema{AnyOf: openapi3.SchemaRefs{{Value: item}}}}, false},
		"additionalProps":  {&openapi3.SchemaRef{Value: &openapi3.Schema{AdditionalProperties: openapi3.AdditionalProperties{Schema: &openapi3.SchemaRef{Value: item}}}}, false},
		"nullable":         {&openapi3.SchemaRef{Value: &openapi3.Schema{Nullable: true}}, false},
		"writeOnly":        {&openapi3.SchemaRef{Value: &openapi3.Schema{WriteOnly: true}}, false},
		"readOnly":         {&openapi3.SchemaRef{Value: &openapi3.Schema{ReadOnly: true}}, false},
	}
	for name, tc := range cases {
		if got := isAnnotationOnly(tc.ref); got != tc.want {
			t.Errorf("%s: isAnnotationOnly = %v, want %v", name, got, tc.want)
		}
	}
}

// The discriminator is what licenses the new hoist arm. isInlineItemUnion
// accepts `type: object` where isRefUnion refuses any type, so a discriminator-less
// `{type: object, oneOf: [$ref, $ref]}` property differs only by that arm: without
// the conjunct it would be hoisted, and re-typed from the merge it falls to today.
func TestHoistInlineObjectsDiscriminatedArmIsNarrow(t *testing.T) {
	doc := &openapi3.T{Components: &openapi3.Components{Schemas: openapi3.Schemas{
		"Request": {Value: &openapi3.Schema{
			Type: types("object"),
			Properties: openapi3.Schemas{
				"data": {Value: &openapi3.Schema{
					Type:  types("object"),
					OneOf: openapi3.SchemaRefs{schemaRef("A"), schemaRef("B")},
				}},
			},
		}},
		"A": {Value: openapi3.NewObjectSchema()},
		"B": {Value: openapi3.NewObjectSchema()},
	}}}

	hoistInlineObjects(doc, "json")

	if prop := doc.Components.Schemas["Request"].Value.Properties["data"]; prop.Ref != "" {
		t.Fatalf("a discriminator-less typed oneOf was hoisted to %q", prop.Ref)
	}
}

// The godoc line applyPrivilegeAlternatives rewrites is emitted elsewhere. When
// it stops being emitted the method would carry both claims, so that must fail
// generation rather than be skipped.
func TestApplyPrivilegeAlternativesFailsWhenGodocLineIsGone(t *testing.T) {
	methods := []GoMethod{{Name: "Send", ScopedPrivileges: []string{"a:execute", "b:execute"}, Comment: "Required privileges: a:execute, b:execute."}}
	if err := applyPrivilegeAlternatives(methods, []string{"Send"}); err == nil || !strings.Contains(err.Error(), "has no") {
		t.Fatalf("err = %v, want a refusal naming the missing godoc line", err)
	}
}

// queryMissing is the negation of queryGuard, and every parameter type has its
// own branch. The one configured requiredParams entry is a string, so the other
// branches are exercised here and nowhere else.
func TestQueryMissingNegatesQueryGuard(t *testing.T) {
	for _, tc := range []struct {
		typ, missing string
	}{
		{"string", `p == ""`},
		{"bool", "!p"},
		{"int", "p == 0"},
		{"int64", "p == 0"},
		{"[]string", "len(p) == 0"},
	} {
		qp := ExtraParam{Go: "p", Type: tc.typ}
		got := funcMap["queryMissing"].(func(ExtraParam) string)(qp)
		if got != tc.missing {
			t.Errorf("%s: queryMissing = %q, want %q", tc.typ, got, tc.missing)
		}
		guard := funcMap["queryGuard"].(func(ExtraParam) string)(qp)
		if guard == got {
			t.Errorf("%s: queryGuard and queryMissing are both %q; one must be the negation of the other", tc.typ, got)
		}
	}
}
