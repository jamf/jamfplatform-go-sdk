// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"log"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// jsonScalarKindNumber and jsonScalarKindBool name the generated
// jsonScalarKind constants a lenient key maps to. They are the only two
// kinds: a JSON string is the wire form the coercion accepts, and every
// other declared type either already arrives as a string or is a composite
// the per-type decoders reach through its own method.
const (
	jsonScalarKindNumber = "jsonScalarNumber"
	jsonScalarKindBool   = "jsonScalarBool"
)

// lenientType is one emitted Go struct that gets a tolerant UnmarshalJSON,
// with the JSON keys to coerce and the kind each is declared as.
//
// Keys may be empty. A type earns a decoder either because it declares a
// scalar of its own or because a scalar lives somewhere below it, and in the
// second case the decoder exists only to name the field a child decoder
// failed on — see attributeFieldError in the emitted source.
type lenientType struct {
	Name string
	Keys []lenientKey
}

// lenientKey is one property of a lenientType: the JSON name as it travels
// on the wire, and the generated kind constant naming what the spec declares.
type lenientKey struct {
	JSON string
	Kind string
}

// lenientUnionCase is one path from a discriminated union down to a leaf type
// that carries coerced keys, rendered as the flat JSON object the union's
// generated UnmarshalJSON dispatches on.
//
// It exists because a union hands the *same* bytes to the variant it selects,
// so every discriminator on the path and the leaf's own scalars live in one
// object. Nothing else in the generated tests decodes through that dispatch:
// the per-type table decodes each leaf directly, so a discriminator template
// that stopped delegating to a variant's own UnmarshalJSON would ship green.
type lenientUnionCase struct {
	Name       string // "SwUpdateConfiguration/AUTOMATIC/LATEST"
	UnionType  string // the type to decode into
	Path       string // dotted variant path, for the failure message
	Discrim    []lenientDiscrimValue
	ScalarJSON string // the leaf's coerced key
	ScalarKind string
}

// lenientDiscrimValue is one discriminator property and the value that routes
// to the next type on the path.
type lenientDiscrimValue struct {
	JSON  string
	Value string
}

// lenientDiagnostics records what the plan deliberately left uncovered, so a
// gap is visible in the generator's own output rather than only in a comment.
type lenientDiagnostics struct {
	// SkippedComposites names each "Type.jsonKey (goType)" whose leaf is a
	// number or boolean inside a slice or a map. The coercion does not reach
	// into a composite, and nothing else would report that it did not.
	SkippedComposites []string
	// OwnDecoder names each reached type that already carries a generated
	// UnmarshalJSON — a discriminated union, a request-body union, or a raw
	// JSON schema — and so cannot also carry a lenient one. Attribution stops
	// at such a type.
	OwnDecoder []string
}

// goScalarKind classifies a generated field type as the JSON scalar kind a
// string-encoded value would have to be coerced to, or "" when the field is
// not a scalar the coercion applies to.
//
// Composites are deliberately excluded rather than descended into: a field
// whose leaf is a struct is decoded by that struct's own generated method, and
// a slice or map of numbers is a shape no writer has been observed to
// string-encode. That second half is an observation rather than a guarantee,
// so compositeScalarKind below reports every such field the plan skips.
// Named aliases over an integer base (a numeric enum) count, since the wire
// form is the same integer.
func goScalarKind(fieldType string, aliasBase map[string]string) string {
	// A pointer is the same scalar; a slice or map is not, so only `*` comes
	// off. normalizeTypeRef would strip the containers too.
	bare := strings.TrimLeft(fieldType, "*")
	switch bare {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return jsonScalarKindNumber
	case "bool":
		return jsonScalarKindBool
	}
	switch aliasBase[bare] {
	case "int", "int64":
		return jsonScalarKindNumber
	}
	return ""
}

// compositeScalarKind classifies a field whose leaf is a scalar the coercion
// would cover but whose container it cannot reach into — a slice or a map of
// numbers or booleans. It returns "" for everything goScalarKind already
// answers for, and for a composite of anything else.
//
// The point is not to coerce these. It is that the exclusion rests on an
// observation about writers, and an observation needs a tripwire: a component
// that gains an array-of-integer property would otherwise pass both the
// missing-root and the no-scalar guard while leaving that property completely
// uncovered, which is the defect class the whole mechanism exists to fix.
func compositeScalarKind(fieldType string, aliasBase map[string]string) string {
	if goScalarKind(fieldType, aliasBase) != "" {
		return ""
	}
	bare := normalizeTypeRef(fieldType)
	if bare == fieldType {
		return ""
	}
	return goScalarKind(bare, aliasBase)
}

// numericEnumBases maps each emitted numeric-enum type name to its underlying
// Go base type, so a field typed as one of them is still recognised as a
// number. A string enum is absent: its wire form is already a JSON string.
func numericEnumBases(types []GoType) map[string]string {
	bases := make(map[string]string)
	for _, t := range types {
		if len(t.EnumValues) == 0 || len(t.Fields) > 0 {
			continue
		}
		switch t.EnumBaseType {
		case "int", "int64":
			bases[t.Name] = t.EnumBaseType
		}
	}
	return bases
}

// lenientScalarSeeds walks the schema graph from each named root and returns,
// per root, the Go type names its subtree reaches. Called with the doc after
// every schema pass has run, so hoisted inline objects are present under the
// names they will be emitted with.
//
// The result is keyed by root rather than merged because the no-scalar guard
// is a claim about one root: merging first makes a root that has lost every
// scalar indistinguishable from one whose sibling still has some.
//
// A root that names no schema in this spec is an error rather than a skip: the
// key exists to carry a wire fact about a specific store, and a root that has
// been renamed or withdrawn upstream silently drops the tolerance from every
// type under it.
func lenientScalarSeeds(doc *openapi3.T, roots []string) (map[string]map[string]bool, error) {
	seeds := make(map[string]map[string]bool)
	if len(roots) == 0 {
		return seeds, nil
	}
	if doc.Components == nil || doc.Components.Schemas == nil {
		return nil, fmt.Errorf("lenientScalarRoots names %s but the spec declares no component schemas",
			strings.Join(roots, ", "))
	}
	var missing []string
	for _, root := range roots {
		if _, ok := doc.Components.Schemas[root]; !ok {
			missing = append(missing, root)
			continue
		}
		seed := make(map[string]bool)
		visited := make(map[string]bool)
		walk := newSchemaWalker(doc, func(name string) bool {
			if visited[name] {
				return false
			}
			visited[name] = true
			seed[goTypeName(name)] = true
			return true
		})
		walk(doc.Components.Schemas[root])
		seeds[root] = seed
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("lenientScalarRoots names %d schema(s) this spec does not declare: %s\n\n"+
			"fix: rename the entry to the schema that replaced it, or delete it — a root that resolves to "+
			"nothing removes the tolerance from its whole subtree with no other signal",
			len(missing), strings.Join(missing, ", "))
	}
	return seeds, nil
}

