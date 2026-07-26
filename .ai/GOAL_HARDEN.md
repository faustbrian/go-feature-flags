# Hardening Goal: Feature Flags

## Objective

Prove native evaluation, rollout bucketing, providers, caching, snapshots,
dependencies, management, audit, and OpenFeature interoperability correct under
concurrency and failure.

## Required Audits

- Exhaust every value type, variant, strategy, context state, missing value,
  default, dependency, group, cascade, schedule, and time boundary.
- Freeze stable bucketing vectors across versions, architectures, and languages.
- Attack dependency cycles, deep rules, huge contexts, cardinality, tenant
  confusion, hash bias, stale caches, provider outages, split-brain updates,
  and clock skew.
- Run one provider conformance suite against memory, PostgreSQL, and Valkey.
- Verify snapshots remain internally consistent during concurrent updates.
- Prove fail-open/fail-closed and default behavior is explicit for every error.
- Differential-test OpenFeature adapter context/value/reason/event mapping and
  document every native capability loss.
- Race evaluators, providers, caches, refreshers, and shutdown; detect leaks.
- Fuzz definitions/imports/contexts; mutation-test strategy, precedence,
  bucketing, dependency, default, and tenant decisions.

## Release Blockers

- Unstable rollout assignment, cross-tenant result, feature flag used as
  authorization, silent native-capability loss, inconsistent snapshot,
  management bypass, unbounded evaluation, stale unsafe fallback, race, or leak.

## Completion Criteria

- Strategy, bucketing, provider, failure, snapshot, OpenFeature, fuzz, race,
  mutation, leak, and benchmark suites pass with meaningful 100% coverage.
