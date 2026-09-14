// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"maps"
	"slices"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func types(names ...string) *openapi3.Types {
	t := openapi3.Types(names)
	return &t
}

// A single-member allOf is OpenAPI 3.0's only way to hang a description,
// `nullable` or an `example` off a $ref. Before this collapse the wrapper fell
// through schemaRefToGoType's default branch to `any`, which decodes anything
// and so never fails a test — the failure mode this exercises is silence.
func TestSchemaRefToGoTypeCollapsesSingleRefAllOf(t *testing.T) {
	target := &openapi3.SchemaRef{Ref: "#/components/schemas/DeploymentRun", Value: openapi3.NewObjectSchema()}

	tests := []struct {
		name   string
		schema *openapi3.Schema
		want   string
	}{
		{
			name:   "nullable wrapper collapses to the referenced type",
			schema: &openapi3.Schema{Nullable: true, Description: "d", AllOf: openapi3.SchemaRefs{target}},
			want:   "DeploymentRun",
		},
		{
			name:   "bare wrapper collapses",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{target}},
			want:   "DeploymentRun",
		},
		{
			// Two members are a real composition: no single Go type names it,
			// and picking either one would silently discard the other's fields.
			name:   "multi-member allOf stays any",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{target, target}},
			want:   "any",
		},
		{
			// allOf + own properties is the extend-a-base-schema shape
			// (PolicyDetail over PolicySummary). extractTypes flattens those
			// into their own struct, so collapsing here would drop the
			// extension's fields.
			name: "allOf with own properties stays any",
			schema: &openapi3.Schema{
				AllOf:      openapi3.SchemaRefs{target},
				Properties: openapi3.Schemas{"extra": {Value: openapi3.NewStringSchema()}},
			},
			want: "any",
		},
		{
			// An explicit type means the schema asserts its own shape; the
			// existing object branch answers map[string]any and must win.
			name:   "explicit object type is not collapsed",
			schema: &openapi3.Schema{Type: types("object"), AllOf: openapi3.SchemaRefs{target}},
			want:   "map[string]any",
		},
		{
			name: "allOf beside additionalProperties stays any",
			schema: &openapi3.Schema{
				AllOf: openapi3.SchemaRefs{target},
				AdditionalProperties: openapi3.AdditionalProperties{
					Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()},
				},
			},
			want: "any",
		},
		{
			name:   "allOf beside an enum stays any",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{target}, Enum: []any{"A"}},
			want:   "any",
		},
		{
			name:   "allOf beside a oneOf stays any",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{target}, OneOf: openapi3.SchemaRefs{target}},
			want:   "any",
		},
		{
			// Nesting is legal in 3.0 and the inner wrapper is the same idiom.
			name: "nested wrappers collapse through",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				{Value: &openapi3.Schema{Nullable: true, AllOf: openapi3.SchemaRefs{target}}},
			}},
			want: "DeploymentRun",
		},
		{
			// A wrapper around an inline scalar still has a Go type to name.
			name: "wrapper around an inline scalar collapses",
			schema: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
				{Value: openapi3.NewStringSchema()},
			}},
			want: "string",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := schemaRefToGoType(&openapi3.SchemaRef{Value: tc.schema}); got != tc.want {
				t.Fatalf("schemaRefToGoType = %q, want %q", got, tc.want)
			}
		})
	}
}

// The collapse must never fire for a named component: a $ref returns the
// schema's own name before the switch is reached, so a component declared as a
// single-member allOf keeps emitting a type of its own.
func TestSchemaRefToGoTypeKeepsNamedComponentName(t *testing.T) {
	ref := &openapi3.SchemaRef{
		Ref: "#/components/schemas/PolicyDetail",
		Value: &openapi3.Schema{AllOf: openapi3.SchemaRefs{
			{Ref: "#/components/schemas/PolicySummary", Value: openapi3.NewObjectSchema()},
		}},
	}
	if got := schemaRefToGoType(ref); got != "PolicyDetail" {
		t.Fatalf("schemaRefToGoType = %q, want PolicyDetail", got)
	}
}

