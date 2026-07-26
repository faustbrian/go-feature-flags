# Goal: Advanced Feature Flag And Rollout Engine

## Objective

Build `feature-flags` as the full Toggl-style feature management engine for
Go. Its native API MUST support richer policies than OpenFeature. OpenFeature
is an optional interoperability adapter, not the product boundary or a ceiling
on functionality.

## Native Feature Model

- Stable feature definitions with key, type, default, variants, metadata,
  owner, lifecycle, dependencies, groups, tags, and version.
- Boolean, string, integer, float/decimal, and structured variants with strict
  typed evaluation.
- Explicit evaluation context with subject, tenant, environment, attributes,
  time, and caller-defined typed facts under cardinality/privacy limits.
- Strategies for exact targeting, percentage rollout, allow/deny sets, dates,
  schedules, time bombs, prerequisites, dependencies, groups, cascades,
  inheritance, and custom deterministic evaluation.
- Stable hashing and bucketing so rollout assignments do not shift accidentally.
- Batch evaluation and immutable snapshots for one-request consistency.
- Evaluation detail containing value, variant, reason, matched strategy,
  version, and safe diagnostics.

## Management And Persistence

- Memory, PostgreSQL, and Valkey providers with one shared conformance contract.
- Atomic create/update/activate/deactivate/delete/restore and optimistic
  concurrency.
- Group membership, feature dependencies, snapshots, audit history, staged
  changes, scheduled activation, and cleanup.
- Import/export with versioned deterministic formats and dry-run/conflict
  reporting.
- Cache and refresh policies with bounded staleness, explicit fail-open or
  fail-closed behavior, and observable provider health.
- No mandatory hosted control plane or global singleton.

## Interoperability

- Optional OpenFeature provider exposing compatible native capabilities through
  the OpenFeature contract.
- Document native features that cannot round-trip through OpenFeature.
- OpenFeature hooks, events, shutdown, and evaluation context mapping MUST not
  alter native semantics.
- Optional HTTP/JSON-RPC management adapters remain separate from core and rely
  on application authentication/authorization.

## Boundaries

- Feature flags are rollout/product controls, never authorization or security
  enforcement.
- `rule-engine` MAY evaluate advanced predicates, but core deterministic
  strategies must not require it.
- `authorization` owns access decisions; `config` owns startup config;
  `settings` owns runtime user/tenant settings.
- No reflection-driven feature discovery, global mutable client, hidden
  refresher, or automatic context scraping.

## Security And Bounds

- Bound features, variants, attributes, rules, dependencies, group size,
  rollout precision, cache, refresh, audit, diagnostics, and batch size.
- Detect dependency cycles and evaluation recursion.
- Prevent tenant/context confusion, unstable bucketing, PII in logs/metrics,
  stale unsafe defaults, and management-plane bypass.
- Every background refresh has explicit ownership, cancellation, and join.

## Verification And Documentation

Require meaningful 100% production coverage, strategy truth tables, stable
bucketing vectors, dependency/group/cascade matrices, provider conformance,
PostgreSQL/Valkey failure tests, snapshot consistency, OpenFeature adapter
compatibility, fuzz, race, mutation, leak, and performance benchmarks.

Document native API, variants, contexts, strategies, rollouts, groups,
dependencies, schedules, snapshots, audit, providers, caching, OpenFeature
adapter and limitations, security, operations, migration from Cline Toggl,
cookbook, FAQ, compatibility, and changelog. CI/local gates follow ecosystem
standards.

## Acceptance Criteria

- Native Toggl-level capabilities are complete and not constrained by
  OpenFeature.
- OpenFeature consumers can use the optional adapter with explicit capability
  mapping.
- Evaluations are deterministic, tenant-safe, snapshot-consistent, and bounded.
- Meaningful 100% coverage and every blocking gate pass.
