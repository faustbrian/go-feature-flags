package featureflags

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// Reason is a stable, low-cardinality explanation of an evaluation decision.
type Reason string

const (
	ReasonTargetingMatch   Reason = "targeting_match"
	ReasonRollout          Reason = "rollout"
	ReasonSchedule         Reason = "schedule"
	ReasonDependencyFailed Reason = "dependency_failed"
	ReasonGroupMatch       Reason = "group_match"
	ReasonDefault          Reason = "default"
	ReasonInactive         Reason = "inactive"
)

// Diagnostic contains safe, bounded evaluation information and never embeds
// context values.
type Diagnostic struct {
	Code    string
	Message string
}

// Detail is the typed result of a native evaluation.
type Detail[T any] struct {
	Value           T
	Variant         string
	Reason          Reason
	MatchedStrategy string
	Version         uint64
	Diagnostics     []Diagnostic
}

// EvaluationRequest identifies one strictly typed batch operation.
type EvaluationRequest struct {
	Key  string
	Type Type
}

// EvaluationDetail is the mixed-type equivalent of Detail for batch calls.
type EvaluationDetail struct {
	Key             string
	Value           Value
	Variant         string
	Reason          Reason
	MatchedStrategy string
	Version         uint64
	Diagnostics     []Diagnostic
}

// Snapshot is an immutable collection used for consistent request-scoped
// evaluations.
type Snapshot struct {
	definitions map[string]Definition
	groups      map[string]GroupDefinition
	tenant      string
	limits      Limits
}

func NewSnapshot(definitions []Definition, limits Limits) (Snapshot, error) {
	return NewSnapshotWithGroups(definitions, nil, limits)
}

// NewSnapshotWithGroups creates an immutable snapshot with inherited group
// strategies and validates the complete dependency and group graphs.
func NewSnapshotWithGroups(
	definitions []Definition,
	groups []GroupDefinition,
	limits Limits,
) (Snapshot, error) {
	if len(definitions) > limits.MaxFeatures {
		return Snapshot{}, fmt.Errorf("features exceed limit %d", limits.MaxFeatures)
	}

	cloned := make(map[string]Definition, len(definitions))
	for _, definition := range definitions {
		if err := definition.Validate(limits); err != nil {
			return Snapshot{}, err
		}
		if _, exists := cloned[definition.Key]; exists {
			return Snapshot{}, fmt.Errorf("duplicate feature %q", definition.Key)
		}
		cloned[definition.Key] = cloneDefinition(definition)
	}
	if err := validateDependencyGraph(cloned, limits); err != nil {
		return Snapshot{}, err
	}
	clonedGroups, err := cloneAndValidateGroups(cloned, groups, limits)
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{definitions: cloned, groups: clonedGroups, limits: limits}, nil
}

// Boolean evaluates a boolean feature without coercing another value type.
func (s Snapshot) Boolean(key string, context Context) (Detail[bool], error) {
	result, err := s.evaluate(key, TypeBoolean, context)
	if err != nil {
		return Detail[bool]{}, err
	}
	boolean, _ := result.value.booleanValue()

	return detailOf(boolean, result), nil
}

// String evaluates a string feature without coercion.
func (s Snapshot) String(key string, context Context) (Detail[string], error) {
	result, err := s.evaluate(key, TypeString, context)
	if err != nil {
		return Detail[string]{}, err
	}
	value, _ := result.value.stringValue(TypeString)

	return detailOf(value, result), nil
}

// Integer evaluates a signed 64-bit integer feature without coercion.
func (s Snapshot) Integer(key string, context Context) (Detail[int64], error) {
	result, err := s.evaluate(key, TypeInteger, context)
	if err != nil {
		return Detail[int64]{}, err
	}
	value, _ := result.value.integerValue()

	return detailOf(value, result), nil
}