// The wrapper is inline, so `nullable` stays on the wrapper and the shared
// component the reference points at is never mutated. Collapsing by setting
// Nullable on the target — the way collapseNullableOneOf does for its $ref
// branch — would mark that component nullable for every field referencing it.
func TestSingleRefAllOfCollapseDoesNotMutateTarget(t *testing.T) {
	shared := openapi3.NewObjectSchema()
	wrapper := &openapi3.SchemaRef{Value: &openapi3.Schema{
		Nullable: true,
		AllOf:    openapi3.SchemaRefs{{Ref: "#/components/schemas/DeploymentRun", Value: shared}},
	}}
	if got := schemaRefToGoType(wrapper); got != "DeploymentRun" {
		t.Fatalf("schemaRefToGoType = %q, want DeploymentRun", got)
	}
	if shared.Nullable {
		t.Fatal("collapse marked the shared referenced component nullable")
	}
}

// A discriminator mapping may point several wire values at one variant schema.
// uem-connect does: nine UEM vendors share the generic ConnectorCreateRequest
// and only JAMF_PRO gets its own type. Deduping by Go type — which is right for
// the struct field, since nine identical pointers would be nonsense — used to
// drop the other eight values entirely, and with them their cases in the
// generated marshal switch. The failure mode is silence: a caller setting
// Vendor to one of the dropped values marshals to `{"vendor":"INTUNE"}` with
// every other field gone, and no error at any layer.
func TestSchemaToDiscriminatorTypeGroupsSharedVariants(t *testing.T) {
	ref := func(name string) *openapi3.SchemaRef {
		return &openapi3.SchemaRef{Ref: "#/components/schemas/" + name}
	}
	schema := &openapi3.Schema{
		OneOf: openapi3.SchemaRefs{ref("JamfProConnectorCreateRequest"), ref("ConnectorCreateRequest")},
		Discriminator: &openapi3.Discriminator{
			PropertyName: "vendor",
			Mapping: map[string]openapi3.MappingRef{
				"JAMF_PRO":    {Ref: "#/components/schemas/JamfProConnectorCreateRequest"},
				"INTUNE":      {Ref: "#/components/schemas/ConnectorCreateRequest"},
				"AIRWATCH":    {Ref: "#/components/schemas/ConnectorCreateRequest"},
				"JAMF_SCHOOL": {Ref: "#/components/schemas/ConnectorCreateRequest"},
			},
		},
	}

	// schemaToDiscriminatorType registers the discriminator's enum into the
	// per-spec property-enum registry, which a real run initialises.
	currentPropertyEnums = map[string]GoType{}
	currentSpecTypeNames = map[string]bool{}
	t.Cleanup(func() { currentPropertyEnums, currentSpecTypeNames = nil, nil })

	gt := schemaToDiscriminatorType("ConnectorCreateRequestBody", schema)
	if gt.Discriminator == nil {
		t.Fatal("no discriminator emitted")
	}
	if got := len(gt.Discriminator.Variants); got != 2 {
		t.Fatalf("variants = %d, want 2 (one field per Go type)", got)
	}

	byType := map[string]GoDiscriminatorVariant{}
	for _, v := range gt.Discriminator.Variants {
		byType[v.TypeName] = v
	}

	// Every mapped value must survive as a case, or it marshals to nothing.
	generic, ok := byType["ConnectorCreateRequest"]
	if !ok {
		t.Fatal("no variant for ConnectorCreateRequest")
	}
	wantValues := map[string]bool{"AIRWATCH": true, "INTUNE": true, "JAMF_SCHOOL": true}
	if len(generic.Values) != len(wantValues) {
		t.Fatalf("generic variant values = %v, want the three sharing it", generic.Values)
	}
	for _, v := range generic.Values {
		if !wantValues[v] {
			t.Errorf("unexpected value %q on the generic variant", v)
		}
	}
	// Naming the shared field after one arbitrary member implies the variant is
	// only reachable through it, so it takes the schema name instead.
	if generic.FieldName != "ConnectorCreateRequest" {
		t.Errorf("shared variant field = %q, want the schema name ConnectorCreateRequest", generic.FieldName)
	}

	// A single-value variant keeps the value-derived name, so existing unions
	// (pro's MobileDeviceResponse, blueprints' BookmarkItem) do not churn.
	jamfPro, ok := byType["JamfProConnectorCreateRequest"]
	if !ok {
		t.Fatal("no variant for JamfProConnectorCreateRequest")
	}
	if len(jamfPro.Values) != 1 || jamfPro.Values[0] != "JAMF_PRO" {
		t.Errorf("JAMF_PRO variant values = %v, want exactly [JAMF_PRO]", jamfPro.Values)
	}
	if jamfPro.FieldName != exportedGoName("JAMF_PRO") {
		t.Errorf("single-value variant field = %q, want %q", jamfPro.FieldName, exportedGoName("JAMF_PRO"))
	}

	// The mapping is the only complete list of accepted values once a spec moves
	// a value out of a variant's own enum, so the union has to carry constants
	// for all of them — JAMF_PRO included, which ConnectorCreateRequest.vendor
	// no longer declares.
	if gt.Discriminator.EnumTypeName != "ConnectorCreateRequestBodyVendor" {
		t.Fatalf("EnumTypeName = %q, want ConnectorCreateRequestBodyVendor", gt.Discriminator.EnumTypeName)
	}
	enum, ok := currentPropertyEnums["ConnectorCreateRequestBodyVendor"]
	if !ok {
		t.Fatal("discriminator enum was not registered")
	}
	got := map[string]bool{}
	for _, c := range enum.EnumValues {
		got[c.Value] = true
	}
	for _, want := range []string{"JAMF_PRO", "INTUNE", "AIRWATCH", "JAMF_SCHOOL"} {
		if !got[want] {
			t.Errorf("discriminator enum missing %q", want)
		}
	}
}

