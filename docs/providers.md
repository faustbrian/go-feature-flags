# Providers, caching, and operations

[Documentation index](README.md)

## Shared contract

`Provider` supplies atomic CRUD, optimistic concurrency, snapshots, groups,
audit, deterministic import/export, staged changes, scheduled application,
cleanup, health, and shutdown. `Capabilities` lets adapters advertise these
semantics. Tenant is required on every operation.

`MemoryProvider` is process-local. `postgres.New` stores one versioned tenant
document per row and uses compare-and-swap updates. `valkey.New` stores the
same document behind a namespaced, SHA-256 tenant key and executes compare-
and-swap in Lua. Both durable providers retry bounded storage conflicts by
reloading the entire tenant document. Constructors retain explicit limits
without normalizing them. Use DefaultLimits; memory limits do not bound tenant
count or audit Actor bytes. Memory Close is a context-only no-op; durable
Close delegates and bundled backends do not close application-owned DB/Valkey
clients.

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
`MaxOutageStaleness`; `FailClosed` returns the provider error. Tenant count
and features per tenant are bounded, and deterministic oldest-entry eviction
is used. Successful mutations invalidate the affected local tenant entry and
fence earlier reads from republishing it. Overlapping Refresh calls are not
coalesced, but a later successful refresh cannot be overwritten by an earlier
admitted read. Failed refreshes and mutations do not discard another successful
publication; unrelated tenants retain independent publication state. Ordering
metadata exists only while tenant refreshes are active, not as retained history
for every tenant ever seen. Provider IO and clock callbacks run outside the
cache lock. Close clears entries and fences old reads, then delegates; it does
not reject subsequent cache access or prevent new refreshes.

Tenant keys are bounded in bytes before cache lookup or provider delegation.
Set `CacheConfig.MaxTenantBytes` explicitly for custom providers, or expose a
positive `TenantByteLimit() int` capability. Memory and durable providers and
nested caches expose their effective bound. Zero inherits a discoverable bound;
a positive explicit value may tighten but cannot relax that bound.

Do not use fail-open for a flag whose stale enabled state would be unsafe.
Feature flags remain unsuitable for authorization regardless of cache policy.

`ProviderSnapshotLoader` derives revisions through the portable export codec.
The adapter stamps successful fetch time, not authoritative commit time, and
its canonical export digest includes ordered group attachments. Providers that
need custom nonportable strategies or commit timestamps should implement
`SnapshotLoader` directly so revision, provenance, and source age remain
provider-owned.

## Staging, audit, and cleanup

`StageUpdate` records an expected feature version and optional application
time. `ApplyStage` or `ApplyScheduled` applies it atomically; a conflicting
intervening update prevents stale application. Scheduling is explicit: the
application owns its ticker, cancellation, and shutdown join.

Audit entries record only key, action, actor, and version. They deliberately
do not retain context or feature values. The audit count bound evicts oldest
entries; Actor bytes are caller-bounded and not automatically redacted.
`Cleanup` only performs operations selected by the caller: purging deleted
features, discarding expired stages, or retaining a bounded number of audits.

## Import and export

Documents use deterministic versioned JSON. Features and groups are sorted;
strategy and group attachment order is preserved because it controls precedence.
The same definition codec preserves this order in durable tenant documents.
Documents previously exported or stored with sorted attachments cannot recover
the original order automatically; republish the intended order from its source
when that prior normalization changed targeting. Import supports
dry-run and explicit fail, skip, or replace conflict behavior. Import/stored-
state decoding checks byte lengths first, but backend replies and encoding
allocate before durable size validation. Unknown struct fields and trailing
JSON fail; duplicate keys retain encoding/json semantics. Export itself has no
output cap. These are wire/count bounds, not total heap/CPU limits.

Back up durable state before a format migration. Readers reject unknown format
versions instead of guessing.

## Health and failure handling

Health codes are low-cardinality and contain no tenant or context data. Treat a
failed health result as provider unavailability, not as an evaluation answer.
Applications decide whether to use a bounded cache, a local default, or return
an error. Do not silently convert storage failure into an enabled flag.

For multi-replica operation, use `ProviderSnapshotLoader` and `Fleet`. Fleet
startup, invalidation, resilience composition, bounded convergence, and
shutdown are specified in [fleet.md](fleet.md).
