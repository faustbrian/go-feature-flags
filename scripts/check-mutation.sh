#!/usr/bin/env bash
set -euo pipefail

workspace=$(mktemp -d)
trap 'rm -rf "$workspace"' EXIT
baseline="$workspace/baseline"
mutation_cache="$workspace/gocache"
mkdir -p "$baseline"
tar --exclude=.git --exclude=coverage.out -cf - . | tar -xf - -C "$baseline"

run_mutant() {
	local name=$1 file=$2 from=$3 to=$4 pattern=$5
	local mutant="$workspace/$name"
	mkdir -p "$mutant"
	tar -cf - -C "$baseline" . | tar -xf - -C "$mutant"
	FROM="$from" TO="$to" perl -0pi -e '
$changed = s/\Q$ENV{FROM}\E/$ENV{TO}/;
END { die "mutation source not found: $ENV{FROM}\n" unless $changed }
' "$mutant/$file"
	if (cd "$mutant" && GOCACHE="$mutation_cache" GOWORK=off go test . -run "$pattern" -count=1 >mutation.log 2>&1); then
		echo "survived mutation: $name" >&2
		cat "$mutant/mutation.log" >&2
		exit 1
	fi
	printf 'killed mutation: %s\n' "$name"
	rm -rf "$mutant"
}

