// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// Publish filtered specs
// ---------------------------------------------------------------------------

func publishSpecs(root string, cfg Config) error {
	outDir := filepath.Join(root, cfg.SpecDir)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("creating spec dir: %w", err)
	}

	for _, spec := range cfg.Specs {
		// Types-only specs have no operations to filter — publish the
		// source file verbatim so CI can use it as a fallback when the
		// private testing/ specs are absent.
		if spec.TypesOnly {
			if spec.SpecFile == "" {
				continue
			}
			src := filepath.Join(root, spec.File)
			data, err := os.ReadFile(src)
			if err != nil {
				return fmt.Errorf("reading typesOnly spec %s: %w", spec.File, err)
			}
			outPath := filepath.Join(outDir, spec.SpecFile)
			if err := writeGenerated(outPath, data, 0644); err != nil {
				return fmt.Errorf("writing %s: %w", outPath, err)
			}
			log.Printf("wrote %s/%s", cfg.SpecDir, spec.SpecFile)
			continue
		}
		doc, err := loadSpec(filepath.Join(root, spec.File), allowedOpsSet(spec))
		if err != nil {
			return fmt.Errorf("loading %s: %w", spec.File, err)
		}

		// Apply the same schema-correctness passes as Go code generation
		// (processSpec/processPackage), in the same order, before pruning.
		// Without this, fixups like schemaPatches, propertyRenames, and
		// PostSymmetry's read→post field mirroring appear in generated Go
		// code but not in api/*.json, leaving downstream consumers of the
		// published spec out of sync with the SDK surface. XML-specific
		// Go-type-shaping passes (flattenClassicSizeWrappers, hoistInlineObjects)
		// are deliberately excluded — they restructure schemas purely for Go
		// struct emission and don't affect what the published spec documents.
		applySchemaCreations(doc, spec.SchemaCreations)
		applySchemaRenames(doc, spec.SchemaRenames)
		applySchemaAdditions(doc, spec.SchemaAdditions)
		applyEnumAdditions(doc, spec.EnumAdditions)
		assertSchemaPatchTargetsAbsent(doc, spec.SchemaPatchesRequireAbsent)
		applySchemaPatches(doc, spec.SchemaPatches)
		applyPropertyRenames(doc, spec.PropertyRenames)
		applyPropertyRemovals(doc, spec.PropertyRemovals)
		applyPostSymmetry(doc, spec.PostSymmetryExcludes)

		specFile := toSnakeCase(doc.Info.Title) + ".json"
		if spec.SpecFile != "" {
			specFile = spec.SpecFile
		}

		// Build whitelist of path+method pairs from config.
		type pathMethod struct{ path, method string }
		allowed := make(map[pathMethod]bool)
		for _, op := range spec.Operations {
			method, path := op.parseOp()
			allowed[pathMethod{path, method}] = true
		}

		// Filter paths: remove operations not in whitelist, remove empty path items.
		for _, path := range doc.Paths.InMatchingOrder() {
			item := doc.Paths.Find(path)
			if item == nil {
				continue
			}
			for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
				if item.GetOperation(method) != nil && !allowed[pathMethod{path, method}] {
					switch method {
					case "GET":
						item.Get = nil
					case "POST":
						item.Post = nil
					case "PUT":
						item.Put = nil
					case "PATCH":
						item.Patch = nil
					case "DELETE":
						item.Delete = nil
					}
				}
			}
			// Remove path entirely if no operations remain.
			hasOps := item.Get != nil || item.Post != nil || item.Put != nil ||
				item.Patch != nil || item.Delete != nil
			if !hasOps {
				doc.Paths.Delete(path)
			}
		}

		pruneUnreferencedSchemas(doc, spec)

		// Remove internal paths (e.g. /internal/v1/...).
		for _, path := range doc.Paths.InMatchingOrder() {
			if strings.HasPrefix(path, "/internal/") {
				doc.Paths.Delete(path)
			}
		}

		// Carry the config's wire corrections into the published spec so
		// downstream generators build the same URLs and expect the same
		// statuses as this one.
		applyWireCorrectionExtensions(doc, spec)

		// Marshal to JSON.
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return fmt.Errorf("marshaling %s: %w", specFile, err)
		}
		data, err = restoreRefSiblings(filepath.Join(root, spec.File), data)
		if err != nil {
			return fmt.Errorf("restoring $ref siblings for %s: %w", specFile, err)
		}

		outPath := filepath.Join(outDir, specFile)
		if err := writeGenerated(outPath, append(data, '\n'), 0644); err != nil {
			return fmt.Errorf("writing %s: %w", outPath, err)
		}
		log.Printf("wrote %s/%s", cfg.SpecDir, specFile)
	}
	return nil
}

