// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// applySchemaRenames renames schema keys in doc.Components.Schemas and updates
// all $ref strings that reference the old name throughout the document. Applied
// before other patches so downstream fixups see corrected names. Only the Ref
// string on each SchemaRef is updated; the walker stops at $ref boundaries and
// does not recurse into the referenced schema's Value to avoid cycles.
func applySchemaRenames(doc *openapi3.T, renames map[string]string) {
	if doc == nil || doc.Components == nil || doc.Components.Schemas == nil || len(renames) == 0 {
		return
	}
	refMap := make(map[string]string, len(renames))
	for oldName, newName := range renames {
		refMap["#/components/schemas/"+oldName] = "#/components/schemas/" + newName
	}
	for oldName, newName := range renames {
		if ref, ok := doc.Components.Schemas[oldName]; ok {
			doc.Components.Schemas[newName] = ref
			delete(doc.Components.Schemas, oldName)
		}
	}

	visited := make(map[*openapi3.SchemaRef]bool)
	var walkSchema func(ref *openapi3.SchemaRef)
	walkSchema = func(ref *openapi3.SchemaRef) {
		if ref == nil || visited[ref] {
			return
		}
		visited[ref] = true
		if ref.Ref != "" {
			if newRef, ok := refMap[ref.Ref]; ok {
				ref.Ref = newRef
			}
			return // stop at ref boundaries; Value is the shared component, walked separately
		}
		if ref.Value == nil {
			return
		}
		for _, prop := range ref.Value.Properties {
			walkSchema(prop)
		}
		if ref.Value.Items != nil {
			walkSchema(ref.Value.Items)
		}
		if ref.Value.AdditionalProperties.Schema != nil {
			walkSchema(ref.Value.AdditionalProperties.Schema)
		}
		for _, s := range ref.Value.AllOf {
			walkSchema(s)
		}
		for _, s := range ref.Value.AnyOf {
			walkSchema(s)
		}
		for _, s := range ref.Value.OneOf {
			walkSchema(s)
		}
	}

	for _, ref := range doc.Components.Schemas {
		walkSchema(ref)
	}
	if doc.Paths == nil {
		return
	}
	for _, pathStr := range doc.Paths.InMatchingOrder() {
		item := doc.Paths.Find(pathStr)
		if item == nil {
			continue
		}
		for _, op := range []*openapi3.Operation{
			item.Get, item.Post, item.Put, item.Patch, item.Delete,
		} {
			if op == nil {
				continue
			}
			if op.RequestBody != nil && op.RequestBody.Value != nil {
				for _, content := range op.RequestBody.Value.Content {
					if content != nil && content.Schema != nil {
						walkSchema(content.Schema)
					}
				}
			}
			if op.Responses != nil {
				for _, respRef := range op.Responses.Map() {
					if respRef == nil || respRef.Value == nil {
						continue
					}
					for _, content := range respRef.Value.Content {
						if content != nil && content.Schema != nil {
							walkSchema(content.Schema)
						}
					}
				}
			}
		}
	}
}

// applySchemaAdditions injects missing property declarations into named
// component schemas. Used for specs that omit fields the server actually
// accepts (e.g. Classic's `account` schema has no `password` property
// even though creating a user requires one on the wire). Runs after
// openapi2conv + before hoistInlineObjects so hoisting still sees the
// injected properties. openapi_type supports plain scalar names
// ("string", "integer", "boolean") and the extended form
// "string:password" which sets format=password + writeOnly=true so the
// field comment notes the security sensitivity.
func applySchemaAdditions(doc *openapi3.T, additions map[string]map[string]string) {
	if doc == nil || doc.Components == nil || doc.Components.Schemas == nil || len(additions) == 0 {
		return
	}
	for schemaName, props := range additions {
		ref, ok := doc.Components.Schemas[schemaName]
		if !ok || ref == nil || ref.Value == nil {
			continue
		}
		schema := ref.Value
		if schema.Properties == nil {
			schema.Properties = openapi3.Schemas{}
		}
		for propName, openapiType := range props {
			if _, exists := schema.Properties[propName]; exists {
				continue
			}
			parts := strings.SplitN(openapiType, ":", 2)
			baseType := parts[0]
			propSchema := &openapi3.Schema{Type: &openapi3.Types{baseType}}
			if len(parts) == 2 && parts[1] == "password" {
				propSchema.Format = "password"
				propSchema.WriteOnly = true
			}
			// "object" with no properties → freeform JSON (decoded as json.RawMessage).
			// The schema has type:object and no further constraints so the generator's
			// freeform-object detector fires and emits the field as json.RawMessage.
			if baseType == "object" {
				propSchema.AdditionalProperties = openapi3.AdditionalProperties{}
			}
			schema.Properties[propName] = &openapi3.SchemaRef{Value: propSchema}
		}
	}
}

// applyEnumAdditions appends values to a named component schema's enum list.
// For a value the server produces or accepts that the spec does not declare —
// see SpecDef.EnumAdditions for why an omission here is a correctness bug and
// not a documentation one.
//
// Panics when the value is already declared, which is how the entry expires:
// upstream publishing the value must delete the config entry rather than
// silently duplicate it, because a duplicated enum member emits two Go
// constants with the same value and the second shadows nothing — it just
// compiles, so nothing would ever notice. Panics too when the schema is
// missing or declares no enum at all, since either means the entry no longer
// names what it was written against.
//
// Runs with the other schema patches, so the value reaches the published spec
// under api/ as well as the generated Go.
func applyEnumAdditions(doc *openapi3.T, additions map[string][]string) {
	if doc == nil || doc.Components == nil || doc.Components.Schemas == nil || len(additions) == 0 {
		return
	}
	for _, schemaName := range sortedMapKeys(additions) {
		values := additions[schemaName]
		ref, ok := doc.Components.Schemas[schemaName]
		if !ok || ref == nil || ref.Value == nil {
			panic(fmt.Sprintf("enumAdditions[%q]: no such component schema — delete or retarget the entry", schemaName))
		}
		schema := ref.Value
		if len(schema.Enum) == 0 {
			panic(fmt.Sprintf("enumAdditions[%q]: schema declares no enum to add to — delete or retarget the entry", schemaName))
		}
		declared := make(map[string]bool, len(schema.Enum))
		for _, e := range schema.Enum {
			declared[fmt.Sprint(e)] = true
		}
		for _, v := range values {
			if declared[v] {
				panic(fmt.Sprintf("enumAdditions[%q]: the spec now declares %q — delete it from the entry so the upstream declaration is the only source", schemaName, v))
			}
			schema.Enum = append(schema.Enum, v)
			declared[v] = true
		}
	}
}

// assertSchemaPatchTargetsAbsent enforces SchemaPatchesRequireAbsent: every
// listed "<SchemaName>.<dotted.path>" must NOT already resolve in the spec.
// Panics when one does, because that means upstream has started declaring a
// property we were substituting for and the config entry now shadows the real
// declaration.
//
// Runs before applySchemaPatches, so the check sees the spec as published
// rather than the patched result. A missing schema is not an error here — the
// patch itself is a no-op in that case, and applySchemaPatches skips it too.
func assertSchemaPatchTargetsAbsent(doc *openapi3.T, paths []string) {
	if doc == nil || doc.Components == nil || doc.Components.Schemas == nil {
		return
	}
	for _, full := range paths {
		schemaName, path, found := strings.Cut(full, ".")
		if !found || schemaName == "" || path == "" {
			panic(fmt.Sprintf("schemaPatchesRequireAbsent[%q]: want \"<SchemaName>.<dotted.path>\"", full))
		}
		ref, ok := doc.Components.Schemas[schemaName]
		if !ok || ref == nil || ref.Value == nil {
			continue
		}
		parent, leaf, ok := walkPropertyPath(ref.Value, path)
		if !ok || parent == nil {
			continue
		}
		if _, exists := parent.Properties[leaf]; exists {
			panic(fmt.Sprintf("schemaPatchesRequireAbsent[%q]: the spec now declares this property — delete the schemaPatches entry (and its schemaPatchesRequireAbsent line, and any schemaCreations it depended on) so the upstream declaration is used instead", full))
		}
	}
}

// applySchemaPatches inserts or replaces sub-schemas at dotted property paths
// under named component schemas. Used when a spec omits a structured field the
// server actually returns or accepts (e.g. policy missing top-level `reboot`,
// `scope.jss_users`, or self-service notification scalars). Each patch value
// is a raw OpenAPI 3 Schema JSON fragment; intermediate path segments must
// resolve to object-with-properties so the walker can descend. Replaces an
// existing property when the path already terminates somewhere; otherwise
// adds it. Runs after applySchemaAdditions and before applyPostSymmetry so
// that *_post schemas inherit the patched read shape.
func applySchemaPatches(doc *openapi3.T, patches map[string]map[string]json.RawMessage) {
	if doc == nil || doc.Components == nil || doc.Components.Schemas == nil || len(patches) == 0 {
		return
	}
	for schemaName, paths := range patches {
		ref, ok := doc.Components.Schemas[schemaName]
		if !ok || ref == nil || ref.Value == nil {
			continue
		}
		for path := range paths {
			raw := paths[path]
			if len(raw) == 0 {
				continue
			}
			sub, err := parsePatchSchema(raw)
			if err != nil {
				panic(fmt.Sprintf("schemaPatches[%q][%q]: %v", schemaName, path, err))
			}
			resolvePatchRefs(sub, doc)
			parent, leaf, ok := walkPropertyPath(ref.Value, path)
			if !ok {
				panic(fmt.Sprintf("schemaPatches[%q]: cannot reach path %q in schema", schemaName, path))
			}
			if parent.Properties == nil {
				parent.Properties = openapi3.Schemas{}
			}
			parent.Properties[leaf] = sub
		}
	}
}

// applyPropertyRenames renames property keys at dotted paths inside named
// component schemas. Used to repair spec keys that don't match the wire so
// decode actually succeeds (`re-install_button_text` → `reinstall_button_text`,
// `allow_user_to_defer` → `allow_users_to_defer`). Path's last segment is the
// current key on the parent; value is the new key. Runs before applyPostSymmetry
// so the post sibling sees the corrected key.
func applyPropertyRenames(doc *openapi3.T, renames map[string]map[string]string) {
	if doc == nil || doc.Components == nil || doc.Components.Schemas == nil || len(renames) == 0 {
		return
	}
	for schemaName, paths := range renames {
		ref, ok := doc.Components.Schemas[schemaName]
		if !ok || ref == nil || ref.Value == nil {
			continue
		}
		for path, newKey := range paths {
			parent, leaf, ok := walkPropertyPath(ref.Value, path)
			if !ok {
				panic(fmt.Sprintf("propertyRenames[%q]: cannot reach path %q", schemaName, path))
			}
			cur, exists := parent.Properties[leaf]
			if !exists {
				panic(fmt.Sprintf("propertyRenames[%q]: property %q missing at path %q", schemaName, leaf, path))
			}
			delete(parent.Properties, leaf)
			parent.Properties[newKey] = cur
			// A rename must carry the property's `required` entry with it,
			// for the reason applyPropertyRemovals prunes one: a required
			// name with no property is an invalid spec a consumer reads.
			// v2176's DomainAllocationConnection.authRegion is the first
			// renamed property that is required, so this stopped being
			// latent — the removal side stayed latent the same way until
			// v2154 removed its first required one.
			for i, r := range parent.Required {
				if r == leaf {
					parent.Required[i] = newKey
				}
			}
			// The spec's own examples still spell the old key, and they are
			// published verbatim, so without this api/*.json declares a
			// schema carrying `region` beside an example showing
			// `authRegion` — a self-inconsistent spec handed to consumers,
			// the same defect class as leaving the stale `required` entry
			// above.
			allowed := map[string]bool{leaf: true}
			for name := range parent.Properties {
				allowed[name] = true
			}
			renameKeyInExamples(doc, allowed, leaf, newKey)
		}
	}
}

// renameKeyInExamples rewrites one renamed property key inside the spec's own
// examples, which applyPropertyRenames cannot reach by walking schemas: an
// example is free-form JSON hanging off a media type, not a property.
//
// It is shape-guarded rather than a blind key rewrite, because a property name
// is not unique across a spec — Classic renames `categories`, `users` and
// `security_name`, any of which could plausibly appear in an unrelated
// example. An object is rewritten only when it carries the old key *and* every
// one of its keys is a property of the schema the rename targets, so an example
// of some other type that happens to share a key name is left alone. Today
// exactly one example in the nineteen specs matches (the DomainAllocation
// response), and the other fifteen renamed keys appear in no example at all.
func renameKeyInExamples(doc *openapi3.T, allowed map[string]bool, oldKey, newKey string) {
	var scan func(node any)
	scan = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if _, has := n[oldKey]; has {
				matches := true
				for k := range n {
					if !allowed[k] {
						matches = false
						break
					}
				}
				if matches {
					n[newKey] = n[oldKey]
					delete(n, oldKey)
				}
			}
			for _, v := range n {
				scan(v)
			}
		case []any:
			for _, v := range n {
				scan(v)
			}
		}
	}

	scanContent := func(c openapi3.Content) {
		for _, mt := range c {
			if mt == nil {
				continue
			}
			scan(mt.Example)
			for _, ex := range mt.Examples {
				if ex != nil && ex.Value != nil {
					scan(ex.Value.Value)
				}
			}
		}
	}
	scanParams := func(ps openapi3.Parameters) {
		for _, pr := range ps {
			if pr == nil || pr.Value == nil {
				continue
			}
			scan(pr.Value.Example)
			for _, ex := range pr.Value.Examples {
				if ex != nil && ex.Value != nil {
					scan(ex.Value.Value)
				}
			}
			scanContent(pr.Value.Content)
		}
	}
	scanResponses := func(rs *openapi3.Responses) {
		if rs == nil {
			return
		}
		for _, r := range rs.Map() {
			if r == nil || r.Value == nil {
				continue
			}
			scanContent(r.Value.Content)
		}
	}

	if doc.Components != nil {
		for _, ex := range doc.Components.Examples {
			if ex != nil && ex.Value != nil {
				scan(ex.Value.Value)
			}
		}
		for _, sr := range doc.Components.Schemas {
			if sr != nil && sr.Value != nil {
				scan(sr.Value.Example)
			}
		}
		for _, rb := range doc.Components.RequestBodies {
			if rb != nil && rb.Value != nil {
				scanContent(rb.Value.Content)
			}
		}
		for _, r := range doc.Components.Responses {
			if r != nil && r.Value != nil {
				scanContent(r.Value.Content)
			}
		}
		scanParams(slices.Collect(maps.Values(doc.Components.Parameters)))
	}
	if doc.Paths == nil {
		return
	}
	for _, path := range doc.Paths.InMatchingOrder() {
		item := doc.Paths.Find(path)
		if item == nil {
			continue
		}
		scanParams(item.Parameters)
		for _, op := range item.Operations() {
			if op == nil {
				continue
			}
			scanParams(op.Parameters)
			if op.RequestBody != nil && op.RequestBody.Value != nil {
				scanContent(op.RequestBody.Value.Content)
			}
			scanResponses(op.Responses)
		}
	}
}

