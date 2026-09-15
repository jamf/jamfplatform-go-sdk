// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"log"
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

// goScalarKind classifies a generated field type as the JSON scalar kind a
// string-encoded value would have to be coerced to, or "" when the field is
// not a scalar the coercion applies to.
//
// Composites are deliberately excluded rather than descended into: a field
// whose leaf is a struct is decoded by that struct's own generated method, and
// a slice or map of numbers is a shape no writer has been observed to
// string-encode. Named aliases over an integer base (a numeric enum) count,
// since the wire form is the same integer.
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

// lenientScalarSeed walks the schema graph from each named root and returns the
// Go type names its subtree reaches. Called with the doc after every schema
// pass has run, so hoisted inline objects are present under the names they
// will be emitted with.
//
// A root that names no schema in this spec is an error rather than a skip: the
// key exists to carry a wire fact about a specific store, and a root that has
// been renamed or withdrawn upstream silently drops the tolerance from every
// type under it.
func lenientScalarSeed(doc *openapi3.T, roots []string) (map[string]bool, error) {
	seed := make(map[string]bool)
	if len(roots) == 0 {
		return seed, nil
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
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("lenientScalarRoots names %d schema(s) this spec does not declare: %s\n\n"+
			"fix: rename the entry to the schema that replaced it, or delete it — a root that resolves to "+
			"nothing removes the tolerance from its whole subtree with no other signal",
			len(missing), strings.Join(missing, ", "))
	}
	return seed, nil
}

// lenientScalarTypes closes the seed over the emitted types' own field
// references and returns, in emission order, every struct that both lies in
// the closure and declares at least one number or boolean.
//
// The closure is needed because the seed is derived from schema names while
// the tolerance is emitted against Go types: a field whose type was hoisted
// out of an inline object, or renamed on the way to Go, is reachable only by
// following the emitted field types.
func lenientScalarTypes(types []GoType, seed map[string]bool) []lenientType {
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
		for _, f := range byName[name].Fields {
			ref := normalizeTypeRef(f.Type)
			if _, ok := byName[ref]; ok && !reached[ref] {
				queue = append(queue, ref)
			}
		}
	}

	aliasBase := numericEnumBases(types)
	var out []lenientType
	for _, t := range types {
		if !reached[t.Name] || len(t.Fields) == 0 {
			continue
		}
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
		if len(keys) == 0 {
			continue
		}
		slices.SortFunc(keys, func(a, b lenientKey) int { return strings.Compare(a.JSON, b.JSON) })
		out = append(out, lenientType{Name: t.Name, Keys: keys})
	}
	return out
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

// emitPkgLenientScalars writes lenient_scalars.go: the coercion helper plus one
// UnmarshalJSON per type in entries. A package with no entries gets no file,
// and a spec that asked for roots but produced none is an error — see
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

package %s

import (
	"encoding/json"
	"errors"
)

// jsonScalarKind names the JSON scalar a property is declared as.
type jsonScalarKind uint8

const (
	// jsonScalarNumber: the spec declares an integer or a number.
	jsonScalarNumber jsonScalarKind = iota + 1
	// jsonScalarBool: the spec declares a boolean.
	jsonScalarBool
)

// unmarshalLenientScalars decodes data into v, accepting a JSON string
// wherever keys declares a number or a boolean.
//
// The strict decode is tried first and the rewrite only happens when it fails,
// so a conforming body costs one extra error check and nothing else. A nested
// value is fixed by its own type's method during that first attempt, which is
// why each type only has to describe its own keys.
//
// Three properties worth relying on. Only the listed keys are considered, so a
// string the spec declares as a string is never touched. Only a JSON string is
// rewritten, and only when the text it carries is a valid JSON scalar of the
// declared kind — so "abc" for an integer still fails the decode rather than
// arriving as zero. And marshalling is untouched: the SDK keeps sending the
// numbers and booleans the spec declares.
func unmarshalLenientScalars(data []byte, v any, keys map[string]jsonScalarKind, name string) error {
	err := json.Unmarshal(data, v)
	if err == nil {
		return nil
	}
	fixed, rewritten := unquoteJSONScalars(data, keys)
	if rewritten {
		// The second error is the one to report when it comes: the rewrite
		// has handled the encoding the first error described, so what is left
		// is a fault the coercion has nothing to do with.
		err = json.Unmarshal(fixed, v)
		if err == nil {
			return nil
		}
	}
	return namedStructError(err, name)
}

