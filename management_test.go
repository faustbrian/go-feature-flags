package featureflags

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryProviderLifecycleAndAuditAreAtomic(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	created, err := provider.Create(context.Background(), "tenant-a", Definition{
		Key:       "checkout.redesign",
		Type:      TypeBoolean,
		Default:   BooleanValue(false),
		Lifecycle: LifecycleDraft,
	}, "alice")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	activated, err := provider.Activate(context.Background(), "tenant-a", created.Key, 1, "alice")
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	deactivated, err := provider.Deactivate(context.Background(), "tenant-a", created.Key, 2, "bob")
	if err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	deleted, err := provider.Delete(context.Background(), "tenant-a", created.Key, 3, "bob")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if activated.Lifecycle != LifecycleActive || deactivated.Lifecycle != LifecycleInactive || deleted.Version != 4 {
		t.Fatalf("lifecycle results = (%s, %s, v%d)", activated.Lifecycle, deactivated.Lifecycle, deleted.Version)
	}

	snapshot, err := provider.Snapshot(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("Snapshot(deleted) error = %v", err)
	}
	if _, err := snapshot.Boolean(created.Key, Context{Tenant: "tenant-a"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Boolean(deleted) error = %v, want ErrNotFound", err)
	}

	restored, err := provider.Restore(context.Background(), "tenant-a", created.Key, 4, "carol")
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if restored.Version != 5 || restored.Lifecycle != LifecycleInactive {
		t.Fatalf("Restore() = (v%d, %s), want (v5, inactive)", restored.Version, restored.Lifecycle)
	}

	audit, err := provider.Audit(context.Background(), "tenant-a", created.Key)
	if err != nil {
		t.Fatalf("Audit() error = %v", err)
	}
	wantActions := []AuditAction{AuditCreate, AuditActivate, AuditDeactivate, AuditDelete, AuditRestore}
	if len(audit) != len(wantActions) {
		t.Fatalf("Audit() entries = %d, want %d", len(audit), len(wantActions))
	}
	for index, want := range wantActions {
		if audit[index].Action != want || audit[index].Version != uint64(index+1) {
			t.Fatalf("Audit()[%d] = (%s, v%d), want (%s, v%d)", index, audit[index].Action, audit[index].Version, want, index+1)
		}
	}
}