// pro's GET /inventory-preload declares two content types that disagree:
// text/csv carries the pagination envelope directly, while application/json
// carries an *array of* that envelope. The wire sends the bare envelope
// (probed 2026-08-31), so the envelope form is the correct reading.
//
// Two failures are pinned here. Iterating the Content map directly made the
// answer depend on Go's randomised map order, so the same config generated
// either element type from run to run — and the array branch winning at all
// produced a list method returning []InventoryPreloadRecordSearchResults, one
// level of nesting off, which compiles and decodes into empty structs.
func TestDetectPaginatedItemTypePrefersEnvelopeOverArrayOfEnvelope(t *testing.T) {
	envelope := openapi3.NewObjectSchema()
	envelope.Properties = openapi3.Schemas{
		"totalCount": {Value: openapi3.NewIntegerSchema()},
		"results": {Value: &openapi3.Schema{
			Type:  types("array"),
			Items: &openapi3.SchemaRef{Ref: "#/components/schemas/InventoryPreloadRecord", Value: openapi3.NewObjectSchema()},
		}},
	}
	envelopeRef := &openapi3.SchemaRef{Ref: "#/components/schemas/InventoryPreloadRecordSearchResults", Value: envelope}

	resp := openapi3.NewResponse().WithDescription("OK")
	resp.Content = openapi3.Content{
		// Alphabetically first, and the mis-declared one — so a fix that only
		// sorted the keys without preferring the envelope would still fail.
		"application/json": &openapi3.MediaType{Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type:  types("array"),
			Items: envelopeRef,
		}}},
		"text/csv": &openapi3.MediaType{Schema: envelopeRef},
	}
	op := &openapi3.Operation{Responses: openapi3.NewResponses(openapi3.WithStatus(200, &openapi3.ResponseRef{Value: resp}))}

	// Run repeatedly: a single pass can pick the right answer by luck when the
	// selection depends on map order.
	for i := range 50 {
		if got := detectPaginatedItemType(op, ""); got != "InventoryPreloadRecord" {
			t.Fatalf("iteration %d: detectPaginatedItemType = %q, want InventoryPreloadRecord", i, got)
		}
	}
}

// The raw-array fallback must survive the envelope-first reordering: pagination
// style "rawArray" exists for endpoints that really do return a bare array.
func TestDetectPaginatedItemTypeStillHandlesRawArray(t *testing.T) {
	resp := openapi3.NewResponse().WithDescription("OK")
	resp.Content = openapi3.Content{
		"application/json": &openapi3.MediaType{Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type:  types("array"),
			Items: &openapi3.SchemaRef{Ref: "#/components/schemas/SiteObject", Value: openapi3.NewObjectSchema()},
		}}},
	}
	op := &openapi3.Operation{Responses: openapi3.NewResponses(openapi3.WithStatus(200, &openapi3.ResponseRef{Value: resp}))}

	if got := detectPaginatedItemType(op, ""); got != "SiteObject" {
		t.Fatalf("detectPaginatedItemType = %q, want SiteObject", got)
	}
}

