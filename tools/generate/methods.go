// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// ---------------------------------------------------------------------------
// Operations → Go methods
// ---------------------------------------------------------------------------

func extractMethods(doc *openapi3.T, spec SpecDef, enumTypes map[string]bool) ([]GoMethod, error) {
	// Scope is declared once on the spec root, so resolve it once rather than
	// per operation: repeating the work would repeat the same error 700 times
	// for pro.
	scopes, scopesSource, err := resolveScopeTypes(doc, spec)
	if err != nil {
		return nil, err
	}

	var methods []GoMethod
	for _, opDef := range spec.Operations {
		m, err := buildMethod(doc, spec, opDef, enumTypes)
		if err != nil {
			return nil, fmt.Errorf("operation %s: %w", opDef.Op, err)
		}
		m.Scopes = scopes
		m.ScopesSource = scopesSource
		methods = append(methods, m)
	}
	methods, err = appendResolverMethods(methods, spec)
	if err != nil {
		return nil, err
	}
	methods, err = appendApplyMethods(doc, methods, spec)
	if err != nil {
		return nil, err
	}
	if spec.Undocumented {
		unofficialMsg := "\n//\n// Unofficial: this endpoint is not part of Jamf's published API specification." +
			" It was reverse-engineered from live API traffic and may change or be removed without notice."
		for i := range methods {
			if methods[i].Comment == "" {
				methods[i].Comment = methods[i].Name + " calls an undocumented Jamf endpoint."
			}
			methods[i].Comment += unofficialMsg
		}
	}
	return methods, nil
}

// appendResolverMethods synthesizes one pair of resolver methods per
// operation that carries a Resolver config. Each pair consists of:
//
//   - Resolve<ResourceType>IDByName(ctx, name) (string, error)
//   - Resolve<ResourceType>ByName(ctx, name) (*<TypedReturn>, error)
//
// Synthetic methods inherit Namespace/Version/ResourcePath/Tag from the
// source operation so they land in the same per-tag file and build the
// list URL identically to the source List method.
func appendResolverMethods(methods []GoMethod, spec SpecDef) ([]GoMethod, error) {
	byName := make(map[string]*GoMethod, len(methods))
	for i := range methods {
		byName[methods[i].Name] = &methods[i]
	}
	out := append([]GoMethod(nil), methods...)
	for _, opDef := range spec.Operations {
		// Merge singular and plural resolver configs into one slice.
		var resolvers []ResolverConfig
		if opDef.Resolver != nil {
			resolvers = append(resolvers, *opDef.Resolver)
		}
		resolvers = append(resolvers, opDef.Resolvers...)
		if len(resolvers) == 0 {
			continue
		}
		for _, r := range resolvers {
			switch r.Mode {
			case "filtered", "clientFilter", "direct":
				// supported
			case "":
				return nil, fmt.Errorf("resolver on %s: mode required (filtered, clientFilter, or direct)", opDef.Name)
			default:
				return nil, fmt.Errorf("resolver on %s: unknown mode %q", opDef.Name, r.Mode)
			}
			if r.ResourceType == "" {
				return nil, fmt.Errorf("resolver on %s: resourceType is required", opDef.Name)
			}
			// filtered/clientFilter require name/id field paths; direct mode
			// doesn't — the typed source method already decodes the response
			// and the ID is taken from the top-level *int ID field uniformly.
			if r.Mode != "direct" && (r.NameField == "" || r.IDField == "") {
				return nil, fmt.Errorf("resolver on %s: nameField and idField are required for mode %q", opDef.Name, r.Mode)
			}
			src, ok := byName[opDef.Name]
			if !ok {
				return nil, fmt.Errorf("resolver on %s: source operation not found in extracted methods", opDef.Name)
			}
			if err := refuseHeaderParamsOnSynthetic("resolver", r.ResourceType, opDef.Name, src); err != nil {
				return nil, err
			}
			typedReturn := r.TypedReturn
			if typedReturn == "" {
				typedReturn = r.ResourceType
			}
			byField := r.ByField
			if byField == "" {
				byField = "ByName"
			}
			matchField := r.MatchField
			if matchField == "" {
				matchField = r.NameField
			}
			gr := &GoResolver{
				ResourceType: r.ResourceType,
				Mode:         r.Mode,
				NameField:    r.NameField,
				MatchField:   matchField,
				IDField:      r.IDField,
				IDNumeric:    r.IDNumeric,
				SearchParam:  r.SearchParam,
				ResultsField: r.ResultsField,
				TypedReturn:  typedReturn,
				ExtraParams:  r.ExtraParams,
				Paginated:    r.Mode == "clientFilter" && opDef.Pagination != "",
				ByField:      byField,
				SourceMethod: opDef.Name,
			}
			if r.Mode == "direct" {
				// Pre-expand the Go field chain for direct-mode emission.
				// Composite Classic resources return ID nested under General
				// (policies, mac_application, ebook, …); flat resources have a
				// top-level ID. The config's idField is a Go-struct dot-path —
				// "ID" (default) or "General.ID" for composites. Generator
				// emits per-step nil checks so the resolver surfaces a clean
				// "response missing id" error instead of nil-derefing.
				path := r.IDField
				if path == "" {
					path = "ID"
				}
				parts := strings.Split(path, ".")
				checks := []string{"r == nil"}
				var expr strings.Builder
				expr.WriteString("r")
				for _, p := range parts {
					expr.WriteString("." + p)
					checks = append(checks, expr.String()+" == nil")
				}
				gr.IDNilCheck = strings.Join(checks, " || ")
				gr.IDDeref = "*" + expr.String()
				xmlBody := "42"
				for _, part := range slices.Backward(parts) {
					tag := strings.ToLower(part)
					xmlBody = "<" + tag + ">" + xmlBody + "</" + tag + ">"
				}
				gr.IDTestInnerXML = xmlBody
			}
			// Base synthetic method — shared fields. ResourcePath/SpecPath
			// come from the source op: a List op for filtered/clientFilter
			// (no {name} placeholder), or the GetByName op for direct (which
			// does carry {name}; the test-stub handler resolves that to
			// "test-id" the same way typed GET stubs do).
			base := GoMethod{
				Namespace:        src.Namespace,
				Version:          src.Version,
				Tag:              src.Tag,
				Format:           src.Format,
				ResourcePath:     src.ResourcePath,
				SpecPath:         src.SpecPath,
				HTTPMethod:       http.MethodGet,
				ResponseWireName: src.ResponseWireName,
				Resolver:         gr,
			}
			idMethod := base
			idMethod.Name = "Resolve" + r.ResourceType + "ID" + byField
			typedMethod := base
			typedMethod.Name = "Resolve" + r.ResourceType + byField
			if r.Mode == "direct" {
				idMethod.Category = "resolverIDDirect"
				idMethod.Comment = idMethod.Name + " looks up a " + r.ResourceType + " by name via " + opDef.Name + " and returns its ID as a string. Returns an error when the underlying call returns a nil ID."
				typedMethod.Category = "resolverTypedDirect"
				typedMethod.Comment = typedMethod.Name + " looks up a " + r.ResourceType + " by name. Alias for " + opDef.Name + "; present so callers can use the same Resolve<X>ByName spelling across all resources regardless of resolver mode."
			} else {
				idMethod.Category = "resolverID"
				idMethod.Comment = idMethod.Name + " looks up a " + r.ResourceType + " by its " + r.NameField + " field and returns the ID. Returns *APIResponseError with HasStatus(404) when no match exists, or *AmbiguousMatchError when multiple resources share the name."
				typedMethod.Category = "resolverTyped"
				typedMethod.Comment = typedMethod.Name + " looks up a " + r.ResourceType + " by its " + r.NameField + " field and returns the decoded resource. Shares the same HTTP call as the ID-only variant; error semantics are identical."
			}

			out = append(out, idMethod, typedMethod)
		} // end for resolvers
	}
	return out, nil
}

