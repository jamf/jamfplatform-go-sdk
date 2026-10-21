// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// probeDoc builds the smallest spec buildMethod will accept for one GET
// operation, optionally carrying x-required-privileges.
func probeDoc(t *testing.T, specPrivileges []any) *openapi3.T {
	t.Helper()
	op := &openapi3.Operation{
		Summary:   "probe",
		Responses: openapi3.NewResponses(),
	}
	if specPrivileges != nil {
		op.Extensions = map[string]any{"x-required-privileges": specPrivileges}
	}
	doc := &openapi3.T{Paths: openapi3.NewPaths()}
	doc.Paths.Set("/v1/things", &openapi3.PathItem{Get: op})
	return doc
}

// A privilege set the SDK supplies from the gateway's authorization policy has
// to be distinguishable from a spec-declared one. The account family's 18
// methods are the case: an empty set was being rendered downstream as "no
// permission needed", which is false, and simply filling it in without saying
// where from would replace one wrong claim with an unattributed one.
func TestRequiredPrivilegesConfigSuppliesAndAttributes(t *testing.T) {
	spec := SpecDef{
		Namespace:          "sso",
		RequiredPrivileges: map[string][]string{"GET /v1/things": {"sso-connections:read"}},
	}
	opDef := OperationDef{Op: "GET /v1/things", Name: "ListThings"}

	m, err := buildMethod(probeDoc(t, nil), spec, opDef, nil)
	if err != nil {
		t.Fatalf("buildMethod: %v", err)
	}
	if got := m.ScopedPrivileges; len(got) != 1 || got[0] != "sso-connections:read" {
		t.Fatalf("ScopedPrivileges = %v, want the configured set", got)
	}
	if m.PrivilegeSource != privilegeSourceGatewayPolicy {
		t.Fatalf("PrivilegeSource = %q, want %q", m.PrivilegeSource, privilegeSourceGatewayPolicy)
	}
	if !strings.Contains(m.Comment, "The published spec declares none for this operation") {
		t.Errorf("godoc does not attribute the privileges:\n%s", m.Comment)
	}
}

// Spec-declared privileges keep Source "spec", so adding the config mechanism
// cannot have relabelled the other eleven packages' entries.
func TestSpecDeclaredPrivilegesKeepSpecSource(t *testing.T) {
	spec := SpecDef{Namespace: "pro"}
	opDef := OperationDef{Op: "GET /v1/things", Name: "ListThings"}

	m, err := buildMethod(probeDoc(t, []any{"buildings:read"}), spec, opDef, nil)
	if err != nil {
		t.Fatalf("buildMethod: %v", err)
	}
	if m.PrivilegeSource != privilegeSourceSpec {
		t.Fatalf("PrivilegeSource = %q, want %q", m.PrivilegeSource, privilegeSourceSpec)
	}
	if strings.Contains(m.Comment, "authorization policy") {
		t.Errorf("spec-declared privileges got the gateway-policy attribution:\n%s", m.Comment)
	}
}

// The entry expires when upstream starts declaring the privileges. Choosing a
// winner silently is the failure to avoid: the config values would shadow
// whatever the spec published, including a correction.
func TestRequiredPrivilegesConfigFailsWhenSpecDeclaresThem(t *testing.T) {
	spec := SpecDef{
		Namespace:          "sso",
		RequiredPrivileges: map[string][]string{"GET /v1/things": {"sso-connections:read"}},
	}
	opDef := OperationDef{Op: "GET /v1/things", Name: "ListThings"}

	_, err := buildMethod(probeDoc(t, []any{"sso-connections:read"}), spec, opDef, nil)
	if err == nil {
		t.Fatal("buildMethod accepted a config entry the spec now declares")
	}
	if !strings.Contains(err.Error(), "delete the config entry") {
		t.Errorf("error does not say what to do: %v", err)
	}
}

var registryEntry = regexp.MustCompile(`"([A-Za-z0-9]+)":\s*\{Method:.*?Scoped: (nil|\[\]string\{[^}]*\}).*?Source: "([a-z-]*)"\}`)

// End-to-end guard on the generated account registry. Every one of the 26
// methods must carry a non-empty Scoped set attributed to the gateway policy:
// the values are absent from the published spec by construction — these routes
// resolve the organization from the token, which exempts them from the
// transform the publishing pipeline attaches x-required-privileges during — so
// nothing upstream will ever restore them, and losing the config entry would
// silently return the registry to claiming no privileges are required.
func TestAccountRegistryPrivilegesComeFromGatewayPolicy(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "jamfplatform", "account", "permissions.go"))
	if err != nil {
		t.Fatalf("reading account registry: %v", err)
	}
	matches := registryEntry.FindAllStringSubmatch(string(data), -1)
	if len(matches) != 26 {
		t.Fatalf("parsed %d registry entries, want the account package's 26", len(matches))
	}
	for _, m := range matches {
		method, scoped, source := m[1], m[2], m[3]
		if scoped == "nil" {
			t.Errorf("%s: Scoped is nil — the requiredPrivileges config entry is missing", method)
			continue
		}
		if source != privilegeSourceGatewayPolicy {
			t.Errorf("%s: Source = %q, want %q", method, source, privilegeSourceGatewayPolicy)
		}
	}
}
