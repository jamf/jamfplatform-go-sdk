# jamfplatform-go-sdk

Go client library for the [Jamf Platform API](https://developer.jamf.com/platform-api).

All types, methods, and unit tests are generated from OpenAPI spec files. Published API specs are available in the [`api/`](api/) directory.

## Installation

```bash
go get github.com/Jamf-Concepts/jamfplatform-go-sdk
```

## Usage

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/devices"
)

func main() {
	client := jamfplatform.NewClient(
		"https://eu.api.jamfcloud.com",
		os.Getenv("JAMFPLATFORM_CLIENT_ID"),
		os.Getenv("JAMFPLATFORM_CLIENT_SECRET"),
		jamfplatform.WithEnvironmentID(os.Getenv("JAMFPLATFORM_ENVIRONMENT_ID")),
	)

	ctx := context.Background()

	// List all devices
	ds, err := devices.New(client).ListDevices(ctx, nil, "")
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range ds {
		fmt.Printf("%s  %s  %s\n", d.ID, d.Name, d.SerialNumber)
	}
}
```

### Base URL

Pass the regional gateway root, with no path:

| Region | Base URL |
|---|---|
| US | `https://us.api.jamfcloud.com` |
| EU | `https://eu.api.jamfcloud.com` |
| APAC | `https://apac.api.jamfcloud.com` |

The base URL must be the gateway **root**. The SDK appends
`/{namespace}/{version}/...` for API calls and `/auth/token` for the token
exchange, so a base URL carrying a path prefix (`https://host/api`) sends the
token exchange to `/api/auth/token`, which the gateway does not serve — the
call then fails during authentication rather than on the request you made.
A path prefix works only against a reverse proxy that mounts both the token
endpoint and the API namespaces beneath it.

> **Breaking change for the Jamf Platform API GA (2026-09-01).** The previous
> gateway, `{region}.apigw.jamf.com`, is retired, and it required an `/api`
> segment the new host does not serve. Update the base URL; nothing else in
> your code changes. GA also invalidates every public-beta credential, so
> integrations have to be re-created regardless.

### Authentication

The client uses OAuth 2.0 client credentials. Create API credentials in your Jamf Platform tenant and provide the client ID and secret.

Token refresh is handled automatically.

### Scope

A client carries exactly one scope, chosen at construction and sent as a request
header on every call:

| Scope | Option | Header |
|---|---|---|
| Environment | `WithEnvironmentID` | `X-Environment-Id` |
| Tenant | `WithTenantID` | `X-Tenant-Id` |
| Organization | none | none |

Prefer environment scope. An environment groups a customer's tenants, and it is
the scope Jamf intends new integrations to be created with. Tenant scope is the
legacy form and remains supported — some surfaces are only reachable that way.
Organization scope is the **absence** of a scope option: the gateway resolves the
organization from the access token, and it is what the `account` package uses.

The two ID-bearing scopes are alternatives, not aliases — **the header must match
the credential.** An integration is minted against one scope, and crossing them
over is refused with `403 OWNERSHIP_FORBIDDEN` even when both IDs belong to the
same customer, so this is a choice between two integrations rather than two IDs
for one. Setting both options is a configuration mistake rather than a
combination; environment takes precedence whichever order they are passed in.

Read the scope back with `Client.Scope() (ScopeKind, string)`. It is three-valued
— `ScopeTenant`, `ScopeEnvironment`, or the zero kind with an empty ID for the
organization case — so a caller switching on the kind has to handle all three
rather than assume a scope is always present.

### Client options

```go
client := jamfplatform.NewClient(baseURL, clientID, clientSecret,
	jamfplatform.WithEnvironmentID(environmentID),
	jamfplatform.WithUserAgent("my-app/1.0"),
	jamfplatform.WithHTTPClient(customHTTPClient),
	jamfplatform.WithLogger(myLogger),
	jamfplatform.WithFileTokenCache("/tmp/tokens"),
)
```

`WithHeaders` adds headers to every request, including the token exchange, and
`WithAuthorizationHeaderName` moves the bearer credential to a header of your
choosing. Both exist for callers behind a reverse proxy that authenticates
callers itself; prefer them over `WithHTTPClient`, which replaces the SDK's tuned
transport. See the godoc for the details.

### Error handling

Any non-success HTTP response surfaces as `*jamfplatform.APIResponseError`.
Inspect it via the accessor methods; for non-HTTP errors (denylist refusal,
context cancellation, IO failures) `err.Error()` carries a formatted message.

