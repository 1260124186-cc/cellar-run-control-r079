# Cellar Run Control

Cellar Run Control is a local Go HTTP service for coordinating production
cellars. It keeps formula approval, vessel availability, active fermentation
runs, gravity observations, conditioning, completion metrics, and vessel
cleaning in one deterministic process.

One deployment can serve the main plant and branch cellars at the same time.
Every formula, vessel, and run belongs to exactly one cellar namespace, so
sites may reuse the same recipe names, vessel codes, and run codes without
colliding or leaking into each other's queries.

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

Cellar namespaces:

- `POST /v1/cellars`
- `GET /v1/cellars`
- `GET /v1/cellars/{id}`
- `POST /v1/cellars/{id}/archive`
- `GET /v1/cellars/{id}/export`

All formula, vessel, and run routes are scoped to one cellar. Replace
`{cellar}` with the cellar identifier returned by `POST /v1/cellars`.

Formula workflow:

- `POST /v1/cellars/{cellar}/formulas`
- `GET /v1/cellars/{cellar}/formulas`
- `GET /v1/cellars/{cellar}/formulas/{id}`
- `PUT /v1/cellars/{cellar}/formulas/{id}/steps`
- `POST /v1/cellars/{cellar}/formulas/{id}/approve`
- `POST /v1/cellars/{cellar}/formulas/{id}/retire`

Vessel workflow:

- `POST /v1/cellars/{cellar}/vessels`
- `GET /v1/cellars/{cellar}/vessels`
- `GET /v1/cellars/{cellar}/vessels/{id}`
- `POST /v1/cellars/{cellar}/vessels/{id}/finish-cleaning`
- `POST /v1/cellars/{cellar}/vessels/{id}/retire`

Run workflow:

- `POST /v1/cellars/{cellar}/runs`
- `GET /v1/cellars/{cellar}/runs`
- `GET /v1/cellars/{cellar}/runs/{id}`
- `POST /v1/cellars/{cellar}/runs/{id}/reserve`
- `POST /v1/cellars/{cellar}/runs/{id}/start`
- `POST /v1/cellars/{cellar}/runs/{id}/observations`
- `POST /v1/cellars/{cellar}/runs/{id}/conditioning`
- `POST /v1/cellars/{cellar}/runs/{id}/complete`
- `POST /v1/cellars/{cellar}/runs/{id}/abort`

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

## Cellar Namespaces

A cellar is an isolated namespace for one production site:

- Formula names, vessel codes, and run codes are unique within a cellar and
  may repeat across cellars.
- Every list, detail, and export response only contains the addressed
  cellar's data; an identifier from another cellar answers `404` with the
  usual `*_not_found` code.
- A run can only reference a formula or vessel from its own cellar.
- Archiving a cellar requires that it holds no open runs and no vessels still
  bound to a run, so a frozen namespace never contains half-finished work.
  An archived cellar stays readable but rejects every write with
  `invalid_cellar_state`.
- Cellar creation and archiving are serialized with all other writes through
  the same snapshot mutex, so a concurrent archive and write produce exactly
  one winner and never a cross-space partial state.

## Workflow Checks

Run each production workflow check separately:

```bash
go run ./cmd/fermentctl check formula-approval
go run ./cmd/fermentctl check vessel-run-lifecycle
go run ./cmd/fermentctl check observations-completion
go run ./cmd/fermentctl check cellar-namespaces
```

Each check creates a temporary snapshot directory, starts the real HTTP
handler on a loopback listener, issues JSON requests, verifies success and
failure behavior, and removes its temporary data. The namespace check also
races concurrent writes against archiving and restarts the service over the
same snapshot to prove runs stay in their original cellar.

## Persistence And Recovery

The service stores a complete revisioned snapshot at
`<data-directory>/state.json`. A write creates a temporary file in the same
directory, commits the encoded bytes durably, closes the handle, and renames
the result over the previous snapshot. The in-memory snapshot is replaced only
after that operation succeeds.

On startup, an existing snapshot is decoded and normalized. Unknown or
conflicting map indexes are not silently invented; invalid JSON or a negative
revision stops the process before HTTP serving begins. Every entity carries
its cellar identifier, and startup rejects any formula, vessel, or run that
references an unknown cellar or points across cellar boundaries, so a corrupt
or hand-edited file can never merge namespaces. Snapshots written before
cellar namespaces existed are migrated into a deterministic `cellar_legacy`
cellar, keeping historical runs reachable under their original identifiers.

## Repair-Ready Boundaries

The baseline intentionally keeps several independent behavior surfaces visible
for later engineering evaluation:

- formula name normalization and duplicate detection;
- cellar namespace isolation, uniqueness, and archive guards;
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