// Float evaluates an IEEE-754 float64 feature without coercion.
func (s Snapshot) Float(key string, context Context) (Detail[float64], error) {
	result, err := s.evaluate(key, TypeFloat, context)
	if err != nil {
		return Detail[float64]{}, err
	}
	value, _ := result.value.floatValue()

	return detailOf(value, result), nil
}

// Decimal evaluates a decimal feature as its exact canonical string.
func (s Snapshot) Decimal(key string, context Context) (Detail[string], error) {
	result, err := s.evaluate(key, TypeDecimal, context)
	if err != nil {
		return Detail[string]{}, err
	}
	value, _ := result.value.stringValue(TypeDecimal)

	return detailOf(value, result), nil
}

// Structured evaluates a JSON feature and returns an owned copy of its bytes.
func (s Snapshot) Structured(key string, context Context) (Detail[json.RawMessage], error) {
	result, err := s.evaluate(key, TypeStructured, context)
	if err != nil {
		return Detail[json.RawMessage]{}, err
	}
	value, _ := result.value.structuredValue()

	return detailOf(value, result), nil
}

// Batch evaluates all requests against this immutable snapshot. It returns no
// partial results when any request is invalid.
func (s Snapshot) Batch(context Context, requests []EvaluationRequest) ([]EvaluationDetail, error) {
	if len(requests) > s.limits.MaxBatchSize {
		return nil, fmt.Errorf("batch size %d exceeds %d: %w", len(requests), s.limits.MaxBatchSize, ErrBatchLimit)
	}
	details := make([]EvaluationDetail, 0, len(requests))
	for _, request := range requests {
		result, err := s.evaluate(request.Key, request.Type, context)
		if err != nil {
			return nil, err
		}
		details = append(details, EvaluationDetail{
			Key:             request.Key,
			Value:           result.value.clone(),
			Variant:         result.variant,
			Reason:          result.reason,
			MatchedStrategy: result.matchedStrategy,
			Version:         result.version,
			Diagnostics:     append([]Diagnostic(nil), result.diagnostics...),
		})
	}

	return details, nil
}

type evaluationResult struct {
	value           Value
	variant         string
	reason          Reason
	matchedStrategy string
	version         uint64
	diagnostics     []Diagnostic
}

func (s Snapshot) evaluate(key string, expected Type, context Context) (evaluationResult, error) {
	if s.tenant != "" && context.Tenant != s.tenant {
		return evaluationResult{}, fmt.Errorf(
			"snapshot tenant %q: context tenant does not match: %w",
			s.tenant,
			ErrTenantMismatch,
		)
	}
	if err := context.validate(s.limits); err != nil {
		return evaluationResult{}, err
	}
	definition, exists := s.definitions[key]
	if !exists {
		return evaluationResult{}, fmt.Errorf("feature %q: %w", key, ErrNotFound)
	}
	if definition.Type != expected {
		return evaluationResult{}, fmt.Errorf("feature %q has type %s, want %s", key, definition.Type, expected)
	}
	result, err := s.evaluateKey(key, context, make(map[string]bool), 0)
	if err != nil {
		return evaluationResult{}, err
	}

	return result, nil
}

func (s Snapshot) bindTenant(tenant string) Snapshot {
	s.tenant = tenant

	return s
}

func detailOf[T any](value T, result evaluationResult) Detail[T] {
	return Detail[T]{
		Value:           value,
		Variant:         result.variant,
		Reason:          result.reason,
		MatchedStrategy: result.matchedStrategy,
		Version:         result.version,
		Diagnostics:     append([]Diagnostic(nil), result.diagnostics...),
	}
}

