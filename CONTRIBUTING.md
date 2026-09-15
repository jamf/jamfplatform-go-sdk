# Contributing

Thank you for your interest in contributing to the Jamf Platform Go SDK.

## Prerequisites

- **Go** >= 1.26 (see `go.mod` for the exact version)
- **golangci-lint** for linting
- A Jamf Platform tenant with API credentials (for acceptance tests only)

## Getting Started

```bash
# Clone the repository
git clone https://github.com/jamf/jamfplatform-go-sdk.git
cd jamfplatform-go-sdk

# Run tests
go test -v -count=1 -timeout=120s ./...

# Run linting
golangci-lint run ./...
```

## Development Workflow

1. Create a feature branch from `main`.
2. Make your changes.
3. Run tests and linting before committing:

   ```bash
   go test -v -count=1 -timeout=120s ./...
   go vet ./...
   golangci-lint run ./...
   ```

4. Open a pull request against `main`. CI will run tests, vet, lint, mod tidy, copyright headers, and vulnerability checks automatically.

## Running Acceptance Tests

Acceptance tests run against live Jamf tenants. Credentials come from
`JAMFPLATFORM_ACC_<PRODUCT>_<SCOPE>_<FIELD>` variables — the `ACC_` prefix keeps
them clear of the `JAMFPLATFORM_CLIENT_ID` names `jamfplatform/doc.go` publishes
as the *consumer* convention. There are four sets, all optional; tests whose set
is unset skip.

| Set | Variables |
| --- | --- |
| **Environment** — the workhorse. One environment credential reaches `pro`, `devices`, `blueprints`, `compliancebenchmarks`, `aigovernance` and `securitycloud`. | `JAMFPLATFORM_ACC_ENVIRONMENT_BASE_URL`, `JAMFPLATFORM_ACC_ENVIRONMENT_ID`, `JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_ID`, `JAMFPLATFORM_ACC_ENVIRONMENT_CLIENT_SECRET` |
| **Organization** — Jamf Account. There is no ID: organization scope sends no scope header. The base URL must be the US gateway. | `JAMFPLATFORM_ACC_ORGANIZATION_BASE_URL`, `JAMFPLATFORM_ACC_ORGANIZATION_CLIENT_ID`, `JAMFPLATFORM_ACC_ORGANIZATION_CLIENT_SECRET` |
| **Pro tenant** — the legacy `X-Tenant-Id` scope, kept so a regression in that path cannot ship unnoticed. Its base URL is also the default for the other sets, and the whole set is the fallback when the environment set is absent. | `JAMFPLATFORM_ACC_PRO_TENANT_BASE_URL`, `JAMFPLATFORM_ACC_PRO_TENANT_ID`, `JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_ID`, `JAMFPLATFORM_ACC_PRO_TENANT_CLIENT_SECRET` |
| **Security Cloud tenant** — optional, and preferred over the environment credential when set because it lands on a different Security Cloud tenant carrying the suite's fixtures. The base URL defaults to the Pro tenant's. | `JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_BASE_URL`, `JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_ID`, `JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_ID`, `JAMFPLATFORM_ACC_SECURITYCLOUD_TENANT_CLIENT_SECRET` |

`JAMFPLATFORM_ACC_REQUIRE` takes a comma-separated list of `platform`,
`environment`, `organization` and `securitycloud`, and makes a named set's
absence **fatal** rather than a skip — leave it unset locally, since a skip is
the right outcome for a contributor with no tenant, but CI sets it per lane so a
missing secret fails the build instead of reporting `ok` having asserted nothing.
`.github/workflows/acceptance.yml` is the authoritative variable list, including
the optional Pro fixtures and material.

Then run:

```bash
make testacc
```

Or manually:

```bash
go test -v -cover -count=1 -tags acceptance -timeout 120m -p=1 ./...
```

## Project Structure

| Directory          | Purpose                                          |
| ------------------ | ------------------------------------------------ |
| `jamfplatform/`    | Exported SDK package — typed client methods      |
| `internal/client/` | Transport layer — HTTP, auth, pagination, errors |
| `tools/`           | Dev tool dependencies (copywrite)                |

## Adding a New Resource

1. Add the types and client methods in `jamfplatform/<resource>.go`.
2. Add unit tests in `jamfplatform/<resource>_test.go` using the `testServer` helper.
3. Ensure copyright headers are present (`copywrite headers --config .copywrite.hcl`).
4. Run tests and linting.

## Dependencies

This project uses native Go and `golang.org/x/oauth2`. Do not introduce third-party dependencies without discussion.

## Commit Messages

Use [conventional commit](https://www.conventionalcommits.org/) style messages:

- `feat: add device_group resource support`
- `fix: handle nil response in GetDevice`
- `test: add unit tests for benchmark methods`
- `refactor: extract shared pagination logic`
- `chore: update CI workflow action versions`
- `docs: update README with new usage examples`

## Pull Requests

- Keep PRs focused — one feature or fix per PR.
- Include unit tests for new resources.
- CI must pass before merge.

## Reporting Issues

Open an issue on GitHub with:

- SDK version (Go module version or commit SHA).
- Relevant code snippet (redact credentials).
- Expected vs actual behaviour.
- Any error messages or logs.