// appendApplyMethods synthesizes an Apply<ResourceType> upsert method for
// each resolver that has an Apply config block. The Apply method:
//  1. Extracts the name from the request struct
//  2. Calls Resolve<ResourceType>IDByName
//  3. If 404: calls Create, returns (newID, true, nil)
//  4. If found: calls Update with the resolved ID, returns (existingID, false, nil)
//  5. Ambiguous/other errors propagate as-is
func appendApplyMethods(doc *openapi3.T, methods []GoMethod, spec SpecDef) ([]GoMethod, error) {
	byName := make(map[string]*GoMethod, len(methods))
	for i := range methods {
		byName[methods[i].Name] = &methods[i]
	}
	out := append([]GoMethod(nil), methods...)
	for _, opDef := range spec.Operations {
		var resolvers []ResolverConfig
		if opDef.Resolver != nil {
			resolvers = append(resolvers, *opDef.Resolver)
		}
		resolvers = append(resolvers, opDef.Resolvers...)
		for _, r := range resolvers {
			if r.Apply == nil {
				continue
			}
			ac := r.Apply
			if ac.CreateOp == "" || ac.UpdateOp == "" || ac.NameGoField == "" {
				return nil, fmt.Errorf("apply on %s/%s: createOp, updateOp, and nameGoField are required", opDef.Name, r.ResourceType)
			}
			createM, ok := byName[ac.CreateOp]
			if !ok {
				return nil, fmt.Errorf("apply on %s: createOp %q not found", r.ResourceType, ac.CreateOp)
			}
			updateM, ok := byName[ac.UpdateOp]
			if !ok {
				return nil, fmt.Errorf("apply on %s: updateOp %q not found", r.ResourceType, ac.UpdateOp)
			}
			if err := refuseHeaderParamsOnSynthetic("apply", r.ResourceType, ac.CreateOp, createM); err != nil {
				return nil, err
			}
			if err := refuseHeaderParamsOnSynthetic("apply", r.ResourceType, ac.UpdateOp, updateM); err != nil {
				return nil, err
			}
			// Determine request type from the Create method.
			requestType := createM.RequestType
			if requestType == "" {
				return nil, fmt.Errorf("apply on %s: createOp %q has no request type", r.ResourceType, ac.CreateOp)
			}
			// Determine how to extract ID from create response. Every branch
			// below assigns createReturnID before use, so no initializer.
			var createReturnID string
			if spec.Format == "xml" {
				// Classic: ID is *int
				createReturnID = `fmt.Sprintf("%d", *resp.ID)`
			} else {
				switch createM.ResponseType {
				case "HrefResponse":
					createReturnID = "resp.ID"
				default:
					// Non-HrefResponse: check if the response has a string or int ID.
					// The resolver's IDNumeric flag tells us.
					if r.IDNumeric {
						if r.IDPointer {
							createReturnID = `fmt.Sprintf("%d", *resp.ID)`
						} else {
							createReturnID = "strconv.Itoa(resp.ID)"
						}
					} else if r.IDPointer {
						createReturnID = "*resp.ID"
					} else {
						createReturnID = "resp.ID"
					}
				}
			}
			// Determine if Update returns a value or just error.
			updateReturnsVal := updateM.ResponseType != ""
			// Determine extra args from Create's query params.
			var extraArgs, extraCallArgs, extraTestCallArgs string
			for _, qp := range createM.QueryParams {
				goType := qp.Type
				if goType == "" {
					goType = "string"
				}
				extraArgs += ", " + qp.Go + " " + goType
				extraCallArgs += ", " + qp.Go
				// Literal zero value for test calls.
				switch goType {
				case "bool":
					extraTestCallArgs += ", false"
				case "int", "int64":
					extraTestCallArgs += ", 0"
				default:
					extraTestCallArgs += ", \"\""
				}
			}
			// Determine if this is a Classic create (takes id as first path param).
			classicCreate := spec.Format == "xml"
			// Determine whether the name field is a pointer on the generated
			// Go struct. XML specs always pointer-ify; for JSON specs we
			// check whether the field is in the schema's required list —
			// non-required scalars become pointers per schema.go logic.
			nameIsPointer := spec.Format == "xml"
			nameNested := strings.Contains(ac.NameGoField, ".")
			nameParentField := ""
			nameParentType := ""
			nameLeafField := ac.NameGoField

			// findSchema looks up a schema by Go type name. The schema map
			// is keyed by spec name (e.g. "ebook_post"), but we have the Go
			// name (e.g. "EbookPost"). Try direct lookup first, then iterate.
			findSchema := func(goName string) *openapi3.SchemaRef {
				if ref, ok := doc.Components.Schemas[goName]; ok {
					return ref
				}
				for specName, ref := range doc.Components.Schemas {
					if goTypeName(specName) == goName {
						return ref
					}
				}
				return nil
			}

			if nameNested {
				parts := strings.SplitN(ac.NameGoField, ".", 2)
				nameParentField = parts[0]
				nameLeafField = parts[1]
				// Resolve the parent field's Go type from the schema.
				if schemaRef := findSchema(requestType); schemaRef != nil && schemaRef.Value != nil {
					for pname, pref := range schemaRef.Value.Properties {
						if exportedGoName(pname) == nameParentField {
							if pref != nil && pref.Ref != "" {
								// Extract schema name from $ref like "#/components/schemas/PolicyGeneral"
								refParts := strings.Split(pref.Ref, "/")
								nameParentType = goTypeName(refParts[len(refParts)-1])
							} else if pref != nil && pref.Value != nil && pref.Value.Title != "" {
								nameParentType = goTypeName(pref.Value.Title)
							}
							break
						}
					}
				}
			}
			if !nameIsPointer && !nameNested {
				if schemaRef := findSchema(requestType); schemaRef != nil && schemaRef.Value != nil {
					// Map Go field name → JSON property name via the same
					// exportedGoName conversion the schema emitter uses.
					nameJSONField := ""
					for pname := range schemaRef.Value.Properties {
						if exportedGoName(pname) == ac.NameGoField {
							nameJSONField = pname
							break
						}
					}
					if nameJSONField != "" {
						isRequired := slices.Contains(schemaRef.Value.Required, nameJSONField)
						if !isRequired {
							nameIsPointer = true
						}
					}
				}
			}
			// Resolver method name — typically ByName, but uses the
			// resolver's byField suffix when present (e.g. BySerialNumber).
			byField := "ByName"
			if r.ByField != "" {
				byField = r.ByField
			}
			resolverMethod := "Resolve" + r.ResourceType + "ID" + byField
			// Delete method (for test generation).
			deleteMethod := ac.DeleteOp

			// Inherit tag/namespace/version from the source list operation.
			src := byName[opDef.Name]
			if src == nil {
				return nil, fmt.Errorf("apply on %s: source op %q not found", r.ResourceType, opDef.Name)
			}

			ga := &GoApply{
				ResourceType:      r.ResourceType,
				RequestType:       requestType,
				NameGoField:       ac.NameGoField,
				NameParentField:   nameParentField,
				NameParentType:    nameParentType,
				NameLeafField:     nameLeafField,
				ResolverMethod:    resolverMethod,
				CreateMethod:      ac.CreateOp,
				UpdateMethod:      ac.UpdateOp,
				DeleteMethod:      deleteMethod,
				CreateReturnID:    createReturnID,
				IDNumeric:         r.IDNumeric,
				UpdateReturnsVal:  updateReturnsVal,
				ExtraArgs:         extraArgs,
				ExtraCallArgs:     extraCallArgs,
				ExtraTestCallArgs: extraTestCallArgs,
				ClassicCreate:     classicCreate,
				NameIsPointer:     nameIsPointer,
				NameNested:        nameNested,
				// Test generation paths.
				ListNamespace:    src.Namespace,
				ListVersion:      src.Version,
				ListPath:         src.ResourcePath,
				ListNameField:    r.NameField,
				ListIDField:      r.IDField,
				ListResultsField: r.ResultsField,
				CreateNS:         createM.Namespace,
				CreateVer:        createM.Version,
				CreatePath:       createM.ResourcePath,
				CreateStatus:     createM.ExpectedStatus,
				UpdateNS:         updateM.Namespace,
				UpdateVer:        updateM.Version,
				UpdatePath:       updateM.ResourcePath,
				UpdateStatus:     updateM.ExpectedStatus,
			}
			// Check if list and create share the same URL (both path and
			// namespace/version match). When true, the test template must
			// register a single mux handler that dispatches on HTTP method
			// instead of two separate handlers that would panic.
			ga.SameListCreatePath = ga.ListNamespace == ga.CreateNS &&
				ga.ListVersion == ga.CreateVer &&
				ga.ListPath == ga.CreatePath

			// UpdateType: when the update operation takes a different Go
			// type than the create operation, record it for template use.
			// The template will JSON-marshal the create request and unmarshal
			// into the update type on the update path.
			if ac.UpdateType != "" {
				ga.UpdateType = ac.UpdateType
				ga.HasUpdateType = true
			}

			// VersionLock: optimistic locking for prestages. On create,
			// zero all VersionLock fields recursively. On update, GET the
			// current resource to extract VersionLock values, then inject
			// them into the update request.
			if ac.VersionLock {
				if ac.GetOp == "" {
					return nil, fmt.Errorf("apply on %s: versionLock requires getOp", r.ResourceType)
				}
				getM, ok := byName[ac.GetOp]
				if !ok {
					return nil, fmt.Errorf("apply on %s: getOp %q not found", r.ResourceType, ac.GetOp)
				}
				ga.VersionLock = true
				ga.GetMethod = ac.GetOp
				ga.GetNS = getM.Namespace
				ga.GetVer = getM.Version
				ga.GetPath = getM.ResourcePath
				ga.GetType = getM.ResponseType
				// Check if GET and Update share the same URL path.
				ga.SameGetUpdatePath = ga.GetNS == ga.UpdateNS &&
					ga.GetVer == ga.UpdateVer &&
					ga.GetPath == updateM.ResourcePath
			}

			// Token-upload mode: wire up the upload/replace operations.
			if ac.TokenUploadMode {
				if ac.TokenUploadCreateOp == "" || ac.TokenReplaceOp == "" {
					return nil, fmt.Errorf("apply on %s: tokenUploadMode requires tokenUploadCreateOp and tokenReplaceOp", r.ResourceType)
				}
				uploadM, ok := byName[ac.TokenUploadCreateOp]
				if !ok {
					return nil, fmt.Errorf("apply on %s: tokenUploadCreateOp %q not found", r.ResourceType, ac.TokenUploadCreateOp)
				}
				replaceM, ok := byName[ac.TokenReplaceOp]
				if !ok {
					return nil, fmt.Errorf("apply on %s: tokenReplaceOp %q not found", r.ResourceType, ac.TokenReplaceOp)
				}
				ga.TokenUploadMode = true
				ga.TokenUploadMethod = ac.TokenUploadCreateOp
				ga.TokenReplaceMethod = ac.TokenReplaceOp
				ga.TokenRequestType = uploadM.RequestType
				ga.TokenUploadNS = uploadM.Namespace
				ga.TokenUploadVer = uploadM.Version
				ga.TokenUploadPath = uploadM.ResourcePath
				ga.TokenReplaceNS = replaceM.Namespace
				ga.TokenReplaceVer = replaceM.Version
				ga.TokenReplacePath = replaceM.ResourcePath
				// Token-upload Apply always takes an extra `token string` arg.
				ga.ExtraArgs = ", token string"
				ga.ExtraCallArgs = ""                 // token is used inline, not forwarded to create
				ga.ExtraTestCallArgs = `, "dGVzdA=="` // base64("test") — valid token for unit tests
				// In token-upload mode, the Apply method's request type is the
				// update op's type (the metadata), not the create op's (the token).
				ga.RequestType = updateM.RequestType
			}

			// Membership pre-fetch mode: fetch current membership before patch.
			if ac.MembershipPreFetch != nil {
				mpf := ac.MembershipPreFetch
				fetchM, ok := byName[mpf.FetchOp]
				if !ok {
					return nil, fmt.Errorf("apply on %s: membershipPreFetch fetchOp %q not found", r.ResourceType, mpf.FetchOp)
				}
				// Build zero-value extra call args from the fetch op's query params.
				var extraFetchArgs strings.Builder
				for _, qp := range fetchM.QueryParams {
					extraFetchArgs.WriteString(", ")
					switch {
					case strings.HasPrefix(qp.Type, "[]"):
						extraFetchArgs.WriteString("nil")
					case qp.Type == "bool":
						extraFetchArgs.WriteString("false")
					case qp.Type == "int" || qp.Type == "int64":
						extraFetchArgs.WriteString("0")
					default:
						extraFetchArgs.WriteString(`""`)
					}
				}
				ga.MembershipPreFetch = true
				ga.MembershipFetchMethod = mpf.FetchOp
				ga.MembershipFetchExtraArgs = extraFetchArgs.String()
				ga.MembershipFetchNS = fetchM.Namespace
				ga.MembershipFetchVer = fetchM.Version
				ga.MembershipFetchPath = fetchM.ResourcePath
				ga.MembershipSourceIDField = mpf.SourceIDField
				ga.MembershipAssignmentType = mpf.AssignmentType
				ga.MembershipAssignmentIDField = mpf.AssignmentIDField
				ga.MembershipRequestField = mpf.RequestField
				ga.MembershipRequestFieldIsPtr = mpf.AssignmentFieldIsPtr
			}

			// Classic test stubs need XML wire names for resolver and create responses.
			if classicCreate {
				ga.ClassicResolverWireName = src.ResponseWireName
				ga.ClassicCreateWireName = createM.ResponseWireName
				// Compute the inner XML for the ID field in the resolver response.
				// Mirrors the logic in the resolver's IDTestInnerXML computation.
				idPath := r.IDField
				if idPath == "" {
					idPath = "ID"
				}
				idParts := strings.Split(idPath, ".")
				idXML := "42"
				for _, idPart := range slices.Backward(idParts) {
					tag := strings.ToLower(idPart)
					idXML = "<" + tag + ">" + idXML + "</" + tag + ">"
				}
				ga.ClassicResolverIDInnerXML = idXML
			}

			m := GoMethod{
				Name:      "Apply" + r.ResourceType,
				Category:  "apply",
				Comment:   "Apply" + r.ResourceType + " creates or updates a " + r.ResourceType + " by name. If a resource with the specified name exists, it is updated; if not found, a new resource is created. Returns the resource ID, whether it was created (true) or updated (false), and any error. An *AmbiguousMatchError is returned if multiple resources match the name.",
				Tag:       src.Tag,
				Namespace: src.Namespace,
				Version:   src.Version,
				Format:    src.Format,
				Apply:     ga,
			}
			out = append(out, m)
		}
	}
	return out, nil
}

