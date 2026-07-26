package featureflags

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryProviderRejectsManagementConflictsAndMissingResources(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxFeatures = 1
	limits.MaxAuditEntries = 1
	limits.MaxStagedChanges = 1
	provider := NewMemoryProvider(limits)
	definition := Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
		Lifecycle: LifecycleActive,
		Variants:  map[string]Value{"on": BooleanValue(true)},
	}
	if _, err := provider.Create(t.Context(), "", definition, "alice"); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("Create(empty tenant) error = %v", err)
	}
	if _, err := provider.Create(t.Context(), "tenant", Definition{}, "alice"); err == nil {
		t.Fatal("Create(invalid definition) succeeded")
	}
	created, err := provider.Create(t.Context(), "tenant", definition, "alice")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := provider.Create(t.Context(), "tenant", definition, "alice"); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("Create(duplicate) error = %v", err)
	}
	other := definition
	other.Key = "other"
	if _, err := provider.Create(t.Context(), "tenant", other, "alice"); err == nil {
		t.Fatal("Create(over limit) succeeded")
	}
	if _, err := provider.Update(t.Context(), "tenant", other, 1, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(missing) error = %v", err)
	}
	if _, err := provider.Update(t.Context(), "tenant", Definition{}, 1, "alice"); err == nil {
		t.Fatal("Update(invalid) succeeded")
	}
	if _, err := provider.Update(t.Context(), "tenant", definition, 99, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("Update(conflict) error = %v", err)
	}

	if _, err := provider.CreateGroup(t.Context(), "tenant", GroupDefinition{}, "alice"); err == nil {
		t.Fatal("CreateGroup(invalid) succeeded")
	}
	parent, err := provider.CreateGroup(t.Context(), "tenant", GroupDefinition{Key: "parent"}, "alice")
	if err != nil {
		t.Fatalf("CreateGroup(parent) error = %v", err)
	}
	if _, err := provider.CreateGroup(t.Context(), "tenant", parent, "alice"); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("CreateGroup(duplicate) error = %v", err)
	}
	child, err := provider.CreateGroup(t.Context(), "tenant", GroupDefinition{Key: "child", Parent: parent.Key}, "alice")
	if err != nil {
		t.Fatalf("CreateGroup(child) error = %v", err)
	}
	if _, err := provider.UpdateGroup(t.Context(), "tenant", GroupDefinition{Key: "missing"}, 1, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateGroup(missing) error = %v", err)
	}
	if _, err := provider.UpdateGroup(t.Context(), "tenant", parent, 99, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateGroup(conflict) error = %v", err)
	}
	invalidParent := parent
	invalidParent.Parent = "missing"
	if _, err := provider.UpdateGroup(t.Context(), "tenant", invalidParent, parent.Version, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateGroup(invalid parent) error = %v", err)
	}
	if _, err := provider.DeleteGroup(t.Context(), "tenant", "missing", 1, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteGroup(missing) error = %v", err)
	}
	if _, err := provider.DeleteGroup(t.Context(), "tenant", child.Key, 99, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteGroup(conflict) error = %v", err)
	}
	if _, err := provider.DeleteGroup(t.Context(), "tenant", parent.Key, parent.Version, "alice"); !errors.Is(err, ErrGroupInUse) {
		t.Fatalf("DeleteGroup(parent) error = %v", err)
	}

	if _, err := provider.AssignGroup(t.Context(), "tenant", created.Key, "missing", created.Version, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AssignGroup(missing group) error = %v", err)
	}
	if _, err := provider.AssignGroup(t.Context(), "tenant", "missing", child.Key, 1, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AssignGroup(missing feature) error = %v", err)
	}
	if _, err := provider.AssignGroup(t.Context(), "tenant", created.Key, child.Key, 99, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("AssignGroup(conflict) error = %v", err)
	}
	assigned, err := provider.AssignGroup(t.Context(), "tenant", created.Key, child.Key, created.Version, "alice")
	if err != nil {
		t.Fatalf("AssignGroup() error = %v", err)
	}
	duplicate, err := provider.AssignGroup(t.Context(), "tenant", created.Key, child.Key, assigned.Version, "alice")
	if err != nil || duplicate.Version != assigned.Version {
		t.Fatalf("AssignGroup(idempotent) = (%#v, %v)", duplicate, err)
	}
	if _, err := provider.DeleteGroup(t.Context(), "tenant", child.Key, child.Version, "alice"); !errors.Is(err, ErrGroupInUse) {
		t.Fatalf("DeleteGroup(assigned) error = %v", err)
	}
	if _, err := provider.RemoveGroup(t.Context(), "tenant", "missing", child.Key, 1, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RemoveGroup(missing feature) error = %v", err)
	}
	if _, err := provider.RemoveGroup(t.Context(), "tenant", created.Key, child.Key, 99, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("RemoveGroup(conflict) error = %v", err)
	}
	if _, err := provider.RemoveGroup(t.Context(), "tenant", created.Key, parent.Key, assigned.Version, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RemoveGroup(missing membership) error = %v", err)
	}

	if _, err := provider.Activate(t.Context(), "tenant", "missing", 1, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Activate(missing) error = %v", err)
	}
	if _, err := provider.Activate(t.Context(), "tenant", created.Key, 99, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("Activate(conflict) error = %v", err)
	}
	if _, err := provider.Restore(t.Context(), "tenant", created.Key, assigned.Version, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Restore(active) error = %v", err)
	}
}

