package featureflags

import "testing"

func TestExactEmptyAttributeRequiresPresence(t *testing.T) {
	strategy := ExactTargetStrategy{Name: "empty-region", Variant: "selected", Attributes: map[string]string{"region": ""}}
	snapshot, err := NewSnapshot([]Definition{{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
		Variants: map[string]Value{"selected": BooleanValue(true)}, Strategies: []Strategy{strategy},
	}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name       string
		attributes map[string]string
		want       bool
	}{
		{"nil attributes", nil, false},
		{"empty attributes", map[string]string{}, false},
		{"different empty attribute", map[string]string{"other": ""}, false},
		{"explicit empty", map[string]string{"region": ""}, true},
		{"different value", map[string]string{"region": "west"}, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			input := Context{Attributes: scenario.attributes}
			result, err := strategy.EvaluateStrategy(StrategyInput{FeatureKey: "flag", Context: input})
			if err != nil || result.Match != scenario.want {
				t.Errorf("exact attribute match = %t, %v; want %t", result.Match, err, scenario.want)
			}
			detail, err := snapshot.Boolean("flag", input)
			if err != nil || detail.Value != scenario.want {
				t.Errorf("snapshot selected empty target = %t, %v; want %t", detail.Value, err, scenario.want)
			}
		})
	}
	if result, err := (ExactTargetStrategy{}).EvaluateStrategy(StrategyInput{}); err != nil || !result.Match {
		t.Errorf("unrestricted attributes = %t, %v; want match", result.Match, err)
	}
}
