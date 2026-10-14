// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package profiles

import (
	"encoding/json"
	"strings"
	"testing"
)

// settings decodes a JSON object the way a consumer does before validating it, so the tests carry
// the same float64-shaped numbers the real path sees.
func settings(t *testing.T, encoded string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
		t.Fatalf("failed to decode settings: %v", err)
	}
	return decoded
}

func TestTableLoads(t *testing.T) {
	commit, release := Provenance()
	if len(commit) != 40 {
		t.Errorf("expected a 40-character upstream commit, got %q", commit)
	}
	if release == "" {
		t.Error("expected the upstream release recorded")
	}
	if got := len(PayloadTypes()); got < 100 {
		t.Errorf("expected the table to carry Apple's payload types, got %d", got)
	}
}

func TestValidate_CleanPayload(t *testing.T) {
	problems := Validate("com.apple.applicationaccess", settings(t, `{"allowCamera":true,"allowSafariPrivateBrowsing":false}`))
	if len(problems) != 0 {
		t.Errorf("expected no problems, got %v", problems)
	}
}

func TestValidate_UnknownKey(t *testing.T) {
	problems := Validate("com.apple.applicationaccess", settings(t, `{"bogusKeyOneTwoThree":"x"}`))
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %v", problems)
	}
	if problems[0].Kind != UnknownKey {
		t.Errorf("expected UnknownKey, got %v", problems[0].Kind)
	}
	if !problems[0].StaleTableSuspect() {
		t.Error("expected an unknown key to name the snapshot — a key Apple added since it looks identical to one that never existed")
	}
}

func TestValidate_MiscasedKey(t *testing.T) {
	problems := Validate("com.apple.applicationaccess", settings(t, `{"AllowCamera":true}`))
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %v", problems)
	}
	if problems[0].Kind != MiscasedKey {
		t.Errorf("expected MiscasedKey, got %v", problems[0].Kind)
	}
	if problems[0].Canonical != "allowCamera" {
		t.Errorf("expected Apple's spelling offered, got %q", problems[0].Canonical)
	}
	if problems[0].StaleTableSuspect() {
		t.Error("a miscased key matches a key the table already knows, so the snapshot cannot be the explanation")
	}
}

func TestValidate_WrongType(t *testing.T) {
	problems := Validate("com.apple.applicationaccess", settings(t, `{"allowScreenShot":"yes"}`))
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %v", problems)
	}
	if problems[0].Kind != WrongType {
		t.Errorf("expected WrongType, got %v", problems[0].Kind)
	}
	if !strings.Contains(problems[0].Detail, "a boolean") || !strings.Contains(problems[0].Detail, "a string") {
		t.Errorf("expected the detail to name both types, got %q", problems[0].Detail)
	}
}

func TestValidate_IntegerAcceptsWholeNumberRejectsFraction(t *testing.T) {
	if problems := Validate("com.apple.applicationaccess", settings(t, `{"enforcedSoftwareUpdateDelay":30}`)); len(problems) != 0 {
		t.Errorf("expected a whole number to satisfy an integer, got %v", problems)
	}
	problems := Validate("com.apple.applicationaccess", settings(t, `{"enforcedSoftwareUpdateDelay":30.5}`))
	if len(problems) != 1 || problems[0].Kind != WrongType {
		t.Errorf("expected a fractional value to fail an integer, got %v", problems)
	}
}

func TestValidate_MissingRequiredKey(t *testing.T) {
	problems := Validate("com.apple.notificationsettings", settings(t, `{}`))
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %v", problems)
	}
	if problems[0].Kind != MissingRequiredKey || problems[0].Path != "NotificationSettings" {
		t.Errorf("expected the required top-level key reported, got %v", problems[0])
	}
	if !problems[0].StaleTableSuspect() {
		t.Error("expected a missing required key to name the snapshot — Apple relaxes `required` between revisions, so the key may be optional on the tenant")
	}
}

