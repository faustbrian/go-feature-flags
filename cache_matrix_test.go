package featureflags

import (
	"errors"
	"testing"
	"time"
)

func TestCacheConfigurationAndEvictionBoundaries(t *testing.T) {
	t.Parallel()

	clock := &manualCacheClock{now: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)}
	native := NewMemoryProvider(DefaultLimits())
	valid := CacheConfig{
		Clock: clock, MaxStaleness: time.Minute, MaxOutageStaleness: 2 * time.Minute,
		FailurePolicy: FailClosed, MaxTenants: 1,
	}
	invalid := []struct {
		provider Provider
		config   CacheConfig
	}{
		{nil, valid},
		{native, CacheConfig{}},
		{native, CacheConfig{Clock: clock, MaxStaleness: 0, MaxOutageStaleness: time.Minute, FailurePolicy: FailClosed, MaxTenants: 1}},
		{native, CacheConfig{Clock: clock, MaxStaleness: 2 * time.Minute, MaxOutageStaleness: time.Minute, FailurePolicy: FailClosed, MaxTenants: 1}},
		{native, CacheConfig{Clock: clock, MaxStaleness: time.Minute, MaxOutageStaleness: 2 * time.Minute, FailurePolicy: 100, MaxTenants: 1}},
		{native, CacheConfig{Clock: clock, MaxStaleness: time.Minute, MaxOutageStaleness: 2 * time.Minute, FailurePolicy: FailClosed}},
	}
	for index, test := range invalid {
		if _, err := NewCachedProvider(test.provider, test.config); err == nil {
			t.Fatalf("NewCachedProvider(invalid %d) succeeded", index)
		}
	}
	cached, err := NewCachedProvider(native, valid)
	if err != nil {
		t.Fatalf("NewCachedProvider() error = %v", err)
	}
	for _, tenant := range []string{"tenant-b", "tenant-a"} {
		if _, err := native.Create(t.Context(), tenant, Definition{
			Key: "flag", Type: TypeBoolean, Default: BooleanValue(true),
			Lifecycle: LifecycleActive,
		}, "alice"); err != nil {
			t.Fatalf("Create(%s) error = %v", tenant, err)
		}
		if _, err := cached.Refresh(t.Context(), tenant); err != nil {
			t.Fatalf("Refresh(%s) error = %v", tenant, err)
		}
	}
	if len(cached.entries) != 1 || cached.entries["tenant-a"].fetched.IsZero() {
		t.Fatalf("cache entries after deterministic eviction = %#v", cached.entries)
	}
	if age := boundedAge(clock.now.Add(-time.Minute), clock.now); age != 0 {
		t.Fatalf("boundedAge(clock rollback) = %s", age)
	}
}

func TestCachedProviderUpdateAndAppliedImportInvalidateSnapshots(t *testing.T) {
	t.Parallel()

	native := NewMemoryProvider(DefaultLimits())
	clock := &manualCacheClock{now: time.Now()}
	cached, err := NewCachedProvider(native, CacheConfig{
		Clock: clock, MaxStaleness: time.Hour, MaxOutageStaleness: time.Hour,
		FailurePolicy: FailClosed, MaxTenants: 2, MaxFeaturesPerTenant: 1,
	})
	if err != nil {
		t.Fatalf("NewCachedProvider() error = %v", err)
	}
	created, err := cached.Create(t.Context(), "tenant", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
		Lifecycle: LifecycleActive,
	}, "alice")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := cached.Snapshot(t.Context(), "tenant"); err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if _, err := cached.Snapshot(t.Context(), "tenant"); err != nil {
		t.Fatalf("Snapshot(cached) error = %v", err)
	}
	if _, err := cached.Snapshot(t.Context(), ""); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("Snapshot(empty tenant) error = %v", err)
	}
	updated := cloneDefinition(created)
	updated.Default = BooleanValue(true)
	if _, err := cached.Update(t.Context(), "tenant", updated, created.Version, "alice"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, exists := cached.entries["tenant"]; exists {
		t.Fatal("Update() did not invalidate the cached snapshot")
	}
	document, err := Export([]Definition{{
		Key: "imported", Type: TypeBoolean, Default: BooleanValue(true),
		Lifecycle: LifecycleActive,
	}}, nil, DefaultLimits())
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if _, err := cached.ImportDocument(
		t.Context(), "tenant-two", document, ImportOptions{ConflictPolicy: ConflictFail}, "alice",
	); err != nil {
		t.Fatalf("ImportDocument() error = %v", err)
	}
	if _, err := cached.Refresh(t.Context(), "tenant-two"); err != nil {
		t.Fatalf("Refresh(imported) error = %v", err)
	}
	if _, err := native.Create(t.Context(), "tenant-two", Definition{
		Key: "overflow", Type: TypeBoolean, Default: BooleanValue(true),
		Lifecycle: LifecycleActive,
	}, "alice"); err != nil {
		t.Fatalf("Create(overflow) error = %v", err)
	}
	if _, err := cached.Refresh(t.Context(), "tenant-two"); err == nil {
		t.Fatal("Refresh() accepted too many features")
	}
}

func TestCachedProviderFailClosedAndMutationErrorsPreserveState(t *testing.T) {
	t.Parallel()

	native := &failingSnapshotProvider{Provider: NewMemoryProvider(DefaultLimits()), fail: true}
	clock := &manualCacheClock{now: time.Now()}
	cached, err := NewCachedProvider(native, CacheConfig{
		Clock: clock, MaxStaleness: time.Minute, MaxOutageStaleness: time.Minute,
		FailurePolicy: FailClosed, MaxTenants: 1,
	})
	if err != nil {
		t.Fatalf("NewCachedProvider() error = %v", err)
	}
	if _, err := cached.Snapshot(t.Context(), "tenant"); !errors.Is(err, errProviderUnavailable) {
		t.Fatalf("Snapshot() error = %v", err)
	}
	cached.entries["tenant"] = cachedSnapshot{fetched: clock.now}
	if _, err := cached.Update(t.Context(), "tenant", Definition{
		Key: "missing", Type: TypeBoolean, Default: BooleanValue(false),
	}, 1, "alice"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update(missing) error = %v", err)
	}
	if _, exists := cached.entries["tenant"]; !exists {
		t.Fatal("failed mutation invalidated a cached snapshot")
	}
}
