// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"
	"golang.org/x/tools/imports"
	"gopkg.in/yaml.v3"
)

// loadSpec reads the OpenAPI spec at path, upconverting Swagger 2.0 documents
// to OpenAPI 3 when necessary. Returns a kin-openapi v3 document the rest of
// the generator can treat uniformly.
//
// allowed is an optional allowlist of "METHOD /path" keys. For Swagger 2.0
// specs, paths not in the allowlist are pruned before conversion — Jamf's
// Classic spec has operations that openapi2conv refuses to convert (multiple
// body params) but that are outside any SDK whitelist anyway.
func loadSpec(path string, allowed map[string]bool) (*openapi3.T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Swagger 2.0 detection: the top-level "swagger" key contains "2.0".
	// Works for both JSON and YAML inputs.
	var probe struct {
		Swagger string `json:"swagger" yaml:"swagger"`
		OpenAPI string `json:"openapi" yaml:"openapi"`
	}
	if strings.HasSuffix(strings.ToLower(path), ".yaml") || strings.HasSuffix(strings.ToLower(path), ".yml") {
		_ = yaml.Unmarshal(data, &probe)
	} else {
		_ = json.Unmarshal(data, &probe)
	}
	if strings.HasPrefix(probe.Swagger, "2.") {
		// kin-openapi's openapi2.T unmarshal path expects JSON, because
		// OpenAPI 3 types nested within it have custom JSON decoders that
		// handle OAS 3.1's "type can be a string OR a list of strings"
		// union correctly. YAML decoded directly into the struct fails on
		// those fields. Convert YAML -> JSON in memory first.
		jsonData := data
		if strings.HasSuffix(strings.ToLower(path), ".yaml") || strings.HasSuffix(strings.ToLower(path), ".yml") {
			var generic any
			if err := yaml.Unmarshal(data, &generic); err != nil {
				return nil, fmt.Errorf("parsing swagger 2.0 yaml: %w", err)
			}
			generic = yamlMapsToJSON(generic)
			jsonData, err = json.Marshal(generic)
			if err != nil {
				return nil, fmt.Errorf("re-encoding swagger 2.0 yaml as json: %w", err)
			}
		}
		var v2 openapi2.T
		if err := json.Unmarshal(jsonData, &v2); err != nil {
			return nil, fmt.Errorf("parsing swagger 2.0: %w", err)
		}
		if allowed != nil {
			pruneSwagger2Paths(&v2, allowed)
		}
		basePath := v2.BasePath
		v3, err := openapi2conv.ToV3(&v2)
		if err != nil {
			return nil, err
		}
		// openapi2conv prepends v2.basePath to every path in the v3 output.
		// Strip it so path keys match what the SDK config uses (without
		// the Classic "/JSSResource/" prefix).
		if basePath != "" && basePath != "/" && v3.Paths != nil {
			trimmed := strings.TrimSuffix(basePath, "/")
			rewritten := openapi3.NewPaths()
			for _, p := range v3.Paths.InMatchingOrder() {
				key := p
				if after, ok := strings.CutPrefix(p, trimmed); ok {
					key = after
					if key == "" {
						key = "/"
					}
				}
				rewritten.Set(key, v3.Paths.Value(p))
			}
			v3.Paths = rewritten
		}
		return v3, nil
	}
	// OpenAPI 3 YAML: convert to JSON in memory so we can strip external
	// parameter $refs that kin-openapi can't resolve (no-op for specs without
	// external refs). Load from the stripped JSON bytes rather than the file.
	if strings.HasSuffix(strings.ToLower(path), ".yaml") || strings.HasSuffix(strings.ToLower(path), ".yml") {
		var generic any
		if err := yaml.Unmarshal(data, &generic); err != nil {
			return nil, fmt.Errorf("parsing yaml: %w", err)
		}
		generic = yamlMapsToJSON(generic)
		jsonData, err := json.Marshal(generic)
		if err != nil {
			return nil, fmt.Errorf("re-encoding yaml as json: %w", err)
		}
		jsonData = stripExternalParamRefs(jsonData)
		return openapi3.NewLoader().LoadFromData(jsonData)
	}
	return openapi3.NewLoader().LoadFromFile(path)
}

// stripExternalParamRefs removes parameter entries from path items and
// operation objects whose $ref points to an external file (value does not
// start with "#"). The generator adds pagination params internally; spec-level
// external param refs are pure documentation and cannot be resolved without
// the referenced file.
func stripExternalParamRefs(data []byte) []byte {
	var top map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		return data
	}
	paths, ok := top["paths"].(map[string]any)
	if !ok {
		return data
	}
	modified := false
	httpMethods := []string{"get", "post", "put", "patch", "delete", "head", "options", "trace"}
	filterParams := func(obj map[string]any) {
		params, ok := obj["parameters"].([]any)
		if !ok {
			return
		}
		filtered := make([]any, 0, len(params))
		for _, p := range params {
			pm, ok := p.(map[string]any)
			if !ok {
				filtered = append(filtered, p)
				continue
			}
			ref, _ := pm["$ref"].(string)
			if ref == "" || strings.HasPrefix(ref, "#") {
				filtered = append(filtered, p)
			} else {
				modified = true
			}
		}
		if len(filtered) == 0 {
			delete(obj, "parameters")
		} else {
			obj["parameters"] = filtered
		}
	}
	for _, item := range paths {
		pathItem, ok := item.(map[string]any)
		if !ok {
			continue
		}
		filterParams(pathItem)
		for _, method := range httpMethods {
			if op, ok := pathItem[method].(map[string]any); ok {
				filterParams(op)
			}
		}
	}
	if !modified {
		return data
	}
	result, err := json.Marshal(top)
	if err != nil {
		return data
	}
	return result
}

// allowedOpsSet builds the "METHOD /path" allowlist for a spec from its
// operations + excludePaths lists.
func allowedOpsSet(spec SpecDef) map[string]bool {
	m := make(map[string]bool, len(spec.Operations))
	for _, op := range spec.Operations {
		m[normalizeOpKey(op.Op)] = true
	}
	return m
}

// pruneSwagger2Paths drops operations from v2.Paths that aren't in the
// allowlist (keys "METHOD /path"). Leaves path items intact if at least one
// of their methods survives; otherwise removes the path entry entirely.
func pruneSwagger2Paths(v2 *openapi2.T, allowed map[string]bool) {
	for path, item := range v2.Paths {
		if item == nil {
			continue
		}
		if !allowed["GET "+path] {
			item.Get = nil
		}
		if !allowed["POST "+path] {
			item.Post = nil
		}
		if !allowed["PUT "+path] {
			item.Put = nil
		}
		if !allowed["PATCH "+path] {
			item.Patch = nil
		}
		if !allowed["DELETE "+path] {
			item.Delete = nil
		}
		if !allowed["HEAD "+path] {
			item.Head = nil
		}
		if !allowed["OPTIONS "+path] {
			item.Options = nil
		}
		if item.Get == nil && item.Post == nil && item.Put == nil && item.Patch == nil &&
			item.Delete == nil && item.Head == nil && item.Options == nil {
			delete(v2.Paths, path)
		}
	}
}

// yamlMapsToJSON recursively rewrites map[any]any (yaml.v3's map type) as
// map[string]any so encoding/json can round-trip cleanly.
func yamlMapsToJSON(v any) any {
	switch x := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[fmt.Sprint(k)] = yamlMapsToJSON(v)
		}
		return out
	case map[string]any:
		for k, v := range x {
			x[k] = yamlMapsToJSON(v)
		}
		return x
	case []any:
		for i, v := range x {
			x[i] = yamlMapsToJSON(v)
		}
		return x
	default:
		return v
	}
}

// ---------------------------------------------------------------------------
// Per-spec processing
// ---------------------------------------------------------------------------

// resolveSpecPath returns the path to load for a spec. It tries the source
// spec first (testing/), then falls back to the published spec in api/.
// This allows CI to regenerate Go code from the committed api/ specs when
// the private source specs are not available.
func resolveSpecPath(root string, cfg Config, spec SpecDef) (path string, usedFallback bool, err error) {
	primary := filepath.Join(root, spec.File)
	if _, err := os.Stat(primary); err == nil {
		return primary, false, nil
	}
	if spec.SpecFile == "" {
		return "", false, fmt.Errorf("source spec %s not found and no specFile configured for fallback", spec.File)
	}
	fallback := filepath.Join(root, cfg.SpecDir, spec.SpecFile)
	if _, err := os.Stat(fallback); err != nil {
		return "", false, fmt.Errorf("neither source spec %s nor published spec %s found", spec.File, fallback)
	}
	log.Printf("source spec %s not found, using published spec %s", spec.File, fallback)
	return fallback, true, nil
}