func TestMemoryProviderRejectsInvalidStageAndCleanupOperations(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxStagedChanges = 1
	provider := NewMemoryProvider(limits)
	definition := Definition{Key: "flag", Type: TypeBoolean, Default: BooleanValue(false)}
	created, err := provider.Create(t.Context(), "tenant", definition, "alice")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := provider.StageUpdate(t.Context(), "tenant", Definition{}, 1, time.Time{}, "alice"); err == nil {
		t.Fatal("StageUpdate(invalid) succeeded")
	}
	missing := definition
	missing.Key = "missing"
	if _, err := provider.StageUpdate(t.Context(), "tenant", missing, 1, time.Time{}, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("StageUpdate(missing) error = %v", err)
	}
	if _, err := provider.StageUpdate(t.Context(), "tenant", definition, 99, time.Time{}, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("StageUpdate(conflict) error = %v", err)
	}
	stage, err := provider.StageUpdate(t.Context(), "tenant", definition, created.Version, time.Now(), "alice")
	if err != nil {
		t.Fatalf("StageUpdate() error = %v", err)
	}
	if _, err := provider.StageUpdate(t.Context(), "tenant", definition, created.Version, time.Now(), "alice"); err == nil {
		t.Fatal("StageUpdate(over limit) succeeded")
	}
	if _, err := provider.ApplyStage(t.Context(), "tenant", stage.ID+1, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ApplyStage(missing) error = %v", err)
	}
	if _, err := provider.ApplyScheduled(t.Context(), "tenant", time.Time{}, "alice"); err == nil {
		t.Fatal("ApplyScheduled(zero time) succeeded")
	}
	if applied, err := provider.ApplyScheduled(t.Context(), "tenant", stage.ApplyAt.Add(-time.Second), "alice"); err != nil || len(applied) != 0 {
		t.Fatalf("ApplyScheduled(before due) = (%#v, %v)", applied, err)
	}
	if _, err := provider.Cleanup(t.Context(), "tenant", CleanupOptions{KeepAudit: -1}); err == nil {
		t.Fatal("Cleanup(negative audit) succeeded")
	}
	if _, err := provider.Cleanup(t.Context(), "tenant", CleanupOptions{KeepAudit: limits.MaxAuditEntries + 1}); err == nil {
		t.Fatal("Cleanup(large audit) succeeded")
	}

	updated := definition
	updated.Default = BooleanValue(true)
	if _, err := provider.Update(t.Context(), "tenant", updated, created.Version, "alice"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := provider.ApplyStage(t.Context(), "tenant", stage.ID, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("ApplyStage(stale) error = %v", err)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if health := provider.Health(cancelled); health.Code != "context_cancelled" {
		t.Fatalf("Health(cancelled) = %#v", health)
	}
	if err := provider.Close(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close(cancelled) error = %v", err)
	}
	if _, err := provider.StagedChanges(cancelled, "tenant"); !errors.Is(err, context.Canceled) {
		t.Fatalf("StagedChanges(cancelled) error = %v", err)
	}
}
