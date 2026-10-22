// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"text/template"
)

// ---------------------------------------------------------------------------
// Templates — parsed once at init
// ---------------------------------------------------------------------------

// testQueryArgs renders a generated unit test's trailing query-param arguments.
// Zero values throughout, except that with satisfyRequired a parameter the
// method refuses to send without (OperationDef.RequiredParams) gets a non-zero
// one, so the happy-path test reaches the wire.
func testQueryArgs(m GoMethod, satisfyRequired bool) string {
	if len(m.QueryParams) == 0 {
		return ""
	}
	required := make(map[string]bool, len(m.RequiredParams))
	for _, qp := range m.RequiredParams {
		required[qp.Spec] = satisfyRequired
	}
	args := make([]string, len(m.QueryParams))
	for i, qp := range m.QueryParams {
		set := required[qp.Spec]
		switch qp.Type {
		case "[]string":
			args[i] = "nil"
			if set {
				args[i] = `[]string{"test-value"}`
			}
		case "bool":
			args[i] = fmt.Sprint(set)
		case "int", "int64":
			args[i] = "0"
			if set {
				args[i] = "1"
			}
		default:
			args[i] = `""`
			if set {
				args[i] = `"test-value"`
			}
		}
	}
	return ", " + strings.Join(args, ", ")
}

var funcMap = template.FuncMap{
	"httpConst":   httpConstName,
	"statusConst": statusConstName,
	"fmtPath": func(m GoMethod) string {
		if len(m.PathParams) == 0 {
			return fmt.Sprintf(`prefix + "%s"`, m.ResourcePath)
		}
		fmtStr := pathParamRe.ReplaceAllString(m.ResourcePath, "%s")
		args := []string{"prefix"}
		for _, p := range m.PathParams {
			args = append(args, "url.PathEscape("+p.GoName+")")
		}
		return fmt.Sprintf(`fmt.Sprintf("%%s%s", %s)`, fmtStr, strings.Join(args, ", "))
	},
	"errWrap": func(m GoMethod) string {
		if len(m.PathParams) == 0 {
			return fmt.Sprintf(`"%s: %%w", err`, m.Name)
		}
		return fmt.Sprintf(`"%s(%%s): %%w", %s, err`, m.Name, m.PathParams[0].GoName)
	},
	"testPath": func(m GoMethod) string {
		path := pathParamRe.ReplaceAllString(m.ResourcePath, "test-id")
		return testAPIBase(m.Namespace, m.Version) + path
	},
	"testCallArgs": func(m GoMethod) string {
		if len(m.PathParams) == 0 {
			return ""
		}
		args := make([]string, len(m.PathParams))
		for i := range m.PathParams {
			args[i] = `"test-id"`
		}
		return ", " + strings.Join(args, ", ")
	},
	"testExtraArgs": func(m GoMethod) string { return testQueryArgs(m, true) },
	// testZeroExtraArgs is testExtraArgs with every parameter at its zero value,
	// required ones included — the call a requiredParams method must refuse.
	"testZeroExtraArgs": func(m GoMethod) string { return testQueryArgs(m, false) },
	"isStringSlice": func(s string) bool { return s == "[]string" },
	// sliceElem is the element type of an "[]T" config value, for the
	// generic call in the unwrap template. UnwrapResults is parameterised on
	// the element, while unwrapResults is declared as the slice the method
	// returns.
	"sliceElem": func(s string) string { return strings.TrimPrefix(s, "[]") },
	// queryValue is the expression that converts one query parameter's Go
	// argument into the string sent on the wire; queryGuard is the zero-value
	// condition deciding whether an optional one is sent at all.
	//
	// They are functions rather than a type switch inside the templates
	// because that switch was written out three times — in
	// "buildQueryParams", "paginated" and "paginatedCursor" — and every
	// branch of all three had to honour required-ness. Three copies of a
	// nine-branch switch is how a required param came to be emitted behind an
	// optional param's guard in the first place; one copy is one place to be
	// wrong.
	"queryValue": func(qp ExtraParam) string {
		switch qp.Type {
		case "[]string":
			return "strings.Join(" + qp.Go + `, ",")`
		case "bool":
			// An optional bool is only ever emitted when true, so the literal
			// is exact. A required one has to carry a false the caller meant:
			// omitting it is what this whole path exists to stop, and sending
			// "true" for a false argument would be worse still.
			if qp.AlwaysSend {
				return "strconv.FormatBool(" + qp.Go + ")"
			}
			return `"true"`
		case "int":
			return "strconv.Itoa(" + qp.Go + ")"
		case "int64":
			return "strconv.FormatInt(" + qp.Go + ", 10)"
		}
		return qp.Go
	},
	// queryMissing is the negation of queryGuard, written out rather than
	// wrapped in `!(...)`, for the requiredParams pre-flight check.
	"queryMissing": func(qp ExtraParam) string {
		switch qp.Type {
		case "[]string":
			return "len(" + qp.Go + ") == 0"
		case "bool":
			return "!" + qp.Go
		case "int", "int64":
			return qp.Go + " == 0"
		}
		return qp.Go + ` == ""`
	},
	"queryGuard": func(qp ExtraParam) string {
		switch qp.Type {
		case "[]string":
			return "len(" + qp.Go + ") > 0"
		case "bool":
			return qp.Go
		case "int", "int64":
			return qp.Go + " != 0"
		}
		return qp.Go + ` != ""`
	},
	// requiredQueryAsserts emits, for each spec-required query param, a check
	// that the generated method actually put it on the wire. The unit-test
	// stubs call every method with zero-value arguments, so a required param
	// emitted behind an optional param's zero-value guard is *absent* from
	// the request and these fail — which makes each of them a standing
	// regression test for the bug class, not just for the param it names.
	// url.Values.Has is true for a present-but-empty key, which is exactly
	// the distinction that matters here.
	"requiredQueryAsserts": func(m GoMethod) string {
		var b strings.Builder
		for _, qp := range m.QueryParams {
			if !qp.AlwaysSend {
				continue
			}
			fmt.Fprintf(&b, "\n\t\tif !r.URL.Query().Has(%q) {\n\t\t\tt.Errorf(\"required query param %s not sent: %%q\", r.URL.RawQuery)\n\t\t}", qp.Spec, qp.Spec)
		}
		return b.String()
	},
	// transportCall renders the whole c.transport.X(...) invocation for the
	// five method shapes that reach the transport directly.
	//
	// It is one Go function rather than a conditional in each of those five
	// templates because the entry point is chosen from three independent
	// dimensions — Content-Type, retry opt-out, header params — and a
	// per-template conditional had already been written out twice before
	// headers were a third. The body and result expressions are derived from
	// Category and RequestType rather than passed in, so a template cannot
	// pair a create's `request` with a get's `nil` result.
	//
	// Header-carrying methods route through DoWithOptions because it is the
	// only entry point that can carry headers; everything else keeps the
	// named shorthand it already used, which is what holds the regenerated
	// diff to the operations that actually gained a header.
	"transportCall": transportCall,
	// headerName renders a header's wire name in the canonical form Go's
	// http.Header uses. Set and Get canonicalise either way, so this is for
	// the reader: the spec spells one of these "accept", and generated code
	// saying Set("accept", …) invites a reader to think the case is
	// load-bearing.
	"headerName": http.CanonicalHeaderKey,
	// testHeaderArgs supplies each header param a non-zero sentinel in the
	// stub call, and headerAsserts asserts the request carried it.
	//
	// The pair is what makes the mechanism testable at all. Stubs call every
	// method with zero-value arguments, and an optional header is emitted
	// behind a `!= ""` guard — so with "" the header is legitimately absent
	// and a template that forgot to wire it up passes identically to one that
	// did. Sending a sentinel turns that into a failure.
	"testHeaderArgs": func(m GoMethod) string {
		if len(m.HeaderParams) == 0 {
			return ""
		}
		args := make([]string, len(m.HeaderParams))
		for i, hp := range m.HeaderParams {
			args[i] = fmt.Sprintf("%q", testHeaderValue(hp.Spec))
		}
		return ", " + strings.Join(args, ", ")
	},
	"headerAsserts": func(m GoMethod) string {
		var b strings.Builder
		for _, hp := range m.HeaderParams {
			// Canonical, for the same reason the method's Set is: Get
			// canonicalises either way, so a stub asserting Get("accept")
			// would reintroduce exactly the spelling headerName exists to
			// keep out of generated code.
			name := http.CanonicalHeaderKey(hp.Spec)
			fmt.Fprintf(&b, "\n\t\tif got := r.Header.Get(%q); got != %q {\n\t\t\tt.Errorf(\"header %s = %%q, want %%q\", got, %q)\n\t\t}",
				name, testHeaderValue(hp.Spec), name, testHeaderValue(hp.Spec))
		}
		return b.String()
	},
	"requestArg": func(t string) string {
		// Test stub's zero-value literal for the request parameter.
		// Primitives can't be composite-literal'd (e.g. `&string{}`
		// is invalid), so use new(T) which yields *T zero-pointer.
		switch t {
		case "string", "bool", "int", "int32", "int64", "float32", "float64":
			return "new(" + t + ")"
		case "[]byte":
			return "[]byte{}"
		}
		return "&" + t + "{}"
	},
	// resolverNameStub converts a dot-path nameField to a Go map literal entry
	// for use in test stubs. "name" → `"name": "target"`;
	// "general.name" → `"general": map[string]any{"name": "target"}`.
	"resolverNameStub": func(nameField string) string {
		parts := strings.Split(nameField, ".")
		if len(parts) == 1 {
			return fmt.Sprintf(`%q: "target"`, parts[0])
		}
		inner := fmt.Sprintf(`%q: "target"`, parts[len(parts)-1])
		for i := len(parts) - 2; i >= 0; i-- {
			inner = fmt.Sprintf(`%q: map[string]any{%s}`, parts[i], inner)
		}
		return inner
	},
	// resolverIDStub returns the test stub value for the ID field.
	// Numeric IDs emit 42 (unquoted); string IDs emit "resolved-id".
	"resolverIDStub": func(r *GoResolver) string {
		if r.IDNumeric {
			return fmt.Sprintf(`%q: 42`, r.IDField)
		}
		return fmt.Sprintf(`%q: "resolved-id"`, r.IDField)
	},
	// resolverExpectedID returns the expected ID string for test assertions.
	"resolverExpectedID": func(r *GoResolver) string {
		if r.IDNumeric {
			return "42"
		}
		return "resolved-id"
	},
	"testMultipartArgs": func(m GoMethod) string {
		if len(m.MultipartFields) == 0 {
			return ""
		}
		args := make([]string, 0, len(m.MultipartFields)*2)
		for _, f := range m.MultipartFields {
			if f.IsFile {
				args = append(args, `"test.bin"`, `bytes.NewBufferString("stub")`)
			} else {
				args = append(args, `""`)
			}
		}
		return ", " + strings.Join(args, ", ")
	},
	// applyResultsKey is the envelope key the apply method's resolver reads the
	// element array from. Defaults to "results"; a resolver declaring
	// resultsField (e.g. securitycloud device groups v2's {groups: []}) needs
	// its stubs to answer under that key or the generated _Update test takes
	// the create branch instead of the update one.
	"applyResultsKey": func(a *GoApply) string {
		if a.ListResultsField == "" {
			return "results"
		}
		return a.ListResultsField
	},
	// applyListPath builds the test-server handler path for the list endpoint
	// used by the apply method's resolver call.
	"applyListPath": func(a *GoApply) string {
		return testAPIBase(a.ListNamespace, a.ListVersion) + a.ListPath
	},
	// applyCreatePath builds the test-server handler path for the create endpoint.
	"applyCreatePath": func(a *GoApply) string {
		replacement := "test-id"
		if a.ClassicCreate {
			replacement = "0"
		}
		path := pathParamRe.ReplaceAllString(a.CreatePath, replacement)
		return testAPIBase(a.CreateNS, a.CreateVer) + path
	},
	// applyUpdatePath builds the test-server handler path for the update endpoint.
	"applyUpdatePath": func(a *GoApply) string {
		replacement := "existing-id"
		if a.ClassicCreate {
			replacement = "42"
		}
		path := pathParamRe.ReplaceAllString(a.UpdatePath, replacement)
		return testAPIBase(a.UpdateNS, a.UpdateVer) + path
	},
	// applyGetPath builds the test-server handler path for the get endpoint
	// used by versionLock-enabled apply methods.
	"applyGetPath": func(a *GoApply) string {
		path := pathParamRe.ReplaceAllString(a.GetPath, "existing-id")
		return testAPIBase(a.GetNS, a.GetVer) + path
	},
	// applyTokenUploadPath builds the test-server handler path for the token
	// upload endpoint used to create resources in tokenUploadMode.
	"applyTokenUploadPath": func(a *GoApply) string {
		path := pathParamRe.ReplaceAllString(a.TokenUploadPath, "test-id")
		return testAPIBase(a.TokenUploadNS, a.TokenUploadVer) + path
	},
	// applyTokenReplacePath builds the test-server handler path for the token
	// replace endpoint used to re-upload tokens on update in tokenUploadMode.
	"applyTokenReplacePath": func(a *GoApply) string {
		path := pathParamRe.ReplaceAllString(a.TokenReplacePath, "existing-id")
		return testAPIBase(a.TokenReplaceNS, a.TokenReplaceVer) + path
	},
	// applyMembershipFetchPath builds the test-server handler path for the
	// membership list endpoint used in membershipPreFetch apply mode.
	"applyMembershipFetchPath": func(a *GoApply) string {
		path := pathParamRe.ReplaceAllString(a.MembershipFetchPath, "existing-id")
		return testAPIBase(a.MembershipFetchNS, a.MembershipFetchVer) + path
	},
	// applyTokenCreateUpdatePath builds the test-server handler path for the
	// metadata update endpoint in the token-upload create test. Uses "new-id"
	// as the path parameter since the upload response returns that ID.
	"applyTokenCreateUpdatePath": func(a *GoApply) string {
		path := pathParamRe.ReplaceAllString(a.UpdatePath, "new-id")
		return testAPIBase(a.UpdateNS, a.UpdateVer) + path
	},
	// applyCreateStatus returns the HTTP status code for test create responses.
	"applyCreateStatus": func(a *GoApply) int {
		if a.CreateStatus != 0 {
			return a.CreateStatus
		}
		return 201
	},
	// applyUpdateStatus returns the HTTP status code for test update responses.
	"applyUpdateStatus": func(a *GoApply) int {
		if a.UpdateStatus != 0 {
			return a.UpdateStatus
		}
		return 200
	},
	// applyRequestExpr returns a Go expression for the request struct literal
	// with the name field populated to "target". Handles both pointer and
	// non-pointer name fields, and nested fields (e.g. General.Name).
	"applyRequestExpr": func(a *GoApply) string {
		if a.NameNested {
			// e.g. &Policy{General: &PolicyGeneral{Name: ptrStr("target")}}
			return fmt.Sprintf("&%s{%s: &%s{%s: ptrStr(\"target\")}}", a.RequestType, a.NameParentField, a.NameParentType, a.NameLeafField)
		}
		if a.NameIsPointer {
			return fmt.Sprintf("&%s{%s: ptrStr(\"target\")}", a.RequestType, a.NameGoField)
		}
		return fmt.Sprintf("&%s{%s: \"target\"}", a.RequestType, a.NameGoField)
	},
	// applyTestCreateIDJSON returns the JSON value for the mock create response ID.
	"applyTestCreateIDJSON": func(a *GoApply) string {
		if a.IDNumeric {
			return "42"
		}
		return `"new-id"`
	},
	// applyTestCreateIDExpected returns the expected string ID after create.
	"applyTestCreateIDExpected": func(a *GoApply) string {
		if a.IDNumeric {
			return "42"
		}
		return "new-id"
	},
}