func processSpec(root string, cfg Config, spec SpecDef, specPath string, usedFallback bool, emittedTypes map[string]bool, rootTypes *[]GoType) error {
	doc, err := loadSpec(specPath, allowedOpsSet(spec))
	if err != nil {
		return fmt.Errorf("loading spec: %w", err)
	}

	// Fallback (api/*.json) specs already have these five passes baked in by
	// publishSpecs, in this exact order, before the file is written — see the
	// comment in publish.go. Re-running them against the already-transformed
	// doc panics or corrupts state whenever a later pass's target key/path was
	// itself produced by an earlier one (e.g. a propertyRenames source key
	// that a prior schemaPatches entry populated no longer exists once the
	// rename has already happened). applyPostSymmetry is the exception: its
	// pointer-sharing between a read schema and its *_post sibling must be
	// re-established in this process for hoistInlineObjects' dedup to work,
	// and unlike the others it's provably safe to re-run (see its doc comment).
	if !usedFallback {
		applySchemaCreations(doc, spec.SchemaCreations)
		applySchemaRenames(doc, spec.SchemaRenames)
		applySchemaAdditions(doc, spec.SchemaAdditions)
		applyEnumAdditions(doc, spec.EnumAdditions)
		assertSchemaPatchTargetsAbsent(doc, spec.SchemaPatchesRequireAbsent)
		applySchemaPatches(doc, spec.SchemaPatches)
		applyPropertyRenames(doc, spec.PropertyRenames)
		applyPropertyRemovals(doc, spec.PropertyRemovals)
	}
	normalizeNullableUnions(doc)
	applyPostSymmetry(doc, spec.PostSymmetryExcludes)
	// Must precede hoistInlineObjects and follow applyPostSymmetry — see
	// pruneUnreferencedSchemas' doc comment for why the order is what keeps
	// testing/ and api/ generating identical type names.
	pruneUnreferencedSchemas(doc, spec)
	if spec.Format == "xml" {
		flattenClassicSizeWrappers(doc)
	}
	hoistInlineObjects(doc, spec.Format)

	// Only generate schemas that are actually referenced by the whitelisted operations
	// and haven't already been emitted by a previous spec. Collected before the
	// methods so their parameter docs can point at the enum types this set will
	// produce; the pkg-level dedup below is applied after that read, since a
	// type another spec already emitted is still present in the package.
	referencedSchemas := collectReferencedSchemas(doc, spec)

	methods, err := extractMethods(doc, spec, namedEnumTypes(doc, referencedSchemas))
	if err != nil {
		return err
	}

	for name := range referencedSchemas {
		if emittedTypes[name] {
			delete(referencedSchemas, name)
		}
	}
	currentFieldOverrides = spec.FieldTypeOverrides
	currentEmitNullForOptional = buildEmitNullForOptionalSet(spec.EmitNullForOptional)
	currentFieldOrder = spec.FieldOrder
	types := extractTypes(doc, referencedSchemas, spec.Format)
	currentFieldOverrides = nil
	currentEmitNullForOptional = nil
	currentFieldOrder = nil
	if err := applyDocNotes(types, spec.DocNotes); err != nil {
		return fmt.Errorf("%s: %w", spec.File, err)
	}

	for _, t := range types {
		emittedTypes[t.Name] = true
	}

	// All root-package specs share a single Go package (legacy path), so both
	// validators have visibility into every type already emitted across prior
	// specs — a method reference must resolve against that accumulated set, and
	// a field emitted by an earlier spec is still a field in this package.
	//
	// rootTypes accumulates the whole GoType, fields included, the way
	// processPackage's allTypes does. Handing the validators a slice rebuilt
	// from emittedTypes — a set of type *names* — is what silently disabled
	// validateNoUntypedFields here: every element had a nil Fields slice, so
	// its loop body never ran and it returned success whatever was emitted.
	// validateTypeReferences only reads .Name and was unaffected, which is why
	// nothing failed. Pinned by TestProcessSpecRejectsUntypedFieldOnRootPath.
	*rootTypes = append(*rootTypes, types...)

	if err := validateNoUntypedFields(fmt.Sprintf("spec %s", spec.File), *rootTypes); err != nil {
		return err
	}
	if err := validateTypeReferences(fmt.Sprintf("spec %s", spec.File), *rootTypes, methods); err != nil {
		return err
	}
	if err := validateUnwrapCodec(fmt.Sprintf("spec %s", spec.File), methods); err != nil {
		return err
	}

	gf := GeneratedFile{
		Package: cfg.Package,
		Module:  cfg.Module,
		Format:  spec.Format,
		Types:   types,
		Methods: methods,
	}

	for _, pair := range []struct {
		tmpl *template.Template
		out  string
	}{
		{sourceTmpl, spec.outputFile()},
		{testTmpl, spec.testOutputFile()},
	} {
		var buf bytes.Buffer
		if err := pair.tmpl.Execute(&buf, gf); err != nil {
			return fmt.Errorf("executing template for %s: %w", pair.out, err)
		}
		formatted, err := imports.Process(pair.out, buf.Bytes(), &imports.Options{Comments: true})
		if err != nil {
			return fmt.Errorf("goimports %s: %w\n---raw---\n%s", pair.out, err, buf.String())
		}
		if err := writeGenerated(filepath.Join(root, pair.out), formatted, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", pair.out, err)
		}
		log.Printf("wrote %s", pair.out)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Per-package processing (sub-package emission)
// ---------------------------------------------------------------------------

// loadedSpec pairs a SpecDef with the resolved filesystem path to its spec.
type loadedSpec struct {
	spec         SpecDef
	specPath     string
	usedFallback bool
}

// processPackage emits a sub-package under jamfplatform/<pkgName>/ containing:
//   - client.go       Client struct + New(*jamfplatform.Client) constructor
//   - types.go        all referenced types deduped across specs in the package
//   - <spec>.go       methods from each spec, one file per spec
//   - <spec>_test.go  matching unit tests
//   - helpers_test.go test-only shims (testServer, WithTenantID alias, etc.)
//
// When all specs in the package are typesOnly, the output is limited to:
//   - types.go        all schemas from the spec (not just operation-referenced)
//   - types_test.go   JSON round-trip tests for each struct type
//
// Types deduplicate within the package only — sub-packages do not share type
// namespace with the root or with each other.
//
// pkgName may contain slashes (e.g. "bpcomponents/swupdate") to create nested
// directories. The Go package declaration uses only the last path segment.
func processPackage(root string, cfg Config, pkgName string, specs []loadedSpec) error {
	pkgDir := filepath.Join(root, "jamfplatform", pkgName)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return fmt.Errorf("creating package dir: %w", err)
	}

	// The Go package name is the last segment of a potentially slashed path.
	goPkgName := filepath.Base(pkgName)

	allTypesOnly := true
	for _, ls := range specs {
		if !ls.spec.TypesOnly {
			allTypesOnly = false
			break
		}
	}

	if allTypesOnly {
		return processPackageTypesOnly(root, cfg, pkgDir, goPkgName, specs)
	}

	type specWithMethods struct {
		spec     SpecDef
		methods  []GoMethod
		baseName string
	}
	var allSpecs []specWithMethods
	pkgEmitted := make(map[string]bool)
	var allTypes []GoType

	for _, ls := range specs {
		doc, err := loadSpec(ls.specPath, allowedOpsSet(ls.spec))
		if err == nil {
			// See the comment in processSpec: fallback (api/*.json) sources
			// already have these five passes baked in by publishSpecs, so
			// re-running them panics or corrupts state on cross-pass
			// dependencies (a rename source key a prior patch populated, an
			// already-deleted property, etc). applyPostSymmetry always runs —
			// its read/post pointer-sharing must happen in this process for
			// hoistInlineObjects' dedup to work, and it's safe to re-run.
			if !ls.usedFallback {
				applySchemaCreations(doc, ls.spec.SchemaCreations)
				applySchemaRenames(doc, ls.spec.SchemaRenames)
				applySchemaAdditions(doc, ls.spec.SchemaAdditions)
				applyEnumAdditions(doc, ls.spec.EnumAdditions)
				assertSchemaPatchTargetsAbsent(doc, ls.spec.SchemaPatchesRequireAbsent)
				applySchemaPatches(doc, ls.spec.SchemaPatches)
				applyPropertyRenames(doc, ls.spec.PropertyRenames)
				applyPropertyRemovals(doc, ls.spec.PropertyRemovals)
			}
			normalizeNullableUnions(doc)
			applyPostSymmetry(doc, ls.spec.PostSymmetryExcludes)
			// See pruneUnreferencedSchemas: after post-symmetry, before hoist.
			pruneUnreferencedSchemas(doc, ls.spec)
			if ls.spec.Format == "xml" {
				flattenClassicSizeWrappers(doc)
			}
			hoistInlineObjects(doc, ls.spec.Format)
		}
		if err != nil {
			return fmt.Errorf("loading %s: %w", ls.spec.File, err)
		}
		spec := ls.spec
		// Collected before the methods so their parameter docs can point at the
		// enum types this set will produce (see processSpec).
		refs := collectReferencedSchemas(doc, spec)

		methods, err := extractMethods(doc, spec, namedEnumTypes(doc, refs))
		if err != nil {
			return fmt.Errorf("spec %s: %w", spec.File, err)
		}
		allSpecs = append(allSpecs, specWithMethods{spec: spec, methods: methods, baseName: spec.baseName()})

		for name := range refs {
			if pkgEmitted[name] {
				delete(refs, name)
			}
		}
		currentFieldOverrides = spec.FieldTypeOverrides
		currentEmitNullForOptional = buildEmitNullForOptionalSet(spec.EmitNullForOptional)
		currentFieldOrder = spec.FieldOrder
		types := extractTypes(doc, refs, spec.Format)
		currentFieldOverrides = nil
		currentEmitNullForOptional = nil
		currentFieldOrder = nil
		if err := applyDocNotes(types, spec.DocNotes); err != nil {
			return fmt.Errorf("%s: %w", spec.File, err)
		}
		for _, t := range types {
			pkgEmitted[t.Name] = true
		}
		allTypes = append(allTypes, types...)
	}

	pkgFormat := ""
	if len(allSpecs) > 0 {
		pkgFormat = allSpecs[0].spec.Format
	}

	// Validate references before writing any file — an unresolved Go type
	// reference in a method will surface later as a go build error with no
	// pointer back to the spec/op responsible. The validator works on the
	// union of types emitted across all specs in this package, matching
	// the way the templates will actually see them.
	for _, sm := range allSpecs {
		if err := validateNoUntypedFields(fmt.Sprintf("spec %s (package %s)", sm.spec.File, pkgName), allTypes); err != nil {
			return err
		}
		if err := validateTypeReferences(fmt.Sprintf("spec %s (package %s)", sm.spec.File, pkgName), allTypes, sm.methods); err != nil {
			return err
		}
		if err := validateUnwrapCodec(fmt.Sprintf("spec %s (package %s)", sm.spec.File, pkgName), sm.methods); err != nil {
			return err
		}
	}

	if err := emitPkgClient(pkgDir, cfg, goPkgName); err != nil {
		return err
	}

	var allMethods []GoMethod
	for _, sm := range allSpecs {
		allMethods = append(allMethods, sm.methods...)
	}
	if err := emitPkgPrivileges(pkgDir, cfg, goPkgName, allMethods); err != nil {
		return err
	}

	structTypes, enumTypeDecls := partitionEnumTypes(allTypes)
	typesGF := GeneratedFile{Package: goPkgName, Module: cfg.Module, Format: pkgFormat, Types: structTypes}
	if err := emitTemplated(sourceTmpl, typesGF, filepath.Join(pkgDir, "types.go")); err != nil {
		return err
	}
	if err := emitPkgEnums(pkgDir, cfg, goPkgName, pkgFormat, enumTypeDecls); err != nil {
		return err
	}
	if err := emitUnionRoundTripTest(pkgDir, goPkgName, structTypes); err != nil {
		return err
	}

	// Which spec claimed each tag-derived filename, so a second spec tagging
	// its operations the same way fails loudly instead of overwriting the
	// first spec's file — the methods in it would vanish with no error from
	// the generator, the compiler or the test suite.
	tagFileOwner := make(map[string]string)

	for _, sm := range allSpecs {
		if sm.spec.SplitByTag {
			if err := emitMethodsByTag(pkgDir, cfg, goPkgName, sm.spec, sm.methods, tagFileOwner); err != nil {
				return err
			}
			continue
		}
		mf := GeneratedFile{Package: goPkgName, Module: cfg.Module, Format: sm.spec.Format, Methods: sm.methods}
		if err := emitTemplated(sourceTmpl, mf, filepath.Join(pkgDir, sm.baseName+".go")); err != nil {
			return err
		}
		if err := emitTemplated(testTmpl, mf, filepath.Join(pkgDir, sm.baseName+"_test.go")); err != nil {
			return err
		}
	}

	if err := emitPkgHelpersTest(pkgDir, cfg, goPkgName, pkgFormat); err != nil {
		return err
	}
	if err := emitPkgXMLSupplements(pkgDir, goPkgName, pkgFormat); err != nil {
		return err
	}
	if err := emitPkgXMLSupplementsTest(pkgDir, goPkgName, pkgFormat); err != nil {
		return err
	}
	if err := emitPkgPrivilegesRoundTripTest(pkgDir, goPkgName); err != nil {
		return err
	}

	// Emit versionLock helpers if any Apply method uses optimistic locking.
	hasVersionLock := false
	for _, sm := range allSpecs {
		for _, m := range sm.methods {
			if m.Apply != nil && m.Apply.VersionLock {
				hasVersionLock = true
				break
			}
		}
		if hasVersionLock {
			break
		}
	}
	if hasVersionLock {
		if err := emitPkgVersionLockHelpers(pkgDir, cfg, goPkgName); err != nil {
			return err
		}
	}
	return nil
}

// processPackageTypesOnly emits a types-only sub-package: types.go with all
// schemas from the spec, and types_test.go with JSON round-trip tests. No
// client struct, no method files, no test helpers.
func processPackageTypesOnly(root string, cfg Config, pkgDir, goPkgName string, specs []loadedSpec) error {
	pkgEmitted := make(map[string]bool)
	var allTypes []GoType
	pkgFormat := ""

	for _, ls := range specs {
		doc, err := loadSpec(ls.specPath, nil)
		if err != nil {
			return fmt.Errorf("loading %s: %w", ls.spec.File, err)
		}
		applySchemaCreations(doc, ls.spec.SchemaCreations)
		applySchemaRenames(doc, ls.spec.SchemaRenames)
		applySchemaAdditions(doc, ls.spec.SchemaAdditions)
		// Skipped on the api/ fallback: the published spec already carries the
		// added values, and applyEnumAdditions panics on a value already
		// declared. Unlike the patches around it this pass is not idempotent,
		// by design — see its doc comment.
		if !ls.usedFallback {
			applyEnumAdditions(doc, ls.spec.EnumAdditions)
		}
		assertSchemaPatchTargetsAbsent(doc, ls.spec.SchemaPatchesRequireAbsent)
		applySchemaPatches(doc, ls.spec.SchemaPatches)
		applyPropertyRenames(doc, ls.spec.PropertyRenames)
		applyPropertyRemovals(doc, ls.spec.PropertyRemovals)
		normalizeNullableUnions(doc)
		applyPostSymmetry(doc, ls.spec.PostSymmetryExcludes)
		if ls.spec.Format == "xml" {
			flattenClassicSizeWrappers(doc)
		}
		hoistInlineObjects(doc, ls.spec.Format)

		refs := collectAllSchemas(doc)
		for name := range refs {
			if pkgEmitted[name] {
				delete(refs, name)
			}
		}
		currentFieldOverrides = ls.spec.FieldTypeOverrides
		currentEmitNullForOptional = buildEmitNullForOptionalSet(ls.spec.EmitNullForOptional)
		currentFieldOrder = ls.spec.FieldOrder
		suppressWriteOnly = true
		types := extractTypes(doc, refs, ls.spec.Format)
		suppressWriteOnly = false
		currentFieldOverrides = nil
		currentEmitNullForOptional = nil
		currentFieldOrder = nil
		if err := applyDocNotes(types, ls.spec.DocNotes); err != nil {
			return fmt.Errorf("%s: %w", ls.spec.File, err)
		}
		for _, t := range types {
			pkgEmitted[t.Name] = true
		}
		allTypes = append(allTypes, types...)
		if pkgFormat == "" {
			pkgFormat = ls.spec.Format
		}
	}

	structTypes, enumTypeDecls := partitionEnumTypes(allTypes)
	typesGF := GeneratedFile{Package: goPkgName, Module: cfg.Module, Format: pkgFormat, Types: structTypes}
	if err := emitTemplated(sourceTmpl, typesGF, filepath.Join(pkgDir, "types.go")); err != nil {
		return err
	}
	if err := emitPkgEnums(pkgDir, cfg, goPkgName, pkgFormat, enumTypeDecls); err != nil {
		return err
	}
	if err := emitTypesOnlyTest(pkgDir, goPkgName, allTypes); err != nil {
		return err
	}
	return nil
}

// partitionEnumTypes splits emitted declarations into the structs and scalar
// aliases that belong in types.go, and the string enums that get their own
// file. Order within each partition is preserved so output stays stable.
//
// Enums are separated because the set is about to grow well past the handful
// of spec-named ones: every property-level enum produces a type too, and those
// have no struct to sit beside. One file for all of them beats scattering some
// next to an alias and leaving the rest floating in types.go.
func partitionEnumTypes(types []GoType) (structs, enums []GoType) {
	for _, t := range types {
		if len(t.EnumValues) > 0 {
			enums = append(enums, t)
			continue
		}
		structs = append(structs, t)
	}
	return structs, enums
}

// emitPkgEnums writes enums.go: every string-enum type in the package with its
// constants. Writes nothing when the package has no enums, and removes a stale
// file if a spec change drops the last one — a leftover enums.go would keep
// declaring types no spec backs any more.
func emitPkgEnums(pkgDir string, cfg Config, goPkgName, pkgFormat string, enums []GoType) error {
	outPath := filepath.Join(pkgDir, "enums.go")
	if len(enums) == 0 {
		if err := os.Remove(outPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing stale enums.go: %w", err)
		}
		return nil
	}
	gf := GeneratedFile{Package: goPkgName, Module: cfg.Module, Format: pkgFormat, Types: enums}
	return emitTemplated(sourceTmpl, gf, outPath)
}

// emitTypesOnlyTest writes a types_test.go file with JSON round-trip tests
// for each struct type in a types-only package. Each test constructs a
// zero-value instance, marshals it to JSON, then unmarshals back — proving
// the generated struct tags and any custom marshal/unmarshal methods work
// correctly. Discriminator types get a per-variant round-trip test that
// verifies the correct variant pointer is populated after unmarshaling.
func emitTypesOnlyTest(pkgDir, pkgName string, types []GoType) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, `// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

import (
	"encoding/json"
	"testing"
)
`, pkgName)

	for _, t := range types {
		if t.AliasTarget != "" || t.IsRawJSON {
			continue
		}
		if t.Discriminator != nil {
			// Per-variant round-trip test for discriminator (union) types —
			// one per discriminator *value*, not per variant, so a variant
			// serving several values has every case covered. A per-variant
			// test passes while the other values marshal to nothing but the
			// discriminator, which is exactly how that bug stayed hidden.
			for _, v := range t.Discriminator.Variants {
				for _, dv := range v.Values {
					fmt.Fprintf(&buf, `
func TestRoundTrip_%s_%s(t *testing.T) {
	original := %s{%s: "%s", %s: &%s{}}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %%v", err)
	}
	var decoded %s
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %%v", err)
	}
	if decoded.%s != "%s" {
		t.Errorf("discriminator = %%q, want %%q", decoded.%s, "%s")
	}
	if decoded.%s == nil {
		t.Fatal("expected variant %s to be non-nil")
	}
}
`, t.Name, exportedGoName(dv),
						t.Name, t.Discriminator.GoFieldName, dv, v.FieldName, v.TypeName,
						t.Name,
						t.Discriminator.GoFieldName, dv, t.Discriminator.GoFieldName, dv,
						v.FieldName, v.FieldName)
				}
			}
			continue
		}
		if len(t.Fields) == 0 {
			// Enum string types — no fields, just verify it compiles.
			continue
		}
		fmt.Fprintf(&buf, `
func TestRoundTrip_%s(t *testing.T) {
	original := %s{}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %%v", err)
	}
	var decoded %s
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %%v", err)
	}
}
`, t.Name, t.Name, t.Name)
	}

	outPath := filepath.Join(pkgDir, "types_test.go")
	formatted, err := formatGo("types_test.go", buf.Bytes())
	if err != nil {
		return fmt.Errorf("formatting types_test.go: %w\n---raw---\n%s", err, buf.String())
	}
	if err := writeGenerated(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	log.Printf("wrote %s", outPath)
	return nil
}

