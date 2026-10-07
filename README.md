# Cellar Run Control

Cellar Run Control is a local Go HTTP service for coordinating a small
production cellar. It keeps formula approval, vessel availability, active
fermentation runs, gravity observations, conditioning, completion metrics, and
vessel cleaning in one deterministic process.

One deployment serves the main brewery and any number of branch cellars. Each
site is an isolated namespace: formulas, vessels, and runs belong to exactly
one site, names and codes are unique only within their site, and every query
and write is scoped to the site addressed in the URL.

This repository is a healthy initialization baseline. It has no intentional
defects and no automated test suite. Tests are intentionally deferred to a
later engineering task; the production workflow checks in this repository
remain available for immediate build and behavior verification.

## Requirements

- Go 1.23 or later
- A writable local data directory
- No database, network service, or third-party module

## Build And Run

Build every package:

```bash
go build ./...
```

Start the service:

```bash
go run ./cmd/fermentctl serve
```

The default address is `127.0.0.1:8088`. The default snapshot directory is
`./data`.

Environment variables:

- `CELLAR_CONTROL_ADDR`: HTTP listen address.
- `CELLAR_CONTROL_DATA_DIR`: directory containing `state.json`.

Example:

```bash
CELLAR_CONTROL_ADDR=127.0.0.1:9099 \
CELLAR_CONTROL_DATA_DIR=/tmp/cellar-control \
go run ./cmd/fermentctl serve
```

## Directory Structure

```text
cmd/fermentctl                 service and workflow-check executable
internal/clock                 time boundary
internal/config                environment configuration
internal/domain                entities, validation, transitions, metrics
internal/httpapi               HTTP routes, JSON decoding, errors
internal/service               use-case orchestration
internal/storage               revisioned snapshot persistence
internal/workflowcheck         real HTTP production checks
```

## Public API

Health:

- `GET /healthz`

Site workflow:

- `POST /v1/sites`
- `GET /v1/sites`
- `GET /v1/sites/{id}`
- `POST /v1/sites/{id}/archive`

Formula workflow (scoped to one site):

- `POST /v1/sites/{site}/formulas`
- `GET /v1/sites/{site}/formulas`
- `GET /v1/sites/{site}/formulas/{id}`
- `PUT /v1/sites/{site}/formulas/{id}/steps`
- `POST /v1/sites/{site}/formulas/{id}/approve`
- `POST /v1/sites/{site}/formulas/{id}/retire`

Vessel workflow (scoped to one site):

- `POST /v1/sites/{site}/vessels`
- `GET /v1/sites/{site}/vessels`
- `GET /v1/sites/{site}/vessels/{id}`
- `POST /v1/sites/{site}/vessels/{id}/finish-cleaning`
- `POST /v1/sites/{site}/vessels/{id}/retire`

Run workflow (scoped to one site):

- `POST /v1/sites/{site}/runs`
- `GET /v1/sites/{site}/runs`
- `GET /v1/sites/{site}/runs/{id}`
- `POST /v1/sites/{site}/runs/{id}/reserve`
- `POST /v1/sites/{site}/runs/{id}/start`
- `POST /v1/sites/{site}/runs/{id}/observations`
- `POST /v1/sites/{site}/runs/{id}/conditioning`
- `POST /v1/sites/{site}/runs/{id}/complete`
- `POST /v1/sites/{site}/runs/{id}/abort`

Every formula, vessel, and run carries a `site_id`. Lists and details only
return the addressed site's data, a run can only reference a formula or
vessel from its own site, and an archived site rejects new writes with
`site_archived` while remaining readable.

Writes accept an optional `X-Expected-Version` header. Errors use a stable
shape:

```json
{
  "error": {
    "code": "revision_conflict",
    "detail": "the aggregate version does not match X-Expected-Version",
    "field": ""
  }
}
```

## Workflow Checks

Run each production workflow check separately:

```bash
go run ./cmd/fermentctl check formula-approval
go run ./cmd/fermentctl check vessel-run-lifecycle
go run ./cmd/fermentctl check observations-completion
go run ./cmd/fermentctl check site-isolation
```

Each check creates a temporary snapshot directory, starts the real HTTP
handler on a loopback listener, issues JSON requests, verifies success and
failure behavior, and removes its temporary data. The site-isolation check
additionally restarts the service against the same directory to prove that
historical runs remain in their original sites.

## Persistence And Recovery

The service stores a complete revisioned snapshot at
`<data-directory>/state.json`. A write creates a temporary file in the same
directory, commits the encoded bytes durably, closes the handle, and renames
the result over the previous snapshot. The in-memory snapshot is replaced only
after that operation succeeds.

All sites share one snapshot document. Every formula, vessel, and run records
its owning `site_id`, and the name and code indexes are keyed per site, so
identical codes in different sites never collide. On startup, an existing
snapshot is decoded and normalized: indexes must match their collections per
site, and any reference that crosses a site boundary stops the process before
HTTP serving begins. Snapshots written before sites existed are migrated into
a default `MAIN` site on load.

## Repair-Ready Boundaries

The baseline intentionally keeps several independent behavior surfaces visible
for later engineering evaluation:

- formula name normalization and duplicate detection;
- site-scoped uniqueness and cross-site reference rejection;
- target gravity and process-step range validation;
- aggregate version conflict checks;
- atomic vessel reservation and release;
- run state transition guards;
- observation sequence and time ordering;
- gravity movement and range checks;
- metric calculation and rounding;
- durable snapshot replacement and startup normalization.

These are production mechanisms, not injected defects. A later task can add
automated tests or request a focused repair without changing the public API.

## Test Boundary

This stage does not create unit tests, integration tests, test fixtures, or an
E2E suite. The later test task owns systematic coverage of validation limits,
state transitions, concurrency, persistence failures, and numeric edge cases.

## Scope

The service does not control physical equipment, authenticate remote users,
send notifications, expose a browser interface, or replicate data across
processes. It is intended for a trusted local network or an internal adapter.