func (s Snapshot) evaluateKey(
	key string,
	context Context,
	visiting map[string]bool,
	depth int,
) (evaluationResult, error) {
	if depth > s.limits.MaxEvaluationDepth {
		return evaluationResult{}, fmt.Errorf("feature %q: evaluation depth exceeds %d", key, s.limits.MaxEvaluationDepth)
	}
	if visiting[key] {
		return evaluationResult{}, fmt.Errorf("feature %q: %w", key, ErrDependencyCycle)
	}
	definition, exists := s.definitions[key]
	if !exists {
		return evaluationResult{}, fmt.Errorf("feature %q: %w", key, ErrNotFound)
	}
	visiting[key] = true
	defer delete(visiting, key)

	if definition.Lifecycle != LifecycleActive {
		return defaultResult(definition, ReasonInactive, nil), nil
	}
	for _, dependency := range definition.Dependencies {
		result, err := s.evaluateKey(dependency.FeatureKey, context, visiting, depth+1)
		if err != nil {
			return evaluationResult{}, fmt.Errorf("feature %q dependency %q: %w", key, dependency.FeatureKey, err)
		}
		if result.variant != dependency.RequiredVariant {
			return defaultResult(definition, ReasonDependencyFailed, []Diagnostic{{
				Code:    "dependency_failed",
				Message: "required feature variant was not selected",
			}}), nil
		}
	}
	visitedGroups := make(map[string]bool, len(definition.Groups))
	for _, groupKey := range definition.Groups {
		result, matched, err := s.evaluateGroup(definition, groupKey, context, visitedGroups, 0)
		if err != nil {
			return evaluationResult{}, err
		}
		if matched {
			return result, nil
		}
	}
	for _, strategy := range definition.Strategies {
		result, matched, err := evaluateOneStrategy(definition, strategy, context, "", "", s.limits)
		if err != nil {
			return evaluationResult{}, err
		}
		if matched {
			return result, nil
		}
	}

	return defaultResult(definition, ReasonDefault, nil), nil
}

func evaluateOneStrategy(
	definition Definition,
	strategy Strategy,
	context Context,
	prefix string,
	reasonOverride Reason,
	limits Limits,
) (evaluationResult, bool, error) {
	result, err := strategy.EvaluateStrategy(StrategyInput{FeatureKey: definition.Key, Context: context})
	result.Diagnostics = boundDiagnostics(result.Diagnostics, limits)
	if err != nil {
		return evaluationResult{}, false,
			fmt.Errorf("feature %q strategy %q: %w", definition.Key, strategy.StrategyName(), err)
	}
	if !result.Match {
		return evaluationResult{}, false, nil
	}
	reason := result.Reason
	if reasonOverride != "" {
		reason = reasonOverride
	} else if reason == "" {
		reason = ReasonTargetingMatch
	}

	return evaluationResult{
		value:           definition.Variants[strategy.TargetVariant()],
		variant:         strategy.TargetVariant(),
		reason:          reason,
		matchedStrategy: prefix + strategy.StrategyName(),
		version:         definition.Version,
		diagnostics:     result.Diagnostics,
	}, true, nil
}

func (s Snapshot) evaluateGroup(
	definition Definition,
	key string,
	context Context,
	visited map[string]bool,
	depth int,
) (evaluationResult, bool, error) {
	if depth > s.limits.MaxGroupDepth {
		return evaluationResult{}, false, fmt.Errorf("group %q: inheritance depth exceeds %d", key, s.limits.MaxGroupDepth)
	}
	if visited[key] {
		return evaluationResult{}, false, nil
	}
	visited[key] = true
	group := s.groups[key]
	for _, strategy := range group.Strategies {
		result, matched, err := evaluateOneStrategy(
			definition,
			strategy,
			context,
			"group:"+key+"/",
			ReasonGroupMatch,
			s.limits,
		)
		if err != nil || matched {
			return result, matched, err
		}
	}
	if group.Parent != "" {
		return s.evaluateGroup(definition, group.Parent, context, visited, depth+1)
	}

	return evaluationResult{}, false, nil
}