var sourceTmpl = template.Must(template.New("source").Funcs(funcMap).Parse(sourceTemplate))
var testTmpl = template.Must(template.New("tests").Delims("<%", "%>").Funcs(funcMap).Parse(testTemplate))

// ---------------------------------------------------------------------------
// Source template
// ---------------------------------------------------------------------------

var sourceTemplate = `// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package {{ .Package }}

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"{{ .Module }}/internal/client"
)

{{- if .Types }}
{{ range .Types }}
{{- if .AliasTarget }}
// {{ .Comment }}
type {{ .Name }} = {{ .AliasTarget }}
{{- else if .IsRawJSON }}
// {{ .Comment }}
type {{ .Name }} = json.RawMessage
{{- else if .Discriminator }}
// {{ .Comment }}
type {{ .Name }} struct {
{{- if .Discriminator.EnumTypeName }}
	// Allowed values: see the {{ .Discriminator.EnumTypeName }} constants.
{{- end }}
	{{ .Discriminator.GoFieldName }} string ` + "`" + `json:"{{ .Discriminator.PropertyName }}"` + "`" + `
{{- range .Discriminator.Variants }}
	{{ .FieldName }} *{{ .TypeName }} ` + "`" + `json:"-"` + "`" + `
{{- end }}
}

// UnmarshalJSON dispatches the payload to the variant matching the
// {{ .Discriminator.PropertyName }} discriminator. Unknown values leave the variant
// pointers nil but preserve the discriminator string.
func (m *{{ .Name }}) UnmarshalJSON(data []byte) error {
	var d struct {
		{{ .Discriminator.GoFieldName }} string ` + "`" + `json:"{{ .Discriminator.PropertyName }}"` + "`" + `
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	m.{{ .Discriminator.GoFieldName }} = d.{{ .Discriminator.GoFieldName }}
	switch d.{{ .Discriminator.GoFieldName }} {
{{- range .Discriminator.Variants }}
	case {{ range $i, $v := .Values }}{{ if $i }}, {{ end }}"{{ $v }}"{{ end }}:
		m.{{ .FieldName }} = new({{ .TypeName }})
		return json.Unmarshal(data, m.{{ .FieldName }})
{{- end }}
	}
	return nil
}

// MarshalJSON emits the active variant's JSON. If the matching variant
// pointer is nil, emits a minimal object carrying only the discriminator.
func (m {{ .Name }}) MarshalJSON() ([]byte, error) {
	switch m.{{ .Discriminator.GoFieldName }} {
{{- range .Discriminator.Variants }}
	case {{ range $i, $v := .Values }}{{ if $i }}, {{ end }}"{{ $v }}"{{ end }}:
		if m.{{ .FieldName }} != nil {
			return json.Marshal(m.{{ .FieldName }})
		}
{{- end }}
	}
	return json.Marshal(map[string]string{"{{ .Discriminator.PropertyName }}": m.{{ .Discriminator.GoFieldName }}})
}
{{- else if .Union }}
// {{ .Comment }}
type {{ .Name }} struct {
{{- range .Union.Variants }}
	{{ .FieldName }} *{{ .TypeName }} ` + "`" + `json:"-"` + "`" + `
{{- end }}
	// Raw holds the payload exactly as it arrived. Decoding cannot choose a
	// variant — the spec declares no discriminator, so nothing inside the
	// payload names one — so UnmarshalJSON stores the bytes here and leaves
	// every variant pointer nil. Decode it into the variant whichever sibling
	// field selects.
	Raw json.RawMessage ` + "`" + `json:"-"` + "`" + `
}

// MarshalJSON emits the one variant that is set.
//
// More than one is an error rather than an arbitrary pick between them: the
// wire has room for exactly one, and silently dropping the rest is how a
// caller ships a body it did not write. With none set it emits Raw, or JSON
// null when Raw is empty too — the zero value has to stay marshalable, since
// it is what the enclosing request carries until the caller fills it in, and
// null is what the server rejects with a message naming the field.
func (u {{ .Name }}) MarshalJSON() ([]byte, error) {
	var set []string
	var payload any
{{- range .Union.Variants }}
	if u.{{ .FieldName }} != nil {
		set = append(set, "{{ .FieldName }}")
		payload = u.{{ .FieldName }}
	}
{{- end }}
	switch {
	case len(set) > 1:
		return nil, fmt.Errorf("{{ .Name }}: %d variants set (%s); the payload carries exactly one", len(set), strings.Join(set, ", "))
	case len(set) == 1:
		return json.Marshal(payload)
	case len(u.Raw) > 0:
		return u.Raw, nil
	}
	return []byte("null"), nil
}

// UnmarshalJSON preserves the payload in Raw. See that field's comment for
// why it does not populate a variant.
func (u *{{ .Name }}) UnmarshalJSON(data []byte) error {
	u.Raw = append(u.Raw[:0], data...)
	return nil
}
{{- else if or .Fields (and (eq $.Format "xml") .XMLName) }}
// {{ .Comment }}
type {{ .Name }} struct {
{{- if and (eq $.Format "xml") .XMLName }}
	XMLName xml.Name
{{- end }}
{{- range .Fields }}
{{- if .Comment }}
	// {{ .Comment }}
{{- end }}
{{- if eq $.Format "xml" }}
	{{ .Name }} {{ .Type }} ` + "`" + `xml:"{{ .JSONTag }}"` + "`" + `
{{- else }}
	{{ .Name }} {{ .Type }} ` + "`" + `json:"{{ .JSONTag }}"` + "`" + `
{{- end }}
{{- end }}
}

{{- if and (eq $.Format "xml") .XMLName }}

// MarshalXML forces the {{ .Name }} root element name to the wire value
// declared by the spec (<{{ .XMLName }}>) regardless of what XMLName.Local
// holds. Classic resources are frequently decoded from polymorphic wire
// roots (<static_user_group>, <smart_user_group>, <user_group>, etc.) —
// stashing the incoming root name in XMLName is useful context but must
// not leak back into writes. The shadow type suppresses re-entry into
// this method during encoding.
func (t {{ .Name }}) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	start.Name = xml.Name{Local: "{{ .XMLName }}"}
	type shadow {{ .Name }}
	return e.EncodeElement(shadow(t), start)
}
{{- end }}
{{- else }}
{{- $base := or .EnumBaseType "string" }}
// {{ .Comment }}
type {{ .Name }} = {{ $base }}
{{- if .EnumValues }}
{{- $enumType := .Name }}

// {{ .Name }} values accepted by the Jamf API. The alias above is {{ if eq $base "string" }}a string{{ else }}an {{ $base }}{{ end }}, so
// these constants pass to any parameter or field declared as a plain {{ $base }}.
const (
{{- range .EnumValues }}
	{{ .Name }} {{ $enumType }} = {{ .Literal }}
{{- end }}
)

// {{ .Name }}Values returns every value the Jamf API accepts for {{ .Name }},
// in the order the spec declares them. Returns a fresh slice per call, so no
// caller can corrupt the set for the rest of the process — which a package
// level var would allow. Suits attribute validation (Terraform's
// {{ if eq $base "string" }}stringvalidator.OneOf{{ else }}int64validator.OneOf{{ end }}, say) and anything that needs to enumerate the set
// rather than name one member.
func {{ .Name }}Values() []{{ $enumType }} {
	return []{{ $enumType }}{
{{- range .EnumValues }}
		{{ .Name }},
{{- end }}
	}
}
{{- end }}
{{- end }}
{{ end }}
{{- end }}
{{ range .Methods }}
{{- if eq .Category "paginated" }}
{{ template "paginated" . }}
{{- else if eq .Category "paginatedCursor" }}
{{ template "paginatedCursor" . }}
{{- else if eq .Category "unwrap" }}
{{ template "unwrap" . }}
{{- else if eq .Category "get" }}
{{ template "get" . }}
{{- else if eq .Category "create" }}
{{ template "create" . }}
{{- else if eq .Category "actionWithResponse" }}
{{ template "actionWithResponse" . }}
{{- else if eq .Category "update" }}
{{ template "update" . }}
{{- else if eq .Category "multipart" }}
{{ template "multipart" . }}
{{- else if eq .Category "raw" }}
{{ template "raw" . }}
{{- else if eq .Category "resolverID" }}
{{ template "resolverID" . }}
{{- else if eq .Category "resolverTyped" }}
{{ template "resolverTyped" . }}
{{- else if eq .Category "resolverIDDirect" }}
{{ template "resolverIDDirect" . }}
{{- else if eq .Category "resolverTypedDirect" }}
{{ template "resolverTypedDirect" . }}
{{- else if eq .Category "apply" }}
{{ template "apply" . }}
{{- else }}
{{ template "action" . }}
{{- end }}
{{- end }}

{{/* ---- Shared sub-templates ---- */}}

{{/* AlwaysSend (a spec-required param declaring no default — see
     resolveQueryParams) is set unconditionally: dropping it because the caller
     passed the zero value produces a request the server rejects with a 400
     that reads like a fault rather than a caller error. Everything else keeps
     the zero-value guard — omitting an unset optional param is correct and
     load-bearing. */}}
{{- define "buildQueryParams" -}}
{{- if .QueryParams }}
	params := url.Values{}
{{- range .QueryParams }}
{{- if .AlwaysSend }}
	params.Set("{{ .Spec }}", {{ queryValue . }})
{{- else }}
	if {{ queryGuard . }} {
		params.Set("{{ .Spec }}", {{ queryValue . }})
	}
{{- end }}
{{- end }}
	if encoded := params.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
{{- end }}
{{- end }}

{{- /*
buildHeaderParams stamps the operation's header params onto an http.Header the
transport applies to the request. Optional ones keep a zero-value guard — an
absent header and an empty one are different requests, and for If-Match the
difference is "update unconditionally" versus a malformed precondition. A
spec-required header travels unguarded, for the same reason a required query
param does: the caller gets the server's own complaint rather than a request
that silently omitted it.
*/ -}}
{{- define "buildHeaderParams" -}}
{{- if .HeaderParams }}
	headers := http.Header{}
{{- range .HeaderParams }}
{{- if .AlwaysSend }}
	headers.Set("{{ headerName .Spec }}", {{ .Go }})
{{- else }}
	if {{ .Go }} != "" {
		headers.Set("{{ headerName .Spec }}", {{ .Go }})
	}
{{- end }}
{{- end }}
{{- end }}
{{- end }}

{{- define "get" }}
// {{ .Comment }}
{{- if .ReturnsSlice }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}{{ range .QueryParams }}, {{ .Go }} {{ .Type }}{{ end }}{{ range .HeaderParams }}, {{ .Go }} {{ .Type }}{{ end }}) ({{ .ResponseType }}, error) {
{{- else }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}{{ range .QueryParams }}, {{ .Go }} {{ .Type }}{{ end }}{{ range .HeaderParams }}, {{ .Go }} {{ .Type }}{{ end }}) (*{{ .ResponseType }}, error) {
{{- end }}
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	var result {{ .ResponseType }}
	endpoint := {{ fmtPath . }}
{{- template "buildQueryParams" . }}
{{- template "buildHeaderParams" . }}
	if err := {{ transportCall . }}; err != nil {
		return nil, fmt.Errorf({{ errWrap . }})
	}
{{- if .ReturnsSlice }}
	return result, nil
{{- else }}
	return &result, nil
{{- end }}
}
{{ end }}

{{- define "create" }}
// {{ .Comment }}
{{- if .ReturnsSlice }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }},{{- if eq .RequestType "[]byte" }} body []byte{{- else }} request *{{ .RequestType }}{{- end }}{{ range .QueryParams }}, {{ .Go }} {{ .Type }}{{ end }}{{ range .HeaderParams }}, {{ .Go }} {{ .Type }}{{ end }}) ({{ .ResponseType }}, error) {
{{- else }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }},{{- if eq .RequestType "[]byte" }} body []byte{{- else }} request *{{ .RequestType }}{{- end }}{{ range .QueryParams }}, {{ .Go }} {{ .Type }}{{ end }}{{ range .HeaderParams }}, {{ .Go }} {{ .Type }}{{ end }}) (*{{ .ResponseType }}, error) {
{{- end }}
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	var result {{ .ResponseType }}
	endpoint := {{ fmtPath . }}
{{- template "buildQueryParams" . }}
{{- template "buildHeaderParams" . }}
	if err := {{ transportCall . }}; err != nil {
		return nil, fmt.Errorf({{ errWrap . }})
	}
{{- if .ReturnsSlice }}
	return result, nil
{{- else }}
	return &result, nil
{{- end }}
}
{{ end }}

{{- define "actionWithResponse" }}
// {{ .Comment }}
{{- if .ReturnsSlice }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}{{ range .QueryParams }}, {{ .Go }} {{ .Type }}{{ end }}{{ range .HeaderParams }}, {{ .Go }} {{ .Type }}{{ end }}) ({{ .ResponseType }}, error) {
{{- else }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}{{ range .QueryParams }}, {{ .Go }} {{ .Type }}{{ end }}{{ range .HeaderParams }}, {{ .Go }} {{ .Type }}{{ end }}) (*{{ .ResponseType }}, error) {
{{- end }}
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	var result {{ .ResponseType }}
	endpoint := {{ fmtPath . }}
{{- template "buildQueryParams" . }}
{{- template "buildHeaderParams" . }}
	if err := {{ transportCall . }}; err != nil {
		return nil, fmt.Errorf({{ errWrap . }})
	}
{{- if .ReturnsSlice }}
	return result, nil
{{- else }}
	return &result, nil
{{- end }}
}
{{ end }}

{{- define "update" }}
// {{ .Comment }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }},{{- if eq .RequestType "[]byte" }} body []byte{{- else }} request *{{ .RequestType }}{{- end }}{{ range .QueryParams }}, {{ .Go }} {{ .Type }}{{ end }}{{ range .HeaderParams }}, {{ .Go }} {{ .Type }}{{ end }}) error {
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	endpoint := {{ fmtPath . }}
{{- template "buildQueryParams" . }}
{{- template "buildHeaderParams" . }}
	if err := {{ transportCall . }}; err != nil {
		return fmt.Errorf({{ errWrap . }})
	}
	return nil
}
{{ end }}

{{- define "action" }}
// {{ .Comment }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}{{ range .QueryParams }}, {{ .Go }} {{ .Type }}{{ end }}{{ range .HeaderParams }}, {{ .Go }} {{ .Type }}{{ end }}) error {
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	endpoint := {{ fmtPath . }}
{{- template "buildQueryParams" . }}
{{- template "buildHeaderParams" . }}
	if err := {{ transportCall . }}; err != nil {
		return fmt.Errorf({{ errWrap . }})
	}
	return nil
}
{{ end }}

{{- define "multipart" }}
// {{ .Comment }}
//
// For file parts, pass an *os.File or *bytes.Reader (anything that
// implements io.Seeker) so the SDK can rewind the file and retry on a
// 429/Retry-After. A plain io.Reader is accepted too but is not retried.
// Whatever the reader, the upload is sent over HTTP/1.1 with chunked
// transfer encoding and no declared Content-Length.
{{- if .ResponseType }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}{{ range .MultipartFields }}{{ if .IsFile }}, {{ .GoName }}Filename string, {{ .GoName }} io.Reader{{ else }}, {{ .GoName }} {{ .Type }}{{ end }}{{ end }}) (*{{ .ResponseType }}, error) {
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	var result {{ .ResponseType }}
	endpoint := {{ fmtPath . }}
	parts := []client.MultipartField{
{{- range .MultipartFields }}
{{- if .IsFile }}
		{Name: "{{ .Name }}", Filename: {{ .GoName }}Filename, Content: {{ .GoName }}},
{{- else }}
		{Name: "{{ .Name }}", Value: {{ .GoName }}},
{{- end }}
{{- end }}
	}
	if err := c.transport.DoMultipart(ctx, {{ httpConst .HTTPMethod }}, endpoint, parts, {{ statusConst .ExpectedStatus }}, &result); err != nil {
		return nil, fmt.Errorf({{ errWrap . }})
	}
	return &result, nil
}
{{- else }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}{{ range .MultipartFields }}{{ if .IsFile }}, {{ .GoName }}Filename string, {{ .GoName }} io.Reader{{ else }}, {{ .GoName }} {{ .Type }}{{ end }}{{ end }}) error {
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	endpoint := {{ fmtPath . }}
	parts := []client.MultipartField{
{{- range .MultipartFields }}
{{- if .IsFile }}
		{Name: "{{ .Name }}", Filename: {{ .GoName }}Filename, Content: {{ .GoName }}},
{{- else }}
		{Name: "{{ .Name }}", Value: {{ .GoName }}},
{{- end }}
{{- end }}
	}
	if err := c.transport.DoMultipart(ctx, {{ httpConst .HTTPMethod }}, endpoint, parts, {{ statusConst .ExpectedStatus }}, nil); err != nil {
		return fmt.Errorf({{ errWrap . }})
	}
	return nil
}
{{- end }}
{{ end }}

{{- define "raw" }}
// {{ .Comment }}
{{- if and .RequestType .ResponseType }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}, body []byte) ([]byte, error) {
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	var result []byte
	endpoint := {{ fmtPath . }}
	if err := c.transport.DoExpect(ctx, {{ httpConst .HTTPMethod }}, endpoint, body, {{ statusConst .ExpectedStatus }}, &result); err != nil {
		return nil, fmt.Errorf({{ errWrap . }})
	}
	return result, nil
}
{{- else if .RequestType }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}, body []byte) error {
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	endpoint := {{ fmtPath . }}
	if err := c.transport.DoExpect(ctx, {{ httpConst .HTTPMethod }}, endpoint, body, {{ statusConst .ExpectedStatus }}, nil); err != nil {
		return fmt.Errorf({{ errWrap . }})
	}
	return nil
}
{{- else if .ResponseType }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}) ([]byte, error) {
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	var result []byte
	endpoint := {{ fmtPath . }}
	if err := c.transport.DoExpect(ctx, {{ httpConst .HTTPMethod }}, endpoint, nil, {{ statusConst .ExpectedStatus }}, &result); err != nil {
		return nil, fmt.Errorf({{ errWrap . }})
	}
	return result, nil
}
{{- else }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}) error {
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	endpoint := {{ fmtPath . }}
	if err := c.transport.DoExpect(ctx, {{ httpConst .HTTPMethod }}, endpoint, nil, {{ statusConst .ExpectedStatus }}, nil); err != nil {
		return fmt.Errorf({{ errWrap . }})
	}
	return nil
}
{{- end }}
{{ end }}

{{/* An unpaginated list endpoint may answer with the {totalCount, results}
     envelope its spec declares or with a bare JSON array, and the same
     operation has served both: the account lists flipped from array to
     envelope on 2026-09-01 and broke every caller. So the shape is decided
     from the body rather than assumed here — see client.UnwrapResults. */}}
{{- define "unwrap" }}
// {{ .Comment }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}{{ range .QueryParams }}, {{ .Go }} {{ .Type }}{{ end }}) ({{ .UnwrapResults }}, error) {
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	endpoint := {{ fmtPath . }}
{{- template "buildQueryParams" . }}

	results, err := client.UnwrapResults[{{ sliceElem .UnwrapResults }}](ctx, c.transport, {{ httpConst .HTTPMethod }}, endpoint, "{{ .ResultsField }}")
	if err != nil {
		return nil, fmt.Errorf({{ errWrap . }})
	}
	return results, nil
}
{{ end }}

{{- define "paginated" }}
// {{ .Comment }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}{{ range .QueryParams }}, {{ .Go }} {{ .Type }}{{ end }}) ([]{{ .ItemType }}, error) {
{{- range .RequiredParams }}
	if {{ queryMissing . }} {
		return nil, fmt.Errorf("{{ $.Name }}: {{ .Go }} is required: the server answers 400 without it")
	}
{{- end }}
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	return client.ListAllPages(ctx, {{ .MaxPageSize }}, func(ctx context.Context, page, pageSize int) ([]{{ .ItemType }}, bool, error) {
		params := url.Values{}
		params.Set("page", strconv.Itoa(page))
		params.Set("{{ .PageSizeParam }}", strconv.Itoa(pageSize))
{{- range .QueryParams }}
{{- if .AlwaysSend }}
		params.Set("{{ .Spec }}", {{ queryValue . }})
{{- else }}
		if {{ queryGuard . }} {
			params.Set("{{ .Spec }}", {{ queryValue . }})
		}
{{- end }}
{{- end }}

		endpoint := {{ fmtPath . }}
		if encoded := params.Encode(); encoded != "" {
			endpoint += "?" + encoded
		}

{{- if eq .PaginationStyle "hasNext" }}
		var result struct {
			client.PaginatedResponseRepresentation
			Results []{{ .ItemType }} ` + "`" + `json:"{{ .ResultsField }}"` + "`" + `
		}
		if err := c.transport.Do(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
			return nil, false, err
		}
		return result.Results, result.HasNext, nil
{{- else if eq .PaginationStyle "sizeCheck" }}
		var result struct {
			Results    []{{ .ItemType }} ` + "`" + `json:"{{ .ResultsField }}"` + "`" + `
			TotalCount int64 ` + "`" + `json:"totalCount"` + "`" + `
		}
		if err := c.transport.Do(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
			return nil, false, err
		}
		return result.Results, len(result.Results) >= pageSize && len(result.Results) > 0, nil
{{- else if eq .PaginationStyle "totalCount" }}
		var result struct {
			TotalCount int ` + "`" + `json:"totalCount"` + "`" + `
			Results    []{{ .ItemType }} ` + "`" + `json:"{{ .ResultsField }}"` + "`" + `
		}
		if err := c.transport.Do(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
			return nil, false, err
		}
		hasNext := (page+1)*pageSize < result.TotalCount
		return result.Results, hasNext, nil
{{- else if eq .PaginationStyle "rawArray" }}
		var result []{{ .ItemType }}
		if err := c.transport.Do(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
			return nil, false, err
		}
		return result, len(result) >= pageSize && len(result) > 0, nil
{{- end }}
	})
}
{{ end }}

{{- define "paginatedCursor" }}
// {{ .Comment }}
func (c *Client) {{ .Name }}(ctx context.Context{{ range .PathParams }}, {{ .GoName }} string{{ end }}{{ range .QueryParams }}, {{ .Go }} {{ .Type }}{{ end }}) ([]{{ .ItemType }}, error) {
{{- range .RequiredParams }}
	if {{ queryMissing . }} {
		return nil, fmt.Errorf("{{ $.Name }}: {{ .Go }} is required: the server answers 400 without it")
	}
{{- end }}
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	return client.ListAllCursorPages(ctx, {{ .MaxPageSize }}, func(ctx context.Context, cursor string, pageSize int) ([]{{ .ItemType }}, string, error) {
		params := url.Values{}
		params.Set("{{ .PageSizeParam }}", strconv.Itoa(pageSize))
		if cursor != "" {
			params.Set("{{ .CursorParam }}", cursor)
		}
{{- range .QueryParams }}
{{- if .AlwaysSend }}
		params.Set("{{ .Spec }}", {{ queryValue . }})
{{- else }}
		if {{ queryGuard . }} {
			params.Set("{{ .Spec }}", {{ queryValue . }})
		}
{{- end }}
{{- end }}

		endpoint := {{ fmtPath . }}
		if encoded := params.Encode(); encoded != "" {
			endpoint += "?" + encoded
		}

		var result struct {
			Results    []{{ .ItemType }} ` + "`" + `json:"{{ .ResultsField }}"` + "`" + `
			NextCursor string ` + "`" + `json:"{{ .CursorField }}"` + "`" + `
		}
		if err := c.transport.Do(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
			return nil, "", err
		}
		return result.Results, result.NextCursor, nil
	})
}
{{ end }}

{{- define "resolverID" }}
// {{ .Comment }}
func (c *Client) {{ .Name }}(ctx context.Context, name string) (string, error) {
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	listPath := prefix + "{{ .ResourcePath }}{{ if .Resolver.ExtraParams }}?{{ .Resolver.ExtraParams }}{{ end }}"
{{- if eq .Resolver.Mode "filtered" }}
	id, _, err := c.transport.ResolveByNameFiltered(ctx, listPath, "{{ .Resolver.ResultsField }}", "{{ .Resolver.NameField }}", "{{ .Resolver.MatchField }}", "{{ .Resolver.IDField }}", name)
{{- else if .Resolver.Paginated }}
	id, _, err := c.transport.ResolveByNameClientPaged(ctx, listPath, "{{ .Resolver.SearchParam }}", "{{ .Resolver.ResultsField }}", "{{ .Resolver.NameField }}", "{{ .Resolver.IDField }}", name)
{{- else }}
	id, _, err := c.transport.ResolveByNameClient(ctx, listPath, "{{ .Resolver.SearchParam }}", "{{ .Resolver.ResultsField }}", "{{ .Resolver.NameField }}", "{{ .Resolver.IDField }}", name)
{{- end }}
	if err != nil {
		return "", fmt.Errorf("{{ .Name }}(%s): %w", name, err)
	}
	return id, nil
}
{{ end }}

{{- define "resolverTyped" }}
// {{ .Comment }}
func (c *Client) {{ .Name }}(ctx context.Context, name string) (*{{ .Resolver.TypedReturn }}, error) {
	prefix := c.transport.APIPrefix("{{ .Namespace }}", "{{ .Version }}")
	listPath := prefix + "{{ .ResourcePath }}{{ if .Resolver.ExtraParams }}?{{ .Resolver.ExtraParams }}{{ end }}"
{{- if eq .Resolver.Mode "filtered" }}
	_, raw, err := c.transport.ResolveByNameFiltered(ctx, listPath, "{{ .Resolver.ResultsField }}", "{{ .Resolver.NameField }}", "{{ .Resolver.MatchField }}", "{{ .Resolver.IDField }}", name)
{{- else if .Resolver.Paginated }}
	_, raw, err := c.transport.ResolveByNameClientPaged(ctx, listPath, "{{ .Resolver.SearchParam }}", "{{ .Resolver.ResultsField }}", "{{ .Resolver.NameField }}", "{{ .Resolver.IDField }}", name)
{{- else }}
	_, raw, err := c.transport.ResolveByNameClient(ctx, listPath, "{{ .Resolver.SearchParam }}", "{{ .Resolver.ResultsField }}", "{{ .Resolver.NameField }}", "{{ .Resolver.IDField }}", name)
{{- end }}
	if err != nil {
		return nil, fmt.Errorf("{{ .Name }}(%s): %w", name, err)
	}
	var out {{ .Resolver.TypedReturn }}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("{{ .Name }}(%s): decoding matched element: %w", name, err)
	}
	return &out, nil
}
{{ end }}

{{- define "resolverIDDirect" }}
// {{ .Comment }}
func (c *Client) {{ .Name }}(ctx context.Context, name string) (string, error) {
	r, err := c.{{ .Resolver.SourceMethod }}(ctx, name)
	if err != nil {
		return "", fmt.Errorf("{{ .Name }}(%s): %w", name, err)
	}
	if {{ .Resolver.IDNilCheck }} {
		return "", fmt.Errorf("{{ .Name }}(%s): response missing id", name)
	}
	return strconv.Itoa({{ .Resolver.IDDeref }}), nil
}
{{ end }}

{{- define "resolverTypedDirect" }}
// {{ .Comment }}
func (c *Client) {{ .Name }}(ctx context.Context, name string) (*{{ .Resolver.TypedReturn }}, error) {
	return c.{{ .Resolver.SourceMethod }}(ctx, name)
}
{{ end }}

{{- define "apply" }}
// {{ .Comment }}
func (c *Client) {{ .Name }}(ctx context.Context, request *{{ .Apply.RequestType }}{{ .Apply.ExtraArgs }}) (string, bool, error) {
{{- if .Apply.TokenUploadMode }}
{{- /* ---------- Token-upload mode ---------- */}}
{{- if .Apply.NameIsPointer }}
	var name string
	if request.{{ .Apply.NameGoField }} != nil {
		name = *request.{{ .Apply.NameGoField }}
	}
{{- else }}
	name := request.{{ .Apply.NameGoField }}
{{- end }}
	if name == "" {
		return "", false, fmt.Errorf("{{ .Name }}: {{ .Apply.NameLeafField }} must not be empty")
	}
	id, err := c.{{ .Apply.ResolverMethod }}(ctx, name)
	if err != nil {
		if apiErr := client.AsAPIError(err); apiErr != nil && apiErr.HasStatus(404) {
			if token == "" {
				return "", false, fmt.Errorf("{{ .Name }}: create requires a non-empty token")
			}
			tokenBytes, decodeErr := base64.StdEncoding.DecodeString(token)
			if decodeErr != nil {
				return "", false, fmt.Errorf("{{ .Name }}: decode token: %w", decodeErr)
			}
			tokenReq := &{{ .Apply.TokenRequestType }}{EncodedToken: &tokenBytes}
			resp, createErr := c.{{ .Apply.TokenUploadMethod }}(ctx, tokenReq)
			if createErr != nil {
				return "", false, fmt.Errorf("{{ .Name }}: create: %w", createErr)
			}
			// Update metadata on the newly created resource.
			_, updateErr := c.{{ .Apply.UpdateMethod }}(ctx, resp.ID, request)
			if updateErr != nil {
				return "", false, fmt.Errorf("{{ .Name }}: update metadata after create(%s): %w", resp.ID, updateErr)
			}
			return resp.ID, true, nil
		}
		return "", false, fmt.Errorf("{{ .Name }}: resolve: %w", err)
	}
	// Update path: optionally re-upload token, then always update metadata.
	if token != "" {
		tokenBytes, decodeErr := base64.StdEncoding.DecodeString(token)
		if decodeErr != nil {
			return "", false, fmt.Errorf("{{ .Name }}: decode token: %w", decodeErr)
		}
		tokenReq := &{{ .Apply.TokenRequestType }}{EncodedToken: &tokenBytes}
		_, replaceErr := c.{{ .Apply.TokenReplaceMethod }}(ctx, id, tokenReq)
		if replaceErr != nil {
			return "", false, fmt.Errorf("{{ .Name }}: replace token(%s): %w", id, replaceErr)
		}
	}
{{- if .Apply.UpdateReturnsVal }}
	_, err = c.{{ .Apply.UpdateMethod }}(ctx, id, request)
{{- else }}
	err = c.{{ .Apply.UpdateMethod }}(ctx, id, request)
{{- end }}
	if err != nil {
		return "", false, fmt.Errorf("{{ .Name }}: update(%s): %w", id, err)
	}
	return id, false, nil
{{- else }}
{{- /* ---------- Standard mode ---------- */}}
{{- if .Apply.NameNested }}
	var name string
	if request.{{ .Apply.NameParentField }} != nil && request.{{ .Apply.NameGoField }} != nil {
		name = *request.{{ .Apply.NameGoField }}
	}
{{- else if .Apply.NameIsPointer }}
	var name string
	if request.{{ .Apply.NameGoField }} != nil {
		name = *request.{{ .Apply.NameGoField }}
	}
{{- else }}
	name := request.{{ .Apply.NameGoField }}
{{- end }}
	if name == "" {
		return "", false, fmt.Errorf("{{ .Name }}: {{ .Apply.NameLeafField }} must not be empty")
	}
	id, err := c.{{ .Apply.ResolverMethod }}(ctx, name)
	if err != nil {
		if apiErr := client.AsAPIError(err); apiErr != nil && apiErr.HasStatus(404) {
{{- if .Apply.VersionLock }}
			zeroVersionLock(request)
{{- end }}
{{- if .Apply.ClassicCreate }}
			resp, createErr := c.{{ .Apply.CreateMethod }}(ctx, "0", request)
{{- else }}
			resp, createErr := c.{{ .Apply.CreateMethod }}(ctx, request{{ .Apply.ExtraCallArgs }})
{{- end }}
			if createErr != nil {
				return "", false, fmt.Errorf("{{ .Name }}: create: %w", createErr)
			}
			return {{ .Apply.CreateReturnID }}, true, nil
		}
		return "", false, fmt.Errorf("{{ .Name }}: resolve: %w", err)
	}
{{- if .Apply.HasUpdateType }}
{{- if .Apply.VersionLock }}
	// Fetch current resource to extract versionLock values for optimistic locking.
	current, getErr := c.{{ .Apply.GetMethod }}(ctx, id)
	if getErr != nil {
		return "", false, fmt.Errorf("{{ .Name }}: get for versionLock(%s): %w", id, getErr)
	}
	// Convert create request to update type via JSON round-trip,
	// then inject current versionLock values from the fetched resource.
	updateReq, convErr := convertAndInjectVersionLock[{{ .Apply.UpdateType }}, {{ .Apply.GetType }}](request, current)
	if convErr != nil {
		return "", false, fmt.Errorf("{{ .Name }}: convert for update(%s): %w", id, convErr)
	}
{{- if .Apply.UpdateReturnsVal }}
	_, err = c.{{ .Apply.UpdateMethod }}(ctx, id, updateReq)
{{- else }}
	err = c.{{ .Apply.UpdateMethod }}(ctx, id, updateReq)
{{- end }}
{{- else }}
	// Convert create request to update type via JSON round-trip.
	data, marshalErr := json.Marshal(request)
	if marshalErr != nil {
		return "", false, fmt.Errorf("{{ .Name }}: marshal for update(%s): %w", id, marshalErr)
	}
	var updateReq {{ .Apply.UpdateType }}
	if unmarshalErr := json.Unmarshal(data, &updateReq); unmarshalErr != nil {
		return "", false, fmt.Errorf("{{ .Name }}: unmarshal for update(%s): %w", id, unmarshalErr)
	}
{{- if .Apply.UpdateReturnsVal }}
	_, err = c.{{ .Apply.UpdateMethod }}(ctx, id, &updateReq)
{{- else }}
	err = c.{{ .Apply.UpdateMethod }}(ctx, id, &updateReq)
{{- end }}
{{- end }}
{{- else }}
{{- if .Apply.MembershipPreFetch }}
	// Fetch current membership to preserve existing devices in the patch.
	membership, memberErr := c.{{ .Apply.MembershipFetchMethod }}(ctx, id{{ .Apply.MembershipFetchExtraArgs }})
	if memberErr != nil {
		return "", false, fmt.Errorf("{{ .Name }}: fetch membership(%s): %w", id, memberErr)
	}
	assignments := make([]{{ .Apply.MembershipAssignmentType }}, 0, len(membership))
	for _, m := range membership {
		mid := m.{{ .Apply.MembershipSourceIDField }}
		sel := true
		assignments = append(assignments, {{ .Apply.MembershipAssignmentType }}{
			{{ .Apply.MembershipAssignmentIDField }}: &mid,
			Selected: &sel,
		})
	}
{{- if .Apply.MembershipRequestFieldIsPtr }}
	request.{{ .Apply.MembershipRequestField }} = &assignments
{{- else }}
	request.{{ .Apply.MembershipRequestField }} = assignments
{{- end }}
{{- end }}
{{- if .Apply.UpdateReturnsVal }}
	_, err = c.{{ .Apply.UpdateMethod }}(ctx, id, request)
{{- else }}
	err = c.{{ .Apply.UpdateMethod }}(ctx, id, request)
{{- end }}
{{- end }}
	if err != nil {
		return "", false, fmt.Errorf("{{ .Name }}: update(%s): %w", id, err)
	}
	return id, false, nil
{{- end }}
}
{{ end }}
`

