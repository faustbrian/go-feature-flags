package featureflags

import "fmt"

// StrategyInput is the bounded, explicit input to deterministic strategies.
type StrategyInput struct {
	FeatureKey string
	Context    Context
}

// StrategyResult describes whether a strategy selected its target variant.
type StrategyResult struct {
	Match       bool
	Reason      Reason
	Diagnostics []Diagnostic
}

// Strategy is a deterministic evaluation policy. SnapshotStrategy must return
// a value whose later behavior cannot be changed through the original value.
type Strategy interface {
	StrategyName() string
	TargetVariant() string
	ValidateStrategy(Limits) error
	EvaluateStrategy(StrategyInput) (StrategyResult, error)
	SnapshotStrategy() Strategy
}

// ExactTargetStrategy matches explicitly listed tenants and subjects. When a
// list is non-empty it must contain the corresponding context value.
type ExactTargetStrategy struct {
	Name         string
	Variant      string
	Tenants      []string
	Subjects     []string
	Environments []string
	Attributes   map[string]string
}

func (s ExactTargetStrategy) StrategyName() string { return s.Name }

func (s ExactTargetStrategy) TargetVariant() string { return s.Variant }

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
	Name      string
	Variant   string
	Seed      string
	Threshold uint32
}

func (s PercentageStrategy) StrategyName() string { return s.Name }

func (s PercentageStrategy) TargetVariant() string { return s.Variant }

func (s PercentageStrategy) ValidateStrategy(Limits) error {
	if s.Threshold > uint32(bucketPrecision) {
		return fmt.Errorf("threshold %d exceeds precision %d", s.Threshold, bucketPrecision)
	}

	return nil
}

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

func (s PercentageStrategy) SnapshotStrategy() Strategy { return s }

func (s ExactTargetStrategy) SnapshotStrategy() Strategy {
	s.Tenants = append([]string(nil), s.Tenants...)
	s.Subjects = append([]string(nil), s.Subjects...)
	s.Environments = append([]string(nil), s.Environments...)
	s.Attributes = cloneStringMap(s.Attributes)

	return s
}

func matchesAttributes(required, actual map[string]string) bool {
	for key, value := range required {
		if actual[key] != value {
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