// extractMultipartFields walks a multipart/form-data request body schema's
// properties and returns a list of multipart fields for generator use.
// Binary fields (format: binary) become file uploads; other scalars become
// string form fields.
func extractMultipartFields(schema *openapi3.Schema) []GoMultipartField {
	if schema == nil {
		return nil
	}
	fields := make([]GoMultipartField, 0, len(schema.Properties))
	for _, name := range sortedKeys(schema.Properties) {
		prop := schema.Properties[name].Value
		// A field is a file upload when the spec marks it as such
		// (string + format: binary) OR — as a fallback for spec bugs —
		// when the field is conventionally named "file" with string
		// type but no format. The Jamf Pro spec has at least two
		// endpoints (/v2/inventory-preload/csv, csv-validate) where
		// the author omitted format: binary, and treating those as a
		// plain form string would emit a callable signature that
		// doesn't actually accept a file. Path-name heuristic is safe
		// because any real JSON form field named "file" would be a
		// genuine file upload semantically.
		isFile := prop != nil && prop.Type != nil && prop.Type.Is("string") &&
			(prop.Format == "binary" || name == "file")
		f := GoMultipartField{
			Name:   name,
			GoName: toLowerCamelCase(name),
			IsFile: isFile,
		}
		if !isFile && prop != nil {
			f.Type = schemaRefToGoType(schema.Properties[name])
		}
		fields = append(fields, f)
	}
	return fields
}

// sortedContentEntries iterates a content map in a deterministic order so
// generator output doesn't flip on map iteration randomness.
func sortedContentEntries(content map[string]*openapi3.MediaType) func(yield func(string, *openapi3.MediaType) bool) {
	return func(yield func(string, *openapi3.MediaType) bool) {
		keys := make([]string, 0, len(content))
		for k := range content {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if !yield(k, content[k]) {
				return
			}
		}
	}
}

// deprecationDate returns the value of the x-deprecation-date vendor
// extension on op normalised to YYYY-MM-DD, or "" when absent. Specs in
// this repo use either bare dates (pro: "2025-06-30") or ISO datetimes
// (classic: "2025-02-11T00:00:00.000Z"); both render as YYYY-MM-DD in
// the godoc. kin-openapi stores vendor extensions as raw JSON bytes
// keyed by the extension name.
func deprecationDate(op *openapi3.Operation) string {
	if op == nil {
		return ""
	}
	raw, ok := op.Extensions["x-deprecation-date"]
	if !ok {
		return ""
	}
	var s string
	switch v := raw.(type) {
	case string:
		s = v
	case []byte:
		s = strings.Trim(string(v), `"`)
	case json.RawMessage:
		s = strings.Trim(string(v), `"`)
	default:
		s = strings.Trim(fmt.Sprintf("%s", v), `"`)
	}
	if idx := strings.IndexAny(s, "T "); idx >= 0 {
		s = s[:idx]
	}
	return s
}

// isRateLimited reports whether the operation carries x-rate-limit: true.
// kin-openapi stores vendor extensions as raw JSON bytes keyed by the
// extension name.
func isRateLimited(op *openapi3.Operation) bool {
	if op == nil {
		return false
	}
	raw, ok := op.Extensions["x-rate-limit"]
	if !ok {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case []byte:
		return string(v) == "true"
	case string:
		return v == "true"
	default:
		// Some kin-openapi versions return json.RawMessage.
		s := fmt.Sprintf("%s", v)
		return s == "true"
	}
}

// stringSliceExtension decodes a vendor extension whose value is a JSON array
// of strings (e.g. x-required-privileges). kin-openapi stores extensions as
// raw JSON bytes keyed by the extension name; the value may already be a
// decoded []interface{} on some versions, so handle both. Returns nil when
// the extension is absent or malformed — privileges are advisory metadata, so
// a decode failure degrades to "unknown" rather than aborting generation.
func stringSliceExtension(op *openapi3.Operation, key string) []string {
	if op == nil {
		return nil
	}
	raw, ok := op.Extensions[key]
	if !ok {
		return nil
	}
	var out []string
	switch v := raw.(type) {
	case []string:
		out = append(out, v...)
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
	case json.RawMessage:
		_ = json.Unmarshal(v, &out)
	case []byte:
		_ = json.Unmarshal(v, &out)
	case string:
		_ = json.Unmarshal([]byte(v), &out)
	}
	return out
}

// privilegeComment renders the godoc line listing the privileges a method
// requires, mirroring the deprecation/rate-limit lines. Returns "" when the
// method carries no privilege metadata (synthetic methods). Spec-derived
// methods with no declared privileges report "none" so the absence is
// explicit rather than ambiguous.
func privilegeComment(m GoMethod) string {
	if !m.PrivilegesKnown {
		return ""
	}
	if len(m.ScopedPrivileges) == 0 {
		return "\n//\n// Required privileges: the spec declares none."
	}
	if m.PrivilegeSource == privilegeSourceGatewayPolicy {
		line := "\n//\n// Required privileges: " + strings.Join(m.ScopedPrivileges, ", ") + "."
		if len(m.ScopedPrivileges) > 1 {
			line += "\n// All of them are required, not alternatives."
		}
		return line + "\n// The published spec declares none for this operation; these are the" +
			"\n// capabilities the gateway's own authorization policy enforces. See" +
			"\n// Privileges in this package for the provenance."
	}
	line := "\n//\n// Required privileges: " + strings.Join(m.ScopedPrivileges, ", ") + "."
	if len(m.LegacyPrivileges) > 0 {
		line += " Legacy Jamf Pro privilege name(s): " + strings.Join(m.LegacyPrivileges, ", ") + "."
	}
	if len(m.ScopedPrivileges) > 1 {
		line += "\n// All of them are required, not alternatives."
	}
	// Say so wherever a reader could mistake the two lists for parallel arrays.
	// They are independent sets: the spec's own ordering differs between them
	// and the lengths need not match. See privilegeSetsAreNotPairs.
	if len(m.LegacyPrivileges) > 0 && (len(m.ScopedPrivileges) > 1 || len(m.LegacyPrivileges) > 1) {
		line += "\n// The scoped and legacy lists are independent sets, not pairs: do not match them by position."
	}
	return line
}

// parameterDocWidth is the wrap width for parameter documentation text; the
// rendered line prefix is "//     " (7 columns), so emitted lines top out at
// 107. Wider than an 80-column terminal on purpose: several Pro list
// endpoints inline their whole sortable field set, and at 76 those blocks ran
// to ~76 lines each. 100 costs ~17% fewer lines with nothing dropped.
// Comments elsewhere in the generated tree are far longer still — 1711 lines
// already exceed 100 columns — so this stays the narrowest prose we emit.
const parameterDocWidth = 100

// defaultMethodComment is the leading godoc sentence used when the spec gives
// an operation no summary but the generator still has metadata to append.
// The undocumented-spec wording matches the pass in extractMethods so the two
// cannot drift apart.
func defaultMethodComment(name string, spec SpecDef) string {
	if spec.Undocumented {
		return name + " calls an undocumented Jamf endpoint."
	}
	return name + " calls a Jamf Platform API endpoint."
}

