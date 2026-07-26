package featureflags

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errProviderUnavailable = errors.New("provider unavailable")

type failingSnapshotProvider struct {
	Provider
	fail bool
}

func (provider *failingSnapshotProvider) Snapshot(ctx context.Context, tenant string) (Snapshot, error) {
	if provider.fail {
		return Snapshot{}, errProviderUnavailable
	}
	return provider.Provider.Snapshot(ctx, tenant)
}

type manualCacheClock struct{ now time.Time }

func (clock *manualCacheClock) Now() time.Time { return clock.now }

func TestCachedProviderFailOpenIsBoundedByOutageStaleness(t *testing.T) {
	t.Parallel()

	memory := NewMemoryProvider(DefaultLimits())
	if _, err := memory.Create(context.Background(), "tenant-a", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive,
	}, "alice"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	underlying := &failingSnapshotProvider{Provider: memory}
	clock := &manualCacheClock{now: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)}
	cached, err := NewCachedProvider(underlying, CacheConfig{
		Clock: clock, MaxStaleness: time.Minute, MaxOutageStaleness: 5 * time.Minute,
		FailurePolicy: FailOpen, MaxTenants: 10,
	})
	if err != nil {
		t.Fatalf("NewCachedProvider() error = %v", err)
	}
	if _, err := cached.Snapshot(context.Background(), "tenant-a"); err != nil {
		t.Fatalf("Snapshot(initial) error = %v", err)
	}

	underlying.fail = true
	clock.now = clock.now.Add(2 * time.Minute)
	if _, err := cached.Snapshot(context.Background(), "tenant-a"); err != nil {
		t.Fatalf("Snapshot(fail-open) error = %v", err)
	}
	clock.now = clock.now.Add(4 * time.Minute)
	if _, err := cached.Snapshot(context.Background(), "tenant-a"); !errors.Is(err, errProviderUnavailable) {
		t.Fatalf("Snapshot(expired fallback) error = %v, want provider error", err)
	}
}
