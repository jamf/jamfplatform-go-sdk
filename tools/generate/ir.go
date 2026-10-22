// Copyright Jamf Software LLC 2026
// SPDX-License-Identifier: MIT

package main

// ---------------------------------------------------------------------------
// Intermediate representation
// ---------------------------------------------------------------------------

// GoEnumConst is one value of an enum, emitted as a typed constant alongside
// the enum's `type X = <base>` alias. The base is usually string; numeric
// enums (Jamf uses them for interval and threshold fields) alias int or int64
// so the constants stay assignable to the struct field they constrain.
// The declaration carries the wire value on the same line, so no per-value
// godoc is emitted — a reader grepping for "MII_UNATHORIZED_RESPONSE_NOTIFICATION"
// still finds it even though the identifier normalises the spec's misspelling.
type GoEnumConst struct {
	Name    string // Go identifier, e.g. NotificationTypeApnsCertRevoked
	Value   string // wire value, e.g. APNS_CERT_REVOKED
	Literal string // ready-to-emit Go literal: `"APNS_CERT_REVOKED"` for a string enum, `1440` for a numeric one
}

type GoType struct {
	Name          string
	Comment       string
	Fields        []GoField
	EnumValues    []GoEnumConst // populated for enum schemas and for enums declared inline on a property
	EnumBaseType  string        // underlying type of the enum alias: "string" (default when empty), "int" or "int64"
	IsRawJSON     bool
	Discriminator *GoDiscriminator
	Union         *GoUnion // populated for a discriminator-less oneOf reached only as a request body
	XMLName       string   // wire element name when format=xml and it differs from Go type name; emitted as XMLName xml.Name `xml:"..."` field
	AliasTarget   string   // non-empty → emit as `type Name = AliasTarget` (used for top-level array schemas)
	IsListWrapper bool     // true when this is a Classic list wrapper (flattens {size, resource} array items into sibling fields). Excludes the type from heuristics that inject top-level id or carry id as a resource signal.
}

// GoDiscriminator describes a oneOf-with-discriminator polymorphic schema.
// The Go representation is a single struct carrying the discriminator value
// plus one pointer per variant; a generated UnmarshalJSON dispatches on
// the discriminator and populates the matching variant field.
type GoDiscriminator struct {
	PropertyName string // JSON property name of the discriminator field (e.g. "deviceType")
	GoFieldName  string // Go exported name for the discriminator field (e.g. "DeviceType")
	Variants     []GoDiscriminatorVariant
	// EnumTypeName names the generated enum carrying every discriminator value,
	// when one was emitted. The mapping keys are the authoritative set of values
	// the union accepts, and without constants for them the set is reachable
	// only by reading the generated switch. It matters most when a spec moves a
	// value out of a variant's own enum and into the mapping — uem-connect
	// dropped JAMF_PRO from ConnectorCreateRequest.vendor when it gained a
	// dedicated variant, which would otherwise have left the SDK with no
	// constant for the commonest vendor.
	EnumTypeName string
}

type GoDiscriminatorVariant struct {
	// Values holds every discriminator value routing to this variant, in the
	// order the mapping declares them. Usually one, but a spec may point
	// several values at a single schema — uem-connect maps nine UEM vendors at
	// the generic ConnectorCreateRequest and only JAMF_PRO at its own type.
	// Every value must become a case in the generated marshal and unmarshal
	// switches: collapsing them to one loses the others silently, and a
	// caller setting an unlisted discriminator would marshal to nothing but
	// the discriminator itself.
	Values    []string
	TypeName  string // Go type name (e.g. "MobileDeviceIosInventory")
	FieldName string // exported Go field name in the union struct (e.g. "IOS")
}

// Privilege provenance values for GoMethod.PrivilegeSource and the generated
// MethodPrivileges.Source. An empty string means no privileges are recorded at
// all, which is not the same as none being required — see MethodPrivileges.
const (
	// privilegeSourceSpec: read from the operation's own
	// x-required-privileges extension.
	privilegeSourceSpec = "spec"
	// privilegeSourceGatewayPolicy: supplied by config.requiredPrivileges from
	// the gateway's authorization policy, because the published spec declares
	// none. See SpecDef.RequiredPrivileges.
	privilegeSourceGatewayPolicy = "gateway-policy"
)

