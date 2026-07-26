package featureflags

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEveryManagementEntryPointHonorsCancellation(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	definition := Definition{Key: "flag", Type: TypeBoolean, Default: BooleanValue(false)}
	operations := []func() error{
		func() error { _, err := provider.Update(ctx, "tenant", definition, 1, "actor"); return err },
		func() error { _, err := provider.Snapshot(ctx, "tenant"); return err },
		func() error {
			_, err := provider.CreateGroup(ctx, "tenant", GroupDefinition{Key: "group"}, "actor")
			return err
		},
		func() error {
			_, err := provider.UpdateGroup(ctx, "tenant", GroupDefinition{Key: "group"}, 1, "actor")
			return err
		},
		func() error { _, err := provider.DeleteGroup(ctx, "tenant", "group", 1, "actor"); return err },
		func() error { _, err := provider.AssignGroup(ctx, "tenant", "flag", "group", 1, "actor"); return err },
		func() error { _, err := provider.RemoveGroup(ctx, "tenant", "flag", "group", 1, "actor"); return err },
		func() error { _, err := provider.Restore(ctx, "tenant", "flag", 1, "actor"); return err },
		func() error { _, err := provider.Audit(ctx, "tenant", "flag"); return err },
		func() error { _, err := provider.Activate(ctx, "tenant", "flag", 1, "actor"); return err },
		func() error {
			_, err := provider.StageUpdate(ctx, "tenant", definition, 1, time.Time{}, "actor")
			return err
		},
		func() error { _, err := provider.ApplyStage(ctx, "tenant", 1, "actor"); return err },
		func() error { _, err := provider.ApplyScheduled(ctx, "tenant", time.Now(), "actor"); return err },
		func() error { _, err := provider.Cleanup(ctx, "tenant", CleanupOptions{}); return err },
		func() error { _, err := provider.ExportDocument(ctx, "tenant"); return err },
		func() error {
			_, err := provider.ImportDocument(ctx, "tenant", nil, ImportOptions{}, "actor")
			return err
		},
	}
	for index, operation := range operations {
		if err := operation(); !errors.Is(err, context.Canceled) {
			t.Fatalf("operation %d error = %v, want context.Canceled", index, err)
		}
	}
}

func TestStageOrderingDuplicatesAndCorruptReferences(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	for _, key := range []string{"first", "second"} {
		if _, err := provider.Create(t.Context(), "tenant", Definition{
			Key: key, Type: TypeBoolean, Default: BooleanValue(false),
		}, "actor"); err != nil {
			t.Fatalf("Create(%s) error = %v", key, err)
		}
	}
	due := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	for _, key := range []string{"second", "first"} {
		if _, err := provider.StageUpdate(t.Context(), "tenant", Definition{
			Key: key, Type: TypeBoolean, Default: BooleanValue(true),
		}, 1, due, "actor"); err != nil {
			t.Fatalf("StageUpdate(%s) error = %v", key, err)
		}
	}
	if _, err := provider.StageUpdate(t.Context(), "tenant", Definition{
		Key: "first", Type: TypeBoolean, Default: BooleanValue(true),
	}, 1, due, "actor"); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("StageUpdate(duplicate) error = %v", err)
	}
	stages, err := provider.StagedChanges(t.Context(), "tenant")
	if err != nil || len(stages) != 2 || stages[0].ID >= stages[1].ID {
		t.Fatalf("StagedChanges() = (%#v, %v)", stages, err)
	}
	applied, err := provider.ApplyScheduled(t.Context(), "tenant", due, "actor")
	if err != nil || len(applied) != 2 || applied[0].Key != "second" {
		t.Fatalf("ApplyScheduled() = (%#v, %v)", applied, err)
	}

	corrupt := NewMemoryProvider(DefaultLimits())
	corrupt.staged["tenant"] = map[uint64]StagedChange{1: {
		ID: 1, Definition: Definition{Key: "missing", Type: TypeBoolean, Default: BooleanValue(false)},
		ExpectedVersion: 1,
	}}
	if _, err := corrupt.ApplyStage(t.Context(), "tenant", 1, "actor"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ApplyStage(corrupt reference) error = %v", err)
	}
}

func TestSnapshotAndGroupMutationRejectCorruptCombinedState(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	provider.tenants["tenant"] = map[string]memoryRecord{"invalid": {
		definition: Definition{Key: "", Type: TypeBoolean, Default: BooleanValue(false)},
	}}
	if _, err := provider.Snapshot(t.Context(), "tenant"); err == nil {
		t.Fatal("Snapshot(corrupt state) succeeded")
	}

	provider = NewMemoryProvider(DefaultLimits())
	feature, err := provider.Create(t.Context(), "tenant", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
	}, "actor")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := provider.CreateGroup(t.Context(), "tenant", GroupDefinition{
		Key: "targeted", Strategies: []Strategy{ExactTargetStrategy{
			Name: "rule", Variant: "on",
		}},
	}, "actor"); err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if _, err := provider.AssignGroup(
		t.Context(), "tenant", feature.Key, "targeted", feature.Version, "actor",
	); err == nil {
		t.Fatal("AssignGroup(incompatible target) succeeded")
	}
	deleted, err := provider.Delete(t.Context(), "tenant", feature.Key, feature.Version, "actor")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := provider.Restore(t.Context(), "tenant", feature.Key, deleted.Version+1, "actor"); !errors.Is(err, ErrConflict) {
		t.Fatalf("Restore(conflict) error = %v", err)
	}
}

func TestCleanupDiscardsExpiredStageWithoutPurgingFeature(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	feature, err := provider.Create(t.Context(), "tenant", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
	}, "actor")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	at := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	if _, err := provider.StageUpdate(
		t.Context(), "tenant", feature, feature.Version, at, "actor",
	); err != nil {
		t.Fatalf("StageUpdate() error = %v", err)
	}
	report, err := provider.Cleanup(t.Context(), "tenant", CleanupOptions{
		DiscardStagesBefore: at.Add(time.Second),
	})
	if err != nil || report.DiscardedStages != 1 || report.DeletedFeatures != 0 {
		t.Fatalf("Cleanup() = (%#v, %v)", report, err)
	}
}

func TestStageApplicationValidatesCombinedGroupState(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	feature, err := provider.Create(t.Context(), "tenant", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
	}, "actor")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	feature.Groups = []string{"missing"}
	stage, err := provider.StageUpdate(
		t.Context(), "tenant", feature, feature.Version, time.Time{}, "actor",
	)
	if err != nil {
		t.Fatalf("StageUpdate() error = %v", err)
	}
	if _, err := provider.ApplyStage(t.Context(), "tenant", stage.ID, "actor"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ApplyStage(invalid combined state) error = %v", err)
	}
}
