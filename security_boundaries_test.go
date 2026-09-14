package featureflags

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type countingDocumentBackend struct {
	loads int
}

func (backend *countingDocumentBackend) Load(context.Context, string) ([]byte, uint64, bool, error) {
	backend.loads++
	return nil, 0, false, nil
}

func (*countingDocumentBackend) CompareAndSwap(context.Context, string, uint64, []byte) error {
	return nil
}

func (*countingDocumentBackend) Health(context.Context) ProviderHealth {
	return ProviderHealth{Healthy: true, Code: "ready"}
}

func (*countingDocumentBackend) Close(context.Context) error { return nil }

type countingSnapshotProvider struct {
	Provider
	snapshots int
}

type countingAllProvider struct {
	calls int
}

func (*countingAllProvider) Capabilities() Capabilities { return Capabilities{} }
func (provider *countingAllProvider) called()           { provider.calls++ }
func (provider *countingAllProvider) Create(context.Context, string, Definition, string) (Definition, error) {
	provider.called()
	return Definition{}, nil
}
func (provider *countingAllProvider) Update(context.Context, string, Definition, uint64, string) (Definition, error) {
	provider.called()
	return Definition{}, nil
}
func (provider *countingAllProvider) Activate(context.Context, string, string, uint64, string) (Definition, error) {
	provider.called()
	return Definition{}, nil
}
func (provider *countingAllProvider) Deactivate(context.Context, string, string, uint64, string) (Definition, error) {
	provider.called()
	return Definition{}, nil
}
func (provider *countingAllProvider) Delete(context.Context, string, string, uint64, string) (Definition, error) {
	provider.called()
	return Definition{}, nil
}
func (provider *countingAllProvider) Restore(context.Context, string, string, uint64, string) (Definition, error) {
	provider.called()
	return Definition{}, nil
}
func (provider *countingAllProvider) Snapshot(context.Context, string) (Snapshot, error) {
	provider.called()
	return Snapshot{}, nil
}
func (provider *countingAllProvider) Audit(context.Context, string, string) ([]AuditEntry, error) {
	provider.called()
	return nil, nil
}
func (provider *countingAllProvider) CreateGroup(context.Context, string, GroupDefinition, string) (GroupDefinition, error) {
	provider.called()
	return GroupDefinition{}, nil
}
func (provider *countingAllProvider) UpdateGroup(context.Context, string, GroupDefinition, uint64, string) (GroupDefinition, error) {
	provider.called()
	return GroupDefinition{}, nil
}
func (provider *countingAllProvider) DeleteGroup(context.Context, string, string, uint64, string) (GroupDefinition, error) {
	provider.called()
	return GroupDefinition{}, nil
}
func (provider *countingAllProvider) AssignGroup(context.Context, string, string, string, uint64, string) (Definition, error) {
	provider.called()
	return Definition{}, nil
}
func (provider *countingAllProvider) RemoveGroup(context.Context, string, string, string, uint64, string) (Definition, error) {
	provider.called()
	return Definition{}, nil
}
func (provider *countingAllProvider) ExportDocument(context.Context, string) ([]byte, error) {
	provider.called()
	return nil, nil
}
func (provider *countingAllProvider) ImportDocument(context.Context, string, []byte, ImportOptions, string) (ImportReport, error) {
	provider.called()
	return ImportReport{}, nil
}
func (provider *countingAllProvider) StageUpdate(context.Context, string, Definition, uint64, time.Time, string) (StagedChange, error) {
	provider.called()
	return StagedChange{}, nil
}
func (provider *countingAllProvider) ApplyStage(context.Context, string, uint64, string) (Definition, error) {
	provider.called()
	return Definition{}, nil
}
func (provider *countingAllProvider) ApplyScheduled(context.Context, string, time.Time, string) ([]Definition, error) {
	provider.called()
	return nil, nil
}
func (provider *countingAllProvider) StagedChanges(context.Context, string) ([]StagedChange, error) {
	provider.called()
	return nil, nil
}
func (provider *countingAllProvider) Cleanup(context.Context, string, CleanupOptions) (CleanupReport, error) {
	provider.called()
	return CleanupReport{}, nil
}
func (*countingAllProvider) Health(context.Context) ProviderHealth {
	return ProviderHealth{Healthy: true}
}
func (*countingAllProvider) Close(context.Context) error { return nil }

func (provider *countingSnapshotProvider) Snapshot(ctx context.Context, tenant string) (Snapshot, error) {
	provider.snapshots++
	return provider.Provider.Snapshot(ctx, tenant)
}

