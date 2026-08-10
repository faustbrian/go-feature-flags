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
| Concurrent evaluator, provider, cache, refresh, watcher delivery, and shutdown access | `TestCachedProviderIsRaceSafeDuringRefreshUpdateAndShutdown`, `TestFleetConcurrentEvaluationActivationAndInvalidation`, `TestFleetWatcherDeliversCausalInvalidationsAndJoinsShutdown`, `make race`, `TestNoGoroutineLeaks` |
| Fleet bootstrap, immutable activation, stale policy, and provider outage | `TestFleetBootstrapUsesValidatedPrimarySnapshot`, `TestFleetBootstrapDefinesEmptyStaleMalformedAndUnavailableSources`, `TestFleetRefreshNeverPartiallyActivatesMalformedReplacement` |
| Duplicate, delayed, reordered, lost, future-clock, and cross-revision invalidation | `TestFleetInvalidationClassifiesGapsDuplicatesReorderingAndRevisions`, `TestFleetConvergenceDeadlineUsesBoundedLocalReceiptTime`, and the Kubernetes fleet simulation |
| Refresh storms, provider amplification, waiters, cache deadlines, and shutdown | `TestFleetRefreshCoalescesProviderLoadAndBoundsWaiters`, `TestFleetExecutorCannotExceedProviderLoadBudget`, `TestFleetKubernetesConcurrentColdPodsBoundSharedOverloadAndRecover`, `TestFleetStartJittersRefreshAndShutdownJoinsRefresher`, `TestFleetShutdownCancelsAndJoinsRefreshCacheWork`, race and leak gates |
| Security-sensitive degraded behavior | `TestFleetRejectsUnsafeSecurityPolicy`, `TestFleetDegradedPoliciesPreserveSecurityAndStalenessBoundaries`, the `TestFleetSecuritySensitiveLastKnownGoodAcross*` matrix, and watcher failure tests cross every bounded failure class with fresh, degraded, maximum, and expired last-known-good windows |
| Management validation, optimistic concurrency, audit, groups, and import | The shared `featureflagstest.RunProvider` suite runs against memory, PostgreSQL, and Valkey |
| OpenFeature context, values, defaults, reasons, lifecycle, and events | The `openfeature` test package covers all compatible types, mapped facts and reasons, default preservation, fixed-tenant context, silent event behavior, and concurrent shutdown |
| OpenFeature native capability loss | `TestProviderMakesDecimalCapabilityLossExplicit` and `docs/openfeature.md` document decimal, management, groups, dependencies, staging, audit, cache, health, and event-stream limitations |

The language-neutral bucketing fixture at `testdata/bucketing-v1.json` freezes
the complete digest and bucket, not only an implementation-specific result. It
includes UTF-8, empty input, tenant separation, and length-framing ambiguity.

From the repository root, the release proof is
`make check MODULES=pkg/feature-flags`. It runs the canonical gate list,
including exact coverage and exhaustive mutation, race, fuzz, provider
interoperability, API, documentation, benchmark, vulnerability, secret,
license, and SBOM checks. Package-local `make check-all` is a faster source
gate and does not replace the repository release or provider gates.

The canonical benchmark gate runs equivalent in-memory behavior with
`-benchmem` and a fixed 100 ms minimum sample per benchmark. It publishes
`ns/op`, implied operations per second, bytes per operation, allocations per
operation, Go version, OS, and architecture in the gate artifact. Release
comparisons use at least ten independent `-count=10` samples from the same
corpus and environment and compare them with `benchstat`; a single benchmark
run is smoke evidence, not a statistically significant regression claim.

Feature flags remain product-rollout inputs, never authorization decisions.
No passing evaluator result weakens or replaces an independent authorization
check.
