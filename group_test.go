package featureflags

import (
	"errors"
	"testing"
)

func TestGroupStrategiesCascadeThroughParentInheritance(t *testing.T) {
	t.Parallel()

	snapshot, err := NewSnapshotWithGroups([]Definition{{
		Key:       "checkout.redesign",
		Type:      TypeBoolean,
		Default:   BooleanValue(false),
		Lifecycle: LifecycleActive,
		Variants:  map[string]Value{"enabled": BooleanValue(true)},
		Groups:    []string{"checkout"},
	}}, []GroupDefinition{
		{
			Key: "rollout",
			Strategies: []Strategy{ExactTargetStrategy{
				Name: "early-users", Variant: "enabled", Subjects: []string{"alice"},
			}},
		},
		{
			Key:    "checkout",
			Parent: "rollout",
			Strategies: []Strategy{ExactTargetStrategy{
				Name: "checkout-users", Variant: "enabled", Subjects: []string{"bob"},
			}},
		},
	}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewSnapshotWithGroups() error = %v", err)
	}

	for _, test := range []struct {
		subject string
		want    bool
		matched string
	}{
		{subject: "bob", want: true, matched: "group:checkout/checkout-users"},
		{subject: "alice", want: true, matched: "group:rollout/early-users"},
		{subject: "carol", want: false},
	} {
		t.Run(test.subject, func(t *testing.T) {
			t.Parallel()
			detail, err := snapshot.Boolean("checkout.redesign", Context{Subject: test.subject})
			if err != nil {
				t.Fatalf("Boolean() error = %v", err)
			}
			if detail.Value != test.want || detail.MatchedStrategy != test.matched {
				t.Fatalf("Boolean() = (%t, %q), want (%t, %q)", detail.Value, detail.MatchedStrategy, test.want, test.matched)
			}
		})
	}
}

func TestGroupStrategiesTakePrecedenceOverFeatureStrategies(t *testing.T) {
	t.Parallel()

	snapshot, err := NewSnapshotWithGroups([]Definition{{
		Key:       "checkout.redesign",
		Type:      TypeString,
		Default:   StringValue("control"),
		Lifecycle: LifecycleActive,
		Variants: map[string]Value{
			"group":   StringValue("group"),
			"feature": StringValue("feature"),
		},
		Groups: []string{"early-access"},
		Strategies: []Strategy{ExactTargetStrategy{
			Name: "feature-target", Variant: "feature", Subjects: []string{"alice"},
		}},
	}}, []GroupDefinition{{
		Key: "early-access",
		Strategies: []Strategy{ExactTargetStrategy{
			Name: "group-target", Variant: "group", Subjects: []string{"alice"},
		}},
	}}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewSnapshotWithGroups() error = %v", err)
	}

	detail, err := snapshot.String("checkout.redesign", Context{Subject: "alice"})
	if err != nil {
		t.Fatalf("String() error = %v", err)
	}
	if detail.Value != "group" || detail.MatchedStrategy != "group:early-access/group-target" {
		t.Fatalf(
			"String() = (%q, %q), want group policy to take precedence",
			detail.Value,
			detail.MatchedStrategy,
		)
	}
}

func TestNewSnapshotWithGroupsRejectsInheritanceCycle(t *testing.T) {
	t.Parallel()

	_, err := NewSnapshotWithGroups(nil, []GroupDefinition{
		{Key: "group-a", Parent: "group-b"},
		{Key: "group-b", Parent: "group-a"},
	}, DefaultLimits())
	if !errors.Is(err, ErrGroupCycle) {
		t.Fatalf("NewSnapshotWithGroups() error = %v, want ErrGroupCycle", err)
	}
}