// A whitelist that stops reaching a read schema must also stop that schema
// naming the nested types it shares with its *_post sibling. applyPostSymmetry
// makes the two share the very SchemaRef being lifted, and hoistInlineObjects
// names a lift after whichever parent it reaches first in sorted order — so
// while an unreachable `computer` sits in the document it keeps winning
// `computer_general` over `computer_post`'s claim to it.
//
// That mattered because publishSpecs prunes before writing api/*.json while Go
// generation read testing/*.json unpruned: dropping GET /computers/id/{id}
// from the whitelist made the two inputs hoist different names, and CI —
// which generates from api/ — failed `git diff --exit-code -- jamfplatform/`
// on a rename nothing in config.json mentions. pruneUnreferencedSchemas
// between the two passes is what makes the inputs identical by construction,
// and this test fails if it is moved before applyPostSymmetry (the post type
// loses the inherited section) or after hoistInlineObjects (the stale name
// comes back).
func TestPruneUnreferencedSchemasRunsBeforeHoistNaming(t *testing.T) {
	// general.remote_management is declared inline on the read schema only;
	// computer_post inherits it through post-symmetry, exactly as Classic's
	// spec does.
	newDoc := func() *openapi3.T {
		remote := openapi3.NewObjectSchema()
		remote.Properties = openapi3.Schemas{
			"managed": {Value: openapi3.NewBoolSchema()},
		}
		general := openapi3.NewObjectSchema()
		general.Properties = openapi3.Schemas{
			"name":              {Value: openapi3.NewStringSchema()},
			"remote_management": {Value: remote},
		}
		read := openapi3.NewObjectSchema()
		read.Properties = openapi3.Schemas{"general": {Value: general}}

		return &openapi3.T{
			Paths: openapi3.NewPaths(),
			Components: &openapi3.Components{
				Schemas: openapi3.Schemas{
					"computer":      {Value: read},
					"computer_post": {Value: openapi3.NewObjectSchema()},
				},
			},
		}
	}

	// Only the POST is whitelisted, and it names its body through config —
	// which is how every Classic write operation is declared.
	spec := SpecDef{
		Format: "xml",
		Operations: []OperationDef{
			{Op: "POST /computers/id/{id}", Name: "CreateComputerByID", RequestType: "computer_post"},
		},
	}

	doc := newDoc()
	applyPostSymmetry(doc, nil)
	pruneUnreferencedSchemas(doc, spec)
	hoistInlineObjects(doc, spec.Format)

	if _, ok := doc.Components.Schemas["computer"]; ok {
		t.Error("computer is unreachable from the whitelist but survived the prune")
	}
	if _, ok := doc.Components.Schemas["computer_postGeneral"]; !ok {
		t.Errorf("want computer_postGeneral hoisted; schemas = %v", sortedKeys(doc.Components.Schemas))
	}
	if _, ok := doc.Components.Schemas["computerGeneral"]; ok {
		t.Error("computerGeneral was hoisted from a schema the whitelist no longer reaches")
	}
	if _, ok := doc.Components.Schemas["computer_postGeneralRemoteManagement"]; !ok {
		t.Errorf("want computer_postGeneralRemoteManagement hoisted; schemas = %v", sortedKeys(doc.Components.Schemas))
	}

	// The section itself must still be there: pruning before post-symmetry
	// would have stripped it off the post type along with its read sibling.
	post := doc.Components.Schemas["computer_post"].Value
	if _, ok := post.Properties["general"]; !ok {
		t.Fatal("computer_post lost the inherited general section — prune ran before post-symmetry")
	}
}

// objectVariant builds a `type: object` schema with the named string
// properties, of which those in required are required.
func objectVariant(properties []string, required ...string) *openapi3.SchemaRef {
	s := openapi3.NewObjectSchema()
	for _, p := range properties {
		s.Properties[p] = &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}
	}
	s.Required = required
	return &openapi3.SchemaRef{Value: s}
}