// lenientRefs lists every emitted type name this type can hold a value of.
//
// A union's variant fields are rendered by the template rather than carried in
// Fields, so Fields alone stops dead at a discriminated or request-body union:
// SwUpdateConfiguration declares none, and a walk that reads only Fields never
// reaches SwUpdateLatestConfiguration's enforceAfterDays through it. That is
// the one place the schema seed and the Go-space closure disagree about what a
// type contains, and reading the variants back is what closes it.
func lenientRefs(t GoType) []string {
	var refs []string
	for _, f := range t.Fields {
		refs = append(refs, normalizeTypeRef(f.Type))
	}
	if t.Discriminator != nil {
		for _, v := range t.Discriminator.Variants {
			refs = append(refs, v.TypeName)
		}
	}
	if t.Union != nil {
		for _, v := range t.Union.Variants {
			refs = append(refs, v.TypeName)
		}
	}
	return refs
}

// ownsUnmarshalJSON reports whether the generator already emits an
// UnmarshalJSON for this type elsewhere, which makes a second one a
// redeclaration the package cannot compile.
//
// Three shapes do: a discriminated union and a request-body union both get a
// dispatching decoder from template.go, and a raw-JSON schema gets one that
// preserves the payload. Excluding them is not a silent skip — the plan
// records each in OwnDecoder, and a type that also declares a coerced scalar
// fails generation outright, because there the tolerance is genuinely lost
// rather than merely unattributable.
func ownsUnmarshalJSON(t GoType) bool {
	return t.Discriminator != nil || t.Union != nil || t.IsRawJSON
}

// lenientScalarTypes closes one root's seed over the emitted types' own field
// references and returns, in emission order, every struct that needs a
// tolerant decoder.
//
// The closure is needed because the seed is derived from schema names while
// the tolerance is emitted against Go types: a field whose type was hoisted
// out of an inline object, or renamed on the way to Go, is reachable only by
// following the emitted field types.
//
// A type needs a decoder when a coerced scalar lies at or below it. Below
// matters as much as at: encoding/json hands a nested value straight to that
// type's UnmarshalJSON and returns whatever comes back, so without a decoder
// on the parent a child's failure arrives naming only the child's own type —
// and Deferrals declares four fields of the identical OptionalPeriodInDays
// type, so that error cannot say which field failed. The parent's decoder
// exists to put the field name back.
func lenientScalarTypes(types []GoType, seed map[string]bool) ([]lenientType, lenientDiagnostics, error) {
	var diags lenientDiagnostics
	byName := make(map[string]GoType, len(types))
	for _, t := range types {
		byName[t.Name] = t
	}
	reached := make(map[string]bool)
	var queue []string
	for name := range seed {
		if _, ok := byName[name]; ok {
			queue = append(queue, name)
		}
	}
	slices.Sort(queue)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if reached[name] {
			continue
		}
		reached[name] = true
		for _, ref := range lenientRefs(byName[name]) {
			if _, ok := byName[ref]; ok && !reached[ref] {
				queue = append(queue, ref)
			}
		}
	}

	aliasBase := numericEnumBases(types)
	keysOf := func(t GoType) []lenientKey {
		var keys []lenientKey
		for _, f := range t.Fields {
			kind := goScalarKind(f.Type, aliasBase)
			if kind == "" {
				continue
			}
			name := jsonTagName(f.JSONTag)
			if name == "" {
				continue
			}
			keys = append(keys, lenientKey{JSON: name, Kind: kind})
		}
		slices.SortFunc(keys, func(a, b lenientKey) int { return strings.Compare(a.JSON, b.JSON) })
		return keys
	}

	// reachesScalar memoises "a coerced scalar lies at or below this type",
	// following field references inside the reached set only. A type that
	// carries its own UnmarshalJSON still propagates: attribution cannot pass
	// through it, but its ancestors are still worth a decoder.
	state := make(map[string]int) // 0 unknown, 1 in progress, 2 false, 3 true
	var reachesScalar func(name string) bool
	reachesScalar = func(name string) bool {
		switch state[name] {
		case 1, 2:
			return false // a cycle answers false on the way back down
		case 3:
			return true
		}
		state[name] = 1
		t := byName[name]
		if len(keysOf(t)) > 0 {
			state[name] = 3
			return true
		}
		for _, ref := range lenientRefs(t) {
			if ref == name || !reached[ref] {
				continue
			}
			if _, ok := byName[ref]; !ok {
				continue
			}
			if reachesScalar(ref) {
				state[name] = 3
				return true
			}
		}
		state[name] = 2
		return false
	}

	var out []lenientType
	var refused []string
	for _, t := range types {
		// Not gated on len(t.Fields): a union carries its variants in
		// Discriminator or Union rather than in Fields, so a field count of
		// zero would skip exactly the types the OwnDecoder report exists for.
		// reachesScalar is the real filter, and it answers false for a type
		// that holds nothing.
		if !reached[t.Name] {
			continue
		}
		keys := keysOf(t)
		for _, f := range t.Fields {
			if kind := compositeScalarKind(f.Type, aliasBase); kind != "" {
				if name := jsonTagName(f.JSONTag); name != "" {
					diags.SkippedComposites = append(diags.SkippedComposites,
						fmt.Sprintf("%s.%s (%s)", t.Name, name, f.Type))
				}
			}
		}
		if ownsUnmarshalJSON(t) {
			if len(keys) > 0 {
				refused = append(refused, t.Name)
				continue
			}
			if reachesScalar(t.Name) {
				diags.OwnDecoder = append(diags.OwnDecoder, t.Name)
			}
			continue
		}
		if !reachesScalar(t.Name) {
			continue
		}
		out = append(out, lenientType{Name: t.Name, Keys: keys})
	}
	if len(refused) > 0 {
		return nil, diags, fmt.Errorf("lenientScalarRoots reaches %d type(s) that already carry a generated "+
			"UnmarshalJSON and also declare a coerced scalar: %s\n\n"+
			"fix: a second UnmarshalJSON on the same type will not compile, so the tolerance cannot be "+
			"emitted here. Teach the discriminator or union template to coerce the scalar itself, or move "+
			"the scalar out from under the union",
			len(refused), strings.Join(refused, ", "))
	}
	return out, diags, nil
}

