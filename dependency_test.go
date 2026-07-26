package featureflags

import (
	"errors"
	"testing"
)

func TestDependencyRequiresReferencedVariantBeforeEvaluation(t *testing.T) {
	t.Parallel()

	definitions := []Definition{
		{
			Key:       "account.beta",
			Type:      TypeBoolean,
			Default:   BooleanValue(false),
			Lifecycle: LifecycleActive,
			Variants:  map[string]Value{"member": BooleanValue(true)},
			Strategies: []Strategy{ExactTargetStrategy{
				Name: "all-beta", Variant: "member",
			}},
		},
		{
			Key:          "checkout.redesign",
			Type:         TypeBoolean,
			Default:      BooleanValue(false),
			Lifecycle:    LifecycleActive,
			Dependencies: []Dependency{{FeatureKey: "account.beta", RequiredVariant: "member"}},
			Variants:     map[string]Value{"enabled": BooleanValue(true)},
			Strategies: []Strategy{ExactTargetStrategy{
				Name: "all-checkouts", Variant: "enabled",
			}},
		},
	}
	snapshot, err := NewSnapshot(definitions, DefaultLimits())
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}

	detail, err := snapshot.Boolean("checkout.redesign", Context{})
	if err != nil {
		t.Fatalf("Boolean() error = %v", err)
	}
	if !detail.Value || detail.Variant != "enabled" {
		t.Fatalf("Boolean() = (%t, %q), want (true, enabled)", detail.Value, detail.Variant)
	}

	definitions[0].Strategies = nil
	snapshot, err = NewSnapshot(definitions, DefaultLimits())
	if err != nil {
		t.Fatalf("NewSnapshot(without prerequisite) error = %v", err)
	}
	detail, err = snapshot.Boolean("checkout.redesign", Context{})
	if err != nil {
		t.Fatalf("Boolean(without prerequisite) error = %v", err)
	}
	if detail.Value || detail.Reason != ReasonDependencyFailed {
		t.Fatalf("Boolean(without prerequisite) = (%t, %q), want (false, dependency_failed)", detail.Value, detail.Reason)
	}
}

func TestNewSnapshotRejectsDependencyCycle(t *testing.T) {
	t.Parallel()

	definition := func(key, dependency string) Definition {
		return Definition{
			Key:          key,
			Type:         TypeBoolean,
			Default:      BooleanValue(false),
			Lifecycle:    LifecycleActive,
			Variants:     map[string]Value{"enabled": BooleanValue(true)},
			Dependencies: []Dependency{{FeatureKey: dependency, RequiredVariant: "enabled"}},
		}
	}
	_, err := NewSnapshot([]Definition{
		definition("feature-a", "feature-b"),
		definition("feature-b", "feature-a"),
	}, DefaultLimits())
	if !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("NewSnapshot() error = %v, want ErrDependencyCycle", err)
	}
}