// mergeOneOfVariants collapses a structurally discriminated union — a
// discriminator-less `oneOf` with no properties of its own — into one struct,
// and the whole correctness of that struct is in its required list. The
// required set is *intersected*: a field only some variants require has to
// become optional, or the generated struct claims a presence guarantee no
// single variant makes and audit's AuditEnvelope (gateway events carry
// actor+requestContext, service events carry data) marshals a lie. Getting it
// backwards — a union rather than an intersection — produces a struct that
// still compiles, still round-trips in the generated test, and is wrong only
// in which fields a caller may leave nil.
func TestMergeOneOfVariants(t *testing.T) {
	tests := []struct {
		name         string
		schema       *openapi3.Schema
		wantProps    []string
		wantRequired []string
	}{
		{
			// Required by every variant: the merged struct can promise it.
			name: "required by all variants stays required",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				objectVariant([]string{"id", "actor"}, "id"),
				objectVariant([]string{"id", "data"}, "id"),
			}},
			wantProps:    []string{"actor", "data", "id"},
			wantRequired: []string{"id"},
		},
		{
			// Required by one variant only: optional in the merge, or the
			// service-event variant is unrepresentable.
			name: "required by some variants drops to optional",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				objectVariant([]string{"id", "actor"}, "id", "actor"),
				objectVariant([]string{"id", "data"}, "id"),
			}},
			wantProps:    []string{"actor", "data", "id"},
			wantRequired: []string{"id"},
		},
		{
			name: "no variant requires anything",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				objectVariant([]string{"actor"}),
				objectVariant([]string{"data"}),
			}},
			wantProps:    []string{"actor", "data"},
			wantRequired: []string{},
		},
		{
			// The union root's own required list is not subject to the
			// intersection — nothing about a variant can make a root-declared
			// requirement optional. auditSource is the live shape: declared on
			// the root alongside the oneOf, required there, absent from both
			// variants' required lists.
			name: "root required field survives the intersection",
			schema: func() *openapi3.Schema {
				s := openapi3.NewObjectSchema()
				s.Properties["auditSource"] = &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}
				s.Required = []string{"auditSource"}
				s.OneOf = openapi3.SchemaRefs{
					objectVariant([]string{"actor"}, "actor"),
					objectVariant([]string{"data"}),
				}
				return s
			}(),
			wantProps:    []string{"actor", "auditSource", "data"},
			wantRequired: []string{"auditSource"},
		},
		{
			// Root-required *and* declared by one variant that does not
			// require it: still required, and listed once.
			name: "root required field declared by a variant is not duplicated",
			schema: func() *openapi3.Schema {
				s := openapi3.NewObjectSchema()
				s.Properties["id"] = &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}
				s.Required = []string{"id"}
				s.OneOf = openapi3.SchemaRefs{
					objectVariant([]string{"id", "actor"}, "id"),
					objectVariant([]string{"data"}),
				}
				return s
			}(),
			wantProps:    []string{"actor", "data", "id"},
			wantRequired: []string{"id"},
		},
		{
			// A nil or unresolved variant is skipped, and — the part that is
			// easy to get wrong — it does not count toward the number of
			// variants a field must be required by. Counting it would silently
			// demote every required field in the union.
			name: "nil variants are skipped without raising the intersection bar",
			schema: &openapi3.Schema{OneOf: openapi3.SchemaRefs{
				nil,
				{Value: nil},
				objectVariant([]string{"id"}, "id"),
			}},
			wantProps:    []string{"id"},
			wantRequired: []string{"id"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			merged := mergeOneOfVariants(tc.schema)

			got := slices.Sorted(maps.Keys(merged.Properties))
			if !slices.Equal(got, tc.wantProps) {
				t.Errorf("properties = %v, want %v", got, tc.wantProps)
			}
			// mergeOneOfVariants sorts its own output, so compare as-is: an
			// unsorted result is a bug in its own right (the field order of
			// every generated struct would follow Go's map iteration).
			gotRequired := merged.Required
			if len(gotRequired) == 0 {
				gotRequired = []string{}
			}
			if !slices.Equal(gotRequired, tc.wantRequired) {
				t.Errorf("required = %v, want %v", gotRequired, tc.wantRequired)
			}
		})
	}
}

// A removal must take the property's `required` entry with it. Leaving the name
// behind publishes an api/*.json declaring a required property the schema does
// not have, which is an invalid spec a consumer reads. The live case is
// AccountPreferencesV6.showDirectoryGroupUuidColumn, which v2154 added as
// required against a server (11.31.1) that rejects the field outright —
// removing the property while leaving it required would have shipped that
// contradiction to consumers.
func TestApplyPropertyRemovalsAlsoDropsTheRequiredEntry(t *testing.T) {
	schema := openapi3.NewObjectSchema()
	schema.Properties = map[string]*openapi3.SchemaRef{
		"keep":      {Value: openapi3.NewStringSchema()},
		"phantom":   {Value: openapi3.NewBoolSchema()},
		"keepInReq": {Value: openapi3.NewStringSchema()},
	}
	schema.Required = []string{"keep", "phantom", "keepInReq"}

	doc := &openapi3.T{Components: &openapi3.Components{Schemas: map[string]*openapi3.SchemaRef{
		"Prefs": {Value: schema},
	}}}

	applyPropertyRemovals(doc, map[string][]string{"Prefs": {"phantom"}})

	got := doc.Components.Schemas["Prefs"].Value
	if _, still := got.Properties["phantom"]; still {
		t.Error("phantom property survived the removal")
	}
	if slices.Contains(got.Required, "phantom") {
		t.Errorf("required still names the removed property: %v", got.Required)
	}
	if want := []string{"keep", "keepInReq"}; !slices.Equal(got.Required, want) {
		t.Errorf("required = %v, want %v — the other entries must survive in order", got.Required, want)
	}
	if _, ok := got.Properties["keep"]; !ok {
		t.Error("removal took an unrelated property with it")
	}
}