// namedStructError puts the real type name back into a decode error.
//
// Each generated decoder decodes into a local type named "lenient" to shed the
// method and avoid recursing, and encoding/json reports the *Go* type it was
// decoding — so without this a caller reads "json: cannot unmarshal string
// into Go struct field lenient.Value", which names nothing they can look up.
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
// decimal keeps its exact spelling; json.Valid plus the leading-byte check is
// what decides it is a JSON number token, which rules out the forms Go's own
// parsers accept and JSON does not (Inf, NaN, hex floats, a leading +).
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
`, pkgName)

	for _, e := range entries {
		fmt.Fprintf(&b, "\n// lenientScalars%s names the scalars %s declares, for its UnmarshalJSON.\nvar lenientScalars%s = map[string]jsonScalarKind{\n", e.Name, e.Name, e.Name)
		for _, k := range e.Keys {
			fmt.Fprintf(&b, "\t%q: %s,\n", k.JSON, k.Kind)
		}
		b.WriteString("}\n")
		fmt.Fprintf(&b, `
// UnmarshalJSON decodes s, accepting a JSON string for any of its numbers and
// booleans. See unmarshalLenientScalars for what is and is not coerced.
func (s *%s) UnmarshalJSON(data []byte) error {
	type lenient %s
	var v lenient
	if err := unmarshalLenientScalars(data, &v, lenientScalars%s, %q); err != nil {
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

// validateLenientScalars refuses a spec that asked for tolerance and got none.
//
// A root resolves but reaches no number or boolean when upstream has moved the
// scalars out from under it, and the entry is then a claim about a store that
// no longer needs it. Failing here is what deletes the config entry, the same
// way a redundant scopeTypes override fails.
func validateLenientScalars(pkgContext string, roots []string, entries []lenientType) error {
	if len(roots) == 0 || len(entries) > 0 {
		return nil
	}
	return fmt.Errorf("%s: lenientScalarRoots names %s but its subtree reaches no number or boolean field\n\n"+
		"fix: delete the entry — every scalar it covered has gone, so the tolerance has nothing left to do",
		pkgContext, strings.Join(roots, ", "))
}

// lenientScalarsTestTemplate is the fixed body of lenient_scalars_test.go.
// @BQ@ and @DQ@ stand in for a backtick and a double quote so the whole thing
// can live in one raw string; @PKG@ is the package name and @CASES@ the table
// rows. Written this way rather than through a format string because the
// generated source is dense in quotes of both kinds and a Sprintf verb in the
// middle of them is where the last attempt went wrong.
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
	err := unmarshalLenientScalars([]byte(@BQ@["not","an","object"]@BQ@), &target,
		map[string]jsonScalarKind{"count": jsonScalarNumber}, "probe")
	if err == nil {
		t.Fatal("decoding a JSON array into a struct should fail")
	}
	if !strings.Contains(err.Error(), "array") {
		t.Errorf("error = %v, want the original decode error naming the array", err)
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
	if err := unmarshalLenientScalars([]byte(@BQ@{"count":"9007199254740993"}@BQ@), &target,
		map[string]jsonScalarKind{"count": jsonScalarNumber}, "probe"); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if target.Count != want {
		t.Errorf("Count = %d, want %d", target.Count, want)
	}
}

// TestLenientScalars_JSONScalarLiteralRejectsNonJSONNumbers pins the forms Go's
// own parsers accept and JSON does not. Every one of these would decode
// through strconv and none of them is a JSON number token.
func TestLenientScalars_JSONScalarLiteralRejectsNonJSONNumbers(t *testing.T) {
	for _, s := range []string{"", " ", "+1", "1_0", "0x10", "1e", "Inf", "NaN", "01", ".5", "1.2.3"} {
		if lit, ok := jsonScalarLiteral(s, jsonScalarNumber); ok {
			t.Errorf("jsonScalarLiteral(%q) = %q, true; want rejected", s, lit)
		}
	}
	for _, s := range []string{"0", "-1", "1.5", "1e5", "-1.5e-3", "90"} {
		if lit, ok := jsonScalarLiteral(s, jsonScalarNumber); !ok || lit != s {
			t.Errorf("jsonScalarLiteral(%q) = %q, %v; want %q, true", s, lit, ok, s)
		}
	}
	for _, s := range []string{"True", "TRUE", "1", "", "yes"} {
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
`

// emitPkgLenientScalarsTest writes lenient_scalars_test.go: a table over every
// type that got a tolerant decoder, asserting the string encoding decodes to
// the same value the bare scalar does, and that a string carrying something
// that is not a scalar of the declared kind still fails.
//
// The table is reflective rather than one hand-shaped case per type because
// what is being pinned is uniform: 43 near-identical literal fixtures would go
// stale the first time a spec moved a field, and equality against the
// bare-scalar decode is a stronger assertion than any single expected value.
func emitPkgLenientScalarsTest(pkgDir, pkgName string, entries []lenientType) error {
	if len(entries) == 0 {
		return nil
	}
	var cases strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&cases, "\t{name: %q, typ: reflect.TypeOf(%s{}), keys: lenientScalars%s},\n", e.Name, e.Name, e.Name)
	}
	src := lenientScalarsTestTemplate
	src = strings.ReplaceAll(src, "@PKG@", pkgName)
	src = strings.ReplaceAll(src, "@CASES@", cases.String())
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