// emitPkgXMLSupplements writes xml_helpers.go into XML packages. The file
// declares the supplemental types (BigInt, NotificationValue) that
// FieldTypeOverrides target to paper over Classic spec-vs-wire mismatches.
// No-op for JSON packages. Keeping these in a single generated file
// enforces the invariant that sub-packages under jamfplatform/ contain
// only generator output — no handwritten code to drift out of sync with
// spec changes.
func emitPkgXMLSupplements(pkgDir, pkgName, pkgFormat string) error {
	if pkgFormat != "xml" {
		return nil
	}
	src := fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

// Supplemental types targeted by FieldTypeOverrides to work around Jamf
// Classic spec-vs-wire mismatches — the XML spec under-types some fields
// (integers that overflow int64, booleans whose wire form is repeated
// with a second string value) that need richer Go models to round-trip
// cleanly. Lives in the generated output so it stays in lock-step with
// any spec/override changes; do not add handwritten files to this
// package.

package %s

import (
	"encoding/xml"
	"math/big"
	"strconv"
	"strings"
)

// BigInt is an arbitrary-precision integer with XML/JSON codecs. The zero
// value is usable and equivalent to big.NewInt(0). Targeted by
// FieldTypeOverrides for Classic fields the spec types as `+"`integer`"+` whose
// actual wire values exceed int64 — canonically invitation codes and
// epoch millis beyond year ~2500.
type BigInt struct {
	v big.Int
}

// Int returns a pointer to the underlying math/big.Int so callers can do
// arithmetic without having to export the internal field. Mutations via
// the returned pointer are reflected in subsequent marshalling.
func (b *BigInt) Int() *big.Int { return &b.v }

// String returns the decimal representation, matching the wire form.
func (b BigInt) String() string { return b.v.String() }

// SetString parses a decimal integer and stores it. Returns false if the
// input isn't a valid base-10 integer.
func (b *BigInt) SetString(s string) bool {
	_, ok := b.v.SetString(s, 10)
	return ok
}

// UnmarshalXML reads the element's text value and parses it as a base-10
// integer. Empty content or a non-numeric sentinel (Classic occasionally
// emits "Unlimited" in otherwise-numeric fields) decodes to zero rather
// than erroring out. Consumers who care about sentinel detection can
// inspect the raw body via WithLogger.
func (b *BigInt) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var s string
	if err := d.DecodeElement(&s, &start); err != nil {
		return err
	}
	if s == "" {
		b.v.SetInt64(0)
		return nil
	}
	if _, ok := b.v.SetString(s, 10); !ok {
		b.v.SetInt64(0)
	}
	return nil
}

// MarshalXML emits the decimal string representation as the element's
// text content.
func (b BigInt) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	return e.EncodeElement(b.v.String(), start)
}

// UnmarshalJSON accepts either a JSON number (emitted unquoted) or a JSON
// string containing a decimal integer. Jamf APIs returning JSON
// responses can use either encoding depending on the renderer.
func (b *BigInt) UnmarshalJSON(data []byte) error {
	s := string(data)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if s == "" || s == "null" {
		b.v.SetInt64(0)
		return nil
	}
	if _, ok := b.v.SetString(s, 10); !ok {
		b.v.SetInt64(0)
	}
	return nil
}

// MarshalJSON emits the value as a JSON number (unquoted) so consumers
// see the same numeric semantics they would for a regular integer.
func (b BigInt) MarshalJSON() ([]byte, error) {
	return []byte(b.v.String()), nil
}

// NotificationValue captures the self-service notification wire element.
// The Classic server emits two <notification> tags in one <self_service>
// block — one a boolean ("true"/"false") and one naming the method
// ("Self Service", ...). A scalar *bool or *string can only capture the
// last element, and Go's XML decoder fails outright when it tries to
// ParseBool the string form. NotificationValue decodes each occurrence
// into its semantic slot (Enabled or Method) so both pieces of
// information survive round-trip, and MarshalXML writes them back as
// separate <notification> elements to preserve the expected wire shape.
type NotificationValue struct {
	Enabled *bool
	Method  *string
}

// UnmarshalXML routes the element's text to Enabled when it parses as a
// bool, otherwise into Method. Called once per <notification> element
// the decoder encounters in the parent self_service block.
func (n *NotificationValue) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var s string
	if err := d.DecodeElement(&s, &start); err != nil {
		return err
	}
	if b, err := strconv.ParseBool(s); err == nil {
		n.Enabled = &b
		return nil
	}
	m := s
	n.Method = &m
	return nil
}

// MarshalXML emits up to two <notification> elements: the method form
// first if Method is set, then the bool form if Enabled is set. Omits
// both when neither is set.
//
// Order matters. The Classic server's self_service parser takes the
// second <notification> as the "primary" value; sending bool first then
// method silently drops the bool (server returns false on the next GET
// regardless of what was sent). Wire-probed on a live tenant and
// confirmed against the order the Jamf Pro admin UI itself writes —
// method first, bool second — which is the only order that round-trips
// cleanly for every resource carrying a Notification field.
func (n NotificationValue) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	if n.Method != nil {
		if err := e.EncodeElement(*n.Method, start); err != nil {
			return err
		}
	}
	if n.Enabled != nil {
		if err := e.EncodeElement(strconv.FormatBool(*n.Enabled), start); err != nil {
			return err
		}
	}
	return nil
}

// PayloadsXMLText wraps the configuration-profile <payloads> content
// (raw .mobileconfig plist XML). MarshalXML emits a single CDATA section
// with every `+"`&`"+` escaped once; UnmarshalXML decodes the server's
// entity-escaped text form once. The two directions are deliberately
// asymmetric because the server itself is asymmetric.
//
// The Classic API does not treat <payloads> content per XML 1.0.
// Wire-verified model (2026-07-30, two Jamf Pro 11.x tenants;
// full matrix in acc_proclassic_profile_payloads_test.go):
//
//   - Validation: the server entity-decodes the submitted content once
//     and rejects the write with 409 "Unable to update the database"
//     when the result contains a bare `+"`&`"+` or `+"`<`"+`. Spec-correct raw
//     CDATA is therefore rejected for ANY plist containing `+"`&amp;`"+` or
//     `+"`&lt;`"+` — the escape-once form this type emits is the only
//     submittable one for such profiles.
//   - Storage is per-payload-type: fragments of types the server
//     re-renders (com.apple.ManagedClient.preferences custom settings —
//     values and dict keys — and com.apple.notificationsettings) are
//     entity-decoded once, so this wire form stores them byte-exact.
//     Fragments of every other payload type (TCC, direct
//     com.apple.loginwindow, all mobiledeviceconfigurationprofiles
//     payloads) are stored verbatim, keeping one extra entity layer —
//     values containing `+"`&`"+`/`+"`<`"+` in those types cannot be stored
//     faithfully by ANY client. Consumers doing read-back comparison
//     (e.g. terraform-provider-jamfplatform) surface that as drift; it
//     is a server defect, not an SDK bug.
//
// Historical note: the 2026-05-27 probes that shipped single-escape
// element text used a corpus with no `+"`&`"+`/`+"`<`"+` inside <string> values,
// so the validation 409 went unseen; the 2026-05-26 "double-escape" era
// (html.EscapeString on top of encoding/xml) corrupted content it could
// have stored. Both observations are consistent with the model above.
//
// The `+"`]]>`"+` guard keeps the emitted document well-formed (XML 1.0
// §2.4/§2.7) when a plist legitimately contains the terminator (embedded
// CDATA sections). A single CDATA section is used rather than Go's
// `+"`xml:\",cdata\"`"+` tag because that tag splits content on `+"`]]>`"+` into
// multiple sections, which the server's substring-based extractor would
// truncate.
//
// Note the server canonicalises stored plists regardless of input form:
// entities for `+"`&`"+` `+"`<`"+` `+"`>`"+`, literals for `+"`\"`"+` `+"`'`"+`, sorted keys,
// and leading/trailing whitespace inside string values is trimmed.
// Byte-identical round-trips are impossible by design; consumers must
// compare payloads structurally, never byte-wise.
//
// Line breaks inside string values are their own wire law (wire-verified
// 2026-08-03, Jamf Pro 11.30.2, both profile endpoints; rendering confirmed
// on macOS 26.6):
//
//   - Literal LF and TAB are DELETED outright — not collapsed to a space,
//     so the words either side merge — for every payload type the server
//     stores verbatim and for every slot outside the PayloadContent array
//     (e.g. the top-level ConsentText dictionary). MCX custom settings,
//     com.apple.notificationsettings and com.apple.systempolicy.control
//     keep them.
//   - CR survives everywhere, which is why this type preserves CR
//     references rather than decoding them, and why Jamf Pro's own UI emits
//     `+"`&#13;`"+` for login-window banner line breaks.
//   - U+2028, U+2029 and U+0085 also survive every slot untouched and
//     render as line breaks, so they need no special handling here.
//
// Astral-plane characters (non-BMP, e.g. emoji) are a separate server
// defect no client can work around: verbatim slots store two U+FFFD
// replacement characters in their place, and an MCX payload drops its
// entire mcx_preference_settings dictionary — taking untouched sibling keys
// with it. macOS itself handles them correctly, so consumers should surface
// this as a server-side failure rather than retry.
type PayloadsXMLText string

// minimizePlistSourceEscaping decodes every character/entity reference in
// plist XML source EXCEPT those encoding `+"`&`"+`, `+"`<`"+` and carriage
// return — i.e. `+"`&quot;`"+`/`+"`&#34;`"+` become literal `+"`\"`"+`,
// `+"`&#xA;`"+` a literal newline, `+"`&gt;`"+` a literal `+"`>`"+`, and so on.
// The parsed document is unchanged (both representations decode to identical
// values), but the representation matters on the wire: the server stores
// verbatim-category payload fragments with zero decodes, so every avoidable
// entity in the source surfaces as literal text in stored values.
// Serialisers built on encoding/xml (howett.net/plist — used by consumers
// that re-serialise payloads before sending, e.g. UUID injection) escape
// `+"`\"`"+` `+"`'`"+` and value newlines/tabs numerically, which corrupted TCC
// CodeRequirement strings on update until normalised here.
//
// Three classes must survive undecoded:
//
//   - `+"`&amp;`"+`/`+"`&lt;`"+` (and their numeric forms): decoding them would
//     make the plist source invalid XML.
//   - Carriage-return references (`+"`&#13;`"+`, `+"`&#xD;`"+`, zero-padded and
//     case variants): CR is the ONLY whitespace character Jamf Pro stores
//     inside string values — literal LF and TAB are deleted outright — and
//     XML 1.0 §2.11 line-end normalisation converts a literal CR to LF in
//     transit, so a reference is the only way to transmit one. Decoding
//     these turned a Jamf-authored multi-line value (its own UI emits
//     `+"`&#13;`"+` for banner line breaks) into content the server then
//     destroyed. Wire-verified 2026-08-03 against Jamf Pro 11.30.2 on both
//     osxconfigurationprofiles and mobiledeviceconfigurationprofiles;
//     rendering confirmed on macOS 26.6.
//
// A literal CR byte in the source is deliberately NOT re-encoded: per XML
// 1.0 §2.11 it already means LF, so promoting it to a CR reference would
// change the value.
func minimizePlistSourceEscaping(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != '&' {
			b.WriteByte(c)
			i++
			continue
		}
		semi := strings.IndexByte(s[i:], ';')
		if semi < 0 || semi > 12 {
			b.WriteByte(c)
			i++
			continue
		}
		ref := s[i+1 : i+semi]
		var r rune = -1
		switch {
		case ref == "quot":
			r = '"'
		case ref == "apos":
			r = '\''
		case ref == "gt":
			r = '>'
		case strings.HasPrefix(ref, "#x") || strings.HasPrefix(ref, "#X"):
			if n, err := strconv.ParseInt(ref[2:], 16, 32); err == nil {
				r = rune(n)
			}
		case strings.HasPrefix(ref, "#"):
			if n, err := strconv.ParseInt(ref[1:], 10, 32); err == nil {
				r = rune(n)
			}
		}
		if r < 0 || r == '&' || r == '<' || r == '\r' {
			b.WriteString(s[i : i+semi+1])
		} else {
			b.WriteRune(r)
		}
		i += semi + 1
	}
	return b.String()
}

// crRefLen reports the byte length of a carriage-return character reference
// at the start of s — `+"`&#13;`"+`, `+"`&#xD;`"+` and their zero-padded and
// case variants — or 0 when s does not start with one. Named references are
// not considered: XML 1.0 predefines none for CR.
func crRefLen(s string) int {
	if !strings.HasPrefix(s, "&#") {
		return 0
	}
	semi := strings.IndexByte(s, ';')
	if semi < 0 || semi > 12 {
		return 0
	}
	ref := s[2:semi]
	if ref == "" {
		return 0
	}
	var (
		n   int64
		err error
	)
	if ref[0] == 'x' || ref[0] == 'X' {
		n, err = strconv.ParseInt(ref[1:], 16, 32)
	} else {
		n, err = strconv.ParseInt(ref, 10, 32)
	}
	if err != nil || n != int64('\r') {
		return 0
	}
	return semi + 1
}

// escapeAmpPreservingCRRefs escapes every `+"`&`"+` once so the server's
// single entity-decode restores the minimal source — except the `+"`&`"+` that
// opens a carriage-return reference, which is emitted bare so the decode
// yields an actual CR.
//
// Escaping those too stores the reference as literal text (the extra
// entity layer), so a device would display `+"`&#13;`"+` instead of breaking
// the line. Leaving the reference bare is safe against the server's
// bare-`+"`&`"+`/`+"`<`"+` rejection because it decodes to CR, not to `+"`&`"+`
// — wire-verified 2026-08-03, Jamf Pro 11.30.2. Preserving other numeric
// references is NOT safe and must not be generalised: `+"`&#38;`"+` decodes to
// a bare `+"`&`"+` and the server rejects the write with 409.
func escapeAmpPreservingCRRefs(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		if n := crRefLen(s[i:]); n > 0 {
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		b.WriteString("&amp;")
		i++
	}
	return b.String()
}

