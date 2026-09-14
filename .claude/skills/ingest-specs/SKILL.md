---
name: ingest-specs
description: Ingest a Jamf Platform GitOps spec bundle (zip) into testing/ — all nineteen specs, every package — then evaluate what changed, wire-probe it, regenerate and live-test. Use when the user supplies a jamf-platform-apis-gitops-*.zip, asks whether a build changed anything, or asks to ingest/refresh the specs.
---

# Ingest a GitOps spec bundle

Every spec the SDK carries is published to the bundle's `external/` tree and
every one is copied verbatim by `tools/generate/ingest`. So an ingest is no
longer a per-family hand copy; it is one command, and the work is deciding
whether to *believe* the build.

`CLAUDE.md` is the authority for the current position, the holds and the
package table. `docs/WIRE-FACTS.md` is where probe evidence goes.
`docs/STYLE.md` carries every config mechanism. Read the relevant one rather
than re-deriving it.

Take the zip path from the user; ask if they did not give one. Unpack into the
scratchpad, never into the repo.

## 1. Hash and dry-run before reading anything

A build number advancing is not evidence of a spec change. An interior build
can be a pure pipeline re-run, and two builds land the same day. Two recorded
no-ops so far: v1725 was byte-identical to v1700, and v2024 to v2018 across
every file outside `internal/dev`.

```
make ingest ZIP=~/Downloads/jamf-platform-apis-gitops-vNNNN-*.zip INGEST_FLAGS=-dry-run
```

The report prints the archive's build and SHA-256, and per spec its source
directory, `info.version`, path/operation/privilege counts, and — from
`testing/.ingest-manifest.json`, which records what each file was ingested
from — whether the bytes are `unchanged`, `updated` or `new`. `unchanged
(since vNNNN)` is the whole of step 1: those specs need no further reading.

Three things the tool refuses rather than reports, each because the silent
version of the failure has happened:

- **A family in `external/` that neither the provenance table nor
  `knownUnmapped` accounts for.** A newly published API appears in no diff of
  the specs already carried, so it is the easiest thing in a build to miss.
  Add a row (plus the matching `tools/generate/config.json` entry) or record
  why it is not carried.
- **An `info.title` that does not match the row's assertion.** Directory names
  are not stable — v1401 renamed `securitycloud-dns` to `jsc-dns`, v1865
  dropped the `-beta` suffix from every directory while keeping the beta
  content — and a name-keyed match that silently finds nothing looks exactly
  like "no changes". The concrete trap the assertions close:
  `securitycloud-device-groups-api.yaml` is the Security Cloud *Devices* API,
  while `internal/stage/device-groups` is the unrelated Platform Device Groups
  API, and copying that one replaces the spec.
- **A selected family missing from the archive.** Held rows are tolerated when
  absent, since reconstructing an older build legitimately predates a family's
  publication.

Held specs are skipped and their reason printed on every run, so a hold cannot
rot unnoticed. Take one with `-only <dest>` (or `-include-held` in bulk) and
update `CLAUDE.md`'s holds table in the same change.

A `-only` run **reports `_permissions/{routes,scopes}.yaml` but writes
neither** — they are not spec-scoped, so a narrowed run must never rewind the
privilege oracle to that archive's build. Both rows still print, annotated
`reported only`. Refresh them with a full run.

**Never ingest from `internal/dev`.** It carries a per-spec `x-generated` block
(`commitHash`, `runId`, `timestamp`), so every file reports as changed and a
no-op build reads as a bundle-wide rewrite. Comparing operation *sets* across
environments is what dev is good for — that is how the v1942 publishing filter
was caught. `internal/stage` declares a stage host and an `(STAGE)` title, both
of which would leak into the `api/*.json` a consumer reads.

**Never fall back to the spec source repository's own `teams/` directories.** Those
carry no version prefix and no servers, so the generated URLs are ones the
gateway rejects — invisible in the code until it is called.

## 2. Diff structurally, not textually

A textual diff is mostly prose reflow and tag re-casing. Compare parsed YAML,
and report these separately:

- **path set** added/removed, per method
- **schema set** added/removed
- **per-schema semantic diff** with `description`/`example`/`title`/`summary`
  stripped, so `required`, `enum`, `type`, `minLength`, `pattern`, `format` and
  `default` stand out
- **per-operation semantic diff** with `tags` stripped too

Then read the remaining prose only where the structure moved.

Alarming and inert: OpenAPI tag re-casing (the generator lowercases and maps
`-`/`_`/space alike, so filenames do not move); `X-B3-TraceId` response headers
appearing or vanishing; `servers` and `tokenUrl` host churn. **`servers` is
never the authority — the gateway is**; the SDK never reads it, so a
disagreement is reported upstream and never patched into the transport.

Matters: any `required` change (it flips Go pointer-ness), any new `enum`, any
path-prefix change, any schema rename, and **`config.json`'s own overrides** —
`expectedStatus` and `responseType` have no tripwire, and a silent override
going redundant has happened repeatedly.

