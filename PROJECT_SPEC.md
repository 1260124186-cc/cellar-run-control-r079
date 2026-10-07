# Cellar Run Control Specification

## Goal

Cellar Run Control is a local Go HTTP service for a small production cellar.
It coordinates approved formulas, vessel reservation, active fermentation
runs, gravity observations, conditioning, completion, and vessel release.

One deployment serves the main plant and branch cellars concurrently. Every
formula, vessel, and run belongs to exactly one cellar namespace: names and
codes are unique per cellar, queries only see their own cellar, and runs
cannot reference another cellar's formulas or vessels.

The project is a healthy initialization baseline. It contains no intentional
defects, no model traces, and no automated tests. Verification is intentionally
deferred to a later engineering task.

## Users

- Production planner: creates formulas, updates process steps, and approves a
  formula before it can be used.
- Cellar operator: registers vessels, creates runs, reserves a vessel, and
  starts fermentation.
- Fermentation observer: appends timestamped gravity observations and moves a
  run through conditioning and completion.
- Integration client: calls the stable JSON API from a local production script.

## Entities

### Cellar

A cellar is an isolated namespace for one production site. It contains an
identifier, normalized name, lifecycle state, version, and timestamps.

States are `active` and `archived`.

- Names are unique across all cellars after case folding and whitespace
  normalization.
- `active -> archived` is allowed only when the cellar holds no open runs and
  no vessels still associated with a run, so archiving never strands
  half-finished work.
- An archived cellar remains readable but rejects every write inside its
  namespace.

### Formula

A formula contains an identifier, its cellar identifier, normalized name,
style label, target original gravity, target final gravity, maximum
fermentation temperature, minimum and maximum planned days, ordered process
steps, lifecycle state, version, and timestamps.

States are `draft`, `approved`, and `retired`.

- Names are unique within one cellar after case folding and whitespace
  normalization; different cellars may reuse the same name.
- Target original gravity is in the inclusive range 1.030 to 1.120.
- Target final gravity is positive, lower than target original gravity, and
  differs from it by at least 0.005.
- Maximum fermentation temperature is in the inclusive range 5 to 40 Celsius.
- Planned duration is 1 to 120 days and the minimum cannot exceed the maximum.
- A formula has 1 to 24 process steps.
- Step sequence values must be contiguous and start at 1.
- Step temperatures are in the inclusive range 5 to 90 Celsius.
- Step hold times are 1 to 1440 minutes.
- `draft -> approved` requires a complete valid step list.
- An approved formula is immutable.
- `approved -> retired` is allowed only when no run currently uses it.

### Vessel

A vessel contains an identifier, its cellar identifier, display code unique
within the cellar, capacity in liters, lifecycle state, active run identifier,
version, and timestamps.

States are `available`, `reserved`, `in_use`, `cleaning`, and `retired`.

- Capacity is in the inclusive range 10 to 5000 liters.
- Only an available vessel can be reserved.
- A reserved or in-use vessel must identify exactly one active run.
- Completion moves an in-use vessel to cleaning.
- Cleaning can be finished only when the associated run is complete or
  aborted.
- Finishing cleaning releases the vessel to available.
- A vessel with an active run cannot be retired or directly reserved.

### FermentationRun

A run contains an identifier, its cellar identifier, a run code unique within
the cellar, an approved formula identifier from the same cellar, an optional
vessel identifier from the same cellar, planned volume, notes, lifecycle
state, timetable, ordered observations, final metrics, version, and
timestamps.

States are `planned`, `reserved`, `fermenting`, `conditioning`, `completed`,
and `aborted`.

- Planned volume is greater than zero and cannot exceed vessel capacity.
- Only an approved formula may be attached to a run.
- `planned -> reserved` atomically reserves an available vessel.
- `reserved -> fermenting` requires that the vessel still belongs to the run.
- `fermenting -> conditioning` requires at least two observations.
- `conditioning -> completed` requires at least three observations.
- Completion computes apparent attenuation and estimated alcohol by volume from
  the formula target original gravity and the last observation.
- Completion moves the vessel to cleaning without losing its run association.
- A non-completed run can be aborted. Aborting before fermentation releases an
  available vessel; aborting after fermentation schedules the vessel for
  cleaning.
- Completed and aborted runs are immutable.
- A run can carry at most 500 observations.

### GravityObservation

An observation contains a run identifier, contiguous sequence number, observed
time, gravity, temperature, optional note, and receipt time.

- Gravity is in the inclusive range 0.980 to 1.200.
- Temperature is in the inclusive range -5 to 60 Celsius.
- Observed time begins at or after the run start.
- Observation times strictly increase within a run.
- Gravity must not rise more than 0.010 above the immediately preceding value.
- Observation sequences are contiguous and start at 1.

## Connected Workflows

### 1. Formula Approval

Public entry points are:

- `POST /v1/cellars`
- `POST /v1/cellars/{cellar}/formulas`
- `PUT /v1/cellars/{cellar}/formulas/{id}/steps`
- `POST /v1/cellars/{cellar}/formulas/{id}/approve`
- `GET /v1/cellars/{cellar}/formulas/{id}`

