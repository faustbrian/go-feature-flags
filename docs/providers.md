# Providers, caching, and operations

## Shared contract

`Provider` supplies atomic CRUD, optimistic concurrency, snapshots, groups,
audit, deterministic import/export, staged changes, scheduled application,
cleanup, health, and shutdown. `Capabilities` lets adapters advertise these
semantics. Tenant is required on every operation.

`MemoryProvider` is process-local. `postgres.New` stores one versioned tenant
document per row and uses compare-and-swap updates. `valkey.New` stores the same
document behind a namespaced, SHA-256 tenant key and executes compare-and-swap
in Lua. Both durable providers retry bounded storage conflicts.

## Schema and migration

Call `postgres.Backend.Migrate` during an application-owned migration phase.
It creates `feature_flag_tenant_state`; the package does not start a database,
run migrations in the background, or manage credentials.

Valkey requires no schema. Choose a unique `valkey.Config.Prefix` for each
environment. The package does not perform key discovery or `KEYS` scans.

## Cache policy

`CachedProvider` has no goroutine. A caller owns refresh scheduling and calls
`Refresh`. Fresh entries may be returned for `MaxStaleness`. During provider
failure, `FailOpen` may return a previous snapshot only until
`MaxOutageStaleness`; `FailClosed` returns the provider error. Tenant count and
features per tenant are bounded, and deterministic oldest-entry eviction is
used. Successful mutations invalidate the affected tenant.

Do not use fail-open for a flag whose stale enabled state would be unsafe.
Feature flags remain unsuitable for authorization regardless of cache policy.

## Staging, audit, and cleanup

`StageUpdate` records an expected feature version and optional application
time. `ApplyStage` or `ApplyScheduled` applies it atomically; a conflicting
intervening update prevents stale application. Scheduling is explicit: the
application owns its ticker, cancellation, and shutdown join.

Audit entries record only key, action, actor, and version. They deliberately do
not retain context or feature values. The audit bound evicts oldest entries.
`Cleanup` only performs operations selected by the caller: purging deleted
features, discarding expired stages, or retaining a bounded number of audits.

## Import and export

Documents use deterministic versioned JSON. Features and groups are sorted;
strategy order is preserved because it controls precedence. Import supports
dry-run and explicit fail, skip, or replace conflict behavior. Input and stored
state bytes are bounded, unknown fields fail, and trailing JSON is rejected.

Back up durable state before a format migration. Readers reject unknown format
versions instead of guessing.

## Health and failure handling

Health codes are low-cardinality and contain no tenant or context data. Treat a
failed health result as provider unavailability, not as an evaluation answer.
Applications decide whether to use a bounded cache, a local default, or return
an error. Do not silently convert storage failure into an enabled flag.