```go
import (
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform"
	"github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/devices"
)

device, err := devices.New(client).GetDevice(ctx, id)
if apiErr := jamfplatform.AsAPIError(err); apiErr != nil {
	switch {
	case apiErr.HasStatus(404):
		// handle not found
	case apiErr.StatusCode >= 400 && apiErr.StatusCode < 500:
		// surface field-level validation errors
		for field, msgs := range apiErr.FieldErrors() {
			for _, msg := range msgs {
				fmt.Printf("  %s: %s\n", field, msg)
			}
		}
	default:
		fmt.Println(apiErr.Summary())
	}
}
```

Accessors on `*APIResponseError`:

- `HasStatus(code)` — check for a specific HTTP status.
- `Details()` — raw `[]ErrorDetail` parsed from the response body.
- `FieldErrors()` — details bucketed by field name for attribute-level diagnostics.
- `Summary()` — single-line human-readable summary for logs or CLI output.
- `AsAPIError(err)` — top-level unwrap, returns `*APIResponseError` or `nil`.

Pro JSON endpoints populate `Details`/`FieldErrors` with server-returned
validation info. Classic XML and other non-JSON error bodies may leave those
empty; `Summary()` always formats cleanly regardless.

### RSQL filters

Build filters for list endpoints:

```go
filter := jamfplatform.BuildRSQLExpression([]jamfplatform.RSQLClause{
	{Selector: "name", Operator: "==", Argument: "MacBook*"},
	{Selector: "operatingSystemVersion", Operator: "=gt=", Argument: "15.0"},
})
ds, err := devices.New(client).ListDevices(ctx, nil, filter)
```

### Async polling

For async operations (e.g. benchmark sync), use the `PollUntil` helper:

```go
ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
defer cancel()

cb := compliancebenchmarks.New(client)
err := jamfplatform.PollUntil(ctx, 5*time.Second, func(ctx context.Context) (bool, error) {
	bm, err := cb.GetBenchmark(ctx, id)
	if err != nil {
		return false, err
	}
	return bm.SyncState == "SYNCED", nil
})
```

## API coverage

Each API family lives in its own sub-package under `jamfplatform/`. Construct a service client with `<pkg>.New(rootClient)`.

| Sub-package | API |
|---|---|
| `jamfplatform/devices` | Platform device inventory |
| `jamfplatform/devicegroups` | Platform device groups |
| `jamfplatform/deviceactions` | Platform MDM commands (erase, restart, shutdown, unmanage, check-in) |
| `jamfplatform/blueprints` | Platform blueprints + components |
| `jamfplatform/ddmreport` | Platform declaration reporting |
| `jamfplatform/compliancebenchmarks` | Platform compliance benchmarks |
| `jamfplatform/pro` | Jamf Pro JSON API (buildings, packages, policies, MDM, enrollment, settings, PKI, etc.) |
| `jamfplatform/proclassic` | Jamf Classic XML API (computers, mobile devices, groups, profiles, policies, etc.) |
| `jamfplatform/securitycloud` | Jamf Security Cloud — DNS, ZTNA, content categories, device groups, activation profiles, UEM Connect. 54 operations across six specs; Security Cloud is a separate product with its own tenant identifier, so it needs a Security Cloud credential and its own `WithTenantID` value (an environment-scoped credential also reaches it) |
| `jamfplatform/account` | Jamf Account — licensing, partners, SSO. **Organization-scoped**: pass no scope option and let the gateway resolve the organization from the token. **US gateway only** |
| `jamfplatform/aigovernance` | AI Governance policies and tools. Environment-scoped |
| `jamfplatform/audit` | Platform audit events (read-only). Environment-scoped, and **not usable yet**: the gateway refuses every call, because these routes need an `audit:read` grant that is not currently issued to external credentials |

All list methods handle pagination automatically. Pro's versioned endpoints emit version-suffixed Go methods (`ListBuildingsV1`, `GetCheckInSettingsV3`) so consumers pin to a specific API version. Exact method lists are generated from the OpenAPI specs under `testing/` — see the published specs in [`api/`](api/) for the current surface.

### Classic (XML) example

```go
import "github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/proclassic"

classic := proclassic.New(client)
policy, err := classic.GetPolicyByID(ctx, "42")
if err != nil {
    log.Fatal(err)
}
fmt.Println(policy.General.Name, policy.Scope.AllComputers)
```