// restoreRefSiblings re-applies keywords that sat beside a `$ref` in the source
// spec but were destroyed when kin-openapi re-serialised the document.
//
// kin-openapi v0.146.0 parses `$ref` siblings into a *private* SchemaRef.sibling
// field (refs.go, "OAS 3.1 / JSON Schema 2020-12: sibling keywords alongside
// $ref are valid"), which is why the generator sees them and emits them as Go
// field docs. But SchemaRef.MarshalYAML re-emits only `{$ref, x-extensions}` for
// a reference, dropping sibling. So the description survives into the Go tree
// and vanishes from api/*.json.
//
// That asymmetry is a CI trap, not just missing prose. CI has no testing/ specs
// and regenerates from api/, so a sibling description present locally and absent
// in api/ makes `git diff --exit-code` fail on a tree that is genuinely current
// — the blueprints and compliance-benchmarks specs use the pattern heavily. The
// historical workaround was to revert those doc lines by hand on every regen.
//
// Rather than reach into a private field, walk the source document and the
// marshalled output in parallel and copy siblings back at matching paths.
// publishSpecs prunes operations and schemas but never relocates them, so a node
// that survives is still at its original path. Missing paths are simply skipped.
// Only non-`x-` keywords are copied; extensions already round-trip.
func restoreRefSiblings(sourcePath string, marshalled []byte) ([]byte, error) {
	raw, err := os.ReadFile(sourcePath)
	if err != nil {
		// Source spec absent means we are publishing from a fallback, which has
		// already had siblings restored by an earlier run. Nothing to do.
		return marshalled, nil
	}
	var src any
	if strings.HasSuffix(strings.ToLower(sourcePath), ".yaml") || strings.HasSuffix(strings.ToLower(sourcePath), ".yml") {
		if err := yaml.Unmarshal(raw, &src); err != nil {
			return marshalled, nil
		}
		src = yamlMapsToJSON(src)
	} else if err := json.Unmarshal(raw, &src); err != nil {
		return marshalled, nil
	}

	var out any
	if err := json.Unmarshal(marshalled, &out); err != nil {
		return nil, err
	}
	if !copyRefSiblings(src, out) {
		return marshalled, nil
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return data, nil
}

// copyRefSiblings walks src and dst in lockstep, copying `$ref` siblings from
// src onto any dst object that has been reduced to a bare `$ref`. Reports
// whether anything changed. Recurses through objects and arrays only where both
// sides agree on shape, so a pruned or restructured subtree is left alone.
func copyRefSiblings(src, dst any) bool {
	changed := false
	switch s := src.(type) {
	case map[string]any:
		d, ok := dst.(map[string]any)
		if !ok {
			return false
		}
		if _, srcHasRef := s["$ref"]; srcHasRef {
			if _, dstHasRef := d["$ref"]; dstHasRef {
				for k, v := range s {
					if k == "$ref" || strings.HasPrefix(k, "x-") {
						continue
					}
					if _, exists := d[k]; !exists {
						d[k] = v
						changed = true
					}
				}
			}
		}
		for k, sv := range s {
			if dv, exists := d[k]; exists {
				if copyRefSiblings(sv, dv) {
					changed = true
				}
			}
		}
	case []any:
		d, ok := dst.([]any)
		if !ok || len(d) != len(s) {
			return false
		}
		for i := range s {
			if copyRefSiblings(s[i], d[i]) {
				changed = true
			}
		}
	}
	return changed
}

// ---------------------------------------------------------------------------
// Wire-correction extensions
// ---------------------------------------------------------------------------

// Extension keys carrying this generator's wire corrections into the published
// spec, so downstream generators (jamf-cli) build the same URLs and expect the
// same statuses without re-deriving knowledge that only wire probing revealed.
const (
	// extTenantPathVersion is the URL version segment an operation's path
	// needs but does not carry. The Security Cloud -beta specs inject
	// /tenant/{tenantId} without the version, and the gateway answers 403
	// BAD_PERMISSIONS for the versionless form.
	extTenantPathVersion = "x-jamf-tenant-path-version"

	// extExpectedStatus is the success status the server actually answers,
	// where it differs from the one the spec declares.
	extExpectedStatus = "x-jamf-expected-status"
)

// applyWireCorrectionExtensions annotates doc with the config's path-version
// and expected-status overrides.
//
// These are published as extensions rather than folded into the paths and
// responses they correct because api/*.json doubles as this generator's own
// fallback source (see sourceSpecPath) and config.json's operation keys are
// the *source* spec's paths. Rewriting a path here would stop those keys
// matching on a fallback regen, silently dropping every operation in the spec.
func applyWireCorrectionExtensions(doc *openapi3.T, spec SpecDef) {
	if spec.Version != "" {
		if doc.Extensions == nil {
			doc.Extensions = map[string]any{}
		}
		doc.Extensions[extTenantPathVersion] = spec.Version
	}
	if doc.Paths == nil {
		return
	}
	for _, op := range spec.Operations {
		if op.Version == "" && op.ExpectedStatus == 0 {
			continue
		}
		method, path := op.parseOp()
		item := doc.Paths.Find(path)
		if item == nil {
			continue
		}
		specOp := item.GetOperation(method)
		if specOp == nil {
			continue
		}
		if specOp.Extensions == nil {
			specOp.Extensions = map[string]any{}
		}
		if op.Version != "" {
			specOp.Extensions[extTenantPathVersion] = op.Version
		}
		if op.ExpectedStatus != 0 {
			specOp.Extensions[extExpectedStatus] = op.ExpectedStatus
		}
	}
}

// pruneUnreferencedSchemas deletes every component schema not reachable from
// the whitelisted operations, either by $ref from the surviving path items or
// from a config-level requestType/responseType override. It must run after
// applyPostSymmetry — a *_post schema inherits the read sibling's properties
// by shared pointer, so pruning the read schema first would strip the post
// type — and before hoistInlineObjects, which is why both the publish path
// and the Go-generation path call it at that point.
//
// Ordering it that way is load-bearing for CI parity, not tidiness.
// hoistInlineObjects names a lifted nested object after whichever parent
// schema it reaches first in sorted order, and post-symmetry makes a read
// schema and its *_post sibling share the very SchemaRef pointers being
// lifted. So while an unreachable read schema is still in the document it
// wins the name: dropping GET /computers/id/{id} from the whitelist left
// `computer` unreachable but still in `testing/`, where it kept naming
// `ComputerGeneralManagementStatus` — while `api/`, already pruned by this
// function before publication, hoisted the identical object from
// `computer_post` as `ComputerPostGeneralManagementStatus`. CI generates from
// `api/` and the repo tree came from `testing/`, so the two disagreed and
// `git diff --exit-code -- jamfplatform/` failed on a name nothing in the
// config mentions. Pruning here makes both inputs identical by construction.
//
// The typesOnly path deliberately does not call this: those specs emit every
// schema they declare, reachability being irrelevant.
func pruneUnreferencedSchemas(doc *openapi3.T, spec SpecDef) {
	if doc == nil || doc.Components == nil || doc.Components.Schemas == nil {
		return
	}

	// Collect all $ref'd schemas from the whitelisted operations.
	usedSchemas := make(map[string]bool)
	collectRefs(doc, usedSchemas, allowedOpsSet(spec))

	// Preserve schemas named by config-level requestType/responseType
	// overrides and walk them transitively. These are *not* reachable
	// via $ref from the spec itself (Classic's operations carry no
	// typed request bodies at all — the names come from config), so
	// collectRefs misses them and they'd otherwise be pruned.
	// Without this the published spec drops every *_post schema and
	// downstream generation hits "Go type not emitted" errors.
	walk := newSchemaWalker(doc, func(name string) bool {
		if usedSchemas[name] {
			return false
		}
		usedSchemas[name] = true
		return true
	})
	for _, op := range spec.Operations {
		for _, typeName := range []string{op.RequestType, op.ResponseType} {
			if typeName == "" {
				continue
			}
			usedSchemas[typeName] = true
			if ref, ok := doc.Components.Schemas[typeName]; ok {
				walk(ref)
			}
		}
	}

	var pruned []string
	for name := range doc.Components.Schemas {
		if !usedSchemas[name] {
			delete(doc.Components.Schemas, name)
			pruned = append(pruned, name)
		}
	}
	dropDanglingComponents(doc, pruned)
}

// dropDanglingComponents removes the shared parameters, responses and request
// bodies that still $ref a schema pruneUnreferencedSchemas just deleted. They
// are reachable only from operations the whitelist does not carry, so they are
// dead, but they are published and re-read from api/ in CI, where a $ref to a
// missing schema fails the load (account-sso's `userType` parameter after the
// v2495 `/v1/users` operations arrived unwhitelisted).
func dropDanglingComponents(doc *openapi3.T, prunedSchemas []string) {
	if len(prunedSchemas) == 0 {
		return
	}
	dangles := func(v any) bool {
		raw, err := json.Marshal(v)
		if err != nil {
			return false
		}
		for _, name := range prunedSchemas {
			if strings.Contains(string(raw), `"#/components/schemas/`+name+`"`) {
				return true
			}
		}
		return false
	}
	for name, p := range doc.Components.Parameters {
		if dangles(p) {
			delete(doc.Components.Parameters, name)
		}
	}
	for name, r := range doc.Components.Responses {
		if dangles(r) {
			delete(doc.Components.Responses, name)
		}
	}
	for name, rb := range doc.Components.RequestBodies {
		if dangles(rb) {
			delete(doc.Components.RequestBodies, name)
		}
	}
}