// stripPayloadCDATASections rewrites every <![CDATA[...]]> section in a
// plist as equivalent escaped character data. The parsed document is
// unchanged (CDATA content is literal text by definition — XML 1.0
// §2.7), but the literal `+"`]]>`"+` terminators disappear so the plist can
// itself be CDATA-wrapped. Textual rewrite by design: plist
// parse/re-serialise round trips are not byte-faithful for CDATA text.
// Content after an unterminated <![CDATA[ is left untouched.
func stripPayloadCDATASections(s string) string {
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	var b strings.Builder
	for {
		si := strings.Index(s, "<![CDATA[")
		if si < 0 {
			break
		}
		ei := strings.Index(s[si:], "]]>")
		if ei < 0 {
			break
		}
		b.WriteString(s[:si])
		b.WriteString(esc.Replace(s[si+len("<![CDATA[") : si+ei]))
		s = s[si+ei+len("]]>"):]
	}
	if b.Len() == 0 {
		return s
	}
	b.WriteString(s)
	return b.String()
}

// MarshalXML emits the plist as a single CDATA section: embedded CDATA
// sections rewritten as escaped character data, source escaping minimised
// (see minimizePlistSourceEscaping — avoidable entities become literal
// text in verbatim-stored values otherwise), any `+"`]]>`"+` entity-guarded,
// and every `+"`&`"+` escaped once apart from carriage-return references (see
// escapeAmpPreservingCRRefs). The innerxml field is used because
// xml.Encoder has no raw-CDATA API. Ordering is load-bearing: CDATA
// sections must be rewritten before minimising (their content is literal
// text whose references must NOT be decoded), and the `+"`]]>`"+` guard must
// follow minimising (decoding `+"`&gt;`"+` can resurrect the terminator).
func (p PayloadsXMLText) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	s := string(p)
	if strings.Contains(s, "]]>") {
		s = stripPayloadCDATASections(s)
	}
	s = minimizePlistSourceEscaping(s)
	s = strings.ReplaceAll(s, "]]>", "]]&gt;")
	s = escapeAmpPreservingCRRefs(s)
	return e.EncodeElement(struct {
		S string `+"`xml:\",innerxml\"`"+`
	}{S: "<![CDATA[" + s + "]]>"}, start)
}

// UnmarshalXML decodes the element's text using encoding/xml's default
// pass (one level of entity unescape) and stores the result verbatim —
// correct for GET responses, which the server escapes properly. Not the
// inverse of MarshalXML: decoding our own marshalled form would return
// the pre-escaped wire bytes, but the SDK never reads its own writes.
func (p *PayloadsXMLText) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var s string
	if err := d.DecodeElement(&s, &start); err != nil {
		return err
	}
	*p = PayloadsXMLText(s)
	return nil
}
`, pkgName)
	outPath := filepath.Join(pkgDir, "xml_helpers.go")
	formatted, err := formatGo("xml_helpers.go", []byte(src))
	if err != nil {
		return fmt.Errorf("formatting xml_helpers.go: %w", err)
	}
	if err := writeGenerated(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	log.Printf("wrote %s", outPath)
	return nil
}

// emitPkgXMLSupplementsTest writes xml_helpers_test.go into XML packages,
// covering the supplemental wrapper types declared in xml_helpers.go.
// No-op for JSON packages. Lives in the generated output to keep the
// invariant that sub-packages under jamfplatform/ contain only generator
// output — no handwritten test files in package proclassic.
func emitPkgXMLSupplementsTest(pkgDir, pkgName, pkgFormat string) error {
	if pkgFormat != "xml" {
		return nil
	}
	src := fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

import (
	"encoding/xml"
	"strings"
	"testing"
)

// payloadsTestParent exercises PayloadsXMLText inside a struct field so
// the test covers both the wrapper's MarshalXML/UnmarshalXML AND the
// surrounding encoding/xml machinery (omitempty, nested struct round
// trip). Marshalling the wrapper standalone would skip those paths.
type payloadsTestParent struct {
	XMLName  xml.Name          %sxml:"parent"%s
	Payloads *PayloadsXMLText  %sxml:"payloads,omitempty"%s
}

// marshalPayloads marshals the plist through the parent struct and
// returns the raw content between the CDATA markers, failing the test
// if the wire form is not exactly one well-formed CDATA section.
func marshalPayloads(t *testing.T, plist string) string {
	t.Helper()
	v := PayloadsXMLText(plist)
	out, err := xml.Marshal(payloadsTestParent{Payloads: &v})
	if err != nil {
		t.Fatalf("marshal: %%v", err)
	}
	got := string(out)
	const openMark = "<payloads><![CDATA["
	const closeMark = "]]></payloads>"
	si := strings.Index(got, openMark)
	ei := strings.LastIndex(got, closeMark)
	if si < 0 || ei < 0 {
		t.Fatalf("wire form is not a CDATA-wrapped <payloads> element: %%s", got)
	}
	inner := got[si+len(openMark) : ei]
	if strings.Contains(inner, "]]>") {
		t.Fatalf("CDATA content contains a raw terminator — document is malformed: %%s", got)
	}
	return inner
}

// serverIngest simulates the Classic API's single entity-decode of
// MCX-family payload fragments (wire-verified 2026-07-30) — the
// storage path this wire form targets byte-exact. Other payload types
// store the wire content verbatim (see the PayloadsXMLText type comment).
// Every %s in marshalled output is part of an %s sequence, so one
// non-overlapping replacement is an exact model of the decode.
func serverIngest(inner string) string {
	return strings.ReplaceAll(inner, "&amp;", "&")
}

func TestPayloadsXMLText_ServerDecodeRestoresMinimalSource(t *testing.T) {
	// The marshalled form must be exactly one escape layer above the
	// minimal-escaping plist source: the server's single decode restores
	// that source, which parses to the same values as the input. Covers
	// every reserved character in every legal representation.
	plists := []string{
		"<string>R&amp;D</string>",
		"<string>a &lt; b &gt; c</string>",
		"<string>A &#38; B &#x26; C</string>",
		"<string>x > y</string>",
		%s<string>q " q and p ' p</string>%s,
		"<string>q &quot; q and p &#39; p and tab&#9;end</string>",
		"<string>literal &amp;amp; entity text</string>",
		"<string>target.signing_time >= timestamp('2025-05-31T00:00:00Z') &amp;&amp; ok</string>",
		%sidentifier "com.foo" and anchor apple generic%s,
		%sidentifier &#34;com.foo&#34; and anchor apple generic%s,
		"<string>ALPHA&#13;BRAVO</string>",
		"<string>ALPHA&#xD;BRAVO and R&amp;D</string>",
	}
	for _, p := range plists {
		want := strings.ReplaceAll(minimizePlistSourceEscaping(p), "]]>", "]]&gt;")
		if got := serverIngest(marshalPayloads(t, p)); got != want {
			t.Fatalf("server would store %%q, want %%q", got, want)
		}
	}
}

func TestPayloadsXMLText_MinimizeSourceEscaping(t *testing.T) {
	// Avoidable references decode to literal characters; the references
	// encoding %s and %s (named or numeric) must survive untouched, as
	// must non-reference ampersands, unknown entities, and every form of
	// carriage-return reference (the one whitespace character Jamf Pro
	// stores, unreachable as a literal because XML normalises it to LF).
	cases := map[string]string{
		"&#13;":         "&#13;",
		"&#013;":        "&#013;",
		"&#xD;":         "&#xD;",
		"&#x0d;":        "&#x0d;",
		"&quot;":        %s"%s,
		"&#34;":         %s"%s,
		"&#x22;":        %s"%s,
		"&apos;":        "'",
		"&#39;":         "'",
		"&gt;":          ">",
		"&#62;":         ">",
		"&#xA;":         "\n",
		"&#9;":          "\t",
		"&amp;":         "&amp;",
		"&lt;":          "&lt;",
		"&#38;":         "&#38;",
		"&#x26;":        "&#x26;",
		"&#60;":         "&#60;",
		"&#x3C;":        "&#x3C;",
		"&bogus;":       "&bogus;",
		"A & B; C":      "A & B; C",
		"&amp;#34;":     "&amp;#34;",
		"a]]&gt;b":      "a]]>b",
	}
	for in, want := range cases {
		if got := minimizePlistSourceEscaping(in); got != want {
			t.Fatalf("minimize(%%q) = %%q, want %%q", in, got, want)
		}
	}
}

func TestPayloadsXMLText_CRReferencesReachServerBare(t *testing.T) {
	// A carriage-return reference must reach the server AS a reference,
	// ampersand unescaped, so its single decode yields an actual CR. Jamf
	// Pro deletes literal LF/TAB from verbatim-stored values and XML
	// line-end normalisation would turn a literal CR into LF in transit, so
	// this is the only representation of a line break that survives — and
	// the one Jamf Pro's own UI emits for login-window banners.
	for _, ref := range []string{"&#13;", "&#013;", "&#xD;", "&#x0d;"} {
		inner := marshalPayloads(t, "<string>ALPHA"+ref+"BRAVO</string>")
		if want := "<string>ALPHA" + ref + "BRAVO</string>"; inner != want {
			t.Fatalf("wire form %%q, want %%q", inner, want)
		}
		if strings.Contains(inner, "&amp;") {
			t.Fatalf("CR reference %%q was escaped — the server would store it as literal text: %%s", ref, inner)
		}
	}
}

func TestPayloadsXMLText_OnlyCRReferencesLeftBare(t *testing.T) {
	// The exemption must NOT generalise to other numeric references: the
	// numeric ampersand decodes to a bare ampersand, which the server
	// rejects with 409, and preserved quote references store as literal
	// text (wire-verified 2026-08-03). Everything but
	// CR therefore stays escaped once (or, when it decodes to a harmless
	// literal, arrives as that literal with no ampersand at all).
	cases := map[string]string{
		"&#38;":  "&amp;#38;",
		"&#x26;": "&amp;#x26;",
		"&#60;":  "&amp;#60;",
		"&#x3C;": "&amp;#x3C;",
		"&amp;":  "&amp;amp;",
		"&lt;":   "&amp;lt;",
		"&#39;":  "'",
		"&#9;":   "\t",
		"&#xA;":  "\n",
	}
	for in, want := range cases {
		inner := marshalPayloads(t, "<string>ALPHA"+in+"BRAVO</string>")
		if got := "<string>ALPHA" + want + "BRAVO</string>"; inner != got {
			t.Fatalf("marshal(%%q) wire form = %%q, want %%q", in, inner, got)
		}
	}
}

func TestPayloadsXMLText_EmbeddedCDATARewrittenNotTruncated(t *testing.T) {
	// A plist may legitimately contain a CDATA section; its literal
	// terminator must not end the wire wrapper early. The section is
	// rewritten as equivalent escaped character data (value-preserving,
	// including edge whitespace — the reason this is textual, not a
	// plist re-serialise).
	inner := marshalPayloads(t, "<string><![CDATA[x & y ]]></string>")
	want := "<string>x &amp; y </string>"
	if got := serverIngest(inner); got != want {
		t.Fatalf("server would store %%q, want %%q", got, want)
	}
}

func TestPayloadsXMLText_StrayTerminatorGuarded(t *testing.T) {
	// A bare "]]>" outside any CDATA section (malformed plist input) is
	// entity-guarded rather than emitted raw; the server decode leaves
	// the valid entity form in the stored plist.
	inner := marshalPayloads(t, "<string>a]]>b</string>")
	want := "<string>a]]&gt;b</string>"
	if got := serverIngest(inner); got != want {
		t.Fatalf("server would store %%q, want %%q", got, want)
	}
}

func TestPayloadsXMLText_UnmarshalSingleDecodes(t *testing.T) {
	// Server returns chardata at one level of XML entity encoding (GET
	// responses are escaped correctly — the ingest quirk is write-only).
	// The decoder unescapes once; the wrapper stores the result verbatim.
	wire := []byte("<parent><payloads>ab&amp;cd&lt;br/&gt;ef</payloads></parent>")
	var got payloadsTestParent
	if err := xml.Unmarshal(wire, &got); err != nil {
		t.Fatalf("unmarshal: %%v", err)
	}
	if got.Payloads == nil {
		t.Fatalf("decoded payloads is nil")
	}
	want := "ab&cd<br/>ef"
	if string(*got.Payloads) != want {
		t.Fatalf("decode mismatch:\n  want: %%s\n  got:  %%s", want, string(*got.Payloads))
	}
}

func TestPayloadsXMLText_NilOmitsField(t *testing.T) {
	parent := payloadsTestParent{}
	out, err := xml.Marshal(parent)
	if err != nil {
		t.Fatalf("marshal: %%v", err)
	}
	if strings.Contains(string(out), "<payloads") {
		t.Fatalf("expected <payloads> omitted when field is nil, got: %%s", out)
	}
}
`, pkgName, "`", "`", "`", "`",
		"`&`", "`&amp;`",
		"`", "`",
		"`", "`",
		"`", "`",
		"`&`", "`<`",
		"`", "`",
		"`", "`",
		"`", "`")
	outPath := filepath.Join(pkgDir, "xml_helpers_test.go")
	formatted, err := formatGo("xml_helpers_test.go", []byte(src))
	if err != nil {
		return fmt.Errorf("formatting xml_helpers_test.go: %w\n---raw---\n%s", err, src)
	}
	if err := writeGenerated(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	log.Printf("wrote %s", outPath)
	return nil
}

// emitPkgPrivilegesRoundTripTest writes accounts_privileges_test.go into the
// proclassic package. It proves that AccountPrivileges and GroupPrivileges
// marshal/unmarshal without privilege collapse — each category must emit a
// single container element with N <privilege> children, not N repeated
// container elements (one per privilege). Also covers Group.LdapServer
// round-trip using the real DataJARLDAPS_JamfPro_Admins wire fixture.
// No-op for all other packages.
func emitPkgPrivilegesRoundTripTest(pkgDir, pkgName string) error {
	if pkgName != "proclassic" {
		return nil
	}
	src := fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

import (
	"encoding/xml"
	"strings"
	"testing"
)

// TestGroupPrivileges_RoundTrip_MarshalXMLCategory verifies that a
// MarshalXML-bearing category (casper_admin) emits ONE container element
// with N <privilege> children — not N repeated container elements. Mirrors
// the class-fix regression guard.
func TestGroupPrivileges_RoundTrip_MarshalXMLCategory(t *testing.T) {
	privs := []string{"Read Computers", "Update Computers", "Create Computers"}
	g := Group{
		Privileges: &GroupPrivileges{
			CasperAdmin: &GroupPrivilegesCasperAdmin{Privilege: &privs},
		},
	}
	out, err := xml.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %%v", err)
	}
	got := string(out)
	if n := strings.Count(got, "<casper_admin>"); n != 1 {
		t.Fatalf("want 1 <casper_admin> wrapper, got %%d:\n%%s", n, got)
	}
	if n := strings.Count(got, "<privilege>"); n != 3 {
		t.Fatalf("want 3 <privilege> children, got %%d:\n%%s", n, got)
	}

	var back Group
	if err := xml.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %%v", err)
	}
	if back.Privileges == nil || back.Privileges.CasperAdmin == nil || back.Privileges.CasperAdmin.Privilege == nil {
		t.Fatal("privileges.casper_admin decoded as nil")
	}
	got2 := *back.Privileges.CasperAdmin.Privilege
	if len(got2) != 3 {
		t.Fatalf("want 3 privileges after unmarshal, got %%d: %%v", len(got2), got2)
	}
	for i, want := range privs {
		if got2[i] != want {
			t.Fatalf("privilege[%%d]: want %%q, got %%q", i, want, got2[i])
		}
	}
}

// TestGroupPrivileges_RoundTrip_PlainCategory verifies that a plain category
// (jss_objects, no custom MarshalXML on the item type) also emits ONE
// container with N children.
func TestGroupPrivileges_RoundTrip_PlainCategory(t *testing.T) {
	privs := []string{"Create Computers", "Read Computers", "Update Computers"}
	g := Group{
		Privileges: &GroupPrivileges{
			JssObjects: &GroupPrivilegesJssObjects{Privilege: &privs},
		},
	}
	out, err := xml.Marshal(g)
	if err != nil {
		t.Fatalf("marshal: %%v", err)
	}
	got := string(out)
	if n := strings.Count(got, "<jss_objects>"); n != 1 {
		t.Fatalf("want 1 <jss_objects> wrapper, got %%d:\n%%s", n, got)
	}
	if n := strings.Count(got, "<privilege>"); n != 3 {
		t.Fatalf("want 3 <privilege> children, got %%d:\n%%s", n, got)
	}

	var back Group
	if err := xml.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %%v", err)
	}
	if back.Privileges == nil || back.Privileges.JssObjects == nil || back.Privileges.JssObjects.Privilege == nil {
		t.Fatal("privileges.jss_objects decoded as nil")
	}
	got2 := *back.Privileges.JssObjects.Privilege
	if len(got2) != 3 {
		t.Fatalf("want 3 privileges after unmarshal, got %%d: %%v", len(got2), got2)
	}
	for i, want := range privs {
		if got2[i] != want {
			t.Fatalf("privilege[%%d]: want %%q, got %%q", i, want, got2[i])
		}
	}
}

// TestAccountPrivileges_RoundTrip_MarshalXMLCategory mirrors the group test
// for Account privileges (same spec defect, same fix, both must pass).
func TestAccountPrivileges_RoundTrip_MarshalXMLCategory(t *testing.T) {
	privs := []string{"Read Computers", "Update Computers", "Create Computers"}
	a := Account{
		Privileges: &AccountPrivileges{
			CasperAdmin: &AccountPrivilegesCasperAdmin{Privilege: &privs},
		},
	}
	out, err := xml.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %%v", err)
	}
	got := string(out)
	if n := strings.Count(got, "<casper_admin>"); n != 1 {
		t.Fatalf("want 1 <casper_admin> wrapper, got %%d:\n%%s", n, got)
	}
	if n := strings.Count(got, "<privilege>"); n != 3 {
		t.Fatalf("want 3 <privilege> children, got %%d:\n%%s", n, got)
	}

	var back Account
	if err := xml.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %%v", err)
	}
	if back.Privileges == nil || back.Privileges.CasperAdmin == nil || back.Privileges.CasperAdmin.Privilege == nil {
		t.Fatal("privileges.casper_admin decoded as nil")
	}
	got2 := *back.Privileges.CasperAdmin.Privilege
	if len(got2) != 3 {
		t.Fatalf("want 3 privileges after unmarshal, got %%d: %%v", len(got2), got2)
	}
	for i, want := range privs {
		if got2[i] != want {
			t.Fatalf("privilege[%%d]: want %%q, got %%q", i, want, got2[i])
		}
	}
}

// TestAccountPrivileges_RoundTrip_PlainCategory mirrors the plain-category
// group test for Account.
func TestAccountPrivileges_RoundTrip_PlainCategory(t *testing.T) {
	privs := []string{"Create Computers", "Read Computers", "Update Computers"}
	a := Account{
		Privileges: &AccountPrivileges{
			JssObjects: &AccountPrivilegesJssObjects{Privilege: &privs},
		},
	}
	out, err := xml.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %%v", err)
	}
	got := string(out)
	if n := strings.Count(got, "<jss_objects>"); n != 1 {
		t.Fatalf("want 1 <jss_objects> wrapper, got %%d:\n%%s", n, got)
	}
	if n := strings.Count(got, "<privilege>"); n != 3 {
		t.Fatalf("want 3 <privilege> children, got %%d:\n%%s", n, got)
	}

	var back Account
	if err := xml.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal: %%v", err)
	}
	if back.Privileges == nil || back.Privileges.JssObjects == nil || back.Privileges.JssObjects.Privilege == nil {
		t.Fatal("privileges.jss_objects decoded as nil")
	}
	got2 := *back.Privileges.JssObjects.Privilege
	if len(got2) != 3 {
		t.Fatalf("want 3 privileges after unmarshal, got %%d: %%v", len(got2), got2)
	}
	for i, want := range privs {
		if got2[i] != want {
			t.Fatalf("privilege[%%d]: want %%q, got %%q", i, want, got2[i])
		}
	}
}

// dataJARLDAPSFixture is a representative slice of the real
// DataJARLDAPS_JamfPro_Admins group GET response captured 2026-06-12
// (the Pro tenant). It carries ldap_server + three populated privilege
// categories (casper_* and recon are absent on this group — omitempty
// must leave them nil). Counts per category match the live dump.
var dataJARLDAPSFixture = []byte(`+"`"+`
<group>
  <id>8</id>
  <name>DataJARLDAPS_JamfPro_Admins</name>
  <access_level>Full Access</access_level>
  <privilege_set>Administrator</privilege_set>
  <ldap_server><id>31</id><name>ldap.example.invalid</name></ldap_server>
  <site><id>-1</id><name>NONE</name></site>
  <privileges>
    <jss_objects>
      <privilege>Create Cloud Distribution Point</privilege>
      <privilege>Read Cloud Distribution Point</privilege>
      <privilege>Update Cloud Distribution Point</privilege>
      <privilege>Create Custom Paths</privilege>
      <privilege>Read Custom Paths</privilege>
      <privilege>Update Custom Paths</privilege>
      <privilege>Delete Custom Paths</privilege>
    </jss_objects>
    <jss_settings>
      <privilege>Read AD CS Certificate Jobs</privilege>
      <privilege>Update AD CS Certificate Jobs</privilege>
      <privilege>Read SSO Settings</privilege>
      <privilege>Update SSO Settings</privilege>
      <privilege>Read Computer Inventory Collection Settings</privilege>
    </jss_settings>
    <jss_actions>
      <privilege>Allow User to Enroll</privilege>
      <privilege>Assign Users to Computers</privilege>
      <privilege>Assign Users to Mobile Devices</privilege>
      <privilege>Change Password</privilege>
    </jss_actions>
  </privileges>
  <members/>
</group>
`+"`"+`)

// TestGroup_WireFixture_LdapServerAndPrivileges unmarshal the real-dump
// fixture and asserts that:
//   - ldap_server.id == 31, ldap_server.name == "ldap.example.invalid"
//   - all three privilege categories survive with correct counts
//   - absent categories (casper_admin etc.) remain nil
//   - re-marshal produces ONE wrapper per populated category
func TestGroup_WireFixture_LdapServerAndPrivileges(t *testing.T) {
	var g Group
	if err := xml.Unmarshal(dataJARLDAPSFixture, &g); err != nil {
		t.Fatalf("unmarshal fixture: %%v", err)
	}

	// Gate C: ldap_server must survive.
	if g.LdapServer == nil {
		t.Fatal("LdapServer is nil after unmarshal")
	}
	if g.LdapServer.ID == nil || *g.LdapServer.ID != 31 {
		t.Fatalf("LdapServer.ID: want 31, got %%v", g.LdapServer.ID)
	}
	if g.LdapServer.Name == nil || *g.LdapServer.Name != "ldap.example.invalid" {
		t.Fatalf("LdapServer.Name: want ldap.example.invalid, got %%v", g.LdapServer.Name)
	}

	// Gate A: privilege counts must survive (no collapse).
	if g.Privileges == nil {
		t.Fatal("Privileges is nil after unmarshal")
	}
	checkPrivCount := func(label string, ptr *[]string, want int) {
		t.Helper()
		if ptr == nil {
			t.Fatalf("%%s: slice is nil, want %%d items", label, want)
		}
		if got := len(*ptr); got != want {
			t.Fatalf("%%s: want %%d privileges, got %%d: %%v", label, want, got, *ptr)
		}
	}
	if g.Privileges.JssObjects == nil {
		t.Fatal("Privileges.JssObjects is nil")
	}
	checkPrivCount("jss_objects", g.Privileges.JssObjects.Privilege, 7)
	if g.Privileges.JssSettings == nil {
		t.Fatal("Privileges.JssSettings is nil")
	}
	checkPrivCount("jss_settings", g.Privileges.JssSettings.Privilege, 5)
	if g.Privileges.JssActions == nil {
		t.Fatal("Privileges.JssActions is nil")
	}
	checkPrivCount("jss_actions", g.Privileges.JssActions.Privilege, 4)

	// Absent categories must stay nil (omitempty must not emit empty wrappers).
	if g.Privileges.CasperAdmin != nil {
		t.Fatal("CasperAdmin: want nil for absent category, got non-nil")
	}
	if g.Privileges.CasperImaging != nil {
		t.Fatal("CasperImaging: want nil for absent category, got non-nil")
	}
	if g.Privileges.CasperRemote != nil {
		t.Fatal("CasperRemote: want nil for absent category, got non-nil")
	}
	if g.Privileges.Recon != nil {
		t.Fatal("Recon: want nil for absent category, got non-nil")
	}

	// Re-marshal: each populated category must appear exactly once.
	out, err := xml.Marshal(g)
	if err != nil {
		t.Fatalf("re-marshal: %%v", err)
	}
	wire := string(out)
	for _, cat := range []string{"jss_objects", "jss_settings", "jss_actions"} {
		open := "<" + cat + ">"
		if n := strings.Count(wire, open); n != 1 {
			t.Fatalf("re-marshal: want 1 %%s wrapper, got %%d", open, n)
		}
	}
	// casper_admin must be absent from the re-marshalled output.
	if strings.Contains(wire, "<casper_admin>") {
		t.Fatal("re-marshal: casper_admin emitted for nil category")
	}

	// Round-trip: re-unmarshal the output and verify counts are stable.
	var g2 Group
	if err := xml.Unmarshal(out, &g2); err != nil {
		t.Fatalf("second unmarshal: %%v", err)
	}
	if g2.LdapServer == nil || g2.LdapServer.ID == nil || *g2.LdapServer.ID != 31 {
		t.Fatalf("second unmarshal LdapServer.ID: want 31, got %%v", g2.LdapServer)
	}
	if g2.Privileges == nil || g2.Privileges.JssObjects == nil {
		t.Fatal("second unmarshal: JssObjects nil")
	}
	checkPrivCount("jss_objects (2nd)", g2.Privileges.JssObjects.Privilege, 7)
}

// directoryAccountFixture is a representative slice of a real directory
// directory account GET response captured 2026-06-12 (the Pro tenant).
// It exercises Account.Groups (group membership with inherited privileges),
// Account.LdapServer, and directory_user:true.
var directoryAccountFixture = []byte(`+"`"+`
<account>
  <id>66</id>
  <name>dir.user@example.invalid</name>
  <directory_user>true</directory_user>
  <email>dir.user@example.invalid</email>
  <email_address>dir.user@example.invalid</email_address>
  <password_sha256/>
  <enabled>Enabled</enabled>
  <ldap_server><id>31</id><name>ldap.example.invalid</name></ldap_server>
  <access_level>Group Access</access_level>
  <privilege_set>Custom</privilege_set>
  <groups>
    <group>
      <id>18</id>
      <name>Example Support</name>
      <site><id>-1</id><name>NONE</name></site>
      <privileges>
        <jss_objects>
          <privilege>Create Computers</privilege>
          <privilege>Read Computers</privilege>
          <privilege>Update Computers</privilege>
        </jss_objects>
        <jss_settings>
          <privilege>Read SSO Settings</privilege>
          <privilege>Read Password Policy</privilege>
        </jss_settings>
      </privileges>
    </group>
  </groups>
</account>
`+"`"+`)

// TestAccount_WireFixture_DirectoryUser asserts that a directory account
// round-trips correctly:
//   - LdapServer.ID == 31, LdapServer.Name == "ldap.example.invalid"
//   - DirectoryUser == true
//   - Groups contains exactly one group with id == 18
//   - Group privileges decoded without collapse (jss_objects=3, jss_settings=2)
//   - Write probe: re-marshal contains exactly ONE <groups> wrapper and
//     ONE <group> child; privilege counts stable after second unmarshal
func TestAccount_WireFixture_DirectoryUser(t *testing.T) {
	var a Account
	if err := xml.Unmarshal(directoryAccountFixture, &a); err != nil {
		t.Fatalf("unmarshal fixture: %%v", err)
	}

	if a.LdapServer == nil || a.LdapServer.ID == nil || *a.LdapServer.ID != 31 {
		t.Fatalf("LdapServer.ID: want 31, got %%v", a.LdapServer)
	}
	if a.LdapServer.Name == nil || *a.LdapServer.Name != "ldap.example.invalid" {
		t.Fatalf("LdapServer.Name: want ldap.example.invalid, got %%v", a.LdapServer.Name)
	}
	if a.DirectoryUser == nil || !*a.DirectoryUser {
		t.Fatalf("DirectoryUser: want true, got %%v", a.DirectoryUser)
	}
	if a.Groups == nil || a.Groups.Group == nil || len(*a.Groups.Group) != 1 {
		t.Fatalf("Groups: want 1 group, got %%v", a.Groups)
	}
	g0 := (*a.Groups.Group)[0]
	if g0.ID == nil || *g0.ID != 18 {
		t.Fatalf("Groups[0].ID: want 18, got %%v", g0.ID)
	}
	if g0.Name == nil || *g0.Name != "Example Support" {
		t.Fatalf("Groups[0].Name: want Example Support, got %%v", g0.Name)
	}
	if g0.Privileges == nil || g0.Privileges.JssObjects == nil || g0.Privileges.JssObjects.Privilege == nil {
		t.Fatal("Groups[0].Privileges.JssObjects is nil")
	}
	if got := len(*g0.Privileges.JssObjects.Privilege); got != 3 {
		t.Fatalf("Groups[0] jss_objects: want 3, got %%d", got)
	}
	if g0.Privileges.JssSettings == nil || g0.Privileges.JssSettings.Privilege == nil {
		t.Fatal("Groups[0].Privileges.JssSettings is nil")
	}
	if got := len(*g0.Privileges.JssSettings.Privilege); got != 2 {
		t.Fatalf("Groups[0] jss_settings: want 2, got %%d", got)
	}

	// Re-marshal and verify structure.
	out, err := xml.Marshal(a)
	if err != nil {
		t.Fatalf("re-marshal: %%v", err)
	}
	wire := string(out)
	if n := strings.Count(wire, "<groups>"); n != 1 {
		t.Fatalf("re-marshal: want 1 <groups> wrapper, got %%d", n)
	}
	if n := strings.Count(wire, "<group>"); n != 1 {
		t.Fatalf("re-marshal: want 1 <group> child, got %%d", n)
	}

	// Second unmarshal: confirm groups and privileges are stable.
	var a2 Account
	if err := xml.Unmarshal(out, &a2); err != nil {
		t.Fatalf("second unmarshal: %%v", err)
	}
	if a2.Groups == nil || a2.Groups.Group == nil || len(*a2.Groups.Group) != 1 {
		t.Fatalf("second unmarshal: Groups: want 1 group, got %%v", a2.Groups)
	}
	g0b := (*a2.Groups.Group)[0]
	if g0b.Privileges == nil || g0b.Privileges.JssObjects == nil || g0b.Privileges.JssObjects.Privilege == nil {
		t.Fatal("second unmarshal: Groups[0].Privileges.JssObjects is nil")
	}
	if got := len(*g0b.Privileges.JssObjects.Privilege); got != 3 {
		t.Fatalf("second unmarshal: Groups[0] jss_objects: want 3, got %%d", got)
	}
}
`, pkgName)
	outPath := filepath.Join(pkgDir, "accounts_privileges_test.go")
	formatted, err := formatGo("accounts_privileges_test.go", []byte(src))
	if err != nil {
		return fmt.Errorf("formatting accounts_privileges_test.go: %w\n---raw---\n%s", err, src)
	}
	if err := writeGenerated(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	log.Printf("wrote %s", outPath)
	return nil
}

// emitPkgVersionLockHelpers writes version_lock_helpers.go into packages
// that have Apply methods with optimistic locking. The file provides two
// runtime helpers:
//   - zeroVersionLock(v any): recursively zeros all VersionLock int fields
//     on a struct pointer (used on create to satisfy the Jamf API requirement)
//   - convertAndInjectVersionLock[U, G any](src, current): JSON round-trips
//     the create request into the update type, then injects VersionLock
//     values from the GET response (used on update to satisfy optimistic locking)
func emitPkgVersionLockHelpers(pkgDir string, cfg Config, pkgName string) error {
	src := fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// zeroVersionLock recursively walks v (must be a pointer to a struct)
// and sets every field named "VersionLock" of type int to 0. This
// satisfies the Jamf API requirement that all versionLock fields be
// zero on resource creation.
func zeroVersionLock(v any) {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return
	}
	rt := rv.Type()
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Field(i)
		ft := rt.Field(i)
		if ft.Name == "VersionLock" && f.Kind() == reflect.Int && f.CanSet() {
			f.SetInt(0)
			continue
		}
		switch f.Kind() {
		case reflect.Struct:
			zeroVersionLock(f.Addr().Interface())
		case reflect.Ptr:
			if !f.IsNil() && f.Elem().Kind() == reflect.Struct {
				zeroVersionLock(f.Interface())
			}
		}
	}
}