// applyPropertyRemovals deletes properties at dotted paths inside named
// component schemas. Used to drop spec properties that are misplaced (e.g.
// mac_application.self_service.vpp is nested under self_service in the spec
// but lives at the top level on the wire; the top-level shape is added via
// schemaPatches, and this removes the phantom nested one so generated Go
// types expose only the correct field). Runs after applyPropertyRenames and
// before applyPostSymmetry so the post sibling inherits the trimmed shape.
// Panics on missing paths — a declared removal whose path can't be resolved
// is always a config bug.
func applyPropertyRemovals(doc *openapi3.T, removals map[string][]string) {
	if doc == nil || doc.Components == nil || doc.Components.Schemas == nil || len(removals) == 0 {
		return
	}
	for schemaName, paths := range removals {
		ref, ok := doc.Components.Schemas[schemaName]
		if !ok || ref == nil || ref.Value == nil {
			continue
		}
		for _, path := range paths {
			parent, leaf, ok := walkPropertyPath(ref.Value, path)
			if !ok {
				panic(fmt.Sprintf("propertyRemovals[%q]: cannot reach path %q", schemaName, path))
			}
			if _, exists := parent.Properties[leaf]; !exists {
				panic(fmt.Sprintf("propertyRemovals[%q]: property %q missing at path %q", schemaName, leaf, path))
			}
			delete(parent.Properties, leaf)
			// A removal must take the property's `required` entry with it.
			// Leaving it behind publishes an api/*.json declaring a required
			// property the schema does not have — an invalid spec a consumer
			// reads — and leaves the same dangling name in front of every
			// later pass that keys off required. Hit for real when v2154 added
			// AccountPreferencesV6.showDirectoryGroupUuidColumn as required
			// and the server rejected it.
			parent.Required = slices.DeleteFunc(parent.Required, func(r string) bool { return r == leaf })
		}
	}
}

// applyPostSymmetry copies properties from each read schema X into its *_post
// sibling X_post so write types accept the full field set the server actually
// honours. Default behaviour for every spec — Jamf's classic specs routinely
// declare X_post as a minimal "general only" object even when the server
// accepts (and persists) every other top-level block. Specs that genuinely
// have write-only fields list them under PostSymmetryExcludes.
//
// Copy semantics: when the read schema declares a property the post sibling
// doesn't, the property's *openapi3.SchemaRef is shared between read and post
// (cheap; hoistInlineObjects mutates per-schema property map entries, not the
// underlying inline objects). When the post sibling already declares the same
// property, the read shape REPLACES it — the brief is explicit that the post
// must mirror the read, not preserve a slimmer alternative shape (e.g. the
// existing EbookPostScope all_* booleans only). The post schema's own xml
// metadata (typically `xml.name = <read-name>`) is preserved.
func applyPostSymmetry(doc *openapi3.T, excludes map[string][]string) {
	if doc == nil || doc.Components == nil || doc.Components.Schemas == nil {
		return
	}
	for postName, ref := range doc.Components.Schemas {
		if !strings.HasSuffix(postName, "_post") || ref == nil || ref.Value == nil {
			continue
		}
		readName := strings.TrimSuffix(postName, "_post")
		readRef, ok := doc.Components.Schemas[readName]
		if !ok || readRef == nil || readRef.Value == nil {
			continue
		}
		post := ref.Value
		read := readRef.Value
		if len(read.Properties) == 0 {
			continue
		}
		skip := make(map[string]bool, len(excludes[postName]))
		for _, p := range excludes[postName] {
			skip[p] = true
		}
		if post.Properties == nil {
			post.Properties = openapi3.Schemas{}
		}
		for propName, propRef := range read.Properties {
			if skip[propName] {
				continue
			}
			post.Properties[propName] = propRef
		}
	}
}

// parsePatchSchema parses a raw JSON OpenAPI Schema fragment into a SchemaRef.
// Uses SchemaRef's own UnmarshalJSON so nested `$ref` strings populate the
// Ref field on the right SchemaRef levels (a direct json.Unmarshal into
// openapi3.Schema would skip that path for property values and items).
// Refs in the fragment are left unresolved on purpose — the parent document
// already carries the target schemas (e.g. id_name), and `openapi3.SchemaRef`
// callers downstream key off `.Ref` strings rather than `.Value` pointers.
func parsePatchSchema(raw json.RawMessage) (*openapi3.SchemaRef, error) {
	var ref openapi3.SchemaRef
	if err := ref.UnmarshalJSON(raw); err != nil {
		return nil, err
	}
	return &ref, nil
}

// resolvePatchRefs walks a freshly-parsed patch SchemaRef tree and points
// each unresolved `$ref` at the matching component schema in the parent
// doc. Manual unmarshal of a SchemaRef populates `Ref` but leaves `Value`
// nil; downstream passes (flattenClassicSizeWrappers, hoistInlineObjects)
// short-circuit on `Value == nil`, treating refs as unknown shape. Loader
// machinery would normally resolve these but bypassing the loader was
// chosen to avoid forcing each patch fragment into a standalone document
// with every transitive $ref target restated.
func resolvePatchRefs(ref *openapi3.SchemaRef, doc *openapi3.T) {
	if ref == nil {
		return
	}
	if ref.Ref != "" && ref.Value == nil {
		if name, ok := strings.CutPrefix(ref.Ref, "#/components/schemas/"); ok {
			if target, exists := doc.Components.Schemas[name]; exists && target != nil {
				ref.Value = target.Value
			}
		}
	}
	if ref.Value == nil {
		return
	}
	for _, p := range ref.Value.Properties {
		resolvePatchRefs(p, doc)
	}
	if ref.Value.Items != nil {
		resolvePatchRefs(ref.Value.Items, doc)
	}
	if ref.Value.AdditionalProperties.Schema != nil {
		resolvePatchRefs(ref.Value.AdditionalProperties.Schema, doc)
	}
	for _, s := range ref.Value.AllOf {
		resolvePatchRefs(s, doc)
	}
}

// applySchemaCreations adds whole named component schemas a spec has stopped
// declaring. Panics when the name already exists: the only reason to create a
// schema here is that upstream dropped one the server still returns, so the
// name reappearing means the spec has been repaired and the config entry (plus
// whatever SchemaPatches referenced it) should be deleted. Failing loudly at
// generate time is the point — a silent skip would leave the local definition
// shadowing the real one indefinitely.
//
// Runs first, before applySchemaRenames, so created schemas are indistinguishable
// from spec-declared ones for every pass that follows.
func applySchemaCreations(doc *openapi3.T, creations map[string]json.RawMessage) {
	if doc == nil || doc.Components == nil || len(creations) == 0 {
		return
	}
	if doc.Components.Schemas == nil {
		doc.Components.Schemas = openapi3.Schemas{}
	}
	for _, name := range sortedKeys(creations) {
		if _, exists := doc.Components.Schemas[name]; exists {
			panic(fmt.Sprintf("schemaCreations[%q]: schema already declared by the spec — delete the config entry", name))
		}
		sub, err := parsePatchSchema(creations[name])
		if err != nil {
			panic(fmt.Sprintf("schemaCreations[%q]: %v", name, err))
		}
		doc.Components.Schemas[name] = sub
	}
	// Second pass: a created schema may $ref another created one, so refs are
	// resolved only once every name is present.
	for _, name := range sortedKeys(creations) {
		resolvePatchRefs(doc.Components.Schemas[name], doc)
	}
}

// walkPropertyPath traverses dotted `a.b.c` into nested object schemas. Returns
// the deepest reachable parent schema and the final leaf segment. Auto-descends
// through array schemas via `.items.value` (Classic spec routinely models
// repeating XML children as `prop: { type: array, items: { type: object,
// properties: { child: { … } } } }`; expressing the descent explicitly in the
// path would force every caller to thread `items.` through every array hop).
// Non-leaf segments must resolve to an object-with-properties (after array
// auto-descent); the leaf segment may or may not exist on the returned parent.
func walkPropertyPath(root *openapi3.Schema, path string) (*openapi3.Schema, string, bool) {
	if root == nil || path == "" {
		return nil, "", false
	}
	parts := strings.Split(path, ".")
	cur := root
	for i := 0; i < len(parts)-1; i++ {
		seg := parts[i]
		if cur.Type != nil && cur.Type.Is("array") && cur.Items != nil && cur.Items.Value != nil {
			cur = cur.Items.Value
		}
		if cur.Properties == nil {
			return nil, "", false
		}
		next, ok := cur.Properties[seg]
		if !ok || next == nil || next.Value == nil {
			return nil, "", false
		}
		cur = next.Value
	}
	if cur.Type != nil && cur.Type.Is("array") && cur.Items != nil && cur.Items.Value != nil {
		cur = cur.Items.Value
	}
	return cur, parts[len(parts)-1], true
}

// flattenClassicSizeWrappers rewrites Jamf Classic's `<wrapper><size>N</size><item>...</item>...</wrapper>`
// XML containers so they round-trip through encoding/xml without data loss.
//
// The Classic spec models these as `wrapper: type:array, items: {size, X}` —
// implying N wrapper elements, each carrying a size sentinel and a single item.
// The actual wire emits ONE `<wrapper>` element with a single `<size>` sibling
// and N repeated `<X>` children. Mapping the spec literally produces
// `Wrapper *[]Item` in Go; encoding/xml then sees one `<wrapper>` element,
// allocates one slot, and the N `<X>` children collide into the slot's
// non-slice X field — only the last survives. Smart groups silently lose every
// criterion except the final one (confirmed across UserGroup, MobileDeviceGroup,
// ComputerGroup on real tenant XML).
//
// Transform: `wrapper: array of {size, X}` → `wrapper: object {size, X: array of <X-shape>}`.
// hoistInlineObjects then lifts the new wrapper into its own named schema, and
// the inner X array hoists / dedups against existing named schemas (criterion,
// computer, mobile_device, etc.) as usual. Output: callers get
// `*UserGroupCriteria { Size *int, Criterion []Criterion }` and the full slice
// survives a decode.
//
// Pattern guard — applied only when ALL of the following hold so we don't
// rewrite legitimate JSON-style arrays-of-{size,item} (none exist in Classic
// today, but the heuristic should be tight regardless):
//   - property is `type: array` with inline items (no $ref)
//   - items.properties has 1 or 2 keys
//   - one is "size" (optional); exactly one other key, naming a singular wire child
//   - the other key references or inlines an object schema
//
// Runs before hoistInlineObjects so hoisting sees the transformed shape.
// XML-format specs only — JSON specs may legitimately use the lossy shape.
func flattenClassicSizeWrappers(doc *openapi3.T) {
	if doc == nil || doc.Components == nil || doc.Components.Schemas == nil {
		return
	}
	visited := map[*openapi3.Schema]bool{}
	for _, ref := range doc.Components.Schemas {
		if ref != nil && ref.Value != nil {
			flattenClassicSizeWrappersInSchema(ref.Value, visited)
		}
	}
}

