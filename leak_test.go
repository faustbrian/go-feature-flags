package featureflags

import (
	"context"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestNoGoroutineLeaks(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	provider := NewMemoryProvider(DefaultLimits())
	clock := &manualCacheClock{now: time.Now()}
	cached, err := NewCachedProvider(provider, CacheConfig{
		Clock: clock, MaxStaleness: time.Minute, MaxOutageStaleness: time.Minute,
		FailurePolicy: FailClosed, MaxTenants: 1,
	})
	if err != nil {
		t.Fatalf("NewCachedProvider() error = %v", err)
	}
	if err := cached.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	now := time.Now().Round(0)
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", false),
		Revision: "leak-test", Provenance: "memory", SourceTime: now,
	}}}
	sleeper := &fleetTestSleeper{delays: make(chan time.Duration, 1), release: make(chan struct{})}
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.Sleeper = sleeper
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForFleetEvent(t, sleeper.delays, "leak-check refresh schedule")
	if err := fleet.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