// A rename must carry the property's `required` entry with it, for the reason
// applyPropertyRemovals prunes one: a required name with no property is an
// invalid spec a consumer reads. Latent in every committed propertyRenames
// entry today — none targets a required property — which is how the removal
// side stayed latent until v2154 removed its first required one.
func TestApplyPropertyRenamesCarriesTheRequiredEntry(t *testing.T) {
	schema := openapi3.NewObjectSchema()
	schema.Properties = map[string]*openapi3.SchemaRef{
		"keep": {Value: openapi3.NewStringSchema()},
		"old":  {Value: openapi3.NewStringSchema()},
	}
	schema.Required = []string{"keep", "old"}

	doc := &openapi3.T{Components: &openapi3.Components{Schemas: map[string]*openapi3.SchemaRef{
		"Prefs": {Value: schema},
	}}}

	applyPropertyRenames(doc, map[string]map[string]string{"Prefs": {"old": "new"}})

	got := doc.Components.Schemas["Prefs"].Value
	if _, still := got.Properties["old"]; still {
		t.Error("the old property survived the rename")
	}
	if _, ok := got.Properties["new"]; !ok {
		t.Fatal("the renamed property is missing")
	}
	if want := []string{"keep", "new"}; !slices.Equal(got.Required, want) {
		t.Errorf("required = %v, want %v — the entry must follow the rename, in place", got.Required, want)
	}
}

// A rename must reach the spec's own examples, or api/*.json publishes a schema
// declaring one key beside an example showing the other — the same
// self-inconsistency applyPropertyRemovals used to publish by leaving a stale
// `required` entry behind. And it must reach ONLY the matching ones: a property
// name is not unique across a spec, so a blind key rewrite would corrupt an
// example of an unrelated type that happens to share a key.
func TestApplyPropertyRenamesRewritesMatchingExamplesOnly(t *testing.T) {
	target := openapi3.NewObjectSchema()
	target.WithProperty("assignedConnection", openapi3.NewStringSchema())
	target.WithProperty("authRegion", openapi3.NewStringSchema())
	target.Required = []string{"assignedConnection", "authRegion"}

	// The example that must be rewritten: every key is a property of the
	// renamed schema.
	match := map[string]any{"assignedConnection": "con_1", "authRegion": "US"}
	// Same key, different type: `authRegion` sits beside a key the target
	// schema does not declare, so this is some other schema's example and must
	// be left exactly as it is.
	other := map[string]any{"authRegion": "US", "unrelatedField": 7}
	// Nested inside an array inside an object, which is where the real one
	// lives (DomainAllocation.connections[]).
	nested := map[string]any{"connections": []any{match}}

	resp := openapi3.NewResponse().WithContent(openapi3.Content{
		"application/json": {Example: nested},
	})
	otherResp := openapi3.NewResponse().WithContent(openapi3.Content{
		"application/json": {Example: other},
	})
	responses := openapi3.NewResponses()
	responses.Set("200", &openapi3.ResponseRef{Value: resp})
	otherResponses := openapi3.NewResponses()
	otherResponses.Set("200", &openapi3.ResponseRef{Value: otherResp})

	paths := openapi3.NewPaths()
	paths.Set("/allocation", &openapi3.PathItem{
		Get: &openapi3.Operation{Responses: responses},
	})
	paths.Set("/unrelated", &openapi3.PathItem{
		Get: &openapi3.Operation{Responses: otherResponses},
	})

	doc := &openapi3.T{
		Paths: paths,
		Components: &openapi3.Components{Schemas: map[string]*openapi3.SchemaRef{
			"DomainAllocationConnection": {Value: target},
		}},
	}

	applyPropertyRenames(doc, map[string]map[string]string{
		"DomainAllocationConnection": {"authRegion": "region"},
	})

	if _, still := match["authRegion"]; still {
		t.Error("the matching example kept the old key")
	}
	if got := match["region"]; got != "US" {
		t.Errorf("the matching example's renamed key = %v, want US", got)
	}
	if got, ok := other["authRegion"]; !ok || got != "US" {
		t.Error("an example of an unrelated schema was rewritten; the shape guard is not holding")
	}
	if _, leaked := other["region"]; leaked {
		t.Error("the rename leaked into an unrelated schema's example")
	}
}

