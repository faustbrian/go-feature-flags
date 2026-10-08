# Changelog

All notable changes are documented here. The project follows Semantic
Versioning.

## Unreleased

### Changed

- Update the PostgreSQL driver to PGX v5.11.0 while retaining the adapter's
  public DB interface, storage schema, and caller-owned pool lifecycle.
- Review application-owned pool configuration for PGX's libpq-aligned
  connection-string parsing and HOME-based Unix defaults. URI `+` values are
  now literal, repeated parameters use the last value, mixed host/port lists
  are positionally aligned, and keyword values must escape backslashes.

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
