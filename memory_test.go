package featureflags

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryProviderUpdateIsTenantScopedAndOptimistic(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	base := Definition{
		Key:       "checkout.redesign",
		Type:      TypeBoolean,
		Default:   BooleanValue(false),
		Lifecycle: LifecycleActive,
	}
	createdA, err := provider.Create(context.Background(), "tenant-a", base, "operator-a")
	if err != nil {
		t.Fatalf("Create(tenant-a) error = %v", err)
	}
	createdB, err := provider.Create(context.Background(), "tenant-b", base, "operator-b")
	if err != nil {
		t.Fatalf("Create(tenant-b) error = %v", err)
	}
	if createdA.Version != 1 || createdB.Version != 1 {
		t.Fatalf("Create() versions = (%d, %d), want (1, 1)", createdA.Version, createdB.Version)
	}

	updated := base
	updated.Default = BooleanValue(true)
	updatedA, err := provider.Update(context.Background(), "tenant-a", updated, 1, "operator-a")
	if err != nil {
		t.Fatalf("Update(tenant-a) error = %v", err)
	}
	if updatedA.Version != 2 {
		t.Fatalf("Update() version = %d, want 2", updatedA.Version)
	}
	_, err = provider.Update(context.Background(), "tenant-a", base, 1, "operator-a")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Update() error = %v, want ErrConflict", err)
	}

	snapshotA, err := provider.Snapshot(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("Snapshot(tenant-a) error = %v", err)
	}
	snapshotB, err := provider.Snapshot(context.Background(), "tenant-b")
	if err != nil {
		t.Fatalf("Snapshot(tenant-b) error = %v", err)
	}
	detailA, err := snapshotA.Boolean("checkout.redesign", Context{Tenant: "tenant-a"})
	if err != nil {
		t.Fatalf("Boolean(tenant-a) error = %v", err)
	}
	detailB, err := snapshotB.Boolean("checkout.redesign", Context{Tenant: "tenant-b"})
	if err != nil {
		t.Fatalf("Boolean(tenant-b) error = %v", err)
	}
	if !detailA.Value || detailB.Value {
		t.Fatalf("tenant values = (%t, %t), want (true, false)", detailA.Value, detailB.Value)
	}

	_, err = snapshotA.Boolean("checkout.redesign", Context{Tenant: "tenant-b"})
	if !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("Boolean(cross-tenant context) error = %v, want ErrTenantMismatch", err)
	}
}
