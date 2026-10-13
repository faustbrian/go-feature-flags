# Native API reference

[Documentation index](README.md)

## Definitions and values

`Definition` owns a stable key, declared type, default value, named variants,
metadata, owner, lifecycle, dependencies, groups, tags, version, and ordered
strategies. Supported values are boolean, string, integer, IEEE-754 float,
exact decimal text, and structured JSON. Evaluation never coerces values.

Any lifecycle other than exactly `active` returns the definition default,
including `deprecated` and unrecognized labels. Validate does not reject
unknown lifecycle labels. Active definitions evaluate dependencies, feature
groups, then their ordered strategies. The first match wins. Details include
value, variant, reason, matched strategy, definition version, and bounded
diagnostics from a selected strategy. Custom diagnostics are truncated, not
sanitized, and diagnostics from strategies that do not match are discarded.

## Context

`Context` contains subject, tenant, environment, string attributes, explicit
time, and typed facts. Limits bound counts, key sizes, value sizes, and JSON
payloads. A tenant-bound snapshot rejects a different tenant. Applications
should use opaque subject identifiers and must not place secrets or unnecessary
personal data in context.

## Strategies

- `ExactTargetStrategy` matches configured tenants, subjects, environments,
  and string attributes. Required attribute keys must be present; an explicit
  empty value matches an empty target, but an absent attribute does not.
- `PercentageStrategy` provides stable 0.001% rollout precision. Keep its seed
  stable to preserve assignments.
- `SetStrategy` applies deny lists before allow lists for tenants and subjects.
- `TimeWindowStrategy` and `TimeBombStrategy` compare the explicit context time.
- `ScheduleStrategy` evaluates weekly local-time windows in an IANA timezone.
- `FactStrategy` performs strict equality against a caller-defined typed fact.
- Applications may implement `Strategy`; `SnapshotStrategy` must deep-copy any
  mutable state. Custom strategies cannot enter portable documents unless a
  future codec explicitly supports their type.

Strategies target named variants. Group strategies are inherited child-first
through the parent chain, then feature strategies run. Group and dependency
cycles, missing targets, and configured depth excesses are rejected.

## Dependencies and groups

A dependency requires another feature to resolve to one named variant. All
dependencies must match before the dependent feature's strategies run.
Snapshots make the entire dependency graph internally consistent.

Groups contain reusable strategies and an optional parent. Features explicitly
join groups. Group create, update, delete, assignment, and removal are atomic;
deleting a referenced or inherited group returns `ErrGroupInUse`.

## Snapshots and batches

Use one snapshot for all evaluations in a request. Typed methods reject type
mismatches. `Batch` accepts mixed typed keys and rejects batches above
`Limits.MaxBatchSize`. Built-in strategy snapshots do not observe later
provider mutations. Custom strategies must preserve immutable behavior and be
safe for concurrent calls; callbacks are synchronous, not panic-isolated or
forcibly deadline-bounded. Context maps are borrowed during evaluation.

## Determinism

Rollout bucketing hashes framed fields using the frozen version-1 mapping.
Native evaluation bounds input sizes; the standalone Bucket helper accepts
arbitrary strings and encodes only the low 32 bits of each byte length.
Scheduling uses `Context.Time`, and scheduled management changes use the time
explicitly passed to `ApplyScheduled`. Native Snapshot evaluation never reads
a process clock; Fleet evaluation uses its explicit Clock for snapshot age.

The portable bucketing contract is version 1: start with the ASCII bytes of
`go-feature-flags/bucket/v1`, append seed, feature key, tenant, and subject in
that order, prefix each raw string byte field with its low 32-bit big-endian
byte length, then compute SHA-256. Interpret the first eight digest bytes as
an unsigned big-endian integer and reduce it modulo 100000. The language-
neutral vectors in `testdata/bucketing-v1.json` freeze the full digest and
assignment, including empty, Unicode, tenant-isolation, and framing cases.