func boundDiagnostics(diagnostics []Diagnostic, limits Limits) []Diagnostic {
	count := len(diagnostics)
	if count > limits.MaxDiagnostics {
		count = limits.MaxDiagnostics
	}
	if count <= 0 {
		return nil
	}
	bounded := make([]Diagnostic, count)
	for index := range count {
		bounded[index] = Diagnostic{
			Code:    truncateUTF8(diagnostics[index].Code, limits.MaxDiagnosticBytes),
			Message: truncateUTF8(diagnostics[index].Message, limits.MaxDiagnosticBytes),
		}
	}

	return bounded
}

func truncateUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}

	return value[:end]
}

func defaultResult(definition Definition, reason Reason, diagnostics []Diagnostic) evaluationResult {
	return evaluationResult{
		value:       definition.Default,
		reason:      reason,
		version:     definition.Version,
		diagnostics: diagnostics,
	}
}

func cloneDefinition(definition Definition) Definition {
	definition.Default = definition.Default.clone()
	variants := make(map[string]Value, len(definition.Variants))
	for name, variant := range definition.Variants {
		variants[name] = variant.clone()
	}
	definition.Variants = variants
	definition.Metadata = cloneStringMap(definition.Metadata)
	definition.Dependencies = append([]Dependency(nil), definition.Dependencies...)
	definition.Groups = append([]string(nil), definition.Groups...)
	definition.Tags = append([]string(nil), definition.Tags...)
	strategies := make([]Strategy, len(definition.Strategies))
	for index, strategy := range definition.Strategies {
		strategies[index] = strategy.SnapshotStrategy()
	}
	definition.Strategies = strategies

	return definition
}

func validateDependencyGraph(definitions map[string]Definition, limits Limits) error {
	for key, definition := range definitions {
		for _, dependency := range definition.Dependencies {
			target, exists := definitions[dependency.FeatureKey]
			if !exists {
				return fmt.Errorf("feature %q dependency %q: %w", key, dependency.FeatureKey, ErrNotFound)
			}
			if _, exists := target.Variants[dependency.RequiredVariant]; !exists {
				return fmt.Errorf(
					"feature %q dependency %q requires unknown variant %q",
					key,
					dependency.FeatureKey,
					dependency.RequiredVariant,
				)
			}
		}
	}

	visited := make(map[string]bool, len(definitions))
	visiting := make(map[string]bool, len(definitions))
	var visit func(string, int) error
	visit = func(key string, depth int) error {
		if depth > limits.MaxEvaluationDepth {
			return fmt.Errorf("feature %q: dependency depth exceeds %d", key, limits.MaxEvaluationDepth)
		}
		if visiting[key] {
			return fmt.Errorf("feature %q: %w", key, ErrDependencyCycle)
		}
		if visited[key] {
			return nil
		}
		visiting[key] = true
		for _, dependency := range definitions[key].Dependencies {
			if err := visit(dependency.FeatureKey, depth+1); err != nil {
				return err
			}
		}
		delete(visiting, key)
		visited[key] = true

		return nil
	}
	for key := range definitions {
		if err := visit(key, 0); err != nil {
			return err
		}
	}

	return nil
}