// The rename has to reach every place the OpenAPI document can hang an
// example, not just the two — a media type's `example` and its `examples` map —
// the walk started with. Each location below is live in the carried specs: a
// media type's schema-level `example` (17 in api/pro_api.json), a nested
// property's `example` (2802 there, against no root-level one at all), and a
// response header's (15 in api/ai_governance_policies_api.json, 16 in
// api/securitycloud_dns_api.json). Links and callbacks carry the same free-form
// JSON and are published verbatim, so a stale key in one is the same
// self-inconsistency even though no carried spec uses them today — the point of
// pinning them is that the next bundle to add one is not a silent miss.
func TestApplyPropertyRenamesReachesEveryExampleLocation(t *testing.T) {
	newExample := func() map[string]any {
		return map[string]any{"assignedConnection": "con_1", "authRegion": "US"}
	}
	// Same key beside one the target schema does not declare: the shape guard
	// must leave every copy of this alone, in every location.
	newForeign := func() map[string]any {
		return map[string]any{"authRegion": "US", "unrelatedField": 7}
	}

	target := func() *openapi3.Schema {
		s := openapi3.NewObjectSchema()
		s.WithProperty("assignedConnection", openapi3.NewStringSchema())
		s.WithProperty("authRegion", openapi3.NewStringSchema())
		return s
	}

	// Each case builds the document around one example object and one foreign
	// one, then asserts the first was rewritten and the second was not.
	cases := map[string]func(ex, foreign map[string]any) *openapi3.T{
		"media type schema example": func(ex, foreign map[string]any) *openapi3.T {
			respSchema := openapi3.NewObjectSchema()
			respSchema.Example = ex
			foreignSchema := openapi3.NewObjectSchema()
			foreignSchema.Example = foreign
			return docWithResponse(openapi3.NewResponse().WithContent(openapi3.Content{
				"application/json": openapi3.NewMediaType().WithSchema(respSchema),
			}), openapi3.NewResponse().WithContent(openapi3.Content{
				"application/json": openapi3.NewMediaType().WithSchema(foreignSchema),
			}))
		},
		"nested property example": func(ex, foreign map[string]any) *openapi3.T {
			// The commonest real shape: the example sits on a property of a
			// component schema, not on the schema's own root.
			item := openapi3.NewObjectSchema()
			item.Example = ex
			envelope := openapi3.NewObjectSchema()
			envelope.WithProperty("connections", openapi3.NewArraySchema().WithItems(item))
			foreignItem := openapi3.NewObjectSchema()
			foreignItem.Example = foreign
			foreignEnvelope := openapi3.NewObjectSchema()
			foreignEnvelope.WithProperty("other", foreignItem)

			doc := docWithResponse(openapi3.NewResponse(), openapi3.NewResponse())
			doc.Components.Schemas["DomainAllocation"] = &openapi3.SchemaRef{Value: envelope}
			doc.Components.Schemas["Unrelated"] = &openapi3.SchemaRef{Value: foreignEnvelope}
			return doc
		},
		"response header example": func(ex, foreign map[string]any) *openapi3.T {
			header := func(v map[string]any) openapi3.Headers {
				return openapi3.Headers{"X-Thing": {Value: &openapi3.Header{
					Parameter: openapi3.Parameter{Example: v},
				}}}
			}
			resp := openapi3.NewResponse()
			resp.Headers = header(ex)
			foreignResp := openapi3.NewResponse()
			foreignResp.Headers = header(foreign)
			return docWithResponse(resp, foreignResp)
		},
		"component header example": func(ex, foreign map[string]any) *openapi3.T {
			doc := docWithResponse(openapi3.NewResponse(), openapi3.NewResponse())
			doc.Components.Headers = openapi3.Headers{
				"X-Thing": {Value: &openapi3.Header{Parameter: openapi3.Parameter{Example: ex}}},
				"X-Other": {Value: &openapi3.Header{Parameter: openapi3.Parameter{Example: foreign}}},
			}
			return doc
		},
		"component link": func(ex, foreign map[string]any) *openapi3.T {
			doc := docWithResponse(openapi3.NewResponse(), openapi3.NewResponse())
			doc.Components.Links = openapi3.Links{
				"self":  {Value: &openapi3.Link{RequestBody: ex}},
				"other": {Value: &openapi3.Link{Parameters: map[string]any{"body": foreign}}},
			}
			return doc
		},
		"operation callback": func(ex, foreign map[string]any) *openapi3.T {
			callbackFor := func(v map[string]any) *openapi3.Callback {
				responses := openapi3.NewResponses()
				responses.Set("200", &openapi3.ResponseRef{
					Value: openapi3.NewResponse().WithContent(openapi3.Content{
						"application/json": {Example: v},
					}),
				})
				cb := openapi3.NewCallback()
				cb.Set("{$request.body#/url}", &openapi3.PathItem{
					Post: &openapi3.Operation{Responses: responses},
				})
				return cb
			}
			doc := docWithResponse(openapi3.NewResponse(), openapi3.NewResponse())
			op := doc.Paths.Find("/allocation").Get
			op.Callbacks = openapi3.Callbacks{"onThing": {Value: callbackFor(ex)}}
			other := doc.Paths.Find("/unrelated").Get
			other.Callbacks = openapi3.Callbacks{"onThing": {Value: callbackFor(foreign)}}
			return doc
		},
	}

	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			ex, foreign := newExample(), newForeign()
			doc := build(ex, foreign)
			doc.Components.Schemas["DomainAllocationConnection"] = &openapi3.SchemaRef{Value: target()}

			applyPropertyRenames(doc, map[string]map[string]string{
				"DomainAllocationConnection": {"authRegion": "region"},
			})

			if _, still := ex["authRegion"]; still {
				t.Error("the example kept the old key — this location is not being walked")
			}
			if got := ex["region"]; got != "US" {
				t.Errorf("the example's renamed key = %v, want US", got)
			}
			if got, ok := foreign["authRegion"]; !ok || got != "US" {
				t.Error("an unrelated schema's example was rewritten; the shape guard is not holding here")
			}
		})
	}
}