The client creates a cellar, creates a draft inside it, sets its process step
list atomically, approves it, and reads the approved record. Invalid ranges,
duplicate names, empty steps, and repeated approval return stable error codes
without partial writes.

This path crosses HTTP protocol handling, application orchestration, domain
rules, and snapshot storage.

### 2. Vessel Run Lifecycle

Public entry points are:

- `POST /v1/cellars/{cellar}/vessels`
- `POST /v1/cellars/{cellar}/runs`
- `POST /v1/cellars/{cellar}/runs/{id}/reserve`
- `POST /v1/cellars/{cellar}/runs/{id}/start`
- `GET /v1/cellars/{cellar}/runs/{id}`

The client registers a vessel, creates a planned run against an approved
formula, reserves the vessel, starts fermentation, and reads the run. Capacity
conflicts, an unavailable vessel, duplicate run codes, and stale versions
produce stable failures without changing vessel or run state.

This path crosses HTTP protocol handling, application orchestration, domain
rules, storage coordination, and resource lifecycle.

### 3. Observations And Completion

Public entry points are:

- `POST /v1/cellars/{cellar}/runs/{id}/observations`
- `POST /v1/cellars/{cellar}/runs/{id}/conditioning`
- `POST /v1/cellars/{cellar}/runs/{id}/complete`
- `GET /v1/cellars/{cellar}/runs/{id}`

The client appends three ordered observations, starts conditioning, completes
the run, and reads the calculated result. Non-monotonic times, rising gravity,
out-of-range values, excessive sequences, incomplete data, and repeated
transitions return stable failures without changing persisted state.

This path crosses HTTP protocol handling, application orchestration, numeric
domain rules, and snapshot storage.

### 4. Cellar Namespaces

Public entry points are:

- `POST /v1/cellars`
- `POST /v1/cellars/{id}/archive`
- `GET /v1/cellars/{id}/export`
- `GET /v1/cellars/{cellar}/formulas`
- `GET /v1/cellars/{cellar}/vessels`
- `GET /v1/cellars/{cellar}/runs`

Two cellars reuse identical formula names, vessel codes, and run codes without
conflict. Lists, details, and exports only contain the addressed cellar's
data; cross-cellar identifiers answer `404`. A run cannot attach another
cellar's formula or vessel. Archiving races a concurrent write with exactly
one winner and never leaves an archived cellar holding open work, and a
service restart keeps every historical run in its original cellar.

This path crosses HTTP protocol handling, application orchestration, domain
rules, storage coordination, concurrency, and snapshot recovery.

## Modules

- `cmd/fermentctl`: executable entry point for the HTTP service and bounded
  workflow checks.
- `internal/config`: environment and command-line configuration.
- `internal/clock`: deterministic clock boundary for production and checks.
- `internal/domain`: entities, validation, state transitions, and metrics.
- `internal/service`: use-case orchestration and revision coordination.
- `internal/storage`: snapshot repository, atomic replacement, and locking.
- `internal/httpapi`: routing, strict JSON decoding, error mapping, and health.
- `internal/workflowcheck`: real HTTP workflow checks used by the executable.

The dependency direction is:

`cmd -> workflowcheck/httpapi -> service -> domain`

`service -> storage`

`storage -> domain`

The domain package does not import outer packages.

## API Behavior

All business routes are JSON under `/v1`. A successful write returns the
updated aggregate and its version. A failed write returns:

```json
{
  "error": {
    "code": "stable_machine_code",
    "detail": "human-readable explanation"
  }
}
```

Write requests may include `X-Expected-Version`. When present, the service
rejects a stale write instead of overwriting newer state.

`GET /healthz` returns process health and the current snapshot revision.

## Persistence And Concurrency

The configured data directory contains `state.json`. Writes are serialized by
an in-process mutex, so cellar creation, archiving, and entity writes never
interleave into a cross-space partial state. The complete state is encoded,
committed to durable storage, and then atomically renamed over the target. An
update is not published in memory unless persistence succeeds.

Reads use copied values and never expose repository-owned maps or slices. The
default address is `127.0.0.1:8088`, the request body limit is 1 MiB, and all
workflow checks use temporary directories.

Every entity persists its cellar identifier, and uniqueness indexes are keyed
per cellar. Startup normalization rejects entities that reference an unknown
cellar or point across cellar boundaries. Snapshots written before namespaces
existed migrate into a deterministic `cellar_legacy` cellar.

## Validation Plan

The baseline declares `testing: deferred` and intentionally contains no unit
tests, fixtures, integration tests, or browser E2E files. A later verification
task owns systematic tests for validation boundaries, state transitions,
numeric calculations, stale revisions, storage failures, and concurrent
reservation attempts.

The current build command is `go build ./...`. Four bounded production checks
start the real HTTP handler, issue real JSON requests, check success and failure
behavior, and exit without external services:

- `go run ./cmd/fermentctl check formula-approval`
- `go run ./cmd/fermentctl check vessel-run-lifecycle`
- `go run ./cmd/fermentctl check observations-completion`
- `go run ./cmd/fermentctl check cellar-namespaces`

## Scope Limits

This service does not control physical equipment, connect to a remote identity
provider, send notifications, expose a browser interface, replicate data, or
replace laboratory measurements. The API is intended for a trusted local
network or an internal adapter.