func cloneAndValidateGroups(
	definitions map[string]Definition,
	groups []GroupDefinition,
	limits Limits,
) (map[string]GroupDefinition, error) {
	if len(groups) > limits.MaxGroups {
		return nil, fmt.Errorf("group definitions exceed limit %d", limits.MaxGroups)
	}
	cloned := make(map[string]GroupDefinition, len(groups))
	for _, group := range groups {
		if group.Key == "" || len(group.Key) > limits.MaxKeyBytes {
			return nil, fmt.Errorf("group key is required and must not exceed %d bytes", limits.MaxKeyBytes)
		}
		if len(group.Parent) > limits.MaxKeyBytes {
			return nil, fmt.Errorf("group %q: parent exceeds configured bounds", group.Key)
		}
		if len(group.Owner) > limits.MaxStringBytes {
			return nil, fmt.Errorf("group %q: owner exceeds configured bounds", group.Key)
		}
		if len(group.Metadata) > limits.MaxMetadata {
			return nil, fmt.Errorf("group %q: metadata entries exceed limit %d", group.Key, limits.MaxMetadata)
		}
		if len(group.Tags) > limits.MaxTags {
			return nil, fmt.Errorf("group %q: tags exceed limit %d", group.Key, limits.MaxTags)
		}
		if _, exists := cloned[group.Key]; exists {
			return nil, fmt.Errorf("duplicate group %q", group.Key)
		}
		if len(group.Strategies) > limits.MaxStrategies {
			return nil, fmt.Errorf("group %q: strategies exceed limit %d", group.Key, limits.MaxStrategies)
		}
		group.Metadata = cloneStringMap(group.Metadata)
		group.Tags = append([]string(nil), group.Tags...)
		for key, value := range group.Metadata {
			if key == "" || len(key) > limits.MaxKeyBytes || len(value) > limits.MaxStringBytes {
				return nil, fmt.Errorf("group %q: metadata entry exceeds configured bounds", group.Key)
			}
		}
		for _, tag := range group.Tags {
			if tag == "" || len(tag) > limits.MaxKeyBytes {
				return nil, fmt.Errorf("group %q: tag exceeds configured bounds", group.Key)
			}
		}
		strategies := make([]Strategy, len(group.Strategies))
		for index, strategy := range group.Strategies {
			if strategy == nil {
				return nil, fmt.Errorf("group %q: strategy %d is nil", group.Key, index)
			}
			if strategy.StrategyName() == "" || len(strategy.StrategyName()) > limits.MaxKeyBytes {
				return nil, fmt.Errorf("group %q: strategy %d name is invalid", group.Key, index)
			}
			if strategy.TargetVariant() == "" || len(strategy.TargetVariant()) > limits.MaxKeyBytes {
				return nil, fmt.Errorf("group %q: strategy %q target is invalid", group.Key, strategy.StrategyName())
			}
			if err := strategy.ValidateStrategy(limits); err != nil {
				return nil, fmt.Errorf("group %q strategy %q: %w", group.Key, strategy.StrategyName(), err)
			}
			strategies[index] = strategy.SnapshotStrategy()
		}
		group.Strategies = strategies
		cloned[group.Key] = group
	}
	for key, group := range cloned {
		if group.Parent != "" {
			if _, exists := cloned[group.Parent]; !exists {
				return nil, fmt.Errorf("group %q parent %q: %w", key, group.Parent, ErrNotFound)
			}
		}
	}
	if err := validateGroupGraph(cloned, limits); err != nil {
		return nil, err
	}
	for featureKey, definition := range definitions {
		for _, groupKey := range definition.Groups {
			if _, exists := cloned[groupKey]; !exists {
				return nil, fmt.Errorf("feature %q group %q: %w", featureKey, groupKey, ErrNotFound)
			}
			for current := groupKey; current != ""; current = cloned[current].Parent {
				for _, strategy := range cloned[current].Strategies {
					if _, exists := definition.Variants[strategy.TargetVariant()]; !exists {
						return nil, fmt.Errorf(
							"feature %q group %q strategy %q targets unknown variant %q",
							featureKey,
							current,
							strategy.StrategyName(),
							strategy.TargetVariant(),
						)
					}
				}
			}
		}
	}

	return cloned, nil
}

func validateGroupGraph(groups map[string]GroupDefinition, limits Limits) error {
	for key := range groups {
		visiting := make(map[string]bool)
		current := key
		for depth := 0; current != ""; depth++ {
			if depth > limits.MaxGroupDepth {
				return fmt.Errorf("group %q: inheritance depth exceeds %d", key, limits.MaxGroupDepth)
			}
			if visiting[current] {
				return fmt.Errorf("group %q: %w", current, ErrGroupCycle)
			}
			visiting[current] = true
			current = groups[current].Parent
		}
	}

	return nil
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}

	return cloned
}