// lenientUnionCases builds one case per path from a discriminated union down to
// a leaf that carries a coerced number, so the generated test decodes through
// the union's own dispatch rather than only into the leaf directly.
//
// A union hands the same bytes to the variant it selects, so the whole path
// renders as one flat object: every discriminator property on the way plus the
// leaf's scalar. Paths are bounded by depth rather than by a visited set, since
// two different values can legitimately route to the same variant type.
func lenientUnionCases(types []GoType, entries []lenientType) []lenientUnionCase {
	byName := make(map[string]GoType, len(types))
	for _, t := range types {
		byName[t.Name] = t
	}
	keyed := make(map[string]lenientKey)
	for _, e := range entries {
		for _, k := range e.Keys {
			if k.Kind == jsonScalarKindNumber {
				keyed[e.Name] = k
				break
			}
		}
	}

	var cases []lenientUnionCase
	// root is the type the case decodes into, which stays the entry point all
	// the way down: a nested chain is still one object handed to the outer
	// union, and a case naming the inner union would decode past the very
	// dispatch it exists to exercise.
	var walk func(root, union GoType, path []string, discrim []lenientDiscrimValue, depth int)
	walk = func(root, union GoType, path []string, discrim []lenientDiscrimValue, depth int) {
		if union.Discriminator == nil || depth > 4 {
			return
		}
		for _, v := range union.Discriminator.Variants {
			if len(v.Values) == 0 {
				continue
			}
			next := append(slices.Clone(discrim), lenientDiscrimValue{
				JSON:  union.Discriminator.PropertyName,
				Value: v.Values[0],
			})
			nextPath := append(slices.Clone(path), v.Values[0])
			if key, ok := keyed[v.TypeName]; ok {
				cases = append(cases, lenientUnionCase{
					Name:       strings.Join(append([]string{root.Name}, nextPath...), "/"),
					UnionType:  root.Name,
					Path:       strings.Join(nextPath, "."),
					Discrim:    next,
					ScalarJSON: key.JSON,
					ScalarKind: key.Kind,
				})
				continue
			}
			if inner, ok := byName[v.TypeName]; ok && inner.Discriminator != nil {
				walk(root, inner, nextPath, next, depth+1)
			}
		}
	}
	// Every union is an entry point, inner ones included: decoding an inner
	// union directly is a dispatch a caller can reach, so it gets its own case
	// rather than only appearing as a leg of the outer chain.
	for _, t := range types {
		if t.Discriminator == nil {
			continue
		}
		walk(t, t, nil, nil, 0)
	}
	slices.SortFunc(cases, func(a, b lenientUnionCase) int { return strings.Compare(a.Name, b.Name) })
	return cases
}

// jsonTagName returns the wire name from a struct tag body, dropping the
// options after the first comma. An empty name yields "": the field does not
// travel under a key the rewrite could find.
//
// Follows encoding/json's own rule for the dash, pedantic as it is: a tag of
// exactly "-" skips the field, while "-," names it "-". Matching the decoder
// exactly is the only way a key list can be trusted to describe what the
// decoder will look for.
func jsonTagName(tag string) string {
	if tag == "-" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	return name
}