// GoUnion describes a discriminator-less `oneOf` whose members are all
// `$ref`s and which is reachable only as a request body. The Go
// representation is one pointer per variant plus a Raw json.RawMessage, with
// a MarshalJSON that emits whichever single variant is set.
//
// It exists because the alternative for this shape was silence. A
// discriminator-less union reached through a *property* had no branch at all:
// schemaRefToGoType fell through to its default and produced a bare `any`,
// which type-checks nothing, marshals whatever it is handed, and — since the
// transport sets no DisallowUnknownFields — decodes anything. account-sso's
// ConnectionRequest.connection carried the entire provider-settings body of
// every SSO connection create and update that way, with all four of the
// settings schemas it can hold emitted as Go types that no signature in the
// module referenced. validateNoUntypedFields now fails generation on a bare
// `any` field so the class cannot recur silently.
//
// Merging the variants into one flat struct — what a *named* discriminator-less
// union gets, see mergeOneOfVariants — is the right answer for a response,
// where decoding has to work without a tag and the union is structural. It is
// the wrong answer for a request: the four account settings schemas share a
// base and then disagree on which of `domain`, `groups`, `clientId` and
// `scopes` are required, so the merge intersects nearly every one of them to
// optional and a caller can populate fields from two providers at once with
// nothing to say so. Pointers keep the pairing visible to the compiler.
//
// Nothing here can validate the variant against its sibling discriminator —
// account-sso's `connectionType` lives on the *parent* schema, which the
// nested type cannot see, and the spec declares no discriminator to key it on
// anyway. Marshaling more than one variant is what does get rejected.
type GoUnion struct {
	Variants []GoUnionVariant
}

type GoUnionVariant struct {
	// FieldName is the exported Go field, and is simply the variant's own
	// type name. Deriving something shorter (Oidc from OidcConnectionSettings)
	// means stripping a suffix the variants happen to share, which is not
	// stable across specs and can collide after stripping; the stutter is
	// worth the determinism.
	FieldName string
	TypeName  string
}

type GoField struct {
	Name    string
	Type    string
	JSONTag string
	Comment string // godoc line emitted immediately above the field, if non-empty
}