// docWithResponse builds the two-path document the example-location cases share:
// /allocation carries the example that must be rewritten and /unrelated the one
// that must not, so every case exercises the shape guard as well as the walk.
func docWithResponse(resp, foreign *openapi3.Response) *openapi3.T {
	responses := openapi3.NewResponses()
	responses.Set("200", &openapi3.ResponseRef{Value: resp})
	foreignResponses := openapi3.NewResponses()
	foreignResponses.Set("200", &openapi3.ResponseRef{Value: foreign})

	paths := openapi3.NewPaths()
	paths.Set("/allocation", &openapi3.PathItem{Get: &openapi3.Operation{Responses: responses}})
	paths.Set("/unrelated", &openapi3.PathItem{Get: &openapi3.Operation{Responses: foreignResponses}})

	return &openapi3.T{
		Paths:      paths,
		Components: &openapi3.Components{Schemas: openapi3.Schemas{}},
	}
}

// The entry has to expire the way every other local spec repair does. Both
// conditions below leave the rename a silent no-op or, worse, a silent
// clobbering: a schema the spec no longer publishes means nobody is applying
// the repair, and a spec declaring both names — the transitional step upstream
// takes mid-rename — means the genuine new property is overwritten by the stale
// one while the old key's presence keeps the existing guards quiet.
func TestApplyPropertyRenamesPanicsOnStaleEntry(t *testing.T) {
	bothNames := openapi3.NewObjectSchema()
	bothNames.WithProperty("authRegion", openapi3.NewStringSchema())
	bothNames.WithProperty("region", openapi3.NewStringSchema())

	cases := map[string]openapi3.Schemas{
		"no such schema": {},
		"spec declares both the old and the new name": {
			"DomainAllocationConnection": {Value: bothNames},
		},
	}
	for name, schemas := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("applyPropertyRenames returned without panicking")
				}
			}()
			applyPropertyRenames(&openapi3.T{Components: &openapi3.Components{Schemas: schemas}},
				map[string]map[string]string{"DomainAllocationConnection": {"authRegion": "region"}})
		})
	}
}
