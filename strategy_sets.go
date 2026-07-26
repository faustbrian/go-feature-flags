package featureflags

// SetStrategy combines tenant and subject allow/deny sets. Deny sets always
// take precedence over allow sets.
type SetStrategy struct {
	Name          string
	Variant       string
	AllowTenants  []string
	DenyTenants   []string
	AllowSubjects []string
	DenySubjects  []string
}

func (s SetStrategy) StrategyName() string { return s.Name }

func (s SetStrategy) TargetVariant() string { return s.Variant }

func (s SetStrategy) ValidateStrategy(limits Limits) error {
	return validateTargetValues(
		limits,
		s.AllowTenants,
		s.DenyTenants,
		s.AllowSubjects,
		s.DenySubjects,
	)
}

func (s SetStrategy) EvaluateStrategy(input StrategyInput) (StrategyResult, error) {
	denied := listed(s.DenyTenants, input.Context.Tenant) || listed(s.DenySubjects, input.Context.Subject)
	allowed := listedOrUnrestricted(s.AllowTenants, input.Context.Tenant) &&
		listedOrUnrestricted(s.AllowSubjects, input.Context.Subject)

	return StrategyResult{Match: allowed && !denied, Reason: ReasonTargetingMatch}, nil
}

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
