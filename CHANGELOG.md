# Changelog

All notable changes will be documented here. The project follows Semantic
Versioning after its first stable release.

## Unreleased

### Compatibility

- Added a pinned module export baseline so incompatible public API changes
  fail the canonical repository gate.

- Added strict native values, deterministic strategies, groups, dependencies,
  immutable tenant snapshots, batch evaluation, and safe diagnostics.
- Added memory, PostgreSQL, and Valkey providers with shared conformance,
  optimistic concurrency, audit, staging, cleanup, and import/export.
- Added bounded fail-open or fail-closed caching and an optional OpenFeature
  evaluation adapter.