// collectSpecParams keys an operation's spec parameter objects by wire name.
// It is the single point at which a config-declared param is matched to what
// the spec says about it: both resolveQueryParams (behaviour) and
// parameterComment (documentation) read the map this returns, so the two
// cannot disagree about which spec parameter a config entry refers to.
func collectSpecParams(pathItem *openapi3.PathItem, op *openapi3.Operation) map[string]*openapi3.Parameter {
	specParams := make(map[string]*openapi3.Parameter)
	collect := func(params openapi3.Parameters) {
		for _, ref := range params {
			if ref == nil || ref.Value == nil || ref.Value.Name == "" {
				continue
			}
			// An operation-level parameter overriding a path-level one keeps
			// the override's schema, but not a placeholder description: the
			// Platform specs declare `id` twice per path — once at path level
			// as a $ref with real prose ("The ID of the device, in UUID
			// format"), once inline on the operation with an autogenerated
			// "Path parameter id". Only the documentation is affected; the
			// signature is config-declared either way.
			if prev, ok := specParams[ref.Value.Name]; ok &&
				prev.Description != "" && isPlaceholderParamDoc(ref.Value) {
				continue
			}
			specParams[ref.Value.Name] = ref.Value
		}
	}
	// Operation-level parameters override same-named path-level ones per the
	// OpenAPI spec, so they are collected second.
	if pathItem != nil {
		collect(pathItem.Parameters)
	}
	if op != nil {
		collect(op.Parameters)
	}
	return specParams
}

// resolveQueryParams cross-checks every config-declared query param against
// the spec and records on the ExtraParam what the spec says about it. Two
// things come out of this one resolution, and they are here together
// deliberately — the first check shipped alone and the second was the gap it
// left:
//
//   - The name-match check. config.json's "params" entries hand-type the wire
//     query-parameter name as a literal string with nothing else
//     cross-checking it against the spec. If Jamf renames or drops the
//     parameter, or the entry was simply mistyped, the generated method keeps
//     compiling and keeps sending a query key the server silently ignores —
//     the GetBaselineRules baselineId/baseline-id incident. Caught at generate
//     time, unless the entry opts out via ":undocumented" (a param that is
//     wire-verified to work but that the spec doesn't declare at all).
//
//   - AlwaysSend. Every query param used to be emitted behind a zero-value
//     guard, spec-required ones included, so a caller passing "" for a
//     required param sent a request with it silently omitted and got back a
//     400 whose wording reads like a server or auth fault rather than a
//     caller error. Required params are emitted unguarded instead. Deriving
//     the flag here rather than accepting a ":required" suffix in config.json
//     is what keeps it correct across ingests: required-ness lives in the
//     spec, so a second declaration of it would rot the first time a bundle
//     flips a param from optional to required.
//
// An ":undocumented" param has no spec parameter to read, so it keeps its
// guard — that is the only safe emission for something the spec is silent
// about.
func resolveQueryParams(m *GoMethod, specParams map[string]*openapi3.Parameter) error {
	var unmatched []string
	for i := range m.QueryParams {
		q := &m.QueryParams[i]
		sp, ok := specParams[q.Spec]
		if !ok {
			if !q.Undocumented {
				unmatched = append(unmatched, q.Spec)
			}
			continue
		}
		if sp.In != "" && sp.In != openapi3.ParameterInQuery {
			return fmt.Errorf("%s %s: config declares %q under \"params\" but the spec says in: %s — a header parameter emitted as a query key is silently ignored by the server; move it to \"headerParams\"",
				m.HTTPMethod, m.SpecPath, q.Spec, sp.In)
		}
		q.AlwaysSend = sp.Required && !hasSpecDefault(sp.Schema)
	}
	if len(unmatched) > 0 {
		known := make([]string, 0, len(specParams))
		for name := range specParams {
			known = append(known, name)
		}
		sort.Strings(known)
		return fmt.Errorf("%s %s: config declares query param(s) %v not found in spec (spec declares: %v) — the spec may have renamed or removed it, fix config.json; if this is a wire-verified param the spec genuinely omits, mark the entry \":undocumented\"",
			m.HTTPMethod, m.SpecPath, unmatched, known)
	}
	return nil
}

// generatorReservedHeaders are request headers the transport owns, which a
// generated method must never take as an argument.
//
// The duplication of internal/client's scope headers is deliberate:
// tools/generate is its own module and importing the SDK into its own
// generator would make the build circular.
//
// TestGeneratorReservedHeadersPinScopeHeaders pins it anyway, by parsing
// ScopeKind.ScopeHeader() out of internal/client/client.go and requiring every
// header it can return to appear here. That is a pin against the source of
// truth rather than against internal/client's own reservedHeaders, which is
// itself derived from ScopeHeader — so a new scope kind fails a test here
// rather than emitting a method that can overwrite the scope.
//
// Authorization is the one entry with no counterpart in that switch: the
// transport owns it through oauth2 and WithAuthorizationHeaderName, not
// through a scope. So this map is a superset of the scope headers, never a
// mirror of internal/client's map.
//
// The scope headers are the load-bearing entries. setScopeHeader stamps them
// from the client's scope and doRequestFull applies extraHeaders *after* it, so
// a generated argument would win — and a wrong scope value is
// 403 OWNERSHIP_FORBIDDEN, which is close to undiagnosable.
var generatorReservedHeaders = map[string]bool{
	"X-Tenant-Id":      true,
	"X-Environment-Id": true,
	"Authorization":    true,
}

// resolveHeaderParams cross-checks every config-declared header param against
// the spec, the way resolveQueryParams does for query params, and refuses four
// things rather than emitting something that cannot work:
//
//   - a name the spec does not declare, unless the entry opts out with
//     ":undocumented";
//   - a name the spec declares somewhere other than `in: header` — the mirror
//     of the guard resolveQueryParams now carries, so neither key can borrow
//     the other's parameters;
//   - a header the transport owns (see generatorReservedHeaders);
//   - any Go type but string. No spec declares a non-string header, and the
//     wire encoding of a repeated or numeric one is a guess: comma-joining a
//     []string here would be inventing a serialisation the spec never stated.
//     Failing is recoverable; guessing ships a request the server misreads.
//
// AlwaysSend is derived from the spec exactly as it is for query params,
// including the hasSpecDefault subtraction: a required header travels
// unguarded, so a caller passing "" gets the server's own complaint rather
// than a request that silently omits it — unless the spec gives the parameter
// a default, which gives its absence a defined meaning and so keeps the
// guard. No header declares a default today, so the subtraction is inert; it
// is here because the divergence would be silent when one does, and the rule
// it mirrors is documented on hasSpecDefault itself.
func resolveHeaderParams(m *GoMethod, specParams map[string]*openapi3.Parameter) error {
	var unmatched []string
	for i := range m.HeaderParams {
		h := &m.HeaderParams[i]
		if h.Type != "string" {
			return fmt.Errorf("%s %s: header param %q is declared as %q — only string is supported, because the wire encoding of any other type is unstated by the spec",
				m.HTTPMethod, m.SpecPath, h.Spec, h.Type)
		}
		if generatorReservedHeaders[http.CanonicalHeaderKey(h.Spec)] {
			return fmt.Errorf("%s %s: header param %q is stamped by the transport and must not be a method argument — remove it from \"headerParams\"",
				m.HTTPMethod, m.SpecPath, h.Spec)
		}
		sp, ok := specParams[h.Spec]
		if !ok {
			if !h.Undocumented {
				unmatched = append(unmatched, h.Spec)
			}
			continue
		}
		if sp.In != openapi3.ParameterInHeader {
			return fmt.Errorf("%s %s: config declares %q under \"headerParams\" but the spec says in: %s — move it to \"params\"",
				m.HTTPMethod, m.SpecPath, h.Spec, sp.In)
		}
		h.AlwaysSend = sp.Required && !hasSpecDefault(sp.Schema)
	}
	if len(unmatched) > 0 {
		known := make([]string, 0, len(specParams))
		for name, sp := range specParams {
			if sp.In == openapi3.ParameterInHeader {
				known = append(known, name)
			}
		}
		sort.Strings(known)
		return fmt.Errorf("%s %s: config declares header param(s) %v not found in spec (spec declares header params: %v) — fix config.json, or mark the entry \":undocumented\" if it is wire-verified and the spec omits it",
			m.HTTPMethod, m.SpecPath, unmatched, known)
	}
	return nil
}

// hasSpecDefault reports whether a parameter schema declares a default that
// gives its own absence a defined meaning. Such a parameter keeps the
// zero-value guard even when the spec marks it required, and the reasoning is
// the mirror image of the guard's removal rather than an exception to it:
// dropping a required param is wrong because the server can do nothing
// sensible without it, but where the spec publishes a default the server can
// and does — so sending an empty value replaces a documented default with
// nothing, which is the same harm pointed the other way.
//
// OpenAPI forbids the combination ("default SHALL NOT be used with required"),
// so any parameter reaching the true branch here is a malformed declaration
// and the choice of which half to follow is evidence-led, not textual. Pro's
// `columns-to-export` on GET /v3/patch-software-title-configurations/{id}/export-report
// is the only live case — required: true with a nine-column default — and the
// wire settles it. Probed 2026-09-01 against a config whose patch report has
// zero rows, so the baseline answer is 400 either way: omitting the parameter
// and passing a valid two-column list both return that same 400, while
// `columns-to-export=` returns **500**. Sending the empty value is strictly
// worse than omitting it, which is the opposite of every other required param
// here. See WIRE-FACTS.md.
//
// If a bundle ever drops that default, this stops applying to the param and it
// starts travelling unguarded on the next generate — the correct response to
// the spec no longer defining its absence.
//
// An empty default (`""`, `[]`) states nothing and does not count.
func hasSpecDefault(ref *openapi3.SchemaRef) bool {
	if ref == nil || ref.Value == nil || ref.Value.Default == nil {
		return false
	}
	switch d := ref.Value.Default.(type) {
	case string:
		return d != ""
	case []any:
		return len(d) > 0
	case map[string]any:
		return len(d) > 0
	}
	return true
}