// TestStaleTableSuspect_PinsTheSet pins which findings a caller is told the snapshot could explain.
// Every finding is an error either way, so the classification is only visible in the diagnostic
// text: without this table, widening or narrowing the set changes what an operator is told with no
// test failing.
func TestStaleTableSuspect_PinsTheSet(t *testing.T) {
	for _, tc := range []struct {
		kind    ProblemKind
		suspect bool
		reason  string
	}{
		{UnknownPayloadType, true, "Apple may have published the type since the snapshot"},
		{UnknownKey, true, "Apple may have published the key since the snapshot"},
		{MissingRequiredKey, true, "Apple relaxes `required` between revisions"},
		{MiscasedPayloadType, false, "the type matches one the table already carries"},
		{MiscasedKey, false, "the key matches one the table already carries"},
		{WrongType, false, "a wrong value type is wrong against every revision"},
		{IntegerOutOfRange, false, "Jamf's 32-bit field is not Apple's constraint"},
	} {
		if got := (Problem{Kind: tc.kind}).StaleTableSuspect(); got != tc.suspect {
			t.Errorf("kind %v: StaleTableSuspect() = %t, want %t — %s", tc.kind, got, tc.suspect, tc.reason)
		}
	}
}

// TestProblemKindString pins a distinct name for every kind. A kind that falls through to the
// default prints as "unknown problem" in every test failure and grouped diagnostic, which reads as a
// finding about the table rather than about the payload. The walk runs to IntegerOutOfRange, the
// last kind declared, so a kind appended after it must extend both the walk and the table.
func TestProblemKindString(t *testing.T) {
	want := map[ProblemKind]string{
		UnknownPayloadType:  "unknown payload type",
		MiscasedPayloadType: "miscased payload type",
		UnknownKey:          "unknown key",
		MiscasedKey:         "miscased key",
		WrongType:           "wrong type",
		MissingRequiredKey:  "missing required key",
		IntegerOutOfRange:   "integer out of range",
	}
	seen := map[string]ProblemKind{}
	for kind := UnknownPayloadType; kind <= IntegerOutOfRange; kind++ {
		got := kind.String()
		if got != want[kind] {
			t.Errorf("ProblemKind(%d).String() = %q, want %q", int(kind), got, want[kind])
		}
		if previous, duplicate := seen[got]; duplicate {
			t.Errorf("ProblemKind(%d) and ProblemKind(%d) both print as %q", int(previous), int(kind), got)
		}
		seen[got] = kind
	}
	if got := (IntegerOutOfRange + 1).String(); got != "unknown problem" {
		t.Errorf("an undeclared kind prints as %q, want the default", got)
	}
}

// TestPayloadSeedOnly asserts a payload type is seed-only exactly when the release branch does not
// declare it. The embedded table may carry no seed branch at all — Apple deletes seed at GA — so the
// seed case is constructed rather than looked up.
func TestPayloadSeedOnly(t *testing.T) {
	release := ReleaseRef()
	if release == "" {
		t.Fatal("table records no release branch; SeedOnly would report every payload as seed-only")
	}
	for _, tc := range []struct {
		name    string
		payload *Payload
		want    bool
	}{
		{"nil payload", nil, false},
		{"no refs recorded", &Payload{}, false},
		{"release only", &Payload{Refs: []string{release}}, false},
		{"release and seed", &Payload{Refs: []string{release, "seed_OS_99_0"}}, false},
		{"seed only", &Payload{Refs: []string{"seed_OS_99_0"}}, true},
	} {
		if got := tc.payload.SeedOnly(); got != tc.want {
			t.Errorf("%s: SeedOnly() = %t, want %t", tc.name, got, tc.want)
		}
	}

	dock, ok := Lookup("com.apple.dock")
	if !ok {
		t.Fatal("com.apple.dock missing from the table")
	}
	if dock.SeedOnly() {
		t.Error("com.apple.dock reads as seed-only; it has been on the release branch for years")
	}
	orientation, ok := dock.Keys["orientation"]
	if !ok {
		t.Fatal("com.apple.dock orientation missing; a nil schema reports false and would pass vacuously")
	}
	if orientation.SeedOnly() {
		t.Error("com.apple.dock orientation reads as seed-only; it is declared on the release branch")
	}
}

func TestValidate_MissingRequiredKeyInsideArrayEntry(t *testing.T) {
	problems := Validate("com.apple.notificationsettings", settings(t, `{"NotificationSettings":[{"AlertType":1}]}`))
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %v", problems)
	}
	if problems[0].Path != "NotificationSettings[0].BundleIdentifier" {
		t.Errorf("expected the nested required key reported with an indexed path, got %q", problems[0].Path)
	}
	if problems[0].Kind != MissingRequiredKey {
		t.Errorf("expected MissingRequiredKey, got %v", problems[0].Kind)
	}
}