// lenientRuntimeSource is the package-independent half of lenient_scalars.go:
// the kind constants and the four decode helpers every generated method calls.
//
// Kept out of the Fprintf that writes the header because it carries %s and %w
// verbs of its own, which a format string would consume.
const lenientRuntimeSource = `
// jsonScalarKind names the JSON scalar a property is declared as.
type jsonScalarKind uint8

const (
	// jsonScalarNumber: the spec declares an integer or a number.
	jsonScalarNumber jsonScalarKind = iota + 1
	// jsonScalarBool: the spec declares a boolean.
	jsonScalarBool
)

// unmarshalLenient decodes data into v, accepting a JSON string wherever keys
// declares a number or a boolean, and naming the field a failure came from.
//
// The strict decode is tried first, so a conforming body costs one error check
// and nothing else. A nested value has already been fixed by its own type's
// method during that attempt, which is why each type only has to describe its
// own keys. Note what that does not say about cost: the retry re-decodes the
// whole object, so a quoted value on this type's own key runs every composite
// child's decoder a second time. The alternative — decoding key by key on the
// success path — would tax every conforming body to save a cold one.
//
// Three properties worth relying on. Only the listed keys are considered, so a
// string the spec declares as a string is never touched. Only a JSON string is
// rewritten, and only when the text it carries is a valid JSON scalar of the
// declared kind — so "abc" for an integer still fails the decode rather than
// arriving as zero. And marshalling is untouched: the SDK keeps sending the
// numbers and booleans the spec declares, which makes the tolerance
// one-directional. Decoding a quoted value and writing the struct back emits
// the bare scalar, so a round trip through these types rewrites the encoding
// the store was holding.
func unmarshalLenient(data []byte, v any, keys map[string]jsonScalarKind, name string) error {
	err := json.Unmarshal(data, v)
	if err == nil {
		return nil
	}
	// body is what the reported error describes, which is the rewritten
	// document whenever a rewrite happened. Attributing against the original
	// would blame the key the rewrite already fixed.
	body := data
	if fixed, rewritten := unquoteJSONScalars(data, keys); rewritten {
		// The second error is the one to report when it comes: the rewrite
		// has handled the encoding the first error described, so what is left
		// is a fault the coercion has nothing to do with.
		retryErr := json.Unmarshal(fixed, v)
		if retryErr == nil {
			return nil
		}
		err, body = retryErr, fixed
	}
	return attributeFieldError(body, v, err, name)
}

// attributeFieldError prefixes err with the field the failure came from.
//
// encoding/json hands a nested value straight to that type's UnmarshalJSON and
// returns whatever it gets back, so a child decoder's error arrives with no
// record of the field it travelled through. Deferrals declares four fields of
// the identical OptionalPeriodInDays type, so the error alone cannot say which
// one failed — and that path is how the decode bug this whole mechanism exists
// to fix was diagnosed.
//
// It runs only on the error path, and it decides nothing: it decodes each
// present key on its own into a fresh value of that field's type, and the
// first key that reproduces a failure is the one to name. A key whose probe
// succeeds is left alone, and a failure no probe reproduces is returned
// unattributed rather than guessed at.
func attributeFieldError(data []byte, v any, err error, name string) error {
	err = namedStructError(err, name)
	var obj map[string]json.RawMessage
	if json.Unmarshal(data, &obj) != nil {
		return err
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Struct {
		return err
	}
	t := rv.Elem().Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue
		}
		key := jsonFieldName(f)
		if key == "" {
			continue
		}
		raw, present := obj[key]
		if !present {
			continue
		}
		probe := reflect.New(f.Type)
		if fieldErr := json.Unmarshal(raw, probe.Interface()); fieldErr != nil {
			return fmt.Errorf("%s.%s: %w", name, key, fieldErr)
		}
	}
	return err
}

// jsonFieldName returns the wire name a struct field travels under, following
// encoding/json's own rule for the dash: a tag of exactly "-" skips the field,
// while "-," names it "-". An absent tag leaves the field name.
func jsonFieldName(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name
	}
	if tag == "-" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" {
		return f.Name
	}
	return name
}

// namedStructError puts the real type name back into a decode error.
//
// Each generated decoder decodes into a local type named "lenient" to shed the
// method and avoid recursing, and encoding/json reports the *Go* type it was
// decoding — so without this a caller reads "json: cannot unmarshal string
// into Go struct field lenient.Value", which names nothing they can look up.
// It restores the type name only; the field path is what attributeFieldError
// puts back.
func namedStructError(err error, name string) error {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Struct == "lenient" {
		typeErr.Struct = name
	}
	return err
}

// unquoteJSONScalars returns data with every listed key whose value arrived as
// a JSON string rewritten to the bare scalar its kind declares. The second
// return is false when nothing was rewritten, which includes a body that is
// not a JSON object at all — the caller then reports the original error.
func unquoteJSONScalars(data []byte, keys map[string]jsonScalarKind) ([]byte, bool) {
	if len(keys) == 0 {
		return data, false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return data, false
	}
	changed := false
	for key, kind := range keys {
		raw, present := obj[key]
		if !present {
			continue
		}
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			// Not a JSON string: already the declared shape, or a shape this
			// coercion has nothing to say about.
			continue
		}
		lit, ok := jsonScalarLiteral(s, kind)
		if !ok {
			continue
		}
		obj[key] = json.RawMessage(lit)
		changed = true
	}
	if !changed {
		return data, false
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return data, false
	}
	return out, true
}

// jsonScalarLiteral returns s as a bare JSON literal of the given kind, and
// false when it is not one.
//
// The number branch re-emits the text verbatim rather than parsing and
// reformatting it, so a value wider than float64 keeps every digit and a
// decimal keeps its exact spelling. Two checks decide it, and both are
// load-bearing: json.Valid rules out the forms Go's own parsers accept and
// JSON does not (Inf, NaN, hex floats, a leading +, 01), and the leading-byte
// check rules out the values that are valid JSON but are not numbers (null,
// true, false, an array, an object). Without the second, a quoted "null"
// would be rewritten to bare null, which decodes into a non-pointer field as
// a silent no-op and leaves the zero value — the one outcome this whole
// mechanism must never produce.
func jsonScalarLiteral(s string, kind jsonScalarKind) (string, bool) {
	switch kind {
	case jsonScalarNumber:
		if s == "" || !json.Valid([]byte(s)) {
			return "", false
		}
		if c := s[0]; c != '-' && (c < '0' || c > '9') {
			return "", false
		}
		return s, true
	case jsonScalarBool:
		if s == "true" || s == "false" {
			return s, true
		}
	}
	return "", false
}
`

// emitPkgLenientScalars writes lenient_scalars.go: the coercion helpers plus
// one UnmarshalJSON per type in entries. A package with no entries gets no
// file, and a spec that asked for roots but produced none is an error — see
// validateLenientScalars.
func emitPkgLenientScalars(pkgDir, pkgName string, entries []lenientType) error {
	if len(entries) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, `// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

// Tolerant decoders for the schemas config names in lenientScalarRoots: a
// store that serves back the JSON its writer sent, rather than re-serialising
// from its own model, answers whatever scalar encoding that writer used.
//
// Emitted here rather than in types.go so the field types stay exactly what
// the spec declares — the tolerance is in the decode, not in the surface a
// consumer programs against — and so this file is the one place to read for
// what the coercion does and does not do.
//
// The tolerance is one-directional. Marshalling is untouched, so decoding a
// quoted scalar and writing the struct back sends the bare value the spec
// declares, and the store then holds that encoding instead.

package %s

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)
`, pkgName)
	b.WriteString(lenientRuntimeSource)

	for _, e := range entries {
		fmt.Fprintf(&b, "\n// lenientScalars%s names the scalars %s declares, for its UnmarshalJSON.\n", e.Name, e.Name)
		if len(e.Keys) == 0 {
			fmt.Fprintf(&b, "// It declares none of its own: the decoder exists to name the field a\n"+
				"// child decoder failed on.\nvar lenientScalars%s = map[string]jsonScalarKind{}\n", e.Name)
		} else {
			fmt.Fprintf(&b, "var lenientScalars%s = map[string]jsonScalarKind{\n", e.Name)
			for _, k := range e.Keys {
				fmt.Fprintf(&b, "\t%q: %s,\n", k.JSON, k.Kind)
			}
			b.WriteString("}\n")
		}
		fmt.Fprintf(&b, `
// UnmarshalJSON decodes s, accepting a JSON string for any number or boolean
// it declares, because the store this schema comes from serves back the
// scalar encoding its writer used. A failure names the field it came from.
//
// Marshalling is unaffected, so writing s back sends the bare number or
// boolean the spec declares rather than the encoding it was read as.
func (s *%s) UnmarshalJSON(data []byte) error {
	type lenient %s
	var v lenient
	if err := unmarshalLenient(data, &v, lenientScalars%s, %q); err != nil {
		return err
	}
	*s = %s(v)
	return nil
}
`, e.Name, e.Name, e.Name, e.Name, e.Name)
	}

	outPath := filepath.Join(pkgDir, "lenient_scalars.go")
	formatted, err := formatGo("lenient_scalars.go", []byte(b.String()))
	if err != nil {
		return fmt.Errorf("formatting lenient_scalars.go: %w", err)
	}
	if err := writeGenerated(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	log.Printf("wrote %s (%d types)", outPath, len(entries))
	return nil
}

