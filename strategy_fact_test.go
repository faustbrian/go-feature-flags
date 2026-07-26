package featureflags

import "testing"

func TestFactStrategyUsesStrictTypedEquality(t *testing.T) {
	t.Parallel()

	strategy := FactStrategy{
		Name:    "mature-account",
		Variant: "enabled",
		Fact:    "account.age",
		Equals:  IntegerValue(10),
	}

	for _, test := range []struct {
		name string
		fact Value
		want bool
	}{
		{name: "same integer", fact: IntegerValue(10), want: true},
		{name: "different integer", fact: IntegerValue(9), want: false},
		{name: "string is not coerced", fact: StringValue("10"), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := strategy.EvaluateStrategy(StrategyInput{Context: Context{
				Facts: map[string]Value{"account.age": test.fact},
			}})
			if err != nil {
				t.Fatalf("EvaluateStrategy() error = %v", err)
			}
			if result.Match != test.want {
				t.Fatalf("EvaluateStrategy() match = %t, want %t", result.Match, test.want)
			}
		})
	}
}

func TestExactTargetStrategyMatchesEnvironmentAndAttributes(t *testing.T) {
	t.Parallel()

	strategy := ExactTargetStrategy{
		Name:         "beta-eu",
		Variant:      "enabled",
		Environments: []string{"production"},
		Attributes:   map[string]string{"region": "eu", "plan": "pro"},
	}
	matched, err := strategy.EvaluateStrategy(StrategyInput{Context: Context{
		Environment: "production",
		Attributes:  map[string]string{"region": "eu", "plan": "pro", "locale": "fi"},
	}})
	if err != nil {
		t.Fatalf("EvaluateStrategy() error = %v", err)
	}
	if !matched.Match {
		t.Fatal("EvaluateStrategy() did not match the exact context")
	}
	unmatched, err := strategy.EvaluateStrategy(StrategyInput{Context: Context{
		Environment: "production",
		Attributes:  map[string]string{"region": "us", "plan": "pro"},
	}})
	if err != nil {
		t.Fatalf("EvaluateStrategy() mismatch error = %v", err)
	}
	if unmatched.Match {
		t.Fatal("EvaluateStrategy() matched the wrong environment")
	}

	snapshot := strategy.SnapshotStrategy().(ExactTargetStrategy)
	strategy.Attributes["region"] = "us"
	if snapshot.Attributes["region"] != "eu" {
		t.Fatal("SnapshotStrategy() retained a mutable attributes map")
	}

	data, err := Export([]Definition{{
		Key: "checkout", Type: TypeBoolean, Default: BooleanValue(false),
		Variants:   map[string]Value{"enabled": BooleanValue(true)},
		Strategies: []Strategy{snapshot},
	}}, nil, DefaultLimits())
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	definitions, _, err := Import(data, DefaultLimits())
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	roundTripped := definitions[0].Strategies[0].(ExactTargetStrategy)
	if len(roundTripped.Environments) != 1 || roundTripped.Attributes["plan"] != "pro" {
		t.Fatalf("round-tripped exact strategy = %#v", roundTripped)
	}
}
