// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// A shared parameter, response or request body that $refs a schema the pruner
// just deleted must go with it, or the published spec cannot be re-read: the
// CI path loads api/ and fails on the dangling $ref. Components that name only
// surviving schemas must stay.
func TestDropDanglingComponentsRemovesOnlyThoseNamingPrunedSchemas(t *testing.T) {
	ref := func(name string) *openapi3.SchemaRef {
		return &openapi3.SchemaRef{Ref: "#/components/schemas/" + name}
	}
	body := func(name string) *openapi3.RequestBodyRef {
		return &openapi3.RequestBodyRef{Value: &openapi3.RequestBody{
			Content: openapi3.Content{"application/json": &openapi3.MediaType{Schema: ref(name)}},
		}}
	}
	resp := func(name string) *openapi3.ResponseRef {
		d := name
		return &openapi3.ResponseRef{Value: &openapi3.Response{
			Description: &d,
			Content:     openapi3.Content{"application/json": &openapi3.MediaType{Schema: ref(name)}},
		}}
	}
	param := func(name string) *openapi3.ParameterRef {
		return &openapi3.ParameterRef{Value: &openapi3.Parameter{Name: name, In: "query", Schema: ref(name)}}
	}
	doc := &openapi3.T{Components: &openapi3.Components{
		Parameters:    openapi3.ParametersMap{"gone": param("Gone"), "kept": param("Kept")},
		Responses:     openapi3.ResponseBodies{"gone": resp("Gone"), "kept": resp("Kept")},
		RequestBodies: openapi3.RequestBodies{"gone": body("Gone"), "kept": body("Kept")},
	}}

	dropDanglingComponents(doc, nil)
	if len(doc.Components.Parameters) != 2 {
		t.Fatal("pruned with nothing to prune")
	}

	dropDanglingComponents(doc, []string{"Gone"})
	for kind, got := range map[string][2]bool{
		"parameter":    {doc.Components.Parameters["gone"] == nil, doc.Components.Parameters["kept"] != nil},
		"response":     {doc.Components.Responses["gone"] == nil, doc.Components.Responses["kept"] != nil},
		"request body": {doc.Components.RequestBodies["gone"] == nil, doc.Components.RequestBodies["kept"] != nil},
	} {
		if !got[0] {
			t.Errorf("%s naming the pruned schema survived", kind)
		}
		if !got[1] {
			t.Errorf("%s naming a surviving schema was dropped", kind)
		}
	}
}
