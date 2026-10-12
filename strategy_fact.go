package featureflags

import "fmt"

// FactStrategy matches one caller-defined typed fact using strict equality.
// It never coerces between native value types.
type FactStrategy struct {
	// Name is the caller-visible match identity.
	Name string
	// Variant names the feature variant selected on a match.
	Variant string
	// Fact names a required typed context fact.
	Fact string
	// Equals uses strict typed equality; structured JSON compares raw bytes.
	Equals Value
}

// StrategyName returns the caller-visible policy name.
func (s FactStrategy) StrategyName() string { return s.Name }

// TargetVariant returns the variant selected on a match.
func (s FactStrategy) TargetVariant() string { return s.Variant }

// ValidateStrategy checks policy bounds; feature validation checks target existence.
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

// EvaluateStrategy evaluates explicit borrowed context without ambient clock or request
// state.
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

// SnapshotStrategy isolates mutable policy state for concurrent snapshot evaluation.
func (s FactStrategy) SnapshotStrategy() Strategy {
	s.Equals = s.Equals.clone()

	return s
}
