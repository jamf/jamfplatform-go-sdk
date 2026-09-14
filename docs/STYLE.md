# Style and conventions

Invariants for changing this repo. The short forms of these rules are in
[CLAUDE.md](../CLAUDE.md); this file carries the reasoning, which is what tells
you whether a new case falls under a rule or outside it.

---

## The three hard rules

**Never modify the OpenAPI specs under `testing/`.** They are source-of-truth
mirrors of Jamf's published specs and must round-trip upstream changes cleanly.
Spec quirks — typos, wrong types, polymorphic roots, missing fields — are fixed at
the generator level via config overrides, post-processing passes, or
generator-emitted supplemental files.

**Never hand-edit generated files.** Change the generator or its config. Every
`.go` file in a generated sub-package (`jamfplatform/pro/`, `proclassic/`,
`devices/`, …) is written by `make generate`, and since the generator now prunes
files it did not write, a hand-added file there is deleted on the next run rather
than merely being wrong.

**Never add handwritten code to a generated sub-package.** Supplemental types
needed as `fieldTypeOverrides` targets (`BigInt`, `NotificationValue`) live in the
generator as emitted static files (`emitPkgXMLSupplements`) so they stay in
lock-step with spec and override changes. Adding `foo.go` to a generated package
is a correctness hazard, not a shortcut — make the generator emit it.

---

## File organization

- Every spec **must** set `"splitByTag": true`. Methods bucket by first OpenAPI
  tag into `<tag>.go` + `<tag>_test.go`; types pool into a shared `types.go`.
  Splitting by path would scatter one resource's CRUD across many files.
- Every spec **must** target a sub-package via `"package": "<name>"`. Jamf's API
  families reuse resource names, so a flat namespace collides.
- Sub-package names follow the namespace (kebab → snake → Go identifier). No
  invention. The one documented exception is `account`, which holds three specs
  (`licensing`, `partners`, `sso`) because they are one Jamf product behind one
  tyk api-product reached with one organization credential; the namespace stays
  per-spec so each method still builds its own URL prefix.
- **Two specs in one package must not emit the same tag filename.**
  `emitMethodsByTag` runs per spec and writes `<tag>.go`, so a shared tag means
  the second spec silently overwrites the first's file and its methods disappear
  with no error from the generator, the compiler or the tests. The generator now
  fails on the collision; resolve it with a per-spec `"tagRenames"` entry
  (filename only — method names, godoc and the published spec are untouched), as
  the uem-connect spec does to keep `activation_profiles.go` free for the
  enrollment spec.

## API formats

Selected per spec via `"format": "json"` (default) or `"format": "xml"`.

- **JSON** — Platform + Pro. `encoding/json`, `json:"..."` tags,
  `Content-Type: application/json`.
- **XML** — Classic only, and XML end-to-end. The transport detects
  `/proclassic/` in the path, switches to `encoding/xml`, and sets both `Accept`
  and `Content-Type` to `application/xml`. Forcing JSON responses is technically
  supported by the server and deliberately not used: Classic stays entirely XML
  for consistency.

## Field pointers — plugin-framework first

Every field in an XML spec is emitted as a pointer with `,omitempty`, regardless
of the spec's `required:` or `nullable:` markers. JSON specs keep the spec-driven
heuristic (pointer when nullable, non-required non-scalar, or request-type
non-required).

The primary consumer is a Terraform provider on the plugin framework, where
three-state null/value semantics are load-bearing. Without pointers:

- **Write-side ambiguity.** `name = null` (no change) and `name = ""` (explicit
  clear) collapse to the same zero string. A pointer distinguishes: `nil` → field
  omitted from the body → server untouched; `&""` → `<name></name>` → server clears.
- **Read-side ambiguity.** Server-omitted and server-returned-empty both decode to
  the zero string. A pointer maps `nil` → `types.StringNull()` and `&""` →
  `types.StringValue("")`, preventing recurring diffs when a Computed/Optional
  attribute flips between absent and empty across refreshes.
- **Partial updates.** `plan.Name.ValueStringPointer()` maps null → `nil` → field
  omitted. Intent preserved with no special-casing.
- **Nested objects.** `*ComputerGeneral` distinguishes "no `<general>` section
  returned" from "an empty one".

The cost is a pointer-deref tax for non-Terraform consumers of Classic. That is
the accepted trade. For JSON APIs the spec marks required/nullable accurately
enough and JSON has explicit field presence, so the heuristic suffices.

---

## Config key inventory

Every key `tools/generate/config.json` accepts, with what it does and who uses it.
The struct definitions and their long-form comments are in
`tools/generate/config.go` — that is the authority; this table is the map.

**Usage counts are as of v1872 and move with every ingest.** A key with zero users
is not dead code: each was added for a real case, is exercised by the generator's
own tests, and is the right tool when that case recurs. Treat a zero-user key as
available, not as a candidate for deletion — the App Installers removal
deliberately kept three of them for exactly that reason.

### Top level

| key | meaning |
|---|---|
| `package` | Go package name for the root output (`jamfplatform`) |
| `module` | module path used in generated imports |
| `specDir` | where published filtered specs are written, and the **CI fallback source** when `testing/` is absent (`api`) |
| `specs[]` | one entry per OpenAPI spec |

### Spec level