// normalizeNullableUnions rewrites OpenAPI 3.1's two nullability idioms into
// the 3.0 shape (`nullable: true`) the rest of the generator reasons about.
// kin-openapi models 3.1 faithfully, and `Types.Is` only matches a
// single-element type list, so without this pass every nullable property falls
// through schemaRefToGoType's default branch and lands as `any`:
//
//	type: [string, "null"]                    -> type: string,  nullable: true
//	oneOf: [{$ref: X}, {type: "null"}]        -> $ref: X,       nullable: true
//
// Both forms are how Jamf's newer 3.1 specs (compliance benchmarks, DDM
// report) spell an optional object or a nullable scalar. The 3.0 specs
// (Pro, Classic, devices) contain neither, so this is a no-op for them.
//
// Idempotent, so it runs for published (api/) specs too — the transform's
// output contains no 3.1 union left to rewrite.
//
// Runs before hoistInlineObjects and collectReferencedSchemas: hoisting must
// see the collapsed shape, and a $ref reachable only through a nullable union
// has to be visible to the reference walker or its type is never emitted.
func normalizeNullableUnions(doc *openapi3.T) {
	if doc == nil || doc.Components == nil || doc.Components.Schemas == nil {
		return
	}
	seen := map[*openapi3.Schema]bool{}
	var walk func(ref *openapi3.SchemaRef, collapsible bool) *openapi3.SchemaRef
	walk = func(ref *openapi3.SchemaRef, collapsible bool) *openapi3.SchemaRef {
		if ref == nil || ref.Value == nil {
			return ref
		}
		if collapsible {
			if repl, ok := collapseNullableOneOf(ref); ok {
				ref = repl
			}
		}
		s := ref.Value
		if seen[s] {
			return ref
		}
		seen[s] = true
		stripNullFromTypeList(s)
		for _, pname := range sortedKeys(s.Properties) {
			s.Properties[pname] = walk(s.Properties[pname], true)
		}
		s.Items = walk(s.Items, true)
		if s.AdditionalProperties.Schema != nil {
			s.AdditionalProperties.Schema = walk(s.AdditionalProperties.Schema, true)
		}
		for _, list := range []openapi3.SchemaRefs{s.AllOf, s.AnyOf, s.OneOf} {
			for i := range list {
				list[i] = walk(list[i], false)
			}
		}
		return ref
	}
	// A named component is never itself collapsed: replacing the map entry
	// would drop the schema name the rest of the generator emits a type for.
	for _, name := range sortedKeys(doc.Components.Schemas) {
		walk(doc.Components.Schemas[name], false)
	}
}

// collapseNullableOneOf rewrites `oneOf: [{$ref: X}, {type: "null"}]` to a
// plain reference to X marked nullable. Returns false unless the union is
// exactly that shape: one non-null member, at least one bare `type: "null"`,
// and no sibling constraints of its own that the collapse would discard.
func collapseNullableOneOf(ref *openapi3.SchemaRef) (*openapi3.SchemaRef, bool) {
	if ref.Ref != "" || ref.Value == nil {
		return ref, false
	}
	s := ref.Value
	if len(s.OneOf) < 2 || len(s.AllOf) > 0 || len(s.AnyOf) > 0 ||
		len(s.Properties) > 0 || len(s.Enum) > 0 || (s.Type != nil && len(*s.Type) > 0) {
		return ref, false
	}
	var member *openapi3.SchemaRef
	nulls := 0
	for _, m := range s.OneOf {
		if m == nil || m.Value == nil {
			return ref, false
		}
		if m.Ref == "" && m.Value.Type.Is("null") {
			nulls++
			continue
		}
		if member != nil {
			return ref, false
		}
		member = m
	}
	if nulls == 0 || member == nil {
		return ref, false
	}
	out := &openapi3.SchemaRef{Ref: member.Ref, Value: member.Value}
	// A description sitting beside the oneOf is the only documentation the
	// property carries; keep it when the target has none of its own. When the
	// member is a $ref there is nowhere to hang it — the referenced schema's
	// own description is what the field comment uses.
	if member.Ref == "" && out.Value.Description == "" {
		out.Value.Description = s.Description
	}
	out.Value.Nullable = true
	return out, true
}

// stripNullFromTypeList rewrites 3.1's `type: [T, "null"]` union to `type: T`
// with `nullable: true`. Unions of two or more non-null types are left alone —
// there is no single Go type for them, and `any` is the honest answer.
func stripNullFromTypeList(s *openapi3.Schema) {
	if s.Type == nil || len(*s.Type) < 2 {
		return
	}
	kept := make(openapi3.Types, 0, len(*s.Type))
	sawNull := false
	for _, t := range *s.Type {
		if t == "null" {
			sawNull = true
			continue
		}
		kept = append(kept, t)
	}
	if !sawNull || len(kept) != 1 {
		return
	}
	s.Type = &kept
	s.Nullable = true
}

func flattenClassicSizeWrappersInSchema(schema *openapi3.Schema, visited map[*openapi3.Schema]bool) {
	if schema == nil || visited[schema] {
		return
	}
	visited[schema] = true
	for propName, propRef := range schema.Properties {
		if propRef == nil || propRef.Value == nil || propRef.Ref != "" {
			continue
		}
		if rewritten := flattenIfClassicWrapper(propRef.Value); rewritten != nil {
			schema.Properties[propName] = &openapi3.SchemaRef{Value: rewritten}
		}
	}
	for _, propRef := range schema.Properties {
		if propRef != nil && propRef.Value != nil {
			flattenClassicSizeWrappersInSchema(propRef.Value, visited)
		}
	}
	if schema.Items != nil && schema.Items.Value != nil {
		flattenClassicSizeWrappersInSchema(schema.Items.Value, visited)
	}
	for _, s := range schema.AllOf {
		if s != nil && s.Value != nil {
			flattenClassicSizeWrappersInSchema(s.Value, visited)
		}
	}
}

func flattenIfClassicWrapper(s *openapi3.Schema) *openapi3.Schema {
	if s == nil || !s.Type.Is("array") || s.Items == nil || s.Items.Ref != "" || s.Items.Value == nil {
		return nil
	}
	items := s.Items.Value
	if len(items.Properties) == 0 || len(items.Properties) > 2 {
		return nil
	}
	var sizeRef, itemRef *openapi3.SchemaRef
	var itemKey string
	for k, v := range items.Properties {
		switch k {
		case "size":
			sizeRef = v
		default:
			if itemRef != nil {
				// More than one non-size key — not a wrapper shape.
				return nil
			}
			itemRef = v
			itemKey = k
		}
	}
	if itemRef == nil || itemRef.Value == nil {
		return nil
	}
	// The non-size key must reference (or inline) an object schema.
	if itemRef.Ref == "" && len(itemRef.Value.Properties) == 0 {
		return nil
	}
	newProps := openapi3.Schemas{}
	if sizeRef != nil {
		newProps["size"] = sizeRef
	}
	newProps[itemKey] = &openapi3.SchemaRef{
		Value: &openapi3.Schema{
			Type:  &openapi3.Types{"array"},
			Items: itemRef,
		},
	}
	return &openapi3.Schema{
		Type:       &openapi3.Types{"object"},
		Properties: newProps,
		XML:        s.XML,
	}
}

// hoistInlineObjects promotes every inline object-with-properties found in
// component schemas to its own named top-level schema and replaces the
// property with a $ref. Specs that model deeply-nested XML resources
// (Classic's Computer has general/hardware/software sections defined
// inline) depend on this pass — without it those nested objects collapse
// to map[string]any, which encoding/xml can't populate from structured
// XML content. Runs in-place on the doc; safe for specs that already use
// named schemas (no inline objects found → no-op).
func hoistInlineObjects(doc *openapi3.T, format string) {
	if doc == nil || doc.Components == nil || doc.Components.Schemas == nil {
		return
	}
	changed := true
	for changed {
		changed = false
		for _, name := range sortedKeys(doc.Components.Schemas) {
			schema := doc.Components.Schemas[name].Value
			if schema == nil {
				continue
			}
			if hoistInlineObjectsInSchema(name, schema, doc, format) {
				changed = true
			}
		}
	}
}

// hoistInlineObjectsInSchema walks one schema's properties and items, lifting
// inline typed objects into named top-level schemas. Returns true when any
// lift happened so the outer loop can revisit schemas added mid-walk. The
// format hint enables XML-specific behaviour: empty `type: object` properties
// are hoisted into named structs (so they round-trip as <element/> rather
// than freeform map[string]any) — JSON specs continue treating those as
// freeform per the OpenAPI convention.
func hoistInlineObjectsInSchema(parentName string, schema *openapi3.Schema, doc *openapi3.T, format string) bool {
	if schema == nil {
		return false
	}
	hoisted := false
	// Top-level array schemas whose items are an inline object need
	// their items promoted so the generated alias has a named element
	// type. The Classic spec is riddled with `type: array, items: {
	// properties: {size, building} }` list shapes; without hoisting
	// the items collapse to map[string]any.
	if schema.Type.Is("array") && schema.Items != nil && schema.Items.Ref == "" &&
		schema.Items.Value != nil && len(schema.Items.Value.Properties) > 0 {
		nested := uniqueSchemaName(doc, parentName+"Item")
		if schema.Items.Value.XML == nil {
			schema.Items.Value.XML = &openapi3.XML{}
		}
		if schema.Items.Value.XML.Name == "" {
			schema.Items.Value.XML.Name = singularize(parentName)
		}
		doc.Components.Schemas[nested] = &openapi3.SchemaRef{Value: schema.Items.Value}
		schema.Items = &openapi3.SchemaRef{Ref: "#/components/schemas/" + nested, Value: schema.Items.Value}
		hoisted = true
	}
	lift := func(propName string, ref *openapi3.SchemaRef) *openapi3.SchemaRef {
		if ref == nil || ref.Ref != "" || ref.Value == nil {
			return ref
		}
		v := ref.Value
		// Treat (Type==nil && Properties>0) as object-shape. Classic's
		// spec frequently omits `type: object` on inline subschemas that
		// are clearly objects (e.g. user_group.criteria.items, user_group.users.items).
		// Mirrors the extractTypes nil-type tolerance at the top of this file.
		//
		// XML specs also hoist explicit empty `type: object` properties:
		// Classic emits per-type binding blocks like <powerbroker_identity_services/>
		// that need to round-trip as an empty struct, not the map[string]any
		// fallback an unhoisted empty object would produce. JSON specs
		// continue treating empty `type: object` as freeform per OpenAPI's
		// convention (the freeform branch in extractTypes turns those into
		// json.RawMessage).
		hasObjShape := func(s *openapi3.Schema) bool {
			if s == nil {
				return false
			}
			if len(s.Properties) > 0 && (s.Type.Is("object") || s.Type == nil) {
				return true
			}
			if format == "xml" && s.Type.Is("object") && s.AdditionalProperties.Schema == nil {
				return true
			}
			return false
		}
		inlineObject := hasObjShape(v) || isRefUnion(v)
		inlineArrayOfObject := v.Type.Is("array") && v.Items != nil && v.Items.Ref == "" &&
			hasObjShape(v.Items.Value)
		if !inlineObject && !inlineArrayOfObject {
			return ref
		}
		if inlineObject {
			// Reuse a top-level schema with matching name + property keyset
			// instead of emitting a near-duplicate hoisted type. Classic's
			// spec sometimes inlines the same shape that's also defined as
			// a named component (e.g. user_group.criteria.items.criterion
			// matches the top-level `criterion` schema verbatim).
			if existing, ok := doc.Components.Schemas[propName]; ok && sameInlineShapeAsNamed(v, existing) {
				return &openapi3.SchemaRef{Ref: "#/components/schemas/" + propName, Value: existing.Value}
			}
			nested := parentName + exportedGoName(propName)
			nested = uniqueSchemaName(doc, nested)
			// Preserve the original property name as the hoisted schema's
			// XML wire name so its XMLName tag matches the containing
			// field's xml tag (Go's encoding/xml enforces agreement).
			if v.XML == nil {
				v.XML = &openapi3.XML{}
			}
			if v.XML.Name == "" {
				v.XML.Name = propName
			}
			doc.Components.Schemas[nested] = &openapi3.SchemaRef{Value: v}
			hoisted = true
			return &openapi3.SchemaRef{Ref: "#/components/schemas/" + nested, Value: v}
		}
		// inline array of object — hoist the element schema. First try to
		// dedup against an existing top-level schema whose name matches the
		// array's element wire shape (typically the singular of the property
		// name, e.g. property `criterion` is already singular, property
		// `users` maps to schema `user`). Mirrors the inline-object dedup
		// above so wrapper-flattened criteria/users/computers/mobile_devices
		// arrays point at the shared component schema instead of emitting a
		// fresh per-parent Item type.
		for _, candidate := range []string{propName, singularize(propName)} {
			if existing, ok := doc.Components.Schemas[candidate]; ok && sameInlineShapeAsNamed(v.Items.Value, existing) {
				v.Items = &openapi3.SchemaRef{Ref: "#/components/schemas/" + candidate, Value: existing.Value}
				return ref
			}
		}
		nested := parentName + exportedGoName(propName) + "Item"
		nested = uniqueSchemaName(doc, nested)
		if v.Items.Value.XML == nil {
			v.Items.Value.XML = &openapi3.XML{}
		}
		if v.Items.Value.XML.Name == "" {
			// Array element wire name defaults to the Jamf convention of
			// singular of the plural property name. Best-effort — curator
			// can fix via xml metadata if needed.
			v.Items.Value.XML.Name = singularize(propName)
		}
		doc.Components.Schemas[nested] = &openapi3.SchemaRef{Value: v.Items.Value}
		v.Items = &openapi3.SchemaRef{Ref: "#/components/schemas/" + nested, Value: v.Items.Value}
		hoisted = true
		return ref
	}
	for _, propName := range sortedKeys(schema.Properties) {
		schema.Properties[propName] = lift(propName, schema.Properties[propName])
	}
	return hoisted
}

// sameInlineShapeAsNamed reports whether `inline` and `named` (a top-level
// schema ref) share the same property keyset. Used by hoistInlineObjectsInSchema
// to deduplicate inline objects against a name-matched top-level schema
// instead of emitting a near-duplicate hoisted type. Conservative — only
// matches when key counts AND keys agree exactly; nested property types
// are not compared, since OpenAPI key collisions across unrelated shapes
// are rare in Jamf's specs (and tests will catch any divergence).
func sameInlineShapeAsNamed(inline *openapi3.Schema, named *openapi3.SchemaRef) bool {
	if inline == nil || named == nil || named.Value == nil {
		return false
	}
	if len(inline.Properties) != len(named.Value.Properties) {
		return false
	}
	for k := range inline.Properties {
		if _, ok := named.Value.Properties[k]; !ok {
			return false
		}
	}
	return true
}