// ---------------------------------------------------------------------------
// Test template (uses <% %> delimiters to avoid {{ }} conflicts with Go maps)
// ---------------------------------------------------------------------------

var testTemplate = `// Code generated by tools/generate; DO NOT EDIT.

// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package <% .Package %>

import (
	"context"
	"net/http"
	"testing"
)

<% range .Methods -%>
<%- if eq .Category "paginated" %>
<% template "testPaginated" . %>
<%- else if eq .Category "paginatedCursor" %>
<% template "testPaginatedCursor" . %>
<%- else if eq .Category "unwrap" %>
<% template "testUnwrap" . %>
<%- else if eq .Category "get" %>
<% template "testGet" . %>
<%- else if eq .Category "create" %>
<% template "testCreate" . %>
<%- else if eq .Category "actionWithResponse" %>
<% template "testActionWithResponse" . %>
<%- else if eq .Category "update" %>
<% template "testUpdate" . %>
<%- else if eq .Category "multipart" %>
<% template "testMultipart" . %>
<%- else if eq .Category "raw" %>
<% template "testRaw" . %>
<%- else if eq .Category "resolverID" %>
<% template "testResolverID" . %>
<%- else if eq .Category "resolverTyped" %>
<% template "testResolverTyped" . %>
<%- else if eq .Category "resolverIDDirect" %>
<% template "testResolverIDDirect" . %>
<%- else if eq .Category "resolverTypedDirect" %>
<% template "testResolverTypedDirect" . %>
<%- else if eq .Category "apply" %>
<% template "testApply" . %>
<%- else %>
<% template "testAction" . %>
<%- end %>
<% end %>

<%- define "testGet" %>
func Test<% .Name %>(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != <% httpConst .HTTPMethod %> {
			t.Errorf("method = %s, want <% .HTTPMethod %>", r.Method)
		}<% requiredQueryAsserts . %><% headerAsserts . %>
		<%- if eq .Format "xml" %>
		writeXML(t, w, http.StatusOK, "<<% .ResponseWireName %>></<% .ResponseWireName %>>")
		<%- else if .ResponseIsJSONArray %>
		writeJSON(t, w, http.StatusOK, []map[string]any{{}})
		<%- else %>
		writeJSON(t, w, http.StatusOK, map[string]any{})
		<%- end %>
	})

	result, err := c.<% .Name %>(context.Background()<% testCallArgs . %><% testExtraArgs . %><% testHeaderArgs . %>)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}

func Test<% .Name %>_NotFound(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, _ *http.Request) {
		<%- if eq .Format "xml" %>
		writeXML(t, w, http.StatusNotFound, "<error>not found</error>")
		<%- else %>
		writeJSON(t, w, http.StatusNotFound, map[string]any{
			"httpStatus": 404,
			"traceId":    "trace-nf",
			"errors":     []map[string]string{{"code": "NOT_FOUND", "field": "id", "description": "not found"}},
		})
		<%- end %>
	})

	_, err := c.<% .Name %>(context.Background()<% testCallArgs . %><% testExtraArgs . %><% testHeaderArgs . %>)
	if err == nil {
		t.Fatal("expected error")
	}
}
<% end %>

<%- define "testCreate" %>
func Test<% .Name %>(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != <% httpConst .HTTPMethod %> {
			t.Errorf("method = %s, want <% .HTTPMethod %>", r.Method)
		}<% requiredQueryAsserts . %><% headerAsserts . %>
<%- if eq .Format "xml" %>
		writeXML(t, w, <% statusConst .ExpectedStatus %>, "<<% .ResponseWireName %>></<% .ResponseWireName %>>")
<%- else if .ResponseIsJSONArray %>
		writeJSON(t, w, <% statusConst .ExpectedStatus %>, []map[string]any{{}})
<%- else %>
		writeJSON(t, w, <% statusConst .ExpectedStatus %>, map[string]any{})
<%- end %>
	})

	result, err := c.<% .Name %>(context.Background()<% testCallArgs . %>, <% requestArg .RequestType %><% testExtraArgs . %><% testHeaderArgs . %>)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}
<% end %>

<%- define "testUpdate" %>
func Test<% .Name %>(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != <% httpConst .HTTPMethod %> {
			t.Errorf("method = %s, want <% .HTTPMethod %>", r.Method)
		}<% requiredQueryAsserts . %><% headerAsserts . %>
		w.WriteHeader(<% statusConst .ExpectedStatus %>)
	})

	err := c.<% .Name %>(context.Background()<% testCallArgs . %>, <% requestArg .RequestType %><% testExtraArgs . %><% testHeaderArgs . %>)
	if err != nil {
		t.Fatal(err)
	}
}
<% end %>

<%- define "testAction" %>
func Test<% .Name %>(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != <% httpConst .HTTPMethod %> {
			t.Errorf("method = %s, want <% .HTTPMethod %>", r.Method)
		}<% requiredQueryAsserts . %><% headerAsserts . %>
		w.WriteHeader(<% statusConst .ExpectedStatus %>)
	})

	err := c.<% .Name %>(context.Background()<% testCallArgs . %><% testExtraArgs . %><% testHeaderArgs . %>)
	if err != nil {
		t.Fatal(err)
	}
}
<% end %>

<%- define "testMultipart" %>
func Test<% .Name %>(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != <% httpConst .HTTPMethod %> {
			t.Errorf("method = %s, want <% .HTTPMethod %>", r.Method)
		}<% requiredQueryAsserts . %><% headerAsserts . %>
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "multipart/form-data") {
			t.Errorf("Content-Type = %q, want multipart/form-data", ct)
		}
		<%- if and .ResponseType .ResponseIsJSONArray %>
		writeJSON(t, w, <% statusConst .ExpectedStatus %>, []map[string]any{{}})
		<%- else if .ResponseType %>
		writeJSON(t, w, <% statusConst .ExpectedStatus %>, map[string]any{})
		<%- else %>
		w.WriteHeader(<% statusConst .ExpectedStatus %>)
		<%- end %>
	})

	<%- if .ResponseType %>
	result, err := c.<% .Name %>(context.Background()<% testCallArgs . %><% testMultipartArgs . %>)
	<%- else %>
	err := c.<% .Name %>(context.Background()<% testCallArgs . %><% testMultipartArgs . %>)
	<%- end %>
	if err != nil {
		t.Fatal(err)
	}
	<%- if .ResponseType %>
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	<%- end %>
}
<% end %>

<%- define "testRaw" %>
func Test<% .Name %>(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != <% httpConst .HTTPMethod %> {
			t.Errorf("method = %s, want <% .HTTPMethod %>", r.Method)
		}<% requiredQueryAsserts . %><% headerAsserts . %>
		<%- if .ResponseType %>
		w.WriteHeader(<% statusConst .ExpectedStatus %>)
		_, _ = w.Write([]byte("<ok/>"))
		<%- else %>
		w.WriteHeader(<% statusConst .ExpectedStatus %>)
		<%- end %>
	})

	<%- if and .RequestType .ResponseType %>
	result, err := c.<% .Name %>(context.Background()<% testCallArgs . %>, []byte("<in/>"))
	<%- else if .RequestType %>
	err := c.<% .Name %>(context.Background()<% testCallArgs . %>, []byte("<in/>"))
	<%- else if .ResponseType %>
	result, err := c.<% .Name %>(context.Background()<% testCallArgs . %>)
	<%- else %>
	err := c.<% .Name %>(context.Background()<% testCallArgs . %>)
	<%- end %>
	if err != nil {
		t.Fatal(err)
	}
	<%- if .ResponseType %>
	if len(result) == 0 {
		t.Fatal("expected non-empty result body")
	}
	<%- end %>
}
<% end %>

<%- define "testUnwrap" %>
// The envelope shape the spec declares and the bare array the same endpoint
// may answer with must both decode. Asserting only the declared one is what
// let the account lists ship broken for five days: the stub served whatever
// the method assumed, so the unit tests passed while every real call failed.
func Test<% .Name %>(t *testing.T) {
	bodies := []struct {
		name string
		body any
	}{
<%- if isStringSlice .UnwrapResults %>
		{name: "envelope", body: map[string]any{"totalCount": 1, "results": []string{"item-1"}}},
		{name: "bare_array", body: []string{"item-1"}},
<%- else %>
		{name: "envelope", body: map[string]any{"totalCount": 1, "results": []map[string]any{{}}}},
		{name: "bare_array", body: []map[string]any{{}}},
<%- end %>
	}

	for _, tc := range bodies {
		t.Run(tc.name, func(t *testing.T) {
			c, mux := testServerWithOpts(t, WithTenantID("t-test"))
			mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
				if r.Method != <% httpConst .HTTPMethod %> {
					t.Errorf("method = %s, want <% .HTTPMethod %>", r.Method)
				}<% requiredQueryAsserts . %><% headerAsserts . %>
				writeJSON(t, w, http.StatusOK, tc.body)
			})

			results, err := c.<% .Name %>(context.Background()<% testCallArgs . %><% testExtraArgs . %><% testHeaderArgs . %>)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 {
				t.Fatalf("len = %d, want 1", len(results))
			}
		})
	}
}

func Test<% .Name %>_NotFound(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, http.StatusNotFound, map[string]any{
			"httpStatus": 404,
			"traceId":    "trace-nf",
			"errors":     []map[string]string{{"code": "NOT_FOUND", "field": "id", "description": "not found"}},
		})
	})

	_, err := c.<% .Name %>(context.Background()<% testCallArgs . %><% testExtraArgs . %><% testHeaderArgs . %>)
	if err == nil {
		t.Fatal("expected error")
	}
}
<% end %>

<%- define "testActionWithResponse" %>
func Test<% .Name %>(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != <% httpConst .HTTPMethod %> {
			t.Errorf("method = %s, want <% .HTTPMethod %>", r.Method)
		}<% requiredQueryAsserts . %><% headerAsserts . %>
<%- if .ResponseIsJSONArray %>
		writeJSON(t, w, <% statusConst .ExpectedStatus %>, []map[string]any{{}})
<%- else %>
		writeJSON(t, w, <% statusConst .ExpectedStatus %>, map[string]any{})
<%- end %>
	})

	result, err := c.<% .Name %>(context.Background()<% testCallArgs . %><% testExtraArgs . %><% testHeaderArgs . %>)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}
<% end %>

<%- define "testRequiredRefusal" %>
<%- if .RequiredParams %>

func Test<% .Name %>RefusesMissingRequiredParam(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		t.Error("request sent without a parameter the server refuses a call without")
	})

	if _, err := c.<% .Name %>(context.Background()<% testCallArgs . %><% testZeroExtraArgs . %><% testHeaderArgs . %>); err == nil {
		t.Fatal("want an error for a missing required parameter, got nil")
	}
}
<%- end %>
<%- end %>

<%- define "testPaginated" %>
func Test<% .Name %>(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != <% httpConst .HTTPMethod %> {
			t.Errorf("method = %s, want <% .HTTPMethod %>", r.Method)
		}<% requiredQueryAsserts . %><% headerAsserts . %>
<%- if eq .PaginationStyle "rawArray" %>
		writeJSON(t, w, http.StatusOK, []map[string]any{{}})
<%- else %>
		writeJSON(t, w, http.StatusOK, map[string]any{
			"<% .ResultsField %>": []map[string]any{{}},
			"totalCount":          1,
			"hasNext":             false,
		})
<%- end %>
	})

	results, err := c.<% .Name %>(context.Background()<% testCallArgs . %><% testExtraArgs . %><% testHeaderArgs . %>)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
}
<% template "testRequiredRefusal" . %><% end %>

<%- define "testPaginatedCursor" %>
func Test<% .Name %>(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	// Two pages, so the walk is actually exercised: the handler hands out a
	// cursor on the first call and withholds one on the second. A single-page
	// stub would pass even if the generated method ignored the cursor entirely.
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != <% httpConst .HTTPMethod %> {
			t.Errorf("method = %s, want <% .HTTPMethod %>", r.Method)
		}<% requiredQueryAsserts . %><% headerAsserts . %>
		switch r.URL.Query().Get("<% .CursorParam %>") {
		case "":
			writeJSON(t, w, http.StatusOK, map[string]any{
				"<% .ResultsField %>": []map[string]any{{}},
				"<% .CursorField %>":  "page-2",
			})
		case "page-2":
			writeJSON(t, w, http.StatusOK, map[string]any{
				"<% .ResultsField %>": []map[string]any{{}},
			})
		default:
			t.Errorf("unexpected cursor %q", r.URL.Query().Get("<% .CursorParam %>"))
		}
	})

	results, err := c.<% .Name %>(context.Background()<% testCallArgs . %><% testExtraArgs . %><% testHeaderArgs . %>)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
}
<% end %>

<%- define "testResolverID" %>
func Test<% .Name %>(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
<%- if .Resolver.ResultsField %>
			"<% .Resolver.ResultsField %>": []map[string]any{
				{<% resolverIDStub .Resolver %>, <% resolverNameStub .Resolver.MatchField %>},
			},
<%- else %>
			"results": []map[string]any{
				{<% resolverIDStub .Resolver %>, <% resolverNameStub .Resolver.MatchField %>},
			},
			"totalCount": 1,
<%- end %>
		})
	})

	id, err := c.<% .Name %>(context.Background(), "target")
	if err != nil {
		t.Fatal(err)
	}
	if id != "<% resolverExpectedID .Resolver %>" {
		t.Errorf("id = %q, want <% resolverExpectedID .Resolver %>", id)
	}
}
<% end %>

<%- define "testResolverTyped" %>
func Test<% .Name %>(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
<%- if .Resolver.ResultsField %>
			"<% .Resolver.ResultsField %>": []map[string]any{
				{<% resolverIDStub .Resolver %>, <% resolverNameStub .Resolver.MatchField %>},
			},
<%- else %>
			"results": []map[string]any{
				{<% resolverIDStub .Resolver %>, <% resolverNameStub .Resolver.MatchField %>},
			},
			"totalCount": 1,
<%- end %>
		})
	})

	result, err := c.<% .Name %>(context.Background(), "target")
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}
<% end %>

<%- define "testResolverIDDirect" %>
func Test<% .Name %>(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		writeXML(t, w, http.StatusOK, "<<% .ResponseWireName %>><% .Resolver.IDTestInnerXML %></<% .ResponseWireName %>>")
	})

	id, err := c.<% .Name %>(context.Background(), "test-id")
	if err != nil {
		t.Fatal(err)
	}
	if id != "42" {
		t.Errorf("id = %q, want 42", id)
	}
}
<% end %>

<%- define "testResolverTypedDirect" %>
func Test<% .Name %>(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
	mux.HandleFunc("<% testPath . %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		writeXML(t, w, http.StatusOK, "<<% .ResponseWireName %>><% .Resolver.IDTestInnerXML %></<% .ResponseWireName %>>")
	})

	result, err := c.<% .Name %>(context.Background(), "test-id")
	if err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
}
<% end %>

<%- define "testApply" %>
func Test<% .Name %>_Create(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
<%- if .Apply.TokenUploadMode %>
	// List returns no matches → resolver returns 404 → apply creates via token upload.
	mux.HandleFunc("<% applyListPath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"<% applyResultsKey .Apply %>": []any{},
			"totalCount":                    0,
		})
	})
	// Token upload creates the resource.
	mux.HandleFunc("<% applyTokenUploadPath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		writeJSON(t, w, http.StatusCreated, map[string]any{
			"id":   "new-id",
			"href": "/new-id",
		})
	})
	// Metadata update after creation.
	mux.HandleFunc("<% applyTokenCreateUpdatePath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"id": "new-id", "name": "target"})
	})

	id, created, err := c.<% .Name %>(context.Background(), <% applyRequestExpr .Apply %><% .Apply.ExtraTestCallArgs %>)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("expected created = true")
	}
	if id != "new-id" {
		t.Errorf("id = %q, want new-id", id)
	}
<%- else if .Apply.ClassicCreate %>
	// Classic direct resolver: GetByName returns 404 → apply creates.
	mux.HandleFunc("<% applyListPath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("<% applyCreatePath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		writeXML(t, w, <% applyCreateStatus .Apply %>, "<<% .Apply.ClassicCreateWireName %>><id>42</id></<% .Apply.ClassicCreateWireName %>>")
	})
<%- else if .Apply.SameListCreatePath %>
	// List and create share the same path — single handler dispatches on method.
	mux.HandleFunc("<% applyListPath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(t, w, http.StatusOK, map[string]any{
				"<% applyResultsKey .Apply %>": []any{},
				"totalCount":                    0,
			})
		case http.MethodPost:
			writeJSON(t, w, <% applyCreateStatus .Apply %>, map[string]any{
				"id":   <% applyTestCreateIDJSON .Apply %>,
				"href": "/new-id",
			})
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
<%- else %>
	// List returns no matches → resolver returns 404 → apply creates.
	mux.HandleFunc("<% applyListPath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"<% applyResultsKey .Apply %>": []any{},
			"totalCount":                    0,
		})
	})
	mux.HandleFunc("<% applyCreatePath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		writeJSON(t, w, <% applyCreateStatus .Apply %>, map[string]any{
			"id":   <% applyTestCreateIDJSON .Apply %>,
			"href": "/new-id",
		})
	})
<%- end %>
<%- if not .Apply.TokenUploadMode %>

	id, created, err := c.<% .Name %>(context.Background(), <% applyRequestExpr .Apply %><% .Apply.ExtraTestCallArgs %>)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("expected created = true")
	}
<%- if .Apply.ClassicCreate %>
	if id != "42" {
		t.Errorf("id = %q, want 42", id)
	}
<%- else %>
	if id != "<% applyTestCreateIDExpected .Apply %>" {
		t.Errorf("id = %q, want <% applyTestCreateIDExpected .Apply %>", id)
	}
<%- end %>
<%- end %>
}

func Test<% .Name %>_Update(t *testing.T) {
	c, mux := testServerWithOpts(t, WithTenantID("t-test"))
<%- if .Apply.TokenUploadMode %>
	// List returns a match → resolver succeeds → apply updates with token replace + metadata.
	mux.HandleFunc("<% applyListPath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"<% applyResultsKey .Apply %>": []map[string]any{
				{"<% .Apply.ListIDField %>": "existing-id", "<% .Apply.ListNameField %>": "target"},
			},
			"totalCount": 1,
		})
	})
	// Token replace (re-upload).
	mux.HandleFunc("<% applyTokenReplacePath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"id": "existing-id", "name": "target"})
	})
	// Metadata update.
	mux.HandleFunc("<% applyUpdatePath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{"id": "existing-id", "name": "target"})
	})

	id, created, err := c.<% .Name %>(context.Background(), <% applyRequestExpr .Apply %><% .Apply.ExtraTestCallArgs %>)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("expected created = false")
	}
	if id != "existing-id" {
		t.Errorf("id = %q, want existing-id", id)
	}
<%- else if .Apply.ClassicCreate %>
	// Classic direct resolver: GetByName returns the resource with id=42 → apply updates.
	mux.HandleFunc("<% applyListPath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		writeXML(t, w, http.StatusOK, "<<% .Apply.ClassicResolverWireName %>><% .Apply.ClassicResolverIDInnerXML %></<% .Apply.ClassicResolverWireName %>>")
	})
	mux.HandleFunc("<% applyUpdatePath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
<%- if .Apply.UpdateReturnsVal %>
		writeXML(t, w, <% applyUpdateStatus .Apply %>, "<<% .Apply.ClassicCreateWireName %>><% .Apply.ClassicResolverIDInnerXML %></<% .Apply.ClassicCreateWireName %>>")
<%- else %>
		w.WriteHeader(<% applyUpdateStatus .Apply %>)
<%- end %>
	})

	id, created, err := c.<% .Name %>(context.Background(), <% applyRequestExpr .Apply %><% .Apply.ExtraTestCallArgs %>)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("expected created = false")
	}
	if id != "42" {
		t.Errorf("id = %q, want 42", id)
	}
<%- else %>
	// List returns a match → resolver succeeds → apply updates.
	mux.HandleFunc("<% applyListPath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"<% applyResultsKey .Apply %>": []map[string]any{
				{"<% .Apply.ListIDField %>": "existing-id", "<% .Apply.ListNameField %>": "target"},
			},
			"totalCount": 1,
		})
	})
<%- if .Apply.VersionLock %>
<%- if .Apply.SameGetUpdatePath %>
	// GET (versionLock fetch) and Update share the same path — dispatch on method.
	mux.HandleFunc("<% applyGetPath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(t, w, http.StatusOK, map[string]any{"id": "existing-id", "versionLock": 5})
		case http.MethodPut, http.MethodPatch:
<%- if .Apply.UpdateReturnsVal %>
			writeJSON(t, w, <% applyUpdateStatus .Apply %>, map[string]any{"id": "existing-id"})
<%- else %>
			w.WriteHeader(<% applyUpdateStatus .Apply %>)
<%- end %>
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	})
<%- else %>
	// GET handler for versionLock fetch.
	mux.HandleFunc("<% applyGetPath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"id": "existing-id", "versionLock": 5})
	})
	mux.HandleFunc("<% applyUpdatePath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
<%- if .Apply.UpdateReturnsVal %>
		writeJSON(t, w, <% applyUpdateStatus .Apply %>, map[string]any{"id": "existing-id"})
<%- else %>
		w.WriteHeader(<% applyUpdateStatus .Apply %>)
<%- end %>
	})
<%- end %>
<%- else %>
<%- if .Apply.MembershipPreFetch %>
	// Membership pre-fetch handler: returns a single device so the Apply method
	// has current membership to inject into the patch request.
	mux.HandleFunc("<% applyMembershipFetchPath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		writeJSON(t, w, http.StatusOK, map[string]any{
			"results":    []map[string]any{{"mobileDeviceId": "d1"}},
			"totalCount": 1,
		})
	})
<%- end %>
	mux.HandleFunc("<% applyUpdatePath .Apply %>", func(w http.ResponseWriter, r *http.Request) {
<%- if .Apply.UpdateReturnsVal %>
<%- if .Apply.IDNumeric %>
		writeJSON(t, w, <% applyUpdateStatus .Apply %>, map[string]any{"id": 99})
<%- else %>
		writeJSON(t, w, <% applyUpdateStatus .Apply %>, map[string]any{"id": "existing-id"})
<%- end %>
<%- else %>
		w.WriteHeader(<% applyUpdateStatus .Apply %>)
<%- end %>
	})
<%- end %>

	id, created, err := c.<% .Name %>(context.Background(), <% applyRequestExpr .Apply %><% .Apply.ExtraTestCallArgs %>)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("expected created = false")
	}
	if id != "existing-id" {
		t.Errorf("id = %q, want existing-id", id)
	}
<%- end %>
}
<% end %>
`