// parameterComment renders a godoc block documenting the parameters a method
// takes, sourced from the spec's parameter objects (see collectSpecParams).
// Ordering follows the Go signature — path params first, then the
// config-declared query params, then the header params — so the block reads
// against the call the consumer is writing. Without it the only place a caller can learn which
// fields a `filter` or `sort` argument accepts is the raw spec: those RSQL
// field lists live in the parameter description and nowhere else in the SDK.
//
// Documentation only: parameter types stay exactly as config declares them.
// That is what makes it safe to quote enum values verbatim — Jamf's Classic
// path enums include values that are unusable as Go identifiers
// ("Pending+Failed", "EnableRemoteDesktop (macOS 10.14.4 and later)") and one
// outright typo ("Hardwre"), all of which are still what the server accepts.
//
// Params the spec doesn't describe are skipped. Returns "" when nothing is
// documentable.
func parameterComment(m GoMethod, specParams map[string]*openapi3.Parameter, enumTypes map[string]bool) string {
	if len(specParams) == 0 {
		return ""
	}

	type docParam struct{ goName, specName string }
	ordered := make([]docParam, 0, len(m.PathParams)+len(m.QueryParams)+len(m.HeaderParams))
	for _, p := range m.PathParams {
		ordered = append(ordered, docParam{goName: p.GoName, specName: p.SpecName})
	}
	for _, q := range m.QueryParams {
		ordered = append(ordered, docParam{goName: q.Go, specName: q.Spec})
	}
	for _, h := range m.HeaderParams {
		ordered = append(ordered, docParam{goName: h.Go, specName: h.Spec})
	}

	var lines []string
	for _, p := range ordered {
		sp, ok := specParams[p.specName]
		if !ok {
			continue
		}
		body := parameterDocLines(sp, enumTypes)
		if extra := acceptHeaderDocLines(sp, m.ProducesMediaTypes); len(extra) > 0 {
			body = append(body, extra...)
		}
		// Last, so it is the line the reader ends on: it is the one thing
		// here that changes whether the call works at all.
		if extra := wireRequiredDocLines(p.specName, m); len(extra) > 0 {
			body = append(body, extra...)
		}
		if len(body) == 0 {
			continue
		}
		lines = append(lines, "//   - "+p.goName+": "+body[0])
		for _, cont := range body[1:] {
			lines = append(lines, "//     "+cont)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n//\n// Parameters:\n" + strings.Join(lines, "\n")
}

// resolveWireRequiredParams validates config's WireRequiredParams against the
// spec and records them on the method for godoc.
//
// Two refusals, and both are what make the key self-expiring rather than a
// note that outlives its own truth:
//
//   - a name the spec does not declare. The same typo guard resolveQueryParams
//     and resolveHeaderParams carry, for the same reason: a note keyed to a
//     renamed parameter silently documents nothing.
//   - a parameter the spec now marks required. The key's whole content is
//     "the spec says optional and the server disagrees", so once the spec
//     agrees the note is restating it, and the emitted line would tell a
//     reader something the parameter's own declaration already says.
func resolveWireRequiredParams(m *GoMethod, names []string, specParams map[string]*openapi3.Parameter) error {
	if len(names) == 0 {
		return nil
	}
	m.WireRequiredParams = make(map[string]bool, len(names))
	for _, name := range names {
		sp, ok := specParams[name]
		if !ok {
			known := make([]string, 0, len(specParams))
			for n := range specParams {
				known = append(known, n)
			}
			sort.Strings(known)
			return fmt.Errorf("%s %s: \"wireRequiredParams\" names %q, which the spec does not declare (spec declares: %v) — fix the name or drop the entry",
				m.HTTPMethod, m.SpecPath, name, known)
		}
		if sp.Required {
			return fmt.Errorf("%s %s: \"wireRequiredParams\" names %q but the spec now marks it required — delete the entry, the parameter's own declaration says it",
				m.HTTPMethod, m.SpecPath, name)
		}
		m.WireRequiredParams[name] = true
	}
	return nil
}

// wireRequiredDocLines renders the note for a parameter the server refuses the
// request without. Deliberately says "required" plainly and names the status,
// because the signature says optional and the spec says optional, so anything
// softer loses to both.
func wireRequiredDocLines(specName string, m GoMethod) []string {
	if !m.WireRequiredParams[specName] {
		return nil
	}
	return wrapCommentText("Required in practice: the server answers 400 when this is omitted, although the spec marks the parameter optional. Passing the zero value omits it.", parameterDocWidth)
}

// acceptHeaderDocLines documents an Accept header param's allowed values from
// the operation's own success-response content types.
//
// This is the one header whose vocabulary a spec can state completely while
// its parameter declaration says nothing: OpenAPI models acceptable media
// types on the response, so `responses.200.content` *is* the enum and the
// parameter beside it is a bare string. Jamf Pro's
// GET /v3/patch-software-title-configurations/{id}/export-report is the live
// case — it produces text/csv or text/tab, answers 400 for anything else
// (not 415: wire-verified 2026-09-09 on application/pdf and text/plain, with
// the error body's own serialisation following Accept), and describes its
// `accept` parameter as "File." A caller reading only the signature had no way
// to learn that text/tab exists.
//
// It defers to a real enum: if a bundle ever gives the parameter one,
// parameterDocLines has already listed those values and this adds nothing. It
// also stays silent when the response declares a single content type, where
// the header cannot change anything worth documenting.
func acceptHeaderDocLines(sp *openapi3.Parameter, produces []string) []string {
	if sp == nil || sp.In != openapi3.ParameterInHeader || http.CanonicalHeaderKey(sp.Name) != "Accept" {
		return nil
	}
	if len(parameterEnumValues(sp.Schema)) > 0 || len(produces) < 2 {
		return nil
	}
	return wrapCommentText("Allowed values, from the operation's declared response content types: "+strings.Join(produces, ", ")+".", parameterDocWidth)
}

// isPlaceholderParamDoc reports whether a parameter's description says nothing
// the parameter's own name doesn't already say — "Path parameter id",
// "Query parameter sort", or just the bare name. Such a description loses to a
// real one when both a path-level and an operation-level declaration exist for
// the same parameter. A parameter carrying enum values is never a placeholder:
// the values are documentation regardless of the prose.
func isPlaceholderParamDoc(p *openapi3.Parameter) bool {
	if p.Schema != nil && p.Schema.Value != nil && len(p.Schema.Value.Enum) > 0 {
		return false
	}
	d := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(p.Description), ".")))
	name := strings.ToLower(p.Name)
	if d == "" || d == name {
		return true
	}
	for _, in := range []string{"path", "query", "header", "cookie"} {
		if d == in+" parameter "+name {
			return true
		}
	}
	return false
}

// parameterDocLines returns the wrapped godoc lines for one spec parameter:
// its description, then its allowed values when the schema constrains them.
// Returns nil when the spec says nothing useful about it.
//
// When the constraint comes from a named schema the generator emits as a type
// (enumTypes), the values are named rather than listed: the type carries a
// constant per value, and godoc groups those under it. That keeps the 60-plus
// value enums out of every call site's doc comment while still naming them
// somewhere the compiler checks.
func parameterDocLines(p *openapi3.Parameter, enumTypes map[string]bool) []string {
	out := docParagraphs(p.Description, parameterDocWidth)
	if t := enumRefTypeName(p.Schema); t != "" && enumTypes[t] {
		out = append(out, wrapCommentText("Allowed values: see the "+t+" constants.", parameterDocWidth)...)
		return out
	}
	if vals := parameterEnumValues(p.Schema); len(vals) > 0 {
		out = append(out, wrapCommentText("Allowed values: "+strings.Join(vals, ", ")+".", parameterDocWidth)...)
	}
	return out
}

// enumRefTypeName returns the Go type name a parameter schema $refs, following
// the item schema for repeatable params. Empty when the schema is inline —
// an inline enum has no type to point at, so its values are listed instead.
func enumRefTypeName(ref *openapi3.SchemaRef) string {
	if ref == nil {
		return ""
	}
	if ref.Ref != "" {
		return goTypeName(path.Base(ref.Ref))
	}
	if ref.Value != nil && ref.Value.Items != nil && ref.Value.Items.Ref != "" {
		return goTypeName(path.Base(ref.Value.Items.Ref))
	}
	return ""
}

// parameterEnumValues formats a parameter schema's enum for godoc. Repeatable
// params (e.g. sort) model the constraint on the item schema, so fall through
// to Items when the top-level schema carries no enum. Strings are quoted
// because Jamf's Classic enums contain spaces and parentheses; other scalars
// print bare.
func parameterEnumValues(ref *openapi3.SchemaRef) []string {
	if ref == nil || ref.Value == nil {
		return nil
	}
	enum := ref.Value.Enum
	if len(enum) == 0 && ref.Value.Items != nil && ref.Value.Items.Value != nil {
		enum = ref.Value.Items.Value.Enum
	}
	out := make([]string, 0, len(enum))
	for _, v := range enum {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			out = append(out, strconv.Quote(s))
			continue
		}
		out = append(out, fmt.Sprint(v))
	}
	return out
}

