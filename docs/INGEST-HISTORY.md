# Ingest history

The build-by-build record of every GitOps bundle this SDK has evaluated,
newest first, moved out of `CLAUDE.md` on 2026-09-30 when the tree was rolled
up to v2362. **It is a record, not guidance.** The current position, the
standing spec/wire disagreements and every rule derived from this history
live in [CLAUDE.md](../CLAUDE.md); wire evidence lives in
[WIRE-FACTS.md](WIRE-FACTS.md).

Read it for *why* something is the way it is: why a hold was placed and how it
lifted, which build withdrew an operation, what a probe found when a spec first
changed. Section references below ("see above", "the holds table") are to this
document. Nothing here is edited after the fact except to append a new build
at the top.

## Builds

**v2508 (bundle dated 2026-10-21) ingested: only jpapi changed, and its one
structural change is `POST /v2/mdm/commands` returning to the spec.** Archive
SHA-256 `8d111835257284b9e85e60da027476ae58e090dd078aabdc47f2232a421ff36a`.
The other nineteen specs are `unchanged (since v2495)`; jpapi stays `11.33.0`;
`_permissions` gains `recovery-lock:execute` (and `execute` in `scopes.yaml`).
`ai-visibility`, `declarative-asset-service`, `inventory-api` and `users` are
still unmounted/ungrantable and still not carried.

- **The op is the second un-withdrawal of an MDM path** (`GET /v1/mdm/commands`
  was the first, v2121). `SendMdmCommandV2` left at v1872 (JSC-73265); it is
  back with `x-required-privileges` split four ways by `commandType`. It is
  routed on the EU gateway under both tenant and environment scope.
- **Generator work, not config.** Whitelisting it failed `validateNoUntypedFields`
  on three fields, two distinct constructs: (1) an inline `oneOf` **with a
  discriminator** as a *property* (`commandData`, 35 variants), which
  `hoistInlineObjects` only handled for `discriminator`-less refs and array
  items — it now hoists it so `extractTypes` builds the union; (2)
  `allOf [MdmCommandBase, {properties: {commandType: {nullable: false,
  example}}}]` on `Enable/DisableRemoteDesktopCommand`, where the later,
  type-less overlay won and replaced the base's `$ref` with `any` —
  `flattenAllOf` now keeps the earlier definition when the later one is
  annotation-only (`isAnnotationOnly`). Neither needs an expiry: both are
  general constructs, and no existing output changed.
- **Privileges.** `TestScopedPrivilegesUseGAVocabulary` failed on
  `recovery-lock:execute` (the map declares `recovery-lock` `{read}`);
  `make permmap` did not change the snapshot, so it is a real disagreement and
  is carried in `permissionsMapExceptions`. The spec's four privileges are one
  flat array and read as a conjunction; the wire says alternatives, so
  `config.privilegeAlternatives` marks the method: the registry gains
  `ScopedAlternatives` and the godoc says "any one of them suffices". Consumers
  of the permissions table must read the new field.
- **Two more generator mechanisms, from the follow-up fixes.** (1) Every
  discriminated union's `MarshalJSON` returned `null` when the matching variant
  pointer was nil, against its own comment promising a minimal object; it now
  falls through to `{"<discriminator>": value}`, which also changed the unions in
  `blueprints` and `securitycloud` (nil-variant case only). (2)
  `config.requiredParams` makes a list method refuse an empty parameter the
  server 400s on — `ListMdmCommandsV2`'s `filter` — before sending;
  `TestAcceptance_Pro_MdmCommandsV2FilterIsMandatory` now reads the server's own
  400 through the raw transport.