// validateLenientScalars refuses a root that asked for tolerance and got none,
// and reports every gap the plan left.
//
// The check is per root, not per package: a root resolves but reaches no
// number or boolean when upstream has moved the scalars out from under it, and
// with the roots merged first a sibling root's entries would hide that. The
// entry is then a claim about a store that no longer needs it, and failing
// here is what deletes it — the same way a redundant scopeTypes override
// fails.
func validateLenientScalars(pkgContext string, perRoot map[string][]lenientType, diags lenientDiagnostics) error {
	var dead []string
	for _, root := range slices.Sorted(maps.Keys(perRoot)) {
		if len(perRoot[root]) == 0 {
			dead = append(dead, root)
		}
	}
	if len(dead) > 0 {
		return fmt.Errorf("%s: lenientScalarRoots names %s but its subtree reaches no number or boolean field\n\n"+
			"fix: delete the entry — every scalar it covered has gone, so the tolerance has nothing left to do",
			pkgContext, strings.Join(dead, ", "))
	}
	for _, name := range diags.OwnDecoder {
		log.Printf("lenient scalars: %s carries its own UnmarshalJSON, so a failure below it is not "+
			"attributed to a field", name)
	}
	for _, field := range diags.SkippedComposites {
		log.Printf("lenient scalars: %s holds its scalars in a slice or map, which the coercion does not "+
			"reach into", field)
	}
	return nil
}

// lenientParentCase is one parent type paired with a child field that carries
// a coerced number, for the generated test that pins field attribution.
//
// It is the regression for the defect the parent decoders exist to fix: a
// failure inside the child used to arrive naming only the child's own type,
// which is indistinguishable across sibling fields of the same type.
type lenientParentCase struct {
	Name      string // parent type
	ChildJSON string // the parent's key the child travels under
	ChildKey  string // the child's own coerced key
}

// lenientParentCases pairs each emitted type with one child field whose type
// is itself emitted and declares a coerced number.
//
// One child per parent is enough: what is pinned is that the parent names the
// field at all, and a second field of the same shape adds a case without
// adding an assertion. Sibling fields sharing a type are exactly why the
// mechanism exists, so the pick is deterministic rather than arbitrary — the
// first field in the parent's declared order.
func lenientParentCases(types []GoType, entries []lenientType) []lenientParentCase {
	byName := make(map[string]GoType, len(types))
	for _, t := range types {
		byName[t.Name] = t
	}
	numberKey := make(map[string]string)
	for _, e := range entries {
		for _, k := range e.Keys {
			if k.Kind == jsonScalarKindNumber {
				numberKey[e.Name] = k.JSON
				break
			}
		}
	}
	var cases []lenientParentCase
	for _, e := range entries {
		for _, f := range byName[e.Name].Fields {
			ref := normalizeTypeRef(f.Type)
			key, ok := numberKey[ref]
			if !ok || ref == e.Name {
				continue
			}
			jsonKey := jsonTagName(f.JSONTag)
			if jsonKey == "" {
				continue
			}
			cases = append(cases, lenientParentCase{Name: e.Name, ChildJSON: jsonKey, ChildKey: key})
			break
		}
	}
	return cases
}