func buildMethod(doc *openapi3.T, spec SpecDef, opDef OperationDef, enumTypes map[string]bool) (GoMethod, error) {
	httpMethod, specPath := opDef.parseOp()

	pathItem := doc.Paths.Find(specPath)
	if pathItem == nil {
		return GoMethod{}, fmt.Errorf("path %s not found in spec", specPath)
	}
	op := pathItem.GetOperation(httpMethod)
	if op == nil {
		return GoMethod{}, fmt.Errorf("%s not found", opDef.Op)
	}

	// Version: operation override > extract from path > spec-level default
	version := coalesce(opDef.Version, coalesce(extractVersion(specPath), spec.Version))

	m := GoMethod{
		Name:            opDef.Name,
		Format:          spec.Format,
		HTTPMethod:      httpMethod,
		Namespace:       spec.Namespace,
		Version:         version,
		ResourcePath:    stripTenantPathSegment(stripVersionPrefix(specPath)),
		QueryParams:     opDef.parseParams(),
		HeaderParams:    opDef.parseHeaderParams(),
		ContentType:     opDef.ContentType,
		PaginationStyle: opDef.Pagination,
		PageSizeParam:   coalesce(opDef.PageSizeParam, "page-size"),
		MaxPageSize:     coalesceInt(opDef.MaxPageSize, defaultMaxPageSize(spec.Package, opDef.Pagination)),
		ResultsField:    coalesce(opDef.ResultsField, "results"),
		CursorField:     coalesce(opDef.CursorField, "nextCursor"),
		CursorParam:     coalesce(opDef.CursorParam, "cursor"),
		SpecPath:        specPath,
		UnwrapResults:   opDef.UnwrapResults,
		NoRetry:         opDef.NoRetry,
	}

	if op.Summary != "" {
		m.Comment = opDef.Name + " " + lowerFirst(cleanComment(op.Summary))
	}

	if len(op.Tags) > 0 {
		m.Tag = op.Tags[0]
		if renamed, ok := spec.TagRenames[m.Tag]; ok {
			m.Tag = renamed
		}
	}

	if isRateLimited(op) {
		if m.Comment != "" {
			m.Comment += "\n//\n// This endpoint is rate-limited. The transport automatically retries a 429 with backoff (honoring a server-supplied Retry-After when present, clamped to a ceiling), giving up only after exhausting its retry budget — at which point the 429 surfaces as an APIResponseError so the caller can apply its own backoff policy."
		} else {
			m.Comment = opDef.Name + " is rate-limited."
		}
	}

	if opDef.NoRetry {
		if m.Comment != "" {
			m.Comment += "\n//\n// This endpoint requires an optimistic-lock precondition in its request body, sourced from a prior GET. The transport does NOT auto-retry a 5xx here — unlike other PUT/DELETE/GET/HEAD calls — because a blind retry would replay the now-stale precondition and could turn a successful-but-500ing write into a masked conflict on the retried attempt. See client.DoWithContentTypeNoRetry."
		} else {
			m.Comment = opDef.Name + " does not auto-retry on a 5xx; see client.DoWithContentTypeNoRetry."
		}
	}

	if op.Deprecated {
		if m.Comment == "" {
			m.Comment = opDef.Name + " is deprecated."
		}
		depMsg := "\n//\n// Deprecated: this endpoint is marked deprecated in the Jamf API spec"
		if d := deprecationDate(op); d != "" {
			depMsg += " (deprecation-date: " + d + ")"
		}
		depMsg += " and may be removed in a future release."
		m.Comment += depMsg
	}

	// Privilege metadata. Spec-derived methods from a documented spec are
	// always marked known, even when the operation declares no privileges
	// (some endpoints — health checks, dashboards, icon downloads — require
	// none); the empty-slice case is meaningful and surfaced as "none" in
	// godoc and the registry. Undocumented (reverse-engineered) specs are left
	// unknown by default — "none" cannot be asserted for an endpoint we never
	// saw published — unless the operation carries an explicit
	// x-required-privileges annotation, which means someone has verified its
	// privileges out of band; honour that.
	_, hasPrivExt := op.Extensions["x-required-privileges"]
	if !spec.Undocumented || hasPrivExt {
		m.PrivilegesKnown = true
		// Both arrays are copied in spec order and NEITHER is sorted. That is
		// deliberate for the legacy one, and the reasoning is not guessable —
		// see privilegeSetsAreNotPairs and TestLegacyPrivilegesAreNotSorted.
		// Sorting it would make an (incorrect) positional pairing come out
		// right on 23 of the 24 equal-length multi-privilege pro operations
		// instead of 16, hiding a consumer's bug almost everywhere rather than
		// exposing it. Upstream already ships every scoped array alphabetical,
		// so the visible disorder in the legacy array is the only signal a
		// consumer gets that the two are not parallel.
		m.ScopedPrivileges = stringSliceExtension(op, "x-required-privileges")
		m.LegacyPrivileges = stringSliceExtension(op, "x-required-privileges-legacy")
		if len(m.ScopedPrivileges) > 0 {
			m.PrivilegeSource = privilegeSourceSpec
		}
		// config.requiredPrivileges supplies what the published spec omits.
		// Only ever additive to an operation declaring none: if upstream has
		// started declaring them, the config entry has expired and generation
		// says so rather than choosing a winner.
		if extra, ok := spec.RequiredPrivileges[normalizeOpKey(opDef.Op)]; ok {
			if hasPrivExt {
				return GoMethod{}, fmt.Errorf("requiredPrivileges[%q]: the spec now declares x-required-privileges — delete the config entry so the spec is the only source", opDef.Op)
			}
			m.ScopedPrivileges = append([]string(nil), extra...)
			m.PrivilegeSource = privilegeSourceGatewayPolicy
		}
		if c := privilegeComment(m); c != "" {
			if m.Comment == "" {
				m.Comment = opDef.Name + " calls a Jamf Platform API endpoint."
			}
			m.Comment += c
		}
	}

	m.PathParams = extractPathParams(m.ResourcePath, opDef.PathNames)

	// One resolution of the spec's parameter objects feeds both the
	// required-ness the templates emit against and the godoc below.
	specParams := collectSpecParams(pathItem, op)
	if err := resolveQueryParams(&m, specParams); err != nil {
		return GoMethod{}, fmt.Errorf("%s: %w", opDef.Name, err)
	}
	if err := resolveHeaderParams(&m, specParams); err != nil {
		return GoMethod{}, fmt.Errorf("%s: %w", opDef.Name, err)
	}

	m.ProducesMediaTypes = successMediaTypes(op)

	if err := resolveWireRequiredParams(&m, opDef.WireRequiredParams, specParams); err != nil {
		return GoMethod{}, fmt.Errorf("%s: %w", opDef.Name, err)
	}

	// Parameter docs come last so the block sits below the summary and the
	// privilege/deprecation lines, matching how godoc reads: prose, then
	// metadata, then the per-argument list.
	if pc := parameterComment(m, specParams, enumTypes); pc != "" {
		if m.Comment == "" {
			m.Comment = defaultMethodComment(opDef.Name, spec)
		}
		m.Comment += pc
	}

	m.ExpectedStatus, m.ResponseType = detectResponse(op)
	// When detectResponse populates ResponseType from the spec, capture
	// the matching schema's XML wire name so test stubs emit bodies the
	// generated decoder accepts. The later config-level responseType
	// override re-derives this, so only fill when both are unset.
	if m.ResponseType != "" && opDef.ResponseType == "" && doc.Components != nil && doc.Components.Schemas != nil {
		for specName, ref := range doc.Components.Schemas {
			if goTypeName(specName) != m.ResponseType {
				continue
			}
			if ref.Value != nil && ref.Value.XML != nil && ref.Value.XML.Name != "" {
				m.ResponseWireName = ref.Value.XML.Name
			} else {
				m.ResponseWireName = specName
			}
			break
		}
	}

	// Request body
	if op.RequestBody != nil && op.RequestBody.Value != nil {
		if mpContent, ok := op.RequestBody.Value.Content["multipart/form-data"]; ok && mpContent.Schema != nil && mpContent.Schema.Value != nil {
			m.MultipartFields = extractMultipartFields(mpContent.Schema.Value)
		} else {
			// Pick the first content-type the spec declares. The generator
			// emits it verbatim so endpoints that spec application/merge-patch+json,
			// application/x-www-form-urlencoded, or application/xml travel with
			// the correct Content-Type header rather than relying on transport
			// heuristics.
			// Honor the spec's declared content-type verbatim. The transport
			// has method-based defaults (PATCH -> merge-patch+json) that
			// would override an endpoint spec'd as application/json — so
			// we always set it explicitly when declared.
			for ct, content := range sortedContentEntries(op.RequestBody.Value.Content) {
				if content.Schema != nil {
					m.RequestType = refName(content.Schema)
					m.ContentType = ct
					break
				}
			}
		}
	}

	// Config-level overrides for request/response types and expected status.
	// Used when the spec is untyped (e.g. Jamf Classic) so the curator
	// explicitly names the schema from definitions/. Names are spec-level
	// (may be snake_case); the generator normalises to Go PascalCase.
	if opDef.RequestType != "" {
		m.RequestType = goTypeName(opDef.RequestType)
	}
	if opDef.ResponseType != "" {
		// A "[]T" literal names a bare JSON array response whose element type
		// is a component schema, for the case where the spec declares an
		// envelope the server does not send. Passed through verbatim rather
		// than run through goTypeName, which would mangle the brackets.
		//
		// This is deliberately an operation-level override and not a spec
		// patch: the disagreement is about what the server does, so it is
		// recorded where the wire evidence is cited (CLAUDE.md) and deleted in
		// one line when the server or the spec changes. A patched schema would
		// instead shadow the corrected declaration silently.
		if elem, isSlice := strings.CutPrefix(opDef.ResponseType, "[]"); isSlice {
			// ReturnsSlice / ResponseIsJSONArray / Category are all derived
			// from ResponseType further down, so setting it here is enough —
			// no need to short-circuit the rest of the build.
			m.ResponseType = "[]" + goTypeName(elem)
			m.ResponseWireName = elem
		} else {
			m.ResponseType = goTypeName(opDef.ResponseType)
		}
		// XML wire name is the raw spec name unless the schema overrides
		// via xml.name — test stubs emit <wireName> bodies so the generated
		// type's XMLName check passes.
		if m.ResponseWireName == "" {
			m.ResponseWireName = opDef.ResponseType
		}
		if doc.Components != nil && doc.Components.Schemas != nil {
			if ref, ok := doc.Components.Schemas[opDef.ResponseType]; ok && ref.Value != nil && ref.Value.XML != nil && ref.Value.XML.Name != "" {
				m.ResponseWireName = ref.Value.XML.Name
			}
		}
	}
	if opDef.ExpectedStatus != 0 {
		m.ExpectedStatus = opDef.ExpectedStatus
	}

	// Paginated item type
	if m.PaginationStyle != "" {
		m.ItemType = detectPaginatedItemType(op, m.ResultsField)
		m.ResponseType = ""
	}

	m.ReturnsSlice = strings.HasPrefix(m.ResponseType, "[]")
	m.ResponseIsJSONArray = m.ReturnsSlice || namedResponseIsArray(doc, m.ResponseType)

	// Determine category
	m.Category = categorize(m)

	// rawBody specs: generator emits []byte methods with no struct marshaling.
	// Resets type-driven state so the "raw" template takes over.
	if spec.RawBody {
		m.Category = "raw"
		m.MultipartFields = nil
		m.PaginationStyle = ""
		m.UnwrapResults = ""
		m.ItemType = ""
		m.ReturnsSlice = false
		m.ResponseIsJSONArray = false
		if httpMethod == http.MethodGet || httpMethod == http.MethodDelete {
			m.RequestType = ""
		} else {
			m.RequestType = "[]byte"
		}
		if httpMethod == http.MethodDelete {
			m.ResponseType = ""
		} else {
			m.ResponseType = "[]byte"
		}
	}

	if err := validateHeaderParamSupport(m); err != nil {
		return GoMethod{}, fmt.Errorf("%s: %w", opDef.Name, err)
	}

	return m, nil
}