**Privileges have a tripwire, and it is the one thing you do not have to diff by
hand.** `TestScopedPrivilegesUseGAVocabulary` checks every generated
`{capability}:{action}` against a committed snapshot of developer.jamf.com's
published permissions map — the only privilege oracle independent of the specs.
A new capability in an ingested spec fails that test: run `make permmap` to
refresh the snapshot, and if the disagreement survives the refresh it is real,
so wire-probe it, report it upstream and record it in
`permissionsMapExceptions` with the evidence. Never delete the assertion to make
an ingest pass. The test also logs which capabilities the map declares that the
SDK does not reach, which is where an operation the whitelist has not caught up
with shows itself.

**Check the path prefixes.** When a spec starts carrying its own `/vN/` prefix,
the spec-level `"version"` key must be deleted in the same change as the `"op"`
paths gain it. Leaving it produces `/v1/v1/…`; forgetting the paths fails
generation outright with `path %s not found in spec`, which is the good
outcome. A *correct* prefix migration produces no diff in the generated
methods — do not read an empty method diff as proof you missed something.

**Diff `_permissions/{routes,scopes}.yaml`.** The tool copies them and reports
a change that is only a reordering as such, because `routes.yaml` has been
byte-different and sort-identical in five builds and mistaking that for a
content change has cost a wrong conclusion. They are exactly self-consistent,
which is what makes them usable to adjudicate a spec's privileges — **but
`routes.yaml` is generated from the specs' own `x-required-privileges`, so it
cannot corroborate them.** For an independent check use
the gateway's authorization policy.

## 3. Check the release state, not just the spec

The bundle publishes specs; it does not control when a change goes live.

- The gateway's own API definitions decide routing and scoping — a directory per
  environment, an api-product YAML per region. `config_data.request-context-allowed-sources`
  is the scoping switch. In v1495 the specs moved the tenant into a header
  weeks before prod allowed `header`, and ingesting then would have broken
  every call.
- The gateway's authorization policy is the per-path allowlist, hand-written OPA and
  so independent of the specs. **`main` is not `deployed`** — date the rollout
  from the wire, not the merge. It is also where a withdrawal stops being a
  spec claim and starts returning 403.
- `git log` in either repo is the fastest explanation for a behaviour change no
  spec mentions: the `href` mystery resolved to "Enable href-injection plugin
  on all securitycloud clusters".

**The specs have led the server, and the SDK has followed them anyway.** v1942
deleted 146 deprecated-with-successor operations that the wire, the OPA
allowlist and the same bundle's `routes.yaml` all still carried. Where a
removal would cost a capability outright, **hold the spec instead** — that is
what `capi` and `securitycloud-devices` are. Record the disagreement either
way; following the spec is a decision, not evidence the server agrees.

**Report upstream, never file it.** Hand the evidence to the user.

## 4. Wire-probe every behavioural claim before ingesting it

**Probe the wire and quote the payload; never conclude from a spec diff.** The
standard:

- **A known-good control in the same invocation.** Tokens live 900 seconds, and
  a rejected token gives the same plain-text `401 Authentication failed` as a
  credential with no policy for the api-product. Two rounds of scope probing
  were reported wrongly — once from a cached expired token, once from a broken
  shell helper — before a control showed the harness was at fault.
- **Use the right credential set for the scope.** Environment, tenant and
  organization credentials are alternatives, not aliases; a pro credential
  answers 403 on every Security Cloud path, indistinguishable from unrouted.
  A capability can also be granted on one credential and not another on the
  same tenant — `uem-connect` versus `device-groups` is the worked example.
- **Two credentials, not two paths, to classify a 403.** One that varies by
  credential is a capability grant; one constant across credentials is a
  missing authorization rule.
- **Repeat a routing probe.** The unrouted tell is a *repeated*
  `403 BAD_PERMISSIONS`; a one-off 500 reads as "routed and merely faulting",
  which is the opposite of the truth. `401/404/403/502/400` each name a
  different layer — a bogus path in the same namespace is the control.
- **Exploit validation ordering to probe safely.** Where field validation runs
  before resource resolution, a request against a nonexistent id still
  exercises every field constraint and creates nothing. **The order is
  per-operation and must be established, not assumed**: uem-connect's
  sync-settings PUT resolves the resource first, and `406` is decided last of
  all, at response serialization, so it is invisible on an operation that
  writes no body.
- **A null-skipping sweep proves nothing.** A field mismatch hid behind an
  all-null column across five rows and failed on row 31 of a larger sample.
- **SDK-mediated observation is not wire truth.** Go adds `Accept-Encoding:
  gzip` unasked, which nulled `href` on every Security Cloud create for weeks
  while curl saw it fine. When curl and the SDK disagree, diff the requests.
- **Check the reverse direction too** — whether the server is *looser* than the
  new spec. Three v1993 constraints are declared and unenforced, and
  uem-connect answers 200 with an XML body against a spec claiming JSON only.
- **Keep probe volume low.** ~150 probe requests during one WAF investigation
  triggered an IP blocklist and caused a second, broader outage.