// lenientScalarsTestTemplate is the fixed body of lenient_scalars_test.go.
// @BQ@ and @DQ@ stand in for a backtick and a double quote so the whole thing
// can live in one raw string; @PKG@ is the package name, @CASES@ the per-type
// table rows, @UNIONCASES@ the union-dispatch rows and @PARENTCASES@ the
// attribution rows. Written this way rather than through a format string
// because the generated source is dense in quotes of both kinds and a Sprintf
// verb in the middle of them is where the last attempt went wrong.
const lenientScalarsTestTemplate = `// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package @PKG@

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// lenientScalarCase is one type carrying a tolerant UnmarshalJSON, paired with
// the scalar keys that decoder coerces.
type lenientScalarCase struct {
	name string
	typ  reflect.Type
	keys map[string]jsonScalarKind
}

// lenientUnionCase is one path from a discriminated union down to a leaf that
// carries a coerced number, rendered as the flat object the union's own
// UnmarshalJSON dispatches on.
type lenientUnionCase struct {
	name       string
	typ        reflect.Type
	discrim    [][2]string
	scalarJSON string
}

// lenientParentCase is one parent type paired with a child field carrying a
// coerced number, for the attribution assertion.
type lenientParentCase struct {
	name      string
	typ       reflect.Type
	childJSON string
	childKey  string
}

// body renders a JSON object setting every key in the case, quoting the values
// when quoted is true. Keys are emitted in sorted order so a failure names the
// same body every run.
func (c lenientScalarCase) body(quoted bool) string {
	names := make([]string, 0, len(c.keys))
	for name := range c.keys {
		names = append(names, name)
	}
	slices.Sort(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		lit := "1"
		if c.keys[name] == jsonScalarBool {
			lit = "true"
		}
		if quoted {
			lit = @BQ@"@BQ@ + lit + @BQ@"@BQ@
		}
		parts = append(parts, fmt.Sprintf("%q:%s", name, lit))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// body renders the union path as one flat object: every discriminator on the
// way down plus the leaf's own scalar, which is what the dispatch actually
// receives since a union hands the same bytes to the variant it selects.
func (c lenientUnionCase) body(quoted bool) string {
	parts := make([]string, 0, len(c.discrim)+1)
	for _, d := range c.discrim {
		parts = append(parts, fmt.Sprintf("%q:%q", d[0], d[1]))
	}
	lit := "1"
	if quoted {
		lit = @BQ@"1"@BQ@
	}
	parts = append(parts, fmt.Sprintf("%q:%s", c.scalarJSON, lit))
	return "{" + strings.Join(parts, ",") + "}"
}

// TestLenientScalars_StringEncodingDecodesToTheSameValue is the regression for
// a store that echoes its writer's JSON: every number and boolean under a
// lenientScalarRoots root must decode from the quoted form to exactly what the
// bare form produces. Comparing the two decodes rather than a literal expected
// value is what keeps this honest when a spec moves a field's type.
func TestLenientScalars_StringEncodingDecodesToTheSameValue(t *testing.T) {
	for _, c := range lenientScalarCases {
		t.Run(c.name, func(t *testing.T) {
			bare := reflect.New(c.typ)
			if err := json.Unmarshal([]byte(c.body(false)), bare.Interface()); err != nil {
				t.Fatalf("decoding the bare form %s: %v", c.body(false), err)
			}
			quoted := reflect.New(c.typ)
			if err := json.Unmarshal([]byte(c.body(true)), quoted.Interface()); err != nil {
				t.Fatalf("decoding the string form %s: %v", c.body(true), err)
			}
			if !reflect.DeepEqual(bare.Elem().Interface(), quoted.Elem().Interface()) {
				t.Fatalf("string form decoded differently:\n bare:   %#v\n quoted: %#v",
					bare.Elem().Interface(), quoted.Elem().Interface())
			}
		})
	}
}

// TestLenientScalars_KeysAreRealFieldTags pins that every coerced key names a
// field the type actually declares. The equality test above cannot see this:
// a key matching no field is ignored by encoding/json on both the bare and the
// quoted side, so both decodes land on the same untouched zero value and the
// comparison passes with nothing coerced.
func TestLenientScalars_KeysAreRealFieldTags(t *testing.T) {
	for _, c := range lenientScalarCases {
		t.Run(c.name, func(t *testing.T) {
			declared := make(map[string]bool)
			for i := range c.typ.NumField() {
				if name := jsonFieldName(c.typ.Field(i)); name != "" {
					declared[name] = true
				}
			}
			for key := range c.keys {
				if !declared[key] {
					t.Errorf("coerced key %q names no field of %s", key, c.name)
				}
			}
		})
	}
}

// TestLenientScalars_UnionDispatchReachesTheLenientLeaf decodes through a
// discriminated union's own UnmarshalJSON rather than into the leaf directly.
// Every other test here constructs the leaf type itself, so a discriminator
// template that stopped delegating to a variant's own UnmarshalJSON — by
// decoding into a value copy, say — would ship with the whole suite green.
func TestLenientScalars_UnionDispatchReachesTheLenientLeaf(t *testing.T) {
	if len(lenientUnionCases) == 0 {
		t.Skip("no discriminated union in this package reaches a coerced number")
	}
	for _, c := range lenientUnionCases {
		t.Run(c.name, func(t *testing.T) {
			bare := reflect.New(c.typ)
			if err := json.Unmarshal([]byte(c.body(false)), bare.Interface()); err != nil {
				t.Fatalf("decoding the bare form %s: %v", c.body(false), err)
			}
			quoted := reflect.New(c.typ)
			if err := json.Unmarshal([]byte(c.body(true)), quoted.Interface()); err != nil {
				t.Fatalf("decoding the string form %s through the union: %v", c.body(true), err)
			}
			if !reflect.DeepEqual(bare.Elem().Interface(), quoted.Elem().Interface()) {
				t.Fatalf("the union dispatched the two encodings differently:\n bare:   %#v\n quoted: %#v",
					bare.Elem().Interface(), quoted.Elem().Interface())
			}
		})
	}
}

// TestLenientScalars_FailureNamesTheChildField is the regression for the
// diagnostic half. encoding/json returns a nested Unmarshaler's error verbatim,
// so before the parent decoders a failure inside a child arrived naming only
// the child's own type — and a parent declaring several fields of that one type
// left the caller unable to tell which field was at fault.
func TestLenientScalars_FailureNamesTheChildField(t *testing.T) {
	if len(lenientParentCases) == 0 {
		t.Skip("no emitted type has a child carrying a coerced number")
	}
	for _, c := range lenientParentCases {
		t.Run(c.name+"/"+c.childJSON, func(t *testing.T) {
			body := fmt.Sprintf(@BQ@{%q:{%q:"not-a-number"}}@BQ@, c.childJSON, c.childKey)
			v := reflect.New(c.typ)
			err := json.Unmarshal([]byte(body), v.Interface())
			if err == nil {
				t.Fatalf("decoding %s should fail", body)
			}
			want := c.name + "." + c.childJSON
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error does not name the field %q: %v", want, err)
			}
		})
	}
}

// TestLenientScalars_NonScalarStringStillFails pins the boundary. The coercion
// unquotes a string only when the text it carries is a valid JSON scalar of the
// declared kind, so a genuinely wrong value has to keep failing the decode —
// letting it through as zero would turn a visible fault into a silently wrong
// configuration.
func TestLenientScalars_NonScalarStringStillFails(t *testing.T) {
	for _, c := range lenientScalarCases {
		for name, kind := range c.keys {
			bad := "not-a-number"
			if kind == jsonScalarBool {
				bad = "yes"
			}
			t.Run(c.name+"/"+name, func(t *testing.T) {
				v := reflect.New(c.typ)
				body := fmt.Sprintf(@BQ@{%q:%q}@BQ@, name, bad)
				if err := json.Unmarshal([]byte(body), v.Interface()); err == nil {
					t.Fatalf("decoding %s succeeded; a string that is not a scalar of kind %d must still fail", body, kind)
				}
			})
		}
	}
}

// TestLenientScalars_ValidJSONNonNumberStillFails is the other half of that
// boundary, and the half json.Valid cannot carry. "null", "true" and the
// bracket forms are all valid JSON, so only the leading-byte check rejects
// them — and a quoted "null" rewritten to bare null decodes into a
// non-pointer field as a silent no-op, which is the one outcome the coercion
// must never produce.
func TestLenientScalars_ValidJSONNonNumberStillFails(t *testing.T) {
	for _, c := range lenientScalarCases {
		for name, kind := range c.keys {
			if kind != jsonScalarNumber {
				continue
			}
			for _, bad := range []string{"null", "true", "false", "[]", "{}"} {
				t.Run(c.name+"/"+name+"/"+bad, func(t *testing.T) {
					v := reflect.New(c.typ)
					body := fmt.Sprintf(@BQ@{%q:%q}@BQ@, name, bad)
					if err := json.Unmarshal([]byte(body), v.Interface()); err == nil {
						t.Fatalf("decoding %s succeeded; %q is valid JSON but not a number", body, bad)
					}
				})
			}
			break
		}
	}
}

// TestLenientScalars_UnlistedKeysAreUntouched pins that the rewrite is keyed on
// the type's own declared scalars: a string the spec declares as a string must
// survive verbatim, which is what stops the coercion mangling prose that
// happens to look numeric.
func TestLenientScalars_UnlistedKeysAreUntouched(t *testing.T) {
	keys := map[string]jsonScalarKind{"count": jsonScalarNumber}
	out, changed := unquoteJSONScalars([]byte(@BQ@{"count":"7","label":"7"}@BQ@), keys)
	if !changed {
		t.Fatal("the listed key was a string and should have been rewritten")
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("re-decoding the rewritten body: %v", err)
	}
	if string(got["count"]) != "7" {
		t.Errorf("count = %s, want the bare number 7", got["count"])
	}
	if string(got["label"]) != @BQ@"7"@BQ@ {
		t.Errorf("label = %s, want the string untouched", got["label"])
	}
}

// TestLenientScalars_NonObjectBodyKeepsTheOriginalError pins that a body the
// rewrite cannot parse as an object reports the decode's own error rather than
// one about a body the caller never sent.
func TestLenientScalars_NonObjectBodyKeepsTheOriginalError(t *testing.T) {
	var target struct {
		Count int @BQ@json:"count"@BQ@
	}
	err := unmarshalLenient([]byte(@BQ@["not","an","object"]@BQ@), &target,
		map[string]jsonScalarKind{"count": jsonScalarNumber}, "probe")
	if err == nil {
		t.Fatal("decoding a JSON array into a struct should fail")
	}
	if !strings.Contains(err.Error(), "array") {
		t.Errorf("error = %v, want the original decode error naming the array", err)
	}
}

// TestLenientScalars_ReportsThePostRewriteError pins which of the two errors a
// caller sees. The rewrite has already handled the encoding the first error
// described, so reporting that one blames a key the coercion fixed and hides
// the fault that is actually left.
func TestLenientScalars_ReportsThePostRewriteError(t *testing.T) {
	type child struct {
		N int @BQ@json:"n"@BQ@
	}
	var target struct {
		Count int    @BQ@json:"count"@BQ@
		Child *child @BQ@json:"child"@BQ@
	}
	err := unmarshalLenient([]byte(@BQ@{"count":"9","child":123}@BQ@), &target,
		map[string]jsonScalarKind{"count": jsonScalarNumber}, "probe")
	if err == nil {
		t.Fatal("decoding an integer into a struct field should fail")
	}
	if !strings.Contains(err.Error(), "child") {
		t.Errorf("error = %v, want it to name child, the fault the rewrite did not fix", err)
	}
	if strings.Contains(err.Error(), "count") {
		t.Errorf("error = %v, names count, which the rewrite already fixed", err)
	}
}

// TestLenientScalars_WideNumberKeepsItsDigits pins that the number branch
// re-emits the text rather than parsing and reformatting it, so a value beyond
// float64's exact range is not silently rounded on the way through.
func TestLenientScalars_WideNumberKeepsItsDigits(t *testing.T) {
	var target struct {
		Count int64 @BQ@json:"count"@BQ@
	}
	const want = 9007199254740993
	if err := unmarshalLenient([]byte(@BQ@{"count":"9007199254740993"}@BQ@), &target,
		map[string]jsonScalarKind{"count": jsonScalarNumber}, "probe"); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if target.Count != want {
		t.Errorf("Count = %d, want %d", target.Count, want)
	}
}

// TestLenientScalars_JSONScalarLiteralRejectsNonJSONNumbers pins both halves of
// the number check. The first group is what json.Valid rejects: forms Go's own
// parsers accept and JSON does not. The second is what only the leading-byte
// check rejects: text that is valid JSON but is not a number.
func TestLenientScalars_JSONScalarLiteralRejectsNonJSONNumbers(t *testing.T) {
	for _, s := range []string{"", " ", "+1", "1_0", "0x10", "1e", "Inf", "NaN", "01", ".5", "1.2.3"} {
		if lit, ok := jsonScalarLiteral(s, jsonScalarNumber); ok {
			t.Errorf("jsonScalarLiteral(%q) = %q, true; want rejected", s, lit)
		}
	}
	for _, s := range []string{"null", "true", "false", "[]", "{}", @BQ@"5"@BQ@, "[1,2]"} {
		if !json.Valid([]byte(s)) {
			t.Fatalf("%q is meant to be valid JSON; the case has stopped testing the leading-byte check", s)
		}
		if lit, ok := jsonScalarLiteral(s, jsonScalarNumber); ok {
			t.Errorf("jsonScalarLiteral(%q) = %q, true; want rejected — valid JSON, not a number", s, lit)
		}
	}
	for _, s := range []string{"0", "-1", "1.5", "1e5", "-1.5e-3", "90"} {
		if lit, ok := jsonScalarLiteral(s, jsonScalarNumber); !ok || lit != s {
			t.Errorf("jsonScalarLiteral(%q) = %q, %v; want %q, true", s, lit, ok, s)
		}
	}
	for _, s := range []string{"True", "TRUE", "1", "", "yes", "null"} {
		if lit, ok := jsonScalarLiteral(s, jsonScalarBool); ok {
			t.Errorf("jsonScalarLiteral(%q, bool) = %q, true; want rejected", s, lit)
		}
	}
}

// TestLenientScalars_DecodeErrorNamesTheRealType pins that a failure names the
// type a caller can look up, not the local alias each decoder decodes into.
func TestLenientScalars_DecodeErrorNamesTheRealType(t *testing.T) {
	for _, c := range lenientScalarCases {
		for name, kind := range c.keys {
			if kind != jsonScalarNumber {
				continue
			}
			v := reflect.New(c.typ)
			err := json.Unmarshal([]byte(fmt.Sprintf(@BQ@{%q:"not-a-number"}@BQ@, name)), v.Interface())
			if err == nil {
				t.Fatalf("%s.%s: decoding a non-numeric string should fail", c.name, name)
			}
			if strings.Contains(err.Error(), "lenient.") {
				t.Errorf("%s.%s: error names the local alias: %v", c.name, name, err)
			}
			if !strings.Contains(err.Error(), c.name) {
				t.Errorf("%s.%s: error does not name the type: %v", c.name, name, err)
			}
			break
		}
	}
}

var lenientScalarCases = []lenientScalarCase{
@CASES@}

var lenientUnionCases = []lenientUnionCase{
@UNIONCASES@}

var lenientParentCases = []lenientParentCase{
@PARENTCASES@}
`