// headerParamCategories are the method shapes whose templates stamp header
// params. The rest — multipart, raw, unwrap, both pagination walkers and the
// synthetic resolver/apply methods — build their requests without an
// http.Header, so a header declared on one of those would be accepted by
// config, appear in the method signature and its godoc, and then never be
// sent.
//
// Failing generation is the point. The alternative shipped shape is a method
// whose signature promises a header the request does not carry, and for a
// precondition header that means an update the caller believes is conditional
// silently becoming unconditional. Extending the set is a template change, not
// a config change.
var headerParamCategories = map[string]bool{
	"get":                true,
	"create":             true,
	"update":             true,
	"action":             true,
	"actionWithResponse": true,
}

// refuseHeaderParamsOnSynthetic refuses to build a resolver or apply method
// over a source operation that declares header params.
//
// It is the counterpart to validateHeaderParamSupport, which cannot reach
// these: a synthetic method is assembled field by field from the source rather
// than through buildMethod, and it deliberately does not copy HeaderParams —
// its signature is fixed (a resolver takes only a name; an apply takes a
// request). So the header would not appear in the signature at all, and the
// resolver/apply templates build their calls through ResolveByNameClient /
// ResolveByNameFiltered or the create/update methods' own paths with no
// http.Header anywhere.
//
// That makes the omission invisible rather than merely wrong: for an If-Match
// the caller has no way to supply the precondition and the write silently
// becomes unconditional. Refusing is the same choice made for the multipart,
// raw, unwrap and pagination templates — fail generation rather than ship a
// surface that cannot carry what the operation requires. Lifting it is a
// template change (thread the header through the synthetic signature), not a
// config change.
func refuseHeaderParamsOnSynthetic(kind, resourceType, opName string, src *GoMethod) error {
	if src == nil || len(src.HeaderParams) == 0 {
		return nil
	}
	names := make([]string, 0, len(src.HeaderParams))
	for _, h := range src.HeaderParams {
		names = append(names, h.Spec)
	}
	return fmt.Errorf("%s on %s: source operation %s declares header param(s) %v, which a synthetic %s method has no signature or template to carry — the header would be silently dropped; extend the %s template or drop the resolver/apply",
		kind, resourceType, opName, names, kind, kind)
}

// validateHeaderParamSupport refuses a header param the emitted method could
// not send. Content-Type and the retry opt-out need no check of their own:
// DoWithOptions carries all three dimensions, so the only thing that can make
// a header undeliverable is a template that never stamps one.
func validateHeaderParamSupport(m GoMethod) error {
	if len(m.HeaderParams) == 0 {
		return nil
	}
	if headerParamCategories[m.Category] {
		return nil
	}
	names := make([]string, 0, len(m.HeaderParams))
	for _, h := range m.HeaderParams {
		names = append(names, h.Spec)
	}
	return fmt.Errorf("declares header param(s) %v but its %q template has no headers form — extend the template, or drop the parameter", names, m.Category)
}

func categorize(m GoMethod) string {
	if len(m.MultipartFields) > 0 {
		return "multipart"
	}
	if m.UnwrapResults != "" {
		return "unwrap"
	}
	if m.PaginationStyle == "cursor" {
		return "paginatedCursor"
	}
	if m.PaginationStyle != "" {
		return "paginated"
	}
	hasReq := m.RequestType != ""
	hasResp := m.ResponseType != ""
	isOK := m.ExpectedStatus == 200

	// "create" covers any shape that sends a body AND returns one — POST 201,
	// PUT/PATCH 200, etc. Naming is historical; the template is request+response.
	switch {
	case hasReq && hasResp:
		return "create"
	case isOK && hasResp:
		return "get"
	case hasResp:
		return "actionWithResponse"
	case hasReq:
		return "update"
	default:
		return "action"
	}
}

// ---------------------------------------------------------------------------
// Spec helpers
// ---------------------------------------------------------------------------

// pathParamRe matches OpenAPI path parameter placeholders. Allows
// hyphens (e.g. {panel-id}) as well as the usual alphanumerics — the
// Jamf Pro spec uses kebab-case segment names in a handful of places
// (enrollment-customization panels).
var pathParamRe = regexp.MustCompile(`\{([\w-]+)\}`)
var versionPrefixRe = regexp.MustCompile(`^/v\d+`)
var tenantPathSegmentRe = regexp.MustCompile(`^/tenant/\{[^}]+\}`)

func extractPathParams(path string, overrides map[string]string) []GoPathParam {
	matches := pathParamRe.FindAllStringSubmatch(path, -1)
	params := make([]GoPathParam, 0, len(matches))
	for _, m := range matches {
		specName := m[1]
		var goName string
		if override, ok := overrides[specName]; ok {
			goName = override
		} else {
			goName = toLowerCamelCase(specName)
		}
		params = append(params, GoPathParam{SpecName: specName, GoName: goName})
	}
	return params
}

func stripVersionPrefix(path string) string {
	return versionPrefixRe.ReplaceAllString(path, "")
}

// stripTenantPathSegment removes a leading /tenant/{param} segment left after
// stripVersionPrefix when specs embed the tenant placeholder in the path
// (e.g. /v1/tenant/{tenantId}/blueprints → /blueprints). The tenantId is
// already injected by APIPrefix, so it must not appear in ResourcePath.
func stripTenantPathSegment(path string) string {
	return tenantPathSegmentRe.ReplaceAllString(path, "")
}

// extractVersion returns "v1" from "/v1/devices" or "v2" from "/v2/benchmarks".
// Returns empty string for non-versioned paths (e.g. "/startup-status") so
// tenantPrefix collapses the segment instead of forcing "v1". Callers that
// need a specific version for a non-versioned path must set it in config.
func extractVersion(path string) string {
	match := versionPrefixRe.FindString(path)
	if match == "" {
		return ""
	}
	return match[1:] // strip leading "/"
}

// namedResponseIsArray reports whether goType names a component schema whose
// own type is `array` — a Go type alias for a slice, so the wire body is a
// JSON array even though the method signature shows a single named type.
func namedResponseIsArray(doc *openapi3.T, goType string) bool {
	if goType == "" || doc.Components == nil || doc.Components.Schemas == nil {
		return false
	}
	for specName, ref := range doc.Components.Schemas {
		if goTypeName(specName) != goType || ref.Value == nil {
			continue
		}
		return ref.Value.Type.Is("array")
	}
	return false
}

// successMediaTypes returns the content types the operation's success
// response declares, sorted. It is the only declaration of what an Accept
// header may be set to: OpenAPI models a request's acceptable media types
// structurally, on the response, and not as a parameter enum — so a spec can
// be complete about the vocabulary while the `Accept` parameter it declares
// alongside is a bare string. See parameterComment for the use.
func successMediaTypes(op *openapi3.Operation) []string {
	if op == nil || op.Responses == nil {
		return nil
	}
	for _, code := range []int{200, 201, 202} {
		resp := op.Responses.Status(code)
		if resp == nil || resp.Value == nil || len(resp.Value.Content) == 0 {
			continue
		}
		out := slices.Collect(maps.Keys(resp.Value.Content))
		sort.Strings(out)
		return out
	}
	return nil
}