func TestProvidersRejectOversizedTenantBeforeRetentionOrStorage(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxKeyBytes = 4
	tenant := "tenant-too-long"
	definition := Definition{
		Key:       "flag",
		Type:      TypeBoolean,
		Default:   BooleanValue(false),
		Lifecycle: LifecycleActive,
	}

	memory := NewMemoryProvider(limits)
	if _, err := memory.Create(t.Context(), tenant, definition, "actor"); !errors.Is(err, ErrContextLimit) {
		t.Fatalf("MemoryProvider.Create() error = %v, want ErrContextLimit", err)
	}
	if len(memory.tenants) != 0 {
		t.Fatalf("MemoryProvider retained %d oversized tenant(s)", len(memory.tenants))
	}
	exactLimit := NewMemoryProvider(limits)
	if _, err := exactLimit.Create(t.Context(), "four", definition, "actor"); err != nil {
		t.Fatalf("MemoryProvider.Create(exact-limit tenant) error = %v", err)
	}

	backend := &countingDocumentBackend{}
	durable := NewDurableProvider(backend, limits)
	if _, err := durable.Snapshot(t.Context(), tenant); !errors.Is(err, ErrContextLimit) {
		t.Fatalf("DurableProvider.Snapshot() error = %v, want ErrContextLimit", err)
	}
	if backend.loads != 0 {
		t.Fatalf("DurableProvider performed %d load(s) for oversized tenant", backend.loads)
	}

	underlying := &countingSnapshotProvider{Provider: NewMemoryProvider(DefaultLimits())}
	cached, err := NewCachedProvider(underlying, CacheConfig{
		Clock:              &manualCacheClock{now: time.Now()},
		MaxStaleness:       time.Minute,
		MaxOutageStaleness: time.Minute,
		FailurePolicy:      FailClosed,
		MaxTenants:         1,
	})
	if err != nil {
		t.Fatalf("NewCachedProvider() error = %v", err)
	}
	if _, err := cached.Refresh(t.Context(), strings.Repeat("t", DefaultLimits().MaxKeyBytes+1)); !errors.Is(err, ErrContextLimit) {
		t.Fatalf("CachedProvider.Refresh() error = %v, want ErrContextLimit", err)
	}
	if underlying.snapshots != 0 {
		t.Fatalf("CachedProvider performed %d snapshot(s) for oversized tenant", underlying.snapshots)
	}
}

func TestCachedProviderRejectsOversizedTenantBeforeEveryDelegation(t *testing.T) {
	t.Parallel()

	native := &countingAllProvider{}
	cached, err := NewCachedProvider(native, CacheConfig{
		Clock:                &manualCacheClock{now: time.Now()},
		MaxStaleness:         time.Minute,
		MaxOutageStaleness:   time.Minute,
		FailurePolicy:        FailClosed,
		MaxTenants:           1,
		MaxFeaturesPerTenant: 1,
	})
	if err != nil {
		t.Fatalf("NewCachedProvider() error = %v", err)
	}
	tenant := strings.Repeat("t", DefaultLimits().MaxKeyBytes+1)
	operations := map[string]func() error{
		"snapshot": func() error { _, err := cached.Snapshot(t.Context(), tenant); return err },
		"refresh":  func() error { _, err := cached.Refresh(t.Context(), tenant); return err },
		"create": func() error {
			_, err := cached.Create(t.Context(), tenant, Definition{}, "actor")
			return err
		},
		"update": func() error {
			_, err := cached.Update(t.Context(), tenant, Definition{}, 1, "actor")
			return err
		},
		"activate": func() error { _, err := cached.Activate(t.Context(), tenant, "key", 1, "actor"); return err },
		"deactivate": func() error {
			_, err := cached.Deactivate(t.Context(), tenant, "key", 1, "actor")
			return err
		},
		"delete":  func() error { _, err := cached.Delete(t.Context(), tenant, "key", 1, "actor"); return err },
		"restore": func() error { _, err := cached.Restore(t.Context(), tenant, "key", 1, "actor"); return err },
		"create group": func() error {
			_, err := cached.CreateGroup(t.Context(), tenant, GroupDefinition{}, "actor")
			return err
		},
		"update group": func() error {
			_, err := cached.UpdateGroup(t.Context(), tenant, GroupDefinition{}, 1, "actor")
			return err
		},
		"delete group": func() error {
			_, err := cached.DeleteGroup(t.Context(), tenant, "group", 1, "actor")
			return err
		},
		"assign group": func() error {
			_, err := cached.AssignGroup(t.Context(), tenant, "feature", "group", 1, "actor")
			return err
		},
		"remove group": func() error {
			_, err := cached.RemoveGroup(t.Context(), tenant, "feature", "group", 1, "actor")
			return err
		},
		"audit":  func() error { _, err := cached.Audit(t.Context(), tenant, "key"); return err },
		"export": func() error { _, err := cached.ExportDocument(t.Context(), tenant); return err },
		"import": func() error {
			_, err := cached.ImportDocument(t.Context(), tenant, nil, ImportOptions{}, "actor")
			return err
		},
		"stage": func() error {
			_, err := cached.StageUpdate(t.Context(), tenant, Definition{}, 1, time.Time{}, "actor")
			return err
		},
		"apply stage": func() error { _, err := cached.ApplyStage(t.Context(), tenant, 1, "actor"); return err },
		"apply scheduled": func() error {
			_, err := cached.ApplyScheduled(t.Context(), tenant, time.Now(), "actor")
			return err
		},
		"staged changes": func() error { _, err := cached.StagedChanges(t.Context(), tenant); return err },
		"cleanup": func() error {
			_, err := cached.Cleanup(t.Context(), tenant, CleanupOptions{})
			return err
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			native.calls = 0
			if err := operation(); !errors.Is(err, ErrContextLimit) {
				t.Fatalf("operation error = %v, want ErrContextLimit", err)
			}
			if native.calls != 0 {
				t.Fatalf("operation delegated %d time(s) for oversized tenant", native.calls)
			}
		})
	}
}