**A quiet spec diff does not mean a quiet build.** v1439's only spec change was
a spelling fix, and the same build silently reverted all three ZTNA creates to
`{id, href}` — invisible to any diff, because the spec had always declared the
shape the server was finally using. So run the live suite even when the diff
looks cosmetic, and treat a create-response assertion as load-bearing.

If a probe needs a resource that does not exist, mint the cheapest possible one
and delete it. **Track everything you create, delete it before finishing, and
re-list at the end.** Where a create cannot be undone — a Security Cloud
activation profile is a soft delete the read surface does not reflect — create
exactly one and fold every assertion into that one body.

## 5. Ingest, regenerate, read the whole diff

```
make ingest ZIP=~/Downloads/jamf-platform-apis-gitops-vNNNN-*.zip
make generate
```

Read the whole diff. Expect it scoped to the affected `api/*.json` and package;
anything outside is a signal — a generator change you made, or a spec written
to the wrong destination.

Review pointer-ness explicitly. A field moving `*T` → `T` is breaking; grep the
repo and the downstream providers before accepting it. Note that **a shared
schema's optional scalars can change pointer-ness with no diff in their own
schema**, because `needsPtr` follows request/response reachability — and that
is not to be fixed by forcing one direction.

**Check CI parity when the whitelist changed, not only when the generator
did.** `testing/` is gitignored and the generator falls back to `api/`, which is
pruned of unreachable schemas while `testing/` is not — so a removal can leave
a schema present-but-unreachable locally and absent in CI, and
`hoistInlineObjects` names nested types after whichever parent it sees first.
Force the fallback path with a scratch root of symlinks carrying no `testing/`.

**Two spec-shape assumptions to keep out of the generated code.** A list method
must not hardcode `{totalCount, results}` *or* a bare array — the four account
endpoints flipped shape mid-life and broke every account list method, and the
first fix was symmetric to the bug. Use `unwrapResults`, and give each such
operation an `assertListBodyShape` acceptance test. And do not configure
`pagination` from an envelope's *shape*: an endpoint that ignores the params
loops and concatenates the same list forever. Probe first.

## 5b. The local repairs are self-expiring — let them expire

`schemaCreations` panics when the name reappears; a stand-in `schemaPatches`
entry is listed in `schemaPatchesRequireAbsent` and panics when the path
resolves; `enumAdditions` panics on a value the spec now declares;
`requiredPrivileges` fails once the operation declares
`x-required-privileges`; a `docNotes` entry naming no emitted type fails hard;
and a `permissionsMapExceptions` entry fails once the published map declares the
permission, or once the SDK stops emitting it.

**A generate that panics with one of those messages is not a problem to work
around: it is the spec being repaired.** Delete the config entry (and its
`schemaPatchesRequireAbsent` line), regenerate, and check whether the upstream
declaration is richer than the local stand-in — a recovered enum usually means
new generated constants.

If you add a new stand-in patch, add its `schemaPatchesRequireAbsent` line in
the same change. A patch that supplies a missing property without that line is
the one failure mode nothing else catches.

## 6. Test

```
go build ./... && go test -count=1 ./...
cd tools/generate && go test -count=1 ./...
go vet -tags acceptance ./jamfplatform/
make lint
go fmt ./... && go fix ./... && go fix -tags acceptance ./jamfplatform/...
```

`go vet -tags acceptance` is not optional: the suite is build-tagged, so
`make test` and CI never compile it and a signature change breaks it silently.
Neither is the tagged `go fix` — an untagged run cannot see the acceptance
files, and modernisations silently accumulate there.

Then the live suite, scoped with `tools/acctargets` where a full run is not
warranted.

**Read the SKIPs, do not just count PASSes** — and treat a skip as a defect in
the test, not a fact about the tenant. A test that skips for want of a fixture
has never verified anything, and it skips hardest on the clean tenant a CI run
starts from. If the fixture is mintable, **make the test mint it**: creating one
by hand fixes one run, self-provisioning fixes every run. Keep the safety gate
where the fixture provisions real infrastructure — the point is that a skip
names the variable that would fix it, not an absent fixture the reader cannot
act on.

**Add an acceptance test for each behavioural change you probed**, pinning the
observed status, `code` and field attribution, and **never tolerate a real
error to make one pass** — no `>= 400 && < 500` escape hatches. Prefer a test
that provisions nothing. If a test pins a current *limitation*, say so in a
comment: it should fail the day the limitation lifts.

**When a limitation lifts, flip the assertion; never delete it.** The negative
test earned its keep by failing at the right moment, and its replacement must
assert the new capability at least as precisely or the coverage silently
shrinks.

## 7. Record what was learned

Update `CLAUDE.md`'s current position, provenance table and holds; put probe
evidence in `docs/WIRE-FACTS.md` with its date and a quoted payload; strike
through anything the probe disproved. Record the reasoning, not just the
conclusion — the value is in why a thing is the way it is.

Report to the user: what changed, what the wire said, what the generated diff
was, what now passes that did not, what is still pending upstream, and anything
worth reporting back to Jamf.
