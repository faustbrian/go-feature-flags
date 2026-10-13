package featureflags

import "fmt"

// StrategyInput is explicit borrowed input to deterministic strategies. Engine
// evaluation validates Context first; direct strategy calls do not do so.
type StrategyInput struct {
	// FeatureKey is the current feature used for deterministic bucketing.
	FeatureKey string
	// Context borrows caller maps; callbacks must not mutate or retain unsafe aliases.
	Context Context
}

// StrategyResult describes whether a strategy selected its target variant.
type StrategyResult struct {
	// Match selects TargetVariant when true; false diagnostics are discarded.
	Match bool
	// Reason labels a match; empty selects targeting-match and groups override it.
	Reason Reason
	// Diagnostics must exclude sensitive data before output truncation.
	Diagnostics []Diagnostic
}

// Strategy is a deterministic evaluation policy. SnapshotStrategy must return
// a value whose later behavior cannot be changed through the original value.
type Strategy interface {
	// StrategyName returns the caller-visible policy name.
	StrategyName() string
	// TargetVariant returns the variant selected on a match.
	TargetVariant() string
	// ValidateStrategy checks policy bounds; feature validation checks target existence.
	ValidateStrategy(Limits) error
	// EvaluateStrategy must be deterministic, concurrent-safe, bounded and panic-free; inputs
	// are borrowed.
	EvaluateStrategy(StrategyInput) (StrategyResult, error)
	// SnapshotStrategy must isolate all mutable state; memory invokes it under provider
	// locks.
	SnapshotStrategy() Strategy
}

// ExactTargetStrategy matches explicitly listed tenants and subjects. When a
// list is non-empty it must contain the corresponding context value.
type ExactTargetStrategy struct {
	// Name is the caller-visible match identity.
	Name string
	// Variant names the feature variant selected on a match.
	Variant string
	// Tenants restricts tenants when nonempty; empty is unrestricted.
	Tenants []string
	// Subjects restricts subjects when nonempty; empty is unrestricted.
	Subjects []string
	// Environments restricts environments when nonempty; empty is unrestricted.
	Environments []string
	// Attributes requires each key to exist and its string value to match exactly.
	Attributes map[string]string
}

// StrategyName returns the caller-visible policy name.
func (s ExactTargetStrategy) StrategyName() string { return s.Name }

// TargetVariant returns the variant selected on a match.
func (s ExactTargetStrategy) TargetVariant() string { return s.Variant }

// ValidateStrategy checks policy bounds; feature validation checks target existence.
func (s ExactTargetStrategy) ValidateStrategy(limits Limits) error {
	if err := validateTargetValues(limits, s.Tenants, s.Subjects, s.Environments); err != nil {
		return err
	}
	if len(s.Attributes) > limits.MaxAttributes {
		return fmt.Errorf("attributes exceed limit %d", limits.MaxAttributes)
	}
	for key, value := range s.Attributes {
		if key == "" || len(key) > limits.MaxContextKeyBytes || len(value) > limits.MaxContextValueBytes {
			return fmt.Errorf("attribute target exceeds configured bounds")
		}
	}

	return nil
}

func validateTargetValues(limits Limits, sets ...[]string) error {
	total := 0
	for _, values := range sets {
		total += len(values)
		for _, value := range values {
			if value == "" || len(value) > limits.MaxContextValueBytes {
				return fmt.Errorf("target value exceeds configured bounds")
			}
		}
	}
	if total > limits.MaxTargetValues {
		return fmt.Errorf("target values exceed limit %d", limits.MaxTargetValues)
	}

	return nil
}

// EvaluateStrategy evaluates explicit borrowed context without ambient clock or request
// state.
func (s ExactTargetStrategy) EvaluateStrategy(input StrategyInput) (StrategyResult, error) {
	return StrategyResult{
		Match: listedOrUnrestricted(s.Tenants, input.Context.Tenant) &&
			listedOrUnrestricted(s.Subjects, input.Context.Subject) &&
			listedOrUnrestricted(s.Environments, input.Context.Environment) &&
			matchesAttributes(s.Attributes, input.Context.Attributes),
		Reason: ReasonTargetingMatch,
	}, nil
}

// PercentageStrategy selects a stable fraction of tenant-scoped subjects.
// Threshold uses five decimal digits of precision: 100000 means 100%.
type PercentageStrategy struct {
	// Name is the caller-visible match identity.
	Name string
	// Variant names the feature variant selected on a match.
	Variant string
	// Seed is versioned hash input; changing it reshuffles assignments.
	Seed string
	// Threshold is the exclusive bucket ceiling in [0,100000]; tenant and subject are
	// required.
	Threshold uint32
}

// StrategyName returns the caller-visible policy name.
func (s PercentageStrategy) StrategyName() string { return s.Name }

// TargetVariant returns the variant selected on a match.
func (s PercentageStrategy) TargetVariant() string { return s.Variant }

// ValidateStrategy checks policy bounds; feature validation checks target existence.
func (s PercentageStrategy) ValidateStrategy(Limits) error {
	if s.Threshold > uint32(bucketPrecision) {
		return fmt.Errorf("threshold %d exceeds precision %d", s.Threshold, bucketPrecision)
	}

	return nil
}

// EvaluateStrategy evaluates explicit borrowed context without ambient clock or request
// state.
func (s PercentageStrategy) EvaluateStrategy(input StrategyInput) (StrategyResult, error) {
	if input.Context.Tenant == "" || input.Context.Subject == "" {
		return StrategyResult{
			Reason: ReasonRollout,
			Diagnostics: []Diagnostic{{
				Code:    "missing_bucketing_identity",
				Message: "percentage rollout requires tenant and subject",
			}},
		}, nil
	}

	return StrategyResult{
		Match:  Bucket(s.Seed, input.FeatureKey, input.Context.Tenant, input.Context.Subject) < s.Threshold,
		Reason: ReasonRollout,
	}, nil
}

// SnapshotStrategy isolates mutable policy state for concurrent snapshot evaluation.
func (s PercentageStrategy) SnapshotStrategy() Strategy { return s }

// SnapshotStrategy isolates mutable policy state for concurrent snapshot evaluation.
func (s ExactTargetStrategy) SnapshotStrategy() Strategy {
	s.Tenants = append([]string(nil), s.Tenants...)
	s.Subjects = append([]string(nil), s.Subjects...)
	s.Environments = append([]string(nil), s.Environments...)
	s.Attributes = cloneStringMap(s.Attributes)

	return s
}

func matchesAttributes(required, actual map[string]string) bool {
	for key, value := range required {
		actualValue, exists := actual[key]
		if !exists || actualValue != value {
			return false
		}
	}

	return true
}

func listedOrUnrestricted(values []string, candidate string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if value == candidate {
			return true
		}
	}

	return false
}
