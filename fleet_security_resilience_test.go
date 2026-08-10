package featureflags

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func securityFleetConfig(clock CacheClock, loader SnapshotLoader) FleetConfig {
	config := validFleetConfig(clock, loader)
	config.Policies = map[string]FlagPolicy{
		"secure": {Mode: DegradedLastKnownGood, MaxStaleness: 5 * time.Minute, SecuritySensitive: true},
	}
	return config
}

func assertSecurityLastKnownGoodWindow(t *testing.T, fleet *Fleet, clock *fleetTestClock, now time.Time) {
	t.Helper()
	detail, err := fleet.Boolean("secure", Context{Tenant: "tenant-a"})
	if err != nil || detail.Value || detail.Reason != ReasonDefault {
		t.Fatalf("fresh security snapshot = %#v, %v", detail, err)
	}
	clock.Set(now.Add(3 * time.Minute))
	detail, err = fleet.Boolean("secure", Context{Tenant: "tenant-a"})
	if err != nil || detail.Value || detail.Reason != ReasonDegradedLastKnownGood {
		t.Fatalf("bounded security last-known-good = %#v, %v", detail, err)
	}
	clock.Set(now.Add(5 * time.Minute))
	detail, err = fleet.Boolean("secure", Context{Tenant: "tenant-a"})
	if err != nil || detail.Value || detail.Reason != ReasonDegradedLastKnownGood {
		t.Fatalf("maximum security last-known-good = %#v, %v", detail, err)
	}
	clock.Set(now.Add(5*time.Minute + time.Nanosecond))
	if _, err := fleet.Boolean("secure", Context{Tenant: "tenant-a"}); !errors.Is(err, ErrSnapshotStale) {
		t.Fatalf("expired security last-known-good = %v", err)
	}
}

func TestFleetSecuritySensitiveLastKnownGoodAcrossResilienceFailures(t *testing.T) {
	now := time.Date(2026, 8, 9, 13, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		code FleetFailureCode
	}{
		{name: "provider", code: FleetFailureProvider},
		{name: "retry exhausted", code: FleetFailureRetryExhausted},
		{name: "circuit open", code: FleetFailureCircuitOpen},
		{name: "bulkhead rejected", code: FleetFailureBulkhead},
		{name: "throttled", code: FleetFailureThrottled},
		{name: "concurrency rejected", code: FleetFailureConcurrency},
		{name: "budget exhausted", code: FleetFailureBudgetExhausted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := &fleetTestClock{now: now}
			providerErr := errors.New("provider detail must not affect policy")
			loader := &fleetTestLoader{
				candidates: []SnapshotCandidate{{
					Snapshot: fleetBooleanSnapshot(t, "tenant-a", "secure", false),
					Revision: "41", Provenance: "postgres", SourceTime: now,
				}},
				errors: []error{nil, providerErr},
			}
			config := securityFleetConfig(clock, loader)
			config.FailureClassifier = FleetFailureClassifyFunc(func(error) FleetFailureCode { return test.code })
			fleet, err := NewFleet(config)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fleet.Bootstrap(context.Background()); err != nil {
				t.Fatal(err)
			}
			clock.Set(now.Add(2 * time.Second))
			if _, err := fleet.Refresh(context.Background()); !errors.Is(err, providerErr) {
				t.Fatalf("refresh error = %v", err)
			}
			status := fleet.Status()
			if status.LastRefreshFailure != test.code || status.State != FleetReady {
				t.Fatalf("failure status = %#v", status)
			}
			assertSecurityLastKnownGoodWindow(t, fleet, clock, now)
		})
	}
}