// convertAndInjectVersionLock converts a create request (src) to update
// type U via JSON round-trip, then injects all VersionLock field values
// from the current GET response (current) into the resulting update request.
// This implements the optimistic locking requirement: the update request
// must carry the current versionLock values so the server can detect
// concurrent modifications.
func convertAndInjectVersionLock[U any, G any](src any, current *G) (*U, error) {
	data, err := json.Marshal(src)
	if err != nil {
		return nil, fmt.Errorf("marshal for update: %%w", err)
	}
	var updateReq U
	if err := json.Unmarshal(data, &updateReq); err != nil {
		return nil, fmt.Errorf("unmarshal for update: %%w", err)
	}
	injectVersionLock(reflect.ValueOf(&updateReq).Elem(), reflect.ValueOf(current).Elem())
	return &updateReq, nil
}

// injectVersionLock recursively copies VersionLock field values from src
// into dst. Both must be struct values. Fields are matched by name; if a
// VersionLock field exists in both, the src value is copied to dst.
func injectVersionLock(dst, src reflect.Value) {
	if dst.Kind() == reflect.Ptr {
		if dst.IsNil() {
			return
		}
		dst = dst.Elem()
	}
	if src.Kind() == reflect.Ptr {
		if src.IsNil() {
			return
		}
		src = src.Elem()
	}
	if dst.Kind() != reflect.Struct || src.Kind() != reflect.Struct {
		return
	}
	dstType := dst.Type()
	for i := 0; i < dst.NumField(); i++ {
		df := dst.Field(i)
		dft := dstType.Field(i)
		if dft.Name == "VersionLock" && df.Kind() == reflect.Int && df.CanSet() {
			sf := src.FieldByName("VersionLock")
			if sf.IsValid() && sf.Kind() == reflect.Int {
				df.SetInt(sf.Int())
			}
			continue
		}
		sf := src.FieldByName(dft.Name)
		if !sf.IsValid() {
			continue
		}
		switch df.Kind() {
		case reflect.Struct:
			injectVersionLock(df, sf)
		case reflect.Ptr:
			if !df.IsNil() {
				injectVersionLock(df, sf)
			}
		}
	}
}
`, pkgName)
	outPath := filepath.Join(pkgDir, "version_lock_helpers.go")
	formatted, err := formatGo("version_lock_helpers.go", []byte(src))
	if err != nil {
		return fmt.Errorf("formatting version_lock_helpers.go: %w", err)
	}
	if err := writeGenerated(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	log.Printf("wrote %s", outPath)
	return nil
}

// emitMethodsByTag buckets methods by the filename their first OpenAPI tag
// maps to (post tagToFileBase normalization) and emits one source + test
// file per distinct filename. Operations without a tag error out —
// untagged ops in splitByTag mode signal a spec bug the curator should
// see. Bucketing by final filename (not raw tag) means two tags that
// normalize to the same base — e.g. `foo` + `foo-preview` after the
// -preview strip — merge into one file instead of the second overwriting
// the first.
func emitMethodsByTag(pkgDir string, cfg Config, pkgName string, spec SpecDef, methods []GoMethod, tagFileOwner map[string]string) error {
	buckets := make(map[string][]GoMethod)
	for _, m := range methods {
		if m.Tag == "" {
			return fmt.Errorf("spec %s: operation %s has no OpenAPI tag but splitByTag is enabled", spec.File, m.Name)
		}
		base := tagToFileBase(m.Tag)
		buckets[base] = append(buckets[base], m)
	}

	bases := make([]string, 0, len(buckets))
	for b := range buckets {
		bases = append(bases, b)
	}
	sort.Strings(bases)

	for _, base := range bases {
		if owner, taken := tagFileOwner[base]; taken && owner != spec.File {
			return fmt.Errorf("spec %s: tag %q writes %s.go, already written by spec %s — two specs in package %s share an OpenAPI tag; add a tagRenames entry for %q in this spec to give it a distinct filename", spec.File, buckets[base][0].Tag, base, owner, pkgName, buckets[base][0].Tag)
		}
		tagFileOwner[base] = spec.File

		mf := GeneratedFile{Package: pkgName, Module: cfg.Module, Format: spec.Format, Methods: buckets[base]}
		if err := emitTemplated(sourceTmpl, mf, filepath.Join(pkgDir, base+".go")); err != nil {
			return err
		}
		if err := emitTemplated(testTmpl, mf, filepath.Join(pkgDir, base+"_test.go")); err != nil {
			return err
		}
	}
	return nil
}

// tagToFileBase converts an OpenAPI tag ("startup-status", "declaration report",
// "mobile-device-extension-attributes-preview") into a Go-friendly filename base.
// Hyphens and whitespace collapse to underscores; non-word characters are dropped.
//
// Two post-processing rules:
//
//  1. Trailing "-preview" is stripped. Jamf's API team tags in-development
//     endpoints with "-preview"; when the endpoint graduates to stable the
//     tag loses the suffix and the filename would churn. Stripping at
//     generate-time keeps the SDK filename stable across that transition
//     and consolidates preview + stable variants of the same resource
//     (e.g. mobile-device-extension-attributes + *-preview) into one file.
//
//  2. Filenames ending in `_<goos>` or `_<goarch>` get `_api` appended so
//     the Go toolchain doesn't interpret them as implicit build constraints
//     — e.g. `self_service_branding_ios.go` would otherwise only compile
//     for GOOS=ios.
func tagToFileBase(tag string) string {
	s := strings.ToLower(strings.TrimSpace(tag))
	s = strings.TrimSuffix(s, "-preview")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == ' ', r == '\t':
			b.WriteByte('_')
		}
	}
	out := b.String()
	for _, suf := range reservedFileSuffixes {
		if strings.HasSuffix(out, "_"+suf) {
			return out + "_api"
		}
	}
	return out
}

// reservedFileSuffixes lists the GOOS and GOARCH values whose trailing
// use as `_<value>.go` would turn the whole file into a per-platform
// build-constrained source. Keep in sync with Go's build constraints.
var reservedFileSuffixes = []string{
	// GOOS
	"aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos",
	"ios", "js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris",
	"wasip1", "windows", "zos",
	// GOARCH
	"386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le",
	"mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm",
}

// emitTemplated executes a template and writes the goimports-formatted result
// to outPath (absolute).
func emitTemplated(tmpl *template.Template, data any, outPath string) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("executing template for %s: %w", outPath, err)
	}
	formatted, err := imports.Process(outPath, buf.Bytes(), &imports.Options{Comments: true})
	if err != nil {
		return fmt.Errorf("goimports %s: %w\n---raw---\n%s", outPath, err, buf.String())
	}
	if err := writeGenerated(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	log.Printf("wrote %s", outPath)
	return nil
}

// emitPkgClient writes the per-sub-package client.go — a small Client struct
// wrapping a transport plus a New constructor that takes the root client.
func emitPkgClient(pkgDir string, cfg Config, pkgName string) error {
	src := fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

// Package %s provides typed access to Jamf Platform %s API endpoints.
package %s

import (
	"%s/internal/client"
	"%s/jamfplatform"
)

// Client provides typed methods for %s operations.
type Client struct {
	transport *client.Transport
}

// New creates a %s client that shares the authenticated transport of the
// given root client.
func New(base *jamfplatform.Client) *Client {
	return &Client{transport: base.Transport()}
}
`, pkgName, pkgName, pkgName, cfg.Module, cfg.Module, pkgName, pkgName)
	outPath := filepath.Join(pkgDir, "client.go")
	formatted, err := imports.Process(outPath, []byte(src), &imports.Options{Comments: true})
	if err != nil {
		return fmt.Errorf("goimports %s: %w", outPath, err)
	}
	if err := writeGenerated(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	log.Printf("wrote %s", outPath)
	return nil
}

