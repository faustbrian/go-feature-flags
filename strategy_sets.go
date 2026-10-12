package featureflags

// SetStrategy combines tenant and subject allow/deny sets. Deny sets always
// take precedence over allow sets.
type SetStrategy struct {
	// Name is the caller-visible match identity.
	Name string
	// Variant names the feature variant selected on a match.
	Variant string
	// AllowTenants restricts tenants when nonempty.
	AllowTenants []string
	// DenyTenants rejects listed tenants regardless of allow sets.
	DenyTenants []string
	// AllowSubjects restricts subjects when nonempty.
	AllowSubjects []string
	// DenySubjects rejects listed subjects regardless of allow sets.
	DenySubjects []string
}

// StrategyName returns the caller-visible policy name.
func (s SetStrategy) StrategyName() string { return s.Name }

// TargetVariant returns the variant selected on a match.
func (s SetStrategy) TargetVariant() string { return s.Variant }

// ValidateStrategy checks policy bounds; feature validation checks target existence.
func (s SetStrategy) ValidateStrategy(limits Limits) error {
	return validateTargetValues(
		limits,
		s.AllowTenants,
		s.DenyTenants,
		s.AllowSubjects,
		s.DenySubjects,
	)
}

// EvaluateStrategy evaluates explicit borrowed context without ambient clock or request
// state.
func (s SetStrategy) EvaluateStrategy(input StrategyInput) (StrategyResult, error) {
	denied := listed(s.DenyTenants, input.Context.Tenant) || listed(s.DenySubjects, input.Context.Subject)
	allowed := listedOrUnrestricted(s.AllowTenants, input.Context.Tenant) &&
		listedOrUnrestricted(s.AllowSubjects, input.Context.Subject)

	return StrategyResult{Match: allowed && !denied, Reason: ReasonTargetingMatch}, nil
}

// SnapshotStrategy isolates mutable policy state for concurrent snapshot evaluation.
func (s SetStrategy) SnapshotStrategy() Strategy {
	s.AllowTenants = append([]string(nil), s.AllowTenants...)
	s.DenyTenants = append([]string(nil), s.DenyTenants...)
	s.AllowSubjects = append([]string(nil), s.AllowSubjects...)
	s.DenySubjects = append([]string(nil), s.DenySubjects...)

	return s
}

func listed(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}

	return false
}