type GoMethod struct {
	Name            string
	Comment         string
	Category        string // get, create, update, action, actionWithResponse, paginated, unwrap, multipart, resolverID, resolverTyped, resolverIDDirect, resolverTypedDirect, apply
	HTTPMethod      string
	Namespace       string
	Version         string
	Tag             string // first OpenAPI tag of the operation, used when SplitByTag is enabled
	ResourcePath    string // path after version prefix, e.g. "/devices/{id}"
	MultipartFields []GoMultipartField
	PathParams      []GoPathParam
	QueryParams     []ExtraParam
	// RequiredParams are the query params the method refuses to send without;
	// see OperationDef.RequiredParams.
	RequiredParams []ExtraParam
	// PrivilegeAlternatives marks ScopedPrivileges as an alternatives set; see
	// SpecDef.PrivilegeAlternatives.
	PrivilegeAlternatives bool
	// ProducesMediaTypes are the content types the success response declares,
	// sorted. Only godoc reads them, and only to document an Accept header
	// param — see parameterComment.
	ProducesMediaTypes []string
	// HeaderParams are request parameters the spec declares `in: header`,
	// emitted as string arguments that the method stamps on the request
	// through DoWithOptions — the only transport entry point that can carry
	// headers. Distinct from QueryParams because the two share a namespace in
	// the spec but nothing else — see parseHeaderParams.
	HeaderParams []ExtraParam
	// WireRequiredParams is the set of parameter wire names the server
	// refuses the request without, although the spec marks them optional.
	// Only godoc reads it — see parameterComment and config's
	// WireRequiredParams for why it must not affect emission.
	WireRequiredParams map[string]bool
	RequestType        string
	ResponseType       string
	ResponseWireName   string // XML element name of the response root (format=xml only); used by test stubs to emit valid wire bodies
	ExpectedStatus     int
	ContentType        string
	NoRetry            bool // from OperationDef.NoRetry — see its doc; drives DoWithContentTypeNoRetry vs DoWithContentType in template.go
	PaginationStyle    string
	PageSizeParam      string
	MaxPageSize        int
	ItemType           string
	ResultsField       string
	CursorField        string
	CursorParam        string
	ReturnsSlice       bool
	// ResponseIsJSONArray reports that the success body is a JSON array,
	// which ReturnsSlice does not: a named schema declared `type: array`
	// travels as an array but appears in the Go signature as its alias, not
	// as a []T. Test stubs pick the body shape from this; the method
	// templates keep using ReturnsSlice, which is about the Go type.
	// Security Cloud's GroupListResponse = []GroupListItem was the worked
	// example until v2082 withdrew GET /v1/groups and pruned the alias.
	ResponseIsJSONArray bool
	SpecPath            string
	UnwrapResults       string
	Format              string      // carried from SpecDef so per-method templates can branch without $-scope
	Resolver            *GoResolver // populated on synthetic resolver methods (Category resolverID/resolverTyped)
	Apply               *GoApply    // populated on synthetic apply (upsert) methods

	// Required-privilege metadata, sourced from the operation's
	// x-required-privileges / x-required-privileges-legacy vendor extensions.
	// PrivilegesKnown is true only for spec-derived methods (those built by
	// buildMethod); it stays false on synthetic resolver/apply methods, which
	// compose underlying endpoints rather than mapping to one operation. A
	// spec-derived method with no declared privileges has PrivilegesKnown=true
	// and empty slices — that means the *spec* declares none, which is
	// distinct from both "unknown" and "none required": the 18 account
	// methods are empty because the licensing/partners/sso specs carry no
	// x-required-privileges, while the gateway policy still gates them.
	// ScopedPrivileges and LegacyPrivileges are INDEPENDENT SETS, never to be
	// paired by position: their lengths differ on 29 pro operations and their
	// orders disagree on 9 more. The legacy form is published only for the Pro
	// family. See privilegeSetsAreNotPairs.
	// PrivilegeSource records where ScopedPrivileges came from, so a consumer
	// can tell a spec-declared set from one this repo supplied out of band.
	// Empty when the set is empty.
	PrivilegeSource  string
	PrivilegesKnown  bool
	ScopedPrivileges []string // GA capability permissions, {capability}:{action}, e.g. "buildings:create"
	// Scopes lists the scope kinds the endpoint accepts, as the Go constant
	// names emitted into the Privileges registry ("ScopeTenant",
	// "ScopeEnvironment", "ScopeOrganization"). Sourced from the spec root's
	// x-scope-types, or from config.scopeTypes where a held spec understates
	// it. Never empty: resolveScopeTypes fails generation instead.
	Scopes []string
	// ScopesSource records which of those two supplied Scopes — "spec" or
	// "config-override" — so the emitted registry can say so rather than
	// asserting spec provenance for a set this repo corrected. Resolved once
	// per spec alongside Scopes, and never empty for the same reason.
	ScopesSource     string
	LegacyPrivileges []string // human-readable Jamf Pro privilege names, e.g. "Create Buildings"
}