func TestFleetSecuritySensitiveLastKnownGoodAcrossSnapshotFailures(t *testing.T) {
	now := time.Date(2026, 8, 9, 14, 0, 0, 0, time.UTC)
	valid := fleetBooleanSnapshot(t, "tenant-a", "secure", false)
	tests := []struct {
		name      string
		candidate SnapshotCandidate
		refreshAt time.Time
		wantErr   error
		wantCode  FleetFailureCode
	}{
		{
			name: "malformed", candidate: SnapshotCandidate{Snapshot: Snapshot{}, Revision: "42", Provenance: "postgres", SourceTime: now.Add(2 * time.Second)},
			refreshAt: now.Add(2 * time.Second),
			wantErr:   ErrMalformedSnapshot, wantCode: FleetFailureInvalidSnapshot,
		},
		{
			name: "stale", candidate: SnapshotCandidate{Snapshot: valid, Revision: "42", Provenance: "postgres", SourceTime: now.Add(-3 * time.Minute)},
			refreshAt: now.Add(2 * time.Second),
			wantErr:   ErrSnapshotStale, wantCode: FleetFailureStaleSnapshot,
		},
		{
			name: "future", candidate: SnapshotCandidate{Snapshot: valid, Revision: "42", Provenance: "postgres", SourceTime: now.Add(13 * time.Second)},
			refreshAt: now.Add(2 * time.Second),
			wantErr:   ErrSnapshotFuture, wantCode: FleetFailureInvalidSnapshot,
		},
		{
			name: "reordered", candidate: SnapshotCandidate{Snapshot: valid, Revision: "42", Provenance: "postgres", SourceTime: now.Add(-time.Second)},
			refreshAt: now.Add(2 * time.Second),
			wantErr:   ErrSnapshotReordered, wantCode: FleetFailureInvalidSnapshot,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := &fleetTestClock{now: now}
			loader := &fleetTestLoader{candidates: []SnapshotCandidate{
				{Snapshot: valid, Revision: "41", Provenance: "postgres", SourceTime: now},
				test.candidate,
			}}
			fleet, err := NewFleet(securityFleetConfig(clock, loader))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fleet.Bootstrap(context.Background()); err != nil {
				t.Fatal(err)
			}
			clock.Set(test.refreshAt)
			if _, err := fleet.Refresh(context.Background()); !errors.Is(err, test.wantErr) {
				t.Fatalf("refresh error = %v", err)
			}
			if status := fleet.Status(); status.LastRefreshFailure != test.wantCode || status.Revision != "41" {
				t.Fatalf("snapshot failure status = %#v", status)
			}
			assertSecurityLastKnownGoodWindow(t, fleet, clock, now)
		})
	}
}