func detectResponse(op *openapi3.Operation) (int, string) {
	for _, code := range []int{200, 201, 202, 204} {
		resp := op.Responses.Status(code)
		if resp == nil {
			continue
		}
		if resp.Value == nil {
			return code, ""
		}
		// Deterministic iteration order: Go maps randomize iteration, so
		// when a response declares multiple content types (e.g. Swagger 2.0
		// `produces: [application/xml, application/json]` converts to two
		// entries with the same schema), picking the "first" non-JSON
		// caused drift between runs (typed vs []byte). Prefer a typed
		// schema over raw bytes; prefer JSON over other content types
		// (the spec schema is the authoritative description of the typed
		// shape regardless of on-the-wire codec, and Classic resources
		// use JSON pagination wrappers in docs even when XML on the wire).
		cts := make([]string, 0, len(resp.Value.Content))
		for ct := range resp.Value.Content {
			cts = append(cts, ct)
		}
		sort.Slice(cts, func(i, j int) bool {
			a, b := cts[i], cts[j]
			aj, bj := isJSONContentType(a), isJSONContentType(b)
			if aj != bj {
				return aj
			}
			return a < b
		})
		for _, ct := range cts {
			content := resp.Value.Content[ct]
			if content.Schema == nil {
				continue
			}
			// Non-JSON content (text/csv, application/octet-stream,
			// application/xml for JSON-format specs, …) returns raw bytes
			// regardless of any schema hint — a CSV export schema for
			// example only carries `format: binary` and callers want the
			// bytes, not an any-typed deserialisation.
			if !isJSONContentType(ct) {
				return code, "[]byte"
			}
			if ref := refName(content.Schema); ref != "" {
				return code, ref
			}
		}
		return code, ""
	}
	return 200, ""
}

// isJSONContentType reports whether ct is a JSON content type we should
// decode via encoding/json. Anything else is treated as raw bytes.
func isJSONContentType(ct string) bool {
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]))
	return base == "" || base == "application/json" || strings.HasSuffix(base, "+json")
}

// detectPaginatedItemType finds the Go element type of a paginated response's
// element array. resultsField names the envelope key holding it, because the
// key is not always "results" — cursor-paginated endpoints in particular name it
// for the resource (audit uses "items" and "transactions"). Defaulting to
// "results" when the caller passes an empty string keeps every existing
// operation on the old behaviour.
//
// Falling back to "any" on a miss is deliberate but easy to misread: it is not a
// harmless default, it silently widens the method's element type, which is what
// a wrong resultsField looks like from the outside.
func detectPaginatedItemType(op *openapi3.Operation, resultsField string) string {
	if resultsField == "" {
		resultsField = "results"
	}
	resp := op.Responses.Status(200)
	if resp == nil || resp.Value == nil {
		return "any"
	}
	// Two passes over deterministically ordered content types, because an
	// operation can declare several and they need not agree. Iterating the
	// Content map directly made the item type depend on Go's randomised map
	// order: pro's GET /inventory-preload declares the envelope under
	// text/csv but an *array of* that envelope under application/json (a spec
	// bug — the wire sends the bare envelope, confirmed 2026-08-31), so the
	// same config generated either InventoryPreloadRecord or
	// InventoryPreloadRecordSearchResults from run to run.
	//
	// The envelope form wins over the raw-array form for the same reason: a
	// resultsField match is positive evidence of the pagination wrapper this
	// function exists to see through, whereas the array branch is a fallback
	// for the "rawArray" style that cannot tell a genuine top-level array
	// from a mis-declared wrapper.
	for _, content := range sortedContentEntries(resp.Value.Content) {
		if content.Schema == nil || content.Schema.Value == nil {
			continue
		}
		schema := content.Schema.Value
		// allOf composition (pagination wrapper)
		for _, part := range schema.AllOf {
			if part.Value == nil {
				continue
			}
			if r := part.Value.Properties[resultsField]; r != nil && r.Value != nil && r.Value.Items != nil {
				return refName(r.Value.Items)
			}
		}
		// Direct results field
		if r := schema.Properties[resultsField]; r != nil && r.Value != nil && r.Value.Items != nil {
			return refName(r.Value.Items)
		}
	}
	// Raw array response — no wrapper, items live at the top level.
	// Paired with pagination style "rawArray" in config.
	for _, content := range sortedContentEntries(resp.Value.Content) {
		if content.Schema == nil || content.Schema.Value == nil {
			continue
		}
		if schema := content.Schema.Value; schema.Type.Is("array") && schema.Items != nil {
			return refName(schema.Items)
		}
	}
	return "any"
}

// scopeKindConstants maps an x-scope-types value to the Go constant the
// Privileges registry carries. Organization is the zero ScopeKind and sends no
// header, but it is named rather than omitted: an empty Scopes slice would be
// indistinguishable from a spec that declared nothing.
var scopeKindConstants = map[string]string{
	"tenant":       "ScopeTenant",
	"environment":  "ScopeEnvironment",
	"organization": "ScopeOrganization",
}

// resolveScopeTypes returns the scope-kind constants for a spec together with
// the provenance of the set, reading the root x-scope-types extension and
// applying config.scopeTypes where the published spec understates what the
// gateway serves. source is "spec" when the extension supplied the value and
// "config-override" when the config entry did; it is carried to each method's
// ScopesSource so a consumer reading one registry entry can tell an ingested
// declaration from a correction this repo made.
//
// Three failure modes are deliberate rather than tolerated. A malformed
// extension is a hard error, because an element the extractor cannot read as a
// string would otherwise vanish and leave a plausible-looking shorter set. An
// unknown value is a hard error for the same reason: silently dropping it
// would understate the scopes an endpoint accepts. And a spec that declares
// nothing with no override is a hard error too: the account trio is the only
// family with no x-scope-types, and it is organization-scoped, so it carries
// an explicit config.scopeTypes entry. A new spec that arrives without the
// extension therefore fails generation rather than emitting an empty scope set
// that a consumer would read as "no scope required".
func resolveScopeTypes(doc *openapi3.T, spec SpecDef) (kinds []string, source string, err error) {
	declared, err := stringSliceRootExtension(doc, "x-scope-types")
	if err != nil {
		return nil, "", fmt.Errorf("scopeTypes for %s: %w", spec.File, err)
	}

	source = "spec"
	if len(spec.ScopeTypes) > 0 {
		if equalStringSets(declared, spec.ScopeTypes) {
			return nil, "", fmt.Errorf("scopeTypes for %s: the spec now declares exactly %v — delete the config entry so the spec is the only source", spec.File, spec.ScopeTypes)
		}
		// The equality check above only expires an override the spec has
		// caught up with exactly. An override exists to WIDEN a spec that
		// understates the gateway, so anything the spec declares and the
		// override omits means the override has gone stale in the one
		// direction equality cannot see: replacing a superset with a subset
		// drops a scope upstream now publishes, and the registry would then
		// tell consumers an endpoint refuses a credential it accepts.
		if len(declared) > 0 && !isSubset(declared, spec.ScopeTypes) {
			return nil, "", fmt.Errorf("scopeTypes for %s: the spec now declares %v, which the config override %v does not cover — reconcile before generating", spec.File, declared, spec.ScopeTypes)
		}
		declared = spec.ScopeTypes
		source = "config-override"
	}
	if len(declared) == 0 {
		return nil, "", fmt.Errorf("scopeTypes for %s: the spec declares no x-scope-types and config supplies none — add a scopeTypes entry rather than emitting an empty scope set", spec.File)
	}

	out := make([]string, 0, len(declared))
	seen := map[string]bool{}
	for _, v := range declared {
		c, ok := scopeKindConstants[v]
		if !ok {
			return nil, "", fmt.Errorf("scopeTypes for %s: unknown scope kind %q (want tenant, environment or organization)", spec.File, v)
		}
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, source, nil
}

// equalStringSets reports whether a and b hold the same values, order and
// duplicates ignored.
func equalStringSets(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	sa, sb := map[string]bool{}, map[string]bool{}
	for _, v := range a {
		sa[v] = true
	}
	for _, v := range b {
		sb[v] = true
	}
	return maps.Equal(sa, sb)
}

// isSubset reports whether every value in a is present in b, order and
// duplicates ignored. An empty a is trivially a subset.
func isSubset(a, b []string) bool {
	in := make(map[string]bool, len(b))
	for _, v := range b {
		in[v] = true
	}
	for _, v := range a {
		if !in[v] {
			return false
		}
	}
	return true
}

// stringSliceRootExtension reads a []string-valued extension off the document
// root, the way stringSliceExtension reads one off an operation.
//
// An absent extension is a nil slice and a nil error, because the caller has
// its own refusal for a spec that declares nothing and the config-override
// path depends on reaching it. A malformed one is an error rather than a
// shorter slice: an element that is not a string would otherwise be dropped,
// and for x-scope-types the result is a registry entry that looks like a
// deliberate single-scope declaration.
func stringSliceRootExtension(doc *openapi3.T, key string) ([]string, error) {
	if doc == nil || doc.Extensions == nil {
		return nil, nil
	}
	raw, ok := doc.Extensions[key]
	if !ok {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is %v, want an array of strings", key, raw)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		str, ok := it.(string)
		if !ok {
			return nil, fmt.Errorf("%s contains %v, which is not a string", key, it)
		}
		out = append(out, str)
	}
	return out, nil
}

// applyMethodNotes appends each configured note to the godoc of the method it
// names. The mirror of applyDocNotes for methods: same keying by generated
// name, same wrapping, and the same refusal when a key matches nothing, so a
// note that has drifted off its operation fails the build instead of vanishing
// — which is also how these notes get deleted when the fact they record
// expires.
//
// The note lands after everything extractMethod already appended: the summary,
// the rate-limit/no-retry paragraphs, the deprecation marker and the
// required-privileges block. It therefore never splits an existing paragraph,
// and the "Deprecated:" paragraph keeps its own line — go/doc and staticcheck
// recognise it wherever it appears in the comment, not only last.
func applyMethodNotes(methods []GoMethod, notes map[string]string) error {
	if len(notes) == 0 {
		return nil
	}
	applied := make(map[string]bool, len(notes))
	for i := range methods {
		note, ok := notes[methods[i].Name]
		if !ok {
			continue
		}
		applied[methods[i].Name] = true
		lines := docParagraphs(note, typeDocWidth)
		if len(lines) == 0 {
			continue
		}
		if methods[i].Comment != "" {
			methods[i].Comment += "\n//\n// "
		}
		methods[i].Comment += strings.Join(lines, "\n// ")
	}
	var missing []string
	for _, name := range sortedKeys(notes) {
		if !applied[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("methodNotes names no emitted method: %s", strings.Join(missing, ", "))
	}
	return nil
}