- **Wire**: see [WIRE-FACTS](WIRE-FACTS.md#post-v2mdmcommands-is-routed-again-and-authorizes-per-commandtype-2026-10-22).
- **Acceptance.** The three withdrawn tests are back in new form
  (`CommandTypeEnumMatchesServer` with a 404-versus-403 oracle, since the
  "known type ids" error is gone; `EnhancedLogCollectionCommands`), plus
  `SendMdmCommandV2UnknownDevice` and a gated `SendMdmCommandV2QueuesCommand`
  (`JAMFPLATFORM_ACC_PRO_MDM_COMMANDS_WRITE_OK`).
- **Open upstream.** The permissions map omits `recovery-lock:execute`; the
  spec's four-privilege list reads as a conjunction; `DEVICE_INFORMATION` and
  `DECLARATIVE_MANAGEMENT` answer a deterministic 500 on the simulated sandbox
  devices.

**v2495 (bundle dated 2026-10-19) ingested: `account-sso` 1.0.0 → 1.1.0 is the
only changed spec bar the newly carried `account-organization`, and its six new operations are published and unrouted.**
Archive SHA-256
`dffdd0b0282ddd30cd0d6ea266189a42b2edc6a219d032d1751307fd9fefa5d2`. Eighteen
specs and both `_permissions` files are `unchanged (since v2481)` against the
local ingest manifest. (The local manifest recorded v2481, a build this file
has no entry for; the provenance table above still reads v2362 for those
specs because their bytes did not move.)

- **`account-organization` is now carried** (reverses the v2362 hold): the
  `/organization` namespace answers on an organization credential as of
  2026-10-21 (`GET /v1/environments` 200 with 9, `GET /v1/tenants` 200 with 40,
  bogus `/organization/v1/nonsense` a repeated `403 BAD_PERMISSIONS`, control
  `/sso/v1/connections` 200). Six operations whitelisted into `account`
  (`ListEnvironments`, `CreateEnvironment`, `UpdateEnvironment`,
  `DeleteEnvironment`, `AssignTenants`, `ListTenants`). **Privileges are
  `platform-environments:{read,create,update,delete}`**, supplied by
  `config.requiredPrivileges` from the gateway's authorization policy
  (rule dated 2026-09-29: GET environments/tenants read, POST create, PUT/environments/{id} and
  PUT .../tenants update, DELETE delete). The spec carries none and
  `routes.yaml` has no `/organization` entry, and **the published permissions
  map does not declare the capability** (checked after `make permmap`) although the Account integrations UI offers it as a grantable Organization permission, "Platform Environments", create/read/update/delete (seen 2026-10-21), so
  `permissionsMapExceptions` carries four self-expiring entries; report the
  omission upstream. (This build first shipped these six with no privileges,
  after a grep that missed the policy rule because the path sat on a different
  line from the word `organization`.) CRUD wire-verified: create 201 trims the
  name and canonicalises a mixed-case label, metaRegion is fixed at creation,
  PUT is a full replacement (missing `label` is `400 FIELD_VALIDATION`), an
  out-of-enum label or region is `400 MALFORMED_REQUEST_BODY` with no field,
  an empty/unknown `tenantIds` is `400 FIELD_VALIDATION`/`UNKNOWN_TENANT`,
  unknown request fields are ignored, and every unknown id is a `500
  UPSTREAM_ERROR` where the spec declares 404. `AssignTenants` was never sent a
  real tenant: it cannot be undone and an environment holding a tenant cannot
  be deleted. Org environment count 9 before and after.
- **`ai-visibility` re-probed 2026-10-21, still unreachable**: on an EU
  environment credential that reads `/ai/governance/policies/v1/policies` at 200
  (2 policies) in the same invocation, real (`devices/count`, `devices/stats`,
  `tools/count`) and bogus paths under `/ai/governance/visibility/v1` all answer
  a repeated `403 BAD_PERMISSIONS`; `/blueprints` 200, `/devices` 200 and
  `/audit` 400 as further controls. This confirms the 2026-10-02 result. (An
  earlier probe in this session used the bare `/ai/governance/policies` as its
  control, which is not a route and 403s for every credential; it proved
  nothing.)
- **Two new `external/` families, both not carried** (added to `knownUnmapped`):
  `declarative-asset-service` (5 paths, `blueprints:*`) and `inventory-api`
  (computers and mobile-devices, `devices:read`; previously stage/dev only).
  Probed 2026-10-21 on an environment credential with a control: `/blueprints/v1/blueprints`
  answered 200, while real and bogus paths in both namespaces answered the
  plain-text `404 page not found` of an unmounted namespace, repeated.
- **`account-sso` gains a `users` tag**: `GET /v1/users` (`type` required,
  `JAMF_ID` paginated / `IDP` not), `POST /v1/users`, `GET
  /v1/users/{userId}/login-history`, `POST /v1/users/{userId}/activate` and
  `/deactivate`, `DELETE /v1/users/{userId}`; seven new schemas; richer 400/500
  response prose. No `x-required-privileges` (stripped, as for the rest of
  `account`). **Not whitelisted**: on an organization credential that reads
  `/sso/v1/connections` at 200 in the same invocation, every `/sso/v1/users`
  call (GET twice, login-history, an invalid-body POST) and the bogus
  `/sso/v1/nonsense` answer the same `403 BAD_PERMISSIONS` — the unrouted tell.
  Whitelist when the paths answer, with the privileges added to
  `config.requiredPrivileges` and checked against the permissions map.
- **Generated diff**: `api/account_sso_api.json` only (+202, the new
  components, which publishing does not prune); no Go change. Unit tests,
  generator tests and `go vet -tags acceptance` pass.
- **To report upstream**: `/sso/v1/users` published ahead of the gateway;
  `declarative-asset-service` and `inventory-api` published to `external/`
  with no prod route.

**Ingested through v2414 (2026-10-02), and nothing is held.** jpapi and
compliance-benchmarks are at v2414, the other seventeen specs at v2362, and
both `_permissions` files at v2414. **v2219 and v2417 were evaluated and not
ingested** — v2219's entire delta is outside `external/`; v2417 is a pipeline
re-run. See below.

**v2417 (bundle dated 2026-10-03) is a pure pipeline re-run of v2414, and was
not ingested.** Archive SHA-256
`2c4cd9309367ac4796e5c986cdf45d4812ae3b1bbc8d72f4488b512d6cdcfa7f`. The dry run
reports all nineteen specs and both `_permissions` files `unchanged (since
v2414)` against the ingest manifest, and the same three families unmapped
(`users`, `account-organization`, `ai-visibility`) with no new ones. Nothing
written, nothing to regenerate; the provenance table keeps v2414. Evaluated
2026-10-14, a day after Jamf Pro 11.33's planned 2026-10-13 ship date. The
EU environment answers `GET /pro/v1/jamf-pro-version` at 200 with
`{"version": "11.33.0-t1790886091046"}` (token and routing proven by that same
200), so jpapi's `11.33.0` no longer leads the server and `JamfProAPIVersion`
is correct.

*Standing pins re-run 2026-10-14 on two environments, nothing resolved.*
`TestAcceptance_` Dss, CacheSettings, ListMdmCommandsV1 (both-parameters still
`500`), Classic `PatchByName` / `PatchByIDWrites` / `GetActivationCode`,
Blueprint component round-trip and `AiGovernancePreviewHeader` all still pass
as recorded, and `AiGovernanceDeploymentReportsReferencingBlueprints` still
logs `KNOWN DEFECT` (0 blueprints reported for policies two blueprints
reference). Not exercised: `DismissAllNotifications` (destructive, opt-in),
tenant-scope (no tenant credential), account trio (no organization
credential), and the uem-connect `Accept` pin (needs the
`JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_*` set). The ZTNA gateway lifecycles
skipped on the write gate. One environment's credential answered `403
BAD_PERMISSIONS` on `/ai/governance/policies/v1/*` while a second EU credential
read it at 200 in the same invocation — a capability grant, not a regression.

*The 11.33 changelog against the ingested spec.* Removals landed (unversioned
and `/v1` inventory-preload, `/v2/environment-type`, none whitelisted). The
`404` on `/v2/oidc/dispatch` and the `500` on
`/v1/cloud-idp/{id}/test-user-membership` are declared. **Two changelog items
are not in the spec as operations:** `POST /v1/system/fedramp-initialize` (only
the orphan `FedRampBrokerInitializeV1` schema arrived) and `POST
/v1/oidc/dispatch` (the changelog lists it beside the v2). Both answer a
repeated `403 BAD_PERMISSIONS` on 11.33.0 — the unrouted tell, identical to a
bogus path in the same namespace — while `POST /v2/oidc/dispatch` reaches the
service (`400 Email address is required`). To report upstream; nothing to
whitelist.

**v2414 (2026-10-02) changed two carried specs, and published a
twenty-first family that is not carried.** Archive SHA-256
`e2eaf2e37b52305f55398b5e4e248cd601c8ad7b6c3bf7fab0da4a105dd7236d`.

- **`ai-visibility` stopped the dry run**, as the unmapped-family refusal is
  meant to: `AI Visibility - Preview`, 24 Preview `GET`s under
  `/ai/governance/visibility`, environment scope, all `ai-visibility:read`.
  The namespace is mounted, but a real and a bogus path both answer a repeated
  `403 BAD_PERMISSIONS` on an environment credential that reads
  `/ai/governance/policies` at 200 in the same invocation. The capability is
  in `_permissions` but not in the published permissions map, and the
  API-integration UI offers nothing new to grant. Recorded in `knownUnmapped`;
  [evidence](WIRE-FACTS.md#ai-visibility-is-published-and-ungrantable-2026-10-02).
- **jpapi went to `11.33.0`, and 11.33 does not ship until 2026-10-13.** No
  operation change. Eight operations gained declared error responses
  (`ApiError` bodies on 400/404/409/500, an empty 403), which the generator does
  not emit. `FedRampBrokerInitializeV1` was added, referenced by nothing; five
  schemas referenced by nothing were dropped (`EnvironmentType` and four
  `InventoryPreload*`). The generated Go is godoc only: three
  extension-attribute `ID` comments now all read "Computer Extension Attribute"
  (upstream copy-paste, wrong on `MobileDeviceExtensionAttributes`), curly
  apostrophes in `BatteryHealth`, and `UserAccount`'s `accessLevel`,
  `privilegeLevel` and `siteId` defaults. The `siteId` one is a behavioural
  claim and **already holds on the 11.32 server**: an update that omits it
  clears the site to `-1`
  ([evidence](WIRE-FACTS.md#put-v1accountsid-without-siteid-clears-the-site-2026-10-02)),
  pinned by `TestAcceptance_Pro_AccountUpdateWithoutSiteIDClearsSite`.
  `JamfProAPIVersion` reads `11.33.0` ahead of the release; taken anyway,
  since nothing else in the delta depends on the server.
- **compliance-benchmarks added `sectionName` to `RuleResult`** as required —
  a new `SectionName string`. Live: every row of
  `GET /v1/benchmarks/{id}/rules` carries it (`"System Settings"`,
  `"Auditing"`), and `TestAcceptance_Benchmark_Reporting/RulesStats` now
  fails on an empty one.
- **`_permissions`**: `ai-visibility` added to both files, and the 20
  unversioned and `/v1/` `inventory-preload` routes dropped from
  `routes.yaml`. The `/v1/` routes are still served
  ([evidence](WIRE-FACTS.md#v2414s-routesyaml-dropped-v1inventory-preload-which-is-still-served-2026-10-02)).

Verified: `go test ./...`, the generator tests, tagged `go vet`, `make lint`,
fmt and both `go fix` passes clean; live (EU environment) the benchmark,
`AccountsV1` and new `siteId` tests pass, and the tenant was re-listed clean
afterwards. Report upstream: `ai-visibility:read` is published in the specs
and `_permissions` but absent from the permissions map and ungrantable; the
"Computer Extension Attribute" description on the mobile-device schema.

**v2362 (2026-09-29) touched every spec, and three of the nineteen changed
anything.** The bundle-wide edit is `info.title` losing its ` API` suffix on
every family (`Jamf Pro API` → `Jamf Pro`, `Classic API` → `Jamf Pro Classic`,
`AI Governance Policies API` → `AI Governance - Preview`, …), which is inert to
Go and moves one line of every `api/*.json`. Stripping titles and prose leaves
**zero operation changes in any spec**, and only three schema-level deltas:

- **`blueprints` gained `com.jamf.ddm-strict` ("All Declarations")** — a
  `DeclarationsComponent` added to `Component`'s `oneOf` and mapping, plus 43
  schemas behind it covering eleven Apple declaration types. It is the first
  schema for one of the live components recorded as having none, and it is
  **live**: `GET /v1/blueprint-components/com.jamf.ddm-strict` answers 200 and
  a create carrying two declarations reads back verbatim (2026-09-30,
  EU environment credential). The service is **looser than the spec**: a
  declaration `type` the closed `oneOf` does not list answers 201 and is
  stored.
- **`uem-connect` added `30` to `deviceUnmanagedThreshold`** on `SyncSettings`
  and `ConnectorConfig` — one new constant each — **and the server refuses
  it.** Probed through the validation ordering against a bogus `configId`
  (JSC environment credential, 2026-09-30): `30` answers
  `422 "Invalid deviceUnmanagedThreshold: 30. Allowed values: 0, 1, 3, 5, 7, 14"`
  2/2, while `14` passes validation and reaches the `404` in the same
  invocation. Taken anyway, per the rule that the spec wins and a hold is not
  worth one constant: `SyncSettingsDeviceUnmanagedThreshold30` exists and a
  caller sending it gets that 422. Pinned by
  `TestAcceptance_SecurityCloudUemConnectThreshold30AheadOfServer`, which failed
  the day `30` reached the 404 (2026-10-08) and was deleted.
- **`jsc-ztna` rewrote the gateway-create rules in prose** — exactly one of
  `dedicatedIps.enabled: true` or `ipsec`, `ipsec` requiring at least one
  `availabilityZones` address, the `field` attribution that tells the three
  400s apart, and `EMPTY_AVAILABILITY_ZONES_NOT_SUPPORTED` on `PATCH`. No
  structural change; godoc only. Not probed — a ZTNA gateway create
  provisions real infrastructure.

**The ` API` rename broke all nineteen title assertions in the ingest tool,
and exposed that contains-matching was about to go blind.** The obvious
repair — drop the suffix from each fragment — leaves jpapi's `Jamf Pro` a
substring of capi's `Jamf Pro Classic`, so a capi spec under the jpapi
directory would pass. `titleOwner` now resolves a title to the row whose
fragment is the **longest** it contains, the same rule the permissions map
uses for overlapping roots, pinned by
`TestIngestRejectsANestedTitleUnderTheShorterRow` and
`TestEveryRowOwnsItsOwnTitle`.

**v2362 also published a twentieth family, `account-organization`, and it is
not carried.** The Jamf Account Organization API — six operations over
environments and tenants (`GET`/`POST /v1/environments`, `PUT`/`DELETE
/v1/environments/{id}`, `PUT /v1/environments/{id}/tenants`,
`GET /v1/tenants`) under `/organization`. The prod gateway **does not mount
the namespace**: every path answers the plain-text `404 page not found` a
nonsense namespace gets, on an organization credential that reads
`/licensing/v1/licenses` at 200 in the same invocation and gets
`403 BAD_PERMISSIONS` on a bogus path under the mounted `/licensing` — so the
404 is the routing layer, not the service. Repeated, and the same from an EU
environment credential. It also declares no `x-required-privileges`, has no
`routes.yaml` entry and no capability in the published permissions map
(refreshed 2026-09-30), so there would be nothing to source privileges from
either. Recorded in `knownUnmapped` beside `users`; ingest it when
`/organization` starts answering, taking privileges from wherever the account
trio's come from then.

**Generating `ddm-strict` found two generator gaps, both of which would have
shipped types that cannot express the payload.**

- **`declarations` generated as `[]any`**, with all eleven declaration types
  emitted and referenced by no field. The array's items are an inline `oneOf`
  with a declared discriminator, and inline items were never hoisted.
  `isInlineItemUnion` now hoists them, giving
  `DeclarationsComponentConfigurationDeclarationsItem` — and its mapping keys
  are reverse-DNS, which `unionSchema` refused. **The refusal now applies only
  to an envelope** (a schema with properties of its own): that is what keeps
  `Component` a struct with `Configuration json.RawMessage`, the shape
  `terraform-provider-jamfplatform` is built on, while a pure union — whose
  fallthrough would be a field-less struct — becomes a discriminated union with
  variant fields named after their schemas.
- **A pure `additionalProperties` schema generated as `= string`.** It reached
  `schemaToGoType`, produced a struct with no fields, and the template renders
  any field-less, enum-less type through its enum-alias branch.
  `AppSettingsPermissionDefaultsMap` and `ManagedAppExtensionConfigs` both
  came out as strings that cannot decode the object the wire carries; they are
  now `map[string]AppSettingsPermissionDefaults` and
  `map[string]ManagedAppConfig`. No other package had the shape — the
  regenerated tree moved nothing outside `blueprints`.

Both are pinned in `itemunion_test.go`, each mutation-checked, and
`TestAcceptance_Blueprint_DeclarationsComponentRoundTrip` writes through the
typed union and the map and decodes the read-back. **Neither was reachable
through an SDK method** — `Component.Configuration` is raw JSON the consumer
decodes — which is why no generated test could see either.

**Generated impact:** `blueprints` (the new types, a discriminator enum, their
lenient decoders and a round-trip test) and 2 enum constants plus ZTNA godoc in
`securitycloud`; every `api/*.json` moves its title line, `blueprints_api.json`
by the new schemas. CI parity re-checked through the `api/` fallback: identical.

**Report upstream:** `/organization` published and unmounted; uem-connect's
`30` declared ahead of the server; the
`ddm-strict` declarations `oneOf` is closed in the spec and open on the wire;
and `DeclarationsComponentConfiguration`'s example is embedded in the schema
`description`, so it renders into the type's godoc as a single line.

**v2267 (2026-09-18) is two things, and the smaller one is the one that reached
Go.** Seventeen of the nineteen specs are byte-identical to v2209 and
`_permissions/scopes.yaml` with them, so the archive's whole delta is `jpapi`,
`capi` and `routes.yaml`.

**The substantive half: `jpapi` withdrew `GET` and `PUT
/v3/sso/oidc-broker-config`** — two of the three operations v2154 added and the
gateway never routed. 704 → 702 operations, **zero schemas added, removed or
changed, and zero other operations changed semantically.** The SDK followed, and
the removal is safe in a way v1942's was not: **this is a genuine source
withdrawal rather than the publishing filter.** The pair is gone from
`external/`, `internal/stage` **and** `internal/dev` — dev's `jpapi` goes
814 → 812, and dev has carried every filtered operation in every previous
build — and from the same bundle's `_permissions/routes.yaml`, whose entire
delta is those two paths, each appearing twice (the dual-scope duplication
defect already reported). `scopes.yaml` is byte-identical, `sso-settings` being
an already-declared capability, so no `make permmap` was needed.

**The v2154 note said to watch for a policy change routing the three. It never
merged, and the spec moved instead** — `git grep oidc-broker origin/main` in the
authorization-policies repo returns nothing as of 2026-09-19. Re-probed
unrouted at v2267 before the withdrawal was taken (11.32.0 tenant, environment
scope: **403 `BAD_PERMISSIONS` 2/2**, with `GET /pro/v3/sso/dependencies` — the
same `sso-settings:read` — at 200, `GET /pro/v1/jamf-pro-version` at 200 and a
bogus path in the same namespace at 403 as controls in the same invocation). So
nothing could call them on any credential, and **no downstream consumer
referenced either method or the `OidcBrokerConfig*` types** (checked across
`terraform-provider-jamfplatform`, `terraform-provider-jamfplatform-internal`
and `jamf-cli-internal`). Generated impact: a **pure deletion** of 284 lines of
`pro` Go — the two methods, `OidcBrokerConfig`/`OidcBrokerConfigUpdate`, three
enum families and two registry entries — and 398 lines of `api/pro_api.json`.
The two `methodNotes` **failed generation** with `methodNotes names no emitted
method` the moment the whitelist entries went, which is the self-expiring
repair working as designed; the two acceptance pins went with the methods.
`DELETE /v1/notifications` survives the spec and is still unrouted — the
policy's only `/v1/notifications` rules remain `GET` on the collection and
`DELETE` on `{type}/{id}` — so its pin stays, and it is deliberately not
re-probed because the call is an irreversible bulk dismiss on a shared tenant.

**The larger half is prose, and it is the v2192 trap again: upstream prepended
`Deprecated - ` to every deprecated operation's summary, and the summary IS the
generated method comment.** 22 operations in `jpapi` and 4 in `capi` (the two
`/activationcode` verbs and both `/computers/match/*` reads), plus a
`**Deprecated:** _As of …_` paragraph prepended to 22 and 24 `description`s
respectively. Structurally nil — zero paths, zero schemas, zero operations
changed either side of it. Left alone it would have rendered
`// GetActivationCode deprecated - finds the Jamf Pro activation code.` on all
26, because `lowerFirst` lowercases the first word of whatever the summary is.

**Fixed at the generator, symmetrically to the preview strip, and the state was
already being emitted separately.** `stripDeprecatedPrefix` reads
`deprecated: true` — the structured OpenAPI field, not the text — and the
existing `Deprecated:` godoc paragraph carries the warning as before, so the
prefix is inert to Go: **zero diff in `jamfplatform/proclassic/` and zero
comment change in `pro`.** Three details worth keeping:

- **The gate is `op.Deprecated`, not the prose**, so a summary that
  legitimately opens with the word — "Deprecated fields are omitted from this
  report" — survives on an operation the spec never marked deprecated, and the
  strip can never rewrite prose on the strength of one word.
- **It deliberately leaves the 20 older inline forms alone.** Those `capi`
  summaries carry the state mid-sentence — `Finds all patches (Deprecated -
  Please transition use to …)` — predate the prefix convention and render as
  valid godoc. Only a *leading* prefix breaks the sentence.
- **The prefix is not stripped from `api/`.** The published spec carries
  upstream's `summary` and `description` verbatim, the same call the preview
  strip and `inferDiscriminator` make. `api/classic_api_resource_documentation.json`
  moves 56 lines and `api/pro_api.json` 106 summary/description lines for that
  reason alone.

`deprecated_test.go` pins the separator variants (hyphen, colon, en and em
dash, no space), the two gates, the inline forms, composition with the preview
strip, and — the assertion that would have caught this build unaided — that
**no generated method comment in any package contains a lowercased state prefix
inside its verb phrase.** That last test is the general guard: the next state
prefix upstream invents fails it rather than shipping.

**v2219 (2026-09-17) is a non-production host migration and was not ingested —
and it is the first build whose delta is entirely *outside* `external/`.** Every
one of the nineteen per-family specs in `external/` is byte-identical to v2209,
`_permissions/{routes,scopes}.yaml` included, so the dry run reported all
twenty-one rows `unchanged (since v2209)` and there was nothing to write. Note
the shape: **the fifth build with nothing in it for the SDK** — after v1725,
v2024, v2100 and v2137 — **but the first of them that is not a pipeline
re-run.** Real work shipped; none of it reached the published tree.

What moved is `servers` and `tokenUrl` in **all 22** `internal/stage` specs and
all 22 `internal/dev` ones:
`https://{region}.api.stage.platform.jamflabs.com/api/{namespace}` →
`https://{region}.integrate.api.stage.platform.jamflabs.com/{namespace}`, and
the same substitution for dev, with the region enum narrowing `us1` → `us`.
`MANIFEST.md` states it outright
("**Staging**: `integrate.api.stage.platform.jamflabs.com`").
A parsed comparison with `servers`, `tokenUrl` and dev's `x-generated` stripped
leaves **all 44 specs semantically identical** to v2209, so that is the whole
change and there is no content hiding behind it. The stage and dev hosts now
carry prod's GA shape — no `/api` segment, `us` not `us1` — which retires a
standing note in the URL section above.

Nothing was probed for it and nothing needed to be: the SDK never reads
`servers`, prod's host did not move, and the blueprints lane was re-run green
against the live gateway anyway on the v1439 principle that a quiet diff is not
a quiet build.

**v2209 (2026-09-17) is `blueprints` and nothing else, and the whole delta is
one declared response.** Every other file in `external/` is byte-identical to
v2204 — `jpapi`, `capi`, both account specs and
`_permissions/{routes,scopes}.yaml` included — so the only other diffs in the
archive are the manifest and the two unified rollups. `internal/stage` and
`internal/dev` carry the identical delta (each differs from `external/` only in
`info.title`, the host, the `tokenUrl`, the region enum and dev's `x-generated`
block), so this is not an environment rollout.

The change is **a `429` added to `GET /v1/blueprints/{blueprintId}/report`**,
`"The reporting is overloaded, retry the request later"`, bodied with the
existing `ApiError`. Zero paths, zero schemas, zero operations, zero
parameters, and no other response on any operation touched. Generated impact:
**10 added lines of `api/blueprints_api.json` and zero Go** — the generator
reads the success response for the return type and does not emit declared error
statuses.

**Nothing was needed for it, and that is worth stating rather than assuming.**
`internal/client/retry.go` already treats 429 as retryable on every method
(`isRetryableStatus`), and `jamfBackoff` already honours a `Retry-After` in
both seconds and date form, clamped to `maxWait` so a hostile header cannot
stall past what callers were promised. Both are pinned in `retry_test.go`. So
this is upstream declaring, on one path, the first rate limit the SDK has ever
had a spec for — and the transport was written for it in advance.

**The 429 is deliberately not wire-probed.** Inducing it means hammering a
report endpoint whose own description says it is overloaded, and ~150 probe
requests against an edge block have already cost this project a second, broader
outage. `GetBlueprintReport` has acceptance coverage on the 200 path
(`acc_blueprint_test.go`), which is the half that can be exercised without
manufacturing the fault. The claim left open is whether the service sends a
`Retry-After` when it does 429: if it does not, `jamfBackoff`'s standing note
— that the deterministic backoff is tolerable only while no Jamf path
rate-limits — is the thing to revisit, by jittering around the exponential
band rather than returning to the linear variant.

**v2204 (2026-09-15) is `account-partners` and nothing else, and it is two
genuinely new operations.** This section previously said v2204 had no write-up
because its archive was not to hand when v2209 was ingested; the archive was
located on 2026-09-18 and the build is characterised here. `info.version` goes
**1.0.0 → 1.1.0**, and against v2192 the spec gains
**`POST /v1/deal-registrations`** and
**`GET /v1/deal-registrations/{partnerRegistrationId}`** plus the two schemas
behind them (`DealRegistrationCreate`, `DealRegistrationCreated`) — 7 → 9
operations, 18 → 20 schemas, **zero** existing operations or schemas changed.
Everything else in `external/` is byte-identical to v2192, `jpapi`, `capi`,
both other account specs and `_permissions/{routes,scopes}.yaml` included.

Both are whitelisted, per the house rule that a published operation is
generated and its refusal pinned rather than left out. They bring the `account`
package to **20 methods**, which is why
`TestAccountRegistryPrivilegesComeFromGatewayPolicy` now parses 20 registry
entries rather than 18. Privileges come from `config.json`'s
`requiredPrivileges` like the rest of the package —
`deal-registration:{read,create}` — and needed no `make permmap`: the committed
permissions map already publishes both actions, so
`TestScopedPrivilegesUseGAVocabulary` passed untouched.

**Wire-classified 2026-09-18, and both new operations answer a deterministic
`500 UPSTREAM_ERROR` "The request could not be completed".** Controls in the
same invocation: `GET /partners/v1/deal-registrations` answers 200
`{"totalCount":0,"results":[]}`, `GET /licensing/v1/licenses` answers 200, and
a bogus path in the same namespace answers `403 BAD_PERMISSIONS`. So they are
**routed and authorized** — the 403 is the unrouted tell and this is not it —
and the fault is behind the gateway. 3/3 on the item read, 3/3 on the create,
the create with an empty body and a plausible fully-populated one alike, which
is what rules out field validation: **the create never reaches it**, so nothing
was created and there is nothing to clean up.

**It is a different upstream from the distributor one, and the two must not
share a matcher.** The five distributor operations answer `400` with the fault
*attributed* — "… via Skyway distributor service" — while these two answer
`500` with no attribution at all. `isUnattributedUpstreamFault` is therefore
separate from `isSkywayScopeFault`, so a fix to either cannot read as a fix to
both.

**The one claim a single credential cannot settle**, and the test says so
rather than guessing: whether the 500 is a service fault or the answer to an
organization that is **not a registered reseller partner**. An empty-but-200
collection is consistent with either, and settling it needs a real partner
organization's credential. Report upstream either way — a 500 is the wrong
answer to "you are not a partner", and the spec declares 401/403/404/500 with
no unregistered-partner case.

**Two spec observations worth reporting with it.** `DealRegistrationCreate`
declares **no `required` set at all**, so every one of its 20 fields generates
as a pointer and the SDK cannot tell a caller what a valid submission needs —
and neither can the server while it 500s before validating. And the create's
declared `href` example names `account.jamf.com/api-external/…`, not the
gateway, which is the same not-callable-by-this-SDK shape already recorded for
the App Installers create; `DealRegistrationCreated.ID` is the field to use.

`TestAcceptance_AccountDealRegistrationItemAndCreate` **asserts** both 500s
rather than calling `skipOnServerError` — that helper is right for a transient
5xx and precisely wrong for a permanent one, since a skipping test can never
report the fix — and each branch names what to write in its place. It also
fails loudly if the create ever *succeeds*, because a deal registration has no
delete on any published surface and a run that submits one to Jamf has left a
permanent record.

**v2192 (2026-09-14) is `ai-governance` and nothing else, and it is the second
consecutive build that is only that spec.** Every other file in `external/` and
`internal/stage` is byte-identical to v2176 — `_permissions/{routes,scopes}.yaml`
included, `jpapi` and `capi` included — so the only other diffs in the archive
are the manifest and the two unified rollups. `internal/stage` and
`internal/dev` took a delta identical to `external/`'s, so this is not an
environment rollout.

Structurally the spec is nil: **zero paths, zero schemas, zero operations, zero
parameters, zero responses.** A prose-stripped comparison of every schema and
every operation leaves only three things, and the first is the one that
mattered:

- **All twelve operation `summary` strings gained a `Preview - ` prefix** —
  and the summary *is* the generated method comment, so unlike v2176's
  description-only change this one reaches Go. Left alone it renders as
  `// ListPolicies preview - List active AI governance policies for the
  tenant.`, a broken doc comment on every exported method in the package,
  because `lowerFirst` lowercases the first word of whatever the summary is.
  Fixed at the generator: see below.
- **`x-preview: true` added to each of the twelve operations.** The spec
  already carried a document-level `x-preview`; this makes the marker
  per-operation, which is what gives the generator a structured source for the
  state instead of a prose prefix.
- **`x-preview-owners: [ai-policy-builder-backend]` deleted from the document
  root** — an internal service name that `api/ai_governance_policies_api.json`
  had been publishing to consumers. Good removal; nothing read it.

`info.description` also gained a paragraph restating the GA date and asserting
that "every successful (2xx) response carries a `Jamf-Preview: true` header".

**The generator now treats preview as a state, not as part of the verb
phrase**, exactly as it already treats deprecation. `isPreview` reads
`x-preview` (via a new `boolExtension`, which `isRateLimited` now shares),
`stripPreviewPrefix` takes the prefix off the summary, and a separate godoc
sentence carries the warning:

```go
// ListPolicies list active AI governance policies for the tenant.
//
// Preview: this endpoint is marked preview in the Jamf API spec; its request
// and response shapes may change without warning, and successful responses
// carry a Jamf-Preview: true header.
```

Two details worth keeping. **The strip is gated on the extension, not on the
text**, so a summary that legitimately begins with the verb — "Preview a report
before sending it" — survives on an operation the spec never marked preview,
and the strip can never silently rewrite prose on the strength of one word.
And **the prefix is not written into `api/`**: the published spec carries
upstream's `summary` and `x-preview` verbatim, the same call
`inferDiscriminator` makes. `preview_test.go` pins the separator variants
(hyphen, colon, en and em dash, no space), all four extension
representations kin-openapi hands back, and the generated godoc line count per
file.

**Generated impact: 24 added lines of godoc across the two `aigovernance`
files, 65 changed lines of `api/ai_governance_policies_api.json`, and no
change to any signature, type or URL.** CI parity was re-checked through the
`api/` fallback after the generator change and the tree is identical.

**The header claim is wire-verified, and it needed a test the generated
surface cannot provide.** `GET /v1/tools` and `GET /v1/policies` both answer
**200 with `Jamf-Preview: true`** (2026-09-14, EU environment credential, a
bogus path in the same namespace returning 403 as the control). The transport
discards response headers on success, so no caller and no generated-method
test can see it; `TestAcceptance_AiGovernancePreviewHeader` goes through
`Transport().HTTPClient()` and stamps the scope header itself from
`Client.Scope()`, since `setScopeHeader` runs inside `Do` rather than in a
RoundTripper. It **asserts** the header rather than logging it: the day it
stops arriving is the day these endpoints have graduated, and that should fail
here.

The rest of the lane is unchanged from v2176 — 3 tools, 2 policies, all twelve
read rejections with their recorded codes, and the `GetPolicyDeployment`
blueprint-reference defect still reporting 0 for two policies two blueprints
actually reference. The write lane was also run at v2192 (create, the
`NO_DRAFT_TO_PUBLISH` 409, wholesale settings replacement, both `If-Match`
forms conflicting on a stale version, rename, archive-then-404) and passes
whole.

**v2176 (2026-09-11) is `ai-governance` and nothing else, and the change is one
sentence repeated twelve times.** Every other file in `external/` and
`internal/stage` is byte-identical to v2154 — `_permissions/{routes,scopes}.yaml`
included, `jpapi` and `capi` included — so the only other diffs in the archive
are the manifest and the two unified rollups, whose counts did not move (837
paths, 1314 schemas). All 22 `internal/dev` specs report as changed and that is
the `x-generated` block alone: stripping it leaves every one except
`ai-governance` semantically identical to v2154.

The whole delta is a **`**Preview endpoint.** Expected to reach general
availability by 2027-03-03, pending feedback on request and response shape.`
paragraph prepended to all twelve operation `description`s. Structurally it is
nil: zero paths, zero schemas, and a prose-stripped comparison of every schema
and every operation is empty. `internal/stage` and `internal/dev` took the
identical text, so this is not an environment rollout.

**Generated impact: 12 lines of `api/ai_governance_policies_api.json` and zero
Go**, because method comments come from the operation `summary` and never its
`description` — the same reason v2005's rewritten `updateSyncSettings` text
landed on no method. There is nothing on the wire to probe in a GA-date claim,
so the check that mattered was the v1439 one — that a cosmetic diff is not a
quiet build — and the ai-governance read lane passes unchanged: 3 tools, 2
policies, all twelve read rejections with their recorded codes, and the
`GetPolicyDeployment` blueprint-reference defect still reporting 0 for two
policies two blueprints actually reference.

**Two out-of-band wire findings came out of the same session, neither from the
bundle.** Both are recorded with payloads in `docs/WIRE-FACTS.md`:

- **`GET /pro/v1/dss-declarations/{declarationId}` is routed now and 500s for
  every identifier**, including a live declaration identifier `ddm/report`
  resolves at 200 in the same invocation. It has been recorded as *unrouted*
  since 2026-08-31, and the pin had been **skipping** past the change since it
  landed, because `skipOnServerError` ran before the routing check. The test is
  renamed `TestAcceptance_Pro_DssDeclarationsBrokenForEveryIdentifier` and now
  asserts the 500 — and separately fails if it ever returns to
  `BAD_PERMISSIONS`, which would be an un-routing rather than this fault.
  Evidence:
  [WIRE-FACTS.md](WIRE-FACTS.md#get-v1dss-declarationsdeclarationid-is-routed-now-and-broken-for-every-identifier-2026-09-11).
- **Blueprints do not support sites, and the answer is not "not yet".** No site
  or division field exists on the API in either direction, the spec is
  byte-identical v2082 → v2176 in all three environments, and no Platform spec
  mentions sites at all. A Jamf Pro site reaches the platform as a **division**
  (`Site.divisionId`, `AuthToken.currentDivisionId`), and blueprints refuse to
  touch one: `PATCH` with `divisionId` is `400 DIVISION_ASSIGNMENT_NOT_ALLOWED`
  for a value and for `null` alike, checked *before* body validation, while
  `POST` silently ignores it. The trap for anyone probing this is that the
  create ignores unknown fields entirely, so **a 201 is not evidence and the
  read-back is the only oracle**. Same session established that a blueprint
  created with `steps: []` — which the create explicitly allows — can never be
  patched, since merge-patch validates the merged entity against an undeclared
  `steps` minimum of 1; `TestAcceptance_Blueprint_EmptyStepsCannotBePatched`
  pins it. Both, plus the create `href` naming an internal tyk host:
  [WIRE-FACTS.md](WIRE-FACTS.md#blueprints-blueprints--environment-scope).

**v2137 (2026-09-10) is a pure pipeline re-run and was not ingested — the
fourth recorded no-op build.** Every per-family spec in `external/` and
`internal/stage` is byte-identical to v2121, `_permissions/{routes,scopes}.yaml`
included; the only diffs in either tree are the two unified rollups. The whole
11.32.0 change landed at v2154, five hours later the same day. Note the shape of
that: **two builds on one day, the first inert and the second substantive**,
which is exactly why step 1 of the ingest is to hash and dry-run rather than to
read.

**`jpapi` gained three genuinely new operations and the gateway routes none of
them.** `DELETE /v1/notifications` (bulk dismiss, `dismiss-notifications:execute`),
`GET` and `PUT /v3/sso/oidc-broker-config` (`sso-settings:{read,update}`). 701 →
704 operations. All three are whitelisted, per the house rule that a published
operation is generated and its refusal pinned rather than left out.

Wire-classified 2026-09-10 under environment scope with a 200 control and a
bogus-path 403 in the same invocation, each refusal reproduced: all three answer
**403 `BAD_PERMISSIONS`**, the unrouted tell. **The credential is short of
neither capability** — `GET /v3/sso/dependencies` (same `sso-settings:read`)
answers 200 and the routed item-level
`DELETE /v1/notifications/{type}/{id}` (same `dismiss-notifications:execute`)
answers 204 — so a sibling path settled the classification and a second
credential was not needed.

**The gateway's authorization policy explains it and the fix is open.** Its
`main` has no rule for any of the three, and a policy change opened 2026-09-10
adds exactly those three, its own body stating that without them "the endpoints
publish in docs but 403 for every caller". So this is a known gap awaiting a merge and a
deploy, not a spec/wire disagreement. Both pinning tests fail the day it lands
and each names the coverage to write in its place. Evidence:
[WIRE-FACTS.md](WIRE-FACTS.md#v2154s-three-new-jpapi-operations-are-published-and-unrouted-2026-09-10).

**Everything else in `jpapi` is additive, and one addition was a shipped
break.** 66 new struct fields, zero removed, **zero type changes** — verified
field-by-field across all 4127 `pro` struct fields, so no pointer-ness moved and
nothing downstream breaks. The new properties are `appleEnrollmentType` (a
five-value enum), `awaitingConfiguration`, `lockdownModeEnabled`,
`returnToServiceEnabled` and a `systemHealth` sub-object, spread over 15 computer
and mobile-device read schemas; all are optional and none is reachable from a
request body, which is why they generate as non-pointer response fields. RSQL
filter vocabularies gained the three booleans. `UserAccount.accountType` gained
`MIGRATED`.

**~~The exception is `AccountPreferencesV6.showDirectoryGroupUuidColumn`~~ —
expired 2026-09-11, and the way it expired is the reusable part.** v2154 added
it as `required` against a server that did not have it. Required means
non-pointer with no `omitempty`, so *every* `UpdateAccountPreferencesV3` call
sent the key and every one failed — the same shape of defect as
`CreateInventoryPreloadHistoryNoteV1`'s wrong `expectedStatus`. `propertyRemovals`
dropped the property and a docNote carried the evidence.

**The server caught up, and the removal is gone.** Wire-verified 2026-09-11 on
a **11.32.0** tenant with `GET /pro/v1/jamf-pro-version` as the control in the
same invocation: the `GET` returns **27 keys** including the field, a `PATCH`
setting it answers 204, and — asserted rather than assumed — the value **reads
back**, `false → true → false`. Omitting the key leaves it untouched, so this
`PATCH` merges. `propertyRemovals`, the docNote and the limitation test are all
deleted; the generated type is 27 fields and `api/pro_api.json` publishes 27/25,
matching upstream exactly.

**The self-expiry was in the acceptance suite, not in config, and that is the
distinction worth keeping.** `propertyRemovals` panics when the *spec* stops
declaring the path, which is the wrong trigger: the event to wait for was the
*server* catching up, and no config mechanism can see that. The test was the
only thing that could, and it is what fired.

**Following the spec here costs pre-11.32 callers this one method, deliberately.**
The field is required, so the SDK now sends it unconditionally and an 11.31
tenant answers `400 [INVALID_CONTENT] Unrecognized field
"showDirectoryGroupUuidColumn" (class …AccountPreferencesDtoV6), not marked as
ignorable` — re-confirmed 2026-09-11. There is no config key that forces a
declared-required property optional, and inventing one for a niche
per-credential preferences write was not worth it: no consumer calls
`AccountPreferences` (checked across `terraform-provider-jamfplatform`), so the
blast radius is the SDK's own method. Per the spec-wins rule, the spec is
followed and the cost is recorded.

**`TestAcceptance_Pro_AccountPreferencesShowDirectoryGroupUuidColumn` is
version-gated, and both halves are assertions.** The CI matrix holds tenants at
both versions at once, so a test asserting either behaviour unconditionally
fails on the other and the failure reads as a defect rather than a rollout.
`proServerAtLeast(t, c, 11, 32)` picks the branch: at or past 11.32 it
round-trips the field, below it asserts the absence and the refusal. The
pre-11.32 branch **fails the day its tenant rolls forward**, which is the
notification to delete it — at that point the gate is dead code. Both branches
were run against real tenants on 2026-09-11 and both pass. `proServerVersion` /
`proServerAtLeast` in `acc_helpers_test.go` are new and general: any future
spec-ahead-of-server property wants the same treatment.

**It also exposed a latent generator bug.** `applyPropertyRemovals` deleted the
property and left its name in the parent's `required`, so `api/pro_api.json`
would have published a schema requiring a property it does not declare — an
invalid spec handed to consumers. It now prunes `required` too, pinned by
`TestApplyPropertyRemovalsAlsoDropsTheRequiredEntry`; the function had **no test
at all** before this.

**`external/jpapi` also stopped pruning unreachable schemas, and that is inert
to the SDK.** 698 → 767 component schemas while only **four** more became
reachable from a published operation (`OidcBrokerConfig`,
`OidcBrokerConfigUpdate`, `SystemHealthV2`, `MobileDeviceSystemHealth`);
orphans went 53 → 118. The 65 new orphans are the schemas the `internal/dev`-only
operations reach — the whole `MdmCommand*`/`ApiRole*`/`ApiIntegration*` set, plus
`InitializeV1` and `PlatformInitializeV1`, whose *operations* v1897 withdrew and
which have **not** come back. `external` and `internal/dev` now carry the
identical 767 schemas against 704 and 814 operations, so the publishing filter
strips the operations and no longer prunes what they orphan. The generator's own
reachability pruning absorbs it, so there is zero Go diff and `api/pro_api.json`
does not grow. Worth reporting as a pipeline regression.

**`capi`'s entire delta is a deprecation flag on both `/activationcode` verbs**
— `deprecated: true` plus `x-deprecation-date: 2026-07-14`, no
`x-successor-endpoint`. Zero operations, zero schemas, zero prose. It was
**held at v2121 and then taken on instruction**, and the reason for the initial
hold is the thing to carry forward rather than the outcome: **`GET
/activationcode` is deprecated with no successor in any environment.** `jpapi`
declares `PUT /v1/activation-code` and `PATCH /v1/activation-code/organization-name`
but **no `GET`** — checked in `external`, `internal/stage` and `internal/dev`,
all three — and no other `capi` operation returns the `activation_code` schema.
So the SDK now ships a `// Deprecated:` marker on a read with nothing to migrate
to, which is what the hard rule below exists to prevent: staticcheck's SA1019 is
on by default, and `terraform-provider-jamfplatform`'s `activation_code`
resource **and** data source rest entirely on `GetActivationCode` (read) and
`UpdateActivationCode` (write). Expect that build to go red. The write is
migratable to `UpdateActivationCodeV1`; the read is not. **Report the missing
Pro-API read upstream** — a deprecation with no successor is the defect, not the
SDK's reaction to it. The endpoint is live: `GET /proclassic/activationcode`
answers 200 with real data (wire-checked 2026-09-10).

**The privilege oracles all agreed and needed no refresh.** `routes.yaml` is
purely additive and its whole delta is the three new operations (each appearing
twice, which is the dual-scope duplication defect already reported);
`scopes.yaml` is byte-identical, because `dismiss-notifications` and
`sso-settings` were already declared capabilities; and the committed permissions
map already publishes `dismiss-notifications:{x}` and `sso-settings:{r,u}`, so
`TestScopedPrivilegesUseGAVocabulary` passed without `make permmap`.

**One ingest-tool footgun was fixed because it fired during this ingest.**
`-only <spec>` did not gate the `_permissions` copy, so restoring `capi` from
the v2121 archive with `-only` silently took `routes.yaml` back to v2121 with
it. Those two files are not spec-scoped, so a narrowed run now reports them and
writes neither, pinned by
`TestOnlyRunReportsPermissionsWithoutWritingThem`.

**v2121 (2026-09-09), retained.** Two specs moved
and everything else in `external/` is byte-identical to v2082, so the only
other diffs in the archive were the manifest, the two unified rollups, and
`_permissions/routes.yaml` — which is byte-different and **`sort`-identical**
again, the sixth build in which that has happened. `scopes.yaml` is unchanged.
`internal/stage` matches `external/` op-for-op, so neither change is an
environment rollout; `internal/dev` carries 811 `jpapi` operations against
`external/`'s 701, so the v1942 publishing filter is still in place and still
prod-only.

**`jpapi` restored `GET /v1/mdm/commands` — the second un-withdrawal of a
v1942 removal**, after v2082's 13 `/v3/computers-inventory` operations. 700 →
701 operations, **zero schemas added or changed** (`MdmCommand` never left the
spec), and the recovered `config.json` entry is the pre-v1942 one verbatim, so
this is a revert rather than a re-derivation. Wire-confirmed 2026-09-09: it
answers 200 with a bare array of real commands under both parameters, and it
is a **two-parameter point lookup, not a paginated list**, so it is not
reachable through `ListMdmCommandsV2` and needed its own method. It still
sends `Deprecation: 2023-10-16`, which the transport logs.

**Two of its wire laws are not in the spec, and one of them is a defect.**
Neither parameter is `400` with an **empty `errors` array**, although the spec
marks both optional; **both parameters together is `500`**, deterministic 2/2,
although the spec's own wording ("choose one of two parameters, but not both")
describes a 400. 41 uuids is `414 INVALID_SIZE` as declared.
`TestAcceptance_Pro_MdmUpdates_ListMdmCommandsV1` **asserts** the 500 rather
than skipping on it, so it fails the day the fix lands — `skipOnServerError` is
right for a transient 5xx and precisely wrong for a permanent one. Report both
upstream. Evidence:
[WIRE-FACTS.md](WIRE-FACTS.md#v2121-restored-get-v1mdmcommands-and-it-has-two-wire-laws-the-spec-does-not-state-2026-09-09).

**`ai-governance` gained an optimistic-concurrency mechanism, and it is fully
enforced on the wire.** `PolicyDetail.version` (nullable `int64` → `*int64`),
an `ETag` response header on the detail `GET`, an `If-Match` request header on
`PATCH`, a `409` on that `PATCH`, and a `Jamf-Preview: true` response header
declared on every 2xx of all 12 operations. `ApiError.httpStatus` also became
declared-and-required, which the wire has always sent.

**Nothing is needed for the read half: the ETag's value *is* `version`.**
Verified `"0"`↔`0` and `"1"`↔`1`, so a caller formats the field rather than
reading a response header, and the transport needs no header plumbing. The
`409` already surfaced through `APIResponseError.Details()` as
`POLICY_VERSION_CONFLICT`, so the error surface needed nothing either.

Wire-verified 2026-09-09 with a control in the same invocation: a policy
created during the probe answered `ETag: "0"` with `"version": 0`; `If-Match:
"999"` and `W/"999"` both got **409 `POLICY_VERSION_CONFLICT`**, the current
value got 204, and `*` or an absent header updated unconditionally. **Every
`PATCH` increments `version` whether or not it changes anything**; publishing
does not, so `version` and `currentVersionNumber` are independent counters.
`If-Match: garbage` is `400 VALIDATION_FAILED`, so the header is parsed rather
than ignored. Both **pre-existing** policies answer `"version": null` and send
no `ETag` at all — which is the spec's documented "legacy document that
predates versioning", so a caller cannot conditionally update one.

**Sending `If-Match` needed the generator to learn header parameters, and it
now has**, via a `headerParams` config key. `UpdatePolicy` takes an `ifMatch`
string and routes through the new `Transport.DoWithOptions`. Wire-confirmed
that the server compares the *number* and accepts the bare, strong and weak
validator forms alike, so a caller needs no ETag quoting —
`strconv.FormatInt(*policy.Version, 10)` is a valid precondition, and a
comma-separated list is the only form refused.

**It must be a separate key from `params`, not a flag on it.**
`collectSpecParams` keys every parameter by wire name regardless of where it
travels, so `"params": ["If-Match"]` matches the spec, passes the name-match
check and emits `?If-Match=…` — a query key the server ignores, turning a
conditional update into an unconditional one with nothing failing at any layer.
Both directions are now refused, along with the scope headers, a non-`string`
type, and a template with no headers form. Mechanism and the four refusals:
[docs/STYLE.md](STYLE.md#header-parameters); wire evidence:
[WIRE-FACTS.md](WIRE-FACTS.md#v2121s-optimistic-concurrency-mechanism-is-live-and-the-sdk-cannot-reach-half-of-it-2026-09-09).

**Three other header parameters were latent in `jpapi` and came with it, and
one was a shipped break.** `ExportPatchSoftwareTitleReportV3` declares an
`accept` header selecting `text/csv` or `text/tab` — and **without it the
endpoint answers 400**, so that method had failed every call it ever made.
Worse, the acceptance suite tolerated the 400 under a wrong diagnosis recorded
since 11.30.2 ("a property of an empty patch report"); the probe behind it
never set the header. With `Accept` set, a zero-row report exports fine, and
the two media types genuinely differ — same 21 bytes, comma versus tab, so TSV
was unreachable. `Accept-Language` on both `/v3/account-preferences`
operations is now reachable and inert on the wire. Both, plus the corrected
`columns-to-export` reasoning:
[WIRE-FACTS.md](WIRE-FACTS.md#export-report-was-never-callable-and-accept-is-why-2026-09-09).

**An `Accept` header's allowed values are documented from the response, not the
parameter.** OpenAPI models acceptable media types structurally on the
response, so `responses.200.content` *is* the enum while the parameter beside
it is a bare string described as "File." — `acceptHeaderDocLines` reads the
vocabulary off the response and defers to a real enum if a bundle adds one. No
header parameter in any of the 19 specs declares an enum of its own.

**Both account holds stand at v2121, unchanged from the 2026-09-09 re-probe.**
`License.type` is still absent from the published spec and
`DomainAllocationConnection.authZeroRegion` still renamed to `authRegion`, so
neither spec's delta has moved and there is nothing new to weigh.

**v2100 (2026-09-08) is a pure pipeline re-run and was not ingested — the
third recorded no-op build.** Every per-family spec in `external/` is
byte-identical to v2082, held rows included, and so is
`_permissions/{routes,scopes}.yaml`. The only diffs in the whole archive are
the manifest's build number, source commit and timestamp,
`info.version: auto-v3.2082` → `auto-v3.2100` plus a `Generated` line in the
two unified rollups, and the `x-generated` block in each of `internal/dev`'s
22 specs — stripping that block leaves all 22 semantically identical to
v2082. `internal/stage` took the same nil delta. Nothing to ingest, no
generated diff, no probing warranted. Note the five-day gap: build recency is
not evidence of spec movement, which is why step 1 of the ingest is to hash
and dry-run before reading.

**v2082 (2026-09-04) is where the current hold position was set**, and two
holds went in that change. `capi`'s is gone because v2082 republished the
patch-management family it was protecting. `securitycloud-devices`' is gone
because `PUT /v2/groups/{groupId}` started working on 2026-09-04, between
12:51 and 13:33 BST, which was the one condition its row named — so the two
v1 operations v1942 withdrew no longer cost a capability and the spec is
ingested at v2082. Seventeen of the nineteen specs are now at v2121 and the
other two at v2082; only the two account specs are held.

**v2082 is the scope-declaration build, and it is two things: a bundle-wide
scope migration that is inert to generated Go, and two operation
restorations that are not.**

Structurally the bundle looks alarming — every operation in every spec
reports as changed — and it is one edit repeated 775 times. Stripping the
scope parameters from each operation leaves **zero** operations changed in any
of the twelve ingested specs, so the whole of that churn is
`components.parameters` plus one `$ref` per operation. The generator never
emits scope headers as parameters (the transport stamps them) and never reads
`x-scope-types`, so the Go diff from it is nil; what it does change is the
`api/*.json` a consumer reads, 14–68 lines each.

**Six Platform specs went tenant → environment-only, and four of the six are
still served under tenant scope.** `blueprints`, `device-groups`, `devices`,
`device-management-action`, `declaration-reporting` and
`compliance-benchmarks` now declare `x-scope-types: [environment]` with
`X-Environment-Id` `required: true` and no `X-Tenant-Id` parameter at all
(two upstream spec changes — "Platform endpoints are environment-scoped
only", sourced from the published permissions map). Wire-probed 2026-09-04
with a tenant credential and `GET /pro/v1/jamf-pro-version` at 200 as the
control in the same invocation: `devices` and `device-groups` answer **200**
under `X-Tenant-Id`, `declaration-reporting` answers its own `400` on a
missing RSQL filter and `device-management-action` a service `404` — both past
the gateway — while a bogus path in the same namespace returns the unrouted
`403 BAD_PERMISSIONS`. So the specs are ahead of the gateway again, and a
consumer told by the spec to migrate is acting on a claim the server does not
yet make. `TestAcceptance_TenantScopePlatformSpecsStillServed` pins all four and
**fails the day the withdrawal lands**, which is the notification to change
the package table below.

`blueprints` and `compliance-benchmarks` are the two the probe could not
settle: both answered `403 BAD_PERMISSIONS` to that tenant credential, and
with one tenant credential that cannot be told apart from an ungranted
capability — classifying a 403 takes two credentials, not two paths. It
agrees with the separately recorded GA decision that those two are
environment-only, so they are deliberately left unpinned rather than pinned
on a guess. `compliance-benchmarks` additionally answered `500 "Upstream host
lookup failed"` 2/2 to the *environment* credential, which is an infra fault
on that environment rather than a scope answer.

**`jpapi`, `capi` and all six Security Cloud specs went tenant → tenant *and*
environment**, both headers `required: false` with "Send exactly one scope
header per request." Wire-confirmed 2026-09-04 under `X-Environment-Id`:
`/pro/v3/computers-inventory`, `/proclassic/patchsoftwaretitles`,
`/securitycloud/v1/categories`, `/securitycloud/v2/groups` and
`/securitycloud/uem-connect/v1/connectors` all 200. The dual declaration is
correct, and the existing `TestAcceptance_EnvironmentScope` /
`TestAcceptance_TenantScope` lanes already cover both directions.

**`_permissions/routes.yaml` doubled in size and every domain is now typed
`environment` — including the tenant half of each dual-scope API, which is an
upstream defect worth reporting.** 6357 → 12367 lines, 18 → 26 domain blocks,
zero `tenant` blocks where there were 16. A dual-scope spec emits **two
blocks, byte-identical in their routes and both typed `environment`** — `pro`
twice at 506 routes each, `proclassic` twice at 273, `securitycloud` twelve
times for its six specs. So the file no longer distinguishes the scopes it
was extended to describe, and asking it what a tenant-scoped integration may
be granted now returns nothing at all. That is the second half of the
routes/scopes disagreement already reported here, resolved in the direction
of "everything is environment" but by duplication rather than by typing.
`scopes.yaml` is byte-identical and still has `environment` as its only
top-level key.

**The two restorations are the substantive half, and both were already live
on the wire.**

**`jpapi` brought back all 13 `/v3/computers-inventory` operations**
on upstream's stated grounds: "they were deprecated 2026-07-14, weeks before the
manifest was written, so callers have had no reasonable window to reach
`/v4`". `v1` (2025-06-30) and `v2` (2025-11-06) stay removed. 687 → 700
operations, zero schemas added or changed — V3 and V4 share them — and the
whitelist is complete again at 700. The recovered config entries are the
pre-v1942 ones verbatim, including the three `ListComputersInventoryV3`
resolvers, so this is a revert rather than a re-derivation. Confirmed
2026-09-04: `GET /pro/v3/computers-inventory` answers 200 with a body
matching `/v4`'s for the same tenant, and the whole V3 read chain and CRUD
lifecycle pass. The endpoints still send a `Deprecation` header dated
2026-07-14, which the transport logs.

**`capi` brought back the entire patch-management family, and that ends the
hold.** Upstream's stated grounds again: "Patch management is where Classic API
callers are most concentrated; that migration gets driven on its own
schedule." Back: `/patches` and its two sub-paths, `/patchpolicies` and
`/patchpolicies/softwaretitleconfig/id/{id}`, both `/patchreports` forms,
`/patchsoftwaretitles` and the three non-`POST` verbs on
`/patchsoftwaretitles/id/{id}`. So `POST /patchsoftwaretitles/id/{id}` — the
single operation the hold existed to keep, because nothing else mints a
`softwareTitleId` for the Pro v3 configuration endpoints — is no longer
carried against the spec, and `seedPatchSoftwareTitleFixture` is back on
supported ground. 575 → 589 operations, mirroring the published spec exactly.

Ingesting `capi` also took the 17 `/computers` withdrawals the config had
already applied at v1993, so that alignment cost nothing: **zero whitelisted
operations are absent from the spec, in either package.**

**Writing the acceptance coverage against the wire rather than the spec found
three defects, two of them shipping silently in the SDK.** All three are
recorded with payloads in
[WIRE-FACTS.md](WIRE-FACTS.md#the-restored-classic-patch-family-2026-09-04):

- **`GET /patchpolicies/softwaretitleconfig/id/{id}` answers
  `<patch_policies>`, a collection, against a spec declaring the singular
  `patch_policy`.** `responseType` is now `patch_policies` and the method
  returns `*PatchPolicies`.
- **`GET /patches/id/{id}/version/{version}` answers `<software_title>` — the
  title filtered to one version — and the spec declares no schema at all**, so
  the pre-v1942 `"responseType": "computers"` was the SDK's own guess and it
  was wrong. Now `software_title`, and the method is renamed
  `GetPatchComputersByIDVersion` → **`GetPatchByIDVersion`**, because a name
  promising computers over a `SoftwareTitle` return is worse than a rename.
- Neither could fail loudly, and that is the general lesson: **generated
  Classic types leave `XMLName xml.Name` untagged, so a mismatched root
  element decodes to a zero-valued struct and the call reports success.** Both
  operations had been doing that since the day they shipped, and neither had
  an acceptance test. A restored operation needs its response *shape* probed,
  not just its status.
- **`POST` and `PUT /patches/id/{id}` are refused whatever the body** — Jamf
  Pro's own HTML `400 "Error in XML file"`, reproduced with the spec's
  `software_title` root, the Go type's `SoftwareTitle` root and a minimal
  one-field body, with `GET` on the same path at 200 in the same invocation.
  `DELETE` works. So `/patches` is a read-and-delete surface whose spec
  declares four verbs. Report upstream.
  `TestAcceptance_Classic_PatchByIDWrites` asserts both refusals and fails if
  either starts working.

**Acceptance coverage is complete for all 27 restored operations**, which it
was not before: six of the fourteen Classic ones had never had a test even
before the withdrawal, and one carried a comment that it "needs a patch
software title config id fixture; skip" — a fixture `seedPatchSoftwareTitleFixture`
mints. Nothing in `acc_proclassic_patch_test.go` skips for want of one.

**Everything else in v2082 is inert.** `ai-governance`, `audit` and
`account-partners` were byte-identical outside the scope parameters;
`account-licensing` and `account-sso` were still held at v1865 at the time (both
lifted at v2176) and `securitycloud-devices` at v1897.

**v2056 is `audit` and nothing else, and the spec has caught up with the wire.**
Every other file outside `internal/dev` is byte-identical to v2051 — `capi`
included, every Security Cloud spec included — so the only other diffs were the
manifest and the two unified rollups. The whole change is the **withdrawal of
organization scope**: `x-scope-types` goes `[environment, organization]` →
`[environment]`, `X-Environment-Id` flips `required: false` → `true` and loses
the sentence offering the header-less organization form, and the four
`**Required Permissions:** audit:read (environment scope … or organization
scope, when X-Environment-Id header not present)` lines shorten to
`audit:read`. `_permissions` agrees on both halves: `routes.yaml` drops the four
`audit:read` routes typed `organization` and `scopes.yaml` drops the whole
`organization` block, leaving **`environment` as its only top-level key**.

**Wire-confirmed 2026-09-03 with a two-sided control, and the spec is right.**
An organization credential that reads `/licensing/v1/licenses` at 200 in the
same session — so genuinely organization-scoped, resolving the org from the
token with no header — gets `400 REQUEST_CONTEXT_NOT_PROVIDED` on
`/audit/v1/audit/sources`. The environment credential answers 200 with
`X-Environment-Id` and the same 400 without it, so `required: true` is correct
too. The refusal is a pre-routing scope check rather than an audit-specific
answer: a bogus path in the same namespace returns the identical body. This is
the spec catching up with the gateway, recorded here as a wire fact since
2026-09-01 and now published.

**Generated impact: docs only, zero Go.** The generator does not emit scope
headers as parameters — the transport stamps them — so the diff is 14 lines in
`api/audit_api.json` and nothing in `jamfplatform/`. `account-partners` moved
v1872 → v2056 in the same run and is inert for the same reason: 4 lines in
`api/account_partners_api.json` narrowing the `servers` region enum to `us`,
which documents the US-only fact recorded below and which the SDK never reads.

**v2024 is a pure pipeline re-run — zero spec content changed in any
environment.** Not one file outside `internal/dev` differs from v2018:
`_permissions/{routes,scopes}.yaml`, `capi`, the account specs, every
Security Cloud spec, all byte-identical. The only diffs in the whole archive
are the manifest's build number and commit, the `Generated` timestamps, and
`info.version: auto-v3.2018` → `auto-v3.2024` in the two unified rollups.
`internal/dev` reports all 22 specs as changed, which is the `x-generated`
block and nothing else — stripping it makes all 22 semantically identical to
v2018. Nothing to ingest and no generated diff. This is the second recorded
no-op build (v1725 vs v1700 was the first), and it is the reason step 1 of the
ingest is to hash before reading.

**v2018 is `uem-connect` and nothing else, and it takes back half of v2005.**
Every other file outside `internal/dev` is byte-identical to v2005 —
`_permissions/{routes,scopes}.yaml` included, `capi` included, the account specs
included — so the only other diffs were the manifest and the two unified
rollups, whose path counts did not move. Zero operations, zero schemas, zero
prose, `415` untouched. The whole change is **`406` deleted from 7 of the 12
operations**, leaving it on exactly the 5 that write a 2xx response body: the
four `GET`s and `POST /connectors` (201). The seven it went from all answer
204/202. `internal/stage` took a byte-identical delta and `internal/dev` agrees
op-for-op, so this is not an environment rollout. Generated diff: **21 deletions
in `api/securitycloud_uem_connect_api.json` and zero Go** —
`components.responses.NotAcceptable` survives, still referenced five times.

**The correction is right, and the reason is that `406` is the *last* check in
the pipeline, not the first** (wire-verified 2026-09-02). It is decided when the
response body is serialized, so an unsatisfiable `Accept` is invisible on an
operation that never writes one — and invisible even on the five that do whenever
an earlier check fires. `Accept: application/pdf` against a bogus `configId`
answered `404` on all seven bodiless operations **and on the `GET`s that keep the
406**; only a request that resolves and reaches serialization gets `406`. The
full order is **`Content-Type` (415) → body validation (422) → resource
resolution (404) → business rules (422 `VENDOR_MISMATCH`) → response
serialization (406)**. That supersedes the flat "406 is enforced" recorded at
v2005, which had only ever probed `GET /connectors` — the one operation where
nothing earlier can fire.

`TestAcceptance_SecurityCloudUemConnectAcceptNegotiation` pins all three halves
and is fixture-free: 406 on the collection `GET`, 404 (**not** 406) on a
bodiless op with a bogus id, and the XML disagreement below as a limitation that
fails the day it lifts. It reaches `Accept` through `WithHeaders`, which is the
only door — no generated method sets it.

**v2005 is `uem-connect` and nothing else, and it is documentation plus declared
error responses.** Every other file outside `internal/dev` is byte-identical to
v1993 — `_permissions/{routes,scopes}.yaml` included, `capi` included — so the
only other diffs were the manifest and the two unified rollups, whose path counts
did not move (802 prod). Zero operations and zero schemas added or removed; the
whole change is a `406` on all twelve operations, a `415` on the four that carry a
request body, two new `components.responses` entries behind them, and prose. The
generated Go diff is **four lines of godoc on `SyncSettings.Vendor`**: method
comments come from the operation `summary`, never its `description`, so the
rewritten `updateSyncSettings` text lands on no method. `internal/stage` took a
byte-identical delta and `internal/dev` agrees op-for-op, so this is not an
environment rollout.

**All three claims are wire-verified (2026-09-02), and one of them is wrong in the
spec.** `422 VENDOR_MISMATCH` is enforced, atomic and unattributed — a
sync-settings PUT carrying a vendor other than the connector's stored one is
rejected whole, with both names in the description and `field: null`. `415` is
enforced and runs **before** resource resolution, which makes it probeable with no
connector at all. `406` is enforced for genuinely unsatisfiable types
(`application/pdf`, `text/csv`) on an operation that reaches serialization — see
v2018 above for where in the pipeline that is, since the claim as recorded here
was too broad — **but `Accept: application/xml` answers 200 with an XML body**,
so the spec's claim that these operations "only produce `application/json`" is
false and worth reporting upstream; v2018 left that prose unchanged and the wire
still disagrees (re-probed 2026-09-02). The SDK is unexposed (it never sets
`Accept`, and `DoWithContentType` always sends `application/json`), but
`WithHeaders` does not reserve `Accept`, so a proxy that sets it would feed XML
to a JSON decoder.

**The recorded validation ordering for this PUT was backwards, and correcting it
reopens the doomed-request trick.** The real order is media type → body
validation → resource resolution → vendor match: an out-of-enum value against a
nonexistent `configId` answers 422, the same body with a valid value answers 404.
So every `SyncSettings` field constraint is now probeable without a connector, and
only `VENDOR_MISMATCH` needs a real one. Two acceptance tests pin this —
`…UemConnectSyncSettingsValidation` (no fixture, asserts both halves of the
ordering) and `…UemConnectVendorMismatch` (a write that is safe because it is
rejected, built from current state so an unexpected success is a no-op).

**Two credentials on one JSC tenant differ in capability**, which is what made
this look unprobeable at first: `uem-connect` is a separate capability from
`device-groups`, and the connector is invisible to a credential without it. Full
table, and the fact that the sandbox connector has a concurrent human operator:
[WIRE-FACTS.md](WIRE-FACTS.md#uem-connect).

**~~The `capi` hold~~ — resolved at v2082; kept here because the reasoning is
what to reuse next time.** On 2026-09-02 the whitelist was aligned to
`external/capi` at v1993 *without* ingesting the spec: the file stayed at v1897
and 31 of the 32 withdrawn operations were dropped from `config.json`, leaving
575. The one kept back was **`POST /patchsoftwaretitles/id/{id}`**, for exactly
one reason: it is the only way to mint a `softwareTitleId` for the Pro v3
patch-configuration endpoints, so there was nothing for a migration to land on.
The rest of that family went with the withdrawals, and the SDK deliberately
reproduced upstream's incoherence on the path — create with no read, no update
and no delete — while the acceptance suite reached Pro v3 for every fixture and
cleanup that used to go through Classic.

**v2082 republished the family and the hold was dropped in the same change**,
which is the outcome the row predicted: "when `/patchsoftwaretitles` reappears
in a published spec, ingest `capi` and drop the hold entirely." It did, the
whitelist is at 589, and the fixture is back on supported ground. The tactic
worth keeping is the one this vindicates — **hold the spec where a withdrawal
would cost a capability outright, mirror it in config where it would not, and
say which**, rather than following the spec off a cliff or blocking the whole
ingest on one operation. Full record of the interim position:
[WIRE-FACTS.md](WIRE-FACTS.md#the-v1993-config-alignment-2026-09-02).

**v1993 is one brand-new spec and nothing else.** `external/securitycloud-enrollment`
appeared in all three environments at once, having existed in none of them at
v1988; every other file outside `internal/dev` is byte-identical, `capi` included,
so the only other diffs were the manifest, the two `_permissions` files and the
two unified rollups (797 → 802 paths, 19 → 20 APIs).

**The Security Cloud Enrollment API is now the `securitycloud` package's sixth
spec** — 6 operations over activation profiles, tenant-scoped, `X-Tenant-Id`, its
own `/v1/` prefix, prod `servers` at the gateway root with no `/api` segment. It
is the missing half of `DeployActivationProfileToUemV1`: uem-connect deploys an
activation profile code the SDK previously had no way to mint or list.

Both privilege oracles agreed before the ingest and the wire agreed after.
`_permissions` is purely additive — `routes.yaml` gains a `tenant`-typed
`securitycloud` domain with the five paths, `scopes.yaml` gains
`activation-profiles` with create/delete/read/update, filed under `environment`,
which extends rather than resolves the routes/scopes scope-type disagreement
already reported upstream. The gateway's authorization policy carries a rule
for Security Cloud enrollment on `main`, allowing all six at
`/api/securitycloud/v1/activation-profiles…`, identical in shape to the DNS
policy, so prod's existing `/api/securitycloud/` Tyk listener already fronts them
and no new api-product was needed. (Prod has no `jsc-activation-profiles`
api-product; the stage and dev one listening on `/api/jsc-enrollment` is a
different surface and is in no default-external plan.)

**Six spec/wire disagreements came out of the probe, and one of them would have
shipped broken.** `POST /v1/activation-profiles` answers `{"code": "…"}` — an
`ActivationProfile` — and not the `ActivationProfileResponse` `{id, href}` the
spec declares, with no `Location` header, verified both with and without
`Accept-Encoding: gzip` so this is **not** the href-injection plugin nulling a
compressed body the way it does on the DNS and ZTNA creates. Without the
`responseType` override the generated create would have decoded a zero-valued
struct and reported success. The other five, and the three declared constraints
the server does not enforce, are in
[WIRE-FACTS.md](WIRE-FACTS.md#security-cloud-enrollment-activation-profiles-2026-09-01)
and carried as `docNotes` on the five affected types.

**Deletion is a soft delete the read surface does not reflect, and that is the
finding worth reporting upstream.** After `POST /delete-multiple` succeeds,
`GET /v1/activation-profiles/{code}` still answers 200 and the collection still
returns the code, indistinguishable from a live profile; only a write reveals the
state, as `409 STATE_CONFLICT` ("… is already deleted."). Combined with a bulk
delete that answers 204 with no body and no per-code result, a caller can neither
confirm a deletion nor filter deleted profiles out of a list. It also means
**every acceptance run leaves a permanent row in the tenant's list**, which is
why the suite creates exactly one profile and folds every assertion that could
have justified another into that one body.

**v1988 changed one spec — `capi` — and the spec was not ingested, but the
config took the removal on 2026-09-02.** It is the held
spec, and the change is twelve more deletions of the same kind v1942 made: the
alternate-identifier computer lookups, `GET`, `PUT` and `DELETE` on each of
`/computers/{macaddress,name,serialnumber,udid}/{value}`. `external/capi` goes
586 → 574 operations, `internal/stage/capi` identically, and every surviving
operation and all 169 schemas are semantically byte-equal to v1981's — zero
changed operations, zero changed schemas, no `servers` or scope-list churn (the
185 OAuth scopes are unchanged, the removed privileges still being referenced by
the surviving `POST`s). Everything else in the bundle outside `internal/dev` was
byte-identical: the only other diffs were the manifest and the two unified
rollups, whose path counts did not move because **the paths survive — only
methods went**.

**The same three signals as v1942 say the server has withdrawn none of them**,
re-established 2026-09-01 against Jamf Pro `11.31.1`: `internal/dev/capi` still
carries all 606 operations; the same bundle's `external/_permissions/routes.yaml`
is a pure reordering (6244 lines, `sort`-identical, zero removed) and still
grants `POST`, `GET`, `PUT` **and** `DELETE` on all four paths; and
the gateway's authorization policy is unmoved, with every alt-identifier allow
block intact. The wire serves all twelve — four `GET`s returned the real computer, and
`PUT`/`DELETE` against a nonexistent identifier returned Jamf Pro's **own HTML
404**, not the gateway's `403 BAD_PERMISSIONS`, which is the routed tell. Table:
[WIRE-FACTS.md](WIRE-FACTS.md#v1988-dropped-12-more-capi-operations-all-twelve-are-live-2026-09-01).

**The deny side is in flight but has not caught up.**
A draft policy change opened 2026-09-01 14:09 — six minutes before this bundle
generated — is the enforcement half of v1942's filter: it strips the
OPA allow blocks for `GET /proclassic/computers`, `/computers/subset/basic` and
`GET`/`PUT /computers/id/{computerid}`, and for Security Cloud's
`GET /v1/groups` + `PUT /v1/groups/{groupId}`. It leaves all twelve
alternate-identifier rules intact, so v1988's removal has no policy counterpart
yet. Watch that PR: it is where a withdrawal stops being a spec claim and starts
returning 403.

**None of the twelve declares a successor.** They carry `deprecated: true` and
`x-deprecation-date: 2025-02-11` and no `x-successor-endpoint` — so unlike
v1942's removals these are not deprecated-with-successor, and the additive-versions
rule's override does not reach them. The functional replacement is an RSQL filter
on Pro `GET /v1/computers-inventory`, which is not a declared migration path.
And v1988 leaves `POST` on each of the four paths while removing the other three
verbs, reproducing exactly the incoherent surface that was the second reason
`capi` was held at v1897.

**v1981 published four Security Cloud specs to `external/` for the first time**,
and the SDK now sources them from there: `jsc-dns`, `jsc-ztna`, `jsc-categories`
and `uem-connect`. The long-standing note that no Security Cloud spec had reached
`external/` is obsolete for those four; `securitycloud-devices` was already there
and `securitycloud-enrollment` followed at v1993.

**Switching those three read-only specs off stage was inert to Go and fixed the
published artifacts.** `jsc-dns`, `jsc-ztna` and `jsc-categories` are identical to
their stage counterparts except `servers`, `info.title` and the OAuth `tokenUrl`,
all of which the SDK ignores — so zero generated Go diff. What changed is
`api/*.json`, 8 lines each: the host stops being
the non-production one with its `/api` segment, the title stops carrying
`(STAGE)`, and the region enum goes `us1` → `us`/`eu`/`apac`. **The published
specs were advertising a stage host and a stage title to consumers**; that is now
correct. Note the prod `servers` URL carries no `/api` segment, matching the GA
gateway.

**`uem-connect` completed the per-vendor create split, and it is breaking.** v1882
gave Jamf Pro its own typed request against a generic `ConnectorCreateRequest` for
the other nine vendors; v1981 deletes the generic type and gives all ten their own
schema, so `ConnectorCreateRequestBody`'s discriminator mapping is now 1:1.
Removed from Go: `ConnectorCreateRequest` and the whole
`ConnectorCreateRequestVendor` enum. Added: `IntuneConnectorCreateRequest`,
`XenMobileConnectorCreateRequest`, `Maas360ConnectorCreateRequest`,
`WorkspaceOneConnectorCreateRequest` (the `AIRWATCH` variant),
`JamfSchoolConnectorCreateRequest`, `MobileIronCloudConnectorCreateRequest`,
`MobileIronCoreConnectorCreateRequest`, `GoogleConnectorCreateRequest`,
`WizyConnectorCreateRequest`, plus `CitrixCloudConfig`, `CitrixCloudOauthConfig`,
`GoogleApiSettings`, `IntuneApiCredentials` and two new enums
(`CitrixCloudOauthConfigRegion`, `XenMobileConnectorCreateRequestAuthStrategy`).
`additionalProperties` also flipped `false` → `true` on the Jamf Pro request and
credentials schemas, and `url` gained `minLength: 1`.

**Only the `JAMF_PRO` variant is wire-verified**; the nine others arrived as
documentation and could not be exercised — the JSC sandbox has no connector and
creating one provisions a UEM-side API role the SDK cannot remove. The
`ConnectorCreateRequestBody` docNote says so.

**No downstream code breaks.** `terraform-provider-jamfplatform`'s `uem_connect`
resource names `ConnectorCreateRequestVendor` only in a comment; its code uses
`ConnectorCreateRequestBodyVendorJamfPro` and
`JamfProConnectorCreateRequestAuthStrategy*`, all of which survive. The comment is
now stale and should be corrected there.

One config repair self-expired as designed: the `ConnectorCreateRequest` docNote
had no schema to attach to and generation **failed hard** with `docNotes names no
emitted type`. Retargeted onto `ConnectorCreateRequestBody`.

`external/_permissions/routes.yaml` changed again and is again a **pure
reordering** — `sort`-identical, 1474 entries both sides, zero removed;
`scopes.yaml` byte-identical. So the 146-operation filter is still in place, and
`internal/dev` still carries all 788 jpapi and 606 capi operations at v1981.

**v1958 changed one spec, `internal/stage/uem-connect`, and the change is
constraints plus one redefinition.** `refreshRateMinutes` went from
`minimum: 1` to `minimum: 60, maximum: 1440` with an enum of
`60, 120, 240, 480, 720, 1440`; `deviceUnmanagedThreshold` gained an enum of
`0, 1, 3, 5, 7, 14`. Both land on `SyncSettings` (the sync-settings PUT body) and
`ConnectorConfig` (the response). The generated diff is godoc only — the
generator records numeric enums as `Allowed values:` lines and emits constants
only for string enums — so this is **not** a breaking change, and the field types
stay `int64` / `int`.

**`deviceUnmanagedThreshold` was redefined, and a caller sending `0` now gets
something different.** It was "number of consecutive syncs a device may be absent
from the UEM platform before it is treated as unmanaged. `0` disables the grace
period." It is now "number of **days since last check-in** before a device is
treated as unmanaged. `0` uses the **platform default (3 days)**." So the unit
changed and `0` flipped from *off* to *default-on*. The spec also now says the
field is **not applicable for `JAMF_PRO`** — any value sent for that vendor is
silently ignored, device status coming exclusively from the UEM.

**None of that is wire-verified, and here is why.** The JSC sandbox tenant has no
UEM connector any more — the `M2M` one recorded at v1882
(`<connector-id>`) is gone, `GET /uem-connect/v1/connectors` returns
`totalCount: 0` — and the sync-settings PUT is the only surface carrying these
fields. The validation-ordering trick does not reach them either: five PUTs
against a bogus `configId` with in-enum, out-of-enum and below-minimum values all
returned the same `404 NOT_FOUND` ("Config with ID … doesn't exist"), so resource
resolution runs **before** field validation on this operation — the opposite of
the ordering that makes doomed-request probing work. Re-probe once a disposable
connector exists; creating one needs a Jamf Pro tenant and provisions an API role
on the Pro side the SDK cannot remove.

Everything else in v1958 was byte-identical to v1942 outside `internal/dev` — the
only other diffs were the manifest and the two unified rollups.

**v1958 also confirms the 146-operation filter is still in place and still
prod-only.** `internal/dev/jpapi` carries all 788 operations and
`internal/dev/capi` all 606 — the exact v1897 `external/` sets, op-for-op — while
`external/` and `internal/stage/` stay at 666 and 586. See the v1942 section
below.

**v1942 deleted 146 deprecated operations from the published specs and the SDK
took 124 of them**, holding the 20 in `capi` and the 2 in `securitycloud-devices`. Every one was
`deprecated: true` in v1897 with a successor version that already existed. This
is the second override of the additive-versions rule, and unlike v1897 it was
**not** evidence-led — the evidence pointed the other way and following the
specs was a deliberate decision. Record both halves. Where the wire evidence
showed a removal would cost a capability outright, the spec was **held instead**;
`securitycloud-devices` is that case, and it is the pattern to follow when the
next bundle does this again.

| spec | ops | what went |
|---|---|---|
| `external/jpapi` | 788 → 666 | computers-inventory `v1`/`v2`/`v3` (v4 survives), inventory-preload `v1` + unversioned (v2 survives), computer-groups `v2` (v3), mobile-device-groups `v1` (v2), mobile-device-prestages `v2` (v3), patch-software-title-configurations `v2` (v3), computer-inventory-collection-settings `v1` (v2), groups `v1` (v2), `GET /v1/mdm/commands` (v2) |
| `external/capi` | *spec was held; 31 of 32 taken in config 2026-09-02, and **v2082 republished the patch family** so only the computers withdrawals stand* | the `/computers` collection, `GET`+`PUT /computers/id/{id}`, `/computers/subset/basic`, `/computers/id/{id}/subset/{subset}`, the twelve v1988 alternate-identifier verbs, the whole `/patches`, `/patchpolicies` (collection) and `/patchreports` families, and four of the five `/patchsoftwaretitles` ops all went. **Only `POST /patchsoftwaretitles/id/{id}` was kept** — see below |
| `external/declaration-reporting` | 5 → 3 | `GET /v1/declarations/{declarationIdentifier}`, `GET /v1/devices/{deviceId}` |
| `external/securitycloud-devices` | *not taken* | would have removed `GET /v1/groups` and `PUT /v1/groups/{groupId}`; **held**, see below |

**Five independent signals say the server has not withdrawn any of them**, so
the SDK is now materially stricter than the gateway:

- **`internal/dev` in the same bundle carries every removed operation.**
  `internal/dev/jpapi` has all 788 and `internal/dev/capi` all 606 — the exact
  v1897 `external/` operation sets, op-for-op — while `external/` and
  `internal/stage/` carry 666 and 586. Still true in v1958. This is the filter
  caught in the act, and it is why `routes.yaml` still agrees with dev rather
  than with prod. (The standing advice not to diff against `internal/dev` is
  about its per-spec `x-generated` block making every file look changed;
  comparing *operation sets* across environments is exactly what it is good
  for.)
- **The same bundle's own privilege oracle still declares all 146.**
  `external/_permissions/routes.yaml` is byte-different from v1897 but
  `sort`-identical — a pure reordering, 1474 route-method entries both sides,
  zero removed, zero permission changes. `/computers`, `/patches` and
  `/v1/computers-inventory` are all still in it. Since `routes.yaml` is generated
  from the specs' own `x-required-privileges`, that generation must run *before*
  the filter.
- **The gateway's authorization policy has nothing.** Its `main` is still at
  v1897's `/v1/system/initialize` deny with no successor commit; the
  hand-written OPA allow blocks for `v1`, `v2` and `v3` `computers-inventory`
  are intact.
- **The wire serves every one probed.** 2026-09-01 against 11.31.1, control in
  the same invocation; deprecated and successor return byte-identical bodies.
  Full table: [WIRE-FACTS.md](WIRE-FACTS.md#v1942-dropped-146-deprecated-operations-from-the-specs-every-one-is-live-2026-09-01).
- **The withdrawn specs' own sunset dates are a year out** — the Security Cloud
  v1 group ops carry `x-sunset-date: 2027-08-25` alongside
  `x-successor-endpoint`, in the very build that deleted them.

**`internal/stage/` carries the identical removals**, so it is not an
environment rollout prod is lagging. Report the filter upstream: a spec that
deletes a path a year before its own declared sunset, while the same bundle's
permission map still grants it, is a pipeline defect whichever way the SDK
follows it.

**What the removal cost, beyond the 146 methods.** Three real capability gaps,
each pinned by a test that fails when it closes:

- **~~`securitycloud-devices` is held at v1897 for exactly this reason.~~ —
  the hold lifted 2026-09-04; the reasoning is kept because it is the pattern
  to reuse.** Taking
  v1942 there would have withdrawn `PUT /v1/groups/{groupId}` — the *only*
  device-group update the gateway routes — leaving the unrouted
  `PUT /v2/groups/{groupId}` as the package's sole update method and killing
  `ApplyDeviceGroupV2`'s update branch. The v1 write **answers 200 on the wire**,
  re-probed 2026-09-01 on the JSC sandbox tenant, where it renamed a real group
  and the rename read back; `PUT /v2/groups/{groupId}` — the declared successor,
  and the *only* item-level v2 verb the spec has — is 403 `BAD_PERMISSIONS` on a
  real id across five sessions (the last 2026-09-02 13:55Z) and on a bogus uuid, and
  **constant across two different credentials while the v1 write varies**, which
  classified it as a missing authorization rule rather than a capability gap.
  **The rule was then written** — a policy change merged 2026-09-02 10:13Z —
  and the 403 survived it, which narrows the diagnosis one
  further step: the v1 and v2 PUT rules declare the identical `tenantPermissions`
  condition, so a credential that passes v1 must pass v2 once the bundle carrying
  the rule is deployed. The remaining gap is the rollout, in the service that
  deploys the policy bundle. Note the resource is
  split across versions by design: `POST /v1/groups`,
  `GET`/`DELETE /v1/groups/{groupId}`, `GET /v2/groups`,
  `PUT /v2/groups/{groupId}` — so `GET` and `DELETE` on the v2 item path are
  undeclared and their 403s are not evidence of anything. Full evidence —
  including the third URL shape the
  runtime `Link` header names, and the scope-header tells — in
  [WIRE-FACTS.md](WIRE-FACTS.md#device-groups-v2id-the-rule-landed-on-main-2026-09-02).
  **Resolved 2026-09-04**, and the resolution is the one the row allowed for
  rather than the one it predicted: the diagnosis above stopped at "the
  remaining gap is the rollout", and the rollout did land — it just exposed a
  second, independent defect in the v2 handler behind it. That was fixed a day
  later, so the hold lifted, the spec is at v2082, and
  `ListDeviceGroupsV1`, `UpdateDeviceGroupV1`, their resolver and
  `ApplyDeviceGroupV1` are gone. `ApplyDeviceGroupV2`'s update branch now runs
  through `UpdateDeviceGroupV2`, which is the branch that could not have worked
  at all before the fix. The lesson worth keeping is that a 403 clearing is not
  the same event as the capability arriving: two layers were broken here, and
  the first fix made the second visible.

  **Resolved 2026-09-04.** v2082 republished the whole patch family, so the
  identifier problem is gone and `capi` is ingested at v2082 with the whitelist
  at 589. The prediction in the paragraph above — that this "lifts only if Jamf
  gives the configurations surface an id-minting create" — was **wrong in its
  mechanism and right in its judgement**: upstream un-withdrew the Classic
  create instead of adding a Pro one, which the hold's own row had allowed for.
  Worth remembering that a structural impossibility on the *current* surface is
  an argument for holding, not a prediction about which side will move.

  Pro v3 **is** otherwise a superset of the Classic
  `patch_software_title` resource — `displayName`, `softwareTitleNameId`,
  `uiNotifications`/`emailNotifications`, `categoryId`, `siteId`,
  `extensionAttributes`, and `packages[] {packageId, version}` covering the
  provider's `version_packages` — plus dashboard, definitions, dependencies,
  export-report, history, patch-report and patch-summary sub-resources. Only
  `source_id` has no equivalent (v3 offers `patchSourceName` /
  `patchSourceEnabled`) and only `softwareTitleId` is unobtainable. So the
  migration is blocked on one identifier, not on the model — and that identifier
  cannot be obtained without the call being withdrawn.

`GET /patches/name/{name}` **answers 500 for every name, present or absent.**
First recorded 2026-09-01 as "500 rather than 404 for a name the tenant does
not have"; re-probed 2026-09-04 on a tenant that *does* have the title, with
`GET /patches` listing it and `GET /patches/id/1` at 200 in the same
invocation, and it still 500s — so the framing as a wrong status for a missing
title was too narrow and the endpoint is simply broken. Report upstream.
`TestAcceptance_Classic_PatchByName` now **asserts** the 500 instead of
skipping on it: `skipOnServerError` is the right convention for a transient
5xx and precisely wrong for a permanent one, since the test could never report
the fix. See
[WIRE-FACTS.md](WIRE-FACTS.md#v1942-dropped-146-deprecated-operations-from-the-specs-every-one-is-live-2026-09-01).

**Acceptance coverage is at parity**: no surviving generated method lost its
only test. 146 operations went, 21 test functions were deleted as superseded by
their successor-version equivalents, two whole files went
(`acc_pro_version_backfill_test.go`, `acc_pro_inventory_preload_legacy_test.go`
— both existed only to cover withdrawn versions), the rest were repointed at the
successor, and two replacement tests were written for the surviving patch
endpoints that lost their enumeration fixture.

**Downstream breakage** (report only — do not migrate from this repo). Holding
`capi` and `securitycloud-devices` removed most of it, but **an earlier version of
this note said `patch_software_title` was unaffected and that was wrong** — it
cost `terraform-provider-jamfplatform` a build break the note had promised would
not happen. The lesson is general: **a hold on one spec does not protect a
downstream resource that reaches the same product through another spec.** Check
every package a resource imports, not just the one whose withdrawal you reverted.

- `internal/resources/pro/patch_software_title` **is** broken, through its Pro
  **v2 extension-attribute side-channel**: `ListPatchSoftwareTitleExtensionAttributesV2`
  is a `jpapi` operation, `pro` is at v1942, and reverting `capi` does not restore
  it. Its Classic types and `{Get,Create,Update,Delete}PatchSoftwareTitleByID` are
  all back; the v2 EA call is not. Migrate that one call to
  `ListPatchSoftwareTitleExtensionAttributesV3`.
- `internal/resources/pro/computer_inventory_collection_settings` —
  `ComputerInventoryCollectionPreferences` → `…V2`. Note the v2 response exposes
  only `applicationPaths`, and `CreatePathV2.Scope` accepts only `APP`, so any
  `FONT`/`PLUGIN` handling has to stay on v1 or be dropped.
- `internal/common/computertarget` and
  `internal/testhelpers/acceptance_assertions.go` —
  `ListComputersInventoryV3` → `…V4`, `ResolveComputerInventoryV3ID*` → `…V4ID*`.
- `internal/resources/pro/patch_policy` and
  `internal/resources/security_cloud/device_group` are unaffected — the holds keep
  the Classic patch-policy family and the v1 device-group ops.
`terraform-provider-jamfplatform-internal` is **unaffected**. `jamf-cli-internal`
is unaffected too: transport only.

**`internal/stage/uem-connect` also moved to v1942, and that change is
documentation only.** `GroupMapping.EmmGroupID` now says the
`computer_<id>` / `mobile_<id>` prefix is **required** and a bare ID rejected,
while `ActivationProfileDeployRequest.UemGroups` says a bare numeric ID *is*
accepted there, that a present prefix must match the `platform`'s device type,
and that scoping is **additive** — the IDs merge into the profile's existing
scope, so a later deploy can widen it but never narrow or clear it, and an
omitted or empty array leaves the existing scope untouched rather than meaning
"all groups". Those are upstream's behavioural claims carried through as godoc,
none wire-verified here, and the same text points callers at
`GET /v1/mobile-device-groups` — one of the paths v1942 deleted from `jpapi`.

**The three `account` specs changed inertly** and were not re-ingested. Each
dropped `eu` and `apac` from its `servers` region-variable enum, leaving `us`,
which documents the US-only fact recorded below. The SDK never reads `servers`,
so there is no Go diff and the holds are unaffected.

Everything else outside `internal/dev` was byte-identical to v1897,
`_permissions/scopes.yaml` included.

**v1897 position, retained:**

**v1897 changed one spec, `external/jpapi`, and the change was two deletions.**
`POST /v1/system/initialize` and `POST /v1/system/platform-initialize` are gone,
along with their `InitializeV1` / `PlatformInitializeV1` schemas — a long-standing
upstream withdrawal landing at last. Everything else outside `internal/dev` was
byte-identical to v1882, `_permissions/{routes,scopes}.yaml` included; the only
other diffs were the manifest and the two unified rollups (867 → 865 paths).

**This is the first time the additive-versions rule has been overridden, and the
reason it does not apply.** The rule protects a *version* whose successor
appeared; these two have no successor and are not being superseded — they are
being withdrawn. A policy change merged 2026-08-31 deletes both allow blocks,
returning them to `allow := false`, and states the
grounds: each bootstraps a Jamf Pro server from an activation code, neither is
applicable to a cloud instance, and neither appears in the capability map. So the
paths are not merely undocumented, they are about to be refused. Removing
`InitializeSystemV1` and `PlatformInitializeSystemV1` is a **breaking change** with
**no downstream consumer** — neither provider referenced either method or either
type.

**The wire still accepts both**, so the SDK is briefly stricter than the gateway.
Probed 2026-08-31 with `GET /pro/v1/jamf-pro-version` → 200 as the control in the
same invocation: `{}` to each path returns `400 INVALID_FIELD` attributed to
`jssUrl`/`password` and `eulaAccepted`/`email` respectively — Jamf Pro's own field
validation, i.e. routed and unrefused. `main` is not `deployed`, as always; the
deny will arrive with the next policy bundle. **Do not re-add them when a probe
shows 400** — the spec and the policy both say withdrawn, and a 400 only dates the
rollout.

Two guards proved themselves here, and neither needed prompting: `pruneStale`
deleted `jamf_pro_initialization.go` and its test, which whitelist removal alone
would have left compiling against types `types.go` no longer declares — the exact
failure mode CI's `git diff --exit-code` cannot see. And the byte-exact
reproduction of the previous YAML→JSON conversion
(`json.dumps(yaml.safe_load(f), indent=2, ensure_ascii=False)`, no trailing
newline) confirmed `testing/openapi-jpapi.json` was semantically identical to
v1882's `openapi.yaml` before the copy, so the generated diff is exactly the
delta: 298 deletions, zero insertions, nothing outside `pro`.

**The `pro` whitelist remains complete — 702 operations as of v2267** — it
reached all 790 on 2026-08-31, v1897 took two away, v1942 took 122 more,
v2043 added App Installers' 23, v2051 took one back, v2082 restored the 13
`/v3/computers-inventory` operations, v2121 restored
`GET /v1/mdm/commands`, v2154 added three genuinely new ones and v2267
withdrew two of those three. 21 of the 38 added on 2026-08-31 were
deprecated, which was no bar at the time: the whitelist carried 111 deprecated
operations, and until v1942 the additive-versions rule kept them until Jamf
removed the path. v1942 removed the paths — and v2082 and v2121 have each put
some back, which is the case the rule's own wording anticipated. Every surviving operation has an
acceptance test.

Five spec/wire disagreements came out of it, all corrected in `config.json` and
evidenced in [WIRE-FACTS.md](WIRE-FACTS.md#the-remaining-38-jpapi-paths-whitelisted-2026-08-31):
two `expectedStatus` values (one of which fixes `CreateInventoryPreloadHistoryNoteV1`,
**already shipping broken** — it has expected 201 against a server that answers
200 since the day it was whitelisted), a `pageSizeParam` that is `pagesize` on
exactly one operation, a double-wrapped response schema, and two schemaless
`text/csv` request bodies. Four operations are generated-but-refused — two
unrouted at the gateway, `PUT /v1/cache-settings` refused on any hosted tenant,
and the deprecated macOS send-updates endpoint refused whenever Managed Software
Update Plans is on — each pinned by a test that fails when the block lifts.

Two generator fixes came with it:

- **`detectPaginatedItemType` iterated the response's `Content` map directly**, so
  an operation declaring several content types got an item type that depended on
  Go's map order. `GET /inventory-preload` declares the pagination envelope under
  `text/csv` and an *array of* that envelope under `application/json`, so it
  generated either `InventoryPreloadRecord` or `InventoryPreloadRecordSearchResults`
  from run to run — the latter compiling fine and decoding into empty structs. Now
  two deterministic passes, envelope before raw-array, pinned by
  `TestDetectPaginatedItemTypePrefersEnvelopeOverArrayOfEnvelope`.
- **An operation-level `"requestType": "[]byte"` now works on operations that
  return a body.** The `update` template already honoured it; `create` did not, so
  a schemaless non-JSON body (both `validate-csv` operations) generated
  `request *map[string]any` and would have sent JSON to an endpoint that answers
  415 for it. The transport already passes `[]byte` through unmarshaled.

v1882 changed **one spec**: `internal/stage/uem-connect`. Everything else outside
`internal/dev` was byte-identical to v1877, `_permissions/{routes,scopes}.yaml`
included; the only other diffs were the manifest and the two unified rollups.

It split the create body into a vendor-discriminated `oneOf`
(`ConnectorCreateRequestBody`), gave Jamf Pro its own
`JamfProConnectorCreateRequest` + `JamfProCredentials`, and **removed `JAMF_PRO`
from the generic `ConnectorCreateRequest`'s vendor enum**. That adopts all three
fields the SDK had been restoring from the wire since 2026-08-21 —
`authStrategy`, `deviceSyncAuth`, `tenantId` — so those `schemaPatches` were
deleted; the `ConnectorConfig` response patches stay, upstream having fixed only
the request side. Details and the two claims the wire contradicts:
[WIRE-FACTS.md](WIRE-FACTS.md#uem-connect).

**The `oneOf` is faithful to the server, not just documentation.** An unknown
vendor returns `422` quoting Jackson's own subtype registry for
`EmmServerConfig`, with `JAMF_PRO` a first-class member — so the split models a
real per-vendor type hierarchy and was taken rather than collapsed. It is a
**breaking change**: `CreateUemConnectorV1` now takes
`*ConnectorCreateRequestBody`, and `ConnectorCreateRequestVendorJamfPro` is gone
(the union's own `ConnectorCreateRequestBodyVendor` enum replaces it and is the
only complete vendor list — see below). `terraform-provider-jamfplatform`'s
`uem_connect` resource needs migrating: `input_builders.go`, `mappings.go`, and
its three test files.

**A discriminated union now also emits an enum for its discriminator values.**
The mapping is the authoritative set once a spec moves a value out of a variant's
own `enum`, which is exactly what v1882 did to `JAMF_PRO`; without this the SDK
would have had no constant for the commonest vendor. Purely additive for the
three existing unions (`MobileDeviceResponse`, `BookmarkItem`,
`SwUpdateAutomaticConfiguration`).

**The generator's discriminator handling had a silent-data-loss bug**, latent
because every previous union mapped 1:1. `schemaToDiscriminatorType` deduped
variants by Go type, so nine of uem-connect's ten mapping keys were dropped along
with their cases in the generated switches, and a caller setting `Vendor:
"INTUNE"` would have marshaled `{"vendor":"INTUNE"}` with every other field gone.
The field is still deduped — nine identical pointers would be nonsense — but each
value now gets its own case, and a variant serving several values is named after
its schema rather than one arbitrary member. `emitTypesOnlyTest` emits a
round-trip test per *value* for the same reason. Pinned by
`TestSchemaToDiscriminatorTypeGroupsSharedVariants`.

The ingest also replaced `testing/securitycloud-uem-connect-api.yaml`'s
**re-emitted** YAML with the bundle file verbatim — the same repair the account
and AI Governance files got. Confirmed inert first: the verbatim v1877 file
regenerated the tree with zero diff, so the v1882 diff is exactly the delta.


| held | why |
|---|---|
| ~~`account-licensing`, `account-sso` at v1865~~ | **Lifted 2026-09-14, and both premises went at once — the wire stopped sending either field, on the very tenant the hold was last confirmed against.** **Licensing:** `License.type` is gone — **0/19 rows**, the key entirely absent, deterministic 2/2, with `GET /licensing/v1/licenses` at 200 and a bogus path in the same namespace at 403 as controls in the same invocation. The decisive control is that this service *does* serialize nulls — `addOnType`, `bundleProductCode` and `contactId` all appear as explicit `null` in the same row — so an absent `type` means the property is off the DTO rather than merely unpopulated, which is a schema-level tell and not data variance. `GET /v1/licenses` is the licensing spec's only operation, so that one body is the whole surface. **SSO:** the wire sends neither name the hold was about — `region` on **5/5** domain allocations, `authZeroRegion` on none and `authRegion` on none. So holding left `AuthZeroRegion` permanently empty and ingesting leaves `AuthRegion` permanently empty: identical badness, and the hold protected nothing. `authRegion` is an upstream authoring slip and the same spec proves it — `Connection`, `ConnectionSummary` and `BaseConnectionSettings` all name the identical `Region` type `region`, and the wire agrees with those three — so `propertyRenames` corrects it to `region` and panics the day the spec declares it. Reported upstream. **Corroborated on `<org-b>` the same day — 24 rows, the `type` key absent on all 24, deterministic 2/2, with a bogus path in the same namespace at 403 as the control.** That is the second tenant of the 2026-09-04 pass, identifiable by its 24 licences with `licenseType` non-null on 16, so it is a before-and-after on a tenant that *had* the field rather than a fresh opinion, and both tenants that populated it have now stopped. The `<org-a>` reading is also **`<org-a>` itself, not a second opinion**, which is what makes it a server change rather than tenant variance: the five domains, the connection identifiers and the region *values* are the same ones the 2026-09-09 probe recorded five days earlier (`<con-1>` / `<org-a>`, `US`×3 / `JP` / `RAMP`) and only the key changed, while the licence list went 16 rows with `type` on all of them to 19 rows with `type` on none. Identify the tenant before reaching for tenant variance as the explanation. Row kept so the next reader sees the outcome rather than the wait, and because the superseded evidence below is what the hold rested on for five weeks. Two breaking changes were **ahead of the server**, both re-confirmed 2026-09-04 on **two independent organization tenants** — `<org-a>` (16 licences, 5 domains) and `<org-b>` (24 licences, 7 domains). Each spec's whole v2082 delta is the one field its hold names, plus an inert `servers` region-enum narrowing to `us`. **Licensing:** `License.type` is deleted though the wire populates it **40/40 rows**, and it is neither a rename of `licenseType` (non-null on only 8/16 and 16/24, so both fields coexist) nor derivable — on **17 of the 40** rows `type` matches none of `licenseType`, `addOnType` or `productTopLine` (`Jamf Trust` → `type: JAMF_SECURITY_CLOUD` while `addOnType: JAMF_TRUST`; `Jamf Pro for iOS` → `JAMF_PRO_SUBSCRIPTION`, the very value the deleted property gave as its `example`). So taking it drops a populated product-family classifier on 42% of rows. **SSO:** `DomainAllocationConnection.authZeroRegion` → `authRegion`, but the wire sends the old name on **11/11 connections** and `authRegion` on none. Both would be **silent** regressions — nothing sets `DisallowUnknownFields`. ~~SSO could not be re-probed — `/sso/v1/domain-allocations` answers 403 `BAD_PERMISSIONS`, so the capability is ungranted and it needs a credential holding `sso-domains`.~~ **That was wrong, and the mistake is worth keeping: there is no `/sso/v1/domain-allocations` path.** The operation is `GET /sso/v1/domains/allocation/{domain}` and it answers **200** on both credentials; the 403 was the gateway refusing an unmapped path, which in this namespace is indistinguishable from an ungranted capability. **Read the path out of the spec before concluding a capability is missing.** **Re-probed 2026-09-09 at v2100 on `<org-a>`: both holds stand unchanged** — `type` populated **16/16** with `licenseType` non-null on only 8, and `authZeroRegion` on **5/5** allocations (`US`×3, `JP`, `RAMP`) with `authRegion` on none — and v2100 changed neither spec, so there is nothing new to weigh. `account-partners` is inert (`servers` only) and moved to v2082. ~~It 403s on both organization credentials, so its own coverage is still ungranted.~~ **Wrong as of 2026-09-09: partners is granted and the whole account lane is green.** `GET /partners/v1/deal-registrations` answers **200** (`{"totalCount":0,"results":[]}`) with a bogus path in the same namespace returning `403 BAD_PERMISSIONS` as the control, so the earlier 403 was the *distributor* surface, not the capability. All five distributor operations are routed and authorized and answer `400 UPSTREAM_ERROR` "… via Skyway distributor service" — the standing distributor-service fault the suite already pins, not a grant problem. Every one of the `TestAcceptance_Account*` tests passes or skips on a write opt-in; none skips for want of a credential. **Two of them now pin an upstream fault rather than exercising a working surface** — the distributor `400` and, as of v2204, the deal-registration `500` — so read the lane's green as "the recorded faults are still the recorded faults", not as coverage. |
| ~~`capi` at v1897~~ | **Lifted 2026-09-04: v2082 republished the whole patch-management family**, including `POST /patchsoftwaretitles/id/{id}` — the one operation the hold existed to keep, because nothing else mints a `softwareTitleId` for the Pro v3 configuration endpoints (upstream's stated grounds: "Patch management is where Classic API callers are most concentrated"). `capi` is at v2082 and the whitelist at 589, mirroring the published spec exactly; the 17 `/computers` withdrawals the config had already taken at v1993 came with it, so the alignment cost nothing. Row kept so the next reader sees the outcome rather than the wait. |
| ~~`securitycloud-devices` at v1897~~ | **Lifted 2026-09-04: `PUT /v2/groups/{groupId}` answers 204 and the write persists.** The row's condition was *"lift when the v2 PUT answers 2xx, not when it stops 403ing"*, and that is exactly what happened — in two steps, five weeks apart. A policy change scoped to `PUT` only deployed at 12:29Z on 2026-09-03 and turned the unrouted `403 BAD_PERMISSIONS` into a service-level `404 NOT_FOUND`: the request began clearing authorization and reaching a handler that could not find a group `GET /v2/groups` returned in the same invocation. The handler was then fixed on **2026-09-04, between 12:51 and 13:33 BST** — a probe at 12:51 still got the 404, one at 13:33 succeeded — and the fix is genuine rather than a status change: verified 3/3 by curl with the rename **read back** through `GET /v2/groups`, a `PUT /v1/groups/{id}` at 200 and a bogus-path `403` as controls in the same invocation. So the two operations v1942 withdrew (`GET /v1/groups`, `PUT /v1/groups/{groupId}`) no longer cost a capability, the spec is ingested at v2082, and `ListDeviceGroupsV1`, `UpdateDeviceGroupV1`, `ResolveDeviceGroupV1*` and `ApplyDeviceGroupV1` are gone. Both withdrawn paths still answer 200 on the wire, so the SDK is deliberately stricter than the gateway here, per the v1942 rule. Row kept so the next reader sees the outcome rather than the wait. |
| `ai/governance/visibility` | No published spec in any environment. Not ingestable. `securitycloud-enrollment` was in this row until v1993 published it. |
| ~~App Installers~~ | **Ingested 2026-09-03 from v2043, 23 operations.** The gateway opened the same day and v2051 withdrew `POST /titles/{id}/cache-update` 80 minutes later, taken. Row kept so the next reader sees the outcome rather than the wait. |
| User Inventory API (`users`), Jamf Inventory API | Not in `config.json`. `users` is prod-published with real privileges but every path 404s — `platform-users-directory` is flag-gated to dev. `inventory-api` is stage/dev only. |

**A verbatim copy of the bundle YAML into `testing/` is safe and worth adopting.**
The account files previously carried re-emitted indentation, making every diff
against `external/` look like hundreds of lines of churn; copying the bundle YAML
in unchanged produced a generated diff of exactly the delta and nothing else. The
formatting is inert to the generator, and bundle diffs become exact.

## Packages: the audit and App Installers records

These were carried in `CLAUDE.md`'s package section until the roll-up.

### audit

**`audit` became usable on 2026-09-03**, after being correct-but-unreachable for
its whole life here. A credential granted `audit:read`, sending
`X-Environment-Id`, reads it: `ListAuditSources` returns real sources
(`api-gateway`, `blueprints`, `ai-policy`) and `ListAuditEvents` walked **1014
events** through the cursor paginator — the first time that walker has met real
data. `GetTransactionTimeline` and `GetResourceLineage` both answer (20
transactions on a blueprint resource). The blocker was only ever the grant, as
recorded; four earlier credentials lacked it.

### App Installers

**App Installers were removed 2026-08-27 and must not be re-added without a
published spec.** They were generated from three hand-carried YAML files flagged
`undocumented: true`, and the covered fraction was arbitrary: none of the 9
operations exists in any published spec, while 15 further app-installer operations
exist only in Jamf's internal hidden-endpoint inventory. `schemaRenames`,
`excludePaths` and the spec-level `undocumented` flag now have zero users and were
all **kept** — each is general-purpose and tested. Re-adding is an ingest, not a
revert: take it from the GitOps bundle and drop the flag; do not restore the YAML
from git history, which is unversioned against upstream.

**Ingested 2026-09-03 from GitOps v2043 at 23 operations, and the gateway opened
the same day.** What follows is the record, because three parts of it are not
recoverable by guesswork.

**The spec shipped via a second upstream change, not the one this section used
to tell you to watch.** The first was a duplicate and is now closed, so anything
keyed on that number is a dead end.

**Three independent sources agreed before the ingest and the wire agreed after.**
The policy change grants the set on the
`applications` capability, `GET /deployments/{id}/computers` being its one
`has_all_permissions` rule at `applications:read` **and** `devices:read`;
`_permissions/routes.yaml` independently lists 18 app-installer paths with exactly
those privileges; and `scopes.yaml` is byte-unchanged because `applications` and
`devices` were already declared capabilities — that is the self-consistency check
passing, not a gap. The wire then served 363 titles with
`cloudServicesEnabled: true`.

**v2051 withdrew `POST /v1/app-installers/titles/{id}/cache-update` 80 minutes
after v2043 published it, and that withdrawal is coordinated rather than
spec-only.** The call ran jss's `assertDebugModeEnabled()` and answered 404
without the toggle; `#430`'s own body flagged it as the one published operation
resting on a decision rather than on the spec and asked reviewers to confirm. The
answer was no. Probed after: it answers **403 `BAD_PERMISSIONS`**, the unrouted
tell, so the OPA rule went with the spec — unlike the two `policyProperties`
operations dropped in the same build, which still answer 200 with real data. That
distinction is worth keeping: one withdrawal costs a capability, the other costs
only its documentation.

**Two wire constraints the spec does not state.** Deployment and computer
identifiers must be a **positive** numeric string or `-1`, and the shape is
validated *before* lookup: `"0"` answers
`400 "id field must be string of positive numeric value or -1"` on all four
deployment-scoped reads and on the per-computer retry. So the obvious doomed-ID
probe fails for the wrong reason; `noSuchDeployment` in the acceptance file
records it.

**The write surface is gated behind
`JAMFPLATFORM_ACC_PRO_APP_INSTALLERS_WRITE_OK`** — its own variable rather than
the suite-wide destructive one, because a deployment installs software on every
computer in its scope and has no dry-run. The reads that need a deployment are
probed with an impossible ID instead, where a 404 proves the URL construction and
the error decode without provisioning anything.

**The gated surface has now been run (2026-09-03), and it found a shipped
break.** The write test creates a **disabled, unscoped `SELF_SERVICE`/`MANUAL`
draft** — which installs nothing — asserts the safety properties round-trip so a
spec change cannot silently make a create active, then covers the real scoped
reads, a history note, the rename, delete-then-404, and a no-op global-settings
round-trip. Four wire facts came out of it, evidenced with controls in
[WIRE-FACTS.md](WIRE-FACTS.md#app-installers-the-write-surface-exercised-2026-09-03):

- **Both history-note `POST`s answer 200 against a spec that declares only 201**,
  so `CreateAppInstallerDeploymentHistoryNoteV1` and
  `CreateAppInstallerGlobalSettingsHistoryNoteV1` were **failing on every
  successful call** — the same defect as `CreateInventoryPreloadHistoryNoteV1`.
  Fixed with an explicit `"expectedStatus": 200`; **deleting the override is
  inert**, since absent one the generator reads the spec's 201. The deployment
  create itself is genuinely 201. Report upstream.
- **The create's `href` names the Jamf Pro instance host and an `/api` prefix**,
  not the gateway, so it is not callable by this SDK. `HrefResponse.ID` carries
  the real identifier — use it.
- **An omitted `smartGroupId` reads back as `"-1"`**, the no-assignment
  sentinel, never `""`. Both mean unscoped.
- **Both installation retries answer 404 with an empty `errors` array** when
  there is nothing to retry, on a deployment `GET` answers 200 for in the same
  invocation. Routed and refused on state, not a gateway gap (the unrouted tell
  here is `403 BAD_PERMISSIONS`, which the bogus-path control returned). Both are
  asserted as 404 so a change to 2xx fails.

**Still unverified: whether a post-GA *tenant* credential reaches these.** Only an
environment credential was live when the gateway opened — every tenant credential
in that session had been revoked within the hour — so `accClient` is used like
every other pro test and the tenant path is untested rather than known good.

**The gateway's policy for the app-installers *UI* surface is not related
coverage.** It matches `/ui/jamfpro/v1/app-installers/…` and requires an
interactive Auth0 token with an internal audience and permissions no M2M
credential carries. That prefix answers **`404 page not found`** on the
GA gateway root — a different listener, not the M2M surface — so do not read its survival as
the API being reachable.