// GoResolver carries the config needed by resolver method templates.
// Populated on synthetic methods produced by appendResolverMethods; never
// present on spec-derived methods. Namespace/Version/ResourcePath on the
// parent GoMethod are inherited from the source op — the List op for
// filtered/clientFilter, the GetByName op for direct.
type GoResolver struct {
	ResourceType string // drives emitted method name suffix
	Mode         string // "filtered", "clientFilter", or "direct"
	NameField    string // filtered/clientFilter only — used in RSQL filter expression
	MatchField   string // client-side match verification path; equals NameField when the RSQL field matches the JSON response path
	IDField      string // filtered/clientFilter only
	IDNumeric    bool   // when true, test stubs emit numeric ID values (42 instead of "resolved-id")
	SearchParam  string // clientFilter only
	ResultsField string // envelope key for the element array; empty → transport defaults to "results"
	TypedReturn  string // Go type of the typed wrapper's return
	ExtraParams  string // filtered mode only: appended to list path before filter (e.g. "section=GENERAL")
	Paginated    bool   // clientFilter only: source list op is paginated — use paged transport walk
	ByField      string // suffix override: "BySerialNumber" → ResolveDeviceIDBySerialNumber. Empty → "ByName"
	SourceMethod string // direct only: existing Get<X>ByName method the wrappers delegate to
	// IDNilCheck and IDDeref are pre-computed expressions the direct-mode
	// template emits verbatim. They cover the nested-ID case: Classic
	// responses for composite resources (policies, mac_application,
	// ebook, …) populate only `<general><id>N</id></general>` on the wire,
	// even when the Go struct also has a top-level ID *int. The config's
	// idField path ("ID" or "General.ID") drives what Go field chain we
	// walk; the generator expands the chain with nil guards per step so
	// callers see "response missing id" rather than a nil-deref panic.
	IDNilCheck string
	IDDeref    string
	// IDTestInnerXML is the XML body fragment the direct-mode test stub
	// emits inside the response's wire-root element. Flat path ("ID")
	// produces "<id>42</id>"; nested path ("General.ID") produces
	// "<general><id>42</id></general>" so the typed decoder populates
	// r.General.ID and the resolver's walk succeeds.
	IDTestInnerXML string
}

