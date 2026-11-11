// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestDeclaredPageSizeMax(t *testing.T) {
	param := func(name string, maxV *float64) *openapi3.ParameterRef {
		return &openapi3.ParameterRef{Value: &openapi3.Parameter{
			Name: name, In: openapi3.ParameterInQuery,
			Schema: &openapi3.SchemaRef{Value: &openapi3.Schema{Max: maxV}},
		}}
	}
	f := func(v float64) *float64 { return &v }

	op := &openapi3.Operation{Parameters: openapi3.Parameters{param("page", f(9999)), param("page-size", f(500))}}
	if got := declaredPageSizeMax(&openapi3.PathItem{}, op, "page-size"); got != 500 {
		t.Errorf("operation-level maximum = %d, want 500", got)
	}
	// A path-level parameter counts too.
	item := &openapi3.PathItem{Parameters: openapi3.Parameters{param("size", f(250))}}
	if got := declaredPageSizeMax(item, &openapi3.Operation{}, "size"); got != 250 {
		t.Errorf("path-level maximum = %d, want 250", got)
	}
	// Nothing declared, or a different parameter name, leaves the default in charge.
	if got := declaredPageSizeMax(&openapi3.PathItem{}, &openapi3.Operation{Parameters: openapi3.Parameters{param("page-size", nil)}}, "page-size"); got != 0 {
		t.Errorf("undeclared maximum = %d, want 0", got)
	}
	if got := declaredPageSizeMax(&openapi3.PathItem{}, op, "pageSize"); got != 0 {
		t.Errorf("other parameter name = %d, want 0", got)
	}
}
