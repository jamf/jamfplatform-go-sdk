// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unwrapResults generates a call to client.UnwrapResults, which json.Unmarshals
// the body. The transport picks XML for any /proclassic/ path and a
// format=xml spec generates XML tags throughout, neither of which looks at the
// Go type — so the combination compiles, emits a passing httptest stub, and
// fails on every real call. Generation refuses it instead.
func TestValidateUnwrapCodecRejectsNonJSON(t *testing.T) {
	tests := []struct {
		name    string
		method  GoMethod
		wantErr string
	}{
		{
			name:    "classic namespace",
			method:  GoMethod{Name: "ListThings", Namespace: "proclassic", UnwrapResults: "[]Thing"},
			wantErr: "decoded as XML by the transport",
		},
		{
			name:    "xml format",
			method:  GoMethod{Name: "ListThings", Namespace: "pro", Format: "xml", UnwrapResults: "[]Thing"},
			wantErr: `"format": "xml"`,
		},
		{
			name:   "json is fine",
			method: GoMethod{Name: "ListThings", Namespace: "licensing", UnwrapResults: "[]Thing"},
		},
		{
			name:   "classic without unwrapResults is fine",
			method: GoMethod{Name: "GetThing", Namespace: "proclassic", ResponseType: "Thing"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUnwrapCodec("spec test.json", []GoMethod{tc.method})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
			// The message has to name the operation and the way out, or it
			// sends whoever hits it back into the generator to find out why.
			if !strings.Contains(err.Error(), "ListThings") || !strings.Contains(err.Error(), "unwrapResults is JSON-only") {
				t.Errorf("error is not actionable: %v", err)
			}
		})
	}
}

// rootLegacySpec is a minimal OpenAPI 3 document whose only response schema
// carries the shape validateNoUntypedFields exists to catch: a property whose
// schema is a construct the generator has no branch for — here an `anyOf` over
// two `$ref`s, the sibling of the discriminator-less `oneOf` that produced
// account-sso's `ConnectionRequest.connection any`. isRefUnion covers `oneOf`
// and hoistInlineObjects lifts it; `anyOf` matches neither, so the property is
// never hoisted and never named, schemaRefToGoType falls through to its
// `default: return "any"`, and the field is emitted as a bare `any`.
const rootLegacySpec = `{
  "openapi": "3.0.3",
  "info": { "title": "Root Legacy API", "version": "1.0.0" },
  "paths": {
    "/v1/things/{id}": {
      "get": {
        "operationId": "getThing",
        "parameters": [
          { "name": "id", "in": "path", "required": true, "schema": { "type": "string" } }
        ],
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": { "schema": { "$ref": "#/components/schemas/Thing" } }
            }
          }
        }
      }
    }
  },
  "components": {
    "schemas": {
      "Thing": {
        "type": "object",
        "properties": {
          "id": { "type": "string" },
          "connection": {
            "anyOf": [
              { "$ref": "#/components/schemas/AlphaConnection" },
              { "$ref": "#/components/schemas/BetaConnection" }
            ]
          }
        }
      },
      "AlphaConnection": {
        "type": "object",
        "properties": { "alpha": { "type": "string" } }
      },
      "BetaConnection": {
        "type": "object",
        "properties": { "beta": { "type": "string" } }
      }
    }
  }
}`

// The legacy root path (a SpecDef with no "package") must be guarded by
// validateNoUntypedFields too. It was wired against emittedTypes — a set of
// type *names* — so every GoType it handed the validator had a nil Fields
// slice and the check could not fire whatever the spec contained. Nothing in
// config.json takes this path today, which is exactly why a regression here
// would go unnoticed until a bare `any` reached a consumer.
func TestProcessSpecRejectsUntypedFieldOnRootPath(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "jamfplatform"), 0o755); err != nil {
		t.Fatalf("creating output dir: %v", err)
	}
	specPath := filepath.Join(root, "root-legacy-api.json")
	if err := os.WriteFile(specPath, []byte(rootLegacySpec), 0o644); err != nil {
		t.Fatalf("writing spec: %v", err)
	}

	cfg := Config{
		Package: "jamfplatform",
		Module:  "github.com/jamf/jamfplatform-go-sdk",
	}
	spec := SpecDef{
		File:      "root-legacy-api.json",
		Namespace: "pro",
		// Package deliberately empty: emits to the root package (legacy).
		// ScopeTypes is set because the fixture spec declares no
		// x-scope-types and resolveScopeTypes refuses that — without it this
		// test fails on the scope guard before reaching the untyped-any
		// assertion it exists to make.
		ScopeTypes: []string{"tenant"},
		Operations: []OperationDef{
			{Op: "GET /v1/things/{id}", Name: "GetThing"},
		},
	}

	var rootTypes []GoType
	err := processSpec(root, cfg, spec, specPath, false, make(map[string]bool), &rootTypes)
	if err == nil {
		t.Fatal("generation succeeded: the root path emitted Thing.Connection as a bare any and validateNoUntypedFields did not fire")
	}
	if !strings.Contains(err.Error(), "emitted as untyped any") {
		t.Fatalf("expected an untyped-any failure, got: %v", err)
	}
	for _, want := range []string{"Thing", "Connection", `"connection"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %s: %v", want, err)
		}
	}
}
