#!/usr/bin/env bash
set -euo pipefail

workspace=$(mktemp -d)
trap 'rm -rf "$workspace"' EXIT
baseline="$workspace/baseline"
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
	if (cd "$mutant" && go test . -run "$pattern" -count=1 >mutation.log 2>&1); then
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

echo 'mutation score: 11/11 killed (100.0%)'
