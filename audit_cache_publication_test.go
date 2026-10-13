package featureflags

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheLaterFailedRefreshKeepsEarlierSuccessfulPublication(t *testing.T) {
	cached, underlying, _ := auditPublicationCache(t)
	underlying.failAt = 2
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := cached.Refresh(ctx, "tenant-a"); done <- err }()
	auditWaitSignal(t, ctx, underlying.entered)
	if _, err := cached.Refresh(ctx, "tenant-a"); !errors.Is(err, errProviderUnavailable) {
		t.Fatalf("later failed refresh = %v; want unavailable", err)
	}
	underlying.unblock()
	auditWaitRefresh(t, ctx, done)
	auditAssertOriginalCacheHit(t, ctx, cached, underlying, 2)
}

func TestCacheFailedMutationDoesNotInvalidateSuccessfulRefresh(t *testing.T) {
	cached, underlying, definition := auditPublicationCache(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := cached.Refresh(ctx, "tenant-a"); done <- err }()
	auditWaitSignal(t, ctx, underlying.entered)
	definition.Default = BooleanValue(true)
	if _, err := cached.Update(ctx, "tenant-a", definition, definition.Version+1, "test"); !errors.Is(err, ErrConflict) {
		t.Fatalf("failed mutation = %v; want conflict", err)
	}
	underlying.unblock()
	auditWaitRefresh(t, ctx, done)
	auditAssertOriginalCacheHit(t, ctx, cached, underlying, 1)
}

func TestCacheOtherTenantMutationDoesNotInvalidateSuccessfulRefresh(t *testing.T) {
	cached, underlying, _ := auditPublicationCache(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := cached.Refresh(ctx, "tenant-a"); done <- err }()
	auditWaitSignal(t, ctx, underlying.entered)
	if _, err := cached.Create(ctx, "tenant-b", Definition{Key: "other", Type: TypeBoolean, Default: BooleanValue(true)}, "test"); err != nil {
		t.Fatal(err)
	}
	underlying.unblock()
	auditWaitRefresh(t, ctx, done)
	auditAssertOriginalCacheHit(t, ctx, cached, underlying, 1)
}

func auditAssertOriginalCacheHit(t *testing.T, ctx context.Context, cached *CachedProvider, underlying *auditHeldSnapshotProvider, reads int32) {
	t.Helper()
	snapshot, err := cached.Snapshot(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := snapshot.Boolean("flag", Context{Tenant: "tenant-a"})
	if err != nil || detail.Value || detail.Version != 1 {
		t.Errorf("original successful cache = %#v, %v; want false at version 1", detail, err)
	}
	if underlying.reads.Load() != reads {
		t.Error("unrelated failed work discarded a successful cache publication")
	}
}

func TestCacheRefreshCannotSurviveSuccessfulMutationInvalidation(t *testing.T) {
	for _, primeReplacement := range []bool{false, true} {
		name := "invalidation without replacement"
		if primeReplacement {
			name = "invalidation with replacement"
		}
		t.Run(name, func(t *testing.T) {
			cached, underlying, definition := auditPublicationCache(t)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := cached.Refresh(ctx, "tenant-a"); done <- err }()
			auditWaitSignal(t, ctx, underlying.entered)
			definition.Default = BooleanValue(true)
			if _, err := cached.Update(ctx, "tenant-a", definition, definition.Version, "test"); err != nil {
				t.Fatal(err)
			}
			if primeReplacement {
				auditAssertLatestCache(t, ctx, cached)
			}
			underlying.unblock()
			auditWaitRefresh(t, ctx, done)
			auditAssertLatestCache(t, ctx, cached)
		})
	}
}

func TestCacheEarlierRefreshCannotOverwriteLaterSuccessfulRefresh(t *testing.T) {
	cached, underlying, definition := auditPublicationCache(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := cached.Refresh(ctx, "tenant-a"); done <- err }()
	auditWaitSignal(t, ctx, underlying.entered)
	definition.Default = BooleanValue(true)
	if _, err := underlying.Update(ctx, "tenant-a", definition, definition.Version, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.Refresh(ctx, "tenant-a"); err != nil {
		t.Fatal(err)
	}
	reads := underlying.reads.Load()
	underlying.unblock()
	auditWaitRefresh(t, ctx, done)
	auditAssertLatestCache(t, ctx, cached)
	if underlying.reads.Load() != reads {
		t.Error("fresh later-generation cache entry required another provider read")
	}
}

func auditPublicationCache(t *testing.T) (*CachedProvider, *auditHeldSnapshotProvider, Definition) {
	t.Helper()
	memory := NewMemoryProvider(DefaultLimits())
	definition, err := memory.Create(t.Context(), "tenant-a", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	underlying := &auditHeldSnapshotProvider{Provider: memory, entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(underlying.unblock)
	cached, err := NewCachedProvider(underlying, CacheConfig{
		Clock:        &manualCacheClock{now: time.Unix(1_700_000_000, 0).UTC()},
		MaxStaleness: time.Minute, MaxOutageStaleness: time.Minute,
		FailurePolicy: FailClosed, MaxTenants: 2, MaxTenantBytes: DefaultLimits().MaxKeyBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cached, underlying, definition
}

func auditAssertLatestCache(t *testing.T, ctx context.Context, cached *CachedProvider) {
	t.Helper()
	snapshot, err := cached.Snapshot(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := snapshot.Boolean("flag", Context{Tenant: "tenant-a"})
	if err != nil || !detail.Value || detail.Version != 2 {
		t.Errorf("post-mutation cache = %#v, %v; want true at version 2", detail, err)
	}
}

type auditHeldSnapshotProvider struct {
	Provider
	reads   atomic.Int32
	failAt  int32
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (provider *auditHeldSnapshotProvider) Snapshot(ctx context.Context, tenant string) (Snapshot, error) {
	snapshot, err := provider.Provider.Snapshot(ctx, tenant)
	if err != nil {
		return Snapshot{}, err
	}
	read := provider.reads.Add(1)
	if read == provider.failAt {
		return Snapshot{}, errProviderUnavailable
	}
	if read == 1 {
		close(provider.entered)
		select {
		case <-provider.release:
		case <-ctx.Done():
			return Snapshot{}, ctx.Err()
		}
	}
	return snapshot, nil
}

func (provider *auditHeldSnapshotProvider) unblock() {
	provider.once.Do(func() { close(provider.release) })
}

func auditWaitSignal(t *testing.T, ctx context.Context, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatal("snapshot gate did not arrive before test deadline")
	}
}

func auditWaitRefresh(t *testing.T, ctx context.Context, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("refresh did not finish before test deadline")
	}
}
