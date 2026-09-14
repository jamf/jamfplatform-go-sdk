# Wire facts

Behaviour established by probing the live API, per package. Everything here
disagrees with — or is absent from — the published specs, which is why it is
written down: the spec is not the authority, the gateway and the server are.

The standard for anything in this file: a probe against a live tenant with a
**known-good control in the same invocation**. A bare 401 or 403 proves nothing
on its own (see [Diagnosing a refusal](#diagnosing-a-refusal)), and a single
probe of a routing question is not evidence — the unrouted tell is a *repeated*
`403 BAD_PERMISSIONS`.

Rules derived from this file live in [CLAUDE.md](../CLAUDE.md); this is the
evidence behind them.

---

## Diagnosing a refusal

Four layers can refuse a request and three of them look alike. The response
*body formatting* separates them:

| probe | status | body | layer |
|---|---|---|---|
| `not-a-namespace/v1/…` | 404 | `404 page not found`, plain text | gateway, unknown namespace |
| `pro/v2/…/no-such-endpoint` | 403 | compact JSON, `BAD_PERMISSIONS`, top-level `traceId` | authorization service, no rule for the path |
| `pro/v1/…/buildings/nonsense` | 400 | pretty-printed (`"code" : "INVALID_ID"`) | reached Jamf Pro |
| anything, ~30 ms, HTML | 403 | CloudFront page, `x-cache: Error from cloudfront`, `x-amz-cf-id`, **no Jamf `traceId`** | edge WAF |

Jamf Pro pretty-prints its JSON; the gateway emits compact JSON. So a compact
`BAD_PERMISSIONS` means the request never reached Pro and no scope grant will fix
it. An edge block carries no Jamf `traceId` at all, so there is nothing in Jamf's
logs to correlate and the `x-amz-cf-id` is the only handle for an AWS-side WAF
lookup. Do not encode any of this in the transport — it is a serialiser tell, not
a contract.

**A 403 that varies by credential is a capability grant. A 403 that is constant
across credentials is a missing authorization rule.** Telling them apart needs
two credentials, not two paths. Probed 2026-08-29:

| path | EU tenant | EU environment | US tenant |
|---|---|---|---|
| `GET /pro/v1/api-roles` | 200 | 403 | 200 |
| `GET /pro/v2/smtp-server/allowed-auth-types` | 403 | 200 | 403 |
| `GET /pro/v1/pki/digicert/…/privilege-check` | 403 | 400 `NOT_FOUND` (reached Pro) | 403 |
| `GET /pro/v2/environment-type` | 403 | 403 | 403 |

`environment-type` was the only structurally-refused row, and it has no rule in
the gateway's authorization policy on any branch — which is what made its 403
permanent and assertable. **A retraction worth not repeating:** an earlier pass
dated the deployed policy bundle by arguing that two newly-added rules were
"still refused". They were refused because those *tenant* credentials lacked the
capability; the same paths answered 200 for an environment credential against the
same bundle. Re-probe with a second credential before concluding anything about a
rollout.

**Tokens live 900 seconds, and a rejected token gives the same plain-text
`401 Authentication failed` as a credential with no policy for the api-product.**
Two rounds of scope probing were reported wrongly — once from a cached expired
token, once from a broken shell helper — before a known-good positive control in
the *same* invocation showed the harness, not the API, was at fault.

### A plain-text 401 does not distinguish a bad token from an ungranted api-product (2026-09-11)

Recorded because the SDK now annotates it, and because the annotation is the only
thing separating two causes that need opposite remedies — rotate the secret versus
get the api-product granted.

Probed against `eu.api.jamfcloud.com` with one environment credential
(`<jsc-tenant-b>`), all in one invocation, with 200 controls:

| request | status | content-type | body | `x-tyk-trace-id` |
|---|---|---|---|---|
| `GET /pro/v1/jamf-pro-version`, valid token | 200 | `application/json` | `{"version":"11.31.1-…"}` | yes |
| `GET /pro/v1/pki/certificate-authority/active`, valid token | 200 | `application/json` | real CA subject | yes |
| `GET /pro/v1/jamf-pro-version`, **bogus** token | 401 | `text/plain; charset=utf-8` | `Authentication failed` (22 B) | yes |
| `GET /audit/v1/audit/sources`, **valid** token | 401 | `text/plain; charset=utf-8` | `Authentication failed` (22 B) | yes |
| `GET /securitycloud/v1/categories`, **valid** token | 401 | `text/plain; charset=utf-8` | `Authentication failed` (22 B) | yes |
| `GET /not-a-namespace/v1/thing`, valid token | 404 | `text/plain; charset=utf-8` | `404 page not found` (19 B) | **no** |
| `GET /licensing/v1/licenses`, valid token | 404 | `text/plain; charset=utf-8` | `404 page not found` (19 B) | **no** |

**The ungranted-api-product 401 and the rejected-token 401 are byte-identical**,
both 22 bytes of `text/plain`, both carrying a trace id — so neither the body, the
content type nor the headers separate them. The rows above settle it the way a 403
has to be settled, by varying the credential rather than the path: the *same*
token that answered 200 on two `pro` paths answered 401 on `audit` and
`securitycloud`. Second credential for the other direction: the organization
credential (`<org-a>`, `us.api.jamfcloud.com`) reads `/licensing/v1/licenses` at
200 — 16 licences — and gets a structured `400 REQUEST_CONTEXT_NOT_PROVIDED` on
`/pro/v1/jamf-pro-version`, which is a scope-header fault and a different layer.

`/licensing/v1/licenses` 404s on the EU gateway with no trace id, which is the
unmounted-namespace answer, not a grant answer, and matches `account` being US
only.

**Two of those rows have already moved, and the wording has not.** Re-probed
later the same day on the same environment credential, with the same 200 control:
`/audit/v1/audit/sources` now answers **200** (real sources) and
`/securitycloud/v1/categories` **200** (36 categories), so the *ungranted*
api-product half of the table is no longer reproducible with this credential —
the grant changed under it, which is a reminder that a 401 classified by varying
the credential is dated evidence, not a standing property. What did not move is
the body: a garbage bearer, a bare `Bearer` carrying no token and a `Basic`
scheme all still answer the byte-identical 22-byte `Authentication failed`, and a
*missing* `Authorization` header answers JSON instead —
`{"httpStatus":401,"message":"unauthorized access"}`, `message` rather than
`errors[]`. A bogus path inside a granted namespace answers a structured
`403 BAD_PERMISSIONS`, not a plain-text 401.

So four distinct credential-shaped faults produce one wording, and nothing yet
observed produces a second one. That is the evidence `nonJSONAuthGuidance` keys on
the status alone rather than matching the body — see its godoc for why the two
failure modes are not symmetric.

**Correction to the table above: `x-cache: Error from cloudfront` is not an
edge-block tell.** It appears on the gateway's own plain-text 401 and 404 as
well — CloudFront emits it for any non-2xx it passes through. The reliable edge
tells stay the ones already listed: an HTML body and **no Jamf `traceId`**.

**`x-amz-cf-id` is on every response, 200s included** (`dzzx90b-D0H…` on the
version control above). So it identifies a CloudFront request, not a CloudFront
error, and must only be read as an edge handle once the body has already been
established as an HTML page — which is what `edgeRequestID` does, being reachable
only from `summarizeNonJSONError`.

**A caller cannot check its own grants.** The token endpoint returns an opaque
token (one segment, not a JWT) and the token response's `scope` comes back empty.
With `GET /v1/api-role-privileges` withdrawn at GA there is no introspection path
at all: whether a credential holds a capability is visible only in Jamf Account.

### Where the per-path allowlist lives

Not in the gateway's API definitions. pro, proclassic and securitycloud are all
**catch-all proxies** — one listen path per product, path prefix stripped, no
per-path allowlist, product-level scopes — and no withdrawn path name appears in
them anywhere. The audit globs in the securitycloud definition are *audit* rules,
not routing.

The decision is made by a separate authorization service, and its policies are
**hand-written OPA rego, one policy per namespace, each defaulting to
`allow := false`** — so **a path with no `allow` rule is a 403 by construction**.
Two things about reading it:

- **Policy path arrays begin with `"api"`** even though the GA edge 404s anything
  under `/api`. The edge re-prefixes before the gateway, so those are internal
  paths and must not be used to reason about caller URLs.
- **A merged rule is not a deployed rule.** The policy bundle is tagged from its
  own commit and rolled out separately, and the gateway's API definitions name the
  bundle but never a version. Nothing readable offline gives the deployed digest —
  the wire is the only oracle. As of 2026-08-29 the deployed bundle sat between 18
  and 28 August in both regions, dated from *deletions* (credential-independent),
  not additions.

Where to read the policy itself, and the internal changes cited throughout this
document, is recorded in `docs/local/internal-provenance.local.md`, which is not
tracked.

It is also genuinely independent evidence, unlike `_permissions/routes.yaml`,
which is **generated from the specs' own `x-required-privileges`** and therefore
cannot corroborate them. Proven, not assumed: twelve pro GETs absent from
`routes.yaml` (`/startup-status`, `/v1/dashboard`, `/v1/health-check`, …) all
answer 200, so a missing route entry is not the mechanism behind any 403.

A stale local checkout of either shows old config and looks authoritative. Read
the fetched remote ref, not the working tree.

### The whole registry cross-checked against the allowlist (2026-08-31)

Every generated `Privileges` entry was matched against every `allow` rule in the
gateway's authorization policy — 1483 SDK operations against 1861 rules in 275
non-test policy files, keyed on HTTP method plus path template with the package's
namespace prefixed under `"api"`. **1475 matched a policy route and
1454 of those agree with it exactly**, so the specs' `x-required-privileges` are
corroborated by an oracle that is not derived from them. The 29 that do not
agree, and the 8 with no matching rule, are each one of five things:

- **18 `account` operations: the spec declares nothing and the policy requires a
  capability.** The gateway's account policy gates them on `licensing:read`,
  `deal-registration:read`, `distributor-actions:{create,read,update}`,
  `sso-connections:{c,r,u,d}` and `sso-domains:{c,r,u,d}` — exactly the five
  capabilities the permissions-map article lists under organization scope. The
  published licensing, partners and sso specs carry no `x-required-privileges` at
  all, so the registry reports an empty set and **the SDK is the one
  under-reporting**. Do not hand-supply the names — a local privilege table is
  the same shadowing liability `schemaPatches` is, with nothing to panic when
  upstream fixes it — and do not read an empty `Scoped` slice as "no privilege
  required"; the godoc says so.

  **The cause is a bundle-pipeline coupling, not missing authorship** (traced
  2026-08-31 against the spec source commit v1877 was built from). All 18
  privileges *are* declared upstream, twice: inline in each account spec's own
  source `openapi.yaml` (1 + 7 + 10) and again in each spec's source `config.yaml`
  under `requiredPrivileges.operations`. The build injects
  them into the published artifact only when `betaApiConfig.enabled: true`, and
  the correlation is exact — those three specs are the only ones without that
  block and the only three whose privileges are stripped; all 19 others have it
  and publish theirs intact. The account-sso source config documents the
  consequence in its own comment. The account APIs opt out of `betaApiConfig`
  correctly: it drives the beta `/{scopeType}/{scopeId}` path transform, which
  cannot apply to an organization-scoped API that resolves the org from the
  token. Privilege injection and path-prefix transformation simply share one
  switch.

  So the values are corroborated three ways — source spec, source config, and the
  gateway's account policy, which the config cites by name — and the published
  artifact is the only dissenter. **The upstream ask is to decouple `requiredPrivileges`
  injection from `betaApiConfig`**, not to add the privileges: any future API
  that opts out of the beta transform loses its privilege metadata the same way,
  silently. Re-check this when the account holds are lifted; if the coupling is
  fixed, the 18 empty sets fill in with no SDK change beyond the ingest.
- **3 `/logflush` DELETEs: the policy keeps an alternative the spec dropped.**
  `has_any_of_permissions([…policies:delete, flush-policy-logs:execute])`, so
  either capability still admits the caller. This is the platform's **only**
  cross-capability OR — every other multi-capability route uses
  `has_all_permissions` — and because the spec declares only
  `flush-policy-logs:execute`, no registry entry is an alternatives set. That is
  what licenses the godoc's "all of them are required".
- **4 `audit` operations, 1 `proclassic` file upload: matched by hand, agree.**
  Not parser gaps in the policy. The audit policy gates on a path *predicate*
  rather than a literal array, requiring `audit:read` on `environmentPermissions`
  — consistent with the package's known blocker and with environment scope. The
  Classic file-uploads policy enumerates the resource segment as literals
  (`computers`, `mobiledevices`, `policies`, …), so the SDK's one
  templated `POST //fileuploads/{resource}/{idType}/{id}` maps to several rules,
  all `file-uploads:create`.
- **1 `securitycloud` `PUT /v2/groups/{groupId}`: no rule.** Already recorded
  below — the v2 rule was never authored.
- **2 `proclassic` parameterised mobile-device-command forms: no rule at any
  arity.** `POST //mobiledevicecommands/command/{command}/{parameter}/id/{id_list}`
  and its `/{version}/` sibling. The policy authors
  `command/{command}/id/{id_list}` plus hand-written rules for the two commands
  that actually take a parameter (`DeviceName`, `ScheduleOSUpdate`), so the
  generic parameterised shape the SDK emits has no `allow` rule and is a 403 by
  construction for any other command. **Unproven on the wire**, and honestly so:
  the probe credential 403s on the parameterised form *and* on the
  `command/{command}/id/{id_list}` form that does have a rule, and on the
  `DeviceName` rule too, so it holds neither `device-actions:execute` nor
  `destructive-device-actions:execute` and cannot discriminate policy-absence
  from a missing grant. Needs a credential holding both.

Two probes worth keeping from the same invocation (EU, 2026-08-31): the control
`GET /pro/v1/jamf-pro-version` → 200 (`11.31.1-t1787060595569`), and
`GET /pro/v1/gsx-connection` → 200, which is the two-capability AND
(`gsx-connection:read` + `push-certificates:read`) actually being satisfied
rather than inferred. Twelve requests total, per the edge-block volume rule.

The permissions-map article's capability reference is now transcribed into
`gaCapabilityActions` in `tools/generate/privileges_test.go` and asserted against
every generated identifier, so a capability or action arriving from an ingest
cannot land in a consumer's table unchecked. One deliberate difference: the
article's table lists `devices:{c,r,u,d}` while **no route uses `devices:delete`**
— device deletes are `destructive-device-actions:execute`. The table keeps the
article's superset; the absence is the fact.

---

## Jamf Pro (`pro`) and Classic (`proclassic`)

### The GA withdrawal (ingested at v1872)

29 whitelisted methods went with paths Jamf removed — 18 pro (api-roles,
api-integrations, api-role-privileges, `/v2/account-preferences`,
`/v1/oidc/dispatch`, `/v2/environment-type`, `POST /v2/mdm/commands`) and 11
Classic (peripherals, peripheraltypes). **28 of the 29 were wire-confirmed live
on 2026-08-29**, each probed individually with bogus IDs and invalid bodies so
routing was exercised without mutating anything; `GET /v2/environment-type` was
the sole exception and had never been routed.

The withdrawal is deliberate and coordinated across the spec source, the gateway
policy and the product tickets behind both: the same tickets that removed the
operations from the specs deleted the matching hand-written policy rules, in four
commits. One of them names the mechanism outright: each removed operation "is
marked deprecated in the source spec and has a non-deprecated successor whose
policy rule already exists here." Credential management moved to Jamf Account.
Another groups peripherals under *"deprecated or not exposed because of
security"*, so that half is documentation-driven cleanup rather than a rider on
the credential change.

**Malformed XML is the right probe for a Classic write** — rejected at parse time,
before anything can be created, so a POST's routing is provable without mutating
the tenant. Classic's two dialects also make the layer unmistakable: XML for a
success, a Tomcat HTML status page for an error, neither confusable with the
gateway's compact JSON.

**Both landed 2026-08-31, and the SDK dropped them:** `POST /v1/system/initialize`
and `POST /v1/system/platform-initialize`, withdrawn upstream in the spec and in
the gateway policy together — they bootstrap an on-prem server from an activation
code and neither applies to cloud. v1897 removed both paths and their
`InitializeV1` / `PlatformInitializeV1` schemas from `external/jpapi`; the policy
change deleted both allow blocks from the Pro M2M-only policy, returning them to
the package default `allow := false`. `InitializeSystemV1` and
`PlatformInitializeSystemV1` are gone from the SDK — a breaking change with no
downstream consumer in either provider.

**The spec and the policy are ahead of the deployed gateway, and a 400 is not a
reason to re-add them.** Re-probed 2026-08-31 with `GET /pro/v1/jamf-pro-version`
→ 200 as the control in the same invocation: `{}` to `/pro/v1/system/initialize`
answers `400 INVALID_FIELD` on `jssUrl` and `password`, and to
`/pro/v1/system/platform-initialize` `400 INVALID_FIELD` on `eulaAccepted` and
`email` — Jamf Pro's own field validation, so both were still routed and unrefused
hours after the policy merged. A merged rule is not a deployed one; the deny
arrives with the next bundle, and until then a probe only dates the rollout.

That change also names why they were reachable at all: every rule in the Pro
M2M-only policy ends in the policy library's access helper, which is defined as
`permissions != null` — **no permission check whatsoever**. It cites the same
decision that dropped `/v1/system/initialize-database-connection` and
`/v2/environment-type` upstream; neither was ever whitelisted here.

**A further policy change removed the rules for four more deprecated jpapi
operations, and the SDK was already clear of all four**:
`GET /v1/macos-managed-software-updates/available-updates`,
`GET`+`PATCH /v2/account-preferences` and `POST /v1/oidc/dispatch`. `config.json`
whitelists only their non-deprecated successors
(`/v1/managed-software-updates/available-updates`, `/v3/account-preferences`,
`/v2/oidc/dispatch`), so the deprecated four had already left the spec in an
earlier build with nothing to migrate. Worth re-checking per bundle: a policy
deletion for a path the SDK *does* whitelist is a coming 403, not a diff.

### Privileges

pro and Classic converted to the GA `{capability}:{action}` form in v1824 and
were ingested at v1872: 746 pro ops, 606 Classic ops, both `securitySchemes`
scope lists, zero `{action}:pro:{resource}` strings left. Cross-checked against
the bundle's own `_permissions/` oracle — all 193 pro and 188 Classic privilege
strings declared in `scopes.yaml`, `routes.yaml` agreeing on every shared route
with zero disagreements.

The Classic conversion consolidates rather than renames. The computer/mobile
split collapses into single capabilities (`devices`, `device-groups`,
`extension-attributes`, `configuration-profiles`, `applications`,
`advanced-device-searches`, `enrollment-invitations`, `device-actions` /
`destructive-device-actions`), the three vpp resources fold into
`volume-purchasing-locations`, and `read:pro:computers` fans out three ways by
context — `device-history:read` (29 ops), `devices:read` (10),
`advanced-device-searches:read` (4), with `device-history` being new.

Eight Classic ops changed what a caller must hold: the three `/logflush` DELETEs
dropped the policy-delete requirement, the five `/mobiledevicecommands` POSTs
split across `destructive-device-actions:execute` and `device-actions:execute`,
device DELETEs became `destructive-device-actions:execute` (**there is no
`devices:delete` capability**), and `computer-inventory-collection` gained a
`-settings` suffix.

Two quirks that survived the conversion and look like bugs but are not:
`DELETE`/`POST .../patch-software-title-configurations/{id}/dashboard` require
`patch-management-software-titles:**read**`, and `DELETE`/`POST
/v2/patch-policies/{id}/dashboard` require `patch-policies:**read**` — dashboard
membership is modelled as a read on the title, not a delete on it.

### Enum behaviour

- **Unrecognised enum values are silently dropped, not rejected.** Pro
  deserialises an unknown member to null and proceeds; only a *recognised* value
  reaches business-rule validation. A typo in a caller's string therefore changes
  behaviour with no error. This is the whole argument for the generated constants.
- **The server is an oracle for value sets.** An invalid value on a validated Pro
  field returns `possible values can be [ A, B, C ]`. Six fields (computer +
  mobile-device extension attributes × `dataType`, `inputType`,
  `inventoryDisplayType`) were confirmed byte-identical to what we emit.
  `ListSmtpServerAllowedAuthTypesV2` returned all four spec values through the
  gateway on 2026-08-29, confirming that enum complete.
- **Classic validates neither `subset` nor `createdBy`** — garbage returns 2xx
  with the full record, and Classic's Tomcat HTML errors never list valid values.
  Classic has no oracle at all; its constants are documentation.
- **Read and write representations can disagree on casing.**
  `ComputerGeneral.platform` (read) is a bare `string` and returns `"Mac"`;
  `ComputerGeneralCreate.platform` (write) declares `[WINDOWS, MAC, NONE]`. Both
  are faithfully generated, but a consumer comparing a read value against
  `ComputerGeneralCreatePlatformMac` never matches. Do not assume a round-trip.
- **`manageExistingData` carries an unencoded precondition:** writable only on
  update, and only when `inputType` is `SCRIPT` with `enabled` false.
  `RETAIN`/`DELETE` are correct but rejected outside that state.
- `MDMCommandType` is now **unverifiable**: its oracle was the withdrawn
  `POST /v2/mdm/commands` type-resolution error (`known type ids = [...]`). The
  enum still generates from `MDMCommand` on the read side.

### Server bugs worth reporting

- `POST .../pro/v3/computer-groups/static-groups` 500s if `assignments` is
  omitted despite being optional in the spec; pass `assignments: []`.
- `POST /v2/mdm/commands` with `{}` answered **500 with an empty `errors` array**,
  while `{"clientData":[],"commandData":{}}` correctly answered `400 INVALID_FIELD`.
  (Recorded before the path was withdrawn.)
- `DownloadIconV1` answers **500** for an icon that uploaded successfully and
  returned a live CDN URL (traceId `3b39c7b12ddad6175c18030c5240c501`, id 2191).
  `TestAcceptance_Pro_IconV1` swallows it through a skip-on-server-error branch —
  contrary to the never-tolerate-real-errors rule — and because a 500 on a GET is
  retryable the test spends ~153 s in the retry loop first.

### The remaining 38 jpapi paths (whitelisted 2026-08-31)

The `pro` whitelist covered all 790 operations in `openapi-jpapi.json` as of that
date; v1897 withdrew two, leaving 788. The
last 38 were probed against `eu.api.jamfcloud.com` with a control
(`GET /pro/v1/jamf-pro-version`) in every invocation. Five disagreements with the
spec, all recorded in `config.json`:

| finding | evidence |
|---|---|
| `PUT /settings/obj/policyProperties` answers **200**, spec declares 201 | write-back of the tenant's own values returned 200 |
| `POST /v1/inventory-preload/history` and `POST /inventory-preload/history/notes` answer **200**, spec declares 201 | same call against `/v2/inventory-preload/history` and `/v1/self-service/settings/history` returns 201, so this is per-endpoint, not a dialect |
| unversioned `GET /inventory-preload` honours **`pagesize` only** — `page-size` is ignored | with four seeded records, `pagesize=2` → 2 rows, `page-size=2` → all 4. `/v1/inventory-preload` honours both |
| unversioned `GET /inventory-preload` sends the bare `{totalCount, results}` envelope, not the *array of* envelopes its `application/json` schema declares | identical body from the `/v1/` twin, which declares it correctly |
| `text/csv` request bodies are schemaless in the spec (`{"type":"object"}`) | `application/json` on `validate-csv` → **415** `Content-Type 'application/json' is not supported`; raw CSV → 412 with per-row codes |

The `expectedStatus` correction on `CreateInventoryPreloadHistoryNoteV1` fixes a
**method that was already shipping broken** — it has expected 201 since it was
whitelisted, so every call failed with "unexpected status code" while the write
itself succeeded.

Two paths are **unrouted at the gateway** while being present in
`_permissions/routes.yaml`. Both are generated and both are pinned by a test
that fails when the block lifts (`acc_pro_gateway_and_hosted_limits_test.go`):

- `GET /v1/dss-declarations/{declarationId}`. Declared with `declarations:read`,
  and the credential holds it — the two `ddmreport` operations requiring the same
  string (`ListDeclarationReportClients`, `GetDeviceDeclarationReport`) both
  answer 200 for it.
- `POST /v1/jamf-pro-server-url/history`, while **GET on the same path is routed
  and answers 200** — a method-level gap. Declared with `jss-url:update`, and the
  credential holds it: `PUT /pro/v1/jamf-pro-server-url` reaches Jamf Pro and
  answers 415 for an unsupported media type, which the gateway would have
  pre-empted.

The tell for both is response shape: the gateway emits compact JSON with a
`traceId` and `errors[].code == BAD_PERMISSIONS`, byte-identical to a
deliberately bogus path (`GET /pro/v1/zzz-not-a-real-endpoint`), whereas Jamf
Pro's own responses are pretty-printed. **That is a cheaper discriminator than a
second credential** for separating an unrouted path from a denied privilege —
but only in one direction: a compact 403 rules out Jamf Pro having answered, it
does not distinguish an unmapped path from an ungranted gateway capability.

Two more are routed and structurally refused, each pinned the same way:

- `PUT /v1/cache-settings` → **403 `HOSTED_ENVIRONMENT`**, "PUT command is not
  available in hosted environments". Pretty-printed, so Jamf Pro's own refusal.
  The GET is fine. Permanently unusable on Jamf Cloud.
- `POST /v1/macos-managed-software-updates/send-updates` → **503**, "This
  endpoint cannot be used if the Managed Software Update Plans toggle is on."
  The toggle check runs **before** field validation, which is what makes an
  empty-body probe safe on either kind of tenant.

**`GET /inventory-preload/csv-template` is not broken — an `Accept` header breaks
it.** An earlier pass here recorded it as server-broken on the strength of a 400
`INVALID_REQUEST_PARAMETER_TYPE` complaining that "csv-template" is not an
integer. The cause was the probe: Jamf Pro maps both
`/inventory-preload/{id}` (produces JSON) and `/inventory-preload/csv-template`
(produces `text/csv`) under one prefix, so `Accept: application/json` is
content-negotiated onto the `{id}` handler. The SDK sends no `Accept` on these
and gets a 200 `text/csv` template. Both header forms were run against the same
tenant to establish it. This is the "SDK-mediated observation is not wire truth"
rule inverted: **curl was the misleading observer**, and the disagreement was
still resolved by diffing the requests.

TeamViewer Remote Administration path parameters are typed `string` in the spec
and the server requires **a positive integer or `-1`** — a UUID gives
`400 INVALID_ID` naming `pathId`, `integrationID` or `configurationId` depending
on the operation. The generated `string` parameters are right; the constraint is
on the value.

### v1942 dropped 146 deprecated operations from the specs; every one is live (2026-09-01)

Build v1942 removed, from both `external/` and `internal/stage/`, every deprecated
operation that had a surviving successor version — 122 from `jpapi`, 20 from
`capi`, 2 from `declaration-reporting`, 2 from `securitycloud-devices`. Nothing
was added, no schema changed, and the 666 surviving `jpapi` operations and 675
schemas were byte-equal to v1897's. The SDK **took** the removals, as a deliberate decision to follow the specs —
so it is now stricter than the gateway on all 146. The evidence that the server
has withdrawn none of them, gathered 2026-09-01 against Jamf Pro
`11.31.1-t1787060595569`:

Probed with a `pro` credential on `eu`, `X-Tenant-Id` header form, with
`GET /pro/v1/jamf-pro-version` → `200 {"version":"11.31.1-…"}` as the control in
the same invocation. Every path below is one v1942 deleted:

| path | status | payload |
|---|---|---|
| `GET /pro/v1/computers-inventory?page-size=1` | 200 | `totalCount: 4`, first row `id 5` |
| `GET /pro/v2/computers-inventory?page-size=1` | 200 | identical body to v1 |
| `GET /pro/v3/computers-inventory?page-size=1` | 200 | identical body to v1 |
| `GET /pro/v4/computers-inventory?page-size=1` | 200 | identical body — the successor, as a second control |
| `GET /pro/v1/groups?page-size=1` | 200 | `totalCount: 382` |
| `GET /pro/v2/groups?page-size=1` | 200 | identical body — successor |
| `GET /pro/v1/inventory-preload?page-size=1` | 200 | `totalCount: 0` |
| `GET /pro/v2/inventory-preload/records?page-size=1` | 200 | identical — successor |
| `GET /pro/v1/computer-inventory-collection-settings` | 200 | preferences object |
| `GET /pro/v2/computer-inventory-collection-settings` | 200 | identical — successor |
| `GET /pro/v1/mobile-device-groups?page-size=1` | 200 | bare array, 2+ groups |
| `GET /pro/v2/patch-software-title-configurations` | 200 | `[ ]` |
| `GET /pro/v2/mobile-device-prestages?page-size=1` | 200 | `totalCount: 6` |
| `GET /pro/v1/mdm/commands?page-size=1` | 400 | `{"httpStatus":400,"errors":[]}` — its required-filter validation, i.e. routed and unrefused |
| `GET /proclassic/computers` | 200 | 4 computers |
| `GET /proclassic/computers/subset/basic` | 200 | 4 computers, basic subset |
| `GET /proclassic/patches` | 200 | `{"patch_management_software_titles":[]}` |
| `GET /proclassic/patchsoftwaretitles` | 200 | `{"patch_software_titles":[]}` |
| `GET /proclassic/patchpolicies` | 200 | `{"patch_policies":[]}` |

The deprecated version and its successor return **byte-identical bodies** wherever
both were sampled, so these are aliases at the gateway, not a legacy shape being
wound down.

Two further checks, each independent of the specs:

- **Four more confirmations on 11.31.1 (2026-09-01), control in the same
  invocation.** `GET /pro/v3/computers-inventory` → 200 with the same 4 records
  v4 returns. `GET /pro/v2/patch-software-title-configurations` → 200 **carrying
  a `deprecation` response header**, `date="Tue, 14 Jul 2026 00:00:00 GMT"` — the
  server is announcing the deprecation it has not enacted.
  `GET /pro/v1/computer-inventory-collection-settings` → 200 still exposing
  `applicationPaths`, `pluginPaths` and `fontPaths` as three sibling collections;
  the v2 response exposes only `applicationPaths`, which is why a v1-created FONT
  or PLUGIN path is invisible to a v2 reader and has to be cleaned up by id.
  And the sharpest: **`POST /pro/v1/…/custom-path` with `{"scope":"FONT"}` → 201**,
  the full three-scope vocabulary alive in the very build whose spec deleted the
  `CreatePathScope` enum. See the scope section below.
- **`internal/dev` in the same bundle carries every removed operation.**
  `internal/dev/jpapi` has all 788 and `internal/dev/capi` all 606 — op-for-op
  the v1897 `external/` sets — while `external/` and `internal/stage/` carry 666
  and 586. Re-checked on v1958: unchanged. So the deletion happens between dev
  and the prod/stage publish, which also explains `routes.yaml`: it is generated
  on the dev side of the filter. Diffing `internal/dev` *file contents* is still
  useless (its `x-generated` block churns every build) but comparing **operation
  sets** across environments is exactly what it is good for.
- **The gateway's authorization policy** is unmoved — its tip is still v1897's
  `/v1/system/initialize` deny, with no successor commit. The hand-written OPA
  allow blocks for `v1`, `v2` and `v3` `computers-inventory` (including the
  capability and wrong-token-guard variants) are all present.
- **The bundle's own `_permissions/routes.yaml`** is byte-different from v1897 and
  `sort`-identical: a pure reordering. 1474 route-method entries on both sides,
  zero removed, zero added, zero permission changes — `/computers`, `/patches`,
  `/patchpolicies` and `/v1/computers-inventory` all still declared with their
  capabilities. Since `routes.yaml` is generated from the specs'
  `x-required-privileges`, its generation must precede the filter, which places
  the deletion at the publish stage.

The only derivative change: `capi`'s OAuth scope list went 188 → 185, losing
`patch-management-software-titles:{create,update,delete}` — referenced solely by
the removed operations. `patch-management-software-titles:read` survives, on
`GET /patches/name/{name}`.

A fourth signal came out of the ingest itself: the withdrawn Security Cloud v1
group operations carried **`x-sunset-date: 2027-08-25`** next to
`x-successor-endpoint`, in the very build that deleted them — a spec deleting a
path a year before its own declared sunset.

**Report upstream, and re-diff every subsequent bundle.** The removal is a
publish-stage filter, not a withdrawal, and the SDK following it is a decision
rather than a corroboration.

### v1988 dropped 12 more `capi` operations; all twelve are live (2026-09-01)

Build v1988 removed, from both `external/capi` and `internal/stage/capi`, the
alternate-identifier computer lookups — `GET`, `PUT` and `DELETE` on each of
`/computers/macaddress/{macaddress}`, `/computers/name/{name}`,
`/computers/serialnumber/{serialnumber}` and `/computers/udid/{udid}`. 586 → 574
operations. `POST` survives on all four paths, so the paths themselves remain and
the unified rollups' path counts did not move. Nothing else in the spec changed:
a semantic diff with `description`/`example`/`title`/`summary` stripped reports
zero changed operations and zero changed schemas across all 169, and the OAuth
scope list is unchanged at 185. Nothing else in the bundle changed outside
`internal/dev`, the manifest and the two rollups.

The SDK **held** `capi` at v1897, so these twelve remain generated. The evidence
that the server has withdrawn none of them, gathered 2026-09-01 against Jamf Pro
`11.31.1-t1787060595569` with a `pro` credential on `eu`, `X-Tenant-Id` header
form, and `GET /pro/v1/jamf-pro-version` → `200` as the control in the same
invocation — twice, once before the reads and once after the writes:

| request | status | payload |
|---|---|---|
| `GET /proclassic/computers/serialnumber/Z4HDVQQPQ6` | 200 | `computer.general.id 31`, full General subset |
| `GET /proclassic/computers/udid/5F4308BD-…-45E83C06AED6` | 200 | same computer, byte-identical body |
| `GET /proclassic/computers/macaddress/CA:13:8A:B9:B5:10` | 200 | same computer |
| `GET /proclassic/computers/name/FVFZCAK0LYWH` | 200 | `id 4` — the name is not unique on this tenant, and the server resolves it to the lowest id |
| `GET /proclassic/computers/serialnumber/NOSUCHSERIAL0` | 404 | Jamf Pro's HTML `Status page` / `Not Found` |
| `GET /proclassic/computers/udid/00000000-0000-0000-0000-000000000000` | 404 | same HTML |
| `GET /proclassic/computers/macaddress/00:00:00:00:00:00` | 404 | same HTML |
| `GET /proclassic/computers/name/zzz-no-such-computer-probe` | 404 | same HTML |
| `PUT` on each of the four bogus identifiers, `text/xml` body | 404 | same HTML, 4/4 |
| `DELETE` on each of the four bogus identifiers | 404 | same HTML, 4/4 |

**The 404 is the routed tell, and it is the application's, not the gateway's.** An
unrouted gateway path answers a *repeated* `403 BAD_PERMISSIONS`; a wrong base
path answers plain-text `404 page not found`. This is Jamf Pro's own styled HTML
error page, which means the gateway routed the request and the application looked
the computer up and did not find it. Probing the destructive verbs against a
nonexistent identifier is what makes them safe to probe at all: there is nothing
to mutate or delete, and resource resolution answers before anything else runs.
The tenant is unchanged — `GET /proclassic/computers` returns the same four
computers before and after.

Three further signals, none of them the wire:

- **`internal/dev/capi` in the same bundle carries all 606 operations** — the
  v1897 `external/` set, op-for-op — while `external/` and `internal/stage/` carry
  574. The publish-stage filter caught in the act again.
- **The same bundle's privilege oracle still declares all twelve.**
  `external/_permissions/routes.yaml` is byte-different from v1981 but
  `sort`-identical: 6244 lines both sides, zero entries removed, and each of the
  four paths still lists `POST`, `GET`, `PUT` and `DELETE` with
  `devices:create`/`users:create`, `devices:read`, `devices:update`/`users:update`
  and `destructive-device-actions:execute` respectively. `_permissions/scopes.yaml`
  is byte-identical.
- **The gateway's authorization policy has nothing.** Its tip is unmoved, and the
  Classic computers policy retains every allow block for all four verbs on all
  four paths.

**Unlike v1942's removals, none of these declares a successor.** Each carries
`deprecated: true` and `x-deprecation-date: 2025-02-11T00:00:00.000Z`, and no
`x-successor-endpoint`. The functional replacement is an RSQL filter on Pro
`GET /v1/computers-inventory` — a capability, not a declared migration path. Note
also that `GET /computers/match/{match}` and `GET /computers/match/name/{matchname}`
carry the identical deprecation date and were **not** removed, so the filter is not
even consistent within the family.

**These twelve were taken into `config.json` on 2026-09-02** — see *The v1993
config alignment* below — so they are no longer pinned. Both tests that pinned
them, `TestAcceptance_Classic_AltIdentifierComputersStillRouted` and
`TestAcceptance_Classic_GetComputerByName`, were deleted with the methods they
called; a withdrawn operation cannot be pinned from inside the SDK. The evidence
above stands as the record that the wire still served all twelve on the day the
SDK stopped generating them.

**Report upstream** alongside the v1942 finding: same defect, same bundle
generation stage, and this time the deleted operations have no successor to
migrate to.

### What v1942's removal cost, probed the same day

Three capability gaps opened, each now pinned by a test that fails when it
closes. All probed 2026-09-01 with `GET /pro/v1/jamf-pro-version` → 200 as the
control in the same invocation.

**Security Cloud has no working device-group update.**
`PUT /v1/groups/{groupId}` is gone and its declared successor
`PUT /v2/groups/{groupId}` is unrouted — 403 `BAD_PERMISSIONS`, 7/7 on
2026-08-29, unchanged. `ApplyDeviceGroupV2` used the v1 write for its update
branch and now uses the v2 one, so Apply cannot update. The standalone pin
`TestAcceptance_SecurityCloudUpdateDeviceGroupV2` survives but its control had
to weaken from a v1 *write* to a v1 read: no working write remains to control
with.

**Patch software titles are unseedable.** The only path that minted an id the
Pro v3 configuration endpoints address was Classic's
`POST /patchsoftwaretitles/id/0`, now withdrawn. The alternatives do not work:

| probe | result |
|---|---|
| `GET /proclassic/patchavailabletitles/sourceid/1` | 200, `size: 1552`; every entry is `{name_id, last_modified, current_version, app_name, publisher}` — **no numeric id field of any kind**, in JSON or XML |
| `POST /pro/v3/patch-software-title-configurations` with `softwareTitleId` = a catalogue `name_id` (`376`, `518`, `575`) | `400 SOFTWARE_TITLE_ID_NOT_FOUND` attributed to `softwareTitleId`, all three |
| … with `softwareTitleId: "Firefox"` | `400 INVALID_ID`, *"id field must be string of positive numeric value or -1"* |
| `GET /pro/v1/patch-software-titles` | `403 BAD_PERMISSIONS` (unrouted) |
| `GET /pro/v2/patch-software-titles` | `403 BAD_PERMISSIONS` (unrouted) |
| `GET /pro/v3/patch-software-title-configurations` | 200 `[ ]` — nothing pre-existing to borrow, and borrowing would mean mutating a tenant's own configuration |

**`patchavailabletitles` cannot supply a `softwareTitleId`, and the reason is
structural rather than empirical.** Of the 1552 `name_id` values in the Jamf
source catalogue, **916 are non-numeric** — `0F5`, `0F6`, `2B8`, `2DE`, `41D`,
`62C` and so on, hex-ish patch-*definition* keys — while `softwareTitleId` must be
a positive numeric string. So 59% of the catalogue cannot even be expressed as a
`softwareTitleId`, and the 636 that happen to be all-digits are rejected anyway
(`376`, `518`, `575` above). The two are different identifier spaces:
`name_id` names *what to subscribe*, `softwareTitleId` names *a subscription*, and
only Classic's `POST /patchsoftwaretitles/id/0` creates one.

`softwareTitleId` must be a **positive numeric string** naming a Jamf Pro
software-title record — `Firefox` returns `400 INVALID_ID` *"id field must be
string of positive numeric value or -1"*, so it is an id space, not a name. The
catalogue is healthy on the probe tenant (internal source id 1, "Jamf",
`enabled: true`, 1551 available titles), which rules out tenant state: what is
missing is the step that *subscribes* a catalogue title and mints the id, and
the only endpoint that does it is the withdrawn Classic POST. A grep of the whole
v1942 bundle — `internal/dev` included — finds no `*softwaretitles*` path other
than the two Classic ones.

**`softwareTitleId` IS the Classic title id, and the only call that mints one
creates the v3 configuration in the same act.** Proved end to end 2026-09-01 with
a control in the same invocation:

| step | result |
|---|---|
| `POST /pro/v3/patch-software-title-configurations` `softwareTitleId: "575"` (a catalogue `name_id`) | `400 SOFTWARE_TITLE_ID_NOT_FOUND` |
| `POST /proclassic/patchsoftwaretitles/id/0` `{name, name_id: 575, source_id: 1}` | **201**, `<id>146</id>` |
| `GET /pro/v3/patch-software-title-configurations` | shows `id=146`, **`softwareTitleId=146`**, `softwareTitleNameId=575` — the same object |
| `POST /pro/v3/…` `softwareTitleId: "146"` | `400 ALREADY_EXISTS_FOR_SITE` (`field: displayName`) |
| `DELETE /pro/v3/patch-software-title-configurations/146` | 204, and the **Classic list is empty too** |

So the v3 create requires an id that only exists once the object exists. **There
is nothing for a create to migrate onto** — this is not "no usable create path
yet", it is structurally impossible on the current surface.

**Therefore the `capi` hold is permanent, not provisional.** It lifts only if
Jamf gives the configurations surface an id-minting create (or publishes a routed
catalogue endpoint that hands out `softwareTitleId`s) — not on the next bundle,
and not on a policy change. Record it that way.

Pro v3 is otherwise a superset of the Classic resource — `displayName`,
`softwareTitleNameId`, `uiNotifications`/`emailNotifications`, `categoryId`,
`siteId`, `extensionAttributes`, and `packages[] {packageId, version,
displayName}` covering the provider's `version_packages`, plus seven
sub-resources Classic never had; only `source_id` lacks an equivalent (v3 has
`patchSourceName` / `patchSourceEnabled`). The blocker is the identifier alone.

Also re-confirmed on 11.31.1: `GET /pro/v4/patch-software-title-configurations`
→ **403**, unchanged from the 11.30.2 observation.

**`GET /patches/name/{name}` answers 500 unconditionally.** First recorded
2026-09-01 as "500, not 404" — `500` for an obviously-absent name 3/3 and again
for a plausible one (`Firefox`), on a tenant whose `/patchsoftwaretitles` list
was empty — which framed it as a wrong status for a missing title.

**~~for a name the tenant does not have~~ — re-probed 2026-09-04, it is not
state-dependent at all**, on a tenant that *does* have a title and with
controls in the same invocation:

```
GET /proclassic/patches                          → 200  <patch_management_software_titles><size>1</size>
                                                          …<id>1</id><name>Microsoft Defender</name>…
GET /proclassic/patches/id/1                     → 200  (control)
GET /proclassic/patches/name/Microsoft%20Defender → 500  Jamf Pro's own HTML
GET /proclassic/patches/name/010%20Editor         → 500  (a freshly seeded title)
GET /proclassic/patches/name/NoSuchTitleXyz       → 500
```

So the endpoint is broken for every name, present or absent, while the by-id
form on the same path works. Report upstream.

**Now pinned.** `TestAcceptance_Classic_PatchByName` used to reach
`skipOnServerError` and skip, which is the right convention for a *transient*
5xx and exactly wrong for a permanent one — the test could never report the
fix. It now asserts the 500 and fails if the call ever succeeds. The name it
probes with comes from Pro v3's configuration list; `ListPatches` is back at
v2082 and could source it too, but the v3 list is the surface a caller would
use.

**Classic computers: the withdrawal is incoherent, and it was taken anyway.**
v1942 removes `GET` and `PUT /computers/id/{id}` while keeping `POST` and
`DELETE` on the same path, and removes both `GET /computers/subset/basic` and
`/computers/id/{id}/subset/{subset}` with no equivalent anywhere. Taken on
2026-09-02 regardless — see *The v1993 config alignment* below. The migration
this section predicted is the one that was used and it works:
`GET /proclassic/computers/match/*` answers 200 with `id`, `name`, `udid`,
`serial_number` and `mac_address` per row, and `MatchComputers` returns the same
`*Computers` type `ListComputers` did, so it was a drop-in enumeration
replacement across six subset/filter fixtures and `ComputerCRUD`. What was lost
is real and has no replacement: the two subset reads, every by-identifier read,
and both by-identifier updates. `MatchComputers` is the only computer lookup
left in the package.
`GET /proclassic/patchpolicies/id/99999999/subset/General` → 404, as expected.

### The v1993 config alignment (2026-09-02)

The whitelist was aligned to `external/capi` at v1993 **without ingesting the
spec** — `testing/Classic-openapi.json` stays at v1897. **31 of the 32 withdrawn
operations were dropped** from `config.json`, leaving `proclassic` at 575. The
single operation kept back is `POST /patchsoftwaretitles/id/{id}`, held for one
reason only: nothing else can mint a `softwareTitleId` for the Pro v3
patch-configuration endpoints. The other four in that family — the collection
`GET`, and `GET`/`PUT`/`DELETE` on the item path — went with the rest. So the
surface mirrors the published spec except for that one `POST`, and the hold is
one operation wide.

The v1993 diff itself is a pure withdrawal — verified 606 → 574 operations and
273 → 262 paths, zero operations added, **zero semantic change to any of the 574
survivors**, and `components/schemas` identical at 169 both sides. The only
non-operation change is the OAuth scope list, 188 → 185: the three
`patch-management-software-titles:{create,update,delete}` write scopes orphaned
by the removals. `info.version` is `11.28.0` on both sides.

| family | ops dropped | what is left |
|---|---|---|
| `computers` | 17 | `POST` + `DELETE /computers/id/{id}`, `POST` on each of the four alternate-identifier paths, `GET /computers/match/{match}`, `GET /computers/match/name/{matchname}` |
| `patches` | 6 | `GET /patches/name/{name}` only |
| `patchpolicies` | 2 | `GET`/`PUT`/`DELETE /patchpolicies/id/{id}`, its subset read, `POST /patchpolicies/softwaretitleconfig/id/{id}` |
| `patchreports` | 2 | nothing |
| `patchsoftwaretitles` | 4 | **`POST /patchsoftwaretitles/id/{id}` only — held** |

**None of the 31 declares a successor.** `x-successor-endpoint` does not exist as
a key anywhere in `capi`, in either version, in any environment — so the
additive-versions rule's deprecated-with-successor override does not reach them,
and following the spec here is the same deliberate override v1942 was.

**The three signals that the server has withdrawn nothing still hold**, so the
SDK is now materially stricter than the gateway on these 31: `internal/dev/capi`
in the v1993 bundle carries all 606 operations, all 169 schemas and all 188
scopes, byte-equal to the v1897 baseline modulo host and title;
`external/_permissions/routes.yaml` still types `proclassic` at **273 routes** —
the unfiltered count — and grants every one of the 31; and `internal/stage/capi`
carries the identical removals, so this is a publication filter and not a
prod-lagging rollout.

What the Go tree lost: 31 methods, their 31 privilege-registry entries, 59 types
(`Computer` and its whole nested tree, `ComputersBasic`,
`PatchManagementSoftwareTitles`, `PatchPolicies`, `PatchReport` and their
children), and the `ComputerHardwareSipStatus` enum. `ComputerPost` survives —
the five surviving `POST`s take it. `Computers` survives via `MatchComputers`.
`patchreports.go` and its test were removed by `pruneStale`, which is the guard
working as designed. `PatchSoftwareTitles` and its two children went with the
collection read; `PatchSoftwareTitle` itself stays as the surviving `POST`'s
request and response type.

**Classic can no longer enumerate patch software titles at all**, which is what
makes the `POST`-only shape bite. Every Classic fixture and cleanup that went
through that family now goes through Pro v3: `Classic_PatchByName` takes its
name from `pro.ListPatchSoftwareTitleConfigurationsV3` (`SoftwareTitleName` is
the same software-title name `GET /patches/name/{name}` keys on), and
`Classic_ProbeCreate_CreatePatchSoftwareTitleByID` cleans up with
`pro.DeletePatchSoftwareTitleConfigurationV3` — the same id, and deleting there
removes the Classic record, as `seedPatchSoftwareTitleFixture` already relied
on. `Classic_ListPatchSoftwareTitles` was deleted with its operation. The seed
helper itself is unaffected: it only ever needed the `POST`.

**31 nested types were also renamed, and the reason is a generator fix this
change forced.** `ComputerGeneralManagementStatus` and 30 siblings became
`ComputerPostGeneralManagementStatus` etc. Those inline sections are declared on
the read schema and reach `computer_post` through `applyPostSymmetry`'s shared
pointers, so `hoistInlineObjects` named them after whichever parent it reached
first in sorted order — `computer`. With `computer` unreachable the name was
being taken from a schema the SDK no longer emits, and worse, only in the
`testing/` path: `publishSpecs` prunes before writing `api/*.json`, so CI —
which generates from `api/` — produced the `ComputerPost*` names and
`git diff --exit-code -- jamfplatform/` failed on 31 renames nothing in
`config.json` mentions. `pruneUnreferencedSchemas` now runs on both paths
between post-symmetry and hoisting, which makes the two inputs identical by
construction; `collectRefs` takes the whitelist because an OAS3 document is
loaded whole and a withdrawn operation's `$ref`s would otherwise keep its
schemas alive. Blast radius was `proclassic/types.go` alone — no other package
moved a byte — and no downstream repo names any of the 56 removed or 31 renamed
types. Mechanism and the ordering invariant:
[STYLE.md](STYLE.md#schema-handling). **The local generate cannot see this class
of break**, since it reads `testing/`; force the `api/` fallback whenever a
change alters which operations are whitelisted.

**Acceptance coverage.** Seven test functions and one whole file went as
superseded: `acc_proclassic_alt_identifier_test.go` in full (both its tests
existed only to pin withdrawn operations), plus `Classic_GetComputerByID`,
`Classic_ListComputers`, `Classic_ListPatches`, `Classic_ListPatchPolicies`,
`Classic_GetComputersBasic`, `Classic_ListPatchSoftwareTitles`,
`Classic_ComputerByIDSubset`,
`Classic_ComputerByMacAddress`, `Classic_ComputerByUDID`,
`Classic_PatchComputersByIDVersion`, `Classic_PatchReportByTitleIDVersion` and
`Classic_ProbeCreate_CreatePatchByID`. Nine fixtures were repointed rather than
deleted: six subset/filter tests and `Classic_GetComputerHistoryByID` moved from
`ListComputers` to `MatchComputers(ctx, "*")` — same `*Computers` return type, so
the change is one line each; `Classic_PatchByName` moved from `ListPatches` to
`ListPatchSoftwareTitles`; and `Classic_PatchPolicyByIDSubset` now sources its id
from `pro.ListPatchPoliciesV2`, because **both** Classic patch-policy
enumerations were withdrawn and the Classic surface can no longer discover a
patch policy id at all. `Classic_ComputerCRUD` was rewritten to the two verbs
that survive on its path — create, then recover the id through `MatchComputers`,
then delete, with absence from a later match as the post-delete assertion since
no read remains.

**One coverage gap is worth naming and it predates this change.**
`CreateComputerByName`, `CreateComputerBySerialNumber`,
`CreateComputerByMacAddress` and `CreateComputerByUDID` have no acceptance test
and had none at v1897 either. That mattered less when those paths carried four
verbs; they now carry `POST` alone, so the untested method *is* the whole
surface. Same for `GetPatchPolicyByID` and `UpdatePatchPolicyByID`, untested before and
after. `GetPatchSoftwareTitleByID` and `UpdatePatchSoftwareTitleByID` were in
that list too and are now simply gone.

### Custom inventory paths: the scope vocabulary narrowed at v2, v1 still serves all three

`computer-inventory-collection-settings/custom-path` takes a `scope`, and the two
versions do not accept the same set. Probed 2026-09-01 on 11.31.1 with
`GET /pro/v1/jamf-pro-version` → 200 as the control:

| request | result |
|---|---|
| `POST /pro/v1/…/custom-path` `{"scope":"APP"}` | **201** |
| `POST /pro/v1/…/custom-path` `{"scope":"FONT"}` | **201** |
| `POST /pro/v1/…/custom-path` `{"scope":"PLUGIN"}` | **201** |
| `POST /pro/v2/…/custom-path` `{"scope":"APP"}` | **201** |
| `POST /pro/v2/…/custom-path` `{"scope":"FONT"}` | **400 `INVALID_FIELD`** |
| `POST /pro/v2/…/custom-path` `{"scope":"PLUGIN"}` | **400 `INVALID_FIELD`** |
| `POST /pro/v2/…/custom-path` `{"scope":"BOGUS"}` | **400 `INVALID_FIELD`** |
| `POST /pro/v2/…/custom-path` `{"scope":"app"}` | **400 `INVALID_FIELD`** |

**So v2 accepts exactly `[APP]`, and it is case-sensitive** — lowercase `app` is
refused identically to `BOGUS`. The 400 is **field-attributed** (`field: "scope"`,
unlike the jsc-dns/jsc-ztna `INVALID_FIELD` shape which names nothing) and it
**names the whole accepted set while leaking the Java DTO class**:

```
Cannot deserialize value of type
`com.jamfsoftware.jamfmanagementframework.web.computerinventorycollectionsettings.dto.PathScopeV2Dto`
from String "FONT": not one of the values accepted for Enum class: [APP]
```

Two things follow. **The narrowing is a real v2 API change, not a publish
artifact** — v1 serves `FONT` and `PLUGIN` on the same instance, minutes apart.
And v1942 deleting the multi-value `CreatePathScope` enum along with the V1 type
left the surviving `CreatePathV2.Scope` a bare `string` with its one legal value
recorded only in prose; that is one of the three cases that forced the generator
to start emitting single-value and numeric enum constants (see
[STYLE.md](STYLE.md#skips-and-why-every-skip-still-reaches-the-caller)).

Cleanup note: a `FONT` or `PLUGIN` path created through v1 does **not** appear in
the v2 settings response, so a probe that tidies up by reading v2 will leave them
behind. Read `/pro/v1/computer-inventory-collection-settings`, which lists all
three collections, and delete by id.

### App Installers: the write surface exercised (2026-09-03)

The ingest at v2043 covered the reads and probed the deployment-scoped ones with
an impossible ID. The write surface has now been run end to end on the eu
sandbox under `JAMFPLATFORM_ACC_PRO_APP_INSTALLERS_WRITE_OK`, creating a
**disabled, unscoped `SELF_SERVICE`/`MANUAL` draft** — which installs nothing —
and deleting it in the same test. Four things came out of it that the spec does
not say, all confirmed curl-direct with
`GET /pro/v1/jamf-pro-version` → 200 and
`GET /pro/v1/app-installers/bogus-control` → 403 as controls in the same
invocation.

**Both history-note `POST`s answer `200`, and the spec declares only `201`.**
`POST /v1/app-installers/deployments/{id}/history` and
`POST /v1/app-installers/global-settings/history` each return the created
`ObjectHistory` under HTTP 200. The spec's `responses` map carries `201` (plus
`404`/`503`) and no `200` at all, so the generator derived `http.StatusCreated`
and **both methods failed on every successful call** — the transport reported
`API request failed with status 200 OK` while quoting the object it had just
created. Same defect class as `CreateInventoryPreloadHistoryNoteV1`, and the same
fix: an explicit `"expectedStatus": 200` in `config.json`. Note that *deleting*
the override is inert — absent one, the generator reads the spec's 201 — so the
value has to be stated. Report upstream. `POST /v1/app-installers/deployments`
itself is genuinely `201`, so this is per-operation, not a family-wide error.

**The create returns a real `id`; the `href` beside it is not callable.**
`HrefResponse` carries both, and the body is
`{"id": "7", "href": "https://<instance>.jamfcloud.com/api/v1/app-installers/deployments/7"}`
— the Jamf Pro **instance** hostname with an `/api` prefix, not the gateway root.
So take `created.ID` and never dereference the href: the SDK's base URL cannot
reach that host, and the `/api` segment does not exist on the GA gateway.

**An omitted `smartGroupId` reads back as `"-1"`, never `""`.** `-1` is this
API's no-assignment sentinel — the same one `categoryId` and `siteId` document in
their own descriptions, but `smartGroupId`'s description says only that a null
means the installer will not be deployed. So an unscoped-draft assertion has to
accept both spellings; treating `""` as the only unscoped value fails on a
deployment that is in fact targeting nothing.

**Both installation retries answer `404` with an empty `errors` array when there
is nothing to retry**, and that is Jamf Pro's own response rather than a routing
gap. `POST /v1/app-installers/deployments/{id}/computers/installation-retry`
returned `{"httpStatus": 404, "errors": []}` for a deployment `GET` returned 200
for in the same invocation, and the idless
`POST /v1/app-installers/deployments/computers/installation-retry` answered
identically. The unrouted tell in this namespace is `403 BAD_PERMISSIONS`, which
the bogus-path control returned, so these requests are routed and refused on
state. The spec documents no status for the empty case. Both are asserted as 404
in `TestAcceptance_Pro_AppInstallers_Writes`, so a change to 2xx fails the test
rather than passing silently.

Still unexercised, and needing a deployment that targets real machines:
`RetryAppInstallerDeploymentComputerInstallationV1` and
`UpdateAppInstallerDeploymentVersionV1`. Both keep their impossible-ID probes.
Also still unverified: whether a post-GA **tenant** credential reaches any of
this — only an environment credential has been live since the gateway opened.

### The restored Classic patch family (2026-09-04)

v2082 republished every Classic patch-management operation v1942 withdrew
(upstream's restoration change). Probing them before writing coverage found three
defects, two of which had been shipping silently in the SDK since the day
those methods first appeared, and none of which a status-only test could see.

All probes on Jamf Pro 11.31.1 through an **environment** credential, with
`GET /proclassic/patchsoftwaretitles` at 200 in the same invocation as the
control and a fixture minted through `POST /patchsoftwaretitles/id/0`.

**1. `GET /patchpolicies/softwaretitleconfig/id/{id}` answers a collection,
not the singular object the spec declares.**

```
GET /proclassic/patchpolicies/softwaretitleconfig/id/1  → 200
<?xml version="1.0" encoding="UTF-8"?><patch_policies><size>0</size></patch_policies>
```

The spec declares `#/components/schemas/patch_policy` for both
`application/xml` and `application/json`. The wire root element is
`patch_policies`. `responseType` is now `patch_policies`, so
`ListPatchPoliciesBySoftwareTitleConfigID` returns `*PatchPolicies` — which
its name always promised.

**2. `GET /patches/id/{id}/version/{version}` answers `<software_title>`, and
the pre-v1942 config said `computers`.**

```
GET /proclassic/patches/id/1/version/101.26062.0012  → 200
<?xml version="1.0" encoding="UTF-8"?><software_title><id>1</id><name>Microsoft Defender</name>
  <name_id>256</name_id><source_id>1</source_id>…<versions><version><software_version>…
```

It is the title filtered to one version. The spec declares **no schema at
all** for this operation, so `"responseType": "computers"` was the SDK's own
guess rather than a spec transcription, and it was wrong. Now `software_title`,
and the method is renamed `GetPatchComputersByIDVersion` →
**`GetPatchByIDVersion`**: a name promising computers over a `SoftwareTitle`
return is worse than a rename, and no consumer can have depended on the old
one because it never returned data.

**Why neither failed loudly, and the general lesson.** Generated Classic types
carry `XMLName xml.Name` with no struct tag. Go's `encoding/xml` does not
validate an untagged root, and none of `<patch_policies>`'s or
`<software_title>`'s children map onto the wrongly-chosen struct's fields — so
both calls decoded a **zero-valued struct and returned `nil` error**. A
consumer saw an empty result, not a failure. So: **a restored operation needs
its response *shape* probed, not just its status**, and a `responseType`
override the spec does not corroborate is a guess until the wire agrees with
it.

**3. `POST` and `PUT /patches/id/{id}` are refused whatever the body.** Jamf
Pro's own HTML `400`, not a gateway JSON error, so this is routed and refused
by Jamf Pro itself:

```
PUT /proclassic/patches/id/79
  <software_title><name>010 Editor</name><notifications>
    <email_notification>false</email_notification></notifications></software_title>
  → 400  "Error in XML file"

PUT, same path, root <SoftwareTitle> (what the Go type marshals)   → 400  "Error in XML file"
PUT, same path, <software_title><name>010 Editor</name></software_title> → 400  "Error in XML file"

POST /proclassic/patches/id/0
  <software_title><name>010 Editor</name><name_id>518</name_id><source_id>1</source_id></software_title>
  → 400  "Error in XML file.  Possible mismatch between resource specified in
          the URL and XML file"

DELETE /proclassic/patches/id/999999  → 404  (Jamf Pro's own HTML — routed)
GET    /proclassic/patches/id/79      → 200  (control, same invocation)
```

Three body shapes, including the spec's own root element and a minimal
one-field body, all rejected identically. So this is not a marshalling problem
the SDK can fix and not a body-shape guess: **`/patches` is a read-and-delete
surface whose spec declares four verbs.** Report upstream.
`TestAcceptance_Classic_PatchByIDWrites` asserts both refusals as 400 and
fails if either starts working, at which point it becomes a real round-trip.

**What does work, all 200 with real data**: `GET /patches`,
`GET /patches/id/{id}` (with the version catalogue —
`total_versions: 113` on the sampled title, which
`GET /patchsoftwaretitles/id/{id}` does not carry),
`GET /patchsoftwaretitles`, `GET /patchsoftwaretitles/id/{id}`,
`PUT`/`DELETE /patchsoftwaretitles/id/{id}`, both `/patchreports` forms, and
`GET /patchpolicies`. The version-scoped report narrows `total_versions`
114 → 1, which is the assertion that proves the path segment is honoured
rather than ignored.

### Export-report was never callable, and `Accept` is why (2026-09-09)

`GET /v3/patch-software-title-configurations/{id}/export-report` declares an
`in: header` `accept` parameter the SDK had no way to send, and **without one
the endpoint answers 400**. So `ExportPatchSoftwareTitleReportV3` had failed
every call it ever made. Go sends no `Accept` unasked and the transport sets
none on Pro paths, so there was no accidental default to rescue it.

**The recorded diagnosis was wrong, and it had stood since 11.30.2.**
`assertPatchExportReport` said "both V2 and V3 answer 400 when the
configuration's patch report has no rows … the rejection is a property of an
empty report", tolerating the 400 whenever `reportRows == 0`. The probe behind
that never set `Accept`. Re-probed on config `1` (patch report `totalCount: 0`)
with `GET /pro/v1/jamf-pro-version` at 200 as the control:

| `Accept` | `columns-to-export` | status | body |
|---|---|---|---|
| absent | two columns | **400** ×2 | `{"httpStatus":400,"errors":[]}` |
| `text/csv` | two columns | **200** | `computerName,version` — 21 bytes, `Content-Type: text/csv` |
| `text/tab` | two columns | **200** | `computerName\tversion` — 21 bytes, `Content-Type: text/tab` |
| `text/csv` | omitted | **400** | Tomcat's HTML 400 |
| `text/csv` | `columns-to-export=` | **500** | `java.lang.IllegalArgumentException: There has to be at least one column to export` |
| `text/csv` | `nonsuchColumn` | **200** | `nonsuchColumn` — column names are not validated |

So a zero-row report exports fine, as a header-only body, and the two media
types genuinely differ — same 21 bytes, comma versus tab. **TSV was
unreachable** until the `accept` parameter existed.

**Error serialisation follows `Accept` too**, which is the tell that the header
was always being read: absent or `application/json` gives Jamf's JSON error
envelope, while `text/csv`, `text/tab` and `application/pdf` all give Tomcat's
HTML 400. A probe that only ever looked at status would miss it.

**`columns-to-export`'s recorded reasoning changes but its conclusion holds.**
`hasSpecDefault` keeps the zero-value guard on that parameter — omitting it
rather than sending `""` — on the grounds that omitting and sending a valid list
both answered 400 while `columns-to-export=` answered 500. The first half of
that was the missing `Accept`, not the parameter. With `Accept` set, omitting is
a **400** because the server applies no default despite the spec declaring a
nine-column one, and sending empty is still a **500**. Omitting still beats
sending empty, so the mechanism is unchanged; only the evidence is.

`assertPatchExportReport` now exports under both media types, asserts the
delimiter differs, and **asserts** both refusals rather than tolerating them —
the no-`Accept` 400 especially, so nobody drops the argument again. Verified
against a freshly seeded config (id 487) with a zero-row report: both exports
returned 21 bytes.

Report upstream: the spec should declare `enum: [text/csv, text/tab]` on the
parameter and mark it required, and `columns-to-export`'s declared default is
not applied.

### `Accept-Language` is accepted and inert (2026-09-09)

`GET` and `PATCH /pro/v3/account-preferences` each declare an `in: header`
`Accept-Language` — "Locale to be used" — that the SDK could not send until the
generator learned header parameters. It is now reachable and changes nothing
observable.

Probed with `fr-FR`, `de-DE`, `ja-JP` and a bogus `xx-ZZ`: every response is
byte-identical to the header-free one, `"language": "en"` throughout. A PATCH
with a deliberately invalid `resultsPerPage` returns the same untranslated
Jackson message with and without the header:

```
"description" : "Cannot deserialize value of type `java.lang.Integer` from String \"not-a-number\": not a valid `java.lang.Integer` value"
```

The response carries no prose to localise, so there may be nothing for the
header to do here. The acceptance test pins **reachability**, not inertness — an
assertion that the bodies match would fail the day Jamf starts honouring it,
which is the wrong way round — but it does require a bogus locale to stay
accepted, since a rejection would mean the header had started being validated.

### v2121 restored `GET /v1/mdm/commands`, and it has two wire laws the spec does not state (2026-09-09)

v2121 is the second restoration of a v1942 withdrawal (`GET /v3/computers-inventory`
was the first, at v2082). `jpapi` goes 700 → 701 operations, zero schemas added
or changed — `MdmCommand` never left the spec — and the recovered `config.json`
entry is the pre-v1942 one verbatim, so this is a revert rather than a
re-derivation.

It is a two-parameter point lookup, not a paginated list, so it is not reachable
through `ListMdmCommandsV2` and needed its own method and its own coverage.
Probed on the EU environment `<jsc-tenant-b>` with `GET /pro/v1/jamf-pro-version` at
200 and a bogus `/pro/v1/…` path returning the unrouted `403 BAD_PERMISSIONS` as
controls in the same invocation:

| request | status | body |
|---|---|---|
| `?uuids=<real>` | **200** | the one command, as `[{…}]` — a bare array, matching the spec |
| `?client-management-id=<real>` | **200** | every command for that client (136 on this tenant) |
| `?uuids=<bogus>` / `?client-management-id=<bogus>` | **200** | `[ ]` |
| neither parameter | **400** | `{"httpStatus":400,"errors":[]}` — deterministic 2/2 |
| **both** parameters | **500** | `{"httpStatus":500,"errors":[]}` — deterministic 2/2 |
| 41 `uuids` | **414** | `INVALID_SIZE` / "This lookup is limited to 40 or fewer UUIDs." |

**Two things to report upstream.** The spec marks both parameters optional while
the server refuses a request with neither, and both bodies carry an **empty
`errors` array** so there is no attribution to work from. And the spec's own
wording — "Choose one of two parameters, but not both" — describes a constraint
the server implements as a **500**, not the 400 it should be.
`TestAcceptance_Pro_MdmUpdates_ListMdmCommandsV1` asserts the 500 rather than
skipping on it, so the test fails the day it is fixed;
`skipOnServerError` would have made the fix invisible, which is the same mistake
`GET /patches/name/{name}` taught.

**The generator's comma-joined `[]string` is correct here even though the spec
says `explode: true`.** `uuids=a,b` and `uuids=a&uuids=b` return the identical
two rows, so the generated `strings.Join(uuids, ",")` needs no override.

The endpoint still sends `Deprecation: date="Mon, 16 Oct 2023 00:00:00 GMT"`,
which the transport logs.

### Self Service categories: `display_in` is what stores them, and only the mobile profile hides it (2026-09-07)

All six Classic resources carrying a `self_service.self_service_categories`
block were probed on Jamf Pro 11.31.1 with an EU tenant, raw XML, each write
carrying `feature_on_main_page` as an in-request control so a write that did
not land could not be misread as the server discarding the category.

**The law is identical on all six.** A `<category>` inside
`<self_service_categories>` persists only when it carries
`<display_in>true</display_in>`:

| sent | stored |
|---|---|
| `<id>` alone | discarded |
| `<id>` + `<name>` | discarded |
| `<id>` + `<feature_in>true` | discarded |
| `<id>` + `<display_in>true` | **persisted** |
| `<id>` + `<display_in>true` + `<feature_in>true` | **persisted** |
| `<id>` + `<display_in>false` | discarded |

So `display_in=false` is a deletion gesture rather than a stored value, and
`feature_in` on its own does nothing — it is only meaningful alongside
`display_in`.

**The read is where they differ, and the difference is the finding.**

| resource | `display_in` echoed on read | `feature_in` stored |
|---|---|---|
| `mobile_device_configuration_profile` | **NO** | no |
| `mobile_device_application` | yes | no |
| `os_x_configuration_profile` | yes | yes (default `false`) |
| `policy` | yes | yes (default `false`) |
| `ebook` | yes | yes (default `false`) |
| `mac_application` | yes | yes (default `false`) |

`mobile_device_configuration_profile` is the only one of the six that hides
`display_in` from the read, so it is the only one no client can drift-detect.
**Report upstream**: the resource whose spec omitted the property — it `$ref`ed
the shared `category` schema (`{id, name, priority}`), which is what left the
generated element type unable to express the write at all — is also the one
whose read surface conceals it. The two halves compound: a caller could neither
send the field nor observe that it was missing.

`feature_in` is a **per-resource capability, not a field the server ignores
everywhere**: both mobile resources store none, the four macOS/ebook ones store
and echo it. That is why the mobile profile's generated item type carries
`id`, `name` and `display_in` only, and `mobile_device_application`'s spec
independently agrees by declaring no `feature_in` at that position.

**Fixture trap.** `mobile_device_application` with `internal_app=true` is
creatable *without* `general.os_type`, and then **every** subsequent PUT —
including one touching nothing but `self_service` — answers `409 "Os type is
required for in-house app. Possible values: iOS, tvOS."` The first probe pass
read as "this resource refuses category writes"; it was the fixture. Setting
`os_type` once clears it, and a create carrying the categories in the POST body
works regardless.

**Incidental, no SDK action.** The mobile profile's POST/PUT root element is
`<configuration_profile>`, not `<mobile_device_configuration_profile>`; the
generated `MarshalXML` already emits it.

### v2082's scope migration: the specs moved, the gateway mostly did not (2026-09-04)

v2082 set a scope level on every spec that had one wrong
(three upstream spec changes). Six Platform specs went tenant →
**environment only** — `x-scope-types: [environment]`, `X-Environment-Id`
`required: true`, and `X-Tenant-Id` deleted from `components.parameters`
outright. `jpapi`, `capi` and all six Security Cloud specs went tenant →
**tenant and environment**, both headers `required: false`.

The gateway has not followed on the withdrawal half. Probed with a tenant
credential, `GET /pro/v1/jamf-pro-version` → 200 as the control in the same
invocation, and `GET /blueprints/v1/bogus-control-xyz` → `403 BAD_PERMISSIONS`
as the unrouted control:

| namespace | spec at v2082 | tenant credential | reading |
|---|---|---|---|
| `devices` | environment only | **200**, 13 devices | tenant still served |
| `device-groups` | environment only | **200**, 53 groups | tenant still served |
| `ddm/report` | environment only | **400** `filter` required (`Unable to parse RSQL filter`) | past the gateway |
| `device-actions` | environment only | **404** `NOT_FOUND` on `id` | past the gateway |
| `blueprints` | environment only | `403 BAD_PERMISSIONS` | unclassified — see below |
| `compliance-benchmarks` | environment only | `403 BAD_PERMISSIONS` | unclassified — see below |

The two 403s cannot be attributed. **Classifying a 403 takes two credentials,
not two paths**, and only one tenant credential was available; a 403 that
varies by credential is a capability grant, one constant across credentials is
a missing authorization rule, and neither can be distinguished from a scope
withdrawal on a single credential. Both agree with the GA decision that
blueprints and benchmarks are environment-only, so they are recorded as
consistent-but-unproven and deliberately left out of the acceptance
assertions. `compliance-benchmarks` also answered
`500 {"error": "Upstream host lookup failed"}` **2/2** to the *environment*
credential, which is an infra fault on that environment and not a scope
answer — repeated, per the rule that a single 500 reads as "routed and merely
faulting". **Confirmed environment-specific 2026-09-09**: a *different*
environment credential (`<jsc-tenant-b>`, EU gateway) answers **200** on
`GET /compliance-benchmarks/v1/benchmarks`, with `GET /pro/v1/jamf-pro-version`
→ `11.31.1` as the control in the same invocation, and the whole benchmark
lane — `TestAcceptance_Benchmark_CreateAndDelete`,
`…_Reporting`, `TestAcceptance_ResolveBenchmark*`,
`TestAcceptance_ResolveBaseline*` — passes against it. So the 500 was an infra
fault on the one environment, not a property of the API.

Two further readings from that credential, worth keeping because they are what
a *partial* environment grant looks like: `GET /device-groups/v1/groups`
answers `403 BAD_PERMISSIONS` while `devices`, `blueprints`,
`compliance-benchmarks`, `audit` and `securitycloud` all answer 200 on the
same token — a per-capability grant gap on the credential, not a namespace
being unrouted. And `GET /declaration-reporting/v1/declarations` answers
`404 page not found`, the unrouted-*path* tell rather than the namespace's own
`403`: the declared path is not that one. Read the path out of the spec before
concluding anything from a 404.

The dual-scope half is correct. Under `X-Environment-Id`:
`GET /pro/v3/computers-inventory` → 200 (5 computers),
`GET /proclassic/patchsoftwaretitles` → 200,
`GET /securitycloud/v1/categories` → 200 (36),
`GET /securitycloud/v2/groups` → 200,
`GET /securitycloud/uem-connect/v1/connectors` → 200 (1).

**`_permissions/routes.yaml` mistypes the tenant half of every dual-scope API
as `environment`.** 6357 → 12367 lines and 18 → 26 domain blocks, with **zero**
`tenant`-typed blocks where v2056 had 16. A dual-scope spec emits two blocks
whose `routes` are byte-identical and both typed `environment`: `pro` twice at
506 routes, `proclassic` twice at 273, `securitycloud` twelve times for six
specs. The file therefore no longer distinguishes the two scopes it was
extended to describe, and a consumer asking what a tenant-scoped integration
may be granted now finds nothing at all — the same question that was already
unanswerable at v1865 for the opposite reason (`routes.yaml` typed 11 domains
`tenant` while `scopes.yaml` had no `tenant` key). Report upstream.
`scopes.yaml` is byte-identical to v2056 and still has `environment` as its
only top-level key.

**`/v3/computers-inventory` is live and matches `/v4`.**
`GET /pro/v3/computers-inventory?page-size=1&section=GENERAL` and the same
call on `/v4` returned byte-identical bodies for the same tenant
(`totalCount: 6`, first record `id=6`). The V3 endpoints still send
`Deprecation: date="Tue, 14 Jul 2026 00:00:00 GMT"`, which the transport logs
on every call — expected, and the reason `#438` restored them: the deprecation
predates the removal manifest by weeks.

**A UEM connector exists on the JSC surface again**, which unblocks a probe
recorded as impossible at v1958. `GET /securitycloud/uem-connect/v1/connectors`
→ `totalCount: 1`, id `6a17f51d35de56a42a87c520`, `JAMF_PRO`,
`connected: true`, `refreshRateMinutes: 1440` (in v1958's new enum),
`deviceUnmanagedThreshold: 0`, `uemVersion: 11.31.1`. The v1958 constraint
claims — `refreshRateMinutes` restricted to
`60, 120, 240, 480, 720, 1440` and `deviceUnmanagedThreshold` to
`0, 1, 3, 5, 7, 14`, with `0` redefined from "grace period off" to "platform
default (3 days)" — are now probeable through the sync-settings PUT, which
resolves the resource before validating fields. Not done here: it is a write
against a live connected connector, and the connector has a concurrent human
operator.

### The gateway-bypass technique

When a gateway 403 blocks verification, separate "Jamf Pro can't do this" from
"the gateway won't forward it": provision an all-privileges API role + client
*through* the gateway, read the instance host from `jamf-pro-server-url`, then
authenticate directly at `POST https://<instance-host>/api/oauth/token` and call
the **tenant-less** Pro paths (`/api/v2/environment-type`, not
`/api/pro/v2/...`). This proved all three 11.31 endpoints worked in Jamf Pro
while the gateway refused them.

**This technique is now broken** and cannot be restored from the SDK: it depended
on `POST /v1/api-roles` + `api-integrations`, both withdrawn. Mint the credential
in Jamf Account instead.

### Resolved: the edge WAF blocked every multipart upload (2026-08-28)

Recorded because the diagnosis is reusable. `POST /pro/v1/packages/{id}/upload`
with a real signed installer answered **403 with a CloudFront HTML page** in
~30 ms. Root cause, confirmed by the API team: the IP had been added to a WAF
blocklist under **XSS attack detection**, and "including plist/XML content within
request body can trigger the detection".

How it was localised, in order, because the first two hypotheses were wrong:
not size (10 MiB of filler passed); not the SDK's request shape (replaying the
exact bytes through curl succeeded whenever the payload was benign); not the
protocol version (blocked on both); **it was the file's own bytes** — truncating
the installer put the boundary between 512 B and 2 KiB, exactly the span of the
xar zlib-compressed table of contents whose uncompressed form is XML carrying an
RSA `<signature>`. The decisive control was the *retired* gateway: byte-identical
content, same credential, same invocation, `apigw.jamf.com` reached Jamf and
`api.jamfcloud.com` blocked.

**Two failure modes were in play at once and are indistinguishable from the
response**: a payload rule blocks one request, and enough of them blocklist the
*IP*, with the same status, HTML and `x-amz-cf-id`. ~150 probe requests during
the investigation triggered the second, so **the investigation caused a second,
broader outage**. When re-probing an edge block: keep volume low, always
interleave a benign control in the same invocation, stop as soon as the answer is
in.

Verified fixed across widening scope — a real 9.8 MiB signed `.pkg` (OK in 3.4 s),
both branding uploads, Classic plain-XML writes, Classic XML carrying an embedded
mobileconfig, and all eight Classic profile-payload roundtrips (quote, ampersand,
line-break and reserved-character matrices). That last row mattered most: Classic
is XML end-to-end across 617 operations, so a live rule would have taken out most
of the SDK's write surface. **No SDK change was needed and none should be added** —
do not reshape a multipart body or escape plist content to placate a WAF.

---

## Pagination

`ListAllPages` computes each page's offset by multiplying the *requested* page
size, so if the true cap is lower than requested, page N+1 skips the untransferred
tail of page N **with no error at any layer**. That is why the default is scoped
as narrowly as the evidence.

- **Every `pro`-package, `totalCount`-style list operation clamps `page-size` at
  exactly 2000, silently, with zero exceptions found.** Verified by seeding real
  data past 2000 rows and comparing a `page-size=5000` request's returned count
  against the seeded total, across **33 operations** of wildly different resource
  shapes: `ListGroupsV1`/`V2`, `ListAccountsV1`, `ListEnrollmentAccessGroupsV3`,
  and 29 History operations. No skip or overlap at any 2000/2001 boundary. Hence
  `defaultMaxPageSize` bakes 2000 in for the whole pro+totalCount combination
  (110 of 112 pro list operations).
  - Re-confirmed 2026-08-31 on `/inventory-preload/history` (both the `/v1/` and
    unversioned forms) against a tenant holding 2052 rows: `page-size=5000`
    returned exactly 2000, and pages 0 and 1 at `page-size=2000` had **zero
    overlap and a union of exactly 2052**. `TestAcceptance_Pro_InventoryPreload_HistoryUnversioned`
    keeps a duplicate check on the walk while the tenant stays over 2000 rows.
  - **The page-size parameter name is not uniform.** Unversioned
    `GET /inventory-preload` reads `pagesize` and ignores `page-size`; every other
    pro list reads `page-size`. Configured via `pageSizeParam`, and load-bearing:
    a list whose page size is silently dropped returns the server's own default
    while `ListAllPages` multiplies offsets by 2000.
- **Deliberately excluded:** `ListUsersV1` (`hasNext`) and `ListSiteObjectsV1`
  (`rawArray`) were not in the verified sample and fail differently under a wrong
  cap — `hasNext` can skip a chunk between pages, `sizeCheck` can stop early and
  truncate the whole remainder. Both stay at 100.
- **`devices/v1` hard-rejects rather than clamping**: `page-size>1000` returns a
  structured 400 (`"Page size must not exceed 1000"`), so `ListDevices`,
  `ListDeviceApplications` and `ListDevicesForUser` set `maxPageSize: 1000`
  explicitly. Same SDK, same generator, genuinely different backend — which is
  exactly why the pro default is not extended to other packages.
- **No other package's cap has been probed.** Classic, DeviceGroups, Blueprints,
  DDMReport and ComplianceBenchmarks stay at 100.

**Security Cloud: only two of four `totalCount` ops actually paginate.**

| op | honours `page`/`page-size`? | wire cap |
|---|---|---|
| `ListZtnaAppsV1` | yes | **1000** (undocumented; `page-size=99999` → 400) |
| `ListUemConnectorSyncRunsV1` | yes | **100** (spec-documented, wire-confirmed) |
| `ListZtnaGatewaysV1` | **no** — params ignored entirely | n/a |
| `ListUemConnectorsV1` | **no** | n/a |

The last two declared a `{totalCount, results}` envelope but always return the
complete list in one shot (`page=99&page-size=1` still returns everything). With
`pagination: totalCount` configured, `hasNext = (page+1)*pageSize < totalCount`
would have looped and **concatenated the same full list repeatedly** on any tenant
with more than 100 — a duplication bug, the mirror image of the truncation risk.
Fixed by dropping the `pagination` key from both.

**AI Governance:** `page`/`page-size` honoured on `/policies` and
`/policies/{id}/versions` (max 500, enforced with a 400); **`GET /v1/tools`
ignores them** and returns the whole catalogue.

**Genuinely untestable, not merely unverified** — listed so nobody re-litigates
the same wall: `compliance-benchmarks/*` (backend 500s tenant-wide),
`blueprint-components` and `enrollment/languages` (fixed catalogs of a few dozen
rows), `pki/adcs-settings/{id}/history` (create needs real X.509 material),
`patch-software-title-configurations/{id}/history` (needs a `softwareTitleId`
from Jamf's external catalog), `cloud-idp/{id}/history` (**no create endpoint
exists at all**), `mdm/commands` (needs a device with a live push channel).

---

## Required query parameters (2026-09-01)

The generator emitted every query param behind a zero-value guard, spec-required
ones included, so a caller passing the zero value sent a request with the
parameter missing. Twelve call sites across five packages were affected; the
emission rule and the one carve-out are in
[STYLE.md](STYLE.md#query-parameter-emission-a-required-param-carries-no-guard).
Probed on the EU gateway, tenant-scoped for Security Cloud and
environment-scoped for the rest, each with a 200 control in the same
invocation. **Absent and empty are different requests on every endpoint here,
and the empty one is the better error in every case but one.**

| endpoint | param omitted | param sent empty | valid value |
|---|---|---|---|
| `GET /securitycloud/v1/activation-profiles` | `400 BAD_REQUEST` "Required parameter 'origin' is not present." — the service's own envelope, no `errors[]` | `400 INVALID_FIELD` "Unknown origin value." — the declared `ApiError` shape | `origin=PUBLIC_API` → 200 |
| `GET /ddm/report/v1/devices/{id}/declarations` | `400`, `field: "filter"`, "Required request parameter 'filter' for method parameter type String is not present", `code: null` | `400`, `field: null`, "Unable to parse RSQL filter" | `filter=active==true` → 200, 28 rows |
| `GET /pro/v1/jamf-package` | `400` with an **empty** `errors` array — nothing at all to act on | `400 INVALID_FORMAT` "Please ensure your application name is alphanumeric" | `application=protect` → 200 |
| `GET /pro/v1/managed-software-updates/plans/group/{id}` | **200** | **200** | `group-type=COMPUTER_GROUP` → 200 |
| `GET /pro/v3/patch-software-title-configurations/{id}/export-report` | `400` (report has 0 rows) | **500** | two-column list → `400`, same as omitted, for the 0-row report |

Three findings in that table.

**`group-type` is `required: true` and the server does not enforce it.** All four
of `COMPUTER_GROUP`, `MOBILE_DEVICE_GROUP`, empty, and omitted answer 200 with
the same body on group id 1 (Jamf Pro 11.31.1); only `group-type=BOGUS` is
refused, `400 INVALID_REQUEST_PARAMETER_TYPE` quoting the Java enum
`com.jamfsoftware.managedsoftwareupdates.web.dto…`. So required-ness there is
documentation, not validation. `TestAcceptance_Pro_MdmUpdates_GetGroupPlansV1`
pins all three behaviours and fails if the server starts enforcing it — that
method had no acceptance test at all before this change.

**`columns-to-export` is the one case where sending the parameter is worse than
omitting it, and it is the reason for the carve-out.** It is `required: true`
*with* a nine-column default, which OpenAPI forbids. Against a config whose
patch report has zero rows, omitting it and passing a valid two-column list both
answer `400` with an empty `errors` array — the "empty report is a 400 here"
behaviour `assertPatchExportReport` already documents — while
`columns-to-export=` answers **500**. Substantive default ⇒ absence is defined
⇒ keep the guard.

**`GET /pro/v1/jamf-package` rejects an underscore.** `application=JAMF_PROTECT`
is `400 INVALID_FORMAT` on the same "must be alphanumeric" rule as the empty
value; `protect` and `PROTECT` both answer 200. The spec constrains the param to
no enum, so the vocabulary is wire-only.

---

## Jamf Security Cloud (`securitycloud`)

### URL shape

The gateway is a catch-all proxy; the path globs in the api-product definition
are **audit** rules covering mutating methods only, and must not be read as a
routing allowlist.

| form | result |
|---|---|
| `/api/securitycloud/tenant/{t}/v1/dns/zones` | 200 — what the SDK builds, and the only form the audit globs match |
| `/api/securitycloud/v1/tenant/{t}/dns/zones` | 200 — also routed; what the SDK sent before 2026-08-21 |
| `/api/securitycloud/tenant/{t}/dns/zones` | **403** — the versionless form the published spec declares |
| `/api/securitycloud/tenant/{t}/uem-connect/v1/connectors` | 200 — UEM Connect carries its own in-path version |

**Routing is indifferent to the order; auditing is not.** The matcher is
`audit-plugin`: `CompileGlob` renders `**` as `.+` (one-or-more, never matching
empty) and matching happens on the raw inbound path with `/api/securitycloud`
removed, before rewriting. Every glob is `/vN/<svc>/…` or `/**/vN/<svc>/…` —
version *after* the wildcard — so a stripped path only matches when the tenant
segment precedes the version. Sending version-first meant **19 of 27 mutating
Security Cloud operations executed but were never audited**. Fixed in the SDK via
`tenantFirstNamespaces`, not upstream: one allowlist entry beats five region
files of new globs. Recompute coverage after any change to either side — the
answer is not guessable.

The tenant segment must be the tenant **UUID**: a tenant name returns
`400 REQUEST_CONTEXT_NOT_PROVIDED` and another organisation's tenant ID returns
`403 OWNERSHIP_FORBIDDEN`, so a failing tenant segment is distinguishable from
both an unrouted path and a privilege failure.

**Not routed** (403 with credentials that reach every other Security Cloud path):
the `jsc-api-gateway` ping, `securitycloud-devices`' `GET /v1/…/devices`, and
**every `/v2/groups/{id}` method** — see below.

### Device groups `PUT /v2/groups/{id}` is FIXED, and the hold is lifted (2026-09-04)

**The handler defect recorded in the section below was fixed the next day, and
the fix window is narrow enough to state exactly: between 12:51 and 13:33 BST on
2026-09-04.** A run of `TestAcceptance_SecurityCloudUpdateDeviceGroupV2` at
12:51 still got the `404`; a run at 13:33 succeeded. Nothing in the SDK changed
between them.

**It is a real fix, not a status change.** The failure mode a status check
cannot see is a handler that accepts a write and discards it, so the rename was
**read back**. Three cycles, raw curl so the payloads are not mediated by the
SDK, with an environment credential on `eu`:

```
control  bogus path                    -> HTTP 403        (unrouted tell, this namespace)
POST /securitycloud/v1/groups          -> {"id":"b0693661-…","name":"sdk-v2put-verify-1-31843"}
PUT  /securitycloud/v1/groups/{id}     -> HTTP 200        {"id":"b0693661-…","name":"…-v1ok"}
PUT  /securitycloud/v2/groups/{id}     -> HTTP 204        (empty body, as declared)
GET  /securitycloud/v2/groups          -> name is "sdk-v2put-verify-1-31843-v2applied"   ← PERSISTED
DELETE /securitycloud/v1/groups/{id}   -> HTTP 204
```

3/3 identical. The `403` control in the same invocation is what rules out the
`204` being a router artefact, and the read-back through `GET /v2/groups` — a
different operation from the one written — is what rules out a write the handler
accepted and dropped.

**Two layers were broken here, and fixing the first made the second visible.**
The 403 → 404 transition on 2026-09-03 was the v2 `PUT` policy change
deploying, and the diagnosis at the time — recorded below — concluded "the
remaining gap is the rollout". The rollout did land. It then exposed an
independent defect in the v2 handler behind it, which is why the hold outlived
the authorization fix by a day. **A 403 clearing is not the same event as the
capability arriving**; that is the part worth carrying forward.

**What it cost to lift the hold.** `securitycloud-device-groups-api.yaml` is
ingested at v2082, taking v1942's two withdrawals with it: `GET /v1/groups` and
`PUT /v1/groups/{groupId}`. Removed from Go — all breaking:
`ListDeviceGroupsV1`, `UpdateDeviceGroupV1`, `ResolveDeviceGroupV1IDByName`,
`ResolveDeviceGroupV1ByName`, `ApplyDeviceGroupV1`, and the
`GroupListResponse = []GroupListItem` alias the withdrawn list was the only user
of. `ApplyDeviceGroupV2`'s update branch is repointed from
`UpdateDeviceGroupV1` to `UpdateDeviceGroupV2` — the branch that could not have
worked at all before this fix. `POST /v1/groups` and `GET`/`DELETE
/v1/groups/{groupId}` survive the withdrawal, so the package still creates and
deletes through v1.

**Both withdrawn paths still answer 200 on the wire**, probed in the same
session with the same 403 bogus-path control:

```
GET /securitycloud/v1/groups        -> HTTP 200  [{"name":"Default Group"}]
PUT /securitycloud/v1/groups/{id}   -> HTTP 200  {"id":"ad1cee93-…","name":"sdk-withdrawal-probe-v1"}
```

So the SDK is deliberately stricter than the gateway here, which is the v1942
rule applied rather than an accident. Worth noting `GET /v1/groups` returns a
bare array whose entries carry only `name` — no `id` — which is why the
withdrawn list's item type was never `Group`.

**The whole spec delta is those two withdrawals plus v2082's scope-parameter
migration.** Stripping the scope parameters leaves **zero** operations
differing across the five survivors, and zero schemas added, removed or
changed. `x-scope-types` goes `[tenant]` → `[tenant, environment]`, which
self-expired the `scopeTypes` config override: generation failed with *"delete
the config entry so the spec is the only source"* and the entry went. The
account trio is now the only user of that key.

### Device groups `/v2/{id}`: the rule deployed, and the v2 handler 404s (2026-09-04)

**The authorization story is over and the hold now rests on a service defect.**
The v2 `PUT` policy change has deployed: `PUT /v2/groups/{groupId}`
stopped answering `403 BAD_PERMISSIONS` and started answering a service-level
**`404 NOT_FOUND`**. First seen 2026-09-03 12:29Z on the JSC sandbox tenant;
**re-probed 2026-09-04 on a second Security Cloud tenant with a second
credential, against a group created seconds earlier, and it is the same.** That
run is the strongest form of the evidence so far, because it removes every
remaining explanation except the handler.

Environment credential on the UAT environment
(`<jsc-tenant-b>`, eu) — a **different** Security Cloud
tenant from the `<jsc-sandbox>` JSC sandbox every earlier probe used. One
invocation, throwaway group created and deleted inside it:

| request | result |
|---|---|
| `POST /securitycloud/v1/groups` | **201** `{"href":"/api/securitycloud/v1/groups/<probe-group-a>","id":"<probe-group-a>","name":"sdk-probe-1788514469"}` |
| `PUT /securitycloud/v2/groups/<probe-group-a>` | **404** `NOT_FOUND` / `"Not Found"`, `field: null` — **3/3**, traceIds `<trace-1>`, `<trace-2>`, `<trace-3>` |
| `GET /securitycloud/v1/groups/<probe-group-a>` (control) | **200** `{"id":"<probe-group-a>","name":"sdk-probe-1788514469"}` |
| `PUT /securitycloud/v1/groups/<probe-group-a>` (sibling-rule control) | **200**, renamed, and the rename read back |
| `GET /securitycloud/v2/groups` | **200** — lists `{"id":"<probe-group-a>","name":"sdk-probe-renamed-v1"}` |
| `GET /securitycloud/v2/groups/{id}` | **403** `BAD_PERMISSIONS` — undeclared verb, proves nothing |
| `PUT /securitycloud/v2/bogus-control/{id}` (unrouted control) | **403** `BAD_PERMISSIONS` |
| `GET /securitycloud/v9/groups` (unrouted control) | **403** `BAD_PERMISSIONS` |
| `DELETE /securitycloud/v1/groups/<probe-group-a>` (cleanup) | **204**, and `GET /v1/groups` back to `[{"name":"Default Group"}]` |

**What each control rules out.** The two bogus-path 403s establish that
`403 BAD_PERMISSIONS` is still this namespace's unrouted signature, so a 404 on
the v2 PUT means the request cleared OPA and reached the service. The v1 `PUT`
at 200 is the same-permission sibling rule — identical `tenantPermissions`
condition in the same rego file — so the credential satisfies the v2 rule by
construction. `GET /v2/groups` returning the group **by that exact id in the
same invocation** rules out the id being wrong or unknown to v2's own read path.
And the group was **created seconds earlier in the same invocation**, which
rules out stale state, replication lag, and anything about the group predating a
migration — the explanations a probe against a long-lived sandbox group could
not exclude.

**So: the v2 update handler cannot find a group its own list endpoint just
returned.** That is a defect in the Security Cloud devices service, not an
authorization gap, and the owner is no longer a policy-bundle rollout. Report upstream. ~~`securitycloud-devices` **stays held at v1897**~~ —
**superseded 2026-09-04: the handler was fixed and the hold lifted; see the
section above.** At the time of this probe, v1942 would have withdrawn
`PUT /v1/groups/{groupId}` — then the only device-group update that worked —
while its declared successor was broken. **Lift the hold when the v2
PUT answers 2xx**, not when it stops 403ing: that already happened and cost the
SDK nothing.

Two details worth keeping. `GET /v2/groups/{id}` and `DELETE /v2/groups/{id}`
are still 403 and still prove nothing — `#265` granted `PUT` alone and the spec
declares neither verb. And the create's `href` is `/api/securitycloud/v1/…`,
carrying an `/api` prefix the GA gateway root does not take, so it is not
callable as returned — the same shape as the App Installers `href` finding. Use
`id`. (`href` was populated here because curl sent no `Accept-Encoding`; through
the SDK it is nulled by response compression, recorded separately.)

### Device groups `/v2/{id}`: the rule landed on `main` 2026-09-02

**Update 2026-09-02: the rule now exists.** A policy change merged at 10:13:41Z
adding `PUT /api/securitycloud/v2/groups/{groupId}` is the tip of the policy's
`main`. It adds exactly one allow block to the Security Cloud devices policy,
taking it from six rules to seven: a `PUT` on that path, for an external M2M
token, requiring either `update:jsc:all` or `device-groups:update` on the
credential's tenant permissions.

Three things to hold onto. **It is `PUT` only** — `GET` and `DELETE` on the v2
item path still have no rule of any method, which is consistent with the spec
declaring neither, so their 403s continue to prove nothing. **The permission is
read from `tenantPermissions`**, matching the v1 rules, and is satisfied by
either `update:jsc:all` (legacy) or `device-groups:update` (capability-first) —
so a credential holding neither will still 403 after rollout, and that 403 will
be a capability gap rather than a missing rule. **A merged rule is not a deployed
one, and the gateway config cannot tell you when it is.** Nothing in the
gateway's API definitions needs to change — the prod Security Cloud product
already listens on `/api/securitycloud/` in all three regions, which routes the
v2 item path into this package — and the middleware-bundle hash recorded there is
a gateway-plugin version, **not** an OPA policy version. The policy bundle is
published by its own CI and rolled out by a separate service. So the wire is the
only oracle for whether it is live.

**Probed, and it is not live yet: still 403, 3/3, 2026-09-02 ~11:00Z**, JSC
sandbox tenant `<jsc-sandbox>` on eu, `PUT /securitycloud/v1/groups/{id}` → **200**
in the same invocation both times and the group unchanged afterwards
(`{"name":"API Demo Group"}` in, same out). v2 traceIds
`80005a5ea6c4ddeb68bad3892adf2d4c`, `0492550017943d37b9bb05664e16b5f6`,
`85d146551ee0628b6156997724610308`.

**This classifies the 403 from one credential, and the method is better than the
two-credential test.** `PUT /v1/groups/{groupId}` and
`PUT /v2/groups/{groupId}` declare the **identical** condition in the same rego
file — `is_external_m2m_token` plus `tenantPermissions` holding
`update:jsc:all` or `device-groups:update`. A credential that satisfies one
satisfies the other by construction, and this one demonstrably satisfies the v1
rule. So the v2 403 cannot be a capability gap: it is the deployed bundle
predating the v2 rule. **Prefer a same-permission sibling rule over two
credentials whenever the rego offers one** — it needs one token, one
invocation, and it cannot be confounded by a grant difference.

Two grants fall out of the same evidence, worth knowing before designing another
probe. The JSC sandbox credential holds the **capability-first** permissions
(`device-groups:read`, `device-groups:update`) and **not** the legacy
`read:jsc:all` / `update:jsc:all` wildcards: `GET /securitycloud/v1/groups` and
`GET /v2/groups` answer 200, and `GET /securitycloud/v1/activation-profiles`
reaches the service (`400 BAD_REQUEST`, "Required parameter 'origin' is not
present" — past OPA), while `GET /securitycloud/uem-connect/v1/connectors` is
**403 `BAD_PERMISSIONS`**. `read:jsc:all` would have allowed that connector list
outright, so its absence is proven, and `uem-connect:read` is absent too. The
2026-09-01 uem-connect probes recorded above must therefore have used a
different credential; **a `uem-connect:*` grant is a prerequisite for probing
`422 VENDOR_MISMATCH`.** Also confirmed in passing: `/api/securitycloud/…`
answers `404 page not found`, so the GA gateway takes no `/api` segment.

**Re-probed 2026-09-02 13:55Z and nothing has moved: still 403.** Same JSC
sandbox tenant `<jsc-sandbox>` on eu, same idempotent body read back from
`GET /v1/groups/{id}` and written unchanged, all in one invocation with
`GET /securitycloud/v2/groups?page-size=2` → **200** as the live-token control:

| request | result |
|---|---|
| `GET /securitycloud/v2/groups?page-size=2` (control) | **200**, `{"groups":[…]}` |
| `PUT /securitycloud/v2/groups/{real-id}` | **403** `BAD_PERMISSIONS`, `70136769181776170d0e85619bf1bd56` |
| `PUT /securitycloud/v2/groups/{bogus-uuid}` | **403** `BAD_PERMISSIONS`, `8bf721480e8b3ff98d1e371abb813653` |
| `PUT /securitycloud/v1/groups/{real-id}` (sibling-rule control) | **200**, `{"id":"d56a8e26…","name":"API Demo Group"}` |

That is **3h42m after the v2 rule merged** and the deployed bundle still predates
it, which sets a floor on the rollout lag rather than a defect: nothing in
either repo publishes a signal this one can read, so the wire stays the only
oracle. Note the bogus-uuid 403 is identical to the real-id 403 — OPA refuses
before the service resolves the id, which is why a real group is not needed to
detect the rollout and the acceptance test can keep asserting on either.

**The deprecated-endpoint deny draft moved, and the move removes the ordering
risk.** It is still a draft, but it was **rebased onto the current policy tip at
2026-09-02 13:22:43Z**, so the v2 `PUT` rule is now an ancestor of its head and
its own copy of the Security Cloud devices policy **retains the v2 `PUT` allow**
while deleting the v1 `GET`/`PUT` ones. So the draft can no longer deny the v1
update
without carrying the v2 successor in the same bundle — the sequence CLAUDE.md
warned about (v1 denied with no v2 successor) is now structurally excluded on
the merge path, not merely unlikely. Verify it stays that way on any later
force-push: an `is-ancestor` check of the v2 rule's commit against the draft's
head, plus a grep for
`"v2", "groups", groupId` in that file is the whole check.

This also clears the blocker #264 named in its own body: it removes the allow
blocks for `GET /v1/groups` and `PUT /v1/groups/{groupId}` among 155 rules across
21 files, and explicitly said it needed the v2 `PUT` rule to land first. It has.

Everything below predates that and is the record of the two-month absence.

The Security Cloud devices policy carries exactly six rules — `POST`/`GET /v1/groups`,
`GET /v2/groups`, and `GET`/`PUT`/`DELETE /v1/groups/{groupId}`. There is **no
`/v2/groups/{groupId}` rule of any method**, and grepping all 60+ remote branches
and all open PRs finds none pending. So this is not awaiting a rollout; it has not
been written. Watch that repo, not the specs and not tyk.

Wire-confirmed 2026-08-29 with an environment-scoped EU credential, v1 control in
the same invocation:

| request | result |
|---|---|
| `PUT /securitycloud/v2/groups/{real-id}` | **403**, repeated |
| `PUT /securitycloud/v2/groups/{bogus-uuid}` | **403**, 3/3 |
| `PUT /securitycloud/v1/groups/{real-id}` (control) | **200** with the `Group` body |
| `PUT /securitycloud/v1/groups/{bogus-uuid}` | `404 GROUP_NOT_FOUND`, `field: groupId` |

**`PUT` is the only verb that matters here, because it is the only item-level v2
verb the spec declares.** The device-groups spec splits the resource across
versions: `POST /v1/groups`, `GET /v1/groups/{groupId}` and
`DELETE /v1/groups/{groupId}` stay on v1, `GET /v2/groups` and
`PUT /v2/groups/{groupId}` are v2. `GET` and `DELETE` on `/v2/groups/{groupId}`
are **not declared in any build**, so their 403s are expected and prove nothing
about routing — earlier revisions of this section cited them as if they widened
the finding. They do not. The missing OPA rule is exactly one,
`PUT /v2/groups/{groupId}`, which is precisely the gap in
the Security Cloud devices policy's six.

`TestAcceptance_SecurityCloudUpdateDeviceGroupV2` asserts the 403 and **fails when
routing lands** — invert it then, and do not weaken it to a skip.

**Re-probed twice on 2026-09-01 with the JSC sandbox tenant `<jsc-sandbox>` on eu —
09:36 and 13:30, the second after v1981 published the Security Cloud specs to
`external/`. Nothing has moved either time, and the v1 write still works.** At
13:30 `PUT /securitycloud/v2/groups/{real-id}` → **403** twice (`<trace-4>`,
`<trace-5>`) with `PUT /v1/groups/{id}` → 200 and `DELETE /v1/groups/{id}` → 204
in the same invocation. Promotion of the specs to prod did not come with the OPA
rule. (`GET` and `DELETE` on the v2 item path were probed too and also 403, but
the spec declares neither, so those results carry no weight — see the note
above.)

**A third run classified the 403 from the wire alone, using two credentials
rather than two paths.** On the same real group, in one invocation with both
tokens proven live (`GET /securitycloud/v2/groups` → 200 on the JSC credential,
`GET /pro/v1/jamf-pro-version` → 200 on a `pro` one):

| request | JSC credential | `pro` credential |
|---|---|---|
| `PUT /securitycloud/v2/groups/{id}` (the declared successor) | 403 `ab3e25bf65c2fed35faa25588a7afe81` | 403 `5526799f5087c641aa8ea97881aab254` |
| `PUT /securitycloud/v1/groups/{id}` | **200**, rename applied | 403 `96faad136dcae9351590ce3961f369ad` |
| `GET /securitycloud/v2/groups/{id}` — undeclared | 403 `55fef538a4b2f0c81665dea059c9d7b0` | 403 `bbc5a3146958a8f3636b9ec865ee8d50` |
| `DELETE /securitycloud/v2/groups/{id}` — undeclared | 403 `584fd8ec537b1311a47fc502fd0a13b4` | 403 `78520b00290262f616d23d8e3fb268de` |

The v1 `PUT` **varies** by credential; the v2 `PUT` is **constant** across both. A
403 that varies is a capability grant, one constant across credentials is a
missing authorization rule — so `PUT /v2/groups/{groupId}` has no rule authored,
established from the wire without reading the policy at all. The
two sources now agree independently. The last two rows are the undeclared verbs,
included only to show they behave no differently; they are not part of the
argument.

The 09:36 sequence in full:

| request | result |
|---|---|
| `GET /securitycloud/v2/groups` (list, control) | **200**, the `{groups: []}` envelope |
| `POST /securitycloud/v1/groups` | **201** `{href, id, name}` |
| `GET /securitycloud/v1/groups/{real-id}` (control) | **200** |
| `PUT /securitycloud/v2/groups/{real-id}` | **403** `BAD_PERMISSIONS`, 3/3 |
| `PUT /securitycloud/v2/groups/{bogus-uuid}` | **403** `BAD_PERMISSIONS` |
| `GET /securitycloud/v2/groups/{real-id}` | **403** `BAD_PERMISSIONS` |
| `PUT /securitycloud/v1/groups/{real-id}` | **200**, body carries the new name |
| `GET /securitycloud/v1/groups/{real-id}` (read-back) | **200**, rename persisted |
| `DELETE /securitycloud/v1/groups/{real-id}` | **204**, then GET → 404 |

Two things this pins down. The 403 is **not** id-shaped or permission-shaped — a
bogus uuid gets the same answer as a real one, and the same credential drives the
v1 write to 200 in the same invocation, so it is the unrouted-path tell, not a
capability gap. And **no `/v2/groups/{groupId}` verb is routed**: the `GET` fails
the same way the `PUT` does, matching the policy having no
rule for that path at all.

**The v1 PUT's own `Link` header names a third URL shape, and it is unrouted
too.** The 200 response carries `deprecation: @1787616000` (2026-08-25) and
`link: </v2/customers/{tenantId}/groups/{groupId}>; rel="successor-version"` —
neither the path the spec's `x-successor-endpoint` names
(`PUT /v2/groups/{groupId}`) nor a published path at all. Probed with the tenant
header: **403 `BAD_PERMISSIONS`**. So the runtime header and the spec disagree
about the successor and both candidates are refused, while the deprecated call
works.

**Scope isolation: the `/customers/{tenantId}/` segment is inert, and the two
failure modes are distinguishable.** Repeating the v2 probes with `X-Tenant-Id`
*removed* returns **`400 REQUEST_CONTEXT_NOT_PROVIDED`** — for the path-scoped
form as well as the bare one — so the gateway does not read the tenant from the
path and the segment contributes nothing. That rules out "path scoping was
retired" as an explanation for the successor path's 403. Keep the pair straight
when diagnosing:

| tell | meaning |
|---|---|
| `400 REQUEST_CONTEXT_NOT_PROVIDED` | no scope header on the request |
| `403 BAD_PERMISSIONS`, constant across credentials and ids | path not routed |

One more for the same team: `POST /securitycloud/v1/groups` returns
`href: "/api/securitycloud/v1/groups/{id}"` — an `/api` prefix the GA gateway
does not serve, so the href is not directly followable.

**~~v1942 would have removed the only working device-group update and kept the
broken one, so `securitycloud-devices` is held at v1897.~~ — the v2 update was
fixed on 2026-09-04 and the hold lifted; the withdrawal is now taken.** The
build withdrew
`PUT /v1/groups/{groupId}` — the call that answers 200 above — which would have left
the unrouted v2 PUT as the package's sole update method and repointed
`ApplyDeviceGroupV2` onto it, killing its update branch. The removal was taken and
then reverted on that evidence: `ListDeviceGroupsV1`, `UpdateDeviceGroupV1`, their
resolver and both Applies all remain, and this probe's v1 *write* control remains
available. Take the removal once the v2 rule is authored, not before.

**Fourth re-probe, 2026-09-01 17:1x on the JSC sandbox `<jsc-sandbox>`/eu. Nothing
has moved, and the picture upstream got worse.** With
`GET /securitycloud/v2/groups` → 200 as the control in the same invocation, on a
group minted for the probe (`POST /securitycloud/v1/groups` → 201
`<probe-group-b>`):

| request | result |
|---|---|
| `PUT /securitycloud/v2/groups/{real-id}` | **403** `BAD_PERMISSIONS`, twice (`<trace-6>`, `<trace-7>`) |
| `PUT /securitycloud/v2/groups/{bogus-uuid}` | **403** `BAD_PERMISSIONS` (`<trace-8>`) — same answer as a real id |
| `PUT /securitycloud/v1/groups/{real-id}` (control) | **200**, `{id, name}` with the new name |
| `GET /securitycloud/v1/groups/{real-id}` (read-back) | **200**, rename persisted |
| `DELETE /securitycloud/v1/groups/{real-id}` | **204**, then `GET` → `404 GROUP_NOT_FOUND` `field: groupId` |

Tenant left clean — the group list is the same 8 entries before and after.
The Security Cloud devices policy on its `main` still carries exactly the same
six rules, and that `main` is unmoved, so nothing has been authored.

**Two open policy changes now look like they bear on this, and only one of them
does.**

- **A change adding policies for `DELETE device-enrollments/{id}` and
  `GET v2/groups/{id}` (open, 2026-09-01) is not Security Cloud.** Its
  `v2/groups/{groupId}` rule is for `/ui/jamfpro/v2/groups/{groupId}`, in the
  Jamf Pro **UI** namespace's policy. It touches four files, none of them the
  Security Cloud policy. A title search for `v2/groups` finds it and means
  nothing here; check the file path, not the title.
- **A change denying manifest-listed deprecated endpoints at the external
  gateway (draft, 2026-09-01) would remove the working update outright.** It
  deletes the `GET /api/securitycloud/v1/groups` and
  `PUT /api/securitycloud/v1/groups/{groupId}` allow blocks from the Security
  Cloud devices policy, leaving four rules — `POST /v1/groups`,
  `GET /v2/groups`, `GET /v1/groups/{groupId}`, `DELETE /v1/groups/{groupId}` —
  and **adds no `PUT /v2/groups/{groupId}` rule**. So if it merges and deploys as
  drafted, device groups become create/read/delete-only: the v1 update is denied
  and the declared v2 successor is still unauthored. **This is the enforcement
  half of v1942's spec filter arriving without the successor rule that filter
  presupposed** — report it before it merges. It also means the
  `securitycloud-devices` hold stops protecting the capability at that point: the
  SDK would keep a method the gateway refuses, which is the honest state but not a
  working update.

That draft is also the deny half of v1942's `capi` withdrawals — it strips
`GET /api/proclassic/computers`, `/computers/subset/basic`, and `GET`/`PUT`
`/computers/id/{computerid}` from the Classic computers policy. It does **not**
touch the twelve alternate-identifier rules v1988 deleted from the spec; all twelve
remain in the branch's version of that file. It was opened at 14:09 and the v1988
bundle generated at 14:15, so the policy side has not caught up with the newer
removal yet. Re-check both when it moves out of draft.

Two methodological warnings from this probe. The first attempt returned **500 on
both v1 and v2**, which reads as "v2 is routed and merely faulting" — the opposite
of the truth; repeating each request 3–4 times showed the steady state. And a
**pro** credential answers 403 on `/securitycloud/v1/groups` *and* `/v2/groups`
alike, so a routing probe on the wrong credential set concludes "still unrouted"
no matter what the gateway is doing.

### Report upstream: the DNS zone operations carry `ztna:*`

The v1559 split gave `search-domains` and `custom-hostname-mappings` — in the same
spec — their own privilege families, so `/v1/dns/zones` carrying `ztna:read`,
`ztna:create`, `ztna:update` and `ztna:delete` reads like a copy-paste rather than
a decision. It may be deliberate (DNS zones being part of the ZTNA capability) but
nothing in the spec says so, and a customer granting `ztna:*` to manage DNS zones
is surprising either way. **The SDK reports what the spec says; do not "correct" it
locally.**

Note these are not gateway scopes: neither the old `*:jsc:all` names nor the new
ones appear anywhere in the gateway's API definitions, which carry only a
product-level `securitycloud-product` scope. Per-privilege checks live in the
authorization service and the Jamf Account permissions model, so the gateway
config cannot answer "is this live" for a privilege rename. The wire can.

### `href` is nulled by response compression

`ZoneRef.href` and `CreateResponse.href` are `required` and *are* sent — but only
when the response is uncompressed. Ask for `Accept-Encoding: gzip` and the same
request returns `"href": null` **and drops the `Location` header**; identity, `br`
and no-header all return both. Deterministic 12/12 each way.

Root cause: the backend never sends `href` at all; the gateway's
`href-injection` plugin synthesises it, and bails out early when the response
carries a `Content-Encoding` other than `identity`, logging *"unsupported
Content-Encoding %q on POST 201; leaving response unchanged"*. Go's `net/http`
adds `Accept-Encoding: gzip` to every request, so **no Go caller can ever see
`href`** and only `id` is usable. Report it as "the injection plugin cannot
rewrite a compressed body", not as "the gateway nulls href".

`assertCreateHrefEmpty` pins the emptiness so the suite **fails when this is
fixed**, rather than silently continuing to assume it.

This is also the cautionary tale for treating SDK-mediated observation as wire
truth: the long-standing note that "the server sends only `id`" was only ever
observed through this SDK. AI Governance is **not** affected — it emits `href`
itself, verified with both `--compressed` and `identity`.

### ZTNA

- **`vendor` is enum-validated and case-sensitive**, and an invalid value returns
  `400 INVALID_FIELD` with body `"Request body is missing or malformed."` and
  **no `field`** — enum coercion fails during deserialisation, before per-field
  validation. An invalid `keyExchange` behaves identically. This is the strongest
  case in the SDK for generated constants: the failure is unattributed. Unknown
  *properties*, by contrast, are silently ignored at every level.
- `ConnectionConfigRightRequest`'s own description contradicts its enum, naming
  lowercase `cisco`, `strongSwan`, `juniper`; the enum says `Cisco`/`Juniper` and
  lowercase gets the unattributed 400 above. **Follow the constants, not the prose.**
- **`left` and `right` differ in both range and cardinality.** `ipsec.left.subnets`
  must be a private range (`10/8` /8–/30, `172.16/12` /12–/30, `192.168/16`
  /16–/30) — the spec's own `0.0.0.0/0` examples are rejected — and takes
  **exactly one** subnet; `right.subnets` accepts public ranges and **many**.
  Nothing explains why. Note the deep IPSec checks do not run while a required
  top-level field is missing, so a left-cardinality probe needs an otherwise-valid
  request.
- `dedicatedIps.enabled: true` is mutually exclusive with `ipsec`, and a gateway
  must be one or the other. `dedicatedIps: {enabled: true}` with no `ipsec` is the
  cheapest throwaway gateway — no subnets, vendor or PSK needed.
- `auth` and `left.vendor` are accepted and silently ignored on write, reading
  back as `"psk"` and `null`.
- **`ipsec: null` on PATCH returns `400 IPSEC_REMOVAL_NOT_SUPPORTED`**, and is
  unreachable from Go either way (`*GatewayIpSecPatchRequest` with `,omitempty`).
  `IPSEC_SECRET_CLEAR_NOT_SUPPORTED` fires for `left.secret: null` and does carry
  a `field`.
- Omitting `left.secret` on PATCH preserves the existing PSK — the documented way
  to patch anything else about the tunnel.
- **Two error field paths map to nothing in the schema:** a missing `tenantIds` is
  reported as `field: "customerIds"`, and an array violation as
  `field: "ipsec.left.subnets[].<iterable element>"`. Both are internal Bean
  Validation names leaking. Any consumer doing attribute-level diagnostics off
  `FieldErrors()` needs a documented fall-through for unmappable keys, and this is
  the concrete case proving it is not hypothetical.
- **`recoveryDelayInSec` is required on create for every routing strategy**, even
  `RANDOM`/`NEAREST` whose prose says it is *ignored* — "ignored" describes the
  semantic, not whether you must supply it. Value must be one of
  `300, 1800, 3600, 10800, 28800`; `0` — the Go zero value a caller gets by
  forgetting the field — is rejected. Unlike `vendor`, this rejection *is*
  attributed. PATCH enforces the same set and rejects `null`; omitting the field
  leaves the stored value untouched.
- **Field validation runs before business rules.** A create carrying *shared*
  gateway IDs and a legal delay reaches `422 SHARED_GATEWAY_MEMBER`; the same
  request with an illegal delay stops at `400` first.
  `TestAcceptance_SecurityCloudZtnaGroupedGatewayRecoveryDelay` exploits exactly
  this to pin the whole matrix without provisioning anything.
- `GatewayContact.email`: the spec's regex requires a dot in the domain; the
  server uses Bean Validation's `@Email`, which does not, so `user@localhost` is
  accepted on the wire and rejected by the spec. **The spec is stricter than the
  server** — do not trust the pattern.
- `App.name` is `required` *and* `nullable`: an app created from a
  `predefinedAppId` inherits its name and returns `name: null`. This is why
  `ResolveZtnaAppV1ByName` cannot reach such apps.
- `App.routing.type` is `CUSTOM` or `DIRECT` only. `categoryName` validates
  against the **`displayName`** of the tenant's own category list, not a fixed
  enum, and an unknown value is `409 MISSING_CATEGORY_NAME` — a state conflict,
  not a malformed request.

### UEM Connect

- **`authStrategy` is required and its absence is a 500** (`500 INTERNAL_ERROR`,
  stable across repeats, re-verified 2026-08-31), not a validation error. Supply
  it and a request without credentials returns a proper `422 VALIDATION_FAILED`.
  A request built from the published spec alone could only ever fail, which is
  why `authStrategy`, `tenantId` and `deviceSyncAuth` were patched in — **until
  v1882, which declares all three** on its new `JamfProConnectorCreateRequest`
  and let the request-side patches go.
- **The 500 is no longer reachable through the typed request, and that is the
  point.** v1882 makes `authStrategy` required, so it generates as a non-pointer
  `string` and absence cannot be expressed. What a forgetful caller now sends is
  the empty string, which the enum coercion refuses properly:
  `422 "Cannot coerce empty String (\"\") to JamfProAuthStrategy"`, field
  unattributed (2026-08-31). Required-ness converts an unactionable 500 into a
  diagnosable 422.
- **Deserialization and field validation run ahead of the singleton pre-check**
  (2026-08-31). An unknown `vendor` is `422` where a well-formed request is
  `409`. This is what makes field-level probing possible at all on a tenant that
  already holds a connector, and every UEM Connect create subtest depends on it.
- **`url` is not required for `M2M`, though v1882 lists it in the variant's
  `required` set.** Omitted *and* empty-string both clear field validation and
  reach the 409; a connector created with `url` absent reads back carrying the
  URL the server derived from the named tenant (`https://<pro-tenant>.jamfcloud.com`
  from tenant `<uem-tenant-id>`, 2026-08-31). That is what makes the generated
  non-pointer `URL string` safe — an M2M caller who never sets it sends
  `"url": ""` and is accepted. For the credential strategies `url` genuinely is
  required: `JAMF_PRO_OAUTH` without one is `422 ": invalid auth configuration
  for Jamf PRO"`, unattributed. **Report upstream:** the flat
  `required: [vendor, url, authStrategy]` cannot express a per-strategy
  requirement and is wrong for one of the three.
- **v1882 claims `tenantId` is never echoed back; the wire echoes it.** The
  schema prose says write-only fields "are never echoed back in any response",
  but a GET on an `M2M`-created connector returns
  `"tenantId": "<uem-tenant-id>"` (2026-08-31, re-confirming
  2026-08-28). The marker itself is on the *request* schema and so is harmless,
  but the prose is false and `ConnectorConfig.tenantId` stays patched — it is the
  only field distinguishing an M2M connector from an OAuth one, since
  `authStrategy` reads back as `JAMF_PRO_OAUTH` either way. **Report upstream.**
- **`additionalProperties: false` is not enforced.** An unknown property on a
  `JAMF_PRO` create reaches the 409 (2026-08-31). The spec is stricter than the
  server; harmless for the SDK, which sends no unknown fields.
- **Three response fields are still undeclared anywhere**, observed on an
  `M2M`-created Jamf Pro connector (2026-08-31): `tokenAuthenticationSupported`
  (`true`), `uemRiskStateExtAttrs` (`null`) and `deployedProfiles` (`null`). Only
  the first is typeable from the observation — a null column proves nothing about
  its type — so none is patched in yet. **Report upstream.**
- **Two usable strategies for a Jamf Pro connector, needing different fields.**
  `JAMF_PRO_OAUTH` takes `deviceSyncAuth.clientId`/`.clientSecret` for an API
  integration the caller created themselves, plus the instance `url`. **`M2M`
  takes `tenantId` and no credentials at all** — Jamf Security Cloud provisions
  its own role and integration ("JSC Connector") on that tenant, and `url` is
  ignored. Missing `tenantId` under M2M is `422 "tenantId: must not be null"`.
  This is what keeps the create path testable now that the SDK can no longer mint
  a Pro credential. Both strategies, and `BASIC`, are declared upstream as of
  v1882 and agree with what was probed.
- **`authStrategy` is a provisioning instruction, not stored state:** a connector
  created with `M2M` reads back as `JAMF_PRO_OAUTH`. It does not round-trip.
- **A tenant may hold exactly one connector, whatever its vendor.** A complete,
  correct request answers `409 CONNECTOR_CONFIG_ALREADY_EXISTS` — the message says
  "incompatible UEM vendor" but INTUNE and JAMF_PRO are refused identically, and
  the check fires *before* credential validation, so bogus credentials give the
  same 409. Consequence: the create path is exercisable on a tenant that already
  has a connector, reaching the singleton pre-check without provisioning anything.
- **This service has a server-side oracle for its vendor enum**, unlike ZTNA. A
  recognised vendor reaches field validation (`emmPassword: must not be null`)
  while an unrecognised one fails Jackson subtype resolution first *and prints the
  whole accepted set*: `known type ids = [AIRWATCH, GOOGLE, INTUNE, JAMF_PRO,
  JAMF_SCHOOL, MAAS360, MOBILEIRONCLOUD, MOBILEIRONCORE, WIZY, XENMOBILE,
  EmmServerConfig]` — less `EmmServerConfig`, which is Jackson's base-class name
  leaking. Do not generalise ZTNA's unhelpfulness to the rest of Security Cloud.
- Secrets are genuinely write-only: a read returns only `clientId`, `username`
  and an `empty` flag. Combined with the singleton rule, that is why a
  credential-bearing connector cannot be deleted and recreated, and why every
  other write in the family needs a tenant whose *only* connector is disposable.
- **An `M2M` connector is the disposable one, and that unblocks the write
  suite.** It is recreatable from `tenantId` alone — no secret to lose — so
  unlike a credential-bearing connector it can be deleted and remade at will.
  The JSC sandbox tenant `<jsc-sandbox>` now holds exactly such a connector
  (`<connector-id>`, `JAMF_PRO`/`M2M` against
  `<pro-tenant>.jamfcloud.com`), created 2026-08-31 in place of the MaaS360 one that
  was there before. So `TestAcceptance_SecurityCloudUemConnectWrites`, whose skip
  reason is that the tenant's connector is unrestorable, is now stale: enablement,
  the sync-settings full-replacement round-trip, trigger/cancel sync and the
  activation-profile deploy are all reachable. Note the deletion must also clear
  the "JSC Connector" API role and integration `M2M` provisions on the Jamf Pro
  side, which the SDK cannot do.
- `EmailMapping`'s `CUSTOM` type returns an extra `fieldName` naming the UEM
  attribute to read, **deliberately undocumented** because ADG-125 requires every
  documented field to be present in every response. The transport decodes
  leniently so nothing breaks, but `fieldName` is unreachable from Go. Worth
  reporting: a documentation policy is costing generated clients a real field.
- **v1981 completed the per-vendor create split and moved the spec to
  `external/` (2026-09-01).** The generic `ConnectorCreateRequest` is gone; all
  ten vendors now have their own typed create schema and
  `ConnectorCreateRequestBody`'s discriminator mapping is 1:1, which the
  generator handles correctly — ten distinct variant fields, ten switch cases,
  no dedup collapsing anything (the v1882 fix holding under a different shape).
  `additionalProperties` flipped `false` → `true` on `JamfProConnectorCreateRequest`
  and `JamfProCredentials`, and `url` gained `minLength: 1`.

  **Only the `JAMF_PRO` variant is wire-verified.** The nine others are
  documentation as of v1981 and nothing here has exercised them: the JSC sandbox
  tenant has no connector and creating one provisions an API role on the UEM side
  the SDK cannot remove. Their required-field sets are upstream's claim. The
  new enums — `CitrixCloudOauthConfigRegion`
  (`ASIA_PACIFIC`/`EU`/`USA`/`JAPAN`) and
  `XenMobileConnectorCreateRequestAuthStrategy`
  (`USERNAME_PASSWORD`/`CITRIX_CLOUD_ADMIN`/`CITRIX_CLOUD_ADMIN_OAUTH`) — are
  unverified for the same reason.

  Also note the spec is now sourced from `external/uem-connect` rather than
  `internal/stage/uem-connect`. Content is identical; what changes is that
  `api/securitycloud_uem_connect_api.json` stops advertising the stage host and
  an `(STAGE)` title.
- **v1958 constrained the two sync-interval fields, redefined one of them, and
  none of it could be probed (2026-09-01).** `refreshRateMinutes` went
  `minimum: 1` → `minimum: 60, maximum: 1440` with enum
  `60, 120, 240, 480, 720, 1440`; `deviceUnmanagedThreshold` gained enum
  `0, 1, 3, 5, 7, 14`. Both sit on `SyncSettings` (PUT body) and
  `ConnectorConfig` (response), and the generated diff is godoc only — numeric
  enums become `Allowed values:` lines, constants are emitted for string enums
  only.

  The redefinition matters more than the constraint:
  `deviceUnmanagedThreshold` was *consecutive syncs absent, `0` disables the
  grace period*; it is now *days since last check-in, `0` uses the platform
  default of 3 days*. The unit changed and `0` flipped from off to default-on, so
  a caller who sent `0` to disable the behaviour now gets a 3-day threshold. The
  spec also newly claims the field is **not applicable to `JAMF_PRO`** — silently
  ignored for that vendor.

  **Both are now wire-verified — create a connector, do not try to shortcut it.**
  (**Superseded on the ordering point, 2026-09-02** — field validation is now
  observed to run *before* resource resolution on this PUT, so the trick does
  work; see the v2005 entry below for the table. The rest of this bullet stands.)
  The doomed-request trick does not work here: five PUTs to a bogus `configId`
  carrying in-enum (`1440`, `3`), out-of-enum (`90`, `2`) and below-minimum (`1`)
  values all returned the identical `404 NOT_FOUND` "Config with ID … doesn't
  exist" with `GET /connectors` → 200 as the control. **Resource resolution runs
  before field validation on this PUT**, so a bogus id reaches no validator — the
  reverse of the ordering that makes the technique work elsewhere. The only route
  is a real connector, and creating one is cheap (below). Probed 2026-09-01 on
  the JSC sandbox against a freshly created `JAMF_PRO`/`M2M` connector:

  **`deviceUnmanagedThreshold` is silently ignored for `JAMF_PRO` — confirmed.**
  PUT sync-settings carrying `3`, `7`, `14`, `1` and `0` each answered **204**,
  and the readback was **`0` every time**. A freshly created connector starts at
  `0`. So the v1958 godoc is right, and the field is inert for this vendor.

  This **refutes an earlier probe** (2026-08-28) that recorded it as stored on
  create, reset to `0` when omitted, and accepting `-1`. The service changed
  between those dates. The consequence downstream was severe rather than
  cosmetic: `terraform-provider-jamfplatform` defaulted the attribute to `3` and
  sent it, so *every* create failed with "Provider produced inconsistent result
  after apply" — fixed in its PR #358 by making the attribute Computed and not
  sending it. Treat a stored-value claim about this field as stale unless it
  carries a date after 2026-09-01.

  **`refreshRateMinutes` enforces the enum — confirmed.** `60` and `1440` → 204.
  `360`, `59`, `1`, `100000` and `0` → **422 `VALIDATION_FAILED`**. So the
  `minimum: 60` / `maximum: 1440` plus enum from v1958 is live, and the older
  "no ceiling, 100000 accepted" observation is dead.

  **The 422 leaks the accepted set in `description` and leaves `field` null:**
  `"Invalid refreshRateMinutes: 360. Allowed values: 60, 120, 240, 480, 720, 1440"`,
  `field: null`. Same shape as uem-connect leaking Jackson's own message
  elsewhere — the useful content is in the prose, not the structured fields, so
  `FieldErrors()` gets nothing here.

  **`M2M` create self-provisions — confirmed, and it leaks.** `POST /connectors`
  with `{vendor: JAMF_PRO, authStrategy: M2M, url, tenantId}` → **201**. The
  readback shows `authStrategy` flipped to `JAMF_PRO_OAUTH`, `connected: true`,
  `uemVersion` resolved to the live Jamf Pro version, and a
  `deviceSyncAuth.clientId` Jamf Security Cloud minted for itself. `tenantId` is
  returned despite the spec calling it write-only, which is why the SDK patches
  it back into `ConnectorConfig`. `DELETE` → 204 and the list returns to
  `totalCount: 0`.

  **But the Jamf Pro side is not cleaned up, and it accumulates.** Each create
  provisions an API integration on the target Jamf Pro instance named
  `JSC Connector`, and the connector DELETE does **not** remove it. Verified
  2026-09-01 with a control: immediately after deleting the connector,
  `GET /pro/v1/api-integrations/2438` still returned `200` for the integration
  whose `clientId` matched the connector's own `deviceSyncAuth.clientId`, still
  `enabled: true`. The tenant carried **97 `JSC Connector` integrations against
  zero live connectors** — 88% of all 110 integrations on the instance — with the
  display names degenerating to `JSC Connector (10) (96)` as the uniquifier
  recursed. One shared API role (`JSC Connector`, id 2174) serves all of them.
  This is worth reporting upstream: 97 enabled OAuth client credentials with no
  owner is a security finding, not untidiness. Delete them by hand from the Pro
  side; the SDK has no way to correlate an integration back to a connector once
  the connector is gone.

- **v1942's `uemGroups` documentation is upstream's claim, not wire truth
  (2026-09-01).** It is the only substantive v1942 change the SDK took, and it is
  doc-only, but it asserts four behaviours nothing here has probed: that
  `GroupMapping.emmGroupId` **requires** the `computer_<id>` / `mobile_<id>`
  prefix for Jamf Pro and rejects a bare ID; that
  `ActivationProfileDeployRequest.uemGroups` accepts a bare numeric ID as well;
  that a present prefix must match the device type the `platform` deploys to; and
  that scoping is **additive** — the IDs merge into the configuration profile's
  existing scope, so a deploy can widen but never narrow or clear it, and an
  omitted or empty array leaves the existing scope untouched rather than meaning
  "all groups". The last is the one that would bite a caller who read the old
  wording ("If omitted or empty, deploys to all UEM groups"), so it is worth
  confirming against the sandbox connector above before a consumer relies on it.
  The same text sends callers to `GET /v1/mobile-device-groups`, which v1942
  deleted from `jpapi` in the same build.

- **v2005 is uem-connect only, and it is documentation plus declared error
  responses (2026-09-02).** Zero operations added or removed, zero schemas added
  or removed, and no semantic change to any operation outside its `responses`
  map: every one of the twelve operations gained a `406`, and the four that carry
  a request body also gained a `415`, with two new `components.responses` entries
  (`NotAcceptable`, `UnsupportedMediaType`) behind them. The generated Go diff is
  **four lines of godoc on `SyncSettings.Vendor`** — method comments are built
  from the operation `summary`, not its `description`, so the rewritten
  `updateSyncSettings` prose reaches no method. `internal/stage/uem-connect` took
  a byte-identical delta, and `internal/dev` agrees op-for-op (12/12) and carries
  the same new responses, so this is not an environment rollout. `_permissions`
  was byte-identical in the same bundle and the prod rollup's path count did not
  move (802).

  Two of the three claims cost the SDK nothing. `415` cannot be reached through
  the generated methods at all: every body-bearing uem-connect operation goes
  through `DoWithContentType(..., "application/json", ...)`, so the header is
  always the one the server wants. `406` likewise — the transport never sends a
  restrictive `Accept`.

  **All three claims were probed on 2026-09-02 and all three are real — with one
  spec/wire disagreement on top.** The probe needed a *third* credential: see the
  credential note at the end. The connector was a pre-existing live
  `JAMF_PRO` one on the JSC (`6a98020c4d55188d25835124`,
  `connected: true`), so nothing was minted and nothing accumulated on the Jamf
  Pro side.

  **`422 VENDOR_MISMATCH` is enforced, atomic, and unattributed.** `PUT
  sync-settings` on the real connector with `vendor: "INTUNE"` and every other
  field set to the connector's current value → **422**,
  `code: VENDOR_MISMATCH`, `field: null`, description
  `"vendor 'INTUNE' in request body does not match stored vendor 'JAMF_PRO'"`.
  The readback immediately after was byte-identical to the readback before, so
  the request is rejected whole rather than partially applied, and the same body
  with `vendor: "JAMF_PRO"` answered **204** as the control with the readback
  again unchanged. Note `field` is null, so the vendor names live only in the
  prose, the same pattern as the `refreshRateMinutes` enum message — and
  `FieldErrors()` does **not** come back empty for a null field, it returns the
  message keyed on `""`. An assertion that the map is empty fails; assert that no
  *named* field is attributed.

  **The sandbox connector is shared and someone else edits it.** Between one
  readback and the next, minutes apart with only rejected writes in between,
  `refreshRateMinutes` moved 1440 → 720 and `groupSettings.groupMappings` went
  from `[]` to `[{emmGroupId: mobile_225, wanderaGroupId: d56a8e26…}]`. So a
  UEM Connect test must not assert equality on fields its own request did not
  touch, and a *successful* full-replacement PUT built from a stale read will
  silently discard a concurrent operator's work — which is the strongest argument
  for keeping the remaining write tests behind their skip.

  **`415` is enforced and it precedes everything.** `PUT sync-settings` against a
  **bogus** `configId` with `Content-Type: text/plain` → **415**,
  `code: UNSUPPORTED_MEDIA_TYPE`, `field: null`. So the media-type check runs
  before resource resolution, which makes it the one claim in this build that is
  probeable with no fixture at all.

  **`406` is enforced, but the spec is wrong about what the operation produces.**
  `Accept: application/pdf`, `text/csv` and `image/png, application/pdf` each
  answered **406** `code: NOT_ACCEPTABLE` on `GET /connectors`. (That probe used
  only the collection `GET`, which is why it read as a first-class check; v2018
  below establishes that `406` is decided at response-serialization time and is
  the *last* thing in the pipeline.) But
  **`Accept: application/xml` answers 200 with an XML body** — `<ConnectorListResult>…`
  on the collection and `<JamfProConnectorConfig>…` on `GET sync-settings` —
  while the spec's new `NotAcceptable` text says the operation "only produces
  `application/json`". uem-connect has Jackson's XML converter registered and its
  own spec does not know. **Report upstream.** The SDK is not exposed: it never
  sets `Accept`, so it always gets JSON — but `WithHeaders` does not reserve
  `Accept`, so a caller or a reverse proxy that sets `application/xml` would feed
  XML to `json.Unmarshal` and fail at the decode rather than silently. Worth
  knowing that the *sibling* APIs behave differently: `GET /securitycloud/v1/groups`
  **does** 406 on `application/xml`, and does it with the legacy error envelope
  (`{messageKey, message, error, logref, statusCode}`) rather than the
  `{httpStatus, traceId, errors[]}` shape uem-connect uses — so the negotiation
  layer is per-service, not gateway-wide.

  **The recorded validation ordering on this PUT is wrong, and it is now the
  useful direction.** The 2026-09-01 note above says resource resolution runs
  *before* field validation, on the strength of five bogus-`configId` PUTs all
  answering `404`. Re-probed 2026-09-02, that is not what happens:

  | body | `configId` | result |
  |---|---|---|
  | `{"vendor":"JAMF_PRO"}` | bogus | **422** `VALIDATION_FAILED` ×2, `field: autoDeviceDeletion`, `field: deviceFieldMappings` |
  | `{"vendor":"JAMF_PRO","refreshRateMinutes":90}` | bogus | **422** `VALIDATION_FAILED`, `field: null`, "Invalid refreshRateMinutes: 90. Allowed values: 60, 120, 240, 480, 720, 1440" |
  | complete valid body, `vendor: JAMF_PRO` | bogus | **404** `NOT_FOUND`, "Config with ID … doesn't exist" |
  | complete valid body, `vendor: INTUNE` | bogus | **404** `NOT_FOUND` |

  So the real order is **media type → body validation → resource resolution →
  vendor match**, and it is self-consistent: an invalid body never reaches the
  lookup, a valid one does. **The doomed-request trick therefore works on this
  PUT after all** — every field constraint on `SyncSettings` is reachable with a
  bogus `configId` and no connector — while `VENDOR_MISMATCH` alone sits past the
  lookup and needs a real one. Whether the service changed between the two dates
  or the earlier probe sent complete bodies cannot be settled from here; treat
  the earlier "resolution first" claim as superseded and the table above as
  current.

  Two smaller facts from the same pass. The PUT's required set is
  `vendor`, `autoDeviceDeletion`, `deviceFieldMappings` — exactly as the spec
  declares — and **validation short-circuits**: adding an out-of-enum
  `refreshRateMinutes` to a body missing both required objects reported *only*
  the enum failure, suppressing the two not-null failures, so a caller fixing
  errors one 422 at a time will not see them all at once.

  **The credential matters more than the tenant here.** Three credentials, all on
  eu, two of them on the same JSC `<jsc-sandbox>`:

  | credential | `securitycloud` device-groups | `uem-connect` |
  |---|---|---|
  | Platform/`pro` (eu) | 403 | 403 |
  | JSC sandbox (`<jsc-tenant-c>`) | 200 (`device-groups:*`) | **403** |
  | older JSC (`<jsc-tenant-d>`) | 200 | **200** |

  So `uem-connect` is a separate capability from `device-groups`, neither
  credential holds the legacy `read:jsc:all` wildcard that would cover both, and
  **the connector that this section's earlier entries describe is visible only to
  the third credential** — which is why the tenant looked like it had none. Any
  future uem-connect probe needs that one. Both tokens are opaque, so the rego's
  permission sets in
  the gateway's uem-connect policy are the only way to
  read a grant back.

- **v2018 is `uem-connect` only, and it takes `406` back off seven of the twelve
  operations (2026-09-02).** Every other file outside `internal/dev` is
  byte-identical to v2005 — `capi`, the three account specs and
  `_permissions/{routes,scopes}.yaml` included — so the only other diffs were the
  manifest and the two unified rollups, whose path counts did not move. Zero
  operations, zero schemas, zero prose changes, `415` untouched at four. The
  whole diff is 21 lines: `406` deleted from the seven operations that answer
  204/202, kept on exactly the five that write a 2xx response body (`GET
  /connectors`, `GET /connectors/{id}`, `GET …/sync-settings`, `GET …/sync/runs`
  and `POST /connectors` → 201). `internal/stage` took a byte-identical delta and
  `internal/dev` agrees op-for-op (5/12 both sides), so this is not an
  environment rollout. Generated diff: 21 deletions in
  `api/securitycloud_uem_connect_api.json`, **zero Go** —
  `components.responses.NotAcceptable` survives, still referenced five times —
  and the `api/`-fallback regen reproduced the identical single diff, so CI
  agrees.

  **The removal is correct, and the reason is that `406` sits at the *end* of the
  pipeline rather than the start.** It is Spring's message-converter selection at
  write time, so it can only fire once the handler has a body to serialize.
  Probed with `Accept: application/pdf` and the connector-bearing JSC
  tenant, control (`GET /connectors`, no `Accept`) 200 in the same invocation:

  | request | result |
  |---|---|
  | `GET /connectors`, `Accept: application/pdf` | **406** `NOT_ACCEPTABLE`, `field: null`, "No acceptable representation available" |
  | `GET …/{real}/sync-settings`, same `Accept` | **406** `NOT_ACCEPTABLE` |
  | `GET /connectors/{bogus}`, same `Accept` | **404** `NOT_FOUND` — negotiation not reached |
  | `GET …/{bogus}/sync-settings`, same `Accept` | **404** `NOT_FOUND` |
  | `POST /connectors`, same `Accept`, out-of-enum `vendor` | **422** `VALIDATION_FAILED` — negotiation not reached |
  | `DELETE /connectors/{bogus}` | **404** |
  | `DELETE …/{bogus}/enablement` | **404** |
  | `PUT …/{bogus}/enablement` (valid body) | **404** |
  | `PUT …/{bogus}/sync-settings` (valid body) | **404** |
  | `POST …/{bogus}/sync/runs` | **404** |
  | `POST /activation-profiles/BOGUSCODE/deploy-to-uem` (valid body) | **404** `ACTIVATION_PROFILE_NOT_FOUND` |
  | `DELETE …/{bogus}/sync/runs/current` | **204** — see below |

  Every one of those eleven carried `Accept: application/pdf` and **not one
  answered 406**, including the four `GET`s whose spec keeps the declaration. So
  the full order is **`Content-Type` (415) → body validation (422) → resource
  resolution (404) → business rules (422 `VENDOR_MISMATCH`) → response
  serialization (406)**, and the flat "406 is enforced" recorded at v2005 is
  superseded: it holds only for a request that gets all the way to writing a
  body. Two consequences. A caller cannot use a restrictive `Accept` to probe
  anything — it is the one check that never short-circuits. And the seven
  operations v2018 stripped genuinely cannot return 406, so upstream's narrowing
  is right rather than an under-declaration.

  **`DELETE /connectors/{configId}/sync/runs/current` answers 204 on a connector
  that does not exist**, both with and without an `Accept` header, and the spec
  declares no `404` for it — consistent, but it means cancel-current-sync is an
  unconditional success that resolves nothing, so a caller cannot distinguish
  "cancelled a running sync" from "typo'd the configId". It is the only
  uem-connect operation with no `404` in its response map. Worth reporting
  upstream as a surface question rather than a defect.

  **The XML disagreement stands.** v2018 left the `NotAcceptable` prose unchanged,
  and `Accept: application/xml` on `GET /connectors` still answers **200** with
  `<ConnectorListResult>…`, re-probed 2026-09-02. The spec still says these
  operations produce only `application/json`. Still worth reporting upstream, and
  still not an SDK exposure except through `WithHeaders`, which does not reserve
  `Accept`.

  `TestAcceptance_SecurityCloudUemConnectAcceptNegotiation` pins all three facts
  and needs no fixture: 406 on the collection `GET` (which always resolves), 404
  — asserted as **not** 406 — on `DisableUemConnectorV1` with a bogus id, and the
  XML case as a limitation whose assertion fails the day upstream restricts the
  converter. It builds its own client via `jscClientWithHeaders`, because
  `WithHeaders` applies to every request and a restrictive `Accept` on the shared
  singleton would break every other test.

### PUT response shapes

`PUT /v1/…/groups/{id}` answers **200 with the updated `Group`**, not 204 — and it
is the only Security Cloud PUT that disagrees with its spec. `PUT dns/search-domains`,
`PUT dns/custom-hostname-mappings`, `PUT uem-connect/…/enablement` and
`PUT uem-connect/…/sync-settings` were each probed by idempotent round-trip (GET
current state, PUT it back unchanged, confirm nothing moved) and all four genuinely
return 204.

**A create's response shape is only ever knowable by probing it.** `POST /ztna/apps`,
`/ztna/grouped-gateways` and `/ztna/gateways` returned the full resource object for
months, then reverted to the spec-declared `CreateResponse` on or before v1439.
Nothing in the spec ever described the old behaviour, so no ingest could have
predicted either direction; an acceptance assertion on response *shape* caught it.

### Device groups v1 deprecation headers

Wire-verified header-by-header across all five v1 group operations:

| op | `Deprecation` header | spec `deprecated` |
|---|---|---|
| `GET /v1/groups` | `@1786492800` (2026-08-12) | **true** |
| `PUT /v1/groups/{id}` | `@1787616000` (2026-08-25) | absent at the time |
| `POST /v1/groups`, `GET`/`DELETE /v1/groups/{id}` | none | absent |

Two things follow. A wire-only deprecation is still surfaced, because
`logDeprecation` logs any runtime `Deprecation` header once per method+path —
nothing needed building for it. And **the successor the header names does not
exist**: `/v2/customers/{tenantId}/groups` is a third URL shape and answers 403,
as does every `/v2/groups/{id}` form. The server is telling callers to migrate a
write to a path that is neither published nor routed.

`ListDeviceGroupsV1` returns a **bare JSON array** and `ListDeviceGroupsV2` wraps it
in `{groups: []}`, so `ResolveDeviceGroupV1ByName` and `…V2ByName` differ in exactly
the way most likely to rot silently — v2 is the SDK's only `resultsField` user, and
the v1 bare-array sibling is what it is contrasted against. Both resolvers and both
Applies exist side by side, sharing the v1 create/update/delete ops since v2 is
list-only. **v1942 withdrew `GET /v1/groups` and `PUT /v1/groups/{groupId}`, which
would have collapsed all of that; the spec was held at v1897 instead — until
2026-09-04, when the v2 update handler was fixed and the withdrawal was taken.
As of then `ListDeviceGroupsV1`, `ResolveDeviceGroupV1ByName` and
`ApplyDeviceGroupV1` no longer exist, so the bare-array contrast above is
history: v2's envelope is the package's only device-group list shape.**

`Default Group` comes back with **no `id`** on both v1 and v2 — the reason
`GroupListItem` requires only `name`, and the reason `ResolveDeviceGroupV1ByName`
yields an empty ID for it.

### Security Cloud Enrollment (activation profiles), 2026-09-01

Ingested at **v1993**, which published `external/securitycloud-enrollment` for the
first time — in all three environments at once, having existed in none of them at
v1988. Six operations, tenant-scoped. Everything below was probed on the EU
gateway against a JSC-granted credential, with `GET /securitycloud/v1/categories`
→ 200 as the routed control and `GET /securitycloud/v1/nonsense-probe-xyz` → 403
`BAD_PERMISSIONS` as the unrouted control, both in the same invocation. A Jamf Pro
credential is useless here: it answers 403 on every `/securitycloud/*` path
including a deliberately nonsense one, so it cannot even classify routing.

**This section supersedes the earlier "not covered — no published spec" note, and
corrects it.** That note recorded `POST /v1/activation-profiles/delete-multiple`
as answering "204 and deleting nothing", concluding a created profile leaks an
undeletable enrollment code. The delete *does* take effect; what it does not do is
show up on the read surface. See the soft-delete row below.

| operation | status | what the wire does |
|---|---|---|
| `GET /v1/activation-profiles` | 200 | `{activationProfiles: [{code}]}`, no pagination params, no envelope counters. `origin` is a **required** query param |
| `POST /v1/activation-profiles` | 201 | body is `{"code": "…"}`, **not** the declared `{id, href}`; no `Location` header |
| `GET /v1/activation-profiles/{code}` | 200 | `{code}` and nothing else. 404 `NOT_FOUND` ("No such item exists.") for an unknown code |
| `POST /v1/activation-profiles/{code}/pause` | 204 | idempotent — a second pause is another 204, not a conflict. 404 for an unknown code |
| `POST /v1/activation-profiles/{code}/resume` | 204 | same |
| `POST /v1/activation-profiles/delete-multiple` | 204 | no body, no per-code result; 204 for an unknown code, an already-deleted code and a duplicated code alike |

#### The create response is not what the spec declares

`{"code": "…"}` where the spec declares `ActivationProfileResponse` `{id, href}`
plus a `Location` header. Verified **both with and without**
`Accept-Encoding: gzip` — identical body, no `Location` either way — so this is
*not* the href-injection plugin nulling a compressed body, which is what happens
on the DNS and ZTNA creates ([`href` is nulled by response
compression](#href-is-nulled-by-response-compression)). The field is simply never
sent.

`CreateActivationProfileV1` therefore carries a `responseType: "ActivationProfile"`
override. Without it the generated method decodes `{code}` into an
`ActivationProfileResponse`, yielding a zero-valued struct and **reporting
success** — the same silent-data-loss shape the v1439 ZTNA create revert had, in
the opposite direction.

#### Deletion is a soft delete the read surface does not reflect

The finding worth reporting upstream. After `delete-multiple` succeeds:

- `GET /v1/activation-profiles/{code}` still answers **200** with the code.
- The collection still returns the code, with no field distinguishing it from a
  live profile.
- Only a **write** reveals the state: pause or resume answers
  `409 STATE_CONFLICT` — `Activation profile with code: <code> is already
  deleted.` — in the service's own envelope, not `ApiError`.

Verified on seven profiles created and deleted during the probe, all seven
answering the 409 afterwards while all seven remained listed, and a pre-existing
profile answering 204/204 to pause/resume in the same session as the live control.
A 5-second settle and a repeated single-code delete changed nothing, so this is
not eventual consistency.

Two consequences. A caller cannot confirm a deletion: 204 covers "deleted",
"never existed" and "silently skipped" indistinguishably, and the read surface
will not settle it. And **every acceptance run leaves a permanent row in the
tenant's list**, which is why the suite creates exactly one profile per run and
folds every assertion that could have justified a second create into that one
request body.

#### Three declared constraints the server does not enforce

| declared | actual |
|---|---|
| `additionalProperties: false` on the create request | an undeclared key is accepted, 201 |
| `capabilities.note` `maxLength: 255` | 256 characters accepted, 201 |
| `platforms` `maxItems: 2` | `["iOS","MAC","iOS"]` accepted, 201 — the bound is applied *after* de-duplication; a genuinely three-valued list is refused |

The `note` and `platforms` cases are asserted in
`TestAcceptance_SecurityCloudActivationProfileLifecycle`'s single create, so they
fail the day upstream enforces its own schema. The `additionalProperties` case is
not expressible through a generated struct and lives only in the
`PublicApiCreateActivationProfileRequest` godoc.

#### One constraint the schema does not declare at all

`networkSecurity` and `vulnerabilityManagement` are **coupled**: both enabled or
both disabled, or the create is refused with `400 INVALID_FIELD` on
`capabilities` — `networkSecurity and vulnerabilityManagement must both be enabled
or both disabled`. `dataPolicy` alone is fine. Nothing in `PublicApiCapabilities`
says so.

#### What is enforced, and how it is attributed

All as `400 INVALID_FIELD` in the declared `ApiError` shape:

| field | trigger | description |
|---|---|---|
| `origin` | absent key | `Missing required attribute origin.` |
| `origin` | present but empty, **or** present and out of enum | `Origin not provided.` |
| `name` | absent or empty | `must not be blank` |
| `name` | 101 characters | `size must be between 0 and 100` |
| `platforms` | `[]` | `must not be empty` **and** `size must be between 1 and 2` (two details for one cause) |
| `platforms[]` | `["ANDROID"]` | `Only 'iOS' or 'MAC' platforms are accepted` — attributed to `platforms[]`, brackets included |
| `codes` | `[]`, absent, or 101 entries | `size must be between 1 and 100` |

Two notes on `origin`. The out-of-enum case reporting `Origin not provided.` when
it *was* provided misstates the cause and is worth reporting. And the absent-key
form is **unreachable through the SDK**: `Origin` is a required non-pointer
string, so an empty one marshals as `"origin": ""` and gets the present-but-empty
message; the absent-key message was only observable by hand.

`groupId` is **not validated for existence** — a nonexistent group ID is accepted
and answers 201, so a typo produces a profile silently scoped to nothing.

#### Two 400s arrive in the wrong envelope

Neither is the `ApiError` shape the spec declares for the status, so the SDK
parses no structured details out of either and only the raw body is matchable:

- **`capabilities: {}`** (the `minProperties: 1` violation) →
  `{"error": "INVALID_INPUT", "message": "Cannot create activation profile with
  given parameters for customer <tenant-uuid>", "logref": …, "statusCode": 400}`.
  So the requirement is real but enforced as a business rule, after field
  validation.
- **`origin` omitted from the list query** →
  `{"error": "BAD_REQUEST", "message": "Required parameter 'origin' is not
  present.", …}`. Reachable through the SDK, because the generated method drops an
  empty query param from the URL rather than sending `origin=`.

#### The list operation's documented error is wrong on both counts

The spec documents an invalid `origin` as `code: INVALID_PARAMETER`,
`field: origin`, `Only 'PUBLIC_API' value is accepted as origin.` The server sends
`code: INVALID_FIELD`, **no field at all**, `Unknown origin value.` Pinned in
`TestAcceptance_SecurityCloudActivationProfileRejections/list/origin_out_of_enum`
so whichever side moves, it shows.

#### The read model carries nothing

`ActivationProfile` is `{code}`. No name, capabilities, platforms or state — so
nothing a create sends can be read back, `pause`/`resume` have no observable
effect on any GET, and a name→ID resolver is impossible (there is no name in the
list). `PauseActivationProfileV1` and `ResumeActivationProfileV1` are therefore
covered as calls, not as outcomes.

---

## Jamf Account (`account`) — organization scope

### Re-probed at v2100: both holds stand, and `partners` turns out to be granted (2026-09-09)

v2100 changed neither held spec, so the hold question is unchanged; the probe
was run anyway because the holds are the only reason the SDK is behind on any
spec. Tenant A (`<org-a>`, US gateway, organization scope so no scope
header), with `GET /licensing/v1/licenses` → 200 as the control in the same
invocation and `GET /licensing/v1/nope-not-a-path` →
`403 BAD_PERMISSIONS` as the unrouted control:

- **Licensing.** `type` non-null on **16/16** rows, `licenseType` on **8**.
  Both fields present in the key union, so `authRegion`-style rename is still
  ruled out and taking v2082 would still drop a populated classifier.
- **SSO.** All five domains resolved through
  `GET /sso/v1/domains/allocation/{domain}` → 200, and every connection
  carries `authZeroRegion` and no `authRegion`:
  `{"assignedConnection":"con_RMBLC9S3qpC6Bzv0","assignedConnectionOrgId":"org_k7LP9cP4h3RijIaR","authZeroRegion":"US"}`
  — **5/5**, values `US`×3, `JP`, `RAMP`. (`RAMP` is the undeclared region the
  SDK carries via `enumAdditions`; it is still on the wire.)

**The recorded claim that `account-partners` "403s on both organization
credentials" is wrong, and the mistake is the same shape as the
`/sso/v1/domain-allocations` one: a namespace judged from one path.**
`GET /partners/v1/deal-registrations` answers **200**
(`{"totalCount":0,"results":[]}`). What 403s is nothing — all five distributor
operations are routed *and* authorized, and every one answers the standing
the standing distributor-service fault:

| operation | status | body |
|---|---|---|
| `GET /partners/v1/deal-registrations` | 200 | `{"totalCount":0,"results":[]}` |
| `GET /partners/v1/distributor/configuration` | 400 | `[UPSTREAM_ERROR] Failed to get configuration via Skyway distributor service` |
| `GET /partners/v1/distributor/quotes/NOPE123` | 400 | `… Failed to get quote NOPE123 via Skyway distributor service` |
| `GET /partners/v1/distributor/purchase-orders/NOPE123` | 400 | `… Failed to get purchase order NOPE123 via Skyway distributor service` |
| `POST /partners/v1/distributor/validate-purchase-order` `{}` | 400 | `… Failed to validate purchase order via Skyway distributor service` |
| `GET /partners/v1/nope-bogus` | 403 | `BAD_PERMISSIONS` (unrouted control) |

Note the last write: `{}` does not reach body validation — the upstream call
fails first — so **field validation on this operation is upstream-side**, and
the doomed-request trick cannot probe it while that fault stands.

The whole account lane is consequently green for the first time: all eleven
`TestAcceptance_Account*` tests pass or skip on a write opt-in
(`JAMFPLATFORM_ACC_ORGANIZATION_WRITE_OK`, plus the SSO-create skip that names
the 500 `UPSTREAM_ERROR`), and **none skips for want of a credential**. The two
`isSkywayScopeFault` pins fired as designed, asserting the block rather than
tolerating it.

### Both holds re-confirmed on two organization tenants (2026-09-04)

The `account-licensing` and `account-sso` holds have stood since v1865. v2082
changed each spec by exactly the one field its hold names — plus a `servers`
region-enum narrowing `[us, eu, apac]` → `[us]`, which the SDK never reads —
so the whole question is whether the wire has caught up. It has not, on either.

Two **independent organization tenants**, both on `https://us.api.jamfcloud.com`,
organization scope so no scope header, each with
`GET /licensing/v9/bogus` → `404 page not found` as the unrouted control:

| | tenant A `<org-a>` | tenant B `<org-b>` |
|---|---|---|
| licences | 16 | 24 |
| SSO domains | 5 | 7 |
| SSO connections | 5 | 6 |

**Licensing: `License.type` is populated 40/40 and is not redundant.**
v2082 deletes the property (it was `deprecated: true`, `type: string`,
`example: JAMF_PRO_SUBSCRIPTION`). On the wire it is present and non-null on
every row of both tenants.

It is **not** a rename of `licenseType`: both fields coexist, and `licenseType`
is non-null on only 8/16 and 16/24. It is **not derivable** either — on **17 of
the 40** rows `type` equals none of `licenseType`, `addOnType` or
`productTopLine`:

```
productName                     type                      licenseType     addOnType
Jamf Trust                      JAMF_SECURITY_CLOUD       null            JAMF_TRUST     <- disagrees with addOnType
Jamf Data Policy                JAMF_SECURITY_CLOUD       NFR             null
Jamf Threat Defense             JAMF_PROTECT              NFR             null
Jamf Private Access             JAMF_PRIVATE_ACCESS       NFR             null
Jamf Pro for iOS                JAMF_PRO_SUBSCRIPTION     SUBSCRIPTION    null
Jamf Pro for macOS              JAMF_CLOUD_SANDBOX        SANDBOX_DEV     null
Jamf Connect                    JAMF_CONNECT              NFR             null
```

`type` is a product-family classifier; `licenseType` is a commercial class
(`NFR`, `BETA`, `SUBSCRIPTION`, `SANDBOX_DEV`) and `addOnType` only ever covers
add-ons. Two rows both read `JAMF_SECURITY_CLOUD` under different
`productName`/`addOnType` combinations, which no other field expresses. So the
removal is a **silent loss of information on 42% of rows**, not a tidy-up of a
duplicate — a stronger claim than the "populated 16/16" this hold was first
recorded with, and the one to put upstream.

**SSO: the wire sends `authZeroRegion` on 11/11 connections and `authRegion` on
none.** v2082 renames the property (and drops "Auth0" from two descriptions).
Every connection on both tenants carries exactly
`["assignedConnection", "assignedConnectionOrgId", "authZeroRegion"]`:

```
GET https://us.api.jamfcloud.com/sso/v1/domains/allocation/reddawgramp.com  → 200
{"domain":"reddawgramp.com","connections":[{"assignedConnection":"con_...",
 "assignedConnectionOrgId":"org_...","authZeroRegion":"RAMP"}],"jamfIdEnabled":true}
```

Tenant B exercises all five region values across its seven domains — `US`, `EU`,
`AU`, `JP` and `RAMP` — which independently confirms the `RAMP` `enumAdditions`
entry is still load-bearing: v2082 still declares `Region` as
`[US, EU, AU, JP]`.

**A recorded diagnosis was wrong, and the error is the reusable part.** This
hold was previously marked un-re-probeable: "`/sso/v1/domain-allocations`
answers `403 BAD_PERMISSIONS` for that credential while `/sso/v1/connections`
answers 200 in the same session, so the capability is ungranted rather than the
endpoint being absent; it needs a credential holding `sso-domains`."

**There is no `/sso/v1/domain-allocations` path.** The operation is
`GET /v1/domains/allocation/{domain}`, and it answers **200** on both
credentials with no special grant. The 403 came from probing a path the spec
never declared, and in this namespace the gateway's refusal of an unmapped path
is byte-identical to its refusal of an ungranted one — same `403`, same
`BAD_PERMISSIONS`, same body. So a wrong URL manufactured a capability gap and
parked a re-probe for a month.

**Read the path out of the spec before concluding a capability is missing**, and
prefer a *declared* sibling in the same namespace as the control: here
`GET /sso/v1/domains` → 200 would have shown the credential reaches SSO domains
perfectly well. (a domain on tenant B returns `connections: []` with
`jamfIdEnabled: true`, so the read is also safe on a domain with no
allocations — no fixture needed to exercise it.)

**`account-partners` is still ungranted.** `GET /partners/v1/partners` → `403
BAD_PERMISSIONS` on **both** organization credentials. Constant across two
credentials, which by the usual rule points at a missing authorization rule
rather than a per-credential grant — but partners is a Jamf-internal surface, so
an unentitled-by-design 403 is equally consistent. Its acceptance coverage
remains unexercised either way.

### Probed 2026-09-02: `RAMP` is accepted on write, and the connection body has a testable tell

One organization (`<org-c>`), controls in the same
invocation throughout. Three findings and one correction to this document.

**`RAMP` is in the server's *write* vocabulary too, not merely its read one.**
`POST /sso/v1/connections` answers `400 BAD_REQUEST "Unsupported region: ZZZZ"`
for a value outside the set, and `region: "RAMP"` does **not** get that answer —
it passes the region check and reaches the upstream call, byte-identically to
`US` and `EU` in the same run (all three `500 [UPSTREAM_ERROR]`, the bogus
`.invalid` domain being what stops them). So the omission is not a read-only
vocabulary the spec deliberately excludes from writes; it is simply missing.
Verified no connection was created: the collection stayed at 22 rows across all
five probes.

**The earlier "not patchable locally" verdict was wrong, and is worth recording
because it was reached for a good reason.** `schemaPatches` does address
properties by dotted path and `Region` is a bare top-level enum, so that key
genuinely cannot reach it — but the conclusion drawn from that ("fix belongs
upstream") treated the existing config keys as the limit of what was possible.
The generator gained `enumAdditions` instead: schema name → values to append,
panicking when the value is already declared so the entry expires the day
upstream publishes it. `RegionValues()` now returns five values and
`TestAcceptance_AccountSsoRegionEnumCoversTheWire` pins the enum against a live
organization's connections, so a sixth region fails a wire test rather than a
consumer's `terraform plan`. Still report it upstream.

**`connection: null` and `connection: {}` are distinguishable from a populated
settings object**, which is what makes the request body assertable without
creating anything:

| body | answer |
|---|---|
| `"connection": null` | `400 BAD_REQUEST "A connection requires a region"` |
| `"connection": {}` | `400 BAD_REQUEST "A connection requires a region"` |
| `"connection": {…, "region": "MARS"}` | `400 BAD_REQUEST "Unsupported region: MARS"` |
| `"connectionType": "NOPE"` | `400 MALFORMED_REQUEST_BODY` (Jackson subtype registry, ahead of everything) |

`TestAcceptance_AccountSsoConnectionBodyReachesTheServer` uses exactly that: an
OIDC variant carrying `region: "MARS"`, asserting the region check quotes the
value. A *"requires a region"* there means the union marshalled to null or an
empty object, so the test fails for the one reason it exists to catch. It is safe
by construction rather than by cleanup — region validation runs ahead of the
upstream call, so the request cannot create a connection.

That matters because until 2026-09-02 `ConnectionRequest.Connection` was `any`:
a discriminator-less `oneOf` reached through a property, which the generator had
no branch for. Nothing type-checked the create or update body, the four settings
schemas were emitted as Go types no signature referenced, and **no test in the
module had ever serialised one** — the generated stub calls `CreateConnection`
with a bare `&ConnectionRequest{}` and puts nothing on the wire. See
[STYLE.md](STYLE.md#schema-handling) for the fix and for the `any`-field guard
that now fails generation on the class.

**All 18 account privileges are corroborated three ways**, which is what
justified supplying them locally after the earlier decision not to:
the account specs' own source `config.yaml`
`requiredPrivileges`; the hand-written OPA rules in
the gateway's authorization policy for the `account` namespace; and
`TestScopedPrivilegesUseGAVocabulary`, which validated every identifier against
the published capability reference with no addition to `gaCapabilityActions`.
The `config.yaml` for each spec states the cause in its own closing comment:
these routes resolve the organization from the token, Tyk's
`request-context-allowed-sources` is `[token]`, so the beta scope-prefix
transform does not apply and **the build strips `x-required-privileges` from the
published artifact by construction**. Note the rego accepts *either* the GA
capability or a retired `read:org:*`/`update:org:*` permission
(`lib.has_any_of_permissions`); only the GA form is carried, because `Scoped` is
a conjunction.

**Unrelated read-back gap, re-confirmed on the wire.**
`GET /sso/v1/connections/{id}` returns no `enabledProducts` and no
`enabledEnvironments` (checked on the RAMP connection, which the collection
reports as enabled for `PRO`), and `ConnectionSummary.enabledApplications`
carries product *names* only, never tenant ids. So the part of a connection a
client writes cannot be read back, and drift detection for it is impossible.
Compounding it, no endpoint in this SDK lists an organization's tenants or
environments — `ListLicenses` is a commercial record with nothing joinable, and
`GetCsaTenantIdV1`/`GetM2MTenantIDV1` return the *caller's own* tenant and need
tenant or environment scope, which an organization-scoped integration does not
have. An organization-scoped tenant/environment listing is the missing
primitive. Report upstream.

### Re-probed 2026-09-01: three of the recorded faults have flipped, and two of them broke the SDK

Two organization credentials, two different organizations
(`<org-a>` and
`<org-c>`), a bogus-path 403 control in every run.
Read the older sections below as history, not as current behaviour.

| recorded fault | 2026-09-01 | consequence |
|---|---|---|
| `GET /sso/v1/connections` → 502 | **200** on both credentials | fixed upstream; the 502 branch is now a regression guard |
| list endpoints send a **bare array**, not `{totalCount, results}` | **all four send the envelope** the spec declares | **every account list method failed to decode.** Fixed: `responseType: "[]T"` → `unwrapResults: "[]T"`, which keeps the `([]T, error)` signature |
| `Domain.id`/`verifiedTldId` are **bare numbers** declared as strings | **quoted strings**, as declared | inert — `json.Number` accepts both forms, which is why it was the right target. Keep it |
| distributor `400 invalid_scope: skyway-use2-product` | `400 [UPSTREAM_ERROR] Failed to … via Skyway distributor service` | same surface still dead, new text; `isSkywayScopeFault` widened to match both |
| `Region` enum omits `RAMP` | **still true** | see below — now seen in *both* organizations |

**The envelope flip is the one that mattered, and nothing caught it.** Four
methods — `ListLicenses`, `ListDomains`, `ListConnections`,
`ListDealRegistrations` — returned
`json: cannot unmarshal object into Go value of type []account.License` and had
been doing so since the server changed. The generated httptest handlers assert
whatever shape config declares, so they passed throughout; only an acceptance run
against the wire could see it. **A `responseType` override that contradicts the
spec is a standing liability: it is right only until the server agrees with its
own spec, and nothing fails when that happens.** Prefer `unwrapResults`, which
follows the spec's envelope and still hands the caller a slice.

**That first fix was symmetric to the bug, and has since been replaced.**
Swapping `responseType: "[]T"` for `unwrapResults: "[]T"` moved the hardcoded
assumption from the array to the envelope; a flip back would have produced
`json: cannot unmarshal array into Go value of type struct{…}` on every call, the
identical outage in the other direction, with the identical absence of any test
signal. `unwrapResults` now generates a call to `client.UnwrapResults[T]`, which
decides the shape from the first non-space byte of the body and accepts both. The
six affected methods keep their `([]T, error)` signatures, so nothing downstream
changed — `terraform-provider-jamfplatform` calls `ListDeviceGroupMembers` from
five places and needed no edit.

No new silent path came with it: an envelope omitting the results key, `{}`, and
`null` already decoded to an empty slice under the struct decode, and the only
case that changed from error to empty is a bare `[]`, which means zero rows.

**The detector moved to the acceptance suite, which is the only layer that sees
the wire.** `assertListBodyShape` fetches a list endpoint outside the generated
methods and asserts the shape, so a future flip fails a wire test instead of a
consumer's `terraform plan`. Observed shapes:

| endpoint | shape | when |
|---|---|---|
| `GET /licensing/v1/licenses` | envelope | 2026-09-01, two organizations (array before) |
| `GET /partners/v1/deal-registrations` | envelope | 2026-09-01, two organizations (array before) |
| `GET /sso/v1/domains` | envelope | 2026-09-01, two organizations (array before) |
| `GET /sso/v1/connections` | envelope | 2026-09-01, two organizations (array before) |
| `GET /device-groups/v1/devices/{id}/device-groups` | envelope | **2026-09-02**, wire, `ListDevices` as the control in the same run |
| `GET /device-groups/v1/device-groups/{id}/members` | envelope | inferred, not probed — the struct decode it used until 2026-09-02 accepted nothing else, so every passing run of `TestAcceptance_DeviceGroup_ListMembers` is evidence. The assertion now dates it properly on the next run |

**Widening `isSkywayScopeFault` exposed a false pass.**
`TestAcceptance_AccountPurchaseOrderValidation` had been logging
"correctly rejected the bogus order (400)" for both distributor POSTs when the
400 was the Skyway fault, not a validation verdict — nothing had validated the
payload. It now fails, correctly.

**`RAMP` is a fifth `Region` the spec's enum omits**, and it is no longer a
single sighting: it appears on `ConnectionSummary.region` in **both**
organizations (1 of 5 and 1 of 22) and on `DomainAllocationConnection.authZeroRegion`
for an allocated domain on a non-production environment. `type Region = string`, so nothing mis-decodes,
but `RegionValues()` exists to feed `stringvalidator.OneOf` and would refuse a
region the server itself returns. **Now carried locally** through
`enumAdditions` — see the 2026-09-02 section below, which also records the
earlier "not patchable locally" verdict and why it was wrong.

**`POST /sso/v1/connections` cannot succeed.** Every well-formed body answers
`500 [UPSTREAM_ERROR] "The request could not be completed"` — probed across a
PENDING domain, a nonexistent domain, a VERIFIED domain, and a body omitting the
oneOf variant's own required members. Only the checks *ahead* of the upstream call
answer anything else, which also fixes the validation ordering: JSON parse
(`MALFORMED_REQUEST_BODY` for an unknown `connectionType` or `product` — Jackson's
subtype registry, the same tell as uem-connect) → top-level bean validation
(`FIELD_VALIDATION` on `connectionType`/`connection`/`domains`/`enabledProducts`)
→ `400 BAD_REQUEST "Unsupported region: MARS"` → upstream 500. **The oneOf
variant's fields are never validated.** So the spec's "every domain must already
be verified" is unobservable, and there is no create to test. Report upstream.

**Domains are the one complete CRUD surface, and delete is a hard delete.**
Full lifecycle wire-verified: `POST /sso/v1/domains` → **201** with a full
`Domain` body and no `Location` header; the row appears in the collection;
`GET /sso/v1/domains/allocation/{domain}` → 200 `{connections: [], jamfIdEnabled:
true}`; `DELETE` → **204**; then the allocation 404s, a second `DELETE` 404s (so
**delete is not idempotent**) and the collection is back to its prior count.
Unlike Security Cloud enrollment's soft delete, nothing is left behind. A
duplicate create is `409 CONFLICT "Domain is already added to your organization"`,
and **domain matching is case-insensitive** — `JAMFSDKPROBE-…CLICK` collided with
the lower-cased row, confirming the spec's "lower-cased on storage".
`.invalid` domains are accepted; `"not a domain"` is `400 BAD_REQUEST
"Invalid domain provided"`.

**Verification is rate-limited from `lastModifiedDate`, and `CreateDomain` sets
it — so create-then-verify can never reach a verification verdict.**
`POST /sso/v1/domains/{id}/actions/verify` answered
`400 BAD_REQUEST "Can only verify once every five minutes"` on a domain that had
never been verified, both immediately after the create and again 3m24s later.
`TestAcceptance_AccountDomainLifecycle` now reports that case separately instead
of logging it as a correct rejection. One unexplained observation alongside it:
`lastModifiedDate` and `verificationExpirationDate` advanced 37 seconds after the
create with only GETs in between, which pushes the rate-limit window out further;
cause not identified.

**The rest of the read surface is faithful.** `Connection` matched the wire
exactly across all five connections in one organization and all four
`connectionType` variants (`WAAD`, `OKTA`, `OIDC`, `GOOGLE_APPS`): zero wire-only
keys, zero spec-only keys, at top level and through every nested
`azureOptions`/`extOptions`/`googleOptions`/`oidcOptions`/`oktaOptions`/`sessionInfo`.
Item-level 404s are coherent too — `NOT_FOUND` for a bogus connection id, domain
id and allocation domain alike. `License.assetId` is **nullable** (8 null rows in
one organization), so it is not a dedupe key.

### The 2026-08-27 baseline

Wire-verified 2026-08-27 with a real organization credential, URLs exactly as
generated. **Note the `/api` prefix in this table is historical** — it now
answers `404 page not found`:

| endpoint | result |
|---|---|
| `GET /api/licensing/v1/licenses` | 200 — 16 real licence rows |
| `GET /api/sso/v1/domains` | 200 — 5 real domain rows |
| `GET /api/partners/v1/deal-registrations` | 200 `[]` |
| `GET /api/sso/v1/connections` | **502** `An upstream service returned an error` |

Three things only the wire could tell us:

- **US only.** The `account` tyk product ships *only* `use1` api-definitions in
  every environment — no euc1, no apne1. An EU credential cannot reach these and
  the failure will not look like a region problem. Use `https://us.api.jamfcloud.com`.
- **The list endpoints return bare JSON arrays, not the spec's
  `{totalCount, results}` envelope.** All four are declared as envelopes with both
  members `required`; the server sends `[…]`. Following the spec produced methods
  that fail every decode. Fixed with `"responseType": "[]License"` etc.
- **`Domain.id` and `Domain.verifiedTldId` are JSON numbers declared as strings.**
  The spec's own description is self-aware — *"Treat it as an opaque string, even
  though it is currently a decimal number"* — but it declares `type: string` and
  the server sends `1552`, unquoted. `json.Number` is the honest target: it
  decodes the number, preserves the text, and converts to the string the
  `domainId` path params want.

**A sweep that skips null values will miss these, and did.** The first pass
reported `Domain.id` as the only mismatch because it compared only non-null
values and `verifiedTldId` was null on all five sampled domains. A larger
organization populated it and the decode failed on row 31. **Treat "no
mismatches" from a small sample as unproven, not as clearance.** The reliable
predictor was in the spec: a `type: string` field whose `example` is a bare
decimal. Those two are the only such fields across the three account specs —
every other string-typed `*Id` carries a UUID, a Salesforce key, an `org_` handle
or a hex trace ID.

**The whole distributor surface is blocked by a half-finished Skyway scope
migration.** `GET /api/partners/v1/distributor/configuration` answers `400` with
an OAuth body on an API path: `{"error":"invalid_scope","error_description":
"Invalid scopes: skyway-use2-product"}`. The partners backend calls Skyway and in
**prod** asks for a scope that exists only in **dev**; prod declares
`skyway-use1-product` and, since a gateway API-definition change, a
region-independent `skyway-product` added for exactly this purpose. Nothing to fix
in the SDK — the URL is confirmed correct, every non-distributor endpoint on the
same credential returns 200, and the gateway's account policy carries the full
distributor surface, so authorization passes. **Note the tell: an `invalid_scope` OAuth error
arriving on a resource path means a *backend* service failed its own token
exchange, not that the caller's credential is wrong.** `isSkywayScopeFault`
matches on the scope name rather than the status code, because a 400 from these
endpoints could equally be a genuine validation verdict.

The 502 on `/sso/v1/connections` is an upstream fault, but note its body is the
account service's own envelope (`{classification, fields, message}`) — a fourth
dialect none of the shapes under [Error handling](#error-dialects) cover.
`Details()`/`FieldErrors()` parse nothing from it. It is also retryable, so a
client with the default policy sits on it until the context deadline.

**No `x-required-privileges` anywhere in these specs** — 0 privileged ops in every
variant, and no `-beta` variant to take them from. So `account/permissions.go` is a
registry of `Scoped: nil`. That is honest reporting, not a gap.

### Held back from v1872

Two breaking changes in the account specs are **ahead of the server** and were not
ingested. Probed 2026-08-29 with the US organization credential, control in the
same invocation:

| probe | result |
|---|---|
| `GET /sso/v1/domains/allocation/{domain}` × all 5 | 200 — **`authZeroRegion` on 5/5, `authRegion` on 0/5** |
| `GET /licensing/v1/licenses` | 200, 16 rows — **`type` populated on 16/16** |

**Re-probed 2026-09-01 across two organizations — both holds stand.**
`License.type` is non-null on **261/261** rows (16 + 245) with 14 distinct values;
`licenseType` is a separate field, non-null on only 8/16 and 711/735, so it is not
the successor. `authZeroRegion` is the wire name on **12/12** connection objects
across 17 domains in both organizations; `authRegion` appears nowhere. Keep
`account-licensing` and `account-sso` at v1865.

So `DomainAllocationConnection.authZeroRegion` → `authRegion` and the removal of
`License.type` would both be **silent** regressions: nothing sets
`DisallowUnknownFields`, so a renamed field the server does not send decodes to
nil and a removed field the server still populates simply becomes unreachable.
Neither fails a test.

**`License.type` is not recoverable by a single substitution when it does land.**
It mixes two taxonomies: on the 5 BETA/NFR rows it duplicates `licenseType`
exactly; on the other 11 it names a product while `licenseType` is `NFR` or null.
Nothing loses its only identification, but there is no one rule — the Jamf Trust
row is `type: JAMF_SECURITY_CLOUD` with `addOnType: JAMF_TRUST`,
`productTopLine: TRUST` and `productParent: SECURITY_CLOUD`, all four different.

**Report upstream: the `Region` enum is incomplete.** It declares `[US, EU, AU, JP]`
and the wire returned **`RAMP`**. Nothing breaks today because `Region` is a
`string` alias, but `RegionValues()` exists to feed `stringvalidator.OneOf` and
would refuse a region the server itself returns. Re-confirmed 2026-09-01 in a
*second* organization, and 2026-09-02 shown to be accepted on **write** as well —
carried locally through `enumAdditions` since then. The "cannot be patched
locally" verdict this note used to end on is superseded; see the 2026-09-02
probe at the top of this section.

---

## AI Governance (`aigovernance`) — environment scope

### v2121's optimistic-concurrency mechanism is live, and the SDK cannot reach half of it (2026-09-09)

v2121 adds an ETag/If-Match concurrency protocol to the policies API:
`PolicyDetail.version` (nullable `int64`), an `ETag` response header on the
detail `GET`, an `If-Match` request header on `PATCH`, and a `409` on that
`PATCH`. Every part of it is enforced on the wire — probed on the EU
environment `<jsc-tenant-b>` with `GET /v1/policies` at 200 as the control in the
same invocation.

**The read half only works for documents created after the rollout, exactly as
the spec says.** Both pre-existing policies on that environment answer
`"version": null` and send **no** `ETag` header at all. A policy created during
the probe answered:

```
$ curl -D - .../ai/governance/policies/v1/policies/b53d619a-…
HTTP/2 200
etag: "0"
jamf-preview: true
…  "version": 0
```

So `version: null` is not "the field is unpopulated" — it is a legacy document
with no ETag to supply, and a caller cannot make a conditional update to one.

**The write half, on that same new policy.** Re-probed with a second policy to
settle the validator forms, which the first pass had left ambiguous:

| `If-Match` | status | body |
|---|---|---|
| `3` (bare, current) | **204** | version → 4 |
| `"3"` (strong, current) | **204** | version increments |
| `W/"3"` (weak, current) | **204** | version increments |
| `"1", "2"` (list) | **400** | `VALIDATION_FAILED` / "Invalid request" |

**The server compares the number and tolerates all three validator forms**, so a
caller needs no ETag quoting: `strconv.FormatInt(*policy.Version, 10)` is a
valid precondition. That is what makes a plain `string` argument safe, and
`TestAcceptance_AiGovernancePolicyLifecycle/If-Match_makes_the_update_conditional`
asserts all three so the SDK notices if the tolerance narrows. Only a
comma-separated list — legal HTTP — is refused.

**And the stale cases:**

| `If-Match` | status | body |
|---|---|---|
| `"999"` | **409** | `POLICY_VERSION_CONFLICT` — "Policy was modified by another request; reload and retry with the current version" |
| `W/"999"` | **409** | same. Ambiguous on its own — a rejected form and a stale one look alike here, which is why the table above re-probed `W/"3"` against the *current* version |
| `"0"` (current) | **204** | version → 1, `ETag: "1"` on the next `GET` |
| `*` | **204** | unconditional; version → 2 |
| absent | **204** | unconditional; version → 3 |
| `garbage` | **400** | `VALIDATION_FAILED` / "Invalid request" — the header is parsed, not ignored |

**Every `PATCH` increments `version`, whether or not it changes anything** —
1 → 2 → 3 above were all the same stored body. Publishing does not touch it, so
`version` and `currentVersionNumber` are independent counters on the same
document.

**Validation ordering on `PATCH` is body → If-Match syntax → resource
resolution → precondition.** A bogus `policyId` with an invalid body answers 400
`VALIDATION_FAILED` naming `schemaVersion`/`settings`; the same bogus id with a
*valid* body answers 404 `POLICY_NOT_FOUND` for every syntactically valid
`If-Match`, and 400 for `garbage`. So the field constraints are probeable
without a policy, and only the 409 needs a real one.

**~~The SDK cannot send `If-Match`.~~ — implemented 2026-09-09.**
`tools/generate` had no concept of `in: header` parameters: it never inspected
a parameter's `In`, so a `params` entry for `If-Match` would have matched the
spec, passed every guard and been emitted as a **query** key the server
ignores — turning a conditional update into an unconditional one with nothing
failing anywhere. `WithHeaders` was no help either, being per-client while the
value changes per request.

It now has a `headerParams` config key, `UpdatePolicy` takes an `ifMatch`
string, and the request routes through `Transport.DoWithOptions`. Mechanism,
the four refusals, and why the key is separate from `params`:
[STYLE.md](STYLE.md#header-parameters). Three other latent header parameters
came with it — `Accept` on export-report and `Accept-Language` on both
account-preferences operations, recorded above.

`Jamf-Preview: true` is now declared on every 2xx of all 12 operations and is
present on the wire. `ApiError.httpStatus` is declared required at v2121 and the
wire has always sent it — every error body quoted in this section carries it.

The probe policy was deleted (`DELETE` 204, `GET` 404 after) and the list
re-read at the original two rows.


**The published path was wrong in every variant and the gateway was the
authority — ~~fixed upstream at v1877~~.** The spec declared
`servers: …/api/ai-governance/policies` while the tyk listen_path is
`/api/ai/governance/policies`, with slashes: the generated form returned 200 with
real data and the spec's hyphenated form `404 page not found`. Reported upstream
and corrected in v1877 (`external/ai-governance`, and `_permissions/routes.yaml`'s
domain key with it) — the whole build was that one line. **Nothing in the SDK
changed**, because the transport never reads `servers`; the only generated diff
was the URL string inside the published `api/ai_governance_policies_api.json`.
The lesson stands even though the instance is closed: the gateway is the
authority, and a `servers` block is not evidence.

**The package is `aigovernance`, not `aigovernancepolicies`, and the gateway
settles that too:** `…/ai/governance/visibility/v1/policies` answers **403**
(routed namespace, no policy rule) while the bare `…/ai/governance/v1/policies`
answers **404 page not found**. Those two answers distinguish "routed but
ungranted" from "no such namespace", and they are the basis for expecting
Visibility to land as a second spec in this package. It still has no spec.

`X-Environment-Id` is `required: true`. **A malformed value is a 500, not a 400**:
a well-formed unknown UUID correctly gives `404 ENVIRONMENT_NOT_FOUND`,
`not-a-uuid` gives `500`, and sending `X-Tenant-Id` as well makes the tenant
header win and the request fail `403 OWNERSHIP_FORBIDDEN`.

### Three real defects (full-surface probe, 2026-08-30)

1. **`GET /v1/policies/{id}/deployment` is non-functional — it always returns
   `{"blueprints":[]}`.** Confirmed three ways, including a blueprint created for
   the probe referencing a freshly published policy. The blueprints service *does*
   resolve the link — referencing a nonexistent policy version is refused
   `400 POLICY_VERSION_NOT_FOUND` on
   `steps[0].components[0].configuration.policies[0].policyId` — so the reference
   exists and the AI-governance side is not reading it. This is the endpoint's
   entire purpose. `TestAcceptance_AiGovernanceDeploymentReportsReferencingBlueprints`
   cross-references the two APIs off existing state and **fails when the endpoint
   starts working**. Do not weaken it to a skip: an empty 200 is exactly the kind
   of answer that goes unnoticed.
2. **Archiving a blueprint-referenced policy succeeds (204)** and leaves the
   blueprint pointing at a policy nothing can read. No referential guard — the
   opposite of Security Cloud's `*_REFERENCED_BY_*` 409s.
3. **Archive is not a readable soft delete.** The spec calls it a soft delete
   retaining published versions "for audit trail integrity". On the wire the
   parent becomes **unreachable** — `GET`, `PATCH`, `publish` and a second
   `DELETE` all 404, and the policy leaves the list — while `/versions`,
   `/versions/{n}` and `/deployment` keep answering 200. So the parent 404s and
   its children 200, `status: ARCHIVED` is a value **no caller can ever read**
   (making `PolicySummaryStatusArchived` unobservable), the audit trail is
   reachable only via an id the caller must already hold, and `DELETE` is not
   idempotent. Every create therefore leaves a permanent row.

### Behaviours a consumer must know, neither in the spec

- **`PATCH` replaces `settings` wholesale — it does not merge.** A patch carrying
  only `permissions.defaultMode` wiped the stored `permissions.allow`/`deny` and
  `env`. Load-bearing for any read-modify-write caller.
- **`hasDraft` is a diff against the published version's `settings`, not a record
  that an update happened.** Publish `{}`, patch a key in → true; patch back to
  `{}` → **false again**, though both PATCHes returned 204 and both bumped the
  write counter. A reconciler that patches then publishes only when `hasDraft` is
  true will silently skip, and calling publish regardless is
  `409 NO_DRAFT_TO_PUBLISH` rather than a no-op.
- **Publishing does not deploy anything.** `POST /publish` freezes the draft into
  an immutable version row and nothing more; a policy reaches a device only by
  being referenced from a blueprint's `com.jamf.ai-governance` component *and*
  that blueprint being deployed. A separate `JAMFPLATFORM_AIGOV_PUBLISH_OK` gate,
  justified as "publishing DEPLOYS an AI policy", was therefore protecting against
  nothing and kept the publish path unexercised; it is folded into
  `JAMFPLATFORM_AIGOV_WRITE_OK`.
- Unknown `settings` keys are **accepted** (the vendor schemas do not set
  `additionalProperties: false` at the root) and persist into the published version.

### Params and errors

- **`schema-drift` is a switch, not a boolean filter.** `true` narrows to drifted
  policies; `false` returns **everything**, drifted included. So the only relation
  a test can assert is subset. `schema-drift=banana` is silently treated as false
  while `page=-1` correctly 400s.
- **Only the first `sort` criterion is validated or applied.** `sort=bogus:asc`
  alone is a 400, but `sort=name:asc,bogus:desc` is 200. `sort=name:sideways` is
  accepted too. The SDK comma-joins the slice, which is not the spec's
  `explode: true` form but is immaterial given only element zero is read.
- Errors report `field: "pageSize"` — the internal camelCase name, not the wire
  key `page-size`.
- **Status contract, worth relying on:** `400 VALIDATION_FAILED` for a missing or
  blank required member (one detail per field), `422` for a semantic failure
  (`TOOL_ID_UNKNOWN`, `SCHEMA_VERSION_UNKNOWN`, `SCHEMA_VALIDATION_FAILED`),
  `409 NO_DRAFT_TO_PUBLISH`, `404 POLICY_NOT_FOUND`/`VERSION_NOT_FOUND`.
  `GET /v1/tools/{id}/schemas/{ver}` answers **422** for an unknown version
  although the operation declares only 200/401/403/404. A non-UUID `policyId` is a
  **404**, not a 400, so status cannot detect a malformed id.
- `SCHEMA_VALIDATION_FAILED` sets `field` to a **JSON Pointer relative to
  `settings`** (`/permissions/defaultMode`), not the documented dot-path. A blank
  `name` yields the raw Bean Validation regex `must match ".*\S.*"`.
- `PolicyDetail` carries an **undeclared `version` integer** — 0 on create,
  incrementing on every write, `null` on UI-created rows. A write counter,
  unreachable from Go, probably meant to be internal.
- Two 401 dialects: the gateway's plain-text `Authentication failed` for a bad
  token, versus JSON `{"httpStatus":401,"message":"unauthorized access"}` for no
  header at all — the latter carrying no `errors` array, so `Details()` parses
  nothing.
- Actor IDs leak the internal issuer
  an internal Jamf M2M realm URL, and the blueprints create
  `href` leaks an internal gateway service hostname.

---

## Audit (`audit`) — environment scope, blocked on a grant

Generated, compiled and typed correctly; **every call is refused**, and the SDK is
not at fault.

A prod gateway API-definition change (2026-08-28, *"TRIVIAL Audit Service is
Environment scoped only"*, all three prod regions plus dev and stage) changed
`platform-audit-service`:

| key | before | after |
|---|---|---|
| `request-context-allowed-sources` | `[token, path]` | **`[token, path, header]`** |
| `request-context-types` | `[environment, organization]` | **`[environment]`** |

That fixed both halves of the original diagnosis. The original 400 came from the
gateway's request-context resolution: an external-M2M token carries no tenant,
environment or organization claim, so the resolver falls through to a server-side
organization lookup — but only when the route declares exactly one request-context
type and that type is `organization`. licensing/partners/sso declare exactly
`[organization]` and work; audit declared two entries, so the
fallback never fired.

Re-probed 2026-08-29 on **two separate** environment credentials, control
(`GET /blueprints/v1/blueprints` → 200) in the same invocation:

| credential | request | result |
|---|---|---|
| environment (eu) | `GET /audit/v1/audit` + `X-Environment-Id` | **403 BAD_PERMISSIONS** |
| environment (eu) | `GET /audit/v1/audit/sources` + `X-Environment-Id` | **403** |
| organization (us) | `GET /audit/v1/audit`, no header | `400 REQUEST_CONTEXT_NOT_PROVIDED` |
| organization (us) | `GET /audit/v1/audit` + `X-Organization-Id` | `400 INVALID_REQUEST_CONTEXT_TYPE` |

**The gateway now says what it wants, in as many words:** *"Request context type
'organization' is invalid. Expected any of 'environment'."* So the context half is
fixed and the remaining 403 is pure authorization. The audit policy (20
lines after the 2026-08-28 *"TRIVIAL Remove organization scoping from Audit"* change, same
day as the tyk change) confirms it independently: environment-only,
`environmentId != ""`, gated on `read:env:audit` or `audit:read`. It also confirms
the path prefix is `/v1/audit/…` — an earlier probe here guessed `/v1/events`.

**What audit needs is an environment-scoped credential granted `audit:read`.** No
SDK change, no gateway change. Two separate credentials now lack it, making it a
provisioning gap rather than a quirk of one integration — and since the token is
opaque with an empty `scope`, no caller can confirm this from the API at all.

### Re-probed 2026-09-01 on a third credential, with live controls — still ungranted

The strongest version of this diagnosis so far, because the credential is
demonstrably granted plenty else in the *same invocation*:

| credential / environment | request | result |
|---|---|---|
| environment (eu), own env `<jsc-tenant-b>` | `GET /devices/v1/devices` | **200** — real devices |
| environment (eu), own env `<jsc-tenant-b>` | `GET /blueprints/v1/blueprints` | **200** |
| environment (eu), own env `<jsc-tenant-b>` | `GET /ai/governance/policies/v1/policies` | **200** — 2 policies |
| environment (eu), own env `<jsc-tenant-b>` | `GET /audit/v1/bogus-xyz` | 403 `BAD_PERMISSIONS` |
| environment (eu), own env `<jsc-tenant-b>` | `GET /audit/v1/audit`, `/audit/v1/audit/sources`, `/audit/v1/audit/transactions/{bogus}`, `/audit/v1/audit/resources/{bogus}/lineage` | **403 `BAD_PERMISSIONS`** — all four |
| organization (us) cred 2, own env `<env-a>` | `GET /audit/v1/audit`, `/audit/v1/audit/sources` | **403 `BAD_PERMISSIONS`** |

**The audit namespace is mounted** — a bogus path under it answers 403, while a
nonexistent namespace answers `404 page not found`, which is the routed/unrouted
distinction. **All four generated operations are refused identically, and
identically to a path that does not exist**, so status alone cannot tell a real
audit path from a typo.

So it is now three credentials across two organizations and two regions, each
using an environment it owns, all 403. Since the audit policy *is*
authored (gated on `read:env:audit` / `audit:read`), the missing piece remains the
capability grant on the credential, not the rule.

**Re-checked 2026-09-02 across both repos: nothing has moved, and one plausible
hypothesis is foreclosed.** Only two changes have touched the prod audit-service
definition since 2026-08-28 — one on 2026-08-28 ("Audit Service is Environment
scoped only", which added `header` to `request-context-allowed-sources` and
**removed `organization`** from `request-context-types`) and one on 2026-09-01 (the
plugin-bundle bump that hit every prod product) — and neither changes
grantability. On the OPA side the audit policy is unchanged since
that 2026-08-28 change, which deleted the organization `allow` block two minutes
before the matching tyk change; the surviving block accepts an `em2m` subject
with a non-empty `environmentId` and `environmentPermissions` holding
`read:env:audit` or `audit:read`. So both halves independently say
environment-only, and both say the rule is not the blocker.

**The foreclosed hypothesis: plan membership.**
`platform-audit-service-external-*` is absent from
`prod/plans/default-external.yaml` while the three securitycloud external APIs
are present — which looks like the cause and is not. 99 of the 201
`external-*`-tagged prod ApiDefinitions are absent from that plan, including
`jamf-pro-external-api-use1` and `jamf-pro-external-capi-use1`, which demonstrably
work for external M2M. Plan membership is not the gate for a
`use_go_plugin_auth` API. Do not spend another pass on it.

### The gateway's refusal layers, fully separated (2026-09-01)

Established while chasing the above; each code names exactly one layer, which is
what makes a refusal diagnosable without two credentials:

| answer | layer |
|---|---|
| `401` plain text | no or invalid token |
| `404 page not found` | namespace not mounted (or an `/api` prefix) |
| `404 ENVIRONMENT_NOT_FOUND` | the environment does not exist — **fires even on a bogus path inside a mounted namespace**, so it precedes OPA |
| `403 OWNERSHIP_FORBIDDEN` | the environment exists but the credential's organization does not own it |
| `403 BAD_PERMISSIONS` | OPA deny. Also what a **bogus path inside a routed namespace** returns, which makes it the cheap control |
| `400 REQUEST_CONTEXT_NOT_PROVIDED` | routed and granted, scope header absent |
| `404 TENANT_NOT_FOUND` | environment exists and is owned, but has no tenant behind it |
| `502 service_unavailable` | past auth *and* OPA; upstream unreachable — **evidence of a grant, not of a missing one** |

Two consequences worth keeping:

- **Ownership is checked before capability**, so a foreign `X-Environment-Id`
  masks whether the credential holds the grant. A 403 cannot be classified until
  the scope is known to belong to the credential — which is how `<env-b>` and
  `<env-a>` were first misread.
- **Report upstream: the ordering is not uniform across namespaces, and two of
  them leak environment existence.** With a credential owning *none* of the
  environments below, `audit` refuses on ownership while `blueprints` and
  `devices` answer past it and disclose the environment's tenant state:

  | environment | `audit` | `blueprints` / `devices` |
  |---|---|---|
  | `<env-a>` (exists, unowned, no tenant) | `403 OWNERSHIP_FORBIDDEN` | `404 TENANT_NOT_FOUND` |
  | `<env-b>`, all-zero UUID (do not exist) | `404 ENVIRONMENT_NOT_FOUND` | `404 ENVIRONMENT_NOT_FOUND` |

  So via blueprints or devices an unauthorized caller can distinguish "this
  environment ID exists" from "it does not", and learn whether it has a tenant,
  for an ID it has no rights to. `audit` demonstrates the correct behaviour.
  Low severity — environment IDs are UUIDs, so this confirms a known or guessed
  ID rather than enabling enumeration — but it is a real disclosure and the
  inconsistency is itself a defect.

**The path-scoping workaround this file once contemplated is confirmed
unnecessary. Do not add it.**

**Report upstream:** the audit spec's prose still says *"`audit:read` (environment
scope … or organization scope, when X-Environment-Id header not present)"*. The
organization half is dead as of 2026-08-28, and `scopes.yaml` in the same bundle
still lists `audit` under `organization` — two published artefacts advertising a
scope the gateway rejects.

`AuditEnvelope` is the SDK's first discriminator-less `oneOf` union. The spec says
it is discriminated *structurally* — a gateway event carrying
`actor` + `requestContext`, a service event carrying `data`, never mixed — so the
merged struct has the six shared base fields required and those three as pointers:
nil `Actor` means service event, nil `Data` means gateway event. **No discriminator
is synthesised**, deliberately: the nearest candidate is `auditSource`, an open
string whose own description says "e.g. `api-gateway`, `blueprints`, `ai-policy`",
so any mapping would rot the first time a new source appears.

---

## Error dialects

Per-family behaviour of `Details()` / `FieldErrors()` / `Summary()`. The probes
validating this are in `acc_api_errors_test.go`.

| family | structured details | `Field` populated |
|---|---|---|
| Pro (JSON) | yes | **yes** — `name`, `id`, … |
| Devices / DeviceGroups / Blueprints / DeviceActions | yes | usually empty, so everything buckets under `""` |
| Compliance Benchmarks 404s | empty body or `errors: []` | n/a — `Summary` falls back to status text |
| Classic | **no** — Tomcat HTML error page, preserved in `Body` | n/a; do not HTML-scrape in the transport |
| DDM Report | n/a — returns an empty report for unknown devices rather than an error | n/a |

**Security Cloud is not one dialect.** DNS, ZTNA and UEM Connect return the
standard `{httpStatus, traceId, errors[]}`, DNS being the only one that populates
`field`/`id` (as nulls). Activation profiles return the same shape minus
`traceId`. Device groups return a different envelope entirely —
`{message, messageKey, messageParams, error, logref, statusCode}` — which parses
to nothing. **The status code is the only thing every Security Cloud service
populates, so branch on `HasStatus`, not on details.**

Rule for consumers doing field-attributed diagnostics (Terraform
`AddAttributeError`, CLI per-field output): iterate `FieldErrors()` and fall
through to a generic diagnostic when the field key is empty. That works across
every family.

---

## Rate limiting

**No Jamf path rate-limits as of 2026-08-31. This is a fact with an expiry date
— rate limiting is planned — so treat it as a snapshot, not a standing
property, and re-probe before relying on it.**

Wire evidence, `us` prod, one `pro` GET plus a 30-request burst shaped like
Terraform's default parallelism (3 rounds x 10 concurrent, no client pacing):
all 30 returned 200, and every response carried

```
x-ratelimit-limit: 0
x-ratelimit-remaining: 0
x-ratelimit-reset: 0
```

Tyk reports the limit as `0` when rate limiting is disabled for the key. **No
response carried a `Retry-After` header.**

Corroborated in the gateway's API definitions: both prod plans carry `rate: -1`,
`per: -1`, `quota_max: -1`. The only global rate limit anywhere in prod is on the
m2m Auth0 client-registration path (`rate: 2, per: 3`), which the SDK never
calls. `Retry-After` appears nowhere in the gateway config or in its plugins.

Two consequences for the transport:

- **429 is currently unreachable on SDK paths**, so `jamfBackoff`'s
  `Retry-After` branch is untested against the real gateway. It is written to
  be correct when rate limiting arrives rather than tuned to the present
  absence — `retryablehttp.DefaultBackoff` supplies the parsing, and the SDK
  adds only a clamp to `retryWaitMax`.
- **When rate limiting does land, the shape matters.** If it emits
  `Retry-After`, the transport already honors it and nothing needs changing. If
  it does not, the exponential curve is the only backpressure response, and
  jittering it becomes worth adding — retries are routinely concurrent and a
  deterministic curve makes requests that failed together collide again. Worth
  asking the gateway team to emit `Retry-After` on 429 and to populate the
  `x-ratelimit-*` headers that currently return `0`; that removes the guessing
  entirely.

### Tyk enforced timeouts on smart-group writes

Not rate limiting, but found in the same config pass and shares the
retry-amplification concern. `prod/api-products/pro/api-definitions-*.yaml`
sets `hard_timeouts` on exactly two operations, in all three regions:

```yaml
hard_timeouts:
- method: POST
  path: "/v2/computer-groups/smart-groups"
  timeout: 180
- method: PUT
  path: "/v2/computer-groups/smart-groups/{id}"
  timeout: 180
```

These are *raised* ceilings — someone hit Tyk's default enforced timeout on
smart-group writes and pushed it to 180s, which is itself evidence the endpoint
struggles.

The PUT is in the transport's retryable set (idempotent method, see
`isRetryableWriteStatus`) and the POST is not. Retrying the PUT is **safe** —
`UpdateSmartComputerGroupV2`/`V3` carry no `VersionLock`, `If-Match` or ETag, so
there is no stale precondition to replay and the final state converges. But it
is expensive: the method expects `202 Accepted`, so the write is asynchronous
and a Tyk timeout means Tyk stopped waiting, not that the upstream declined the
work. A retry re-queues a recomputation that is probably still running, and
because each attempt can burn 180s of upstream work before Tyk cuts it, an
exhausted sequence is ~15 minutes of wall clock.

**This is the one endpoint the 2026-08-31 backoff change does not help.** That
change shortened the *waits* (1+2+4+8 = 15s); it did not shorten the attempts.
Not flagged `noRetry`, because the retry is correct and a genuine transient 504
on this path is worth recovering — and the transport cannot tell an enforced-timeout
504 from a transient one by status alone. Recorded so the latency is not a
surprise; revisit if the deterministic-timeout case turns out to be the common
one.

---

## Transport details established by probing

- **Credentials in the base URL's userinfo (`https://user:pass@host/path`) never
  reach the wire.** `net/http` applies `URL.User` as Basic only when
  `Authorization` is empty, and both the token exchange and API calls already
  carry one. Inline userinfo is reported to work against other Jamf SDKs; here it
  is silently dropped with no error. `TestURLUserinfoIsNotSentAsBasicAuth`
  documents it.
- **A caller-supplied `Authorization` flips the token exchange to
  `AuthStyleInParams`**, because the caller's header takes the slot the client
  credential would use. Wire-verified 2026-08-26 that
  `us.apigw.jamf.com/auth/token` accepts body-form client credentials — a full
  proxied round trip returned `11.31.1`. Without the flip, x/oauth2's
  auto-detection still gets there but only after a rejected header-style attempt
  per fetch.
- **Relocation matches on the `Bearer ` scheme, not the request phase.** Both
  phases put something in `Authorization`: `oauth2.Transport` writes
  `Bearer <token>` on API calls, `clientcredentials` writes `Basic <id:secret>` on
  the token request. Moving the latter sends the client credential to a header the
  token endpoint does not read — authentication fails while the relocation looks
  like it worked.
- **A base URL carrying a path prefix cannot work against Jamf's gateways, and the
  failure lands on authentication.** `TokenURL` is `baseURL + "/auth/token"` with
  no normalisation, so `https://host/api` sends the exchange to `/api/auth/token`
  → 404, and the call fails during auth rather than on the request the caller
  made. Verified across all four combinations of {new, retired} host × {bare,
  `/api`-suffixed}. Prefixes *do* work against a caller's own reverse proxy that
  mounts both the token endpoint and the namespaces beneath it — `fakeProxy` in
  `internal/client/headers_test.go` exercises exactly that. This is why
  `annotateTokenError` special-cases **404** on the token exchange and names the
  base URL rather than the WAF/IP-allowlist cause it reports for other statuses.
- **A wrong base URL presents as a total 404 with working authentication**, which
  reads as a routing regression in the SDK rather than a config error: `/auth/token`
  sits at the root on both the GA and the retired host, so the exchange still
  succeeds while every API call 404s. This is the symptom every consumer will report
  at GA, since all of them have to change the host.
- **Tokens are portable across both gateway hosts** and `/auth/token` sits at the
  root on both; `{base}/api/auth/token` is 404 on both.
- **Region isolation is real despite one shared CloudFront distribution.** All
  three regional names resolve to `d2jmnb3kwds4a0`; routing is by Host header. An
  EU credential gets 200 on `eu.` and **`401 Authentication failed`** on `us.` and
  `apac.` — the same body a wrong secret gives, so a wrong-region base URL is
  indistinguishable from a bad credential from the error alone.
- **One credential set reaches one product.** A Security Cloud client answers 403
  on `/api/pro` and a Jamf Pro client answers 403 on `/api/securitycloud`. A single
  `Client` cannot span products however many tenant IDs it holds — hence no
  per-namespace tenant override, and in Terraform a provider alias per credential.
- **Scope headers must match the credential.** An environment credential sending
  `X-Tenant-Id` gets `403 OWNERSHIP_FORBIDDEN` / *"Tenant 'x' is not part of your
  organization"*, and a tenant credential sending `X-Environment-Id` gets the
  mirror image, even when both IDs belong to the same customer.
  `TestAcceptance_EnvironmentScopeMismatch` asserts **both** directions — the
  mismatch is the half worth testing, because it is what stops a consumer treating
  the options as interchangeable spellings.
- Environment scope reaches `blueprints`, `devices`, `pro`, `proclassic`,
  `securitycloud`, `compliance-benchmarks` and `ai-governance`; read-only probes
  returned real data from all of them on 2026-08-25. `audit` is the **one** spec
  this SDK generates that declares `X-Environment-Id` on every operation; every
  other declares `X-Tenant-Id`.
- **`request-context-types` absent means unrestricted, not tenant-only.** It is
  absent for jamf-pro, securitycloud and blueprints, and present only on `account`
  (`[organization]`) and `audit` (`[environment]`). Two traps while establishing
  that: `prod/api-products/pro/` is the **`/ui/jamfpro/`** definition and has no
  `header` source — the external one is
  `prod/api-products/jamf-pro/api-definitions-external-*.yaml`, found by grepping
  for `listen_path: /api/pro/`, not by directory name.

### v2056 withdrew organization scope from the spec, and the wire agrees (2026-09-03)

The spec has caught up with the gateway. `x-scope-types` goes
`[environment, organization]` → `[environment]`, `X-Environment-Id` flips
`required: false` → `true` and loses the sentence offering the header-less
organization form, and `_permissions` agrees on both halves: `routes.yaml` drops
the four `audit:read` routes typed `organization`, and `scopes.yaml` drops the
whole `organization` block, leaving `environment` as its only top-level key.

The probe that matters is the **control**, because "an organization credential
is refused" has two explanations and only one of them is about audit:

| credential | request | result |
|---|---|---|
| organization (`<org-a>`, us), no scope header | `GET /licensing/v1/licenses` | **200** — 16 real licence rows |
| organization (`<org-a>`, us), no scope header | `GET /audit/v1/audit/sources` | **400** `REQUEST_CONTEXT_NOT_PROVIDED` |
| environment (`<jsc-tenant-b>`, eu), `X-Environment-Id` | `GET /audit/v1/audit/sources` | **200** — `api-gateway`, `blueprints`, `ai-policy`, all `hasEvents: true` |
| environment (`<jsc-tenant-b>`, eu), **no** scope header | `GET /audit/v1/audit/sources` | **400** `REQUEST_CONTEXT_NOT_PROVIDED` |
| organization (`<org-a>`, us), no scope header | `GET /audit/v1/bogus-control` | **400** `REQUEST_CONTEXT_NOT_PROVIDED` |

```json
{"httpStatus":400,"traceId":"8997900a14884822ef2e455a4ba86800","errors":[
  {"code":"REQUEST_CONTEXT_NOT_PROVIDED","field":"","description":"The request context could not be detected.","id":""}]}
```

Row 1 is what rules out "that credential is not organization-scoped": it
resolves an organization from the token with no header and reads a genuinely
organization-scoped API. So the refusal on row 2 is audit declining
organization scope, not the credential failing to establish one. Row 4 confirms
`required: true` is right rather than merely tightened documentation.

Row 5 is the limit of the probe: **the scope check runs before routing**, so a
path that does not exist in the namespace returns the identical body. This
probe therefore establishes that audit cannot be reached without an
environment header — it does not establish anything audit-specific about the
`organization` value, and no probe through this gateway can.

### The published permissions map, cross-checked against the whole registry (2026-09-03)

`TestScopedPrivilegesUseGAVocabulary` now parses a committed snapshot of
developer.jamf.com's permissions map instead of a hand-typed transcription of
it, and checks all **337** distinct generated `{capability}:{action}`
identifiers against it. Result: **it declares every one, action for action**, so
`permissionsMapExceptions` is empty. That is the first machine-checked
corroboration of the specs' `x-required-privileges` by a source not derived from
them — `_permissions/routes.yaml` is generated from those same extensions, so
its agreement has always been tautological.

Most valuable where it is most needed: the map's *Organization management scope*
section declares `licensing:{r}`, `deal-registration:{c,r}`,
`distributor-actions:{c,r,u}`, `sso-connections:{c,r,u,d}` and
`sso-domains:{c,r,u,d}` — all eighteen `account` privileges that no published
spec carries and `config.json` supplies by hand.

**Two coverage gaps came out of the other direction**, where the map declares
something the SDK's surface never uses. Neither is a defect in the SDK, and both
are worth reporting:

- **`deal-registration:create` has no endpoint.** The map declares
  `deal-registration:{c,r}`, but `external/account-partners` declares only
  `GET /v1/deal-registrations`. So the gateway's capability model carries a deal
  registration *create* that the published spec does not — in the package whose
  privilege publishing is already known to be broken.
- **`devices:delete` has no endpoint either**, and here the SDK's answer looks
  like the better one: Classic's `DELETE /computers/id/{id}` declares
  `destructive-device-actions:execute`, not `devices:delete`. Either the map
  declares an action the gateway never enforces, or a device-delete endpoint
  exists that the whitelist does not reach.

Seven further capabilities are declared and unreached —
`detection-analytics`, `prevent-lists`, `protection-plans`,
`security-audit-log`, `threat-alerts`, `threat-definition-versions`,
`unified-logging-filters`. All seven are Jamf Protect, addressed through its
GraphQL API rather than by path (their Endpoints cells list operation names like
`listThreatAlerts`), so their absence is correct and not a gap.

One trap, recorded because it produced a false report before the check was
written: **a capability is declared across as many rows as its resources need**,
and the rows must be unioned. `compliance-benchmarks` appears twice — `{c,r,d}`
over `/benchmarks`, `{r}` over `/baselines` — and a parser keeping only the last
row reports the spec's `compliance-benchmarks:create` and `:delete` as
undeclared. They are declared. `parsePermissionsMap` unions, and
`TestParsePermissionsMapUnionsRowsForOneCapability` pins it.

### The organization credential's own capability map (2026-09-03)

Incidental to the above and useful for the `account` holds: on organization
tenant `<org-a>` the grant is partial, and the split is per capability rather
than per spec.

| request | result |
|---|---|
| `GET /licensing/v1/licenses` | **200** — 16 rows |
| `GET /sso/v1/connections` | **200** — 5 connections |
| `GET /partners/v1/partners` | **403** `BAD_PERMISSIONS` |
| `GET /sso/v1/domain-allocations` | **403** `BAD_PERMISSIONS` |

Two consequences:

- **The `account-licensing` hold stands, re-established on a second
  organization tenant.** `type` is populated on 16/16 rows here too, and
  `licenseType` on 8/16 — so they are distinct fields, not a rename, and
  v1872's removal of `type` would still be a silent regression.
- **The `account-sso` hold could not be re-probed.**
  `DomainAllocationConnection.authZeroRegion` lives on
  `/sso/v1/domain-allocations`, which this credential cannot reach while
  `/sso/v1/connections` answers 200 in the same session — an ungranted
  capability (`sso-domains`), not an absent endpoint. Note the published
  permissions map types those as two capabilities,
  `sso-connections:{c,r,u,d}` and `sso-domains:{c,r,u,d}`, which is exactly
  the split observed. Lifting that hold needs a credential holding
  `sso-domains`.

The token is opaque — 342 characters, not a JWT — so none of this is readable
from the credential itself; it has to be probed.