run_mutant bucket_boundary bucket.go '% bucketPrecision' '% (bucketPrecision - 1)' 'TestBucket'
run_mutant rollout_boundary strategy.go '< s.Threshold' '<= s.Threshold' 'TestPercentage'
run_mutant set_deny strategy_sets.go 'denied := listed(s.DenyTenants, input.Context.Tenant) || listed(s.DenySubjects, input.Context.Subject)' 'denied := listed(s.DenyTenants, input.Context.Tenant) && listed(s.DenySubjects, input.Context.Subject)' 'TestSetStrategy'
run_mutant dependency_match evaluate.go 'result.variant != dependency.RequiredVariant' 'result.variant == dependency.RequiredVariant' 'TestDependency'
run_mutant tenant_binding evaluate.go 'context.Tenant != s.tenant' 'context.Tenant == s.tenant' 'TestMemoryProviderUpdateIsTenantScoped'
run_mutant batch_limit evaluate.go 'len(requests) > s.limits.MaxBatchSize' 'len(requests) < s.limits.MaxBatchSize' 'TestSnapshotBatch'
run_mutant group_cycle evaluate.go 'if visiting[current]' 'if false && visiting[current]' 'TestNewSnapshotWithGroupsRejectsInheritanceCycle'
run_mutant stage_due staged.go '!change.ApplyAt.After(now)' 'change.ApplyAt.After(now)' 'TestMemoryProviderAppliesScheduled'
run_mutant group_precedence evaluate.go 'for _, groupKey := range definition.Groups {' 'for _, groupKey := range []string{} {' 'TestGroupStrategiesTakePrecedenceOverFeatureStrategies'
run_mutant strategy_precedence evaluate.go 'for _, strategy := range definition.Strategies {' 'for index := len(definition.Strategies) - 1; index >= 0; index-- { strategy := definition.Strategies[index]' 'TestFeatureStrategiesUseFirstMatchPrecedence'
run_mutant default_reason evaluate.go 'return defaultResult(definition, ReasonDefault, nil)' 'return defaultResult(definition, ReasonRollout, nil)' 'TestEvaluationCoversStrategyErrorsDefaultsAndTypeFailures'
run_mutant fleet_stale_refresh fleet.go 'fleet.activate(candidate, false)' 'fleet.activate(candidate, true)' 'TestFleetRefreshRejectsStaleReplacementAndKeepsLastKnownGood'
run_mutant fleet_provider_load_limit fleet.go 'calls.Add(1) > uint64(fleet.config.MaxProviderLoads)' 'calls.Add(1) >= uint64(fleet.config.MaxProviderLoads)' 'TestFleetExecutorCannotExceedProviderLoadBudget'
run_mutant fleet_waiter_limit fleet.go 'fleet.waiters >= fleet.config.MaxWaiters' 'fleet.waiters > fleet.config.MaxWaiters' 'TestFleetRefreshCoalescesProviderLoadAndBoundsWaiters'
run_mutant fleet_refresh_boundary fleet.go 'boundedAge(now, fleet.lastRefreshAt) < fleet.config.MinRefreshInterval' 'boundedAge(now, fleet.lastRefreshAt) <= fleet.config.MinRefreshInterval' 'TestFleetFreshnessAndRefreshBoundsAreInclusiveAtTheirLimits'
run_mutant fleet_freshness_boundary fleet.go '(!allowStale && age > fleet.config.FreshFor)' '(!allowStale && age >= fleet.config.FreshFor)' 'TestFleetFreshnessAndRefreshBoundsAreInclusiveAtTheirLimits'
run_mutant fleet_staleness_boundary fleet.go 'age > fleet.config.MaxStaleness' 'age >= fleet.config.MaxStaleness' 'TestFleetFreshnessAndRefreshBoundsAreInclusiveAtTheirLimits'
run_mutant fleet_lkg_boundary fleet.go 'cmp.Compare(active.Age(fleet.now()), policy.MaxStaleness) == 1' 'cmp.Compare(active.Age(fleet.now()), policy.MaxStaleness) != 1' 'TestFleetDegradedPoliciesPreserveSecurityAndStalenessBoundaries'
run_mutant fleet_security_policy fleet.go 'policy.Mode == DegradedFailOpen || policy.Mode == DegradedDefault' 'policy.Mode == DegradedFailOpen && policy.Mode == DegradedDefault' 'TestFleetRejectsUnsafeSecurityPolicy'
run_mutant fleet_jitter_bound fleet.go '% (uint64(maximum) + 1)' '% uint64(maximum)' 'TestFleetAdaptersAndSystemSchedulingValidateBoundaryFailures'
run_mutant fleet_scheduler_failure fleet.go 'if ctx.Err() == nil {
				fleet.recordRefreshFailure(FleetFailureScheduler)' 'if ctx.Err() != nil {
				fleet.recordRefreshFailure(FleetFailureScheduler)' 'TestFleetSleeperFailureStopsBackgroundRefresherObservably'
run_mutant fleet_duplicate_invalidation fleet.go 'case 0:
		result.Disposition = InvalidationDuplicate' 'case 1:
		result.Disposition = InvalidationDuplicate' 'TestFleetInvalidationClassifiesGapsDuplicatesReorderingAndRevisions'
run_mutant fleet_gap_invalidation fleet.go 'cmp.Compare(event.Sequence, stream.last+1) == 1' 'cmp.Compare(event.Sequence, stream.last+1) != 1' 'TestFleetInvalidationClassifiesGapsDuplicatesReorderingAndRevisions'
run_mutant fleet_source_order fleet.go 'candidate.SourceTime.Before(fleet.active.SourceTime)' 'candidate.SourceTime.After(fleet.active.SourceTime)' 'TestFleetRefreshFrequencyWaiterCancellationAndSourceOrdering'
run_mutant fleet_convergence_revision fleet.go 'fleet.active.Revision != fleet.convergenceRevision' 'fleet.active.Revision == fleet.convergenceRevision' 'TestFleetKubernetesProviderOutagePreservesSecurityPolicyAndReportsBreach'
run_mutant fleet_fail_open_value fleet.go 'return evaluationResult{value: BooleanValue(true), reason: ReasonDegradedFailOpen}' 'return evaluationResult{value: BooleanValue(false), reason: ReasonDegradedFailOpen}' 'TestFleetAppliesTypedExplicitDefaultsWithoutSnapshot'
run_mutant fleet_validation_activation fleet.go 'validated, err := fleet.validateCandidate(candidate, now, allowStale)' 'validated, err := candidate.Snapshot, error(nil)' 'TestFleetRefreshNeverPartiallyActivatesMalformedReplacement'
run_mutant fleet_cache_failure_status fleet.go 'fleet.setCacheFailure(FleetFailureCacheStore)' 'fleet.setCacheFailure(FleetFailureNone)' 'TestFleetCacheStoreFailureDoesNotUndoValidatedActivation'
run_mutant fleet_current_revision fleet.go 'event.Revision == fleet.active.Revision' 'event.Revision != fleet.active.Revision' 'TestFleetInvalidationClassifiesGapsDuplicatesReorderingAndRevisions'
run_mutant fleet_convergence_receipt_time fleet.go 'now.Add(fleet.config.ConvergenceWindow)' 'event.ObservedAt.Add(fleet.config.ConvergenceWindow)' 'TestFleetConvergenceDeadlineUsesBoundedLocalReceiptTime'
run_mutant fleet_external_deadline fleet.go 'context.WithTimeout(parent, fleet.config.LoadTimeout)' 'context.WithCancel(parent)' 'TestFleetBoundsCacheOperationsWithDerivedDeadlines'
run_mutant fleet_lifecycle_cancellation fleet.go 'bounded, cancel := context.WithTimeout(parent, fleet.config.LoadTimeout)
	stopLifecycle := context.AfterFunc(fleet.lifecycleCtx, cancel)' 'bounded, cancel := context.WithTimeout(parent, fleet.config.LoadTimeout)
	stopLifecycle := func() bool { return true }' 'TestFleetShutdownCancelsAndJoinsRefreshCacheWork'
run_mutant fleet_physical_concurrency fleet.go 'make(chan struct{}, config.MaxConcurrentProviderLoads)' 'make(chan struct{}, config.MaxConcurrentProviderLoads + 1)' 'TestFleetBoundsConcurrentPhysicalLoadsInsideExecutor'
run_mutant fleet_nil_operation_context fleet.go 'operationCtx == nil' 'false && operationCtx == nil' 'TestFleetExecutorCannotBypassOperationContext/nil'
run_mutant fleet_queued_operation_cancellation fleet.go 'if err := operationCtx.Err(); err != nil {
			return SnapshotCandidate{}, err
		}
		incrementSaturating(&fleet.providerLoads)' 'if err := operationCtx.Err(); false && err != nil {
			return SnapshotCandidate{}, err
		}
		incrementSaturating(&fleet.providerLoads)' 'TestFleetExecutorCannotBypassOperationContext/cancelled|TestFleetQueuedPhysicalLoadHonorsAttemptCancellation'