// testAPIBase builds the URL prefix the generated httptest handlers register,
// for one namespace and version. It mirrors Transport.APIPrefix.
//
// There is no /api segment: the GA gateway mounts each namespace at the root
// (see Transport.APIPrefix). This file is a deliberate second copy of that
// rule, because the generator is its own Go module and cannot import the SDK —
// a divergence is self-detecting rather than silent, since the generated
// handlers would register at a path the client never calls and make test fails.
//
// There is no scope segment either: the tenant travels in the X-Tenant-Id
// header now, so the path is the same for every tenant. That also retired the
// duplicated tenant-first namespace allowlist this file used to carry.
func testAPIBase(namespace, version string) string {
	if version == "" {
		return fmt.Sprintf("/%s", namespace)
	}
	return fmt.Sprintf("/%s/%s", namespace, version)
}

// transportCall renders one generated method's transport invocation. See the
// funcMap entry of the same name for why it lives in Go.
func transportCall(m GoMethod) string {
	bodyArg := "nil"
	if m.Category == "create" || m.Category == "update" {
		bodyArg = "request"
		if m.RequestType == "[]byte" {
			bodyArg = "body"
		}
	}
	resultArg := "nil"
	switch m.Category {
	case "get", "create", "actionWithResponse":
		resultArg = "&result"
	}
	method := httpConstName(m.HTTPMethod)

	if len(m.HeaderParams) > 0 {
		var opts []string
		// The "get" shape has always gone through Do, which hardcodes 200 and
		// ignores ExpectedStatus. Leaving the field unset preserves that
		// exactly rather than promoting a value the old call never read.
		if m.Category != "get" {
			opts = append(opts, "ExpectedStatus: "+statusConstName(m.ExpectedStatus))
		}
		if m.ContentType != "" {
			opts = append(opts, fmt.Sprintf("ContentType: %q", m.ContentType))
		}
		if m.NoRetry {
			opts = append(opts, "NoRetry: true")
		}
		opts = append(opts, "Headers: headers")
		return fmt.Sprintf("c.transport.DoWithOptions(ctx, %s, endpoint, %s, client.RequestOptions{%s}, %s)",
			method, bodyArg, strings.Join(opts, ", "), resultArg)
	}

	if m.ContentType != "" {
		name := "DoWithContentType"
		if m.NoRetry {
			name = "DoWithContentTypeNoRetry"
		}
		return fmt.Sprintf("c.transport.%s(ctx, %s, endpoint, %s, %q, %s, %s)",
			name, method, bodyArg, m.ContentType, statusConstName(m.ExpectedStatus), resultArg)
	}
	if m.Category == "get" {
		return fmt.Sprintf("c.transport.Do(ctx, %s, endpoint, %s, %s)", method, bodyArg, resultArg)
	}
	return fmt.Sprintf("c.transport.DoExpect(ctx, %s, endpoint, %s, %s, %s)",
		method, bodyArg, statusConstName(m.ExpectedStatus), resultArg)
}

// httpConstName and statusConstName render an HTTP method and status as the
// net/http constant a generated file refers to, falling back to a literal for
// anything net/http does not name. Shared by the templates (as httpConst /
// statusConst) and by transportCall.
func httpConstName(method string) string {
	m := map[string]string{
		"GET": "http.MethodGet", "POST": "http.MethodPost",
		"PATCH": "http.MethodPatch", "PUT": "http.MethodPut",
		"DELETE": "http.MethodDelete",
	}
	if v, ok := m[method]; ok {
		return v
	}
	return fmt.Sprintf("%q", method)
}

func statusConstName(code int) string {
	m := map[int]string{200: "http.StatusOK", 201: "http.StatusCreated", 202: "http.StatusAccepted", 204: "http.StatusNoContent"}
	if v, ok := m[code]; ok {
		return v
	}
	return strconv.Itoa(code)
}