func TestValidate_NullIsTreatedAsAbsent(t *testing.T) {
	// A null is discarded by Jamf, so it is never a type error — but under a required key it is a
	// missing key, because the stored payload will not carry it.
	problems := Validate("com.apple.notificationsettings", settings(t,
		`{"NotificationSettings":[{"BundleIdentifier":"com.example.app","PreviewType":null,"GroupingType":null}]}`))
	if len(problems) != 0 {
		t.Errorf("expected nulls to be ignored, got %v", problems)
	}

	problems = Validate("com.apple.notificationsettings", settings(t, `{"NotificationSettings":null}`))
	if len(problems) != 1 || problems[0].Kind != MissingRequiredKey {
		t.Errorf("expected a null required key reported as missing, got %v", problems)
	}
}

func TestValidate_UnknownPayloadType(t *testing.T) {
	problems := Validate("com.example.notarealpayload", settings(t, `{"foo":"bar"}`))
	if len(problems) != 1 || problems[0].Kind != UnknownPayloadType {
		t.Fatalf("expected UnknownPayloadType, got %v", problems)
	}
	if !problems[0].StaleTableSuspect() {
		t.Error("expected an unknown payload type to name the snapshot — Apple may have published one since")
	}
	if problems[0].Path != "" {
		t.Errorf("expected an empty path for a payload-level problem, got %q", problems[0].Path)
	}
}

func TestValidate_MiscasedPayloadType(t *testing.T) {
	// Jamf matches the payload type case-sensitively and rejects the write, unlike a key.
	problems := Validate("com.apple.managedclient.preferences", settings(t, `{}`))
	if len(problems) != 1 || problems[0].Kind != MiscasedPayloadType {
		t.Fatalf("expected MiscasedPayloadType, got %v", problems)
	}
	if problems[0].Canonical != "com.apple.ManagedClient.preferences" {
		t.Errorf("expected Apple's spelling offered, got %q", problems[0].Canonical)
	}
	if problems[0].StaleTableSuspect() {
		t.Error("a miscased payload type matches one the table already knows, so the snapshot cannot be the explanation")
	}
}

func TestValidate_FreeFormSubtreeIsNotValidated(t *testing.T) {
	// Everything below an MCX preference domain is free-form: Jamf stored arbitrary keys there
	// verbatim, and even accepted an element that omitted mcx_preference_settings, so validating
	// past the wildcard would manufacture findings the service does not agree with.
	problems := Validate("com.apple.ManagedClient.preferences", settings(t,
		`{"PayloadContent":{"com.example.notarealapp":{"Forced":[{"mcx_preference_settings":{"totallyMadeUp":"whatever","nested":{"deep":[1,2,{"x":true}]}}}]}}}`))
	if len(problems) != 0 {
		t.Errorf("expected an MCX payload to pass untouched, got %v", problems)
	}

	problems = Validate("com.apple.ManagedClient.preferences", settings(t,
		`{"PayloadContent":{"com.example.app":{"Forced":[{"wrongInnerKey":{"a":1}}]}}}`))
	if len(problems) != 0 {
		t.Errorf("expected no findings below the wildcard, got %v", problems)
	}
}

func TestValidate_MCXRequiresPayloadContent(t *testing.T) {
	// The envelope itself is still validated: Jamf rejects an MCX payload with no PayloadContent.
	problems := Validate("com.apple.ManagedClient.preferences", settings(t, `{}`))
	if len(problems) != 1 || problems[0].Kind != MissingRequiredKey || problems[0].Path != "PayloadContent" {
		t.Errorf("expected PayloadContent reported as required, got %v", problems)
	}
}

func TestValidate_CommonPayloadKeysAccepted(t *testing.T) {
	// Jamf echoes an authored value for the common metadata keys, so an author who sets one must not
	// be told it is unrecognised — and must not be told they are required either, since the consumer
	// and the service supply them.
	problems := Validate("com.apple.applicationaccess", settings(t,
		`{"payloadDisplayName":"My Own Name","PayloadOrganization":"Acme","allowCamera":true}`))
	for _, problem := range problems {
		if problem.Kind == UnknownKey || problem.Kind == MissingRequiredKey {
			t.Errorf("unexpected problem for a common payload key: %v", problem)
		}
	}
}

func TestValidate_WildcardPayloadAcceptsAnyKey(t *testing.T) {
	problems := Validate("com.apple.firstactiveethernet.managed", settings(t, `{"anythingAtAll":123}`))
	if len(problems) != 0 {
		t.Errorf("expected a wildcard payload to accept any key, got %v", problems)
	}
}