// goStringSliceLiteral renders ss as a Go source expression: "nil" for an
// empty slice, otherwise a []string composite literal. Used to emit the
// Scoped/Legacy fields of the per-package privilege registry.
// goScopeKindSliceLiteral renders scope-kind constant names as a
// []jamfplatform.ScopeKind literal. Unlike goStringSliceLiteral the values are
// identifiers, not quoted strings, and an empty slice is unreachable:
// resolveScopeTypes fails generation before it can produce one.
func goScopeKindSliceLiteral(names []string) string {
	if len(names) == 0 {
		return "nil"
	}
	qualified := make([]string, len(names))
	for i, n := range names {
		qualified[i] = "jamfplatform." + n
	}
	return "[]jamfplatform.ScopeKind{" + strings.Join(qualified, ", ") + "}"
}

func goStringSliceLiteral(ss []string) string {
	if len(ss) == 0 {
		return "nil"
	}
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = strconv.Quote(s)
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

// emitPkgPrivileges writes permissions.go into a sub-package: a Privileges map
// keyed by method name plus a PrivilegesFor lookup helper. Only spec-derived
// methods (PrivilegesKnown) are included; synthetic resolver/apply methods are
// omitted because they compose multiple endpoints rather than mapping to one
// operation. Entries are emitted in method-name order for deterministic
// output so CI's git-diff check stays stable.
func emitPkgPrivileges(pkgDir string, cfg Config, pkgName string, methods []GoMethod) error {
	type entry struct {
		method GoMethod
		path   string
	}
	var entries []entry
	seen := make(map[string]bool)
	for _, m := range methods {
		if !m.PrivilegesKnown || seen[m.Name] {
			continue
		}
		seen[m.Name] = true
		path := "/" + m.Version + m.ResourcePath
		entries = append(entries, entry{method: m, path: path})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].method.Name < entries[j].method.Name })

	var b strings.Builder
	fmt.Fprintf(&b, `// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

import "%s/jamfplatform"

// Privileges maps each %s SDK method name to the Jamf API privileges it
// requires, sourced from the x-required-privileges vendor extensions in the
// Jamf OpenAPI specs. Identifiers are GA capability permissions in
// {capability}:{action} form and a multi-entry Scoped slice means all of them
// are required.
//
// Source names where each entry's Scoped set came from: "spec" for the
// operation's own x-required-privileges, "gateway-policy" for one the
// published spec omits and this SDK supplies from the gateway's authorization
// policy, and "" when Scoped is empty. An empty Scoped slice means nothing
// declares a privilege for the endpoint, which is NOT the same as none being
// required — see jamfplatform.MethodPrivileges. Do not render it as "no
// permission needed".
//
// Scopes lists the scope kinds each endpoint accepts. It is an alternatives
// set: a client carries one scope, so a consumer needs a credential matching
// one of the listed kinds. ScopesSource names where the set came from —
// "spec" for the spec root's own x-scope-types, "config-override" for one this
// SDK supplies because the published spec understates what the gateway serves
// or declares no extension at all. A spec-sourced set is what the spec
// declares, which for the Platform APIs is currently stricter than the
// gateway — see jamfplatform.MethodPrivileges.
//
// Synthetic Resolve<X>ByName / Apply<X> methods are not present; document the
// privileges of the operations they call instead.
var Privileges = map[string]jamfplatform.MethodPrivileges{
`, pkgName, cfg.Module, pkgName)

	for _, e := range entries {
		m := e.method
		fmt.Fprintf(&b, "\t%s: {Method: %s, HTTPMethod: %s, Path: %s, Scopes: %s, ScopesSource: %s, Scoped: %s, Legacy: %s, Source: %s},\n",
			strconv.Quote(m.Name),
			strconv.Quote(m.Name),
			strconv.Quote(m.HTTPMethod),
			strconv.Quote(e.path),
			goScopeKindSliceLiteral(m.Scopes),
			strconv.Quote(m.ScopesSource),
			goStringSliceLiteral(m.ScopedPrivileges),
			goStringSliceLiteral(m.LegacyPrivileges),
			strconv.Quote(m.PrivilegeSource),
		)
	}

	b.WriteString(`}

// PrivilegesFor returns the privilege metadata for the named SDK method and
// true when the method is present in the registry, or the zero value and
// false otherwise.
func PrivilegesFor(method string) (jamfplatform.MethodPrivileges, bool) {
	p, ok := Privileges[method]
	return p, ok
}
`)

	outPath := filepath.Join(pkgDir, "permissions.go")
	formatted, err := imports.Process(outPath, []byte(b.String()), &imports.Options{Comments: true})
	if err != nil {
		return fmt.Errorf("goimports %s: %w\n---raw---\n%s", outPath, err, b.String())
	}
	if err := writeGenerated(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	log.Printf("wrote %s", outPath)
	return nil
}