// emitPkgLenientScalarsTest writes lenient_scalars_test.go: a table over every
// type that got a tolerant decoder, asserting the string encoding decodes to
// the same value the bare scalar does, that a string carrying something that is
// not a scalar of the declared kind still fails, that a failure names the field
// it came from, and that a union's dispatch still reaches a lenient leaf.
//
// The tables are reflective rather than one hand-shaped case per type because
// what is being pinned is uniform: dozens of near-identical literal fixtures
// would go stale the first time a spec moved a field, and equality against the
// bare-scalar decode is a stronger assertion than any single expected value.
func emitPkgLenientScalarsTest(pkgDir, pkgName string, entries []lenientType,
	unions []lenientUnionCase, parents []lenientParentCase) error {
	if len(entries) == 0 {
		return nil
	}
	var cases strings.Builder
	for _, e := range entries {
		if len(e.Keys) == 0 {
			continue
		}
		fmt.Fprintf(&cases, "\t{name: %q, typ: reflect.TypeOf(%s{}), keys: lenientScalars%s},\n", e.Name, e.Name, e.Name)
	}
	var unionCases strings.Builder
	for _, u := range unions {
		fmt.Fprintf(&unionCases, "\t{name: %q, typ: reflect.TypeOf(%s{}), scalarJSON: %q, discrim: [][2]string{",
			u.Name, u.UnionType, u.ScalarJSON)
		for _, d := range u.Discrim {
			fmt.Fprintf(&unionCases, "{%q, %q},", d.JSON, d.Value)
		}
		unionCases.WriteString("}},\n")
	}
	var parentCases strings.Builder
	for _, p := range parents {
		fmt.Fprintf(&parentCases, "\t{name: %q, typ: reflect.TypeOf(%s{}), childJSON: %q, childKey: %q},\n",
			p.Name, p.Name, p.ChildJSON, p.ChildKey)
	}

	src := lenientScalarsTestTemplate
	src = strings.ReplaceAll(src, "@PKG@", pkgName)
	src = strings.ReplaceAll(src, "@CASES@", cases.String())
	src = strings.ReplaceAll(src, "@UNIONCASES@", unionCases.String())
	src = strings.ReplaceAll(src, "@PARENTCASES@", parentCases.String())
	src = strings.ReplaceAll(src, "@BQ@", "`")

	outPath := filepath.Join(pkgDir, "lenient_scalars_test.go")
	formatted, err := formatGo("lenient_scalars_test.go", []byte(src))
	if err != nil {
		return fmt.Errorf("formatting lenient_scalars_test.go: %w", err)
	}
	if err := writeGenerated(outPath, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}
	log.Printf("wrote %s", outPath)
	return nil
}

