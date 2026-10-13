# Migration

[Documentation index](README.md)

## From v1 to v2

Use Go 1.27 or newer and change native, OpenFeature, PostgreSQL, Valkey, and
test-support imports to `github.com/faustbrian/go-feature-flags/v2` and its
package suffixes. The production source remains at the root of main.

`CachedProvider` inherits a wrapped provider's positive `TenantByteLimit()`
when discoverable. Otherwise, provide an explicit positive
`CacheConfig.MaxTenantBytes`. An explicit smaller limit tightens the cache;
a larger limit cannot relax the wrapped provider's bound. Custom wrappers
must expose this capability or set the cache limit explicitly. Limits count
bytes, not Unicode characters, and are enforced before cache access or
provider delegation.

OpenFeature rejects custom JSON/text encoders and nested `json.RawMessage`
without calling custom encoders. Convert these inputs into bounded supported
maps, slices, scalars, or a valid bounded top-level `json.RawMessage` instead.
Tenant document formats, PostgreSQL schema, Valkey key namespaces, and
deterministic bucketing are unchanged; the major version is an API and input
acceptance boundary, not a storage migration.

## v2.0.1 patch adoption

The prepared v2.0.1 patch keeps the `/v2` imports, Go 1.27 language floor,
tenant document versions, PostgreSQL schema and Valkey namespaces. Select it
explicitly after its public tag and module artifacts are available; metadata
preparation alone does not publish the module.

Missing exact-target attributes no longer match an explicitly empty target.
Review callers that previously relied on absent attributes being treated as empty.
Native group attachment order now survives export, durable storage and loader
revision identity. If older sorting changed intended targeting precedence,
recover that order from its authoritative source and republish the affected
artifacts. Upgrading cannot reconstruct an order already lost by sorting;
verify the resulting decisions before rollout.

Earlier cache reads cannot republish after successful local mutation invalidation
or overwrite a later successful refresh. This is local publication ownership,
not fleet-wide convergence or refresh coalescing. Applications retain callback,
provider, OpenFeature SDK registration and durable backend lifecycle ownership.
The patch also adopts the SDK and PGX versions described in the
[changelog](../CHANGELOG.md#201---2026-10-13); review PGX pool configuration there.

## From Cline Toggl

Inventory each Toggl feature, its type, variants, activation state, strategies,
groups, and prerequisites. Assign a stable native key and rollout seed. Convert
implicit request data into explicit `Context` fields or typed facts.

Export the target native document and run `ImportDocument` with `DryRun: true`.
Resolve every conflict explicitly, then import with fail-on-conflict for the
cutover. Compare old and native decisions in shadow traffic without using
either result for authorization. Freeze bucketing vectors for representative
subjects before increasing rollout percentages.

OpenFeature migration is evaluation-only. Keep management operations on the
native provider and document decimal and policy capability losses before
moving an OpenFeature consumer.

During cutover, acquire one native snapshot per request, monitor low-cardinality
reason and error counts, and retain an application-owned rollback path to the
previous evaluator. Never dual-write without explicit conflict ownership.
