# Changelog

All notable changes are documented here. The project follows Semantic
Versioning.

## Unreleased

## 2.0.1 - 2026-10-13

Recorded as a prepared patch candidate on this date; public availability
requires the v2.0.1 tag and release.
The frozen source range is public v2.0.0 (`5a1a5e27e11c1dcd449ca86119ef6fc4345e131d`)
through `2757b4b96ec08572502a8702c385582701d3532f`. Final source-accountability
alignment is outside that source range.

### Fixed

- Require exact-target attribute keys to be present, distinguishing missing
  attributes from explicitly supplied empty values.
- Preserve native group attachment precedence in portable exports, durable
  tenant documents, and provider-loader revision identity. Previously sorted
  artifacts cannot recover their original intended order automatically; republish
  that order from its source when the old normalization changed targeting.
- Prevent earlier cache reads from republishing after successful local mutation
  invalidation or overwriting a later successful refresh. Failed or unrelated
  work does not discard a valid publication; refreshes remain caller-owned and
  are not coalesced.

### Changed

- Clarify evaluation precedence, callback and snapshot ownership, cache/fleet
  lifecycle, provider limits and portable document ordering without runtime changes.

- Update the OpenFeature SDK to v1.18.0 while preserving the adapter's
  fixed-tenant evaluation, typed details, hooks, and native provider ownership.
  SDK client/provider binding and event shutdown use the updated SDK;
  applications continue to own SDK registration and lifecycle.
- Update the PostgreSQL driver to PGX v5.11.0 while retaining the adapter's
  public DB interface, storage schema, and caller-owned pool lifecycle.
- Review application-owned pool configuration for PGX's libpq-aligned
  connection-string parsing and HOME-based Unix defaults. URI `+` values are
  now literal, repeated parameters use the last value, mixed host/port lists
  are positionally aligned, and keyword values must escape backslashes.

### Source accountability

Each non-merge commit in the frozen range is accounted for exactly once:

- Targeting, attachment precedence and cache publication repair:
  [4e64e8fbc9](https://github.com/faustbrian/go-feature-flags/commit/4e64e8fbc9e2ab317148168601c51a0095b08a41).
- OpenFeature SDK v1.18.0 adoption and real-SDK client/hook lifecycle coverage:
  [9c15bc32be](https://github.com/faustbrian/go-feature-flags/commit/9c15bc32be7f477b652303e46406ef94e6bb73d1).
- PGX v5.11.0 adoption and application-owned connection configuration guidance:
  [b0f89ba46d](https://github.com/faustbrian/go-feature-flags/commit/b0f89ba46de56c9d0ea7d6fe741933ebed275541).
- Snapshot, provider and fleet contract documentation:
  [c42ef0a593](https://github.com/faustbrian/go-feature-flags/commit/c42ef0a5939c769c4e3df40ec94952cbc9dba2a3).
- Adoption and documentation navigation:
  [4d9632c279](https://github.com/faustbrian/go-feature-flags/commit/4d9632c2791b2ffdb8583ff4921478b01e2d573a).
- Scanner conversion and checksum-purpose comments, without runtime changes:
  [3da86f9463](https://github.com/faustbrian/go-feature-flags/commit/3da86f9463c0fb4e7aec44ed46969e725304aeee).
- Shared workflow update, patched documentation tooling, bounded verification,
  development policy, supply-chain tooling and source-documentation enforcement:
  [45d06306e9](https://github.com/faustbrian/go-feature-flags/commit/45d06306e92b57603474c9621a8554bccf619ecc),
  [5329d7530e](https://github.com/faustbrian/go-feature-flags/commit/5329d7530e509d30f62789e1e6d19250f2495156),
  [06b2730808](https://github.com/faustbrian/go-feature-flags/commit/06b27308086c76f9f9e679a144fe990062df0bb3),
  [4fb344e9cb](https://github.com/faustbrian/go-feature-flags/commit/4fb344e9cb6333c6e70d46fe3432771ae6a20b05),
  [e7af09251f](https://github.com/faustbrian/go-feature-flags/commit/e7af09251f02b9c9a99bd4fb5d1447d0a7d50950),
  [fbed7204b0](https://github.com/faustbrian/go-feature-flags/commit/fbed7204b0e85c9fd29f7dd59872bf885368574d).

- Patch metadata and adoption guidance:
  [b2380c6de8](https://github.com/faustbrian/go-feature-flags/commit/b2380c6de8c53f13e4e2372a5cb426974c96af6c).
- Lazy refresh tracking, mutation fencing and completed-refresh lifetime
  regression coverage, with the narrow equivalent publication comparison:
  [460b92ec5b](https://github.com/faustbrian/go-feature-flags/commit/460b92ec5bf28edbed19b85eac28fcc878236abe),
  [2757b4b96e](https://github.com/faustbrian/go-feature-flags/commit/2757b4b96ec08572502a8702c385582701d3532f).

## 2.0.0 - 2026-09-27

### Breaking changes

- Move the root module and all public package imports to
  `github.com/faustbrian/go-feature-flags/v2` and require Go 1.27.0.
- Require an explicit positive `CacheConfig.MaxTenantBytes` when the wrapped
  provider does not expose a positive `TenantByteLimit()`. Built-in providers
  and nested caches expose their limit; a cache cannot relax that limit.
- Keep rejection of custom structured encoders and nested `json.RawMessage`
  rather than restoring these formerly accepted v1 inputs. Migration guidance
  is in [docs/migration.md](docs/migration.md). Stored document formats remain
  unchanged.

The prepared 1.0.1 release was not published and is superseded by this major.

### Security

- Reject oversized tenant partitions before memory retention, cache lookup, or
  durable backend work, and bound OpenFeature flattened contexts before native
  snapshot loading or structured JSON encoding.
- Reject typed-nil native OpenFeature providers and unbounded custom structured
  encoders, and document the package threat model and accepted residual risks.
- Reject typed-nil cache providers before tenant-limit discovery.

### Changed

- Require Go 1.27.0 across module, development, CI, and compatibility
  metadata.
- Adopt the checksum-verified `go-library-tools` v1.4.0 CLI and immutable W14
  reusable workflow, including strict online specification validation, without
  changing the feature-flag API or runtime behavior.
- Replace copied repository tooling with the checksum-pinned shared contract
  while retaining package-owned policy and verification evidence.
- Publish schema-v2 cohesion metadata and a repository-local cohesion gate
  through the immutable shared workflow.
- Preserve the approved mutation checkpoints under `.verification` for the
  shared content-addressed evidence workflow.

### Documentation

- State the supported-Go contract, provide a compiler-checked
  quick start, and expose migration, support, troubleshooting, security, and
  license entry points.
- Validate the current verification guide instead of the removed hardening
  document in the documentation gate.
- Add the canonical module installation command and direct compatibility and
  release-history navigation to the root README.
- Link ecosystem and Persistence and durability family guidance to the
  immutable v1.4.0 documentation release and correct the stable-release
  compatibility statement.
- Replace archived monorepo and hardening terminology with package-owned
  documentation and verification guidance.

## 1.0.0 - 2026-08-25

### Compatibility

- Regenerate the exported API baseline with the repository's Go 1.26
  toolchain so structured JSON values retain their intended stable identity.

### Changed

- Exclude intentional nested modules from root local-proxy archives so local,
  bootstrap, CI, and public module checksums describe the same source
  boundary.

- Track the pinned documentation-tool lockfile so clean CI checkouts install
  the exact validated cspell dependency.

- Reconcile standalone dependency checksums against deterministic current
  module archives so CI, local verification, and release consumers resolve
  identical content.

- Harden standalone documentation validation with deterministic spelling and
  link checks, package-specific documentation gates, and repository-local
  contributor guidance.

### Changed

- Publish the module from its standalone `github.com/faustbrian/go-feature-flags` identity while preserving its documented API and behavior.

### Documentation

- Link the package README to package-owned documentation.

### Security

- Upgrade `golang.org/x/text` to v0.41.0 and `golang.org/x/sys` to v0.47.0 so
  the dependency graph no longer contains GO-2026-5970 or GO-2026-5024.

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
