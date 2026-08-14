# Changelog

All notable changes will be documented here. The project follows Semantic
Versioning after its first stable release.

## Unreleased

### Documentation

- Link the package README to the repository-wide Golib documentation portal.

### Compatibility

- Added a pinned module export baseline so incompatible public API changes
  fail the canonical repository gate.

- Added strict native values, deterministic strategies, groups, dependencies,
  immutable tenant snapshots, batch evaluation, and safe diagnostics.
- Added memory, PostgreSQL, and Valkey providers with shared conformance,
  optimistic concurrency, audit, staging, cleanup, and import/export.
- Added bounded fail-open or fail-closed caching and an optional OpenFeature
  evaluation adapter.
- Added explicit fleet bootstrap, immutable last-known-good metadata, bounded
  refresh and invalidation convergence, per-flag degraded policy, deterministic
  replica jitter, resilience composition seams, and joined shutdown semantics.
- Added caller-owned invalidation watchers with bounded failure classification
  and shutdown joining, plus concurrent cold-pod overload recovery semantics.