Classic is fully typed — the generator hoists nested XML sections (`general`, `hardware`, `purchasing`, etc.) into named structs and emits every field as a pointer so three-state null/value semantics round-trip cleanly (required for the upcoming Terraform provider).

### Pro example

```go
import "github.com/Jamf-Concepts/jamfplatform-go-sdk/jamfplatform/pro"

p := pro.New(client)
pkgs, err := p.ListPackagesV1(ctx, nil, "")
if err != nil {
    log.Fatal(err)
}
for _, pkg := range pkgs {
    fmt.Println(pkg.ID, pkg.PackageName, pkg.FileName)
}

// Multipart .pkg upload
f, _ := os.Open("my-app.pkg")
defer f.Close()
created, _ := p.CreatePackageV1(ctx, &pro.Package{
    PackageName: "my-app",
    FileName:    "my-app.pkg",
    CategoryID:  "-1",
})
_, err = p.UploadPackageV1(ctx, created.ID, "my-app.pkg", f)
```

## Code generation

All SDK types, methods, and unit tests are generated from OpenAPI spec files using a custom generator built on [kin-openapi](https://github.com/getkin/kin-openapi). The generator also publishes filtered API specs to `api/` containing only the public SDK surface.

```bash
make generate    # regenerate Go code, tests, and published API specs
make test        # run unit tests
make testacc     # run acceptance tests (requires API credentials)
make lint        # run golangci-lint
```

The only handwritten source file is `jamfplatform/client.go`. Everything else is generated from the specs in `testing/` via the config in `tools/generate/config.json`.

To add a new API endpoint:

1. Ensure the endpoint is defined in the OpenAPI spec file under `testing/`
2. Add an operation entry to `tools/generate/config.json`
3. Run `make generate`

CI enforces that generated output is current on every pull request.

## Getting help

Open an issue: <https://github.com/Jamf-Concepts/jamfplatform-go-sdk/issues>.
There are templates for bug reports and feature requests. Jamf Concepts
publishes this SDK and Jamf Support does not cover it, so raise anything about
the library here. Take a defect in the API behind it to a Jamf Support case.

Report a security vulnerability through [SECURITY.md](SECURITY.md). Do not open
an issue for one.

## Troubleshooting

**`401 Authentication failed`, as plain text.** The gateway sends this body both
for a credential it cannot authenticate and for one it authenticates but which
holds no policy for the API product you called, so the status will not tell you
which you hit. Check the credential, then check whether it holds the capability
the endpoint needs. `Privileges` in each sub-package lists those capabilities,
and `MethodPrivileges.Scopes` gives the scope the credential must carry.

**The token exchange answers 404.** The SDK builds the token endpoint as
`{baseURL}/auth/token`, so a base URL carrying a path prefix sends the exchange
somewhere the gateway does not serve. Pass the gateway root,
`https://{region}.api.jamfcloud.com`, with no `/api` segment. The SDK catches
this case and names your base URL in the error.

**`403 OWNERSHIP_FORBIDDEN`.** Your scope header does not match your credential.
`WithTenantID` and `WithEnvironmentID` stamp different headers, and a client
carries one scope, so the gateway refuses a tenant header on an
environment-scoped credential even within one customer. Call `Client.Scope()` to
see which one the client holds.

**`403 BAD_PERMISSIONS` on a path that should exist, every time you call it.**
In most cases the gateway has no route for that path. It sends the same 403 when
your credential lacks the capability, and one credential cannot separate the
two: vary the credential, and a 403 that changes points at the grant while a 403
that holds points at the route.

**Inspecting requests on the wire.** `WithLogger` hands your logger each
request's method, URL and body, and each response's status, headers and body.
The SDK logs nothing until you install one, and it redacts nothing. Request
bodies carry whatever secrets a write sends: `ClientSecret`, `AdminPassword`,
`KeystorePassword`, the plist inside a configuration profile's `Payloads`. So
redact inside your `Logger`, or log only the method, URL and status, before you
attach the output to a ticket or leave it in CI.

`LogRequest` never sees the bearer token or the client credential: the SDK
passes it no headers, and the OAuth2 token exchange runs outside the logged
path. `LogResponse` is different — it receives the response headers unfiltered,
so filter there rather than printing them wholesale. The SDK's own acceptance
tracer prints response headers from a fixed allowlist for that reason.

## License

[MIT](LICENSE)
