package featureflags

import (
	"context"
	"testing"
	"time"
)

func TestFleetWithoutWatcherStartsAndShutsDownWithinBound(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "42", Provenance: "provider", SourceTime: now,
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
	waitForFleetEvent(t, sleeper.delays, "watcher-free refresh schedule")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := fleet.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown without watcher: %v", err)
	}
	if status := fleet.Status(); status.WatcherRunning {
		t.Fatalf("watcher reported running without source: %#v", status)
	}
}
