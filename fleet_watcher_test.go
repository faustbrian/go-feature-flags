package featureflags

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestInvalidationWatchFuncAdaptsTenantAndEvent(t *testing.T) {
	want := Invalidation{Tenant: "tenant-a", Stream: "valkey", Sequence: 1, Revision: "42", ObservedAt: time.Now()}
	watcher := InvalidationWatchFunc(func(_ context.Context, tenant string) (Invalidation, error) {
		if tenant != "tenant-a" {
			t.Fatalf("tenant = %q", tenant)
		}
		return want, nil
	})
	got, err := watcher.Next(context.Background(), "tenant-a")
	if err != nil || got != want {
		t.Fatalf("watch function = %#v, %v", got, err)
	}
}

type fleetChannelWatcher struct {
	events  chan Invalidation
	errors  chan error
	stopped chan struct{}
	once    sync.Once
	calls   atomic.Uint64
}

func (watcher *fleetChannelWatcher) Next(ctx context.Context, tenant string) (Invalidation, error) {
	watcher.calls.Add(1)
	select {
	case event := <-watcher.events:
		if event.Tenant != tenant {
			return Invalidation{}, errors.New("watcher received the wrong tenant")
		}
		return event, nil
	case err := <-watcher.errors:
		return Invalidation{}, err
	case <-ctx.Done():
		watcher.once.Do(func() { close(watcher.stopped) })
		return Invalidation{}, ctx.Err()
	}
}

func TestFleetWatcherDeliversCausalInvalidationsAndJoinsShutdown(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "secure", false), Revision: "41", Provenance: "postgres", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "secure", true), Revision: "42", Provenance: "postgres", SourceTime: now.Add(time.Minute)},
	}}
	watcher := &fleetChannelWatcher{
		events: make(chan Invalidation, 3), errors: make(chan error, 1), stopped: make(chan struct{}),
	}
	sleeper := &fleetTestSleeper{delays: make(chan time.Duration, 1), release: make(chan struct{})}
	config := validFleetConfig(clock, loader)
	config.Watcher = watcher
	config.Sleeper = sleeper
	config.Policies = map[string]FlagPolicy{
		"secure": {Mode: DegradedLastKnownGood, MaxStaleness: 5 * time.Minute, SecuritySensitive: true},
	}
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	startCtx, cancelStart := context.WithCancel(context.Background())
	if _, err := fleet.Start(startCtx); err != nil {
		t.Fatal(err)
	}
	waitForFleetEvent(t, sleeper.delays, "watcher refresh schedule")
	clock.Set(now.Add(time.Minute))
	watcher.events <- Invalidation{
		Tenant: "tenant-a", Stream: "valkey", Sequence: 2,
		Revision: "42", ObservedAt: now.Add(time.Minute),
	}
	watcher.events <- Invalidation{
		Tenant: "tenant-a", Stream: "valkey", Sequence: 1,
		Revision: "41", ObservedAt: now,
	}
	watcher.events <- Invalidation{
		Tenant: "tenant-a", Stream: "valkey", Sequence: 2,
		Revision: "42", ObservedAt: now.Add(time.Minute),
	}

	deadline := time.Now().Add(time.Second)
	for {
		active, ok := fleet.Current()
		status := fleet.Status()
		if ok && active.Revision == "42" && status.InvalidationGaps == 1 && watcher.calls.Load() >= 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("watcher did not converge: active=%#v status=%#v", active, status)
		}
	}
	if status := fleet.Status(); status.LastWatcherFailure != FleetFailureNone {
		t.Fatalf("watcher recovery status = %#v", status)
	}
	detail, err := fleet.Boolean("secure", Context{Tenant: "tenant-a"})
	if err != nil || !detail.Value {
		t.Fatalf("watcher activation = %#v, %v", detail, err)
	}

	cancelStart()
	if err := fleet.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-watcher.stopped:
	default:
		t.Fatal("watcher was not cancelled and joined before shutdown returned")
	}
}