// singularize returns a best-effort singular form of a plural noun — used
// as the default XML element name for array items when the spec doesn't
// provide one. Handles the common English plural suffixes Jamf uses
// (-ies, -s). Curators can override via explicit xml metadata.
func singularize(plural string) string {
	switch {
	case strings.HasSuffix(plural, "ies"):
		return plural[:len(plural)-3] + "y"
	case strings.HasSuffix(plural, "ses"):
		return plural[:len(plural)-2]
	case strings.HasSuffix(plural, "s") && !strings.HasSuffix(plural, "ss"):
		return plural[:len(plural)-1]
	}
	return plural
}

// uniqueSchemaName disambiguates a proposed schema name if the name is
// already taken by an unrelated schema. Checks both the spec namespace
// (exact key collisions) AND the Go-identifier namespace produced by
// goTypeName (e.g. "computer_extension_attributesItem" and
// "computerExtensionAttributesItem" are distinct spec keys but map to
// the same exported Go type "ComputerExtensionAttributesItem"). Without
// the Go-name check the generator silently emits duplicate Go type
// declarations and the package fails to compile.
func uniqueSchemaName(doc *openapi3.T, base string) string {
	taken := func(s string) bool {
		if _, exists := doc.Components.Schemas[s]; exists {
			return true
		}
		want := goTypeName(s)
		for k := range doc.Components.Schemas {
			if goTypeName(k) == want {
				return true
			}
		}
		return false
	}
	if !taken(base) {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s%d", base, i)
		if !taken(candidate) {
			return candidate
		}
	}
}

// ---------------------------------------------------------------------------
// Shared schema walker
// ---------------------------------------------------------------------------

// newSchemaWalker returns a function that transitively walks $ref'd schemas.
// onRef is called for each unique $ref name encountered; it should return true
// if the walker should recurse into the referenced schema (i.e. it hasn't been
// visited yet in this walk's context).
func newSchemaWalker(doc *openapi3.T, onRef func(name string) bool) func(ref *openapi3.SchemaRef) {
	var walk func(ref *openapi3.SchemaRef)
	walk = func(ref *openapi3.SchemaRef) {
		if ref == nil {
			return
		}
		if ref.Ref != "" {
			parts := strings.Split(ref.Ref, "/")
			name := parts[len(parts)-1]
			if !onRef(name) {
				return
			}
			if schema, ok := doc.Components.Schemas[name]; ok {
				walk(schema)
			}
		}
		if ref.Value == nil {
			return
		}
		for _, prop := range ref.Value.Properties {
			walk(prop)
		}
		if ref.Value.Items != nil {
			walk(ref.Value.Items)
		}
		if ref.Value.AdditionalProperties.Schema != nil {
			walk(ref.Value.AdditionalProperties.Schema)
		}
		for _, s := range ref.Value.AllOf {
			walk(s)
		}
		for _, s := range ref.Value.OneOf {
			walk(s)
		}
		for _, s := range ref.Value.AnyOf {
			walk(s)
		}
	}
	return walk
}

