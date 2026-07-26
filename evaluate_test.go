package featureflags

import "testing"

func TestSnapshotBooleanMatchesTenantScopedExactTarget(t *testing.T) {
	t.Parallel()

	snapshot, err := NewSnapshot([]Definition{{
		Key:       "checkout.redesign",
		Type:      TypeBoolean,
		Default:   BooleanValue(false),
		Lifecycle: LifecycleActive,
		Version:   7,
		Variants: map[string]Value{
			"enabled": BooleanValue(true),
		},
		Strategies: []Strategy{
			ExactTargetStrategy{
				Name:     "beta-users",
				Variant:  "enabled",
				Tenants:  []string{"tenant-a"},
				Subjects: []string{"user-123"},
			},
		},
	}}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}

	detail, err := snapshot.Boolean("checkout.redesign", Context{
		Tenant:  "tenant-a",
		Subject: "user-123",
	})
	if err != nil {
		t.Fatalf("Boolean() error = %v", err)
	}
	if !detail.Value {
		t.Fatal("Boolean() value = false, want true")
	}
	if got, want := detail.Variant, "enabled"; got != want {
		t.Fatalf("Boolean() variant = %q, want %q", got, want)
	}
	if got, want := detail.Reason, ReasonTargetingMatch; got != want {
		t.Fatalf("Boolean() reason = %q, want %q", got, want)
	}
	if got, want := detail.MatchedStrategy, "beta-users"; got != want {
		t.Fatalf("Boolean() matched strategy = %q, want %q", got, want)
	}
	if got, want := detail.Version, uint64(7); got != want {
		t.Fatalf("Boolean() version = %d, want %d", got, want)
	}
}

func TestPercentageStrategyKeepsTenantAssignmentsIndependent(t *testing.T) {
	t.Parallel()

	snapshot, err := NewSnapshot([]Definition{{
		Key:       "search.rank",
		Type:      TypeBoolean,
		Default:   BooleanValue(false),
		Lifecycle: LifecycleActive,
		Variants:  map[string]Value{"enabled": BooleanValue(true)},
		Strategies: []Strategy{PercentageStrategy{
			Name:      "twenty-percent",
			Variant:   "enabled",
			Seed:      "experiment-7",
			Threshold: 20_000,
		}},
	}}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}

	tenantA, err := snapshot.Boolean("search.rank", Context{Tenant: "tenant-a", Subject: "user-123"})
	if err != nil {
		t.Fatalf("Boolean(tenant-a) error = %v", err)
	}
	tenantB, err := snapshot.Boolean("search.rank", Context{Tenant: "tenant-b", Subject: "user-123"})
	if err != nil {
		t.Fatalf("Boolean(tenant-b) error = %v", err)
	}
	if tenantA.Value {
		t.Fatal("Boolean(tenant-a) = true, want false for bucket 26947")
	}
	if !tenantB.Value {
		t.Fatal("Boolean(tenant-b) = false, want true for bucket 17802")
	}
}

func TestPercentageStrategyUsesExclusiveThreshold(t *testing.T) {
	t.Parallel()

	input := StrategyInput{
		FeatureKey: "checkout",
		Context:    Context{Tenant: "tenant-a", Subject: "subject-1"},
	}
	threshold := Bucket("v1", input.FeatureKey, input.Context.Tenant, input.Context.Subject)
	atBoundary := PercentageStrategy{
		Name: "boundary", Variant: "enabled", Seed: "v1", Threshold: threshold,
	}
	result, err := atBoundary.EvaluateStrategy(input)
	if err != nil {
		t.Fatalf("EvaluateStrategy() error = %v", err)
	}
	if result.Match {
		t.Fatal("EvaluateStrategy() matched a bucket equal to its threshold")
	}
	if threshold < uint32(bucketPrecision) {
		atBoundary.Threshold++
		result, err = atBoundary.EvaluateStrategy(input)
		if err != nil || !result.Match {
			t.Fatalf("EvaluateStrategy() above boundary = (%#v, %v)", result, err)
		}
	}
}

func TestFeatureStrategiesUseFirstMatchPrecedence(t *testing.T) {
	t.Parallel()

	snapshot, err := NewSnapshot([]Definition{{
		Key: "flag", Type: TypeString, Default: StringValue("control"),
		Lifecycle: LifecycleActive,
		Variants: map[string]Value{
			"first":  StringValue("first"),
			"second": StringValue("second"),
		},
		Strategies: []Strategy{
			ExactTargetStrategy{Name: "first", Variant: "first", Subjects: []string{"alice"}},
			ExactTargetStrategy{Name: "second", Variant: "second", Subjects: []string{"alice"}},
		},
	}}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}
	detail, err := snapshot.String("flag", Context{Subject: "alice"})
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if detail.Value != "first" || detail.MatchedStrategy != "first" {
		t.Fatalf("String() = (%q, %q), want first matching strategy", detail.Value, detail.MatchedStrategy)
	}
}
