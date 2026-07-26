package featureflags

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryProviderAssignsFeatureToManagedGroupAtomically(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	feature, err := provider.Create(context.Background(), "tenant-a", Definition{
		Key:       "checkout.redesign",
		Type:      TypeBoolean,
		Default:   BooleanValue(false),
		Lifecycle: LifecycleActive,
		Variants:  map[string]Value{"enabled": BooleanValue(true)},
	}, "alice")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	group, err := provider.CreateGroup(context.Background(), "tenant-a", GroupDefinition{
		Key: "beta",
		Strategies: []Strategy{ExactTargetStrategy{
			Name: "beta-users", Variant: "enabled", Subjects: []string{"user-123"},
		}},
	}, "alice")
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if group.Version != 1 {
		t.Fatalf("CreateGroup() version = %d, want 1", group.Version)
	}

	assigned, err := provider.AssignGroup(
		context.Background(),
		"tenant-a",
		feature.Key,
		"beta",
		feature.Version,
		"alice",
	)
	if err != nil {
		t.Fatalf("AssignGroup() error = %v", err)
	}
	if assigned.Version != 2 || len(assigned.Groups) != 1 || assigned.Groups[0] != "beta" {
		t.Fatalf("AssignGroup() = (v%d, %v), want (v2, [beta])", assigned.Version, assigned.Groups)
	}

	snapshot, err := provider.Snapshot(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	detail, err := snapshot.Boolean(feature.Key, Context{Tenant: "tenant-a", Subject: "user-123"})
	if err != nil {
		t.Fatalf("Boolean() error = %v", err)
	}
	if !detail.Value || detail.Reason != ReasonGroupMatch {
		t.Fatalf("Boolean() = (%t, %q), want (true, group_match)", detail.Value, detail.Reason)
	}
}

func TestMemoryProviderUpdatesRemovesAndDeletesManagedGroups(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	feature, err := provider.Create(context.Background(), "tenant-a", Definition{
		Key:       "checkout.redesign",
		Type:      TypeBoolean,
		Default:   BooleanValue(false),
		Lifecycle: LifecycleActive,
		Variants:  map[string]Value{"enabled": BooleanValue(true)},
	}, "alice")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	group, err := provider.CreateGroup(context.Background(), "tenant-a", GroupDefinition{
		Key: "beta",
		Strategies: []Strategy{ExactTargetStrategy{
			Name: "beta-users", Variant: "enabled", Subjects: []string{"user-123"},
		}},
	}, "alice")
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	feature, err = provider.AssignGroup(
		context.Background(), "tenant-a", feature.Key, group.Key, feature.Version, "alice",
	)
	if err != nil {
		t.Fatalf("AssignGroup() error = %v", err)
	}

	group.Owner = "platform"
	updated, err := provider.UpdateGroup(
		context.Background(), "tenant-a", group, group.Version, "alice",
	)
	if err != nil {
		t.Fatalf("UpdateGroup() error = %v", err)
	}
	if updated.Version != 2 || updated.Owner != "platform" {
		t.Fatalf("UpdateGroup() = %#v, want version 2 owned by platform", updated)
	}
	if _, err := provider.DeleteGroup(
		context.Background(), "tenant-a", group.Key, updated.Version, "alice",
	); !errors.Is(err, ErrGroupInUse) {
		t.Fatalf("DeleteGroup() while assigned error = %v, want ErrGroupInUse", err)
	}

	feature, err = provider.RemoveGroup(
		context.Background(), "tenant-a", feature.Key, group.Key, feature.Version, "alice",
	)
	if err != nil {
		t.Fatalf("RemoveGroup() error = %v", err)
	}
	if len(feature.Groups) != 0 || feature.Version != 3 {
		t.Fatalf("RemoveGroup() = (v%d, %v), want (v3, none)", feature.Version, feature.Groups)
	}
	deleted, err := provider.DeleteGroup(
		context.Background(), "tenant-a", group.Key, updated.Version, "alice",
	)
	if err != nil {
		t.Fatalf("DeleteGroup() error = %v", err)
	}
	if deleted.Key != group.Key || deleted.Version != 3 {
		t.Fatalf("DeleteGroup() = %#v, want beta version 3", deleted)
	}
}