run_mutant fleet_future_source_time fleet.go 'candidate.SourceTime.After(now.Add(fleet.config.MaxFutureSkew))' 'candidate.SourceTime.Before(now.Add(fleet.config.MaxFutureSkew))' 'TestFleetRejectsSourceTimeBeyondExplicitFutureSkew'
run_mutant fleet_future_skew_config fleet.go 'cmp.Compare(config.MaxFutureSkew, time.Duration(0)) == -1' 'cmp.Compare(config.MaxFutureSkew, time.Duration(0)) != -1' 'TestFleetRejectsSourceTimeBeyondExplicitFutureSkew'
run_mutant fleet_clock_rollback fleet.go 'observed.Before(fleet.lastObservedAt)' 'observed.After(fleet.lastObservedAt)' 'TestFleetDegradedPoliciesPreserveSecurityAndStalenessBoundaries'
run_mutant fleet_clock_high_watermark fleet.go 'fleet.lastObservedAt = observed' 'fleet.lastObservedAt = time.Time{}' 'TestFleetDegradedPoliciesPreserveSecurityAndStalenessBoundaries'
run_mutant fleet_failure_classifier_boundary fleet.go 'builtIn != FleetFailureProvider || fleet.config.FailureClassifier == nil' 'builtIn != FleetFailureProvider && fleet.config.FailureClassifier == nil' 'TestFleetClassifiesCallerOwnedResilienceRejectionsWithoutRetainingErrors'
run_mutant fleet_failure_classifier_unknown fleet.go 'case FleetFailureProvider, FleetFailureRetryExhausted, FleetFailureCircuitOpen,
		FleetFailureBulkhead, FleetFailureThrottled, FleetFailureConcurrency,
		FleetFailureBudgetExhausted:
		return classified
	default:
		return FleetFailureProvider
	}' 'case FleetFailureProvider, FleetFailureRetryExhausted, FleetFailureCircuitOpen,
		FleetFailureBulkhead, FleetFailureThrottled, FleetFailureConcurrency,
		FleetFailureBudgetExhausted:
		return classified
	default:
		return classified
	}' 'TestFleetClassifiesCallerOwnedResilienceRejectionsWithoutRetainingErrors'
run_mutant fleet_provider_counter_saturation fleet.go 'incrementSaturating(&fleet.providerLoads)' 'fleet.providerLoads.Add(1)' 'TestFleetOperationalCountersSaturateInsteadOfWrapping'
run_mutant fleet_gap_counter_saturation fleet.go 'fleet.invalidationGaps < math.MaxUint64' 'fleet.invalidationGaps <= math.MaxUint64' 'TestFleetOperationalCountersSaturateInsteadOfWrapping'
run_mutant fleet_waiter_maximum fleet.go 'maxFleetWaiters                 = 65_536' 'maxFleetWaiters                 = 65_537' 'TestFleetConfigurationRejectsEveryUnboundedOrUnsafeShape/excess_waiters'
run_mutant fleet_provider_attempt_maximum fleet.go 'maxFleetProviderLoads           = 1_024' 'maxFleetProviderLoads           = 1_025' 'TestFleetConfigurationRejectsEveryUnboundedOrUnsafeShape/excess_provider_loads'
run_mutant fleet_provider_concurrency_maximum fleet.go 'maxFleetConcurrentProviderLoads = 1_024' 'maxFleetConcurrentProviderLoads = 1_025' 'TestFleetConfigurationRejectsEveryUnboundedOrUnsafeShape/excess_provider_concurrency'
run_mutant fleet_invalidation_stream_maximum fleet.go 'maxFleetInvalidationStreams     = 10_000' 'maxFleetInvalidationStreams     = 10_001' 'TestFleetConfigurationRejectsEveryUnboundedOrUnsafeShape/excess_invalidation_streams'
run_mutant fleet_policy_maximum fleet.go 'cmp.Compare(config.MaxPolicies, DefaultLimits().MaxFeatures) == 1' 'cmp.Compare(config.MaxPolicies, DefaultLimits().MaxFeatures + 1) == 1' 'TestFleetConfigurationRejectsEveryUnboundedOrUnsafeShape/excess_policy_bound'
run_mutant fleet_load_duration_overflow fleet.go 'if cmp.Compare(config.LoadTimeout, time.Duration(math.MaxInt64)-refreshBound) == 1 {' 'if false && cmp.Compare(config.LoadTimeout, time.Duration(math.MaxInt64)-refreshBound) == 1 {' 'TestFleetConfigurationRejectsEveryUnboundedOrUnsafeShape/overflow_load_bound'

echo 'mutation score: 49/49 killed (100.0%)'