// TestValidate_NamedKeysBesideAWildcardAreChecked pins the wire probe of 2026-10-14 against
// com.apple.firstethernet.managed, a payload whose root accepts arbitrary key names and also
// declares the common metadata keys. Jamf stored an undeclared key with any value, and refused
// `PayloadVersion: "x"` while accepting `PayloadVersion: 1`, so the wildcard suppresses only the
// unknown-key finding.
func TestValidate_NamedKeysBesideAWildcardAreChecked(t *testing.T) {
	problems := Validate("com.apple.firstethernet.managed", settings(t,
		`{"AutoJoin":"notabool","VendorThing":{"a":[1,"b"]},"PayloadVersion":1}`))
	if len(problems) != 0 {
		t.Errorf("expected undeclared keys beside a wildcard to pass, got %v", problems)
	}

	problems = Validate("com.apple.firstethernet.managed", settings(t, `{"AutoJoin":true,"PayloadVersion":"x"}`))
	if len(problems) != 1 || problems[0].Kind != WrongType || problems[0].Path != "PayloadVersion" {
		t.Errorf("expected PayloadVersion reported as the wrong type, got %v", problems)
	}

	// The same rule reaches a nested dictionary: ExtensionData carries Apple's documented keys
	// beside vendor extensions.
	problems = Validate("com.apple.extensiblesso", settings(t,
		`{"ExtensionIdentifier":"com.example.sso","Type":"Redirect","ExtensionData":{"allowPassword":"notabool","vendorKey":"x"}}`))
	if len(problems) != 1 || problems[0].Kind != WrongType || problems[0].Path != "ExtensionData.allowPassword" {
		t.Errorf("expected ExtensionData.allowPassword reported as the wrong type, got %v", problems)
	}
}

func TestValidate_ProblemsAreOrderedByPath(t *testing.T) {
	problems := Validate("com.apple.applicationaccess", settings(t, `{"zzBogus":1,"aaBogus":2}`))
	if len(problems) != 2 {
		t.Fatalf("expected 2 problems, got %v", problems)
	}
	if problems[0].Path != "aaBogus" || problems[1].Path != "zzBogus" {
		t.Errorf("expected problems ordered by path, got %q then %q", problems[0].Path, problems[1].Path)
	}
}

func TestValidate_IntegerOutOfJamfRange(t *testing.T) {
	// Apple declares CacheLimit unbounded; Jamf stores a 32-bit signed integer. Wire probing found
	// 2147483647 accepted and 2147483648 rejected with a validation failure.
	if problems := Validate("com.apple.AssetCache.managed", settings(t, `{"CacheLimit":2147483647}`)); len(problems) != 0 {
		t.Errorf("expected int32 max to be accepted, got %v", problems)
	}

	problems := Validate("com.apple.AssetCache.managed", settings(t, `{"CacheLimit":2147483648}`))
	if len(problems) != 1 {
		t.Fatalf("expected 1 problem, got %v", problems)
	}
	if problems[0].Kind != IntegerOutOfRange {
		t.Errorf("expected IntegerOutOfRange, got %v", problems[0].Kind)
	}
	if problems[0].StaleTableSuspect() {
		t.Error("an out-of-range integer is wrong against every version of the schema, so the snapshot cannot be the explanation")
	}
	if !strings.Contains(problems[0].Detail, "2147483648") {
		t.Errorf("expected the offending value in the detail without exponent notation, got %q", problems[0].Detail)
	}
}

func TestValidate_NegativeIntegerOutOfJamfRange(t *testing.T) {
	problems := Validate("com.apple.AssetCache.managed", settings(t, `{"CacheLimit":-2147483649}`))
	if len(problems) != 1 || problems[0].Kind != IntegerOutOfRange {
		t.Errorf("expected IntegerOutOfRange below the 32-bit floor, got %v", problems)
	}
}

func TestValidate_IntegerRangeCheckedInsideNesting(t *testing.T) {
	// The bound applies wherever an integer appears, not only at the top level.
	problems := Validate("com.apple.notificationsettings", settings(t,
		`{"NotificationSettings":[{"BundleIdentifier":"com.example.app","AlertType":2147483648}]}`))
	if len(problems) != 1 || problems[0].Kind != IntegerOutOfRange {
		t.Fatalf("expected IntegerOutOfRange, got %v", problems)
	}
	if problems[0].Path != "NotificationSettings[0].AlertType" {
		t.Errorf("expected an indexed path, got %q", problems[0].Path)
	}
}
