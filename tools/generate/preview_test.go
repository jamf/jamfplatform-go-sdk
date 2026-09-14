// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// GitOps v2192 prefixed all twelve ai-governance summaries with "Preview - ".
// The summary is what becomes a method's godoc sentence, so without the strip
// every exported method read "ListPolicies preview - List active …".
func TestStripPreviewPrefixOnlyFiresWhenTheOperationDeclaresPreview(t *testing.T) {
	for _, tc := range []struct {
		name    string
		summary string
		preview bool
		want    string
	}{
		{"hyphen", "Preview - List active AI governance policies", true, "List active AI governance policies"},
		{"colon", "Preview: Get a policy by ID", true, "Get a policy by ID"},
		{"en dash", "Preview – Get tool detail", true, "Get tool detail"},
		{"em dash", "Preview — Get tool detail", true, "Get tool detail"},
		{"no space", "Preview-Get tool detail", true, "Get tool detail"},
		{"leading space", "  Preview - Get tool detail", true, "Get tool detail"},
		{"unprefixed summary is untouched", "List available vendor tools", true, "List available vendor tools"},
		// The gate is the extension, not the text, so an operation that is not
		// declared preview keeps its prose even when it starts with the word —
		// "Preview a report before sending it" is a verb, not a state.
		{"not declared preview", "Preview - List active policies", false, "Preview - List active policies"},
		{"preview as a verb on a non-preview op", "Preview a report before sending it", false, "Preview a report before sending it"},
		// A declared-preview operation whose summary genuinely starts with the
		// verb is the one ambiguous case. The prefix form requires a
		// separator, so a bare verb survives.
		{"preview as a verb on a preview op", "Preview a report before sending it", true, "Preview a report before sending it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripPreviewPrefix(tc.summary, tc.preview); got != tc.want {
				t.Fatalf("stripPreviewPrefix(%q, %v) = %q, want %q", tc.summary, tc.preview, got, tc.want)
			}
		})
	}
}

// kin-openapi hands a vendor extension back as a decoded bool, raw JSON
// bytes, a string or a json.RawMessage depending on version and code path, so
// the decoder has to cope with all four or the marker silently reads false and
// the godoc line vanishes with nothing failing.
func TestIsPreviewDecodesEveryExtensionRepresentation(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  any
		want bool
	}{
		{"bool true", true, true},
		{"bool false", false, false},
		{"raw bytes", []byte("true"), true},
		{"raw bytes false", []byte("false"), false},
		{"string", "true", true},
		{"json.RawMessage", json.RawMessage("true"), true},
		{"json.RawMessage false", json.RawMessage("false"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			op := &openapi3.Operation{Extensions: map[string]any{"x-preview": tc.raw}}
			if got := isPreview(op); got != tc.want {
				t.Fatalf("isPreview(x-preview: %v) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}

	if isPreview(&openapi3.Operation{}) {
		t.Fatal("isPreview on an operation with no extension = true, want false")
	}
	if isPreview(nil) {
		t.Fatal("isPreview(nil) = true, want false")
	}
}

// Every ai-governance operation declares x-preview: true as of v2192, and the
// marker must reach the generated godoc as its own sentence rather than being
// folded into the method's verb phrase.
func TestAIGovernanceMethodsCarryThePreviewGodocLine(t *testing.T) {
	src := readGenerated(t, "policies.go")
	const marker = "// Preview: this endpoint is marked preview in the Jamf API spec"

	if n := strings.Count(src, marker); n != 9 {
		t.Fatalf("policies.go carries %d preview godoc lines, want 9 (one per operation)", n)
	}
	// The prefix must not survive into the verb phrase.
	if strings.Contains(src, "preview - ") {
		t.Error(`policies.go contains "preview - ": the summary's state prefix reached the method comment`)
	}
	if !strings.Contains(src, "// ListPolicies list active AI governance policies for the tenant.") {
		t.Error("ListPolicies lost its summary sentence")
	}

	tools := readGenerated(t, "tools.go")
	if n := strings.Count(tools, marker); n != 3 {
		t.Fatalf("tools.go carries %d preview godoc lines, want 3", n)
	}
	if !strings.Contains(tools, "// ListTools list available vendor tools.") {
		t.Error("ListTools lost its summary sentence")
	}
}

// readGenerated reads one of the generated aigovernance files from the repo
// root, which is two levels up from tools/generate.
func readGenerated(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "jamfplatform", "aigovernance", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(raw)
}
