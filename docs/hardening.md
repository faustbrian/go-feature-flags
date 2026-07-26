# Hardening evidence

This matrix ties the release-blocking feature-flag risks to executable evidence.
All commands run against the current source; integration and coverage require
the PostgreSQL and Valkey environment variables documented in the README.

| Risk | Executable evidence |
|---|---|
| Native value types, lifecycle defaults, variants, and strict typing | `TestSnapshotEvaluatesEveryNativeValueTypeWithoutCoercion`, `TestValueAccessAndEqualityCoverEveryType`, `TestEveryTypedEvaluatorRejectsWrongDefinitionType` |
| Strategy truth tables, time boundaries, schedules, and missing input | `TestStrategyValidationAndScheduleTruthTable`, `TestTimeWindowStrategyUsesExplicitHalfOpenBoundaries`, `TestScheduleStrategyEvaluatesInConfiguredTimeZone`, `TestFactAndPercentageStrategiesCoverMissingInput` |
| Ordered feature and group precedence | `TestFeatureStrategiesUseFirstMatchPrecedence`, `TestGroupStrategiesTakePrecedenceOverFeatureStrategies`, mutation cases `strategy_precedence` and `group_precedence` |
| Stable unbiased tenant bucketing | `TestBucketPortableCompatibilityVectors`, `TestPercentageStrategyKeepsTenantAssignmentsIndependent`, `FuzzBucketIsStableAndBounded`, mutation cases `bucket_boundary` and `rollout_boundary` |
| Dependency and group cycles or excessive depth | `TestNewSnapshotRejectsDependencyCycle`, `TestNewSnapshotWithGroupsRejectsInheritanceCycle`, `TestSnapshotConstructionRejectsEveryGraphFailure`, `TestGroupConstructionRejectsEveryInvalidShape` |
| Huge definitions, contexts, imports, batches, and diagnostics | `TestDefinitionValidationRejectsEveryBoundedCollectionAndReference`, `TestContextValidationRejectsEveryCardinalityAndSizeBound`, `TestSnapshotBatchEvaluatesMixedTypesWithinConfiguredBound`, `FuzzDefinitionValidationNeverPanics`, `FuzzContextEvaluationNeverPanics`, `FuzzImportNeverPanics` |
| Cross-tenant evaluation or management | `TestMemoryProviderUpdateIsTenantScopedAndOptimistic`, shared provider conformance `tenant isolation`, OpenFeature cross-tenant context test, mutation case `tenant_binding` |
| Atomic snapshots and split-brain provider updates | `TestSnapshotsRemainConsistentDuringConcurrentUpdates`, shared provider conformance `immutable snapshots and audit`, `TestDurableProviderSharesAtomicStateAcrossInstances` |
| Stale cache, provider outage, and clock rollback | `TestCachedProviderFailOpenIsBoundedByOutageStaleness`, `TestCachedProviderFailClosedAndMutationErrorsPreserveState`, `TestCacheConfigurationAndEvictionBoundaries` |
| Concurrent evaluator, provider, cache, refresh, and shutdown access | `TestCachedProviderIsRaceSafeDuringRefreshUpdateAndShutdown`, `make race`, `TestNoGoroutineLeaks` |
| Management validation, optimistic concurrency, audit, groups, and import | The shared `featureflagstest.RunProvider` suite runs against memory, PostgreSQL, and Valkey |
| OpenFeature context, values, defaults, reasons, lifecycle, and events | The `openfeature` test package covers all compatible types, mapped facts and reasons, default preservation, fixed-tenant context, silent event behavior, and concurrent shutdown |
| OpenFeature native capability loss | `TestProviderMakesDecimalCapabilityLossExplicit` and `docs/openfeature.md` document decimal, management, groups, dependencies, staging, audit, cache, health, and event-stream limitations |

The language-neutral bucketing fixture at `testdata/bucketing-v1.json` freezes
the complete digest and bucket, not only an implementation-specific result. It
includes UTF-8, empty input, tenant separation, and length-framing ambiguity.

The release proof is `make check-all`. It includes format and module checks,
tests, the race detector, exact production coverage, all four fuzz targets,
targeted mutation cases for strategy, precedence, bucketing, dependency,
default, tenant, grouping, batching, and scheduling, leak detection,
benchmarks, documentation, workflow validation, lint, static analysis, and
vulnerability scanning. `make integration` separately makes the shared
provider conformance run explicit.

Feature flags remain product-rollout inputs, never authorization decisions.
No passing evaluator result weakens or replaces an independent authorization
check.