// lenientScalarPlan resolves one package's whole lenient-scalar emission from
// the per-root seeds its specs accumulated.
//
// It is one call rather than four so the ordering and the deduplication live
// beside the rules they serve: entries follow the emitted type order whatever
// order the roots were walked in, and a type two roots both reach is emitted
// once. Validation stays per root — see validateLenientScalars.
func lenientScalarPlan(pkgContext string, structTypes []GoType,
	seeds map[string]map[string]bool) ([]lenientType, []lenientUnionCase, []lenientParentCase, error) {
	if len(seeds) == 0 {
		return nil, nil, nil, nil
	}
	perRoot := make(map[string][]lenientType, len(seeds))
	byName := make(map[string]lenientType)
	var diags lenientDiagnostics
	for _, root := range slices.Sorted(maps.Keys(seeds)) {
		entries, d, err := lenientScalarTypes(structTypes, seeds[root])
		if err != nil {
			return nil, nil, nil, err
		}
		perRoot[root] = entries
		diags.SkippedComposites = append(diags.SkippedComposites, d.SkippedComposites...)
		diags.OwnDecoder = append(diags.OwnDecoder, d.OwnDecoder...)
		for _, e := range entries {
			byName[e.Name] = e
		}
	}
	slices.Sort(diags.SkippedComposites)
	diags.SkippedComposites = slices.Compact(diags.SkippedComposites)
	slices.Sort(diags.OwnDecoder)
	diags.OwnDecoder = slices.Compact(diags.OwnDecoder)
	if err := validateLenientScalars(pkgContext, perRoot, diags); err != nil {
		return nil, nil, nil, err
	}
	var entries []lenientType
	for _, t := range structTypes {
		if e, ok := byName[t.Name]; ok {
			entries = append(entries, e)
		}
	}
	return entries, lenientUnionCases(structTypes, entries), lenientParentCases(structTypes, entries), nil
}