// collectRefs walks the remaining spec paths and collects all referenced schema names,
// following nested $refs transitively. Used for pruning published specs.
func collectRefs(doc *openapi3.T, used map[string]bool, allowed map[string]bool) {
	walk := newSchemaWalker(doc, func(name string) bool {
		if used[name] {
			return false
		}
		used[name] = true
		return true
	})

	for _, path := range doc.Paths.InMatchingOrder() {
		item := doc.Paths.Find(path)
		if item == nil {
			continue
		}
		for _, p := range item.Parameters {
			if p.Value != nil && p.Value.Schema != nil {
				walk(p.Value.Schema)
			}
		}
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
			op := item.GetOperation(method)
			if op == nil {
				continue
			}
			// An OAS3 doc is loaded whole — loadSpec's allowlist only prunes
			// Swagger 2.0 paths — so a withdrawn operation still sits in
			// doc.Paths and its $refs would otherwise keep its schemas
			// reachable. Skipping them here is what lets
			// pruneUnreferencedSchemas drop a schema the whitelist no longer
			// reaches.
			if allowed != nil && !allowed[normalizeOpKey(method+" "+path)] {
				continue
			}
			for _, p := range op.Parameters {
				if p.Value != nil && p.Value.Schema != nil {
					walk(p.Value.Schema)
				}
			}
			if op.RequestBody != nil && op.RequestBody.Value != nil {
				for _, content := range op.RequestBody.Value.Content {
					if content.Schema != nil {
						walk(content.Schema)
					}
				}
			}
			if op.Responses != nil {
				for _, respRef := range op.Responses.Map() {
					if respRef.Value == nil {
						continue
					}
					for _, content := range respRef.Value.Content {
						if content.Schema != nil {
							walk(content.Schema)
						}
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Schema reference collection — determines which schemas to generate
// ---------------------------------------------------------------------------

// schemaUsage tracks whether a schema is used as a request body, response body, or both.
// Request schemas get pointer fields for unrequired scalars (to distinguish omit vs zero value).
type schemaUsage struct {
	isRequest  bool
	isResponse bool
}

// collectAllSchemas returns all component schemas in the doc, marked as both
// request and response. Used in typesOnly mode where every schema should be
// emitted regardless of operation references.
func collectAllSchemas(doc *openapi3.T) map[string]*schemaUsage {
	all := make(map[string]*schemaUsage)
	if doc.Components == nil || doc.Components.Schemas == nil {
		return all
	}
	for name := range doc.Components.Schemas {
		all[name] = &schemaUsage{isRequest: true, isResponse: true}
	}
	return all
}

// collectReferencedSchemas walks all whitelisted operations in a spec and
// transitively collects every schema name referenced by request bodies,
// responses, and their nested properties, tracking request vs response usage.
func collectReferencedSchemas(doc *openapi3.T, spec SpecDef) map[string]*schemaUsage {
	used := make(map[string]*schemaUsage)

	makeWalker := func(isRequest bool) func(ref *openapi3.SchemaRef) {
		visited := make(map[string]bool)
		return newSchemaWalker(doc, func(name string) bool {
			if visited[name] {
				return false
			}
			visited[name] = true
			if used[name] == nil {
				used[name] = &schemaUsage{}
			}
			if isRequest {
				used[name].isRequest = true
			} else {
				used[name].isResponse = true
			}
			return true
		})
	}

	for _, opDef := range spec.Operations {
		method, path := opDef.parseOp()
		pathItem := doc.Paths.Find(path)
		if pathItem == nil {
			continue
		}
		op := pathItem.GetOperation(method)
		if op == nil {
			continue
		}
		if op.RequestBody != nil && op.RequestBody.Value != nil {
			walkReq := makeWalker(true)
			for _, content := range op.RequestBody.Value.Content {
				if content.Schema != nil {
					walkReq(content.Schema)
				}
			}
		}
		if op.Responses != nil {
			walkResp := makeWalker(false)
			for _, respRef := range op.Responses.Map() {
				if respRef.Value == nil {
					continue
				}
				for _, content := range respRef.Value.Content {
					if content.Schema != nil {
						walkResp(content.Schema)
					}
				}
			}
		}
		// Named string-enum schemas reached only through a parameter. These are
		// registered so they are emitted as a Go type with constants, which is
		// what lets a caller pass pro.ComputerSectionV4General instead of the
		// bare literal "GENERAL". Before this, an enum schema reachable *only*
		// from a parameter was never emitted and the values survived solely as
		// an inline `Allowed values:` list in the method godoc — readable, but
		// not referenceable, so every call site hard-coded strings.
		//
		// Deliberately NOT a full parameter walk. Descending into parameter
		// schemas the way the request/response walkers do would register
		// arbitrary object schemas that nothing currently emits, changing the
		// type set across every spec. This registers a name only when it
		// resolves to a component that is itself a string enum, so the blast
		// radius is exactly the enum types and nothing else. Today that is 5
		// schemas, all in pro: ComputerSection, ComputerSectionV2/V3/V4 and
		// MobileDeviceSection.
		//
		// Both shapes the specs use are handled: a parameter whose schema is a
		// direct $ref, and the far more common `type: array` whose items are a
		// $ref (the repeatable `section` query param).
		registerParamEnum := func(ref *openapi3.SchemaRef) {
			if ref == nil || doc.Components == nil || doc.Components.Schemas == nil {
				return
			}
			candidates := []string{}
			if ref.Ref != "" {
				candidates = append(candidates, ref.Ref)
			}
			if ref.Value != nil && ref.Value.Items != nil && ref.Value.Items.Ref != "" {
				candidates = append(candidates, ref.Value.Items.Ref)
			}
			for _, refStr := range candidates {
				name := refStr[strings.LastIndex(refStr, "/")+1:]
				target, ok := doc.Components.Schemas[name]
				if !ok || target.Value == nil {
					continue
				}
				if !target.Value.Type.Is("string") || len(target.Value.Enum) == 0 {
					continue
				}
				if used[name] == nil {
					used[name] = &schemaUsage{}
				}
				// Read-side: a bare `= string` alias carries no fields, so the
				// request/response distinction drives nothing here. isResponse
				// is the conservative choice — it keeps the request-type
				// pointer heuristics in schemaToGoType out of play entirely.
				used[name].isResponse = true
			}
		}
		for _, paramRef := range op.Parameters {
			if paramRef != nil && paramRef.Value != nil {
				registerParamEnum(paramRef.Value.Schema)
			}
		}
		for _, paramRef := range pathItem.Parameters {
			if paramRef != nil && paramRef.Value != nil {
				registerParamEnum(paramRef.Value.Schema)
			}
		}

		// Config-level type overrides: when the spec is untyped (e.g. Classic)
		// the curator names request/response schemas explicitly. Record those
		// as referenced and descend into their properties.
		walkNamed := func(name string, isRequest bool) {
			if doc.Components == nil || doc.Components.Schemas == nil {
				return
			}
			ref, ok := doc.Components.Schemas[name]
			if !ok {
				return
			}
			// The walker only calls onRef when it encounters a $ref. A
			// top-level schema we're told to walk by name has no $ref, so
			// register it manually before descending.
			if used[name] == nil {
				used[name] = &schemaUsage{}
			}
			if isRequest {
				used[name].isRequest = true
			} else {
				used[name].isResponse = true
			}
			makeWalker(isRequest)(ref)
		}
		if opDef.RequestType != "" {
			walkNamed(opDef.RequestType, true)
		}
		if opDef.ResponseType != "" {
			walkNamed(opDef.ResponseType, false)
		}
	}
	return used
}

// ---------------------------------------------------------------------------
// Schema → Go types
// ---------------------------------------------------------------------------

// currentFieldOverrides threads the per-spec override map into extractTypes
// without adding a parameter to schemaToGoType. Set by the caller for the
// duration of the extractTypes call; intentionally package-level because
// the schema walker already relies on package-level helpers.
var currentFieldOverrides map[string]string

// suppressWriteOnly suppresses write-only comments during type extraction.
// Set to true for typesOnly specs where the writeOnly annotation is misleading.
var suppressWriteOnly bool

// currentEmitNullForOptional is the per-spec set of schemas whose optional
// pointer fields must marshal as JSON null (no ",omitempty"). Threaded the
// same way as currentFieldOverrides — set/cleared around extractTypes calls
// in emit.go. Keys are snake_case (the form schemaToGoType derives via
// goNameToSpecName), populated by buildEmitNullForOptionalSet.
var currentEmitNullForOptional map[string]bool

// currentFieldOrder carries the per-spec explicit property-emission order for
// named schemas (config FieldOrder). Threaded like currentFieldOverrides —
// set/cleared around extractTypes calls in emit.go. Outer key: spec-form
// schema name (snake_case). Value: ordered list of property names.
var currentFieldOrder map[string][]string

// buildEmitNullForOptionalSet normalises a SpecDef.EmitNullForOptional list
// into the snake_case lookup set the schema walker expects. Returns nil for
// an empty input so callers can keep the "set then nil-out" pattern.
func buildEmitNullForOptionalSet(names []string) map[string]bool {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]bool, len(names))
	for _, n := range names {
		out[toSnakeCase(n)] = true
	}
	return out
}

func extractTypes(doc *openapi3.T, allow map[string]*schemaUsage, format string) []GoType {
	names := sortedKeys(doc.Components.Schemas)
	var types []GoType

	// Inline property enums are collected as schemaToGoType walks fields, then
	// appended below. Both maps are reset per pass so one spec's enums can't
	// leak into the next spec's output. The name set is a superset of what gets
	// emitted (some allowed schemas produce no type), which only makes the
	// collision check more conservative.
	currentPropertyEnums = make(map[string]GoType)
	currentSpecTypeNames = make(map[string]bool, len(allow))
	for specName := range allow {
		currentSpecTypeNames[goTypeName(specName)] = true
	}
	defer func() {
		currentPropertyEnums = nil
		currentSpecTypeNames = nil
	}()

	for _, specName := range names {
		usage := allow[specName]
		if usage == nil {
			continue
		}
		schema := doc.Components.Schemas[specName].Value
		if schema == nil {
			continue
		}
		name := goTypeName(specName)
		xmlName := xmlWireName(specName, schema)
		// allOf composition without an explicit type: merge properties from
		// each composed schema into a single flat struct.
		if len(schema.AllOf) > 0 && (schema.Type == nil || !schema.Type.Is("object")) {
			t := schemaToGoType(name, schema, usage.isRequest, format)
			t.XMLName = xmlName
			types = append(types, t)
			continue
		}
		if schema.Type == nil {
			// oneOf + discriminator with no explicit type field (some specs
			// omit "type: object" on union roots, e.g. BookmarkItem), the
			// discriminator being inferred when the spec declares none.
			if u := unionSchema(name, schema); u != nil {
				types = append(types, schemaToDiscriminatorType(name, u))
				continue
			}
			// Swagger 2.0 often omits type: object on definitions that are
			// clearly objects (Classic spec does this). If there are
			// properties, treat it as an object anyway.
			if len(schema.Properties) > 0 {
				t := schemaToGoType(name, schema, usage.isRequest, format)
				t.XMLName = xmlName
				types = append(types, t)
			} else if isRefUnion(schema) && usage.isRequest && !usage.isResponse {
				// A request-only union of named payloads. Merging would make
				// every variant-specific field optional and let a caller
				// populate two variants at once; a pointer per variant keeps
				// the choice visible to the compiler and makes marshaling two
				// an error. See GoUnion.
				types = append(types, schemaToUnionType(name, schema))
				continue
			} else if len(schema.OneOf) > 0 {
				// oneOf with no discriminator and no properties of its own:
				// a *structurally* discriminated union, where the variant is
				// identified by which optional fields are present rather than
				// by a tag value. Merge the variants into one struct.
				//
				// Emitting nothing was the previous behaviour and it failed
				// closed only by luck: a union used as a *named* response type
				// aborts generation with "Go type not emitted", but one
				// reached solely through a property would have silently become
				// `any`. audit's AuditEnvelope is the first case — a gateway
				// event carries actor+requestContext, a service event carries
				// data, and the two never mix.
				//
				// A discriminator cannot be synthesised here: the nearest
				// candidate field (auditSource) is an open string, so a
				// mapping would rot the moment a new source appears.
				t := schemaToGoType(name, mergeOneOfVariants(schema), usage.isRequest, format)
				t.XMLName = xmlName
				types = append(types, t)
			} else if len(schema.AllOf) == 0 && len(schema.AnyOf) == 0 {
				// Completely empty schema (e.g. JsonNode: {}) — no type, no
				// properties, no composition. Treat as freeform → json.RawMessage.
				comment := name + " represents a freeform JSON value."
				if schema.Description != "" {
					comment = name + " " + lowerFirst(cleanComment(schema.Description))
				}
				types = append(types, GoType{Name: name, Comment: comment, IsRawJSON: true})
			}
			continue
		}

		// Enum string → type alias plus a constant per value.
		if schema.Type.Is("string") && len(schema.Enum) > 0 {
			types = append(types, GoType{
				Name:       name,
				Comment:    fmt.Sprintf("%s represents a %s value.", name, camelToWords(name)),
				EnumValues: enumConsts(name, schema.Enum),
			})
			continue
		}

		// Top-level array → type emission strategy depends on item shape.
		// For XML specs (Classic) the wire shape is `<root><size>N</size>
		// <resource>...</resource>*</root>`, modelled in Swagger 2.0 as
		// `type: array, items: {properties: {size, resource}}`. A raw
		// Go slice alias can't decode this — Go's xml.Unmarshal requires
		// a struct root to bind the wrapping element. Detect the {size,
		// single-ref} pattern and emit a wrapper struct that flattens
		// the item into a sibling `[]Resource` slice. Non-matching
		// arrays still get the alias treatment.
		if schema.Type.Is("array") {
			if format == "xml" {
				if wrapper, ok := classicListWrapper(name, specName, schema, doc); ok {
					types = append(types, wrapper)
					continue
				}
			}
			itemType := "any"
			if schema.Items != nil {
				itemType = schemaRefToGoType(schema.Items)
			}
			types = append(types, GoType{
				Name:        name,
				AliasTarget: "[]" + itemType,
				Comment:     fmt.Sprintf("%s is a list of %s.", name, itemType),
			})
			continue
		}

		// Top-level scalar (integer/number/string/boolean without enum) →
		// type alias. Classic uses these as shared field schemas (`size`,
		// `id_name`, etc.) referenced by $ref from other schemas. Skipping
		// them leaves the referencing struct with an undefined Go type.
		if schema.Type.Is("string") || schema.Type.Is("integer") || schema.Type.Is("number") || schema.Type.Is("boolean") {
			target := schemaRefToGoType(&openapi3.SchemaRef{Value: schema})
			types = append(types, GoType{
				Name:        name,
				AliasTarget: target,
				Comment:     fmt.Sprintf("%s is an alias for %s.", name, target),
			})
			continue
		}

		if !schema.Type.Is("object") {
			continue
		}

		// oneOf + discriminator → union type with per-variant pointer fields.
		// Skip when the discriminator mapping keys are content-addressing
		// identifiers (contain dots, e.g. "com.jamf.ddm.*"). Those are value
		// discriminants for routing/documentation, not Go-safe type tags. The
		// schema falls through to normal struct generation instead.
		//
		// A oneOf whose discriminator the spec forgot is inferred from the
		// variants' own single-value enums, which is what stops the union being
		// dropped whole — see inferDiscriminator.
		if u := unionSchema(name, schema); u != nil {
			types = append(types, schemaToDiscriminatorType(name, u))
			continue
		}

		// Freeform object (no properties): JSON specs treat as
		// json.RawMessage (the OpenAPI convention for unconstrained shape).
		// XML specs need an empty struct instead — Classic emits per-type
		// blocks like <powerbroker_identity_services/> whose presence is
		// itself semantic and must round-trip as an empty element, not a
		// freeform body.
		if len(schema.Properties) == 0 && schema.AdditionalProperties.Schema == nil {
			if format == "xml" {
				t := schemaToGoType(name, schema, usage.isRequest, format)
				t.XMLName = xmlName
				types = append(types, t)
				continue
			}
			comment := name + " represents a freeform JSON object."
			if schema.Description != "" {
				comment = name + " " + lowerFirst(cleanComment(schema.Description))
			}
			types = append(types, GoType{Name: name, Comment: comment, IsRawJSON: true})
			continue
		}

		t := schemaToGoType(name, schema, usage.isRequest, format)
		t.XMLName = xmlName
		types = append(types, t)
	}
	if format == "xml" {
		stripConflictingXMLNames(types)
		addTopLevelIDsForClassic(types)
	}
	return append(types, drainPropertyEnums()...)
}

// typeDocWidth is the wrap width for type-level documentation. Matches
// fieldDocWidth; the rendered prefix here is "// " with no leading tab, so the
// two land within a couple of columns of each other.
const typeDocWidth = 100

// applyDocNotes appends each configured note to the godoc of the type it names.
// Returns an error naming every key that matched nothing, so a note that has
// drifted off its type fails the build instead of vanishing — the wrong doc it
// was written to correct would otherwise stay in place unremarked.
//
// The note is wrapped and joined the way struct field docs are, which renders
// through the template's single "// {{ .Comment }}" line as one comment block
// per type. No IR or template change is needed for that.
func applyDocNotes(types []GoType, notes map[string]string) error {
	if len(notes) == 0 {
		return nil
	}
	applied := make(map[string]bool, len(notes))
	for i := range types {
		note, ok := notes[types[i].Name]
		if !ok {
			continue
		}
		applied[types[i].Name] = true
		lines := docParagraphs(note, typeDocWidth)
		if len(lines) == 0 {
			continue
		}
		if types[i].Comment != "" {
			types[i].Comment += "\n// "
		}
		types[i].Comment += strings.Join(lines, "\n// ")
	}
	var missing []string
	for _, name := range sortedKeys(notes) {
		if !applied[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("docNotes names no emitted type: %s", strings.Join(missing, ", "))
	}
	return nil
}

// drainPropertyEnums returns the collected inline-property enum types, name
// order, so output is stable across runs. Collisions are already resolved at
// registration time; anything reaching here is safe to declare.
func drainPropertyEnums() []GoType {
	out := make([]GoType, 0, len(currentPropertyEnums))
	for _, name := range sortedKeys(currentPropertyEnums) {
		out = append(out, currentPropertyEnums[name])
	}
	return out
}

// classicListWrapper detects the Jamf Classic list schema shape and
// returns a GoType representing a proper wrapper struct. The pattern is
// `type: array, items: {properties: {size, <resource>}}` where <resource>
// is a single named property (typically a $ref to the resource schema or
// a simple object). Returns (GoType, true) when the pattern matches;
// otherwise (GoType{}, false) so the caller falls back to the alias
// path.
//
// Emits roughly:
//
//	type Buildings struct {
//	    XMLName  xml.Name
//	    Size     *int        `xml:"size,omitempty"`
//	    Items    []Building  `xml:"building"`
//	}
//
// Tenants return the top-level <buildings> element with any number of
// <size> and <building> children at the same level, not nested under
// an intermediate wrapper. Flattening the {size, building} item pair
// into sibling fields on the wrapper matches the actual wire shape,
// where a naive []Item slice decodes to sparse pairs.
func classicListWrapper(goName, specName string, schema *openapi3.Schema, doc *openapi3.T) (GoType, bool) {
	if schema.Items == nil {
		return GoType{}, false
	}
	itemsVal := schema.Items.Value
	if itemsVal == nil || len(itemsVal.Properties) == 0 {
		return GoType{}, false
	}
	var sizeProp *openapi3.SchemaRef
	var resourceName string
	var resourceProp *openapi3.SchemaRef
	for name, prop := range itemsVal.Properties {
		if name == "size" {
			sizeProp = prop
			continue
		}
		if resourceName != "" {
			// More than one non-size property — not the Classic pattern.
			return GoType{}, false
		}
		resourceName = name
		resourceProp = prop
	}
	if resourceName == "" || resourceProp == nil {
		return GoType{}, false
	}
	// Classic spec quirk: some list specs nest the resource property as
	// `type: array` inside the items wrapper (computer_groups does this —
	// `computer_groups.items.computer_group: type: array, items: {...}`)
	// while the wire is still `<computer_groups><size/><computer_group>...
	// </computer_group>*</computer_groups>`. Without descending, the
	// wrapper emits `[][]ComputerGroupsItemComputerGroupItem` — a
	// double-slice Go can't decode against the flat repeated-child wire.
	// Drop the inner array; use its element type as the slice element.
	if resourceProp.Ref == "" && resourceProp.Value != nil &&
		resourceProp.Value.Type.Is("array") && resourceProp.Value.Items != nil {
		resourceProp = resourceProp.Value.Items
	}
	resourceGo := refName(resourceProp)
	fields := []GoField{}
	if sizeProp != nil {
		sizeGo := "*int"
		if sizeProp.Ref != "" {
			sizeGo = "*" + refName(sizeProp)
		}
		fields = append(fields, GoField{Name: "Size", Type: sizeGo, JSONTag: "size,omitempty"})
	}
	fields = append(fields, GoField{Name: exportedGoName(plural(resourceName)), Type: "[]" + resourceGo, JSONTag: resourceName})
	wrapper := GoType{
		Name:          goName,
		XMLName:       specName,
		IsListWrapper: true,
		Comment:       fmt.Sprintf("%s wraps a Jamf Classic list response with a flat slice of %s.", goName, resourceGo),
		Fields:        fields,
	}
	_ = doc
	return wrapper, true
}

// plural returns a best-effort plural form for the items-field identifier
// in a Classic list wrapper. The resource element name on the wire is
// singular (e.g. `<building>`) while the Go field holding the slice reads
// more naturally as plural (e.g. `Buildings`).
func plural(singular string) string {
	switch {
	case strings.HasSuffix(singular, "y") && len(singular) > 1 && !strings.ContainsAny(singular[len(singular)-2:len(singular)-1], "aeiou"):
		return singular[:len(singular)-1] + "ies"
	case strings.HasSuffix(singular, "s"), strings.HasSuffix(singular, "sh"), strings.HasSuffix(singular, "ch"), strings.HasSuffix(singular, "x"):
		return singular + "es"
	}
	return singular + "s"
}

// addTopLevelIDsForClassic injects a top-level `ID *int` field on Classic
// types whose id lives inside a nested General sub-object. Classic servers
// return the new record's id at the top level of the create-response body
// (<policy><id>N</id></policy>), but Jamf's spec nests id under <general>
// in the shared read schema — so the generated struct has no top-level
// ID to capture the write-response id. Without this, callers must look
// up the new record by name after every Create. The injected field is a
// no-op on reads (server never populates it there) and populates cleanly
// on writes.
func addTopLevelIDsForClassic(types []GoType) {
	// Named aliases to scalar types (e.g. `type Size = int`) are written
	// as `*Size` in fields but are not real sub-objects; without this
	// registry the loop misclassifies any type with a `*Size` field as
	// "has a sub-object" and injects a bogus top-level ID into hoisted
	// wrapper types like UserGroupCriteria / ComputerGroupComputers.
	scalarAliases := map[string]bool{}
	for _, t := range types {
		if t.AliasTarget != "" && isScalar(t.AliasTarget) {
			scalarAliases[t.Name] = true
		}
	}
	for i := range types {
		t := &types[i]
		if len(t.Fields) == 0 || t.AliasTarget != "" || t.IsRawJSON || t.IsListWrapper {
			continue
		}
		var hasSubObject, hasTopID bool
		for _, f := range t.Fields {
			if f.Name == "ID" {
				hasTopID = true
			}
			// Any pointer-to-struct field — i.e. a nested sub-object like
			// General, Connection, Scope — is a signal the server probably
			// returns id at the top level on create while the spec nests it
			// inside one of these children. Scalar ptr fields (*string, *int,
			// *bool), scalar-aliased ptr fields (*Size), and slice/map types
			// don't count.
			if strings.HasPrefix(f.Type, "*") && !strings.HasPrefix(f.Type, "*[]") &&
				!isScalar(strings.TrimPrefix(f.Type, "*")) &&
				!scalarAliases[strings.TrimPrefix(f.Type, "*")] &&
				!strings.HasPrefix(f.Type, "*map[") {
				hasSubObject = true
			}
		}
		if !hasSubObject || hasTopID {
			continue
		}
		t.Fields = append([]GoField{{
			Name:    "ID",
			Type:    "*int",
			JSONTag: "id,omitempty",
		}}, t.Fields...)
	}
}

// stripConflictingXMLNames clears the XMLName on any struct that is
// referenced as a field in another struct under a different tag. Go's
// encoding/xml refuses to unmarshal when a field tag and the target
// struct's XMLName disagree — Classic hits this via shared schemas like
// `id_name` that are embedded under parent-defined tags (`<computer>`,
// `<user>`, etc.). When a type is used only as a root (no referrers) or
// always as a matching tag, its XMLName stays. When it appears under at
// least one mismatching tag, we drop the XMLName — decoding relies on
// the parent field's tag to bind the element, and marshal of the root
// still works for fully-qualified request roots whose tag matches.
func stripConflictingXMLNames(types []GoType) {
	tagsByType := make(map[string]map[string]bool)
	for _, t := range types {
		for _, f := range t.Fields {
			ref := normalizeTypeRef(f.Type)
			if ref == "" {
				continue
			}
			tag := f.JSONTag
			if i := strings.Index(tag, ","); i >= 0 {
				tag = tag[:i]
			}
			if tagsByType[ref] == nil {
				tagsByType[ref] = map[string]bool{}
			}
			tagsByType[ref][tag] = true
		}
	}
	for i := range types {
		t := &types[i]
		if t.XMLName == "" {
			continue
		}
		tags, ok := tagsByType[t.Name]
		if !ok {
			continue
		}
		conflict := false
		for tag := range tags {
			if tag != "" && tag != t.XMLName {
				conflict = true
				break
			}
		}
		if conflict {
			t.XMLName = ""
		}
	}
}

// hasContentAddressedDiscriminator reports whether any discriminator mapping
// key contains a dot. Dot-containing keys are reverse-DNS content addresses
// (e.g. "com.jamf.ddm.passcode-settings"), not simple type discriminators.
// Schemas with such keys should not be emitted as Go union structs because
// the keys cannot become valid Go identifiers via exportedGoName.
func hasContentAddressedDiscriminator(schema *openapi3.Schema) bool {
	if schema.Discriminator == nil {
		return false
	}
	for k := range schema.Discriminator.Mapping {
		if strings.Contains(k, ".") {
			return true
		}
	}
	return false
}

// schemaToDiscriminatorType builds a GoType for a oneOf+discriminator schema.
// Variants come from the discriminator Mapping if present, else from the
// OneOf refs directly. The on-the-wire discriminator value lives in the
// mapping keys (or falls back to the Go type name) — important for specs
// where the wire value differs from the variant schema name.
func schemaToDiscriminatorType(name string, schema *openapi3.Schema) GoType {
	gt := GoType{
		Name:    name,
		Comment: fmt.Sprintf("%s is a polymorphic response keyed by %s. Exactly one variant pointer is populated after unmarshaling.", name, schema.Discriminator.PropertyName),
	}
	if schema.Description != "" {
		gt.Comment = name + " " + cleanComment(schema.Description)
	}
	gt.Discriminator = &GoDiscriminator{
		PropertyName: schema.Discriminator.PropertyName,
		GoFieldName:  exportedGoName(schema.Discriminator.PropertyName),
	}
	// Group by Go type rather than dropping repeats: several discriminator
	// values may legitimately share one variant schema, and each still needs
	// its own case in the generated switches. Deduping the *field* is right —
	// nine identical pointers would be nonsense — but deduping the *value*
	// silently strips the cases, and a caller setting one of the dropped
	// discriminators marshals to a lone discriminator with every other field
	// gone. Variant order follows first appearance, so it stays deterministic.
	byType := make(map[string]int)
	addVariant := func(value, typeName string) {
		if value == "" || typeName == "" {
			return
		}
		if i, ok := byType[typeName]; ok {
			v := &gt.Discriminator.Variants[i]
			v.Values = append(v.Values, value)
			// The field can no longer be named after a single value without
			// implying this variant is only reachable through it. Name it
			// after the schema instead, which covers every value equally.
			v.FieldName = typeName
			return
		}
		byType[typeName] = len(gt.Discriminator.Variants)
		gt.Discriminator.Variants = append(gt.Discriminator.Variants, GoDiscriminatorVariant{
			Values:    []string{value},
			TypeName:  typeName,
			FieldName: exportedGoName(value),
		})
	}
	for _, mapKey := range sortedMapKeys(schema.Discriminator.Mapping) {
		ref := schema.Discriminator.Mapping[mapKey]
		parts := strings.Split(ref.Ref, "/")
		addVariant(mapKey, exportedGoName(parts[len(parts)-1]))
	}
	if len(gt.Discriminator.Variants) == 0 {
		for _, one := range schema.OneOf {
			if one.Ref == "" {
				continue
			}
			parts := strings.Split(one.Ref, "/")
			tn := parts[len(parts)-1]
			addVariant(tn, exportedGoName(tn))
		}
	}
	gt.Discriminator.EnumTypeName = registerDiscriminatorEnum(name, gt.Discriminator)
	return gt
}

// unionSchema reports the schema to build a discriminated union from, or nil
// when this schema is not one. The declared discriminator wins; a missing one is
// inferred. Content-addressed mappings ("com.jamf.ddm.*") are refused either
// way — those keys are routing identifiers, not Go-safe type tags — and fall
// through to normal struct generation.
func unionSchema(name string, schema *openapi3.Schema) *openapi3.Schema {
	if schema == nil || len(schema.OneOf) == 0 {
		return nil
	}
	if schema.Discriminator != nil {
		if hasContentAddressedDiscriminator(schema) {
			return nil
		}
		return schema
	}
	d := inferDiscriminator(name, schema)
	if d == nil {
		return nil
	}
	candidate := withDiscriminator(schema, d)
	if hasContentAddressedDiscriminator(candidate) {
		return nil
	}
	return candidate
}

// inferDiscriminator derives the discriminator a spec forgot to declare, for a
// `oneOf` whose parent already carries the tag property and whose every variant
// pins that property to a single enum value.
//
// blueprints' SwUpdateConfiguration is the case. It declares
// `type: object`, a `oneOf` over SwUpdateManualConfiguration and
// SwUpdateAutomaticConfiguration, and one property of its own —
// `enforcementType` — but no `discriminator` object. So it fell through to plain
// struct generation and emitted **that one field**, dropping the union whole: a
// caller could read or write the enforcement type and nothing else, which is why
// terraform-provider-jamfplatform builds this component as a `map[string]any`
// and never names the type. Its sibling SwUpdateAutomaticConfiguration declares
// the identical shape *with* a discriminator and generates correctly, so the
// omission is an authoring slip rather than a different model.
//
// Nothing is guessed. Every variant constrains `enforcementType` to exactly one
// value — MANUAL on the manual variant, AUTOMATIC on both of the automatic one's
// own variants — so the mapping is read off the spec, and the discriminator ends
// up *inside* each variant where the wire already carries it. That is what
// separates this from account-sso's ConnectionRequest.connection, whose tag lives
// on the parent request and cannot be reached from the nested type: there,
// synthesising a discriminator would have emitted the tag twice and marshalled
// an empty one. See GoUnion.
//
// Deliberately strict, because a wrong tag is worse than no tag — it silently
// routes a payload to the wrong variant:
//
//   - The candidate must be declared on the parent. That is the "author wrote
//     the tag but forgot the discriminator object" shape, and it is what keeps
//     audit's AuditEnvelope out: it has no properties of its own, so nothing is
//     eligible and the structural merge still applies.
//   - Every variant must resolve the property to exactly one string value, and
//     the values must be pairwise distinct. A variant that leaves it open, or
//     two variants claiming the same value, means the property does not identify
//     anything.
//   - Resolution follows a variant's own `oneOf` and requires its members to
//     agree, which is what lets the AUTOMATIC branch resolve through
//     SwUpdateLatestConfiguration and SwUpdateSemanticConfiguration.
//   - Two candidates that both discriminate is refused rather than resolved by
//     picking the first: either would work on the wire, but they produce
//     different Go field names and a different emitted enum, so the choice is
//     visible and belongs to a human.
func inferDiscriminator(name string, schema *openapi3.Schema) *openapi3.Discriminator {
	if schema == nil || schema.Discriminator != nil || len(schema.OneOf) < 2 || len(schema.Properties) == 0 {
		return nil
	}
	for _, m := range schema.OneOf {
		if m == nil || m.Ref == "" || m.Value == nil {
			return nil
		}
	}

	var found []*openapi3.Discriminator
	for _, prop := range sortedKeys(schema.Properties) {
		decl := schema.Properties[prop]
		if decl == nil || decl.Value == nil || !decl.Value.Type.Is("string") {
			continue
		}
		mapping := make(map[string]openapi3.MappingRef, len(schema.OneOf))
		ok := true
		for _, m := range schema.OneOf {
			value, resolved := singleEnumValue(m.Value, prop, map[*openapi3.Schema]bool{})
			if !resolved || value == "" || mapping[value].Ref != "" {
				ok = false
				break
			}
			mapping[value] = openapi3.MappingRef{Ref: m.Ref}
		}
		if ok {
			found = append(found, &openapi3.Discriminator{PropertyName: prop, Mapping: mapping})
		}
	}
	switch len(found) {
	case 0:
		return nil
	case 1:
		log.Printf("%s: oneOf declares no discriminator; inferred %q from the variants' single-value enums", name, found[0].PropertyName)
		return found[0]
	default:
		names := make([]string, 0, len(found))
		for _, d := range found {
			names = append(names, d.PropertyName)
		}
		log.Printf("%s: oneOf declares no discriminator and %v all discriminate — refusing to choose; declare one upstream or pick it in config", name, names)
		return nil
	}
}

// singleEnumValue reports the one value a schema pins a property to, following
// the schema's own `oneOf` and requiring its members to agree. Returns false
// when the property is absent, is not a single-valued string enum, or when a
// nested union's members disagree.
func singleEnumValue(schema *openapi3.Schema, prop string, seen map[*openapi3.Schema]bool) (string, bool) {
	if schema == nil || seen[schema] {
		return "", false
	}
	seen[schema] = true
	props, _ := flattenAllOf(schema)
	if p, ok := props[prop]; ok && p != nil && p.Value != nil &&
		p.Value.Type.Is("string") && len(p.Value.Enum) == 1 {
		if v, ok := p.Value.Enum[0].(string); ok && v != "" {
			return v, true
		}
		return "", false
	}
	if len(schema.OneOf) == 0 {
		return "", false
	}
	agreed := ""
	for _, m := range schema.OneOf {
		if m == nil || m.Value == nil {
			return "", false
		}
		v, ok := singleEnumValue(m.Value, prop, seen)
		if !ok || (agreed != "" && v != agreed) {
			return "", false
		}
		agreed = v
	}
	return agreed, agreed != ""
}

// withDiscriminator returns a shallow copy of the schema carrying the given
// discriminator. A copy rather than a mutation: the document is reloaded for
// the publish pass, but an inferred discriminator is this generator's reading of
// the spec and must not reach api/*.json as though upstream had declared it.
func withDiscriminator(schema *openapi3.Schema, d *openapi3.Discriminator) *openapi3.Schema {
	copied := *schema
	copied.Discriminator = d
	return &copied
}

// registerDiscriminatorEnum emits an enum type for the union's discriminator
// values, so the accepted set is nameable rather than only readable off the
// generated switch. The mapping is the authoritative list: a variant's own
// `enum` covers just the values routing to that variant, and a spec that
// splits one schema into several moves values out of those enums altogether.
//
// Registered through currentPropertyEnums so it lands in enums.go beside every
// other property enum, and skipped — rather than shadowing — when a spec schema
// already claims either spelling, matching propertyEnumTypeName's rule.
func registerDiscriminatorEnum(unionName string, d *GoDiscriminator) string {
	var values []any
	for _, v := range d.Variants {
		for _, dv := range v.Values {
			values = append(values, dv)
		}
	}
	if len(values) < 2 {
		return ""
	}
	typeName := unionName + d.GoFieldName
	if currentSpecTypeNames[typeName] || currentSpecTypeNames[typeName+"Values"] {
		log.Printf("discriminator enum %s.%s: skipping — %s is already declared by a spec schema",
			unionName, d.GoFieldName, typeName)
		return ""
	}
	if _, exists := currentPropertyEnums[typeName]; exists {
		return typeName
	}
	consts := enumConsts(typeName, values)
	if len(consts) == 0 {
		return ""
	}
	currentPropertyEnums[typeName] = GoType{
		Name: typeName,
		Comment: fmt.Sprintf("%s is the set of values accepted by %s.%s.",
			typeName, unionName, d.GoFieldName),
		EnumValues: consts,
	}
	return typeName
}

// sortedMapKeys returns deterministically-ordered keys for a string-keyed map.
func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// flattenAllOf merges properties and required lists from an allOf composition
// into a single property map. Resolved schemas carried on SchemaRef.Value
// (kin-openapi pre-populates these) let us walk $refs without a separate
// lookup. Later properties override earlier ones on name collision, matching
// OpenAPI's "latest wins" semantic.
func flattenAllOf(schema *openapi3.Schema) (map[string]*openapi3.SchemaRef, []string) {
	props := make(map[string]*openapi3.SchemaRef)
	reqSet := make(map[string]bool)
	var walk func(s *openapi3.Schema)
	walk = func(s *openapi3.Schema) {
		if s == nil {
			return
		}
		maps.Copy(props, s.Properties)
		for _, r := range s.Required {
			reqSet[r] = true
		}
		for _, one := range s.AllOf {
			if one.Value != nil {
				walk(one.Value)
			}
		}
	}
	walk(schema)
	required := make([]string, 0, len(reqSet))
	for r := range reqSet {
		required = append(required, r)
	}
	sort.Strings(required)
	return props, required
}

// isRefUnion reports whether an inline schema is nothing but a
// discriminator-less `oneOf` over two or more `$ref`s — the shape a spec uses
// for "one of these named payloads, and the tag naming which is somewhere
// else".
//
// Hoisting it is what gives it a Go type at all. hasObjShape only recognises
// properties, so a union reached through a property was never lifted, never
// named, and resolved to a bare `any` in the containing struct. Every member
// must be a `$ref` so the hoisted schema's variants are types the generator
// already emits; a member declaring its own shape inline has nothing to point
// at and falls through to the merge, as before.
//
// Two members are the minimum. A one-member `oneOf` is 3.0's way of attaching
// siblings to a `$ref` and collapses elsewhere, and `oneOf: [{$ref}, {type:
// null}]` is a nullable reference that normalizeNullableUnions has already
// rewritten by the time hoisting runs.
func isRefUnion(s *openapi3.Schema) bool {
	if s == nil || len(s.OneOf) < 2 {
		return false
	}
	if (s.Type != nil && len(*s.Type) > 0) || len(s.Properties) > 0 ||
		len(s.AllOf) > 0 || len(s.AnyOf) > 0 || len(s.Enum) > 0 ||
		s.Discriminator != nil || s.AdditionalProperties.Schema != nil {
		return false
	}
	for _, m := range s.OneOf {
		if m == nil || m.Ref == "" {
			return false
		}
	}
	return true
}

// schemaToUnionType builds the GoType for an isRefUnion schema: one pointer
// field per variant, named after the variant's own type. See GoUnion for why
// this shape rather than a merge, and why the field names stutter.
func schemaToUnionType(name string, schema *openapi3.Schema) GoType {
	gt := GoType{Name: name, Comment: name + " carries the payload for exactly one of the variants below."}
	if schema.Description != "" {
		gt.Comment = name + " " + cleanComment(schema.Description)
	}
	// The contract is appended rather than substituted: the property
	// description says what the payload *is*, and this says how to supply it.
	// A reader who only ever sees the spec's sentence has no way to know that
	// setting two pointers is an error. Wrapped and joined the way applyDocNotes
	// does it, so the template's single "// {{ .Comment }}" line renders a
	// paragraph rather than one enormous line.
	contract := "Set exactly one variant pointer. The spec declares no discriminator, so nothing " +
		"here can check the choice against the sibling field that selects it; marshaling more " +
		"than one is an error, and marshaling none emits null."
	if lines := docParagraphs(contract, typeDocWidth); len(lines) > 0 {
		gt.Comment += "\n// \n// " + strings.Join(lines, "\n// ")
	}
	gt.Union = &GoUnion{}
	seen := map[string]bool{}
	for _, m := range schema.OneOf {
		tn := refName(m)
		if tn == "" || seen[tn] {
			continue
		}
		seen[tn] = true
		gt.Union.Variants = append(gt.Union.Variants, GoUnionVariant{FieldName: tn, TypeName: tn})
	}
	return gt
}

// mergeOneOfVariants collapses a discriminator-less oneOf union into a single
// schema carrying the union of every variant's properties. A property required
// by *every* variant stays required; one required by only some becomes
// optional, which is what makes the merged struct able to represent any variant
// without lying about presence. Variant-specific fields therefore land as
// pointers and nil is the caller's signal for "not this variant".
//
// The union root's own properties and required list are kept and take part in
// the intersection, so a schema mixing `oneOf` with its own `properties`
// behaves the same as one without.
func mergeOneOfVariants(schema *openapi3.Schema) *openapi3.Schema {
	merged := openapi3.NewObjectSchema()
	merged.Description = schema.Description
	merged.Properties = make(map[string]*openapi3.SchemaRef)

	if rootProps, rootRequired := flattenAllOf(schema); len(rootProps) > 0 {
		maps.Copy(merged.Properties, rootProps)
		merged.Required = rootRequired
	}

	// Intersect required across variants: counted, then kept only where the
	// count reaches the number of variants that actually resolved.
	requiredCount := make(map[string]int)
	resolved := 0
	for _, variant := range schema.OneOf {
		if variant == nil || variant.Value == nil {
			continue
		}
		resolved++
		props, required := flattenAllOf(variant.Value)
		maps.Copy(merged.Properties, props)
		for _, r := range required {
			requiredCount[r]++
		}
	}

	rootRequired := toSet(merged.Required)
	required := make([]string, 0, len(requiredCount))
	for name, n := range requiredCount {
		if n == resolved || rootRequired[name] {
			required = append(required, name)
		}
	}
	for name := range rootRequired {
		if requiredCount[name] == 0 {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	merged.Required = required
	return merged
}

// fieldDocWidth is the wrap width for struct field documentation. Matches
// parameterDocWidth; the rendered prefix is one tab plus "// ".
const fieldDocWidth = 100

// currentPropertyEnums collects the enum types synthesised for inline property
// enums during one extractTypes pass, keyed by Go type name. Follows the same
// pass-scoped-global convention as currentFieldOverrides: schemaToGoType is
// called per schema and has nowhere to return extra declarations.
var currentPropertyEnums map[string]GoType

// currentSpecTypeNames is the set of Go type names the spec itself declares in
// the current extractTypes pass. A synthesised <Owner><Property> name that
// lands on one of these must be abandoned before the field references it —
// checking at drain time instead left the field pointing at a type that has no
// constants (Deployment.State → DeploymentState, an existing struct).
var currentSpecTypeNames map[string]bool

// registerPropertyEnum synthesises an enum type for an enum declared inline on
// a property, and returns its Go type name so the field can point at it.
// Returns "" when there is nothing to synthesise.
//
// Named as <OwningType><Property>: the same property name carries conflicting
// value sets across schemas (`type` alone has 14 distinct sets across 18
// schemas), so nothing shorter is safe. Identical value-sets under different
// owners are deliberately duplicated rather than folded into one shared type —
// a shared name would have to be invented, and it would then churn whenever
// Jamf changed one owner's values but not the other's.
//
// Skipped cases, each for a reason:
//   - $ref'd properties. The field's own type already names the enum, so a
//     "see the X constants" line would just repeat the signature.
//   - Names the spec already uses for a schema (see below).
//
// Non-string and single-value enums used to be skipped too. Both are now
// emitted, because a consumer validating against the set has to get it from
// somewhere and the alternative is retyping the literals: the Terraform
// provider's enumguard exists precisely to forbid that, and it lost its
// protection three times over (CreatePathV2.Scope's one-value [APP] set, and
// uem-connect's two numeric interval enums). Numeric enums alias int or int64
// rather than string so the constants stay assignable to the field.
//   - Names the spec already uses for a schema. Redeclaring one would break
//     the build, and referencing it from the field would point the reader at a
//     type carrying no constants.
func registerPropertyEnum(ownerType, pname string, propRef *openapi3.SchemaRef) string {
	if currentPropertyEnums == nil || propRef == nil || propRef.Ref != "" || propRef.Value == nil {
		return ""
	}
	prop := propRef.Value

	// Repeatable properties model the constraint on the item schema. An item
	// $ref is skipped for the same reason a property $ref is: the element type
	// already names it.
	enumSrc := prop
	if len(enumSrc.Enum) == 0 && prop.Items != nil {
		if prop.Items.Ref != "" {
			return ""
		}
		if prop.Items.Value != nil {
			enumSrc = prop.Items.Value
		}
	}
	base := propertyEnumBase(enumSrc)
	if len(enumSrc.Enum) == 0 || base == "" {
		return ""
	}

	typeName := ownerType + exportedGoName(pname)
	// The Values accessor shares the type's namespace, so both spellings have
	// to be free before the type can be declared.
	if currentSpecTypeNames[typeName] || currentSpecTypeNames[typeName+"Values"] {
		log.Printf("property enum %s.%s: skipping — %s is already declared by a spec schema",
			ownerType, exportedGoName(pname), typeName)
		return ""
	}
	if _, exists := currentPropertyEnums[typeName]; exists {
		return typeName
	}
	consts := enumConstsOfBase(typeName, enumSrc.Enum, base)
	if len(consts) == 0 {
		return ""
	}
	currentPropertyEnums[typeName] = GoType{
		Name: typeName,
		Comment: fmt.Sprintf("%s is the set of values accepted by %s.%s.",
			typeName, ownerType, exportedGoName(pname)),
		EnumValues:   consts,
		EnumBaseType: base,
	}
	return typeName
}

// namedEnumTypes returns the Go type names of the string-enum schemas that
// extractTypes will emit for this spec, keyed for lookup. Parameter docs use
// it to point at the generated type instead of re-listing its values inline —
// the constants carry the same information, and godoc groups them under the
// type they are declared with.
//
// Derived from the same referenced-schema set extractTypes consumes, so the
// two cannot disagree about what exists. Enum schemas reached only from a
// parameter are not emitted as types, which is exactly why the check is
// needed: those keep their inline list.
func namedEnumTypes(doc *openapi3.T, refs map[string]*schemaUsage) map[string]bool {
	if doc.Components == nil || doc.Components.Schemas == nil {
		return nil
	}
	out := make(map[string]bool)
	for specName := range refs {
		ref := doc.Components.Schemas[specName]
		if ref == nil || ref.Value == nil {
			continue
		}
		if ref.Value.Type.Is("string") && len(ref.Value.Enum) > 0 {
			out[goTypeName(specName)] = true
		}
	}
	return out
}

// enumConsts turns a named string-enum schema's values into typed constants.
// Names are <TypeName><TitleCasedValue>; the wire value is what the constant
// holds, so a value the spec spells oddly still reaches the server verbatim.
// A godoc line is attached whenever the identifier does not read back as the
// value it carries — the constant for "MII_UNATHORIZED_RESPONSE_NOTIFICATION"
// should not hide the spec's misspelling from someone grepping for it.
//
// Constants are why these enums are worth generating at all: the alias is
// `type X = string`, so a constant typed X assigns to any existing `string`
// parameter with no cast and no signature change.
//
// Values that yield no identifier, and the second of any two values that
// collide on one, are skipped with a log line. Silently dropping one would
// read as "the server does not accept this".
func enumConsts(typeName string, enum []any) []GoEnumConst {
	return enumConstsOfBase(typeName, enum, "string")
}

// enumConstsOfBase renders one constant per enum value against the given Go
// base type. Values that do not match the base are skipped rather than
// coerced: a spec mixing types in one enum is a spec bug, and silently
// stringifying a number would emit a constant the field cannot accept.
//
// Numeric identifiers are the decimal digits, with a Neg prefix for negatives
// (Go identifiers cannot carry a minus sign, and -1 is a real Jamf sentinel).
// A fractional value yields no identifier and is skipped with a log line —
// none exists today and inventing a spelling for one would be guesswork.
func enumConstsOfBase(typeName string, enum []any, base string) []GoEnumConst {
	out := make([]GoEnumConst, 0, len(enum))
	seen := make(map[string]string, len(enum)) // const name → first wire value
	for _, raw := range enum {
		var value, ident, literal string
		switch base {
		case "int", "int64":
			n, ok := enumNumericValue(raw)
			if !ok {
				log.Printf("enum %s: skipping value %v — not an integer, and the alias is %s", typeName, raw, base)
				continue
			}
			value = strconv.FormatInt(n, 10)
			literal = value
			ident = value
			if n < 0 {
				ident = "Neg" + strconv.FormatInt(-n, 10)
			}
		default:
			v, ok := raw.(string)
			if !ok || v == "" {
				continue
			}
			value = v
			literal = strconv.Quote(v)
			ident = enumConstIdent(v)
		}
		if ident == "" {
			log.Printf("enum %s: skipping value %q — yields no Go identifier", typeName, value)
			continue
		}
		constName := typeName + ident
		if first, dup := seen[constName]; dup {
			log.Printf("enum %s: skipping value %q — collides with %q on %s", typeName, value, first, constName)
			continue
		}
		seen[constName] = value
		out = append(out, GoEnumConst{Name: constName, Value: value, Literal: literal})
	}
	return out
}

// enumNumericValue accepts the shapes a JSON or YAML decoder produces for an
// integer literal. kin-openapi hands back float64 for JSON numbers, so a
// whole-valued float is legitimate; a fractional one is not an integer enum.
func enumNumericValue(raw any) (int64, bool) {
	switch v := raw.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		if v != math.Trunc(v) {
			return 0, false
		}
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	}
	return 0, false
}

// propertyEnumBase reports the Go base type for an enum declared on a
// property, or "" when the property's type carries no enum the SDK emits
// constants for. int64 tracks the field's own Go type so the constants stay
// assignable: format int64 generates an int64 field, everything else int.
func propertyEnumBase(s *openapi3.Schema) string {
	switch {
	case s.Type.Is("string"):
		return "string"
	case s.Type.Is("integer"):
		if s.Format == "int64" {
			return "int64"
		}
		return "int"
	}
	return ""
}

func schemaToGoType(name string, schema *openapi3.Schema, isRequest bool, format string) GoType {
	gt := GoType{
		Name:    name,
		Comment: fmt.Sprintf("%s represents a %s.", name, camelToWords(name)),
	}
	if schema.Description != "" {
		gt.Comment = name + " " + cleanComment(schema.Description)
	}

	// specName is the lowercase_snake name the override table keys on.
	// Passed down through currentFieldOverrides; we recover it by lowering
	// the Go identifier back to snake case — good enough for the Classic
	// spec's naming which is already snake_case.
	specName := goNameToSpecName(name)

	props, requiredList := flattenAllOf(schema)
	required := toSet(requiredList)
	for _, pnameRaw := range orderedProps(props, currentFieldOrder[specName]) {
		propRef := props[pnameRaw]
		// Classic's spec encodes deprecation inline in property names
		// (e.g. `management_username deprecated="10.48"`). Everything after
		// the first whitespace is metadata the generator doesn't model.
		pname := pnameRaw
		if i := strings.IndexAny(pname, " \t"); i >= 0 {
			pname = pname[:i]
		}
		prop := propRef.Value

		goType := schemaRefToGoType(propRef)
		// Per-spec field-type override (config.fieldTypeOverrides). Applied
		// at the shallowest opportunity so needsPtr/isNullable reasoning
		// below still drives pointer wrapping exactly as it would for the
		// spec-declared type. Override lookup: exact "schema.prop" wins
		// over the "*.prop" wildcard. The wildcard lets a single entry
		// cover both a canonical schema and its hoisted list-item clones
		// (e.g. computer_invitation's invitation field appears under the
		// parent schema AND under ComputerInvitationsItemComputerInvitation
		// after array hoisting; one "*.invitation" entry handles both).
		if currentFieldOverrides != nil {
			if v, ok := currentFieldOverrides[specName+"."+pname]; ok {
				goType = v
			} else if v, ok := currentFieldOverrides["*."+pname]; ok {
				goType = v
			}
		}
		jsonTag := pname

		isNullable := prop != nil && prop.Nullable
		isRequired := required[pname]

		// Pointer for: nullable, unrequired non-scalars, or $ref to object with properties.
		// For request types, unrequired scalars also get pointers so callers can
		// distinguish "omit field" from "send zero value" (critical for PATCH).
		// $ref struct pointers only apply to response types; for request types the
		// (isRequest && !isRequired) term handles optional fields instead.
		//
		// For XML specs (Jamf Classic) every field becomes a pointer with
		// omitempty regardless of required/nullable flags. Classic consumers
		// (especially the TF provider) rely on three-state semantics: nil to
		// omit, &"" to clear, &value to set. The spec under-declares
		// nullability so the usual heuristic produces non-pointer scalars
		// that conflate "omit" and "clear" on the wire.
		isStructRef := propRef.Ref != "" && prop != nil && prop.Type != nil &&
			prop.Type.Is("object") && len(prop.Properties) > 0
		needsPtr := isNullable || (isStructRef && !isRequest) || (!isRequired && !isScalar(goType)) ||
			(isRequest && !isRequired) || format == "xml"

		if format == "xml" && !strings.HasPrefix(goType, "*") && goType != "any" {
			// XML mode: every field is `*T` with `,omitempty`, including
			// slices/maps. Matches the three-state null/empty/value
			// semantics the Terraform plugin framework depends on for
			// Classic-backed resources — a nil slice field must be
			// distinguishable from an empty slice so the provider can
			// send "omit" vs "clear" to the server.
			goType = "*" + goType
			jsonTag += ",omitempty"
		} else if isRequest && !isRequired && !strings.HasPrefix(goType, "*") && (strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "map[")) {
			// For request types, unrequired slices/maps get pointer-wrapped so
			// callers can distinguish "omit field" (nil) from "send empty" (&[]T{}).
			goType = "*" + goType
			jsonTag += ",omitempty"
		} else if needsPtr && !strings.HasPrefix(goType, "*") && !strings.HasPrefix(goType, "[]") && !strings.HasPrefix(goType, "map[") && goType != "any" {
			goType = "*" + goType
			jsonTag += ",omitempty"
		} else if isNullable && !strings.HasPrefix(goType, "*") && goType != "any" {
			goType = "*" + goType
			jsonTag += ",omitempty"
		}

		// Field godoc: the property's own description first, then the
		// write-only note as trailing metadata. Continuation lines carry
		// "\t// " because the struct template indents one level.
		var fieldDoc []string
		if prop != nil {
			fieldDoc = docParagraphs(prop.Description, fieldDocWidth)
		}
		if enumType := registerPropertyEnum(gt.Name, pname, propRef); enumType != "" {
			fieldDoc = append(fieldDoc, wrapCommentText(
				"Allowed values: see the "+enumType+" constants.", fieldDocWidth)...)
		} else if vals := inlineFieldEnumValues(propRef); len(vals) > 0 {
			// The enum exists but no Go type carries it: non-string values,
			// a single-value set, or a name collision. Without this the
			// field's only record of the constraint is the spec's own prose,
			// which routinely says "must be one of the listed durations"
			// and leaves the list to the schema — unreadable from Go.
			fieldDoc = append(fieldDoc, wrapCommentText(
				"Allowed values: "+strings.Join(vals, ", ")+".", fieldDocWidth)...)
		}
		if !suppressWriteOnly && prop != nil && (prop.WriteOnly || prop.Format == "password") {
			fieldDoc = append(fieldDoc, wrapCommentText(
				"Write-only. Servers MUST NOT return this field in responses; the SDK preserves it only so the caller can supply a value on update.",
				fieldDocWidth)...)
		}
		fieldComment := strings.Join(fieldDoc, "\n\t// ")

		// Strip ",omitempty" when the enclosing schema is listed in the
		// per-spec EmitNullForOptional set. Lets callers emit explicit JSON
		// null for unpopulated optional pointer fields — required by Pro v3
		// PUT /sso, whose validator rejects when expected fields are absent
		// from the request body. Pointer wrapping is unchanged; only the
		// marshal behaviour shifts from "omit when nil" to "emit null when
		// nil". See SpecDef.EmitNullForOptional for full rationale.
		if currentEmitNullForOptional[specName] {
			jsonTag = strings.TrimSuffix(jsonTag, ",omitempty")
		}

		gt.Fields = append(gt.Fields, GoField{
			Name:    exportedGoName(pname),
			Type:    goType,
			JSONTag: jsonTag,
			Comment: fieldComment,
		})
	}
	return gt
}

// schemaRefToGoType returns the Go type string for a schema reference.
// kin-openapi populates Value for all refs at load time, so we never
// need to manually resolve.
func schemaRefToGoType(ref *openapi3.SchemaRef) string {
	if ref.Ref != "" {
		parts := strings.Split(ref.Ref, "/")
		return goTypeName(parts[len(parts)-1])
	}
	schema := ref.Value
	if schema == nil {
		return "any"
	}
	// OpenAPI 3.0's `$ref`-with-siblings idiom. A property needing its own
	// description, `nullable` or `example` alongside a reference has to wrap
	// the reference in a single-member `allOf`, because 3.0 ignores every
	// sibling of a bare `$ref`. The wrapper declares no type and no properties
	// of its own, so without this it falls through to the default branch and
	// becomes `any` — silently, since the transport sets no
	// DisallowUnknownFields and an `any` field decodes anything. The
	// referenced struct's fields are then unreachable without a map
	// type-assert.
	//
	// aigovernance's BlueprintDeployment.lastDeployment is the live case:
	// `nullable: true` + `allOf: [{$ref: DeploymentRun}]`, which produced
	// `LastDeployment any` and hid DeploymentRun's started/state entirely.
	//
	// Only an exactly-one-member wrapper collapses. A multi-member `allOf` is
	// a real composition with no single Go type, and a schema carrying `allOf`
	// *plus* properties of its own (PolicyDetail extending PolicySummary) is a
	// named component that extractTypes flattens into its own struct — this
	// branch is only ever reached by an inline schema, since a `$ref` returns
	// above.
	//
	// Nullability is unaffected and deliberately so: the wrapper is inline, so
	// the field emitter still reads `nullable` off the wrapper rather than off
	// the shared component the reference points at. Collapsing by mutating the
	// target's Nullable — the way collapseNullableOneOf does for its $ref
	// branch — would mark that component nullable for every field in the spec
	// that references it.
	if isSingleRefAllOfWrapper(schema) {
		return schemaRefToGoType(schema.AllOf[0])
	}
	switch {
	case schema.Type.Is("string"):
		// OpenAPI format: byte means base64-encoded bytes. Go's encoding/json
		// handles base64 natively for []byte so callers work with raw bytes.
		if schema.Format == "byte" {
			return "[]byte"
		}
		// OpenAPI format: date-time is ISO 8601 / RFC 3339 per the Jamf
		// style guide. Emit as time.Time so callers get parsed timestamps
		// rather than raw strings; encoding/json handles the RFC 3339
		// round-trip natively for time.Time. Classic's XML codec also
		// honors time.Time via xml.MarshalerAttr/Unmarshaler defaults.
		if schema.Format == "date-time" {
			return "time.Time"
		}
		return "string"
	case schema.Type.Is("integer"):
		if schema.Format == "int64" {
			return "int64"
		}
		return "int"
	case schema.Type.Is("number"):
		if schema.Format == "float" {
			return "float32"
		}
		return "float64"
	case schema.Type.Is("boolean"):
		return "bool"
	case schema.Type.Is("array"):
		if schema.Items != nil {
			return "[]" + schemaRefToGoType(schema.Items)
		}
		return "[]any"
	case schema.Type.Is("object"):
		if schema.AdditionalProperties.Schema != nil {
			return "map[string]" + schemaRefToGoType(schema.AdditionalProperties.Schema)
		}
		return "map[string]any"
	default:
		return "any"
	}
}

// isSingleRefAllOfWrapper reports whether a schema is nothing but a
// single-member `allOf` — OpenAPI 3.0's only way to attach a description,
// `nullable` or an `example` to a `$ref`. Every other member of the schema
// must be empty: a type, properties, an enum, `additionalProperties` or a
// second composition keyword all mean the schema says something of its own
// that collapsing to the reference would discard.
func isSingleRefAllOfWrapper(schema *openapi3.Schema) bool {
	if schema == nil || len(schema.AllOf) != 1 || schema.AllOf[0] == nil {
		return false
	}
	if schema.Type != nil && len(*schema.Type) > 0 {
		return false
	}
	return len(schema.Properties) == 0 && len(schema.OneOf) == 0 &&
		len(schema.AnyOf) == 0 && len(schema.Enum) == 0 &&
		schema.AdditionalProperties.Schema == nil
}

// refName extracts the schema name from a $ref string, or falls back to
// computing the Go type from the inline schema.
func refName(ref *openapi3.SchemaRef) string {
	if ref.Ref != "" {
		parts := strings.Split(ref.Ref, "/")
		return goTypeName(parts[len(parts)-1])
	}
	return schemaRefToGoType(ref)
}

// goTypeName converts a spec schema name to a valid Go identifier: PascalCase,
// underscores/hyphens removed, leading lowercase letter capitalised. Platform
// specs already use PascalCase so these are no-ops; Classic uses snake_case.
func goTypeName(specName string) string {
	if specName == "" {
		return specName
	}
	// Already a canonical Go reserved name like []byte, any, map[...] etc.
	if strings.HasPrefix(specName, "[]") || strings.HasPrefix(specName, "map[") || specName == "any" {
		return specName
	}
	return exportedGoName(specName)
}

// goNameToSpecName converts a PascalCase Go type name back to its spec
// snake_case form for looking up per-field config overrides. Relies on
// the generator's round-trip property: exportedGoName("computer_invitation")
// == "ComputerInvitation", so the inverse is just splitting on case
// boundaries and re-joining with underscores (lowercased). Good enough
// for the Classic/Pro specs whose schema names are either snake_case or
// camelCase; would need refinement for specs with exotic naming.
func goNameToSpecName(goName string) string {
	return toSnakeCase(goName)
}

// xmlWireName returns the root XML element name a schema serializes to.
// Spec-level xml.name overrides take priority (e.g. computer_post -> <computer>);
// otherwise the schema's original name is used verbatim (which for Classic
// is already the wire shape since the spec is snake_case).
//
// When the spec is missing an xml.name override on a `_post` write-schema
// (Jamf Classic convention: computer_post / policy_post / user_post / etc.
// all ship with `xml.name: <resource>` except a few where it's forgotten),
// we default to the suffix-stripped name. Without this, marshal emits a
// `<ldap_server_post>` root the server rejects. Spec-level overrides
// always win when present.
func xmlWireName(specName string, schema *openapi3.Schema) string {
	if schema != nil && schema.XML != nil && schema.XML.Name != "" {
		return schema.XML.Name
	}
	if before, ok := strings.CutSuffix(specName, "_post"); ok {
		return before
	}
	return specName
}

// inlineFieldEnumValues returns the formatted enum values of an inline
// property schema, for the field-godoc fallback used when the enum has no
// generated Go type to point at. A $ref'd property (or a $ref'd item schema)
// returns nil: the field's own Go type already names the enum, so listing the
// values again on every field that carries it is duplication that rots
// independently. Values are formatted exactly as parameterEnumValues does, so
// a field and a parameter constrained by the same enum read identically.
func inlineFieldEnumValues(propRef *openapi3.SchemaRef) []string {
	if propRef == nil || propRef.Ref != "" || propRef.Value == nil {
		return nil
	}
	prop := propRef.Value
	if len(prop.Enum) == 0 && prop.Items != nil && prop.Items.Ref != "" {
		return nil
	}
	return parameterEnumValues(propRef)
}
