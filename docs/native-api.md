# Native API reference

## Definitions and values

`Definition` owns a stable key, declared type, default value, named variants,
metadata, owner, lifecycle, dependencies, groups, tags, version, and ordered
strategies. Supported values are boolean, string, integer, IEEE-754 float,
exact decimal text, and structured JSON. Evaluation never coerces values.

Definitions in `draft`, `inactive`, or `archived` state return their default.
Active definitions evaluate dependencies, feature groups, then their ordered
strategies. The first match wins. Details include value, variant, reason,
matched strategy, definition version, and bounded safe diagnostics.

## Context

`Context` contains subject, tenant, environment, string attributes, explicit
time, and typed facts. Limits bound counts, key sizes, value sizes, and JSON
payloads. A tenant-bound snapshot rejects a different tenant. Applications
should use opaque subject identifiers and must not place secrets or unnecessary
personal data in context.

## Strategies

- `ExactTargetStrategy` matches configured tenants, subjects, environments,
  and exact string attributes.
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
mismatches. `EvaluateBatch` accepts mixed typed keys and rejects batches above
`Limits.MaxBatchSize`. Snapshots never observe later provider mutations.

## Determinism

Rollout bucketing hashes length-prefixed fields, so concatenation ambiguity is
impossible. Scheduling uses `Context.Time`, and scheduled management changes
use the time explicitly passed to `ApplyScheduled`. The package never reads a
process clock during evaluation.

The portable bucketing contract is version 1: start with the UTF-8 bytes of
`feature-flags/bucket/v1`, append seed, feature key, tenant, and subject in
that order, prefix each UTF-8 field with its unsigned 32-bit big-endian byte
length, then compute SHA-256. Interpret the first eight digest bytes as an
unsigned big-endian integer and reduce it modulo 100000. The language-neutral
vectors in `testdata/bucketing-v1.json` freeze the full digest and assignment,
including empty, Unicode, tenant-isolation, and framing cases.