// emitUnionRoundTripTest writes unions_test.go for a package holding at least
// one union — either a discriminator-less request union (GoUnion) or a
// oneOf-with-discriminator one. No-op otherwise.
//
// The generated method tests cannot cover either kind. A method's httptest stub
// is built from the request type, so it serialises whatever the generated code
// assumed — the account SSO create test passed for as long as the field was
// `any`, calling CreateConnection with a bare &ConnectionRequest{} and putting
// nothing on the wire, which is exactly why nothing ever exercised any of the
// four settings schemas the connection body can hold. These tests marshal each
// variant in turn and assert the bytes, so a marshaler that emitted the wrong
// variant, dropped fields, or silently picked one of two fails here.
//
// It matters most for a union whose discriminator was *inferred*
// (inferDiscriminator): the mapping is read off the variants' enums rather than
// declared, so the value-to-variant pairing is the generator's reading of the
// spec and a wrong one routes a payload to the wrong type in silence.
//
// A variant whose Go type is not a struct declared in this package is skipped —
// `&T{}` is not a valid literal for an alias to a slice or json.RawMessage — and
// a union with no usable variant is left out entirely.
func emitUnionRoundTripTest(pkgDir, pkgName string, types []GoType) error {
	// Every emitted type takes a `&T{}` literal except a json.RawMessage
	// alias, a `type X = Y` alias and an enum. A nested union counts as a
	// struct — SwUpdateConfiguration's AUTOMATIC variant is itself a
	// discriminated union, and excluding it skipped the very union whose
	// discriminator is inferred.
	structNames := make(map[string]bool, len(types))
	for _, t := range types {
		if !t.IsRawJSON && t.AliasTarget == "" && len(t.EnumValues) == 0 {
			structNames[t.Name] = true
		}
	}
	usable := func(variantTypes ...string) bool {
		for _, tn := range variantTypes {
			if !structNames[tn] {
				return false
			}
		}
		return true
	}

	var unions, tagged []GoType
	for _, t := range types {
		switch {
		case t.Union != nil && len(t.Union.Variants) > 0:
			unions = append(unions, t)
		case t.Discriminator != nil && len(t.Discriminator.Variants) > 0:
			ok := true
			for _, v := range t.Discriminator.Variants {
				if !usable(v.TypeName) {
					ok = false
				}
			}
			if ok {
				tagged = append(tagged, t)
			} else {
				log.Printf("%s: skipping union round-trip test — a variant type is not a struct in this package", t.Name)
			}
		}
	}
	if len(unions) == 0 && len(tagged) == 0 {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, `// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

import (
	"bytes"
	"encoding/json"
	"testing"
)
`, pkgName)

	for _, u := range unions {
		fmt.Fprintf(&b, `
// Test%[1]s_Marshal asserts that each variant marshals to exactly the bytes
// the variant alone produces — the union adds no wrapper and drops no field —
// that the payload survives a decode into Raw, that setting no variant emits
// null rather than an empty object, and that setting two is refused instead of
// silently shipping one of them.
func Test%[1]s_Marshal(t *testing.T) {
`, u.Name)
		for _, v := range u.Union.Variants {
			fmt.Fprintf(&b, `	t.Run(%[3]q, func(t *testing.T) {
		variant := &%[2]s{}
		got, err := json.Marshal(%[1]s{%[3]s: variant})
		if err != nil {
			t.Fatalf("marshal union: %%v", err)
		}
		want, err := json.Marshal(variant)
		if err != nil {
			t.Fatalf("marshal variant: %%v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("union marshalled to %%s, want the bare variant %%s", got, want)
		}
		var back %[1]s
		if err := json.Unmarshal(got, &back); err != nil {
			t.Fatalf("unmarshal union: %%v", err)
		}
		if !bytes.Equal(back.Raw, got) {
			t.Fatalf("Raw is %%s, want the payload %%s", back.Raw, got)
		}
		if err := json.Unmarshal(back.Raw, &%[2]s{}); err != nil {
			t.Fatalf("decoding Raw back into %[2]s: %%v", err)
		}
	})
`, u.Name, v.TypeName, v.FieldName)
		}
		fmt.Fprintf(&b, `	t.Run("no variant set", func(t *testing.T) {
		got, err := json.Marshal(%[1]s{})
		if err != nil {
			t.Fatalf("marshal: %%v", err)
		}
		if string(got) != "null" {
			t.Fatalf("zero union marshalled to %%s, want null", got)
		}
	})
`, u.Name)
		if len(u.Union.Variants) > 1 {
			fmt.Fprintf(&b, `	t.Run("two variants set", func(t *testing.T) {
		if _, err := json.Marshal(%[1]s{%[2]s: &%[3]s{}, %[4]s: &%[5]s{}}); err == nil {
			t.Fatal("marshalling two variants succeeded; want an error naming both")
		}
	})
`, u.Name,
				u.Union.Variants[0].FieldName, u.Union.Variants[0].TypeName,
				u.Union.Variants[1].FieldName, u.Union.Variants[1].TypeName)
		}
		b.WriteString("}\n")
	}

	for _, u := range tagged {
		d := u.Discriminator
		fmt.Fprintf(&b, `
// Test%[1]s_Marshal asserts that each %[2]q value marshals to exactly the bytes
// its variant alone produces, that a payload carrying the value decodes into
// that variant, and that an unrecognised value leaves every variant nil while
// preserving the discriminator rather than guessing.
func Test%[1]s_Marshal(t *testing.T) {
`, u.Name, d.PropertyName)
		for _, v := range d.Variants {
			for _, value := range v.Values {
				fmt.Fprintf(&b, `	t.Run(%[4]q, func(t *testing.T) {
		variant := &%[3]s{}
		got, err := json.Marshal(%[1]s{%[2]s: %[4]q, %[5]s: variant})
		if err != nil {
			t.Fatalf("marshal union: %%v", err)
		}
		want, err := json.Marshal(variant)
		if err != nil {
			t.Fatalf("marshal variant: %%v", err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("union marshalled to %%s, want the bare variant %%s", got, want)
		}
		var back %[1]s
		if err := json.Unmarshal([]byte(%[6]s), &back); err != nil {
			t.Fatalf("unmarshal union: %%v", err)
		}
		if back.%[2]s != %[4]q {
			t.Fatalf("%[2]s = %%q after decode, want %%q", back.%[2]s, %[4]q)
		}
		if back.%[5]s == nil {
			t.Fatal("%[4]s decoded with %[5]s nil — the value is mapped to no variant")
		}
	})
`, u.Name, d.GoFieldName, v.TypeName, value, v.FieldName,
					"`"+fmt.Sprintf(`{%q:%q}`, d.PropertyName, value)+"`")
			}
		}
		fmt.Fprintf(&b, `	t.Run("unrecognised discriminator", func(t *testing.T) {
		var back %[1]s
		if err := json.Unmarshal([]byte(%[3]s), &back); err != nil {
			t.Fatalf("unmarshal: %%v", err)
		}
		if back.%[2]s != "NOT_A_%[2]s_VALUE" {
			t.Fatalf("%[2]s = %%q, want the unrecognised value preserved", back.%[2]s)
		}
`, u.Name, d.GoFieldName,
			"`"+fmt.Sprintf(`{%q:%q}`, d.PropertyName, "NOT_A_"+d.GoFieldName+"_VALUE")+"`")
		for _, v := range d.Variants {
			fmt.Fprintf(&b, `		if back.%s != nil {
			t.Error("%s was populated for an unrecognised discriminator")
		}
`, v.FieldName, v.FieldName)
		}
		b.WriteString("	})\n}\n")
	}

	outPath := filepath.Join(pkgDir, "unions_test.go")
	formatted, err := imports.Process(outPath, []byte(b.String()), &imports.Options{Comments: true})
	if err != nil {
		return fmt.Errorf("goimports %s: %w\n---raw---\n%s", outPath, err, b.String())
	}
	if err := writeGenerated(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	log.Printf("wrote %s", outPath)
	return nil
}

// emitPkgHelpersTest writes the per-sub-package helpers_test.go — test-only
// shims that alias jamfplatform.Option and WithTenantID into the sub-package
// namespace so generated test files can use them unqualified.
func emitPkgHelpersTest(pkgDir string, cfg Config, pkgName, format string) error {
	xmlHelpers := ""
	if format == "xml" {
		xmlHelpers = `

func writeXML(t *testing.T, w http.ResponseWriter, status int, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	if body != "" {
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("writeXML: %v", err)
		}
	}
}`
	}
	src := fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"%s/jamfplatform"
)

type Option = jamfplatform.Option

var WithTenantID = jamfplatform.WithTenantID

func testServer(t *testing.T) (*Client, *http.ServeMux) {
	return testServerWithOpts(t)
}

func testServerWithOpts(t *testing.T, opts ...Option) (*Client, *http.ServeMux) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-token",
			"token_type":   "bearer",
			"expires_in":   3600,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// Disable inter-request pacing in unit tests so the per-method httptest
	// suite isn't throttled to the default 100ms cadence. The gate's own
	// behavior is covered in internal/client. Also disable automatic
	// retry-on-transient-failure: a handler that deliberately keeps
	// returning e.g. 500 to exercise a caller's error-handling path would
	// otherwise hang for the production 1s-60s backoff window on every
	// run. Retry's own behavior is covered in internal/client too.
	clientOpts := append([]Option{jamfplatform.WithMinRequestInterval(0), jamfplatform.WithRetryPolicy(0, 0, 0)}, opts...)
	base := jamfplatform.NewClient(srv.URL, "test-id", "test-secret", clientOpts...)
	return New(base), mux
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Fatalf("writeJSON: %%v", err)
		}
	}
}

func readJSON(t *testing.T, r *http.Request, v any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatalf("readJSON: %%v", err)
	}
}

func ptrStr(s string) *string { return &s }%s
`, pkgName, cfg.Module, xmlHelpers)
	outPath := filepath.Join(pkgDir, "helpers_test.go")
	formatted, err := imports.Process(outPath, []byte(src), &imports.Options{Comments: true})
	if err != nil {
		return fmt.Errorf("goimports %s: %w", outPath, err)
	}
	if err := writeGenerated(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	log.Printf("wrote %s", outPath)
	return nil
}

// ---------------------------------------------------------------------------
// Static files
// ---------------------------------------------------------------------------

// formatGo runs goimports which handles both formatting and unused import removal.
func formatGo(filename string, src []byte) ([]byte, error) {
	return imports.Process(filename, src, &imports.Options{Comments: true})
}

// readJamfProAPIVersion returns info.version from the configured Jamf Pro
// spec (the entry with package "pro"). Generation MUST fail loud rather
// than bake a placeholder constant: the whole point of JamfProAPIVersion
// is build-time provenance, and "unknown" defeats that.
//
// Loads via resolveSpecPath so CI — which doesn't have access to the
// private testing/ source specs — transparently falls back to the
// already-published api/ spec, which carries the same info.version.
func readJamfProAPIVersion(root string, cfg Config) (string, error) {
	var proSpec SpecDef
	found := false
	for _, s := range cfg.Specs {
		if s.Package == "pro" {
			proSpec = s
			found = true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("readJamfProAPIVersion: no spec entry with package %q in config", "pro")
	}
	specPath, _, err := resolveSpecPath(root, cfg, proSpec)
	if err != nil {
		return "", fmt.Errorf("readJamfProAPIVersion: %w", err)
	}
	data, err := os.ReadFile(specPath)
	if err != nil {
		return "", fmt.Errorf("readJamfProAPIVersion: reading %s: %w", specPath, err)
	}
	// yaml.Unmarshal, not json.Unmarshal: the source spec in testing/ is the
	// bundle's own YAML, copied verbatim, while the api/ fallback is JSON.
	// YAML is a JSON superset, so one decoder reads both; json.Unmarshal reads
	// only the fallback and fails on the primary path.
	var doc struct {
		Info struct {
			Version string `json:"version" yaml:"version"`
		} `json:"info" yaml:"info"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("readJamfProAPIVersion: parsing %s: %w", specPath, err)
	}
	if doc.Info.Version == "" {
		return "", fmt.Errorf("readJamfProAPIVersion: %s has empty info.version", specPath)
	}
	return doc.Info.Version, nil
}