func TestFleetWatcherFailureIsObservableWithoutProviderMisclassification(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	watcher := &fleetChannelWatcher{
		events: make(chan Invalidation), errors: make(chan error, 1), stopped: make(chan struct{}),
	}
	watcher.errors <- errors.New("valkey subscription ended")
	sleeper := &fleetTestSleeper{delays: make(chan time.Duration, 1), release: make(chan struct{})}
	config := securityFleetConfig(clock, &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "secure", false),
		Revision: "41", Provenance: "postgres", SourceTime: now,
	}}})
	config.Watcher = watcher
	config.Sleeper = sleeper
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForFleetEvent(t, sleeper.delays, "watcher failure refresh schedule")
	deadline := time.Now().Add(time.Second)
	for fleet.Status().LastWatcherFailure == FleetFailureNone {
		if time.Now().After(deadline) {
			t.Fatal("watcher failure was not observable")
		}
	}
	status := fleet.Status()
	if status.LastWatcherFailure != FleetFailureWatcher || status.LastRefreshFailure != FleetFailureNone || status.State != FleetReady {
		t.Fatalf("watcher failure classification = %#v", status)
	}
	deadline = time.Now().Add(time.Second)
	for fleet.Status().WatcherRunning {
		if time.Now().After(deadline) {
			t.Fatalf("terminal watcher failure did not stop delivery: %#v", fleet.Status())
		}
	}
	if watcher.calls.Load() != 1 {
		t.Fatalf("terminal watcher failure was polled %d times", watcher.calls.Load())
	}
	assertSecurityLastKnownGoodWindow(t, fleet, clock, now)
	if err := fleet.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFleetWatcherClassifiesDeliveryFailuresAndRecovers(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	providerErr := errors.New("provider unavailable")
	watcher := &fleetChannelWatcher{
		events: make(chan Invalidation, 2), errors: make(chan error), stopped: make(chan struct{}),
	}
	sleeper := &fleetTestSleeper{delays: make(chan time.Duration, 1), release: make(chan struct{})}
	loader := &fleetTestLoader{
		candidates: []SnapshotCandidate{{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "secure", false),
			Revision: "41", Provenance: "postgres", SourceTime: now,
		}},
		errors: []error{nil, providerErr},
	}
	clock := &fleetTestClock{now: now}
	config := securityFleetConfig(clock, loader)
	config.Watcher = watcher
	config.Sleeper = sleeper
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForFleetEvent(t, sleeper.delays, "watcher recovery refresh schedule")
	clock.Set(now.Add(2 * time.Second))
	watcher.events <- Invalidation{Tenant: "tenant-a", Sequence: 1, Revision: "42", ObservedAt: now}
	waitForWatcherFailure(t, fleet, FleetFailureInvalidation)
	assertSecurityLastKnownGoodWindow(t, fleet, clock, now)
	watcher.events <- Invalidation{Tenant: "tenant-a", Stream: "valkey", Sequence: 1, Revision: "42", ObservedAt: now}
	waitForWatcherFailure(t, fleet, FleetFailureProvider)
	if fleet.Status().LastRefreshFailure != FleetFailureProvider {
		t.Fatalf("provider refresh failure was not retained separately: %#v", fleet.Status())
	}
	if err := fleet.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFleetWatcherCancellationDuringDeliveryJoinsWithoutWatcherFailure(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	initial := fleetBooleanSnapshot(t, "tenant-a", "secure", false)
	entered := make(chan struct{})
	var calls atomic.Uint64
	loader := SnapshotLoadFunc(func(ctx context.Context, _ string) (SnapshotCandidate, error) {
		if calls.Add(1) == 1 {
			return SnapshotCandidate{Snapshot: initial, Revision: "41", Provenance: "postgres", SourceTime: now}, nil
		}
		close(entered)
		<-ctx.Done()
		return SnapshotCandidate{}, ctx.Err()
	})
	watcher := &fleetChannelWatcher{
		events: make(chan Invalidation, 1), errors: make(chan error), stopped: make(chan struct{}),
	}
	sleeper := &fleetTestSleeper{delays: make(chan time.Duration, 1), release: make(chan struct{})}
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.Watcher = watcher
	config.Sleeper = sleeper
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForFleetEvent(t, sleeper.delays, "watcher cancellation refresh schedule")
	watcher.events <- Invalidation{Tenant: "tenant-a", Stream: "valkey", Sequence: 1, Revision: "42", ObservedAt: now}
	waitForFleetEvent(t, entered, "watcher-triggered provider load")
	if err := fleet.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := fleet.Status(); status.LastWatcherFailure != FleetFailureNone || status.LastRefreshFailure != FleetFailureNone {
		t.Fatalf("cancellation classification = %#v", status)
	}
}

func waitForWatcherFailure(t *testing.T, fleet *Fleet, want FleetFailureCode) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for fleet.Status().LastWatcherFailure != want {
		if time.Now().After(deadline) {
			t.Fatalf("watcher failure = %q, want %q", fleet.Status().LastWatcherFailure, want)
		}
	}
}
