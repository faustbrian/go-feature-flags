package featureflags

import "fmt"

// Lifecycle describes the management state of a feature definition.
type Lifecycle string

const (
	LifecycleDraft      Lifecycle = "draft"
	LifecycleActive     Lifecycle = "active"
	LifecycleInactive   Lifecycle = "inactive"
	LifecycleDeprecated Lifecycle = "deprecated"
	LifecycleArchived   Lifecycle = "archived"
)

// Definition is the stable native representation of a feature.
type Definition struct {
	Key          string
	Type         Type
	Default      Value
	Variants     map[string]Value
	Metadata     map[string]string
	Owner        string
	Lifecycle    Lifecycle
	Dependencies []Dependency
	Groups       []string
	Tags         []string
	Version      uint64
	Strategies   []Strategy
}

// Dependency requires another feature to select a named variant first.
type Dependency struct {
	FeatureKey      string
	RequiredVariant string
}

// GroupDefinition supplies reusable strategies inherited through an explicit
// parent chain. Feature definitions opt in through their Groups field.
type GroupDefinition struct {
	Key        string
	Parent     string
	Metadata   map[string]string
	Owner      string
	Tags       []string
	Version    uint64
	Strategies []Strategy
}

// Limits bounds untrusted management-plane definitions.
type Limits struct {
	MaxFeatures          int
	MaxVariants          int
	MaxDependencies      int
	MaxGroups            int
	MaxTags              int
	MaxMetadata          int
	MaxAuditEntries      int
	MaxAttributes        int
	MaxFacts             int
	MaxStrategies        int
	MaxTargetValues      int
	MaxScheduleWindows   int
	MaxEvaluationDepth   int
	MaxBatchSize         int
	MaxGroupDepth        int
	MaxStringBytes       int
	MaxStructuredBytes   int
	MaxContextKeyBytes   int
	MaxContextValueBytes int
	MaxKeyBytes          int
	MaxDiagnostics       int
	MaxDiagnosticBytes   int
	MaxImportBytes       int
	MaxStateBytes        int
	MaxStorageRetries    int
	MaxStagedChanges     int
}

func DefaultLimits() Limits {
	return Limits{
		MaxFeatures:          10_000,
		MaxVariants:          100,
		MaxDependencies:      32,
		MaxGroups:            32,
		MaxTags:              64,
		MaxMetadata:          64,
		MaxAuditEntries:      10_000,
		MaxAttributes:        128,
		MaxFacts:             128,
		MaxStrategies:        128,
		MaxTargetValues:      10_000,
		MaxScheduleWindows:   128,
		MaxEvaluationDepth:   64,
		MaxBatchSize:         256,
		MaxGroupDepth:        32,
		MaxStringBytes:       8 * 1024,
		MaxStructuredBytes:   1024 * 1024,
		MaxContextKeyBytes:   256,
		MaxContextValueBytes: 8 * 1024,
		MaxKeyBytes:          256,
		MaxDiagnostics:       16,
		MaxDiagnosticBytes:   256,
		MaxImportBytes:       16 * 1024 * 1024,
		MaxStateBytes:        32 * 1024 * 1024,
		MaxStorageRetries:    8,
		MaxStagedChanges:     1_000,
	}
}

// Validate rejects definitions whose values violate their declared type.
func (d Definition) Validate(limits Limits) error {
	if d.Key == "" || len(d.Key) > limits.MaxKeyBytes {
		return fmt.Errorf("feature key is required and must not exceed %d bytes", limits.MaxKeyBytes)
	}
	if err := d.Default.validate(limits); err != nil {
		return fmt.Errorf("feature %q default: %w", d.Key, err)
	}
	if d.Default.Type() != d.Type {
		return fmt.Errorf("feature %q: default has type %s, want %s", d.Key, d.Default.Type(), d.Type)
	}
	if len(d.Variants) > limits.MaxVariants {
		return fmt.Errorf("feature %q: variants exceed limit %d", d.Key, limits.MaxVariants)
	}
	if len(d.Metadata) > limits.MaxMetadata {
		return fmt.Errorf("feature %q: metadata entries exceed limit %d", d.Key, limits.MaxMetadata)
	}
	if len(d.Owner) > limits.MaxStringBytes {
		return fmt.Errorf("feature %q: owner exceeds limit %d", d.Key, limits.MaxStringBytes)
	}
	if len(d.Tags) > limits.MaxTags {
		return fmt.Errorf("feature %q: tags exceed limit %d", d.Key, limits.MaxTags)
	}
	if len(d.Strategies) > limits.MaxStrategies {
		return fmt.Errorf("feature %q: strategies exceed limit %d", d.Key, limits.MaxStrategies)
	}
	if len(d.Dependencies) > limits.MaxDependencies {
		return fmt.Errorf("feature %q: dependencies exceed limit %d", d.Key, limits.MaxDependencies)
	}
	if len(d.Groups) > limits.MaxGroups {
		return fmt.Errorf("feature %q: groups exceed limit %d", d.Key, limits.MaxGroups)
	}
	for index, dependency := range d.Dependencies {
		if dependency.FeatureKey == "" || dependency.RequiredVariant == "" ||
			len(dependency.FeatureKey) > limits.MaxKeyBytes ||
			len(dependency.RequiredVariant) > limits.MaxKeyBytes {
			return fmt.Errorf("feature %q: dependency %d requires feature key and variant", d.Key, index)
		}
	}
	for index, group := range d.Groups {
		if group == "" || len(group) > limits.MaxKeyBytes {
			return fmt.Errorf("feature %q: group %d exceeds configured bounds", d.Key, index)
		}
	}
	for name, variant := range d.Variants {
		if name == "" || len(name) > limits.MaxKeyBytes {
			return fmt.Errorf("feature %q: variant name is invalid", d.Key)
		}
		if err := variant.validate(limits); err != nil {
			return fmt.Errorf("feature %q variant %q: %w", d.Key, name, err)
		}
		if variant.Type() != d.Type {
			return fmt.Errorf("feature %q: variant %q has type %s, want %s", d.Key, name, variant.Type(), d.Type)
		}
	}
	for index, strategy := range d.Strategies {
		if strategy == nil {
			return fmt.Errorf("feature %q: strategy %d is nil", d.Key, index)
		}
		if strategy.StrategyName() == "" || len(strategy.StrategyName()) > limits.MaxKeyBytes {
			return fmt.Errorf("feature %q: strategy %d name is invalid", d.Key, index)
		}
		variant := strategy.TargetVariant()
		if _, exists := d.Variants[variant]; !exists {
			return fmt.Errorf("feature %q: strategy %q targets unknown variant %q", d.Key, strategy.StrategyName(), variant)
		}
		if err := strategy.ValidateStrategy(limits); err != nil {
			return fmt.Errorf("feature %q: strategy %q: %w", d.Key, strategy.StrategyName(), err)
		}
	}
	for key, value := range d.Metadata {
		if key == "" || len(key) > limits.MaxKeyBytes || len(value) > limits.MaxStringBytes {
			return fmt.Errorf("feature %q: metadata entry exceeds configured bounds", d.Key)
		}
	}
	for _, tag := range d.Tags {
		if tag == "" || len(tag) > limits.MaxKeyBytes {
			return fmt.Errorf("feature %q: tag exceeds configured bounds", d.Key)
		}
	}

	return nil
}
