package featureflags

import "fmt"

// FactStrategy matches one caller-defined typed fact using strict equality.
// It never coerces between native value types.
type FactStrategy struct {
	Name    string
	Variant string
	Fact    string
	Equals  Value
}

func (s FactStrategy) StrategyName() string { return s.Name }

func (s FactStrategy) TargetVariant() string { return s.Variant }

func (s FactStrategy) ValidateStrategy(limits Limits) error {
	if s.Fact == "" {
		return fmt.Errorf("fact name is required")
	}
	if s.Equals.Type() == "" {
		return fmt.Errorf("fact comparison value is required")
	}
	if err := s.Equals.validate(limits); err != nil {
		return fmt.Errorf("comparison value: %w", err)
	}

	return nil
}

func (s FactStrategy) EvaluateStrategy(input StrategyInput) (StrategyResult, error) {
	value, exists := input.Context.Facts[s.Fact]
	if !exists {
		return StrategyResult{
			Reason: ReasonTargetingMatch,
			Diagnostics: []Diagnostic{{
				Code:    "missing_fact",
				Message: "required typed fact is absent",
			}},
		}, nil
	}

	return StrategyResult{Match: value.equal(s.Equals), Reason: ReasonTargetingMatch}, nil
}

func (s FactStrategy) SnapshotStrategy() Strategy {
	s.Equals = s.Equals.clone()

	return s
}