| key | users | meaning |
|---|---|---|
| `file` | 18 | source spec path, e.g. `testing/openapi-jpapi.json` |
| `namespace` | 18 | first URL segment(s) after the host; slashes allowed (`ai/governance/policies`, `ddm/report`) |
| `package` | 18 | target sub-package under `jamfplatform/`. Empty emits to the root (legacy; nothing uses it) |
| `specFile` | 18 | filename for the published filtered spec under `specDir` |
| `splitByTag` | 18 | **required** — one methods file per OpenAPI tag |
| `fieldTypeOverrides` | 5 | `"schema.property"` → Go type, to correct a spec bug. `*.property` matches the property on every schema. Applied per spec so an upstream fix isn't silently overwritten |
| `docNotes` | 3 | Go type name → prose appended to its godoc. For corrections belonging to the type as a whole, which `schemaPatches` cannot reach (it patches properties). **A key matching no emitted type is a build failure** — a silently dropped correction leaves the wrong doc in place |
| `scopeTypes` | 3 | spec → the scope kinds its operations accept (`tenant`, `environment`, `organization`), overriding a published `x-scope-types` that understates what the gateway serves. Carried to each method's `Scopes` in the Privileges registry, never into `api/`, with `ScopesSource` recording that the value is a config correction rather than the spec's own claim. Self-expiring in both directions: generation fails once the spec declares the same set, and also once the spec declares anything the override omits — an override widens an understated spec, so a spec that has moved past it is about to lose a declared scope, which the equality check alone cannot see. The account trio is the only user: no account spec declares the extension at all. `securitycloud-devices` was the second until 2026-09-04, when its hold lifted and the entry self-expired |
| `schemaPatches` | 3 | schema → dotted property path → raw OpenAPI 3 Schema. Adds or replaces at that path |
| `requiredPrivileges` | 3 | `"METHOD /path"` → GA capability permissions, for an operation the **published spec declares none for** but an authoritative out-of-band source does. Carried straight to the generated registry with `Source: "gateway-policy"`, never into the spec document, so `api/` stays faithful to upstream. **Fails generation if the operation now declares `x-required-privileges`** — that means upstream published them and the entry must go. All three users are the `account` specs; see [Required privileges](#required-privileges) |
| `enumAdditions` | 1 | schema name → wire values to append to its `enum`, for a value the server produces or accepts that the spec omits. Applied with the other schema patches, so the value reaches `api/` too. **Panics when the value is already declared**, when the schema declares no enum, or when the schema is missing — a duplicated enum member would emit two identical constants and compile, so nothing else would ever notice. One user: `Region` gaining `RAMP` |
| `emitNullForOptional` | 2 | schema names whose optional pointer fields must marshal as explicit `null` when nil rather than being omitted. For servers that distinguish "omitted" (keep) from "present and null" (clear). Accepts snake_case or PascalCase; JSON marshalling only |
| `propertyRenames` | 2 | schema → dotted path → new key. Repairs a spec key that doesn't match the wire, which otherwise decodes silently to nothing |
| `schemaAdditions` | 2 | schema → property → openapi type (`string`, `integer`, `boolean`, `string:password` for a writeOnly string). Injects a property the server accepts but the spec omits |
| `fieldOrder` | 1 | schema → ordered property list, overriding the default alphabetical emission. For servers that are order-sensitive **within an XML element** (Classic `/vppinvitations <general>`). Unlisted properties are appended alphabetically |
| `format` | 1 | `json` (default) or `xml`. Drives struct tags and the transport codec |
| `propertyRemovals` | 1 | schema → list of dotted paths to delete. **Panics on a missing path** — a declared removal that can't be walked is a typo. Targets inline objects only: a path resolving through a shared `$ref` affects every schema referencing it. Prunes the property's `required` entry with it, so a removal cannot publish a schema requiring a property it does not carry |
| `schemaCreations` | 1 | schema name → raw OpenAPI 3 Schema, for a component upstream **dropped** but the server still returns. Applied before every other pass, so it is indistinguishable from a spec-declared schema downstream. **Panics if the name already exists** |
| `schemaPatchesRequireAbsent` | 1 | `"<Schema>.<dotted.path>"` entries that must **not** already resolve. The `schemaPatches` counterpart of the panic `schemaCreations` gets free |
| `tagRenames` | 1 | OpenAPI tag → tag, **for filename selection only**. Method names, godoc and the published spec are untouched |
| `version` | **0** | URL version segment for operations whose own path carries none. Precedence: operation `version` > a version prefix on the spec path > this. Zero users because all five Security Cloud specs now carry their own `/v1/` — see the version-key rule in [CLAUDE.md](../CLAUDE.md#ingesting-a-new-gitops-bundle) |
| `excludePaths` | **0** | `"METHOD /path"` entries the generator must refuse to include. `validateConfig` errors if one also appears in `operations`, so it catches an accidental re-add rather than merely documenting one |
| `schemaRenames` | **0** | old schema name → new, updating every `$ref`. Applied before all other patches. For a generic name in one spec that would silently shadow a same-named type from another spec in the same package |
| `postSymmetryExcludes` | **0** | per `*_post` schema, property names **not** to mirror from the read sibling. The post-symmetry pass always runs and defaults to full symmetry; add an entry only when the server genuinely cannot accept a field on write (a server-assigned timestamp) |
| `rawBody` | **0** | emit `[]byte` in and out with no struct marshaling; the consumer owns it. Resets every type-driven field, so the `raw` template takes over |
| `typesOnly` | **0** | emit types from **all** schemas and nothing else — no methods, no method tests. For a spec defining types consumed inside another API's payloads (blueprint component configurations). `validateConfig` rejects it alongside `operations` |
| `undocumented` | **0** | every operation is reverse-engineered: stamps an `Unofficial:` godoc line and skips the privilege name-match check. See the App Installers note in [CLAUDE.md](../CLAUDE.md#packages) for why the last user was removed and what the bar is for a new one |

### Operation level

| key | users | meaning |
|---|---|---|
| `op` | 1483 | `"GET /v1/devices/{id}"` — matched against the **spec**, not the wire |
| `name` | 1483 | Go method name |
| `expectedStatus` | 351 | success status when not 200 |
| `responseType` | 320 | explicit response schema name, for an untyped spec body. Also accepts a `[]T` literal — see [bare-array responses](#responsetype-t--bare-array-responses) |
| `pathNames` | 313 | spec path param → Go param name |
| `requestType` | 229 | explicit request schema name |
| `params` | 161 | query params, compact: `"name"`, `"name:type"`, `"spec:type:goName"`, plus a trailing `:undocumented` to opt out of the spec name-match check. **Required-ness is not declarable here** — it is derived from the spec, see [query parameter emission](#query-parameter-emission-a-required-param-carries-no-guard) |
| `headerParams` | 4 | request headers the spec declares `in: header`, same compact notation as `params`. Refuses the scope headers, a non-`string` type, and a parameter the spec declares anywhere but `in: header` — see [header parameters](#header-parameters) |
| `wireRequiredParams` | 1 | parameter wire names the server refuses the request without although the spec marks them optional. **Godoc only** — it must never flip `AlwaysSend`, because sending the parameter empty is not better than omitting it. Self-expiring: fails generation once the spec marks the parameter required — see [header parameters](#header-parameters) |
| `pagination` | 127 | one of the five styles below |
| `resolver` | 108 | one name→ID resolver, optionally with `apply` |
| `contentType` | 8 | request Content-Type override. Every current user is `application/merge-patch+json` |
| `resolvers` | 6 | **multiple** resolvers on one operation, e.g. resolve a device by name *and* by serial number |
| `maxPageSize` | 3 | page size requested per page (default 100). **Raise only once the endpoint's true server-side cap is wire-verified** |
| `pageSizeParam` | 3 | page-size query key (default `page-size`). All three users are `ddmreport`, which calls it `size` |
| `noRetry` | 2 | opt out of the transport's automatic 5xx retry despite the method qualifying |
| `resultsField` | 5 | envelope key holding the element array (default `results`) |
| `unwrapResults` | 6 | return the list body's array directly as this Go type, for a non-paginated list endpoint |
| `version` | **0** | per-operation URL version override; highest precedence |
| `cursorField` | **0** | envelope key carrying the next cursor (default `nextCursor`) |
| `cursorParam` | **0** | query key the cursor goes back in (default `cursor`) |

`noRetry` is a small enumerable exception list, not an escape hatch. Set it **only**
when the endpoint requires a side-channel precondition in its body — an
optimistic-lock field sourced from a GET taken before the write — that a blind
byte-for-byte retry would replay stale, turning a successful-but-500ing write into
a masked conflict on the retry. Both current users are prestage PUTs, which is the
confirmed live case. Most PUTs have no such precondition and should keep the default.

`unwrapResults` **decides the body shape from the body**, not from the spec. It
generates a call to `client.UnwrapResults[T]`, which reads the first non-space
byte and accepts either the `{totalCount, results}` envelope the spec declares or
a bare JSON array. `resultsField` names the envelope key and is honoured (default
`results`).

That tolerance is not defensive padding — it is the fix for a shipped outage. The
four account list endpoints served a bare array until 2026-09-01 and the envelope
their specs had declared all along after; every one of `ListLicenses`,
`ListDomains`, `ListConnections` and `ListDealRegistrations` then failed *every
call* with `json: cannot unmarshal object into Go value of type
[]account.License`. **No unit test can catch that**, because the generated
httptest stub serves whichever shape the method assumed, and the first fix —
swapping the `responseType: "[]T"` override for `unwrapResults` — was symmetric
to the bug: it would break the same way if the server ever went back. So the
generated method no longer holds an opinion about which shape arrives.

The signal a shape did change moves to the acceptance suite, which is the only
layer that sees the wire: `assertListBodyShape` in `acc_helpers_test.go` fetches
a list endpoint outside the generated methods and pins what it answers with
today. **Every `unwrapResults` operation should be covered by one**, and a
failure there means "the server moved, date it in WIRE-FACTS", never "the SDK is
broken". Current coverage: the four account lists
(`TestAcceptance_AccountListBodyShapes`) and both devicegroups lists, asserted
inside the tests that already hold an id.

An emitted method's error path is unchanged in one respect worth knowing: an
envelope that omits the results key, `{}`, and `null` all decode to an empty
slice with no error, exactly as the struct decode this replaced did. The only
behaviour that changed from error to empty is a bare `[]`, which means zero rows.

### Header parameters

`headerParams` emits a spec's `in: header` request parameters as string
arguments the method stamps on the request. Four operations use it:

| operation | header | what it does |
|---|---|---|
| `UpdatePolicy` (aigovernance) | `If-Match` | optimistic-lock precondition; a stale value is 409 `POLICY_VERSION_CONFLICT` |
| `ExportPatchSoftwareTitleReportV3` | `Accept` | selects `text/csv` or `text/tab`. **Mandatory** — see below |
| `GetAccountPreferencesV3`, `UpdateAccountPreferencesV3` | `Accept-Language` | accepted, and inert on the wire today |

**It is a separate key from `params`, and that is the whole point.**
`collectSpecParams` keys every parameter by wire name regardless of where it
travels, so `"params": ["If-Match"]` matches the spec, passes the name-match
check, and emits `?If-Match=…` — a query key the server ignores, turning a
conditional update into an unconditional one with nothing failing anywhere.
Splitting the key lets each resolver assert the `in` it expects, and both
directions are refused: `resolveQueryParams` rejects a parameter the spec
declares `in: header`, `resolveHeaderParams` rejects one declared `in: query`.

Three more refusals, each because the alternative ships something that cannot
work:

- **The scope headers.** `generatorReservedHeaders` refuses `X-Tenant-Id`,
  `X-Environment-Id` and `Authorization`. `doRequestFull` applies extra headers
  *after* `setScopeHeader`, so a generated argument would win, and a wrong scope
  value is `403 OWNERSHIP_FORBIDDEN` — close to undiagnosable. The list is
  duplicated from `internal/client`'s `reservedHeaders` because
  `tools/generate` is its own module; `TestGeneratorReservedHeadersCoverTheScopeHeaders`
  pins the duplication.
- **Any type but `string`.** No spec declares a non-string header, and the wire
  encoding of a repeated or numeric one is unstated — comma-joining a
  `[]string` here would be inventing a serialisation.
- **A template with no headers form.** `headerParamCategories` is `get`,
  `create`, `update`, `action` and `actionWithResponse`. The rest — multipart,
  raw, unwrap and both pagination walkers — build their requests without an
  `http.Header`, so a header declared on one would appear in the signature and
  its godoc and then never be sent.
- **A resolver or apply over a header-bearing operation.**
  `refuseHeaderParamsOnSynthetic` covers what `validateHeaderParamSupport`
  structurally cannot: a synthetic method is assembled field by field from its
  source rather than through `buildMethod`, and it deliberately does not copy
  `HeaderParams` — a resolver's signature is `(ctx, name)` and an apply's is
  fixed too. So the header would not reach the signature at all, and the
  caller would have no way to supply the precondition. Both the `resolver`
  source and an `apply`'s `createOp`/`updateOp` are checked.

**Emission mirrors query params: optional headers keep a zero-value guard,
spec-required ones travel unguarded.** For `If-Match` the distinction is load
bearing rather than stylistic — an absent header means "update
unconditionally", while an empty one is a malformed precondition.

**The generated stub sends a sentinel and asserts it came back.** Stubs call
every method with zero-value arguments, and with `""` an optional header is
legitimately absent — so a template that forgot to wire one up would pass
identically to one that did. `testHeaderArgs` sends `hdr-<header-name>` and
`headerAsserts` requires the request carried it.

#### One transport entry point instead of a wrapper per combination

A header-carrying method routes through `Transport.DoWithOptions` and a
`client.RequestOptions` literal; every other method keeps the named shorthand
it already used. That confinement is deliberate — it holds the regenerated diff
to the operations that actually gained a header, and
`TestNoHeaderParamsKeepsTheNamedTransportCall` pins it.

`RequestOptions` exists because the per-request dimensions are independent:
expected status, Content-Type, extra headers, retry opt-out. Naming a wrapper
per combination had already produced `DoWithContentType` and
`DoWithContentTypeNoRetry`, and headers would have doubled it again. The choice
of entry point now lives in `transportCall` — one Go function rather than a
conditional repeated in five templates, which is also what stops a template
pairing a create's `request` with a get's `nil` result.

A `get` deliberately sets **no** `ExpectedStatus`. The shape it replaces called
`Do`, which hardcodes 200 and never read the field, so promoting the spec's
value would change behaviour for any read declaring something else.

#### An `Accept` header's allowed values come from the response, not the parameter

`acceptHeaderDocLines` documents an `Accept` parameter's vocabulary from the
operation's own success-response content types. This is the one header whose
allowed values a spec can state completely while its parameter declaration says
nothing, because OpenAPI models acceptable media types structurally on the
response: `responses.200.content` *is* the enum.

`GET /v3/patch-software-title-configurations/{id}/export-report` is the live
case. It produces `text/csv` or `text/tab`, answers **400** for anything else,
and describes its `accept` parameter as "File." — so a caller reading the
signature had no way to learn `text/tab` exists. It defers to a real enum if a
bundle ever adds one, and stays silent when the response declares a single
content type.

**The refusal is a 400, not the 415 an unsatisfiable `Accept` usually gets.**
Wire-verified 2026-09-09 with `GET /pro/v1/jamf-pro-version` at 200 as the
control in the same invocation: `application/pdf` (2/2) and `text/plain` both
answer 400, and the *error serialisation itself follows `Accept`* — absent or
`application/json` gives Jamf's JSON envelope, while `text/csv`, `text/tab`,
`application/pdf` and `text/plain` give Tomcat's HTML. That is the tell that the
header was always being read, and it is why a probe watching only the status
code would have missed the whole mechanism.

#### `wireRequiredParams` says what the signature cannot

A generated argument carries no required-ness. `accept` is declared
`required: false`, so `resolveHeaderParams` gives it a zero-value guard and the
signature reads `accept string` like any optional parameter — while the endpoint
answers 400 without it. That gap is how `ExportPatchSoftwareTitleReportV3`
shipped broken for its whole life, and a consumer migrating past the new
compile error would have reproduced it by passing `""`.

`wireRequiredParams` adds the missing line to the parameter's godoc:

```
//   - accept: File.
//     Allowed values, from the operation's declared response content types: text/csv, text/tab.
//     Required in practice: the server answers 400 when this is omitted, although the spec marks the
//     parameter optional. Passing the zero value omits it.
```

**It is documentation only, and must stay that way.** Flipping `AlwaysSend`
would send `Accept: ` empty, which the server refuses exactly as it refuses an
absent one — and on `columns-to-export` the empty form is a *500* where omitting
is a 400. The caller has to pass a real value, so the only useful fix is telling
the caller.

Both refusals are what stop the note outliving the defect: `resolveWireRequiredParams`
fails generation on a name the spec does not declare, and on a parameter the
spec has started marking `required` — at which point the parameter's own
declaration says it and the note would be restating it.

**That operation had never once succeeded.** The SDK sends no `Accept` of its
own on Pro paths, and without one the export answers 400 with an empty `errors`
array. The recorded diagnosis — that the 400 was a property of an empty patch
report — was wrong and had stood since 11.30.2, because the probe behind it
never set the header. Evidence and the corrected picture:
[WIRE-FACTS.md](WIRE-FACTS.md#export-report-was-never-callable-and-accept-is-why-2026-09-09).

### Pagination styles

All four offset styles wrap `ListAllPages[T]`; `cursor` wraps
`ListAllCursorPages[T]`. What each uses to decide there is another page:

| style | users | continuation test | shape |
|---|---|---|---|
| `totalCount` | 115 | `(page+1)*pageSize < totalCount` | `{totalCount, results}` |
| `hasNext` | 5 | the response's own `hasNext` | `{hasNext, results}` |
| `sizeCheck` | 4 | `len(results) >= pageSize && len(results) > 0` | `{results}`; `totalCount` is decoded and ignored |
| `rawArray` | 1 | same as `sizeCheck`, on a top-level array | bare `[…]`, no envelope |
| `cursor` | 2 | the server's own next cursor; **an empty page does not end the walk**, and a repeated cursor is an error | `{<resultsField>, nextCursor}` |

**The style is a safety decision, not a shape decision.** `totalCount` multiplies
the *requested* page size to compute each offset, so a server whose real cap is
lower silently skips the untransferred tail; `sizeCheck` and `rawArray` stop early
and truncate the remainder; `hasNext` can skip a chunk between pages. Configuring
any of them for an endpoint that **ignores** `page`/`page-size` is a *duplication*
bug — it re-fetches the full list and concatenates it. Probe before trusting a
`{totalCount, results}` envelope; two Security Cloud operations were configured off
their envelope shape alone and had to have the key removed.

### Resolver keys

| key | users | meaning |
|---|---|---|
| `mode` | 119 | `filtered` (42), `direct` (41), `clientFilter` (36) |
| `resourceType` | 119 | Go type name used in the emitted method names |
| `idField` | 89 | dotted JSON path to the ID on each list element |
| `apply` | 81 | emit an `Apply<ResourceType>` upsert too |
| `typedReturn` | 79 | Go type the typed wrapper returns; defaults to `resourceType` |
| `nameField` | 78 | dotted JSON path to the name on each list element |
| `byField` | 12 | override the `ByName` method suffix (`BySerialNumber` → `ResolveDeviceIDBySerialNumber`) |
| `extraParams` | 11 | `filtered` only: query params appended before the filter, for endpoints needing e.g. `section=GENERAL` to populate the filterable fields |
| `idNumeric` | 6 | the ID is a JSON number; test stubs emit numeric IDs |
| `idPointer` | 3 | the ID is a `*int`; overrides `idNumeric`'s `strconv.Itoa` with a dereferencing `fmt.Sprintf` |
| `matchField` | 3 | dotted path for client-side match verification when it differs from `nameField` — RSQL filters on `displayName` while the response nests it at `general.displayName` |
| `resultsField` | 3 | envelope key holding the array (default `results`) |
| `searchParam` | 1 | `clientFilter` only: server-side search key to narrow the list first. Empty fetches the whole list |

`mode` and `resourceType` are on all 119; `nameField`/`idField` are absent on the
41 `direct` resolvers, which delegate to a spec-generated `Get<X>ByName` and so
need no field paths.

### Apply keys

| key | users | meaning |
|---|---|---|
| `createOp`, `updateOp`, `deleteOp`, `nameGoField` | 81 | the baseline set; `deleteOp` is for test generation |
| `updateType` | 13 | Go type for the update request when it differs from create; triggers a JSON round-trip |
| `getOp` | 3 | GET used to read current state — **required** when `versionLock` is set |
| `versionLock` | 3 | zero `VersionLock` on create; fetch and inject the current value before update |
| `tokenUploadMode` + `tokenUploadCreateOp` + `tokenReplaceOp` | 1 | create by uploading an encoded token then updating metadata; the Apply signature gains a trailing `token string` |
| `membershipPreFetch` | 2 | re-specify current membership before a PATCH that would otherwise remove omitted members |

`membershipPreFetch` takes `fetchOp`, `sourceIdField`, `assignmentType`,
`assignmentIdField`, `requestField`, and `assignmentFieldIsSlicePtr` (set when the
request field is `*[]T` rather than `[]T`). The generated assignment item always
uses pointer fields.

---

## Config mechanisms

### Local spec repairs are self-expiring by design

Both mechanisms exist for a spec that is *wrong*, so both are built to fail loudly
the day upstream fixes it. Deleting the entry is then the whole fix.

- **`schemaCreations`** declares a whole named component the spec no longer
  carries. Applied before every other pass, so `schemaPatches` can point at it and
  it is emitted as soon as a whitelisted operation reaches it. It **panics if the
  name already exists**: the only reason to create one is that upstream deleted a
  schema the server still returns, so the name reappearing means the spec is
  repaired. A silent skip would leave the local definition shadowing the real one
  forever.
- **`schemaPatches`** silently overwrites, which is correct when *correcting* a
  wrongly-declared property and wrong when *supplying* a missing one — the day
  upstream adds the real property, the stand-in shadows it and discards whatever
  enum, format or required-ness upstream declared. Listing
  `"<Schema>.<dotted.path>"` in **`schemaPatchesRequireAbsent`** makes generation
  panic if the path already resolves, naming the entry to delete. Use it for every
  patch standing in for something absent; leave it off patches that deliberately
  replace.

Neither tripwire catches a **re-typed root**, and neither catches a stale
`expectedStatus` / `responseType` override. The compliance-benchmarks repair is
the worked example: our creation was `ODVRecommendation`, upstream shipped
`OdvRecommendation`, so the casing difference meant no panic fired even though the
spec had been fully repaired. **Diff config's overrides against every ingest, not
just the schemas.** A silent override going redundant has happened three times in
five builds.

### The generator keeps its own copy of `tenantFirstNamespaces`

`tools/generate/template.go` carries `testTenantFirstNamespaces`, duplicating the
transport's `tenantFirstNamespaces` list, because the generator is its own Go
module and cannot import the SDK. A divergence is **self-detecting rather than
silent**: the generated httptest handlers register at a path the client never
calls, and `make test` fails on the next run. Change both together anyway.

### `"responseType": "[]T"` — bare-array responses

`responseType` normally names a component schema; it also accepts a `[]T` literal
whose element is a schema name, for when the spec declares a wrapper the server
does not send. The account list endpoints are the reason.

This is deliberately an operation-level override rather than a
`schemaPatches`/`schemaCreations` repair: the disagreement is about *server
behaviour*, so it belongs where the wire evidence is cited and it is one line to
delete when either side changes. A patched schema would instead shadow the
corrected declaration forever, with neither tripwire firing.

### Query parameter emission: a required param carries no guard

An optional query param is emitted behind a zero-value guard — `!= ""`,
`len(x) > 0`, `!= 0`, truthiness for `bool` — so an argument the caller never set
does not travel. **A spec-required one is emitted unconditionally.** Guarding it
means a caller passing the zero value sends a request with a required parameter
missing, and the rejection reads like a fault rather than a caller error:
Security Cloud's `ListActivationProfilesV1` shipped that way, `origin=""`
producing `400 BAD_REQUEST` "Required parameter 'origin' is not present." in the
service's own envelope, with no `errors[]` and nothing naming the caller's
mistake. Sent empty, the same call comes back `400 INVALID_FIELD "Unknown origin
value."` — the declared shape, attributable. Pro's `GET /v1/jamf-package` is the
same story: omitted is `400` with an **empty** `errors` array, `application=` is
`400 INVALID_FORMAT` explaining that the name must be alphanumeric.

**Required-ness is derived from the spec, never declared in config.json**, in
`resolveQueryParams` — the same pass that runs the name-match check, so one
resolution of the spec's parameter objects feeds both. A `:required` suffix
alongside `:undocumented` was rejected: `:undocumented` exists only because that
flag genuinely is not in the spec, whereas required-ness is, so a manual suffix
would be a second source of truth that rots the first time a bundle flips a
param from optional to required. Derived, the next `make generate` picks the flip
up. An `:undocumented` param has no spec parameter to read and keeps its guard.

**One carve-out: a required param whose schema declares a substantive default
keeps its guard.** OpenAPI forbids the combination outright ("default SHALL NOT
be used with required"), so such a parameter is a malformed declaration and the
choice of which half to follow is settled by the wire, not the text. Pro's
`columns-to-export` on `GET /v3/patch-software-title-configurations/{id}/export-report`
is the only live case — `required: true` with a nine-column default — and
sending it empty is *worse* than omitting it: on a config whose patch report has
zero rows, omitting it and passing a valid two-column list both answer 400,
while `columns-to-export=` answers **500**. An empty default (`""`, `[]`) states
nothing and does not earn the carve-out. `hasSpecDefault` is the predicate; if a
bundle drops the default, the param starts travelling unguarded on the next
generate, which is the right response to the spec no longer defining its
absence.

The guard logic lives in two template funcs, `queryValue` and `queryGuard`, not
in the templates. The type switch had been written out three times — in
`buildQueryParams`, `paginated` and `paginatedCursor` — and every branch of all
three had to honour required-ness; three copies of a nine-branch switch is how
the bug survived the `fb2c38e9` hardening that covered the parameter *name*
dimension and left this one alone. Two layers pin it: a generator test renders
every category × every type and asserts the guard's presence exactly on the
optional side, and each generated httptest stub calls its method with zero-value
arguments and asserts `r.URL.Query().Has(...)` for every required param, which
fails if the guard ever comes back.

One conversion is not mechanical. An optional `bool` is only ever emitted when
true, so `"true"` as a literal is exact; a required one uses
`strconv.FormatBool` so it can carry a false the caller meant.

### Endpoint versions are additive

Jamf publishes new versions of Pro endpoints (V1 → V2 → V3) and keeps prior
versions in the spec until they are physically removed. **SDK policy is to retain
every spec version side-by-side as a typed function** —
`ListComputersInventoryV1`, `V2`, `V3` all coexist — so downstream consumers get a
real migration window. A version is removed only when Jamf removes it from the
spec, never merely because a successor appeared.

Each version gets its own resolver and apply sugar, with the `V<N>` suffix in
`resourceType` to disambiguate method names. `typedReturn` falls back to the
unsuffixed Go type when the spec hasn't suffixed the V1 schema.

`python3 tools/scripts/backfill_versions.py` synthesises missing version entries
by cloning the closest sibling and version-suffixing the names; it is idempotent.
**It only reaches base paths that already exist at more than one version.** It
keys on the path with `/vN` stripped, so a path the new version *introduces* is
invisible — `len(versions) == 1` means no sibling to clone. Jamf does this when it
relocates an operation: 11.30.0 moved erase and remove-mdm-profile from
`/v1/…/computer-inventory/{id}/` (singular, deprecated) to
`/v4/…/computers-inventory/{id}/` (plural), which reads as two unrelated
single-version bases. Those need entries written by hand. **After every spec bump,
diff the spec's operation set against config and check the additions** — do not
trust the insert count, because a whole surface can end up deprecated with no
successor emitted (issue #50).

### Deprecation

Endpoints marked `deprecated: true` are always emitted, with a `// Deprecated:`
godoc line carrying the normalised `x-deprecation-date`. There is no
`skipDeprecated` knob: deprecation is opt-out by retaining the config entry,
opt-in by deleting it once Jamf drops the path.

**A `// Deprecated:` marker must never ship without its successor whitelisted
alongside it.** `staticcheck`'s SA1019 is on by default, so deprecating a surface
with nothing to migrate to turns every consumer's build red for no reason.
Consumers also need both versions live at once. A wire-only deprecation — a
runtime `Deprecation` header with nothing in the spec — is covered for free:
`logDeprecation` logs it once per method+path.

### Name→ID resolvers

Two generated methods per resource: `Resolve<X>IDByName` and `Resolve<X>ByName`,
attached via a `"resolver"` block. Three modes, selected by what the API actually
supports:

- **`filtered`** — the list endpoint accepts RSQL equality
  (`filter=name=="x"`). One GET with `page-size=2`; ambiguity from
  `totalCount > 1`. Attaches to the List op.
- **`clientFilter`** — no RSQL, or only a substring `search`. Fetches the list
  (optionally narrowed via `searchParam`) and walks it in memory for an exact
  `nameField` match. Attaches to the List op.
- **`direct`** — Classic only. The spec already generates `Get<X>ByName`; the
  wrappers delegate and stringify the typed `*int` ID, so no name/id field paths
  are needed. Attaches to the by-name GET. `typedReturn` overrides only when the
  Go struct name differs from the resource name.

The error contract is identical across modes: not-found is an `*APIResponseError`
with `HasStatus(404)`; ambiguous matches (filtered/clientFilter only) are
`*AmbiguousMatchError`.

`resultsField` names the envelope key holding the array. It is easy to get wrong
in a way nothing catches: the Apply test templates once hardcoded `"results"`
while the *resolver* templates already branched on it, so a generated `_Update`
test stubbed the list under a key its own resolver never read, silently took
Apply's **create** branch, and failed on an unstubbed create path rather than on
the mismatch. Threading `ListResultsField` through the Apply IR fixed it. Every
other Apply has an empty `resultsField`, which is exactly why nothing caught it —
**watch for any hardcoded envelope key while every spec happens to use the
default.**

### Apply (upsert)

`Apply<ResourceType>(ctx, request, ...) (id string, created bool, err error)`,
emitted when a resolver block includes `"apply"`. Resolves the name; 404 →
create, found → update. **Declared entirely in `config.json` — never hand-coded.**

| mode | when |
|---|---|
| standard | one request type shared by create and update |
| `updateType` | create and update take different Go types; the generator JSON-round-trips create → update |
| `versionLock` | prestages: zeroes `VersionLock` on create, GETs current and injects `versionLock` before PATCH |
| `tokenUploadMode` | DEP enrollment: uploads an encoded token then updates metadata; the Apply signature gains a trailing `token string` |
| `membershipPreFetch` | resources whose PATCH must re-specify the current member list, or omitted members are removed |

`membershipPreFetch` maps each current member's ID into an `Assignment`-like
struct with `Selected=true` and injects it into the request before the patch. Set
`assignmentFieldIsSlicePtr: true` when the request field is `*[]T`. The generated
item always uses pointer fields.

### Schema handling

- **A discriminator-less `oneOf` reached through a property used to become
  `any`, silently.** `schemaRefToGoType` ends in `default: return "any"`, and an
  inline union has no properties, so `hoistInlineObjects` never lifted it, no
  component schema was ever named for it, and the containing field type-checked
  nothing. The transport sets no `DisallowUnknownFields`, so such a field also
  decodes anything without complaint — nothing fails at generate time or at run
  time. account-sso's `ConnectionRequest.connection` shipped that way: the entire
  provider-settings body of every SSO connection create and update, with all four
  settings schemas it can hold emitted as Go types **no signature in the module
  referenced**. Three parts to the fix, each load-bearing:
  - `isRefUnion` recognises the shape — a discriminator-less `oneOf` over two or
    more bare `$ref`s, with no type, properties or other composition of its own —
    and `hoistInlineObjects` now lifts it into a named schema
    (`ConnectionRequestConnection`), which is what gives it a Go type at all.
    Every member must be a `$ref` so each variant has a type to point at; a
    one-member `oneOf` and `oneOf: [{$ref}, {type: null}]` are other idioms and
    are excluded.
  - **A request-only union gets variant pointers; everything else keeps merging.**
    `mergeOneOfVariants` is right for a *response*, where decoding has to work
    with no tag in the body — audit's `AuditEnvelope` is that case and is
    untouched. It is wrong for a request: the four account settings schemas share
    a base and then disagree on which of `domain`, `groups`, `clientId` and
    `scopes` are required, so the merge intersects nearly all of them to optional
    and a caller can populate two providers' fields at once with nothing to say
    so. So `usage.isRequest && !usage.isResponse` selects `schemaToUnionType`:
    one pointer per variant, a `Raw json.RawMessage`, and a `MarshalJSON` that
    emits the single non-nil variant, **errors on two**, and emits `null` on none
    so a zero-valued enclosing request stays marshalable. Decoding cannot pick a
    variant, so `UnmarshalJSON` preserves the bytes in `Raw` and says so.
  - **Nothing here validates the variant against its sibling discriminator.**
    account-sso's `connectionType` lives on the *parent* schema, which the nested
    type cannot see, and the spec declares no `discriminator` to key on. Adding
    one through config was considered and rejected: the union machinery puts the
    discriminator field *inside* the nested struct, so `ConnectionRequest` would
    carry `connectionType` twice and a caller setting only the outer one
    marshals `{"connectionType":""}` into `connection`.
  - Field names are the variant's own type name (`OidcConnectionSettings
    *OidcConnectionSettings`). Stripping the suffix the variants happen to share
    reads better and is not stable across specs — it can collide after
    stripping — so the stutter is the deliberate trade.
  - `emitUnionRoundTripTest` writes `unions_test.go`, marshaling each variant and
    asserting the bytes equal the bare variant. The generated *method* test cannot
    cover this: its stub is built from the request type, so it serialises whatever
    the generated code assumed. The account SSO create test passed for as long as
    the field was `any`, calling `CreateConnection` with a bare
    `&ConnectionRequest{}` and putting nothing on the wire.
- **A bare `any` field now fails generation.** `validateNoUntypedFields` rejects
  any emitted field typed `any` or `*any`, because that is the tell of a schema
  construct the generator has no branch for and the failure mode is silence. It
  passes with zero exemptions across all twelve packages. `map[string]any` and
  `[]any` are allowed and are what a genuinely freeform schema should produce.
  **Do not widen the check to make a spec pass** — teach the generator the
  construct, or correct the property through config.
  - Note this guard would *not* have caught the sibling shape, which is why the
    inference below exists: a schema declaring `type: object` **plus** `oneOf`
    plus its own tag property emits a named type carrying only the tag, so there
    is no `any` to fail on.
- **A `oneOf` whose `discriminator` the spec forgot is inferred from the
  variants' own enums.** blueprints' `SwUpdateConfiguration` declares
  `type: object`, a `oneOf` over `SwUpdateManualConfiguration` and
  `SwUpdateAutomaticConfiguration`, and one property of its own —
  `enforcementType` — but no `discriminator` object. It fell through to plain
  struct generation and emitted **that one field**, dropping the union whole: a
  caller could read or write the enforcement type and nothing else, so the type
  could not express a software-update configuration at all. That is why
  `terraform-provider-jamfplatform` builds this component as a `map[string]any`
  and never names the type — a consumer routing around a generated type is the
  signal to look at.
  - Nothing is guessed. Every variant constrains `enforcementType` to exactly
    one value — `MANUAL` on the manual variant, `AUTOMATIC` on both of the
    automatic one's own variants — so `inferDiscriminator` reads the mapping off
    the spec. `unionSchema` is the single gate both call sites use: a declared
    discriminator wins, a missing one is inferred, and a content-addressed
    mapping (`com.jamf.ddm.*`) is refused either way.
  - **The sibling proves it is an authoring slip, not a different model.**
    `SwUpdateAutomaticConfiguration` declares the identical shape *with* a
    discriminator and has always generated correctly. The inferred union comes
    out shaped exactly like it, and the two nest: the outer dispatches on
    `enforcementType`, the inner on `strategy`, and a full
    `SwUpdateComponent` now round-trips to
    `{"configuration":{"deploymentTime":"16:00","enforceAfterDays":14,"enforcementType":"AUTOMATIC","strategy":"LATEST"},…}`.
  - **This is the opposite call from account-sso, and the difference is where the
    tag lives.** Here the discriminator is inside each variant, where the wire
    already carries it, so `MarshalJSON`/`UnmarshalJSON` work. There
    `connectionType` is on the parent request, unreachable from the nested type,
    so synthesising a discriminator would have emitted the tag twice and
    marshalled an empty one — hence variant pointers and no tag at all.
  - Deliberately strict, because a wrong tag is worse than no tag — it routes a
    payload to the wrong variant in silence. The candidate must be declared on
    the parent (which is what keeps `AuditEnvelope`, with no properties of its
    own, on the structural merge); every variant must resolve it to exactly one
    string value with the values pairwise distinct; resolution follows a
    variant's own `oneOf` and requires its members to agree; and **two
    candidates that both discriminate is refused rather than resolved by taking
    the first**, since either works on the wire but they produce different Go
    field names and a different emitted enum.
  - The inferred discriminator is a shallow copy and **never reaches
    `api/*.json`**. It is this generator's reading of the spec, not something
    upstream declared. Report the omission upstream.
  - `emitUnionRoundTripTest` covers both union kinds, so every generated union
    now has a marshal test — five of them across `blueprints`, `pro` and
    `securitycloud`. It matters most for an *inferred* discriminator, where the
    value-to-variant pairing is the generator's inference rather than a
    declaration.

- **`$ref` siblings are restored on publish.** kin-openapi drops a `description`
  (or any sibling) that sits next to a `$ref`, so marshaling the parsed document
  would strip prose the source spec carries — visible as large, spurious doc churn
  in `api/blueprints_api.json` and `api/compliance_benchmark_engine.json`.
  `restoreRefSiblings` re-attaches them from the source file before writing.
  **Do not "fix" that churn by reverting it**: a generate run without this pass
  rewrites unrelated packages backwards.
- **Unreachable schemas are pruned between post-symmetry and hoisting, and that
  ordering is load-bearing.** `pruneUnreferencedSchemas` deletes every component
  schema the whitelist no longer reaches, and both the publish path and the Go
  path call it at the same point: **after** `applyPostSymmetry`, **before**
  `hoistInlineObjects`. Neither side of that sandwich is stylistic.
  `applyPostSymmetry` gives a `*_post` schema its read sibling's properties by
  **shared `SchemaRef` pointer**, so pruning first strips the post type of the
  sections it inherits. And `hoistInlineObjects` names a lifted nested object
  after whichever parent it reaches first in sorted order — so an *unreachable*
  read schema still in the document keeps winning the name over the `_post`
  sibling that is the only remaining owner.
  That combination broke CI parity once and would again: dropping
  `GET /computers/id/{id}` from the whitelist left `computer` unreachable but
  present in `testing/`, where it went on naming `ComputerGeneralManagementStatus`,
  while `api/` — pruned by this same function before publication — hoisted the
  identical object off `computer_post` as `ComputerPostGeneralManagementStatus`.
  CI generates from `api/`, the committed tree came from `testing/`, and
  `git diff --exit-code -- jamfplatform/` failed on 31 renames no config key
  mentions. Note the failure is **invisible to a normal local generate**, which
  reads `testing/` — only the fallback-forcing run catches it, which is why that
  check belongs in any change that alters which operations are whitelisted, not
  just ones that touch the generator. Pinned by
  `TestPruneUnreferencedSchemasRunsBeforeHoistNaming`. `collectRefs` takes the
  whitelist for the same reason: an OAS3 document is loaded whole (`loadSpec`
  only prunes Swagger 2.0 paths), so a withdrawn operation still sits in
  `doc.Paths` and its `$ref`s would otherwise keep its schemas reachable
  forever. The `typesOnly` path deliberately does not prune — those specs emit
  every schema they declare.
- **Swagger 2.0** is upconverted in-memory via `openapi2conv.ToV3`. Whitelisted
  paths are pruned from the Swagger document *before* conversion, because
  openapi2conv rejects some Classic operations (multiple body params) that are
  outside any whitelist anyway.
- **A `type: string` field whose `example` is a bare decimal** is the reliable
  predictor of a server that sends an unquoted number, and `json.Number` is the
  honest override target: it decodes the number, preserves the exact text, and
  converts to the string a path param wants, so it keeps working if the server ever
  starts quoting. Precedents: `deployment_task.id` and
  `sso_keystore_details.serialNumber` in pro, `domain.id` and
  `domain.verifiedTldId` in account.
- **Untyped operations.** Classic has 444 ops but 0 typed responses and only 6
  typed request bodies in the raw spec; `requestType`/`responseType` overrides
  name a schema for the generator to wire through.
- **`allOf`** is flattened — properties merged into one struct, avoiding Go
  embedding rules for OpenAPI's base+extensions pattern.
- **`oneOf` + discriminator** emits a union struct with one pointer field per
  variant plus `UnmarshalJSON`/`MarshalJSON` dispatching on the discriminator.
- **`oneOf` with no discriminator and no properties of its own** used to emit
  **nothing at all**, failing loudly only by luck: such a union as a named
  *response* type aborts generation with "Go type not emitted", but one reached
  solely through a property would have silently become `any`.
  `mergeOneOfVariants` collapses these into one struct — the union of every
  variant's properties, with `required` **intersected** across variants, so a
  property required by only some variants becomes an optional pointer and the
  merged struct can represent any variant without lying about presence. A union
  that declares its own `type: object` plus `properties` takes the pre-existing
  path and is unaffected.
- **OpenAPI 3.1 nullability.** `normalizeNullableUnions` rewrites
  `type: [T, "null"]` and `oneOf: [{$ref: X}, {type: "null"}]` into 3.0's
  `nullable: true` before anything else reads the doc. kin-openapi models 3.1
  faithfully and `Types.Is` only matches a single-element type list, so without the
  pass every nullable property lands as `any` — silently, and only in the 3.1
  specs. It runs before `hoistInlineObjects` and `collectReferencedSchemas`:
  hoisting must see the collapsed shape, and a `$ref` reachable only through a
  nullable union has to be visible to the reference walker or its type is never
  emitted. Unions of two or more non-null types stay `any` — there is no single Go
  type for them. **Reach for this pass, not a `fieldTypeOverride`, when a new 3.1
  spec produces an unexpected `any`:** an override is per-field and rots silently
  when the spec adds another nullable property.
- **The 3.0 `$ref`-with-siblings idiom.** `nullable: true` + `allOf: [{$ref: X}]`
  is 3.0's only way to attach a description, `nullable` or an `example` to a
  reference. Such a wrapper declares no type and no properties, so it used to fall
  through to `any` — and **failed silently by construction**, since an `any` field
  decodes anything and the transport sets no `DisallowUnknownFields`.
  `isSingleRefAllOfWrapper` collapses it, with a deliberately tight guard: exactly
  one `allOf` member, and no type, properties, enum, `additionalProperties`,
  `oneOf` or `anyOf` of its own. A multi-member `allOf` is a real composition with
  no single Go type; a wrapper carrying its own properties is a named component
  `extractTypes` already flattens; and a bare `$ref` returns its own name before
  the switch. Nullability is deliberately unaffected: the wrapper is inline, so
  the field emitter reads `nullable` off the wrapper rather than off the shared
  component — collapsing by mutating the target's `Nullable`, as
  `collapseNullableOneOf` does for its `$ref` branch, would mark that component
  nullable for **every** field referencing it. That latent hazard in
  `collapseNullableOneOf` is untouched and worth fixing separately.

---

## A shared schema's optionality follows its request/response reachability

`needsPtr` in `schemaToGoType` (`tools/generate/schema.go`) includes
`isRequest && !isRequired`: for a **request** type an unrequired scalar becomes
`*T` with `,omitempty`, so a caller can distinguish "omit" from "send zero" —
which PATCH depends on. For a response-only type the same field is a plain `T`.

**A named schema emitted once and shared by both kinds takes its shape from
whichever parents are reachable, so a field can change shape with no diff in its
own schema.** `SmartGroupCriteria.OpeningParen` and `.ClosingParen` went `*bool` →
`bool` between v1897 and v1942 while that schema stayed **byte-identical** and its
`required` list never mentioned either field. The trigger was v1942 withdrawing
the V1 mobile smart-group operations, which took `SmartGroupAssignment` — a
request body embedding `[]SmartGroupCriteria` — with them. It is the only one of
five paren-bearing types to have moved, because the other four still have
surviving request parents.

**Checked, and this is not a generator bug.** `schemaUsage` already unions
`isRequest`/`isResponse` across every referencing parent and request wins, so
there is no ordering dependence of the `detectPaginatedItemType` kind. And in the
v1942 spec `SmartGroupCriteria` is not reachable from any request body **at all**,
whitelist ignored — so response rules are the correct output for that input. The
generator tracked a real spec change.

What *is* latent, and has not happened yet: the same flip could be caused by a
**whitelist** removal rather than a spec removal, and then there would be no spec
diff to explain it. Diagnose it by asking whether the schema is reachable from any
`requestBody` in the spec document, not whether the generated parents exist.

**Do not "fix" it by forcing one direction.** Measured: making every named schema
use request rules pointer-ises **711 fields across 11 packages** (2127 changed
lines, 1963 of them in `pro`) — a breaking change to the whole surface to restore
two fields with no consumer. Forcing response rules instead would break every
PATCH caller that relies on omit-vs-zero. Both are policy decisions about the
whole SDK; if one is ever wanted, take it deliberately and on its own, not while
fixing something else.

## Enum constants

Every enum becomes a Go type plus one constant per value, in a per-package
`enums.go` — never `types.go`, see `partitionEnumTypes`. Two sources feed it:

- **Spec-named enum schemas** keep their own name (`NotificationType`), emitted by
  the fieldless-type branch of the schema walker via `enumConsts`.
- **Inline property enums** are synthesised as `<Owner><Property>` by
  `registerPropertyEnum` (`PolicyTrigger`, `AccountGroupV1AccessLevel`). Nothing
  shorter is safe — `type` alone carries 14 distinct value sets across 18 schemas.
  Identical value sets under different owners are **duplicated rather than folded**:
  folding means inventing a name, and that name churns whenever Jamf changes one
  owner's values but not the other's.

### Design decisions that look wrong and are not

**The alias is `=` (a type alias, not a defined type)**, so a constant typed
`NotificationType` assigns to any existing `notificationType string` parameter
with no cast. Promoting these to defined types, or retyping the struct fields that
carry them (`State DeploymentState` instead of `State string`), would churn the
whole tree and entangle with the pointer/three-state design. **Field types are
left alone.** The one narrow exception is not a change of policy: a field that was
`any` may end up typed as an enum alias, because the `$ref`-with-siblings collapse
resolves such a wrapper to whatever it references. No field that was already
`string` has been retyped.

**Constant names are `<TypeName><TitleCasedValue>`:**
`NotificationTypeApnsCertRevoked = "APNS_CERT_REVOKED"`. Title-casing each segment
is deliberate over preserving all-caps runs — `APNS_CERT_REVOKED` is a wire-format
artefact, not an acronym the spec is asserting. The wire value sits on the same
line as the identifier, so no per-value godoc is emitted and the spec's own
misspellings (`MII_UNATHORIZED_RESPONSE_NOTIFICATION`, `PATCH_EXTENTION_ATTRIBUTE`)
stay greppable.

**Classic is where this pays off most:** its values are prose, not identifiers —
`Full Access`, `Text Field`, `Pop-up Menu`, `Current or Next User`,
`Pending+Failed`. Callers had no way to guess the exact spacing and casing. The
title-caser handles them with no special sanitiser because it splits on
non-alphanumerics.

**`<Type>Values() []<Type>` returns every value in spec order**, for
`stringvalidator.OneOf(pro.PolicyTriggerValues()...)` and anything else that
enumerates rather than names. A function, not a var: a var would let one consumer
mutate the set for the whole process. It cannot be a method — Go forbids methods
on an alias to a predeclared type, and that alias is what keeps constants
assignable to plain `string`.

Fields and parameters point at the type rather than re-listing values
(`Allowed values: see the NotificationType constants.`), since godoc groups
constants under their declared type.

### Parameter-only enum schemas

A named enum schema reachable **only from a parameter** is emitted too.
Previously it was not, and its values survived solely as an inline
`Allowed values:` list in the method godoc — readable but not referenceable, so
every call site hard-coded string literals. `collectReferencedSchemas` walks
request bodies and responses only, which is why these were invisible; it now also
registers a parameter's schema **when that schema resolves to a component that is
itself a string enum**, handling both shapes the specs use (a direct `$ref`, and
`type: array` whose `items` are a `$ref`).

This is deliberately **not** a full parameter walk. Descending into parameter
schemas the way the body walkers do would register arbitrary object schemas
nothing currently emits, changing the type set across every spec. Restricting
registration to string-enum components bounds the blast radius to the enum types
alone — **5 schemas, all in `pro`: `ComputerSection`, `ComputerSectionV2`/`V3`/`V4`
and `MobileDeviceSection`, 106 constants between them** — with no signature change
anywhere. Callers pass `pro.ComputerSectionV4General` where they previously wrote
`"GENERAL"`. One case still keeps an inline list: a `$ref`'d property, where the
field's own type already names the enum.

### Skips, and why every skip still reaches the caller

Skipped, each deliberately: values yielding no Go identifier, the second of any
two values colliding on one identifier, a `$ref`'d property or item schema (the
field's own type already names the enum), a base type the SDK has no alias for
(booleans), and any synthesised name the spec already uses — checked for both
`<Owner><Property>` and `<Owner><Property>Values`, since the accessor shares the
namespace. Every skip logs.

**Non-string and single-value enums used to be skipped and are now emitted.** The
old rationale was that a `= string` alias cannot type a number and that a
one-value constant is noise. Both were wrong in the same way: a consumer
validating input has to get the set from somewhere, and the only alternative is
retyping the literals, which is exactly what `terraform-provider-jamfplatform`'s
`enumguard` forbids. It lost that protection three times — `CreatePathV2.Scope`'s
one-value `[APP]`, and uem-connect's `refreshRateMinutes` and
`deviceUnmanagedThreshold` — before the rule changed.

- **Numeric enums alias `int` or `int64`, not `string`.** The base tracks the
  field's own Go type (`format: int64` → `int64`, otherwise `int`) so the
  constants stay assignable to the field they constrain; `GoType.EnumBaseType`
  carries it and `GoEnumConst.Literal` carries the rendered literal, unquoted.
  The identifier is the decimal digits — `SyncSettingsRefreshRateMinutes1440` —
  with a `Neg` prefix for negatives, since Go identifiers cannot carry a minus
  sign and `-1` is a real Jamf sentinel. `Values()` returns `[]<alias>`, which is
  `[]int64` by definition: **do not grep for `[]int64` to check the helper
  exists**, it renders as the alias name.
- **Single-value sets are emitted for the same reason as any other.** They are
  the larger population: 50 new enum types appeared when the rule changed, most
  of them the mandatory magic strings on DDM blueprint components.
- A fractional numeric value would yield no identifier and is skipped with a log
  line. None exists today, and inventing a spelling for one would be guesswork.

**The collision check must happen at registration, not when draining the collected
enums.** Draining late still writes the field's `see the X constants` line,
leaving it pointing at a type that carries no constants.

`Deployment.State` is the only skip firing today — `DeploymentState` is already a
struct name — and its values remain reachable through the oddly-named
`DeploymentStateState`. A clean generate log carries exactly that one skip line.

**Every skip still gets its values into the godoc.** When `registerPropertyEnum`
declines, `inlineFieldEnumValues` lists them on the field instead:
`// Allowed values: 300, 1800, 3600, 10800, 28800.` It is now a genuine fallback
rather than the main path for numeric and single-value sets — those get
constants, and the field's doc line points at them instead. What still reaches
the godoc this way is a `$ref`'d property, a boolean enum, and a name the spec
already claimed.

It is scoped to **inline** property schemas: a `$ref`'d property (or `$ref`'d item
schema) returns nil, because the field's Go type already names the enum and godoc
groups the constants under it. Re-listing there would duplicate a list that then
rots independently.

`enumConstsOfBase` logs per-value drops but **silently skips an empty-string
member of a string enum, and a member whose type does not match the alias base**
— a number in a string enum, or vice versa. Mixing types in one enum is a spec
bug and coercing would emit a constant the field cannot accept; nothing in the
tree hits it today, and it is the first path to check if a set comes out short.

### Auditing coverage

**Match on value sets, not type names.** A name-based check produces false
positives on every acronym the generator re-cases (`Id`→`ID`, `Mdm`→`MDM`,
`IdP`→`IDP`) and on `fieldRenames` (`date_type`→`data_type`). Five apparent gaps
in the 2026-08-29 audit were all of that kind. Of 286 schema string-enums with ≥2
distinct values, all were emitted and value-complete, with zero missing values,
zero identifier failures and zero intra-set collisions.

**The one genuine gap was a map-key problem, not an enum-emission problem.**
`ParentApp.restrictedTimes` is `map[string]TimeFrame`, which is right for the
wire, but its legal keys are the `DayOfWeek` enum and **no Go field ever carries
that type**, so no constants were emitted and the values reached callers nowhere —
not even in godoc. The cause is in the spec: OpenAPI cannot constrain a map's *key*
type, so the author wrote `properties: {key: {$ref: DayOfWeek}}` beside
`additionalProperties: {$ref: TimeFrame}`. That pseudo-property named `key` is not
a real property, the generator correctly ignores it, and `DayOfWeek` becomes
unreachable. Closed with a `docNotes` entry; the field type is untouched. **Any
future `additionalProperties` map whose keys are constrained by this pseudo-`key`
trick will have the same gap and will not be caught by a name-based audit.**

---

## Documentation emitted into the SDK

### Parameter documentation

Each method carries a `// Parameters:` list built from the spec's parameter objects
(`parameterComment`): the description, then `Allowed values:` when the schema
declares an enum. Ordering follows the Go signature — path params, then
config-declared query params. Params the spec doesn't describe are skipped.

**This is the only place a caller can learn what a `filter` or `sort` argument
accepts.** Jamf documents the RSQL-filterable and sortable field lists in the
parameter description and nowhere else, so a bare `filter string` signature is
unusable without the block. Same for Classic's `subset` path params, whose legal
values (`General`, `Location`, `Purchasing`, …) are a path-param enum.

**A config-declared query param's `Spec` name is a hand-typed literal that is also
the exact string sent on the wire** via `params.Set(...)`. `parameterComment`
therefore **fails generation** if that name matches no parameter the spec declares:
a spec rename or a config typo otherwise compiles fine and silently sends a query
key the server ignores, with the only symptom being a method with no
`// Parameters:` block at all. (`baselineId` was renamed to `baseline-id`
upstream, config wasn't updated, caught 2026-08-18.) A param wire-verified to work
despite the spec omitting it entirely opts out with a trailing `:undocumented`
segment — reach for this only when the spec's silence is confirmed
deliberate/wrong to the same standard as everything else here, not as a quick way
past the check.

**An operation-level parameter overrides a same-named path-level one** per the
OpenAPI spec — **but not its *description* when the override's is a placeholder**
(`isPlaceholderParamDoc`: empty, the bare param name, or `"<in> parameter <name>"`).
The Platform specs declare `id` twice on every path: once at path level as a
`$ref` carrying real prose (`The ID of the device, in UUID format`), once inline on
the operation with an autogenerated `Path parameter id`. The literal precedence
rule throws away the only useful sentence. A parameter carrying enum values is
never treated as a placeholder — the values are documentation regardless of the
prose. Only the godoc is affected, never the signature.

**Parameter types stay exactly as config declares them.** The block is
documentation, never signature. That is what makes it safe to quote enum values
verbatim, including ones no Go identifier can spell
(`EnableRemoteDesktop (macOS 10.14.4 and later)`) and the spec's typo `Hardwre`,
all of which are still what the server accepts.

Struct fields are documented the same way from each property's `description`, with
the write-only note appended as trailing metadata. Both share `docParagraphs`.

Spec prose is wrapped at 100 columns, paragraph by paragraph, with HTML tags
stripped and entities decoded. Angle-bracket placeholders like `<field_name>` are
deliberately preserved — the tag stripper matches an explicit HTML tag allowlist,
not a generic `<...>` pattern. **Long descriptions are never truncated**: a
half-printed RSQL field list is worse than none, since the caller can't tell
whether their field was in the dropped tail.

### Required privileges

**`Scoped` and `Legacy` are independent sets. Never pair them by position.**
A downstream consumer rendered its "Required Jamf privileges" table by zipping
`Scoped[i]` with `Legacy[i]`, and shipped two swapped labels for
`RedeployJamfManagementFrameworkV1` before anyone noticed. Wrong privilege
guidance sends an operator to grant the opposite privilege, so this is worth
stating twice. Two facts make the pairing impossible, either one sufficient:

- **The lengths differ on 29 pro operations**, because the GA capability
  consolidation mapped several legacy privileges onto one capability.
  `GET /v1/computer-groups` is one `device-groups:read` against
  *Read Smart Computer Groups* + *Read Static Computer Groups*. There is no
  bijection to zip.
- **Where the lengths match, the orders still disagree — in the spec itself.**
  `POST /v1/jamf-management-framework/redeploy/{id}` declares
  `[computer-check-in:read, device-actions:execute]` against
  `[Send Computer Remote Command to Install Package, Read Computer Check-In]`,
  reversed. 9 of the 24 equal-length multi-privilege operations are like this, so
  a length check does not make a zip safe either.

**The legacy array is deliberately left unsorted, and this is the part most
likely to be "cleaned up".** Upstream already ships every scoped array
alphabetical (0 of 746 unsorted), so sorting legacy for stable diffs looks
obviously right. It is not: measured against the spec, it would take an incorrect
positional pairing from 16 of 24 equal-length operations coming out right to
**23 of 24** — converting a bug that is visibly wrong on nine operations into one
that is silently wrong on one, including the case above. The visible disorder is
the only signal a consumer gets. `TestLegacyPrivilegesAreNotSorted` reads the
generated `pro/permissions.go` and fails if the sort is ever introduced;
`privilegeSetsAreNotPairs` in `tools/generate/privileges_test.go` carries the
reasoning. Note the tripwire must read the *generated registry*, not a spec —
`publishSpecs` copies the extension values straight from the source, so a
generator-side sort never reaches the published spec and a spec-reading test
cannot see it.

The emitted method godoc says so inline wherever the two lists could be mistaken
for parallel arrays (43 methods): *"The scoped and legacy lists are independent
sets, not pairs: do not match them by position."*

**Upstream ask:** Jamf publishes no scoped→legacy mapping in the specs. Its
"Jamf Pro permissions map" article is the only artefact carrying it, and it gives
the mapping only at *capability* level (which legacy privileges a capability
absorbed), never per operation — so a consumer wanting labelled rows still has to
label from `Scoped` alone, or present the two lists separately.

What the article does settle, and what now depends on it:

- **The identifier form is `{capability}:{action}`** — kebab-case capability with
  no product name, one of six lowercase actions (`create`, `read`, `update`,
  `delete`, `deploy`, `execute`). The three-part beta slug `create:pro:buildings`
  is retired; the `MethodPrivileges` godoc claimed that form until 2026-08-31
  while the data had been two-part since v1824.
- **A multi-entry `Scoped` slice means all of them are required.** The deployed
  allowlist expresses every multi-capability route in the SDK's surface with
  `has_all_permissions`; its one cross-capability OR (`DELETE /proclassic/logflush`
  — `flush-policy-logs:execute` *or* `policies:delete`) reaches the registry as a
  single identifier because the spec declares only the first. Evidence:
  [WIRE-FACTS.md](WIRE-FACTS.md#the-whole-registry-cross-checked-against-the-allowlist-2026-08-31).
- **An empty `Scoped` slice means nothing declares a privilege, not that none is
  required.** Both the struct godoc and `privilegeComment` say so rather than the
  old "callable by any authenticated API client". A consumer still has to be told,
  because it cannot see a godoc: a Terraform provider rendered the empty account
  sets as *"None — any authenticated Jamf Platform API integration may call the
  underlying endpoints"* and was about to publish that to the Terraform Registry.
  `MethodPrivileges.Source` is what makes the distinction machine-readable —
  `"spec"`, `"gateway-policy"`, or `""` when the set is empty.
- **The capability reference is a closed vocabulary, so it is a tripwire — and
  the vocabulary is now parsed, not transcribed.** A committed snapshot of the
  article lives at `tools/generate/permissions-map.md`, `parsePermissionsMap` in
  `permmap.go` reads its capability tables, and
  `TestScopedPrivilegesUseGAVocabulary` asserts all **337** distinct generated
  identifiers against it: unknown capability, an action the capability does not
  offer, or a return to the three-part form each fail. It used to assert against
  `gaCapabilityActions`, a hand-typed copy of the same tables — that caught real
  defects but drifted silently from its source and had to be extended by hand on
  every ingest that met a new capability. **`make permmap` refreshes the
  snapshot**; a new capability upstream is expected, so the failure is the
  notification to refresh, and the assertion is never to be deleted to make an
  ingest pass. A disagreement that survives a refresh is real: wire-probe it,
  report it upstream, and record it in `permissionsMapExceptions`, which is
  self-expiring in both directions like `schemaPatchesRequireAbsent` — an entry
  the map has caught up with, and an entry for a permission the SDK no longer
  emits, both fail. It is currently **empty**.

  Two guards make the parse trustworthy. `minDeclaredCapabilities` fails a
  snapshot that parsed to fewer than 100 capabilities, because a parser that
  silently finds nothing reports perfect agreement — the one failure mode this
  check must not have. And **rows contribute the union of their action sets**: a
  capability is declared across as many rows as its resources need, and reading
  one row as the whole declaration manufactures a disagreement. That is not
  hypothetical — it is how two spurious `compliance-benchmarks` exceptions came
  to be written, from a parser that kept `{r}` over `/baselines` and dropped
  `{c,r,d}` over `/benchmarks`.

  **The path dimension is deliberately not checked.** The map's Endpoints column
  looks mechanical — resource-root prefixes, versions collapsed,
  longest-prefix-wins — but the cells abbreviate continuation roots:
  `disk-encryption-recovery-key` reads
  "Pro `/computers-inventory/filevault`, `/{id}/filevault`", and the second
  entry's prefix has to be inferred from the first. Guessing it wrong either
  manufactures a disagreement or hides one, so the checker does not guess.
  `permmap.go`'s header carries the full reasoning.

`x-required-privileges` feeds a per-package `Privileges` registry plus a
`// Required privileges:` godoc line, and downstream a Terraform provider
permissions table. The SDK **reports what the spec says** and does not correct
it locally; upstream disagreements get reported, not patched.

**The `account` family is the one exception, and the grounds are structural
rather than "upstream is slow".** The earlier position — recorded in the
`MethodPrivileges` godoc itself — was that its 18 empty sets stay empty and get
reported upstream. That waits forever. Each of the three specs *is* authored
with a `requiredPrivileges` block in
the spec source repository's own per-team `config.yaml`, and that
file's own closing comment says why the published artifact carries none: these
routes resolve the organization from the access token, so Tyk's
`request-context-allowed-sources` is `[token]`, so the beta transform that
attaches `x-required-privileges` does not apply to them. The omission is a
consequence of the routing model, not a backlog item, and it will not change
while the routing model does not.

So `requiredPrivileges` in `config.json` supplies them, and three things keep
that honest:

- **The values are attributed, not asserted.** They reach the registry as
  `Source: "gateway-policy"` and the method godoc says *"The published spec
  declares none for this operation; these are the capabilities the gateway's own
  authorization policy enforces."*
- **They never enter the spec document.** `api/*.json` still declares none, which
  is what upstream publishes. Unlike `schemaPatches`, this key is not part of the
  spec-patch pipeline.
- **Three independent sources agree on all 18, and the agreement is now checked
  rather than asserted.** The `config.yaml` blocks above; the hand-written OPA
  rules in
  the gateway's authorization policy for the `account` namespace; and
  the published permissions map, whose *Organization management scope* section
  declares `licensing:{r}`, `deal-registration:{c,r}`,
  `distributor-actions:{c,r,u}`, `sso-connections:{c,r,u,d}` and
  `sso-domains:{c,r,u,d}` — every one of the 18, action for action. That third
  source is the only one independent of the specs, and
  `TestScopedPrivilegesUseGAVocabulary` fails the build if it stops agreeing. It
  matters most here, because `account` is the one package whose privileges no
  spec carries. Note the rego accepts *either* the GA capability or a
  retired `read:org:*`/`update:org:*` permission
  (`lib.has_any_of_permissions`), and only the GA form is carried: `Scoped` is a
  conjunction, so listing the alternative would read as "both required".

`undocumented: true` on a spec skips the privilege name-match check and stamps an
"Unofficial:" godoc line — it marks a spec whose operations are in no spec Jamf
publishes. It currently has zero users, and the bar for a new one is a published
spec.

### Endpoint scope

`MethodPrivileges` carries `Scopes []ScopeKind` beside `Scoped`, so one registry
lookup answers both *"what privileges does this method need"* and *"what kind of
credential must I hold to call it"*. The two fields sit next to each other in one
struct and **mean opposite things**:

- **`Scoped` is a conjunction.** More than one entry means every identifier is
  required. Render it as *"grant all of these"*.
- **`Scopes` is an alternatives set.** A client carries exactly one scope, so
  more than one entry means the endpoint is published under each of them and the
  caller picks one. Render it as *"use a credential of one of these kinds"*.
  Sending a header that disagrees with the credential is
  `403 OWNERSHIP_FORBIDDEN` even within one customer, so "one of" is the whole
  point rather than a nicety.

That difference is the same shape as the mistake the section above exists for. A
consumer reading either field by analogy with the other produces a table that is
confidently wrong and type-checks perfectly: zipping `Scoped` with `Legacy`
shipped two swapped privilege labels for `RedeployJamfManagementFrameworkV1`, and
reading `Scopes` as a conjunction would tell an operator to hold two credentials
for an endpoint that refuses the second header. Nothing in the types can stop
either, which is why both are written down here.

**The three kinds and the header each travels in** — all a consumer needs in
order to act on a `Scopes` entry:

| kind | header | note |
|---|---|---|
| `ScopeTenant` | `X-Tenant-Id` | the legacy scope; still declared almost everywhere, but not the one to build on |
| `ScopeEnvironment` | `X-Environment-Id` | the scope to prefer — an environment groups a customer's tenants, and Jamf intends new integrations to be environment-scoped |
| `ScopeOrganization` | *none at all* | the gateway resolves the organization from the access token, so there is no header to send |

`ScopeOrganization` exists so that the registry can *name* that scope. Client
code has no use for it: an unset scope already means organization, so there is no
`WithOrganizationID` option and never will be.

**The set is never empty, and that is enforced rather than hoped for.**
`resolveScopeTypes` fails generation on a spec that declares no `x-scope-types`
with no `scopeTypes` override, on an unknown kind, on a non-string element, on an
override the spec has caught up with exactly, and on an override the spec has
outgrown in the widening direction. `TestEveryConfiguredSpecResolvesAScope` walks
all 19 configured specs, so an ingest that drops the extension fails a test
rather than shipping a registry whose empty slice reads as *"no scope
required"* — the same misreading that got the empty account privilege sets
published as *"any authenticated integration may call these"*. A consumer
therefore never has to interpret an empty `Scopes`, which is the opposite of
`Scoped`, where empty is meaningful and `Source` is what makes the distinction
machine-readable.

**Scope is declared per spec and stored per method.** Every method built from one
spec carries the same set, so the field looks like it should have been a
package-level accessor. It should not: two specs in one package can disagree, and
`securitycloud` did. It is built from six specs, and
`securitycloud-device-groups-api.yaml` sat at v1897 — a build predating the
environment declaration the other five carried — so a package-level answer would
have had to pick one of the two and lie about the rest of the package. That
particular divergence closed on 2026-09-04 when the hold lifted, which is the
argument for keeping the storage per-method rather than against it: the next one
needs no structural change.

**Provenance is recorded, because the value is not always the spec's.**
`ScopesSource` is `"spec"` or `"config-override"`, mirroring exactly what `Source`
does for `Scoped`, and it is never empty for the same reason `Scopes` is never
empty. Three spec entries carry an override today, all one case, through the
[`scopeTypes` spec-level key](#spec-level) — read that row for the mechanism
rather than repeating it here:

- **The account trio** declares no `x-scope-types` at all, and will not while the
  routing model does not change: these routes resolve the organization from the
  access token, which exempts them from the publishing transform that attaches
  the extension — the same structural reason they carry no
  `x-required-privileges`. Organization scope is gateway configuration rather
  than a spec claim, so config is where it is stated.
- **`securitycloud-device-groups`** was the second case, and it is the worked
  example of the mechanism expiring on its own. It was held at v1897, declaring
  `[tenant]`, while v2082 declared `[tenant, environment]` for the identical
  operation set and an environment credential demonstrably reached every one of
  its operations — so the override stated a wire-established fact rather than a
  guess. On 2026-09-04 the v2 update handler was fixed, the hold lifted, and the
  ingested spec began declaring exactly what config asserted; generation failed
  with *"delete the config entry so the spec is the only source"* and the entry
  went. Nobody had to remember it was there, which is the whole point of the
  self-expiry.

**The caveat a consumer must not lose: the registry reports the SPEC's claim, and
as of v2082 that is stricter than the gateway for the Platform APIs.** Six
Platform specs went tenant → environment-only in that build and four of the six
still answer under `X-Tenant-Id`, wire-probed 2026-09-04 with a control in the
same invocation. A consumer that reads `Scopes` as a statement about what the
server *refuses* will therefore tell callers to migrate ahead of the gateway. The
`MethodPrivileges` godoc carries the caveat inline so it reaches someone who
reads only the type; the evidence, and which four, are in
[WIRE-FACTS.md](WIRE-FACTS.md#v2082s-scope-migration-the-specs-moved-the-gateway-mostly-did-not-2026-09-04).
`TestAcceptance_TenantScopePlatformSpecsStillServed` pins all four and fails the
day the withdrawal lands, which is the notification to rewrite this paragraph.

---

## Error surface

One error type, `*APIResponseError`, for every non-success HTTP response, plus
exactly one sentinel, `ErrUnexpectedResponse`. Non-HTTP errors (denylist refusal,
context cancellation, IO failures) surface as plain wrapped errors.

**Sentinels stay a closed set of one.** An earlier crop was removed because
`errors.Is` on them was never honoured by the transport — a dead pattern.
`ErrUnexpectedResponse` earns its place on two counts the removed ones failed:

- **It is inferred, not reported.** A non-JSON body where JSON was expected means
  an edge proxy, WAF, or IP allowlist answered instead of Jamf. No status code or
  structured detail says so, so there is nothing for `*APIResponseError` to carry.
- **Callers branch on it, they don't just print it.**
  `terraform-provider-jamfprotect` looks up the host's public egress IP and emits a
  support block on this condition. A branch needs a matchable error, not a message
  to grep.

A rejected credential answers with JSON (`401 invalid_client`) and never carries
the sentinel, keeping "wrong secret" and "blocked network" distinguishable — they
need opposite remedies. The name matches `jamfprotect-go-sdk` deliberately: the
Protect provider's resources fold into `terraform-provider-jamfplatform` once
Protect has Platform API support, and a shared error surface is one less thing to
rewrite. **Prefer widening this sentinel's reach over adding a second one** —
which is what happened next.

### Widened to API response bodies

It was raised on the OAuth token exchange only, and that was an inconsistency
rather than a scope: the *same* CloudFront page arrives on either path, and got a
matchable error on one and an unmatchable one on the other. `handleResponse` now
raises it too, and the format guard this section asked for is three-way rather
than two, because "not JSON" turned out to be three different conditions with
three different remedies:

| error body | treatment | sentinel |
|---|---|---|
| Pro/gateway JSON | `Errors` from the body | no |
| Jamf Pro's own HTML "Status page" | message lifted to `Details()` | no |
| any other HTML page | condensed to headings + edge request id | **yes** |
| plain text, 401 | api-product guidance appended | no |
| plain text, anything else | raw body, untouched | no |

Three things about it that are not guessable:

- **The Classic gate is a separate predicate from the Classic scrape.**
  `isClassicStatusPage` classifies; `parseClassicErrorMessage` extracts. They are
  not the same question, and collapsing them would attach the sentinel to a
  Status page the scrape happened to get nothing out of — reporting a response
  that *did* reach Jamf as a network block, and sending the caller to their
  network team. (Note also that this template is Jamf Pro's own, not stock
  Tomcat, as `classic_errors.go` records.)
- **The HTML test is not "not JSON", and not "starts with `<`".** Classic is XML
  end-to-end, and some Classic endpoints return an XML entity echo under a 4xx,
  so the sniff requires a doctype or an `<html>` root specifically. `<?xml` must
  never match.
- **A plain-text 401 deliberately gets guidance and no sentinel.** The sentinel
  means "something in front of Jamf answered", and a consumer branching on it
  reports the host's egress IP — the wrong remedy for a missing grant, and worse
  than no branch at all. The gateway answers a rejected token and an ungranted
  api-product with the byte-identical 22-byte `Authentication failed`, so the
  response cannot distinguish them; the guidance names that rather than guessing.
  Wire evidence, including the two-credential probe that establishes it:
  [WIRE-FACTS.md](WIRE-FACTS.md#a-plain-text-401-does-not-distinguish-a-bad-token-from-an-ungranted-api-product-2026-09-11).

**An HTML body is condensed, not echoed.** The page's headings are the only part
that varies with the failure; the gateway's own 503 page is 149 lines of CSS and
ASCII art around "503 Error" and "Service currently unavailable", and CloudFront's
is 39 lines around three headings. Both had been rendering verbatim into Terraform
diagnostics and CI logs. `summarizeNonJSONError` keeps at most three distinct
headings plus any edge request id; the full page stays on `APIResponseError.Body`.

**A page the summary cannot read is named, not echoed.** An unrecognised template
yields no headings and no request id, and falling back to the raw body there put
the whole page into `Error()` on precisely the input nobody has seen before —
reintroducing the wall of text one layer down. `describeNonJSONError` is the
guaranteed-non-empty form the transport calls, and `collectTagText` flushes a
pending element at EOF and when another wanted tag opens, because the tokenizer
never invents a missing end tag: an unclosed heading otherwise swallowed every
later one and the element never flushed at all, so a page with unclosed headings
summarized to nothing and took that fallback.

**`x-amz-cf-id` is read only once the body is known to be a page.** CloudFront
sets it on *every* response including 200s, so it identifies a CloudFront request
and not a CloudFront error — reading it unconditionally would stamp an "edge
request id" onto ordinary Jamf errors.

**What this does not do: attribute the refusal to a layer.** The JSON formatting
tells (Jamf Pro pretty-prints, the gateway emits compact) stay out of the
transport, per
[WIRE-FACTS.md](WIRE-FACTS.md#diagnosing-a-refusal). "This is a web page, not an
API response" is a weaker claim than layer attribution, and one the SDK already
made on the token exchange.

`Error()` gained two fixes in the same change, both exposed by an HTML body
reaching the detail-carrying branch for the first time: the `traceId` segment is
omitted when empty (an edge block carries no Jamf traceId at all, so it rendered
`traceId  (method=…`), and that branch now prints the HTTP status *text* like the
other one, having formatted the bare code with `%d`.

Accessors: `HasStatus(code)`, `Details()`, `FieldErrors()`, `Summary()`, and
`AsAPIError(err)` for a top-level unwrap that saves callers managing `errors.As`
target pointers. Per-family dialects are in
[WIRE-FACTS.md](WIRE-FACTS.md#error-dialects).

---

## Acceptance tests

Every new generated method **must** get an acceptance test in
`jamfplatform/acc_<pkg>_test.go` (external `jamfplatform_test` package,
`//go:build acceptance`). Read-only endpoints call directly and log shape.
Mutating endpoints use a CRUD lifecycle: `t.Cleanup` defers the delete, the test
verifies the round-trip.

**Be clever about destructive endpoints — never run them against shared state.**

- **Password changes:** don't change the OAuth client's own credential. Create a
  test user via `/v1/accounts` and change its password, or call with clearly-wrong
  values and assert the API rejects.
- **Device actions** (erase, restart): target a fixture device declared via env
  var (`JAMFPLATFORM_DEVICE_ID`), or skip when unset.
- **Delete endpoints:** always pair with a preceding create in the same test.
  Never delete pre-existing resources the tenant owns.

When an endpoint can't be exercised safely, `t.Skip()` with a comment explaining
why. A skipped test still documents intent; a destructive test that corrupts the
tenant costs more than the coverage is worth.

**Never silently tolerate real errors to make a test pass.** If an endpoint
rejects a request with 400/500, fetch the full response body — not just the status
— and understand what the server is objecting to. Acceptable reactions: fix the
payload, add a `fieldTypeOverride` for a spec/server drift, or explicitly surface
the bug (leave the test failing or skipped with the server's error text captured
in a comment). **Not** acceptable: catching a category of status codes
(`>= 400 && < 500`) as a generic escape hatch. 4xx-tolerance is justified only
when the rejection is an expected property of the probe (bogus-id probes,
unconfigured-integration probes) **and that property is named in the log
message**. If a server bug blocks coverage, flag it — don't paper over it.

**Never reintroduce a blanket-403 skip.** `skipOnGatewayUnrouted` existed for
exactly two endpoints, both of which later became routed; it was deleted rather
than kept, so those tests now *fail* on a 403. That is the point — a 403
resurfacing means routing regressed or the region in use lags. Name the endpoint
and fail; a blanket tolerance is what hid the device-groups `/v2` gap for weeks.
Where a refusal is genuinely structural (no policy rule exists on any branch),
assert the 403 and fail if it ever succeeds, with a comment saying to invert the
test at that point.

**Prefer a self-provisioning fixture over a skip.** Four ZTNA tests needed a real
dedicated gateway ID and used to skip whenever the tenant had none — which is
exactly the state a clean tenant starts in, so their create paths went unexercised
precisely when coverage mattered most. `jscEnsureGateways(t, sc, n)` returns the
existing gateways or creates the shortfall (`enabled: false` with `dedicatedIps`
and no `ipsec`, the cheapest form the server accepts) and registers each for
deletion. Pre-existing gateways are used as-is and never deleted. The skip message
now names the variable that would fix it instead of an absent fixture the reader
cannot act on.

**Assert response *shape*, not just "did I get an ID".** The three ZTNA creates
silently changed what they returned; an assertion on shape caught it, and nothing
in the spec diff could have.

### Scope, credentials and gates

The suite prefers **environment** scope when a complete
`JAMFPLATFORM_ENV_*` set is configured, falling back to the tenant set — a
credential is minted against one scope and the header must match it, so this is a
choice between two integrations rather than two IDs for one. `accScopeInUse`
records which it settled on, because a silent switch between scopes is
indistinguishable from the tenant's data changing underneath the suite.

Security Cloud tests go through `accSecurityCloudClient`, because a Security Cloud
client answers 403 on `/pro` and vice versa — a second tenant ID alone is not enough.
They prefer their own credential set
(`JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_ID`,
`..._CLIENT_SECRET`, `..._TENANT_ID`, and optionally `..._BASE_URL`) and fall back to
the environment set, which also reaches `/securitycloud` (wire-verified 2026-09-03).
The tenant goes through plain `WithTenantID`: the per-namespace override this used to
exercise (`WithSecurityCloudTenantID`) went away with header scoping. Which of the two
credentials a run settled on is logged once, because the two land on different Security
Cloud tenants with different fixtures. Unset credentials skip; supplied-but-rejected
credentials fail.

Opt-in gates exist where a write provisions real infrastructure:
`JAMFPLATFORM_ACC_SECURITYCLOUD_GATEWAY_WRITE_OK` (creating a ZTNA gateway provisions real
network egress; deleting one severs traffic for every access policy routed through
it) and `JAMFPLATFORM_AIGOV_WRITE_OK` (every create leaves a permanent row,
because archive is not a readable soft delete). IPSec *rejection* cases are
ungated: they omit required top-level fields, so nothing can be provisioned even
if a server-side rule stops being enforced.

### Diagnostics

Every acceptance client constructor spreads `accTraceOpts()`, so one variable
applies to the whole suite.

- **`JAMFPLATFORM_ACC_TRACE=1`** prints each request and response to stderr —
  method, URL, an allowlisted set of response headers (traceId, `Deprecation`,
  `Link`, `Content-Encoding`) and indented bodies, capped by
  `JAMFPLATFORM_ACC_TRACE_MAX`. **`-v` is required**: without it `go test` buffers
  the binary's output and shows it only for a failing test, so a passing test's
  trace is swallowed. Headers print from a fixed allowlist rather than being
  filtered, so `Authorization` — which `LogResponse` receives in full, carrying a
  token live for 900 seconds — can never leak by accident, and a header added
  upstream later cannot either. Bodies have credential-shaped members replaced by
  name, which is best-effort: **treat a trace as sensitive.**
- **`JAMFPLATFORM_ACC_FAST_RETRY=1`** installs `WithRetryPolicy(50ms, 500ms, 2)`.
  A trace makes the retry policy look like a hang, because retries happen inside
  `retryablehttp`, *below* the SDK's `Logger`: a trace shows one request line and
  then silence for minutes. The production policy is `RetryMax=4`,
  `RetryWaitMin=1s`, `RetryWaitMax=60s`, and `RateLimitLinearJitterBackoff` treats
  the two durations as the **jitter range, not (initial, cap)**: the wait is
  `(1s + rand×59s) × attemptNum`, capped at 60s. So the *first* retry alone waits a
  median of ~30 s and four retries total a median of ~184 s. Worth considering
  separately: anyone reading `RetryWaitMin = 1s` expects the first retry to wait
  about a second, not thirty. That is a production-timing question, not a test one,
  and has deliberately not been changed.

`tools/acctargets` scopes a run per test function. Package-level scoping cannot
work — there is only one test package.

---

## Commit hygiene

- **Before every commit**, run `go fmt ./...` and `go fix ./...` on the tree,
  **and `go fix -tags acceptance ./jamfplatform/...`**. `go fmt` normalises
  whitespace; `go fix` rewrites deprecated stdlib usages (e.g. old `rand.Seed` →
  `math/rand/v2`). Both are idempotent on a clean tree; any diff they produce is
  hygiene that belongs **with** the functional change, not as a follow-up "lint"
  commit.
- **The tagged `go fix` is load-bearing for the same reason the tagged `go vet`
  is.** The acceptance suite is behind `//go:build acceptance`, so an untagged
  `go fix ./...` — which is what this rule and CI's fmt gate both ran — never
  reaches 56 of `jamfplatform/`'s files, and the rewrites pile up invisibly.
  `8a38c91` cleared that backlog: `strings.Split` → `strings.SplitSeq`, a
  hand-rolled search → `slices.Contains`, and `ptr(v)` → `new(v)` across 26
  files, none of it behavioural and none of it reachable by the untagged gate.
- **`go fix` is not idempotent in a single pass, so run it to a fixpoint.** A
  pass that stamps `//go:fix inline` on a helper only enables the *next* pass to
  inline that helper's call sites. `8a38c91` ran it once and left three more
  files to rewrite — including `min(len(x), n)` collapses that appeared only
  after the preceding pass. CI's drift gate loops for the same reason; a gate
  that ran once would fail on a tree a correct single local run produced.
- **Run `go vet -tags acceptance ./jamfplatform/` after any config or generator
  change that alters a type or a return type.** The acceptance file is behind
  `//go:build acceptance`, so `make test` and CI never compile it and a signature
  change can break it silently and stay broken. This is not hypothetical: the
  gateway/connector envelope change left seven call sites uncompilable and its
  commit message asserted the opposite, and the `$ref`-with-siblings collapse
  caught four `%q`/`%v` verbs that had been formatting `any` and were suddenly
  formatting a `*string`.
- MIT licence; copyright headers managed by HashiCorp `copywrite` (uses `--plan`,
  not `--check`).
- CI enforces that generated output is current (`git diff --exit-code -- jamfplatform/`).
