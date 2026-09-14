// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestStripSpecHTML(t *testing.T) {
	cases := map[string]string{
		"Includes all results.</br> Fields allowed:": "Includes all results.  Fields allowed:",
		"one<br/>two": "one two",
		"a &amp; b":   "a & b",
		// Angle brackets used as placeholder syntax are not HTML and must
		// survive — Jamf documents sort as "<field_name>:asc".
		"format <field_name>:asc": "format <field_name>:asc",
		"plain text":              "plain text",
	}
	for in, want := range cases {
		if got := stripSpecHTML(in); got != want {
			t.Errorf("stripSpecHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWrapCommentText(t *testing.T) {
	got := wrapCommentText("alpha bravo charlie delta echo", 12)
	want := []string{"alpha bravo", "charlie", "delta echo"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if lines := wrapCommentText("   ", 10); lines != nil {
		t.Errorf("blank input produced %q, want nil", lines)
	}
	// A word longer than the width is never split.
	long := "supercalifragilisticexpialidocious"
	if lines := wrapCommentText(long, 10); len(lines) != 1 || lines[0] != long {
		t.Errorf("oversized word wrapped to %q", lines)
	}
}

func TestParameterEnumValues(t *testing.T) {
	strEnum := &openapi3.SchemaRef{Value: &openapi3.Schema{Enum: []any{"General", "Pending+Failed"}}}
	if got := parameterEnumValues(strEnum); strings.Join(got, ",") != `"General","Pending+Failed"` {
		t.Errorf("string enum = %q", got)
	}

	// Repeatable params carry the constraint on the item schema.
	itemEnum := &openapi3.SchemaRef{Value: &openapi3.Schema{
		Items: &openapi3.SchemaRef{Value: &openapi3.Schema{Enum: []any{"asc", "desc"}}},
	}}
	if got := parameterEnumValues(itemEnum); strings.Join(got, ",") != `"asc","desc"` {
		t.Errorf("item enum = %q", got)
	}

	if got := parameterEnumValues(nil); got != nil {
		t.Errorf("nil schema = %q, want nil", got)
	}
	if got := parameterEnumValues(&openapi3.SchemaRef{Value: &openapi3.Schema{}}); len(got) != 0 {
		t.Errorf("no enum = %q, want empty", got)
	}
}

func TestParameterComment(t *testing.T) {
	param := func(name, desc string, enum ...any) *openapi3.ParameterRef {
		p := &openapi3.Parameter{Name: name, Description: desc}
		if len(enum) > 0 {
			p.Schema = &openapi3.SchemaRef{Value: &openapi3.Schema{Enum: enum}}
		}
		return &openapi3.ParameterRef{Value: p}
	}

	m := GoMethod{
		PathParams:  []GoPathParam{{SpecName: "id", GoName: "id"}, {SpecName: "subset", GoName: "subset"}},
		QueryParams: []ExtraParam{{Spec: "filter", Go: "filter"}, {Spec: "undocumented-key", Go: "undocumentedKey", Undocumented: true}},
	}
	pathItem := &openapi3.PathItem{Parameters: openapi3.Parameters{param("id", "ID to filter by")}}
	op := &openapi3.Operation{Parameters: openapi3.Parameters{
		param("subset", "Subset to filter by", "General", "Location"),
		param("filter", "RSQL query.\n\nFields allowed in the query: name"),
		param("ignored", "Not in the Go signature, must not appear"),
	}}

	got := parameterComment(m, collectSpecParams(pathItem, op), nil)
	wantLines := []string{
		"\n//\n// Parameters:",
		"//   - id: ID to filter by.",
		"//   - subset: Subset to filter by.",
		`//     Allowed values: "General", "Location".`,
		"//   - filter: RSQL query.",
		"//     Fields allowed in the query: name.",
	}
	if want := strings.Join(wantLines, "\n"); got != want {
		t.Errorf("parameterComment mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
	// A param declared ":undocumented" is skipped, not emitted as an empty
	// entry — the spec genuinely says nothing about it.
	if strings.Contains(got, "undocumentedKey") {
		t.Error("undocumented param leaked into the comment")
	}

	// Nothing documentable → no block at all, so the method keeps its
	// single-line godoc.
	bare := GoMethod{PathParams: []GoPathParam{{SpecName: "id", GoName: "id"}}}
	bareItem := &openapi3.PathItem{Parameters: openapi3.Parameters{param("id", "")}}
	if got := parameterComment(bare, collectSpecParams(bareItem, &openapi3.Operation{}), nil); got != "" {
		t.Errorf("undescribed param produced %q, want empty", got)
	}
}

// TestResolveQueryParamsRejectsUnmatchedQueryParam pins the name-match half of
// resolveQueryParams: the GetBaselineRules baselineId/baseline-id incident, in
// which a mistyped config entry generated a method that sent a query key the
// server ignored and kept compiling.
func TestResolveQueryParamsRejectsUnmatchedQueryParam(t *testing.T) {
	m := GoMethod{
		HTTPMethod:  "GET",
		SpecPath:    "/v1/tenant/{tenantId}/rules",
		QueryParams: []ExtraParam{{Spec: "baselineId", Go: "baselineID"}},
	}
	op := &openapi3.Operation{Parameters: openapi3.Parameters{
		{Value: &openapi3.Parameter{Name: "baseline-id", Description: "Given baseline ID", Required: true}},
	}}
	specParams := collectSpecParams(nil, op)

	err := resolveQueryParams(&m, specParams)
	if err == nil {
		t.Fatal("expected an error for a config param absent from the spec, got nil")
	}
	if !strings.Contains(err.Error(), "baselineId") || !strings.Contains(err.Error(), "baseline-id") {
		t.Errorf("error %q should name both the unmatched config param and the spec's actual param", err)
	}

	// Marking the same mismatch ":undocumented" opts it out of the check. It
	// stays OPTIONAL: there is no spec parameter to read required-ness from,
	// and the zero-value guard is the only safe emission for a param the spec
	// is silent about.
	m.QueryParams[0].Undocumented = true
	if err := resolveQueryParams(&m, specParams); err != nil {
		t.Errorf("undocumented opt-out should suppress the error, got %v", err)
	}
	if m.QueryParams[0].AlwaysSend {
		t.Error("an undocumented param must not be marked required")
	}
}

// TestResolveQueryParamsDerivesRequired pins the other half: required-ness is
// read off the spec parameter, never declared in config.json. A required param
// emitted behind the optional zero-value guard is silently dropped when the
// caller passes the zero value, and the server's 400 reads like a fault rather
// than a caller error — Security Cloud's ListActivationProfilesV1 `origin`
// shipped that way.
func TestResolveQueryParamsDerivesRequired(t *testing.T) {
	m := GoMethod{
		HTTPMethod: "GET",
		SpecPath:   "/v1/activation-profiles",
		QueryParams: []ExtraParam{
			{Spec: "origin", Go: "origin", Type: "string"},
			{Spec: "sort", Go: "sort", Type: "[]string"},
		},
	}
	op := &openapi3.Operation{Parameters: openapi3.Parameters{
		{Value: &openapi3.Parameter{Name: "origin", Required: true, Description: "Origin."}},
		{Value: &openapi3.Parameter{Name: "sort", Description: "Sort."}},
	}}
	if err := resolveQueryParams(&m, collectSpecParams(nil, op)); err != nil {
		t.Fatalf("resolveQueryParams: %v", err)
	}
	if !m.QueryParams[0].AlwaysSend {
		t.Error("origin is required: true in the spec but was not marked required")
	}
	if m.QueryParams[1].AlwaysSend {
		t.Error("sort carries no required flag but was marked required")
	}

	// An operation-level declaration overriding a path-level one supplies the
	// required flag too — the same precedence collectSpecParams applies to
	// descriptions.
	m2 := GoMethod{QueryParams: []ExtraParam{{Spec: "filter", Go: "filter", Type: "string"}}}
	pathItem := &openapi3.PathItem{Parameters: openapi3.Parameters{
		{Value: &openapi3.Parameter{Name: "filter", Description: "Path-level filter."}},
	}}
	opOverride := &openapi3.Operation{Parameters: openapi3.Parameters{
		{Value: &openapi3.Parameter{Name: "filter", Required: true, Description: "Operation-level filter."}},
	}}
	if err := resolveQueryParams(&m2, collectSpecParams(pathItem, opOverride)); err != nil {
		t.Fatalf("resolveQueryParams: %v", err)
	}
	if !m2.QueryParams[0].AlwaysSend {
		t.Error("an operation-level required override did not reach the ExtraParam")
	}
}

// TestResolveQueryParamsKeepsGuardWhenSpecDeclaresDefault pins the one
// carve-out. A required param that also declares a default is a malformed
// declaration (OpenAPI: "default SHALL NOT be used with required"), and where
// the default is substantive the server has something to fall back on — so
// absence is defined and the guard stays. Pro's columns-to-export is the live
// case. An empty default states nothing and does not earn the carve-out;
// Pro's FailCloudDistributionPointUploadV1 `type` is that case, default "".
func TestResolveQueryParamsKeepsGuardWhenSpecDeclaresDefault(t *testing.T) {
	param := func(name string, def any) *openapi3.ParameterRef {
		return &openapi3.ParameterRef{Value: &openapi3.Parameter{
			Name: name, Required: true, Description: name + ".",
			Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{Default: def}},
		}}
	}
	m := GoMethod{QueryParams: []ExtraParam{
		{Spec: "columns", Go: "columns", Type: "[]string"},
		{Spec: "emptyList", Go: "emptyList", Type: "[]string"},
		{Spec: "emptyStr", Go: "emptyStr", Type: "string"},
		{Spec: "noDefault", Go: "noDefault", Type: "string"},
	}}
	op := &openapi3.Operation{Parameters: openapi3.Parameters{
		param("columns", []any{"computerName", "deviceId"}),
		param("emptyList", []any{}),
		param("emptyStr", ""),
		param("noDefault", nil),
	}}
	if err := resolveQueryParams(&m, collectSpecParams(nil, op)); err != nil {
		t.Fatalf("resolveQueryParams: %v", err)
	}
	want := map[string]bool{
		"columns":   false, // substantive default → absence is defined, keep the guard
		"emptyList": true,
		"emptyStr":  true,
		"noDefault": true,
	}
	for _, q := range m.QueryParams {
		if q.AlwaysSend != want[q.Spec] {
			t.Errorf("%s: AlwaysSend = %v, want %v", q.Spec, q.AlwaysSend, want[q.Spec])
		}
	}
}

func TestParameterCommentNamesEmittedEnumTypes(t *testing.T) {
	// A $ref'd enum schema that the generator emits as a type is named, not
	// re-listed; the constants under that type carry the values.
	refd := &openapi3.ParameterRef{Value: &openapi3.Parameter{
		Name:        "notificationType",
		Description: "type of the notification",
		Schema: &openapi3.SchemaRef{
			Ref:   "#/components/schemas/NotificationType",
			Value: &openapi3.Schema{Enum: []any{"APNS_CERT_REVOKED", "GSX_CERT_EXPIRED"}},
		},
	}}
	m := GoMethod{PathParams: []GoPathParam{{SpecName: "notificationType", GoName: "notificationType"}}}
	op := &openapi3.Operation{Parameters: openapi3.Parameters{refd}}

	got := parameterComment(m, collectSpecParams(nil, op), map[string]bool{"NotificationType": true})
	if !strings.Contains(got, "Allowed values: see the NotificationType constants.") {
		t.Errorf("expected a pointer to the emitted type, got:\n%s", got)
	}
	if strings.Contains(got, "APNS_CERT_REVOKED") {
		t.Error("values were listed inline despite an emitted type existing")
	}

	// The same schema when NO type is emitted for it (enum reachable only from
	// a parameter) must fall back to the inline list.
	got = parameterComment(m, collectSpecParams(nil, op), nil)
	if !strings.Contains(got, `"APNS_CERT_REVOKED", "GSX_CERT_EXPIRED"`) {
		t.Errorf("expected an inline list with no emitted type, got:\n%s", got)
	}
}

func TestEnumConstIdent(t *testing.T) {
	cases := map[string]string{
		"APNS_CERT_REVOKED":                   "ApnsCertRevoked",
		"APPLE_SCHOOL_MANAGER_T_C_NOT_SIGNED": "AppleSchoolManagerTCNotSigned",
		"mdm-byod":                            "MDMByod",
		"General":                             "General",
		"Pending+Failed":                      "PendingFailed",
		// A leading digit is fine — the fragment is only ever a suffix.
		"10.15": "1015",
		// No alphanumerics at all → caller skips and logs.
		"+/+": "",
		"":    "",
	}
	for in, want := range cases {
		if got := enumConstIdent(in); got != want {
			t.Errorf("enumConstIdent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegisterPropertyEnum(t *testing.T) {
	strEnum := func(vals ...any) *openapi3.SchemaRef {
		return &openapi3.SchemaRef{Value: &openapi3.Schema{
			Type: &openapi3.Types{"string"}, Enum: vals,
		}}
	}

	currentPropertyEnums = map[string]GoType{}
	currentSpecTypeNames = map[string]bool{"AccountAccessLevel": true}
	t.Cleanup(func() { currentPropertyEnums, currentSpecTypeNames = nil, nil })

	// Happy path: <Owner><Property>, registered and referenced.
	if got := registerPropertyEnum("Policy", "trigger", strEnum("EVENT", "USER_INITIATED")); got != "PolicyTrigger" {
		t.Errorf("got %q, want PolicyTrigger", got)
	}
	reg, ok := currentPropertyEnums["PolicyTrigger"]
	if !ok || len(reg.EnumValues) != 2 {
		t.Fatalf("PolicyTrigger not registered correctly: %+v", reg)
	}

	// A name the spec already declares must be abandoned, not referenced —
	// otherwise the field points at a type carrying no constants.
	if got := registerPropertyEnum("Account", "accessLevel", strEnum("FullAccess", "SiteAccess")); got != "" {
		t.Errorf("collision returned %q, want empty", got)
	}
	if _, dup := currentPropertyEnums["AccountAccessLevel"]; dup {
		t.Error("collision was registered anyway")
	}

	// Skips: $ref (field type already names it), an unsupported base type, no
	// enum at all. Non-string and single-value enums are NOT skipped — see
	// TestRegisterPropertyEnumNumeric and TestRegisterPropertyEnumSingleValue.
	cases := map[string]*openapi3.SchemaRef{
		"ref":         {Ref: "#/components/schemas/NotificationType", Value: &openapi3.Schema{Enum: []any{"A", "B"}}},
		"boolean":     {Value: &openapi3.Schema{Type: &openapi3.Types{"boolean"}, Enum: []any{true, false}}},
		"no enum":     strEnum(),
		"nil":         nil,
		"item is ref": {Value: &openapi3.Schema{Items: &openapi3.SchemaRef{Ref: "#/components/schemas/X", Value: &openapi3.Schema{Enum: []any{"A", "B"}}}}},
	}
	for label, ref := range cases {
		if got := registerPropertyEnum("Owner", label, ref); got != "" {
			t.Errorf("%s: got %q, want empty", label, got)
		}
	}

	// A repeatable property carries the constraint on its inline item schema.
	items := &openapi3.SchemaRef{Value: &openapi3.Schema{
		Type:  &openapi3.Types{"array"},
		Items: strEnum("MAC", "IOS"),
	}}
	if got := registerPropertyEnum("Device", "platforms", items); got != "DevicePlatforms" {
		t.Errorf("item enum: got %q, want DevicePlatforms", got)
	}
}

// TestRegisterPropertyEnumNumeric pins that a numeric inline enum gets
// constants. uem-connect's refreshRateMinutes and deviceUnmanagedThreshold are
// the live cases: a consumer validating against those sets otherwise has to
// retype six integers with no SDK source, which is what the Terraform
// provider's enumguard forbids.
//
// The alias base tracks the field's own Go type — format int64 generates an
// int64 field, so the alias must be int64 for the constants to be assignable.
func TestRegisterPropertyEnumNumeric(t *testing.T) {
	currentPropertyEnums = map[string]GoType{}
	currentSpecTypeNames = map[string]bool{}
	t.Cleanup(func() { currentPropertyEnums, currentSpecTypeNames = nil, nil })

	int64Enum := &openapi3.SchemaRef{Value: &openapi3.Schema{
		Type: &openapi3.Types{"integer"}, Format: "int64", Enum: []any{60, 120, 1440},
	}}
	if got := registerPropertyEnum("SyncSettings", "refreshRateMinutes", int64Enum); got != "SyncSettingsRefreshRateMinutes" {
		t.Fatalf("got %q, want SyncSettingsRefreshRateMinutes", got)
	}
	reg := currentPropertyEnums["SyncSettingsRefreshRateMinutes"]
	if reg.EnumBaseType != "int64" {
		t.Errorf("base type = %q, want int64", reg.EnumBaseType)
	}
	if len(reg.EnumValues) != 3 {
		t.Fatalf("got %d consts, want 3: %+v", len(reg.EnumValues), reg.EnumValues)
	}
	if reg.EnumValues[0].Name != "SyncSettingsRefreshRateMinutes60" || reg.EnumValues[0].Literal != "60" {
		t.Errorf("first const = %+v", reg.EnumValues[0])
	}

	// No format → int, matching the generated field.
	plain := &openapi3.SchemaRef{Value: &openapi3.Schema{
		Type: &openapi3.Types{"integer"}, Enum: []any{0, 3, 14},
	}}
	if got := registerPropertyEnum("SyncSettings", "deviceUnmanagedThreshold", plain); got == "" {
		t.Fatal("plain integer enum was skipped")
	}
	if base := currentPropertyEnums["SyncSettingsDeviceUnmanagedThreshold"].EnumBaseType; base != "int" {
		t.Errorf("base type = %q, want int", base)
	}
}

// TestRegisterPropertyEnumSingleValue pins that a one-value enum still gets a
// constant. CreatePathV2.Scope accepts exactly [APP] and is the live case: the
// V1 sibling that used to carry the multi-value CreatePathScope block was
// withdrawn, leaving the field a bare string with the vocabulary recorded
// nowhere a consumer could reference.
func TestRegisterPropertyEnumSingleValue(t *testing.T) {
	currentPropertyEnums = map[string]GoType{}
	currentSpecTypeNames = map[string]bool{}
	t.Cleanup(func() { currentPropertyEnums, currentSpecTypeNames = nil, nil })

	one := &openapi3.SchemaRef{Value: &openapi3.Schema{
		Type: &openapi3.Types{"string"}, Enum: []any{"APP"},
	}}
	if got := registerPropertyEnum("CreatePathV2", "scope", one); got != "CreatePathV2Scope" {
		t.Fatalf("got %q, want CreatePathV2Scope", got)
	}
	reg := currentPropertyEnums["CreatePathV2Scope"]
	if len(reg.EnumValues) != 1 || reg.EnumValues[0].Name != "CreatePathV2ScopeApp" || reg.EnumValues[0].Literal != `"APP"` {
		t.Errorf("registered %+v", reg.EnumValues)
	}
	if reg.EnumBaseType != "string" {
		t.Errorf("base type = %q, want string", reg.EnumBaseType)
	}
}

// TestEnumConstsOfBaseNumeric covers the value shapes a decoder hands back for
// an integer literal, and the negative sentinel Jamf uses (-1).
func TestEnumConstsOfBaseNumeric(t *testing.T) {
	got := enumConstsOfBase("T", []any{float64(60), 120, int64(1440), -1, 1.5, "60"}, "int64")
	var names []string
	for _, c := range got {
		names = append(names, c.Name+"="+c.Literal)
	}
	want := "T60=60 T120=120 T1440=1440 TNeg1=-1"
	if strings.Join(names, " ") != want {
		t.Errorf("got %q, want %q (1.5 and \"60\" dropped)", strings.Join(names, " "), want)
	}
}

func TestEnumConsts(t *testing.T) {
	got := enumConsts("Subset", []any{"General", "Location", 42, ""})
	if len(got) != 2 {
		t.Fatalf("got %d consts, want 2 (non-string and empty values dropped): %+v", len(got), got)
	}
	if got[0].Name != "SubsetGeneral" || got[0].Value != "General" || got[0].Literal != `"General"` {
		t.Errorf("first const = %+v", got[0])
	}

	// Two values colliding on one identifier: the first wins, the second is
	// skipped rather than shadowing it.
	coll := enumConsts("T", []any{"FOO_BAR", "foo.bar"})
	if len(coll) != 1 || coll[0].Value != "FOO_BAR" {
		t.Errorf("collision handling produced %+v", coll)
	}
}

func TestDefaultMethodComment(t *testing.T) {
	if got := defaultMethodComment("GetX", SpecDef{}); got != "GetX calls a Jamf Platform API endpoint." {
		t.Errorf("documented spec = %q", got)
	}
	if got := defaultMethodComment("GetX", SpecDef{Undocumented: true}); got != "GetX calls an undocumented Jamf endpoint." {
		t.Errorf("undocumented spec = %q", got)
	}
}

// TestApplyDocNotes pins the three behaviours the config key exists for: a note
// reaches the type it names, it is appended rather than substituted for the
// spec's own description, and a note naming nothing fails loudly instead of
// being dropped.
func TestApplyDocNotes(t *testing.T) {
	t.Run("appends to an existing comment", func(t *testing.T) {
		types := []GoType{{Name: "SyncSettings", Comment: "SyncSettings is a full replacement."}}
		if err := applyDocNotes(types, map[string]string{"SyncSettings": "Except groupSettings."}); err != nil {
			t.Fatalf("applyDocNotes: %v", err)
		}
		want := "SyncSettings is a full replacement.\n// Except groupSettings."
		if types[0].Comment != want {
			t.Errorf("Comment = %q, want %q", types[0].Comment, want)
		}
	})

	t.Run("wraps a long note across comment lines", func(t *testing.T) {
		types := []GoType{{Name: "EmailMappingType", Comment: "EmailMappingType is a set."}}
		note := strings.Repeat("this set spans every vendor and is not a validator source. ", 6)
		if err := applyDocNotes(types, map[string]string{"EmailMappingType": note}); err != nil {
			t.Fatalf("applyDocNotes: %v", err)
		}
		for line := range strings.SplitSeq(types[0].Comment, "\n") {
			if len(strings.TrimPrefix(line, "// ")) > typeDocWidth {
				t.Errorf("line exceeds typeDocWidth (%d): %q", typeDocWidth, line)
			}
		}
	})

	t.Run("a note naming no emitted type is an error", func(t *testing.T) {
		types := []GoType{{Name: "SyncSettings"}}
		err := applyDocNotes(types, map[string]string{
			"SyncSettings": "kept",
			"RenamedAway":  "dropped",
		})
		if err == nil {
			t.Fatal("applyDocNotes returned nil for a key matching no type")
		}
		if !strings.Contains(err.Error(), "RenamedAway") {
			t.Errorf("error does not name the missing key: %v", err)
		}
		if strings.Contains(err.Error(), "SyncSettings") {
			t.Errorf("error names a key that did match: %v", err)
		}
	})

	t.Run("no notes is a no-op", func(t *testing.T) {
		types := []GoType{{Name: "SyncSettings", Comment: "unchanged"}}
		if err := applyDocNotes(types, nil); err != nil {
			t.Fatalf("applyDocNotes: %v", err)
		}
		if types[0].Comment != "unchanged" {
			t.Errorf("Comment = %q, want unchanged", types[0].Comment)
		}
	})
}

// TestApplyMethodNotes pins the same three behaviours TestApplyDocNotes does,
// for the method-level key: a note reaches the method it names, it is appended
// as a new paragraph after everything the spec produced rather than replacing
// it, and a note naming no emitted method fails the build. That last one is the
// whole self-expiry mechanism — these notes record refusals and version floors,
// and the build refusing is what forces one to be deleted when it lifts.
func TestApplyMethodNotes(t *testing.T) {
	t.Run("appends a paragraph after the existing comment", func(t *testing.T) {
		methods := []GoMethod{{
			Name:    "DismissAllNotificationsV1",
			Comment: "DismissAllNotificationsV1 dismisses all notifications.\n//\n// Required privileges: dismiss-notifications:execute.",
		}}
		if err := applyMethodNotes(methods, map[string]string{"DismissAllNotificationsV1": "Not routed at the gateway."}); err != nil {
			t.Fatalf("applyMethodNotes: %v", err)
		}
		want := "DismissAllNotificationsV1 dismisses all notifications.\n//\n// Required privileges: dismiss-notifications:execute.\n//\n// Not routed at the gateway."
		if methods[0].Comment != want {
			t.Errorf("Comment = %q, want %q", methods[0].Comment, want)
		}
	})

	t.Run("keeps the Deprecated paragraph on its own line", func(t *testing.T) {
		methods := []GoMethod{{
			Name:    "GetActivationCode",
			Comment: "GetActivationCode finds the activation code.\n//\n// Deprecated: marked deprecated in the spec.",
		}}
		if err := applyMethodNotes(methods, map[string]string{"GetActivationCode": "No successor exists."}); err != nil {
			t.Fatalf("applyMethodNotes: %v", err)
		}
		// The marker must still start a paragraph of its own — go/doc and
		// staticcheck key on that, and a note run onto the same line would
		// swallow it.
		if !strings.Contains(methods[0].Comment, "\n// Deprecated: marked deprecated in the spec.\n//\n// No successor exists.") {
			t.Errorf("Deprecated paragraph not intact: %q", methods[0].Comment)
		}
	})

	t.Run("wraps a long note across comment lines", func(t *testing.T) {
		methods := []GoMethod{{Name: "GetSsoOidcBrokerConfigV3", Comment: "GetSsoOidcBrokerConfigV3 reads the config."}}
		note := strings.Repeat("the gateway does not route this operation and answers 403 for every caller. ", 6)
		if err := applyMethodNotes(methods, map[string]string{"GetSsoOidcBrokerConfigV3": note}); err != nil {
			t.Fatalf("applyMethodNotes: %v", err)
		}
		for line := range strings.SplitSeq(methods[0].Comment, "\n") {
			if len(strings.TrimPrefix(line, "// ")) > typeDocWidth {
				t.Errorf("line exceeds typeDocWidth (%d): %q", typeDocWidth, line)
			}
		}
	})

	t.Run("a note naming no emitted method is an error", func(t *testing.T) {
		methods := []GoMethod{{Name: "GetActivationCode"}}
		err := applyMethodNotes(methods, map[string]string{
			"GetActivationCode": "kept",
			"WithdrawnUpstream": "dropped",
		})
		if err == nil {
			t.Fatal("applyMethodNotes returned nil for a key matching no method")
		}
		if !strings.Contains(err.Error(), "WithdrawnUpstream") {
			t.Errorf("error does not name the missing key: %v", err)
		}
		if strings.Contains(err.Error(), "GetActivationCode") {
			t.Errorf("error names a key that did match: %v", err)
		}
	})

	t.Run("no notes is a no-op", func(t *testing.T) {
		methods := []GoMethod{{Name: "GetActivationCode", Comment: "unchanged"}}
		if err := applyMethodNotes(methods, nil); err != nil {
			t.Fatalf("applyMethodNotes: %v", err)
		}
		if methods[0].Comment != "unchanged" {
			t.Errorf("Comment = %q, want unchanged", methods[0].Comment)
		}
	})
}