func TestFleetSecuritySensitiveLastKnownGoodAcrossLifecycleAndResourceFailures(t *testing.T) {
	now := time.Date(2026, 8, 9, 15, 0, 0, 0, time.UTC)
	valid := fleetBooleanSnapshot(t, "tenant-a", "secure", false)

	t.Run("cancelled refresh", func(t *testing.T) {
		clock := &fleetTestClock{now: now}
		loader := &fleetTestLoader{candidates: []SnapshotCandidate{{Snapshot: valid, Revision: "41", Provenance: "postgres", SourceTime: now}}}
		fleet, err := NewFleet(securityFleetConfig(clock, loader))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Bootstrap(context.Background()); err != nil {
			t.Fatal(err)
		}
		clock.Set(now.Add(2 * time.Second))
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := fleet.Refresh(cancelled); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled refresh = %v", err)
		}
		if fleet.Status().LastRefreshFailure != FleetFailureCancelled {
			t.Fatalf("cancelled status = %#v", fleet.Status())
		}
		assertSecurityLastKnownGoodWindow(t, fleet, clock, now)
	})

	t.Run("provider load limit", func(t *testing.T) {
		clock := &fleetTestClock{now: now}
		providerErr := errors.New("provider unavailable")
		loader := &fleetTestLoader{
			candidates: []SnapshotCandidate{{Snapshot: valid, Revision: "41", Provenance: "postgres", SourceTime: now}},
			errors:     []error{nil, providerErr},
		}
		var executions atomic.Uint64
		config := securityFleetConfig(clock, loader)
		config.MaxProviderLoads = 1
		config.Executor = RefreshExecuteFunc(func(ctx context.Context, operation RefreshOperation) (SnapshotCandidate, error) {
			if executions.Add(1) == 1 {
				return operation(ctx)
			}
			_, _ = operation(ctx)
			return operation(ctx)
		})
		fleet, err := NewFleet(config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Bootstrap(context.Background()); err != nil {
			t.Fatal(err)
		}
		clock.Set(now.Add(2 * time.Second))
		if _, err := fleet.Refresh(context.Background()); !errors.Is(err, ErrRefreshLoadLimit) {
			t.Fatalf("load limit refresh = %v", err)
		}
		if fleet.Status().LastRefreshFailure != FleetFailureLoadLimit {
			t.Fatalf("load limit status = %#v", fleet.Status())
		}
		assertSecurityLastKnownGoodWindow(t, fleet, clock, now)
	})

	t.Run("refresh waiter limit", func(t *testing.T) {
		clock := &fleetTestClock{now: now}
		entered := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Uint64
		loader := SnapshotLoadFunc(func(ctx context.Context, _ string) (SnapshotCandidate, error) {
			if calls.Add(1) == 1 {
				return SnapshotCandidate{Snapshot: valid, Revision: "41", Provenance: "postgres", SourceTime: now}, nil
			}
			close(entered)
			select {
			case <-release:
				return SnapshotCandidate{Snapshot: valid, Revision: "42", Provenance: "postgres", SourceTime: now.Add(5*time.Minute + time.Nanosecond)}, nil
			case <-ctx.Done():
				return SnapshotCandidate{}, ctx.Err()
			}
		})
		config := securityFleetConfig(clock, loader)
		config.MaxWaiters = 1
		fleet, err := NewFleet(config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Bootstrap(context.Background()); err != nil {
			t.Fatal(err)
		}
		clock.Set(now.Add(2 * time.Second))
		leaderDone := make(chan error, 1)
		go func() {
			_, refreshErr := fleet.Refresh(context.Background())
			leaderDone <- refreshErr
		}()
		waitForFleetEvent(t, entered, "security waiter provider load")
		waiterDone := make(chan error, 1)
		go func() {
			_, refreshErr := fleet.Refresh(context.Background())
			waiterDone <- refreshErr
		}()
		deadline := time.Now().Add(time.Second)
		for fleet.Status().RefreshWaiters != 1 {
			if time.Now().After(deadline) {
				t.Fatalf("waiter status = %#v", fleet.Status())
			}
			runtime.Gosched()
		}
		if _, err := fleet.Refresh(context.Background()); !errors.Is(err, ErrRefreshWaiterLimit) {
			t.Fatalf("waiter limit refresh = %v", err)
		}
		assertSecurityLastKnownGoodWindow(t, fleet, clock, now)
		close(release)
		if err := waitForFleetEvent(t, leaderDone, "security leader refresh"); err != nil {
			t.Fatal(err)
		}
		if err := waitForFleetEvent(t, waiterDone, "security waiter refresh"); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("cache load failure", func(t *testing.T) {
		clock := &fleetTestClock{now: now}
		providerErr := errors.New("provider unavailable")
		cacheErr := errors.New("cache unavailable")
		loader := &fleetTestLoader{
			candidates: []SnapshotCandidate{{Snapshot: valid, Revision: "41", Provenance: "postgres", SourceTime: now}},
			errors:     []error{nil, providerErr},
		}
		config := securityFleetConfig(clock, loader)
		config.Cache = &fleetTestCache{loadErr: cacheErr}
		fleet, err := NewFleet(config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Bootstrap(context.Background()); err != nil {
			t.Fatal(err)
		}
		clock.Set(now.Add(2 * time.Second))
		if _, err := fleet.Bootstrap(context.Background()); !errors.Is(err, providerErr) || !errors.Is(err, cacheErr) {
			t.Fatalf("cache load bootstrap = %v", err)
		}
		if fleet.Status().LastCacheFailure != FleetFailureCacheLoad {
			t.Fatalf("cache load status = %#v", fleet.Status())
		}
		assertSecurityLastKnownGoodWindow(t, fleet, clock, now)
	})

	t.Run("cache store failure", func(t *testing.T) {
		clock := &fleetTestClock{now: now}
		cacheErr := errors.New("cache unavailable")
		loader := &fleetTestLoader{candidates: []SnapshotCandidate{{Snapshot: valid, Revision: "41", Provenance: "postgres", SourceTime: now}}}
		config := securityFleetConfig(clock, loader)
		config.Cache = &fleetTestCache{storeErr: cacheErr}
		fleet, err := NewFleet(config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Bootstrap(context.Background()); err != nil {
			t.Fatal(err)
		}
		if fleet.Status().LastCacheFailure != FleetFailureCacheStore {
			t.Fatalf("cache store status = %#v", fleet.Status())
		}
		assertSecurityLastKnownGoodWindow(t, fleet, clock, now)
	})

	t.Run("scheduler failure", func(t *testing.T) {
		clock := &fleetTestClock{now: now}
		loader := &fleetTestLoader{candidates: []SnapshotCandidate{{Snapshot: valid, Revision: "41", Provenance: "postgres", SourceTime: now}}}
		config := securityFleetConfig(clock, loader)
		config.Sleeper = &errorFleetSleeper{err: errors.New("scheduler failed")}
		fleet, err := NewFleet(config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(time.Second)
		for fleet.Status().LastRefreshFailure != FleetFailureScheduler {
			if time.Now().After(deadline) {
				t.Fatalf("scheduler status = %#v", fleet.Status())
			}
			runtime.Gosched()
		}
		assertSecurityLastKnownGoodWindow(t, fleet, clock, now)
		if err := fleet.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("stopped fleet", func(t *testing.T) {
		clock := &fleetTestClock{now: now}
		loader := &fleetTestLoader{candidates: []SnapshotCandidate{{Snapshot: valid, Revision: "41", Provenance: "postgres", SourceTime: now}}}
		fleet, err := NewFleet(securityFleetConfig(clock, loader))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Bootstrap(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := fleet.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Refresh(context.Background()); !errors.Is(err, ErrFleetStopped) {
			t.Fatalf("stopped refresh = %v", err)
		}
		assertSecurityLastKnownGoodWindow(t, fleet, clock, now)
	})
}
