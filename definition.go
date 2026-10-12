package featureflags

import "fmt"

// Lifecycle describes the management state of a feature definition.
type Lifecycle string

const (
	// LifecycleDraft is a non-active management label; evaluation returns the definition
	// default.
	LifecycleDraft Lifecycle = "draft"
	// LifecycleActive enables dependency, group and strategy evaluation.
	LifecycleActive Lifecycle = "active"
	// LifecycleInactive is a non-active management label; evaluation returns the definition
	// default.
	LifecycleInactive Lifecycle = "inactive"
	// LifecycleDeprecated is a non-active management label; evaluation returns the definition
	// default.
	LifecycleDeprecated Lifecycle = "deprecated"
	// LifecycleArchived is a non-active management label; evaluation returns the definition
	// default.
	LifecycleArchived Lifecycle = "archived"
)

// Definition is the mutable native representation of a feature. Snapshot
// construction copies collections and invokes trusted SnapshotStrategy callbacks.
// Validate does not validate the Lifecycle enum or authenticate Owner/Metadata.
type Definition struct {
	// Key is the nonempty tenant-local identity, bounded by MaxKeyBytes.
	Key string
	// Type is the exact native type shared by Default and all Variants.
	Type Type
	// Default is returned for inactive, unmatched or dependency-failed evaluation.
	Default Value
	// Variants maps nonempty names to typed values; snapshot construction copies it.
	Variants map[string]Value
	// Metadata contains caller-supplied management labels, not evaluation context.
	Metadata map[string]string
	// Owner is caller-supplied attribution, not a verified principal.
	Owner string
	// Lifecycle enables strategies only when exactly LifecycleActive; other values default.
	Lifecycle Lifecycle
	// Dependencies requires every referenced variant before evaluating this feature.
	Dependencies []Dependency
	// Groups orders child-first inherited groups before local Strategies.
	Groups []string
	// Tags contains bounded management labels, not targeting predicates.
	Tags []string
	// Version is the optimistic feature version; management mutations assign it.
	Version uint64
	// Strategies is ordered first-match policy; custom snapshot callbacks must isolate state.
	Strategies []Strategy
}

// Dependency requires another feature to select a named variant first.
type Dependency struct {
	// FeatureKey names the required feature in the same snapshot.
	FeatureKey string
	// RequiredVariant names the variant that must be selected; default has no variant.
	RequiredVariant string
}

// GroupDefinition supplies reusable strategies inherited through an explicit
// parent chain. Feature definitions opt in through their Groups field.
type GroupDefinition struct {
	// Key is the nonempty tenant-local group identity.
	Key string
	// Parent is the optional inherited group, evaluated after child strategies.
	Parent string
	// Metadata contains bounded caller-supplied management labels.
	Metadata map[string]string
	// Owner is caller-supplied attribution, not authentication.
	Owner string
	// Tags contains bounded management labels.
	Tags []string
	// Version is the optimistic group version assigned by management mutations.
	Version uint64
	// Strategies is ordered child policy; SnapshotStrategy must isolate mutable state.
	Strategies []Strategy
}

// Limits supplies explicit count/byte/depth bounds, not a total heap/time cap.
// Use DefaultLimits explicitly: constructors do not merge or validate all fields.
// Limits do not bound tenant count, audit Actor bytes or custom callback work.
type Limits struct {
	// MaxFeatures bounds retained features per tenant, including memory tombstones.
	MaxFeatures int
	// MaxVariants bounds named variants per feature.
	MaxVariants int
	// MaxDependencies bounds references per feature.
	MaxDependencies int
	// MaxGroups bounds group definitions and feature group attachments.
	MaxGroups int
	// MaxTags bounds tags per feature/group.
	MaxTags int
	// MaxMetadata bounds metadata entries per feature/group.
	MaxMetadata int
	// MaxAuditEntries bounds retained audit entry count, not actor byte length.
	MaxAuditEntries int
	// MaxAttributes bounds string context/target attributes.
	MaxAttributes int
	// MaxFacts bounds typed context facts.
	MaxFacts int
	// MaxStrategies bounds ordered strategies per feature/group.
	MaxStrategies int
	// MaxTargetValues bounds combined exact/set target entries.
	MaxTargetValues int
	// MaxScheduleWindows bounds weekly windows per schedule.
	MaxScheduleWindows int
	// MaxEvaluationDepth bounds dependency/structured traversal depth.
	MaxEvaluationDepth int
	// MaxBatchSize bounds requests per native Batch.
	MaxBatchSize int
	// MaxGroupDepth bounds parent-chain depth.
	MaxGroupDepth int
	// MaxStringBytes bounds string/decimal/owner/metadata value bytes.
	MaxStringBytes int
	// MaxStructuredBytes bounds structured JSON bytes, not total heap allocation.
	MaxStructuredBytes int
	// MaxContextKeyBytes bounds attribute/fact key bytes.
	MaxContextKeyBytes int
	// MaxContextValueBytes bounds subject/tenant/environment/attribute value bytes.
	MaxContextValueBytes int
	// MaxKeyBytes bounds management identities and tenant key bytes.
	MaxKeyBytes int
	// MaxDiagnostics bounds retained diagnostics per matching strategy.
	MaxDiagnostics int
	// MaxDiagnosticBytes bounds bytes per diagnostic code/message.
	MaxDiagnosticBytes int
	// MaxImportBytes bounds input portable document bytes before decoding.
	MaxImportBytes int
	// MaxStateBytes bounds persisted document bytes; encoding allocates before rejection.
	MaxStateBytes int
	// MaxStorageRetries bounds additional durable CAS conflict retries beyond the first
	// attempt.
	MaxStorageRetries int
	// MaxStagedChanges bounds pending changes per tenant.
	MaxStagedChanges int
}

// DefaultLimits returns the finite default limits; constructors do not merge zero fields.
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