func writeStaticFiles(root string, cfg Config) error {
	pkg := cfg.Package
	mod := cfg.Module

	jamfProAPIVersion, err := readJamfProAPIVersion(root, cfg)
	if err != nil {
		return err
	}

	staticFiles := map[string]string{
		"jamfplatform/version.go": fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

// JamfProAPIVersion is the Jamf Pro release this SDK was generated from,
// read directly from the info.version field of the openapi-jpapi.json
// spec at generate time. Use it to log/report which API surface the
// linked SDK build targets.
const JamfProAPIVersion = %q
`, pkg, jamfProAPIVersion),
		"jamfplatform/doc.go": fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

// Package %s provides a Go client for the Jamf Platform API.
//
// Create a root client with [NewClient], then construct service clients
// from the sub-packages under jamfplatform/ (devices, devicegroups,
// deviceactions, blueprints, ddmreport, compliancebenchmarks, pro, ...)
// to call typed methods.
//
//	c := %s.NewClient(
//		"https://eu.api.jamfcloud.com",
//		os.Getenv("JAMFPLATFORM_CLIENT_ID"),
//		os.Getenv("JAMFPLATFORM_CLIENT_SECRET"),
//		%s.WithTenantID(os.Getenv("JAMFPLATFORM_TENANT_ID")),
//	)
//
//	ds, err := devices.New(c).ListDevices(ctx, nil, "")
//
// The root client handles OAuth2 authentication and token refresh
// automatically; each sub-package shares the same transport via its
// [New] constructor.
//
// Error handling uses [*APIResponseError] for structured API errors:
//
//	d, err := devices.New(c).GetDevice(ctx, id)
//	if errors.As(err, &apiErr) && apiErr.HasStatus(404) {
//		// handle not found
//	}
//
// # Response headers
//
// Generated methods return the decoded body only. Response headers —
// including Location on 201 Created, Retry-After on 429 (which the
// transport already honors with a bounded single retry), and
// Deprecation on soon-to-be-removed endpoints (logged automatically)
// — are available to consumers via the [WithLogger] option. Install a
// Logger whose LogResponse receives http.Header if you need to inspect
// Location or any other per-request header.
//
// Note that the body returned by create endpoints already carries an
// "href" field pointing at the new resource, equivalent to Location.
package %s
`, pkg, pkg, pkg, pkg),

		"jamfplatform/errors.go": fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

import "%s/internal/client"

// APIResponseError is returned for any non-success HTTP status. Consumers
// should inspect it via AsAPIError plus the accessor methods
// (HasStatus/Details/FieldErrors/Summary) rather than string-matching
// the Error() output. Non-HTTP errors (denylist refusal, context
// cancellation, IO failures, etc.) surface as plain wrapped errors —
// format them with err.Error(), except for ErrUnexpectedResponse below.
type APIResponseError = client.APIResponseError

// ErrUnexpectedResponse reports that an endpoint answered with an HTML page
// where JSON was expected — an edge proxy, WAF or IP allowlist rejecting the
// caller, or a gateway error page — which means the request did not reach Jamf.
// Raised on the OAuth token exchange, where such a block surfaces first, and on
// any API response whose error body is an HTML page.
//
// Jamf Pro's own HTML error template is excluded: it is a real application
// message and is lifted into Details() instead. A plain-text refusal is excluded
// too — notably the gateway's "Authentication failed", which is a credential or
// api-product problem and not a block, so it carries guidance rather than this
// sentinel.
//
// The page itself is condensed to its headings and any edge request id, so
// Error() stays one line; the full body remains on APIResponseError.Body.
//
// Read the status before choosing a remedy. This sentinel says only that a page
// answered instead of an API, and two different faults do that: a WAF, proxy or
// allowlist refusing this host, which arrives as a 403 or as a 200 carrying a
// login shell and is a standing block; and Jamf's own gateway failing, which
// arrives as a 502/503/504 the transport has already retried and is usually
// transient. AsAPIError(err).StatusCode separates them, so report an egress IP
// for the first rather than for both.
//
// The only sentinel the SDK exposes, and the only error worth matching with
// errors.Is; everything else is *APIResponseError. It exists because the
// condition is inferred from the shape of the body rather than reported by Jamf,
// and because acting on it means doing something extra rather than just
// rendering a message:
//
//	if errors.Is(err, jamfplatform.ErrUnexpectedResponse) {
//		// Not a credential problem. Report the host's egress IP and a
//		// timestamp so Jamf Support can find the block.
//	}
//
// A rejected credential returns JSON (401 invalid_client) and never carries this
// sentinel, so the two causes stay distinguishable — they need opposite remedies.
//
// Named to match jamfprotect-go-sdk so provider code ports unchanged when the
// Protect resources move into terraform-provider-jamfplatform.
var ErrUnexpectedResponse = client.ErrUnexpectedResponse

// ErrorDetail is a single structured error entry parsed from an API response
// body. Consumers receive these via APIResponseError.Details() or
// APIResponseError.FieldErrors().
type ErrorDetail = client.Error

// AmbiguousMatchError is returned by Resolve<Resource>ByName methods when
// multiple resources share the requested name. Matches carries the IDs of
// all colliding resources so consumers can surface disambiguation options.
type AmbiguousMatchError = client.AmbiguousMatchError

// AsAPIError unwraps err and returns the underlying *APIResponseError if
// present, otherwise nil. Shorthand for errors.As that saves callers from
// managing the target pointer and importing the concrete error type.
func AsAPIError(err error) *APIResponseError {
	return client.AsAPIError(err)
}
`, pkg, mod),

		"jamfplatform/permissions.go": fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

// MethodPrivileges describes the Jamf API privileges required to call a
// generated SDK method. It is sourced from the x-required-privileges and
// x-required-privileges-legacy vendor extensions the Jamf OpenAPI specs
// attach to each operation.
//
// Each generated sub-package (pro, devices, devicegroups, proclassic, ...)
// exposes a package-level Privileges map keyed by method name plus a
// PrivilegesFor helper. Consumers that need to document the permissions a
// given operation requires — for example a Terraform provider emitting a
// table of required privileges per resource — look the method up there.
//
// Only methods built directly from a spec operation appear in the registry.
// Synthetic convenience methods (Resolve<X>ByName, Apply<X>) compose one or
// more underlying endpoints and are intentionally absent; document the
// privileges of the operations they call instead.
type MethodPrivileges struct {
	// Method is the generated Go method name, e.g. "CreateBuildingV1".
	Method string
	// HTTPMethod is the HTTP verb of the underlying endpoint, e.g. "POST".
	HTTPMethod string
	// Path is the endpoint's resource path relative to the tenant prefix,
	// e.g. "/buildings/{id}".
	Path string
	// Scopes lists the scope kinds the endpoint accepts. A client carries
	// exactly one scope, so a consumer needs a credential whose scope appears
	// here: ScopeTenant sends X-Tenant-Id, ScopeEnvironment sends
	// X-Environment-Id, and ScopeOrganization sends no header at all because
	// the gateway resolves the organization from the access token.
	//
	// Where two kinds are present the endpoint is published at both and the
	// caller picks one — the header must match the credential, and crossing
	// them over is 403 OWNERSHIP_FORBIDDEN even within one customer. So this
	// is an alternatives set, the opposite of Scoped, which is a conjunction.
	//
	// Where ScopesSource is "spec" this is the spec's own x-scope-types,
	// carried through unchanged; otherwise it is a correction this SDK
	// supplies for a spec that understates the gateway or declares no
	// extension at all. Read ScopesSource to tell the two apart. It is never
	// empty either way: a spec that declares no scope with no correction fails
	// generation rather than emitting an empty set a consumer would read as
	// "no scope required".
	//
	// Two caveats a consumer must not lose. A spec-sourced set is what the
	// spec declares, which is not always what the gateway serves: as of GitOps
	// v2082 the six Platform specs declare environment only, while devices,
	// device-groups, declaration-reporting and device-management-action still
	// answer under X-Tenant-Id (wire-verified 2026-09-04). And scope is
	// declared per spec rather than per operation, so every method built from
	// one spec carries the same set. The field is per-method as a precaution
	// against two specs in one package disagreeing — securitycloud did, one of
	// its six specs having been held at a build predating the others'
	// environment declaration, until v2082 was ingested there and closed the
	// gap. Keeping the field per-method means the next such divergence needs
	// no structural change.
	Scopes []ScopeKind
	// ScopesSource names where Scopes came from:
	//
	//   - "spec": the spec root's own x-scope-types extension.
	//   - "config-override": the SDK's own config.scopeTypes, because the
	//     published spec either understates what the gateway serves or
	//     declares no extension at all. One family is this case: the account
	//     trio (licensing, partners, sso) carries no x-scope-types in any
	//     published build and is organization-scoped by gateway
	//     configuration, the routes resolving the organization from the
	//     access token. securitycloud-device-groups was the second until
	//     2026-09-04, when the v2 update handler was fixed, its hold lifted
	//     and the ingested spec began declaring the set the override had been
	//     asserting — at which point the override self-expired, exactly as
	//     designed.
	//
	// A "config-override" entry is therefore an assertion about the gateway,
	// evidenced on the wire, rather than a claim about the ingested artifact —
	// the same distinction Source draws for Scoped. It is never empty: every
	// spec resolves a scope from one of the two sources or generation fails.
	ScopesSource string
	// Scoped lists the GA capability permissions the endpoint requires, in
	// {capability}:{action} form, e.g. "buildings:create". The capability is
	// kebab-case and carries no product name — one capability is reached by
	// endpoints across several products — and the action is one of exactly
	// six, lowercase and case-sensitive: create, read, update, delete,
	// deploy, execute. The three-part beta slug ("create:pro:buildings") is
	// the retired form and never appears here.
	//
	// Where more than one identifier is present, ALL of them are required.
	// The platform has exactly one route that accepts either of two
	// capabilities rather than both — DELETE /proclassic/logflush takes
	// flush-policy-logs:execute or policies:delete — and its specs declare
	// only the first, so no entry in this registry is an alternatives set. A
	// consumer can therefore render a multi-entry Scoped slice as "grant all
	// of these".
	//
	// An empty slice means nothing declares a privilege for the endpoint,
	// which is not the same as none being required. Most such endpoints are
	// genuinely unauthenticated — /v1/jamf-pro-version,
	// /v2/jamf-pro-information, the /v1/notifications list. A consumer
	// rendering a permissions table must not print an empty slice as "no
	// permission needed"; read Source to tell the two apart.
	Scoped []string
	// Source names where Scoped came from:
	//
	//   - "spec": the operation's own x-required-privileges extension.
	//   - "gateway-policy": the published spec declares none and the SDK
	//     supplies what the gateway's own authorization policy enforces. The
	//     account package's 18 methods are this case, and permanently so:
	//     these routes resolve the organization from the access token, which
	//     exempts them from the transform the publishing pipeline attaches
	//     x-required-privileges during, so the artifact ships without them by
	//     construction. The values come from
	//     the spec source repository's own per-team config.yaml
	//     and the hand-written OPA rules in
	//     the gateway's authorization policy for the account namespace,
	//     which agree on all 18.
	//   - "": Scoped is empty.
	//
	// Note for "gateway-policy": several account rules accept *either* the GA
	// capability recorded here or a retired read:org:*/update:org:* permission.
	// Only the GA form is carried, because Scoped is a conjunction and listing
	// the alternative would read as "both required".
	Source string
	// Legacy lists the human-readable Jamf Pro privilege names, e.g.
	// "Create Buildings". It is populated for the Pro API family only —
	// other families do not publish legacy names.
	//
	// Scoped and Legacy are INDEPENDENT SETS, not parallel arrays. Do not
	// match them by position, and do not assume equal length. Both are copied
	// in spec order.
	//
	// Two things make a positional pairing wrong, and each on its own is
	// sufficient (counts are pro at spec 11.31.0, 746 privileged operations):
	//
	//   - The lengths differ on 29 operations, because the GA capability
	//     consolidation mapped several legacy privileges onto one capability.
	//     GET /v1/computer-groups is Scoped ["device-groups:read"] against
	//     Legacy ["Read Smart Computer Groups", "Read Static Computer
	//     Groups"]. There is no bijection to zip.
	//   - Where the lengths do match, the orders still disagree, in the spec
	//     itself. POST /v1/jamf-management-framework/redeploy/{id} is Scoped
	//     ["computer-check-in:read", "device-actions:execute"] against Legacy
	//     ["Send Computer Remote Command to Install Package", "Read Computer
	//     Check-In"] — reversed. 9 of the 24 equal-length multi-privilege
	//     operations are like this.
	//
	// A consumer rendering a table of required privileges therefore has to
	// present the two lists separately, or label from Scoped alone. Jamf does
	// not publish the scoped-to-legacy mapping in the specs; its "Jamf Pro
	// permissions map" documentation article is the only artefact that carries
	// it, and until that is machine-readable a correct label per scoped
	// identifier cannot be derived here.
	Legacy []string
}
`, pkg),

		"jamfplatform/rsql.go": fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

import "%s/internal/client"

type RSQLClause = client.RSQLClause

var BuildRSQLExpression = client.BuildRSQLExpression
var FormatArgument = client.FormatArgument
`, pkg, mod),

		"jamfplatform/poll.go": fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

import (
	"context"
	"time"

	"%s/internal/client"
)

func PollUntil(ctx context.Context, interval time.Duration, checker func(context.Context) (bool, error)) error {
	return client.PollUntil(ctx, interval, checker)
}
`, pkg, mod),

		"jamfplatform/types.go": fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

import (
	"context"
	"net/http"
	"time"
)

type TokenCache interface {
	Load(key string) (token string, expiresAt time.Time, ok bool)
	Store(key string, token string, expiresAt time.Time) error
}

type Logger interface {
	LogRequest(ctx context.Context, method, url string, body []byte)
	LogResponse(ctx context.Context, statusCode int, headers http.Header, body []byte)
}
`, pkg),

		"jamfplatform/helpers_test.go": fmt.Sprintf(`// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package %s

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testServer(t *testing.T) (*Client, *http.ServeMux) {
	t.Helper()
	return testServerWithOpts(t)
}

func testServerWithOpts(t *testing.T, opts ...Option) (*Client, *http.ServeMux) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-token",
			"token_type":   "bearer",
			"expires_in":   3600,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// Disable inter-request pacing in unit tests so the per-method httptest
	// suite isn't throttled to the default 100ms cadence. The gate's own
	// behavior is covered in internal/client. Also disable automatic
	// retry-on-transient-failure: a handler that deliberately keeps
	// returning e.g. 500 to exercise a caller's error-handling path would
	// otherwise hang for the production 1s-60s backoff window on every
	// run. Retry's own behavior is covered in internal/client too.
	clientOpts := append([]Option{WithMinRequestInterval(0), WithRetryPolicy(0, 0, 0)}, opts...)
	c := NewClient(srv.URL, "test-id", "test-secret", clientOpts...)
	return c, mux
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			t.Fatalf("writeJSON: %%v", err)
		}
	}
}

func readJSON(t *testing.T, r *http.Request, v any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatalf("readJSON: %%v", err)
	}
}

func ptrStr(s string) *string { return &s }
`, pkg),
	}

	for relPath, content := range staticFiles {
		outPath := filepath.Join(root, relPath)
		formatted, err := formatGo(relPath, []byte(content))
		if err != nil {
			return fmt.Errorf("formatting %s: %w", relPath, err)
		}
		if err := writeGenerated(outPath, formatted, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", relPath, err)
		}
		log.Printf("wrote %s", relPath)
	}
	return nil
}