// GoApply carries the config needed by the apply (upsert) method template.
// Populated on synthetic methods produced by appendApplyMethods; never
// present on spec-derived methods.
type GoApply struct {
	ResourceType      string // "BuildingV1"
	RequestType       string // Go type for the request parameter (e.g. "Building")
	NameGoField       string // Go struct field path to extract name (e.g. "Name", "DisplayName")
	NameParentField   string // non-empty for nested paths: "General" when NameGoField is "General.Name"
	NameParentType    string // Go type for the parent struct: "PolicyGeneral" when NameGoField is "General.Name"
	NameLeafField     string // leaf field name for nested paths: "Name" when NameGoField is "General.Name"; equals NameGoField when flat
	ResolverMethod    string // "ResolveBuildingV1IDByName"
	CreateMethod      string // "CreateBuildingV1"
	UpdateMethod      string // "UpdateBuildingV1"
	DeleteMethod      string // "DeleteBuildingV1" (for test generation only)
	CreateReturnID    string // expression to extract ID from create response: "resp.ID" for HrefResponse, "strconv.Itoa(resp.ID)" for int, "fmt.Sprintf(\"%d\", *resp.ID)" for *int
	IDNumeric         bool   // true when the create response ID is int (test mock should use numeric JSON)
	UpdateReturnsVal  bool   // true when Update returns (*T, error), false for error-only
	ExtraArgs         string // additional method signature args, e.g. ", platform bool"
	ExtraCallArgs     string // additional create call args, e.g. ", platform"
	ExtraTestCallArgs string // additional create call args with literal zero values for tests, e.g. ", false"
	ClassicCreate     bool   // true for classic API: Create takes (ctx, "0", request) instead of (ctx, request)
	NameIsPointer     bool   // true when the name field is a pointer (Classic XML types)
	NameNested        bool   // true when the name is inside a nested struct (e.g. General.Name)

	// UpdateType support — when the update operation takes a different Go type than create.
	UpdateType    string // Go type for the update request (empty = same as RequestType)
	HasUpdateType bool   // true when UpdateType is set (different create/update types)

	// Optimistic locking (versionLock) — for prestages.
	VersionLock       bool   // true when create must zero VersionLock fields and update must GET→inject them
	GetMethod         string // GET operation name (e.g. "GetComputerPrestageV3") — required when VersionLock is true
	GetNS             string // namespace for get
	GetVer            string // version for get
	GetPath           string // resource path for get endpoint
	GetType           string // Go type for the GET response (e.g. "GetComputerPrestageV3")
	SameGetUpdatePath bool   // true when GET and Update share the same URL (need combined handler in tests)

	// Token-upload mode — for resources created via token upload (e.g. DEP instances).
	TokenUploadMode    bool   // true when Apply uses upload-token create + optional token replace on update
	TokenUploadMethod  string // method that uploads token to create the resource
	TokenReplaceMethod string // method that re-uploads token to an existing resource
	TokenRequestType   string // Go type for the token request (e.g. "DeviceEnrollmentToken")
	TokenUploadNS      string // namespace for upload
	TokenUploadVer     string // version for upload
	TokenUploadPath    string // path for upload endpoint
	TokenReplaceNS     string // namespace for replace
	TokenReplaceVer    string // version for replace
	TokenReplacePath   string // path for replace endpoint

	// Membership pre-fetch mode — fetch current members before patch.
	MembershipPreFetch          bool   // true when Apply must fetch membership before patch
	MembershipFetchMethod       string // "ListStaticMobileDeviceGroupMembershipV1"
	MembershipFetchExtraArgs    string // ", nil, \"\"" — zero-value extra params for the fetch call
	MembershipFetchNS           string // "pro"
	MembershipFetchVer          string // "v1"
	MembershipFetchPath         string // "/mobile-device-groups/static-group-membership/{id}"
	MembershipSourceIDField     string // field on each result item (e.g. "MobileDeviceID")
	MembershipAssignmentType    string // Go type for each assignment item (e.g. "Assignment")
	MembershipAssignmentIDField string // ID field on assignment type (e.g. "MobileDeviceID")
	MembershipRequestField      string // field on request to inject into (e.g. "Assignments")
	MembershipRequestFieldIsPtr bool   // true when request.Assignments is *[]T

	// Test generation paths (pre-computed from the source ops).
	ListNamespace string // namespace for the list/resolver call
	ListVersion   string // version for the list/resolver call
	ListPath      string // resource path for the list endpoint
	ListNameField string // JSON name field for resolver response stubs
	ListIDField   string // JSON id field for resolver response stubs
	// ListResultsField is the envelope key holding the element array, mirrored
	// from the resolver config so the generated Apply test stubs answer with
	// the same shape the resolver actually reads. Empty means "results".
	// Without this the stubs hardcode "results" and an Apply whose resolver
	// declares a non-standard envelope (securitycloud device groups v2:
	// {groups: []}) generates an _Update test that silently takes the *create*
	// branch — the resolver finds nothing under the key it looks for, so the
	// test fails on an unstubbed create path rather than on the real mismatch.
	ListResultsField   string
	CreateNS           string // namespace for create
	CreateVer          string // version for create
	CreatePath         string // resource path for create endpoint (e.g. "/buildings")
	CreateStatus       int    // expected HTTP status for create response
	UpdateNS           string // namespace for update
	UpdateVer          string // version for update
	UpdatePath         string // resource path for update endpoint (e.g. "/buildings/{id}")
	UpdateStatus       int    // expected HTTP status for update response
	SameListCreatePath bool   // true when list and create share the same URL (need combined handler in tests)

	// Classic (XML) test generation fields — only set when ClassicCreate is true.
	ClassicResolverWireName   string // XML root element for the resolver's GetByName response (e.g. "computer_extension_attribute")
	ClassicResolverIDInnerXML string // inner XML for ID in resolver response (e.g. "<id>42</id>" or "<general><id>42</id></general>")
	ClassicCreateWireName     string // XML root element for the create response (e.g. "computer_extension_attribute")
}

type GoPathParam struct {
	SpecName string
	GoName   string
}

// GoMultipartField describes one part of a multipart/form-data request body.
// Binary fields (format: binary) emit two Go parameters: a filename string
// and an io.Reader content. Non-binary fields emit one typed parameter.
type GoMultipartField struct {
	Name   string // spec field name ("file")
	GoName string // Go param identifier (camelCase)
	IsFile bool
	Type   string // Go type for non-file fields
}

type GeneratedFile struct {
	Package string
	Module  string
	Format  string // "json" (default) or "xml" — drives struct tag style and transport codec
	Types   []GoType
	Methods []GoMethod
}
