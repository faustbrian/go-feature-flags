package featureflags

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type invalidFleetJitter struct {
	delay time.Duration
	err   error
}

type errorFleetSleeper struct{ err error }

func (sleeper errorFleetSleeper) Sleep(context.Context, time.Duration) error { return sleeper.err }

type fleetBlockingCache struct {
	entered chan struct{}
	release chan struct{}
}

func (cache *fleetBlockingCache) Load(context.Context, string) (SnapshotCandidate, bool, error) {
	return SnapshotCandidate{}, false, nil
}

func (cache *fleetBlockingCache) Store(context.Context, string, SnapshotCandidate) error {
	close(cache.entered)
	<-cache.release
	return nil
}

type fleetOneRefreshSleeper struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Uint64
}

func (sleeper *fleetOneRefreshSleeper) Sleep(ctx context.Context, _ time.Duration) error {
	if sleeper.calls.Add(1) == 1 {
		close(sleeper.entered)
		select {
		case <-sleeper.release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

func (jitter invalidFleetJitter) Delay(string, uint64, time.Duration) (time.Duration, error) {
	return jitter.delay, jitter.err
}

func TestFleetLifecycleRejectsInvalidCallsAndSupportsStartupRetry(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	providerErr := errors.New("provider unavailable")
	loader := &fleetTestLoader{
		candidates: []SnapshotCandidate{
			{},
			{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true), Revision: "42", Provenance: "provider", SourceTime: now},
		},
		errors: []error{providerErr, nil},
	}
	sleeper := &fleetTestSleeper{delays: make(chan time.Duration, 1), release: make(chan struct{})}
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.Sleeper = sleeper
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	//lint:ignore SA1012 This verifies the public nil-context rejection contract.
	if _, err := fleet.Bootstrap(nil); err == nil { //nolint:staticcheck // Verifies nil-context rejection.
		t.Fatal("nil bootstrap context succeeded")
	}
	//lint:ignore SA1012 This verifies the public nil-context rejection contract.
	if _, err := fleet.Start(nil); err == nil { //nolint:staticcheck // Verifies nil-context rejection.
		t.Fatal("nil start context succeeded")
	}
	//lint:ignore SA1012 This verifies the public nil-context rejection contract.
	if _, err := fleet.Refresh(nil); err == nil { //nolint:staticcheck // Verifies nil-context rejection.
		t.Fatal("nil refresh context succeeded")
	}
	//lint:ignore SA1012 This verifies the public nil-context rejection contract.
	if err := fleet.Shutdown(nil); err == nil { //nolint:staticcheck // Verifies nil-context rejection.
		t.Fatal("nil shutdown context succeeded")
	}
	if _, err := fleet.Start(context.Background()); !errors.Is(err, providerErr) {
		t.Fatalf("failed startup = %v", err)
	}
	if active, err := fleet.Start(context.Background()); err != nil || active.Revision != "42" {
		t.Fatalf("startup retry = %#v, %v", active, err)
	}
	if _, err := fleet.Start(context.Background()); err == nil {
		t.Fatal("duplicate start succeeded")
	}
	<-sleeper.delays
	if err := fleet.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); !errors.Is(err, ErrFleetStopped) {
		t.Fatalf("bootstrap after stop = %v", err)
	}
	if _, err := fleet.Start(context.Background()); !errors.Is(err, ErrFleetStopped) {
		t.Fatalf("start after stop = %v", err)
	}
}

func TestFleetRefreshFrequencyWaiterCancellationAndSourceOrdering(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", false), Revision: "42", Provenance: "provider", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true), Revision: "43", Provenance: "provider", SourceTime: now.Add(-time.Second)},
	}}
	fleet, err := NewFleet(validFleetConfig(clock, loader))
	if err != nil {
		t.Fatal(err)
	}
	first, err := fleet.Refresh(context.Background())
	if err != nil || first.Active.Revision != "42" {
		t.Fatalf("first refresh = %#v, %v", first, err)
	}
	deferred, err := fleet.Refresh(context.Background())
	if err != nil || deferred.Disposition != RefreshDeferred || loader.calls != 1 {
		t.Fatalf("deferred refresh = %#v, %v, calls=%d", deferred, err, loader.calls)
	}
	clock.Set(now.Add(2 * time.Second))
	if _, err := fleet.Refresh(context.Background()); !errors.Is(err, ErrSnapshotReordered) {
		t.Fatalf("reordered source refresh = %v", err)
	}
	active, _ := fleet.Current()
	if active.Revision != "42" {
		t.Fatalf("reordered source activated: %#v", active)
	}

	blocking := &fleetBlockingLoader{
		candidate: SnapshotCandidate{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
			Revision: "wait", Provenance: "provider", SourceTime: now,
		},
		entered: make(chan struct{}, 1), release: make(chan struct{}),
	}
	waitFleet, err := NewFleet(validFleetConfig(&fleetTestClock{now: now}, blocking))
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, refreshErr := waitFleet.Refresh(context.Background())
		firstDone <- refreshErr
	}()
	<-blocking.entered
	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		_, refreshErr := waitFleet.Refresh(waiterCtx)
		waiterDone <- refreshErr
	}()
	deadline := time.Now().Add(time.Second)
	for waitFleet.Status().RefreshWaiters != 1 {
		if time.Now().After(deadline) {
			t.Fatal("waiter did not register")
		}
		runtime.Gosched()
	}
	cancelWaiter()
	if err := <-waiterDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled waiter = %v", err)
	}
	if waitFleet.Status().RefreshWaiters != 0 {
		t.Fatalf("cancelled waiter remained registered: %#v", waitFleet.Status())
	}
	close(blocking.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestFleetInvalidationValidationAndStreamBound(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "42", Provenance: "provider", SourceTime: now,
	}}}
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.MaxInvalidationStreams = 1
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	//lint:ignore SA1012 This verifies the public nil-context rejection contract.
	if _, err := fleet.Invalidate(nil, Invalidation{ //nolint:staticcheck // Verifies nil-context rejection.
		Tenant: "tenant-a", Stream: "nil-context", Sequence: 1, Revision: "42", ObservedAt: now,
	}); err == nil {
		t.Fatal("nil invalidation context succeeded")
	}
	invalid := []Invalidation{
		{},
		{Tenant: "tenant-b", Stream: "stream", Sequence: 1, Revision: "42", ObservedAt: now},
		{Tenant: "tenant-a", Stream: "", Sequence: 1, Revision: "42", ObservedAt: now},
		{Tenant: "tenant-a", Stream: strings.Repeat("s", maxFleetIdentityLength+1), Sequence: 1, Revision: "42", ObservedAt: now},
		{Tenant: "tenant-a", Stream: "stream", Sequence: 0, Revision: "42", ObservedAt: now},
		{Tenant: "tenant-a", Stream: "stream", Sequence: 1, Revision: "", ObservedAt: now},
		{Tenant: "tenant-a", Stream: "stream", Sequence: 1, Revision: strings.Repeat("r", maxFleetIdentityLength+1), ObservedAt: now},
		{Tenant: "tenant-a", Stream: "stream", Sequence: 1, Revision: "42"},
	}
	for _, event := range invalid {
		if _, err := fleet.Invalidate(context.Background(), event); !errors.Is(err, ErrInvalidInvalidation) {
			t.Fatalf("invalid event %#v = %v", event, err)
		}
	}
	if result, err := fleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "one", Sequence: 1, Revision: "42", ObservedAt: now,
	}); err != nil || result.Disposition != InvalidationCurrent {
		t.Fatalf("first stream = %#v, %v", result, err)
	}
	if _, err := fleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "two", Sequence: 1, Revision: "42", ObservedAt: now,
	}); !errors.Is(err, ErrInvalidationStreams) {
		t.Fatalf("excess stream = %v", err)
	}
}

func TestFleetInvalidJitterStopsBackgroundRefresher(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	for name, jitter := range map[string]FleetJitter{
		"error": invalidFleetJitter{err: errors.New("jitter failed")},
		"bound": invalidFleetJitter{delay: 11 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
				Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
				Revision: "42", Provenance: "provider", SourceTime: now,
			}}}
			config := validFleetConfig(&fleetTestClock{now: now}, loader)
			config.Jitter = jitter
			fleet, err := NewFleet(config)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fleet.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(time.Second)
			for fleet.Status().State != FleetStopped {
				if time.Now().After(deadline) {
					t.Fatal("invalid jitter did not stop fleet")
				}
				runtime.Gosched()
			}
			if fleet.Status().LastRefreshFailure != FleetFailureScheduler {
				t.Fatalf("invalid jitter status = %#v", fleet.Status())
			}
			if err := fleet.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFleetSleeperFailureStopsBackgroundRefresherObservably(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	config := validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "42", Provenance: "provider", SourceTime: now,
	}}})
	config.Sleeper = errorFleetSleeper{err: errors.New("scheduler unavailable")}
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for fleet.Status().State != FleetStopped {
		if time.Now().After(deadline) {
			t.Fatal("sleeper failure did not stop fleet")
		}
		runtime.Gosched()
	}
	if status := fleet.Status(); status.LastRefreshFailure != FleetFailureScheduler {
		t.Fatalf("sleeper failure status = %#v", status)
	}
	if err := fleet.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFleetBackgroundRefreshReportsSaturatedWaiters(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	loader := &fleetShutdownLoader{
		first: SnapshotCandidate{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
			Revision: "42", Provenance: "provider", SourceTime: now,
		},
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	sleeper := &fleetOneRefreshSleeper{entered: make(chan struct{}), release: make(chan struct{})}
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.MaxWaiters = 1
	config.Sleeper = sleeper
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-sleeper.entered
	first := make(chan error, 1)
	go func() {
		_, refreshErr := fleet.Refresh(context.Background())
		first <- refreshErr
	}()
	<-loader.entered
	waiter := make(chan error, 1)
	go func() {
		_, refreshErr := fleet.Refresh(context.Background())
		waiter <- refreshErr
	}()
	deadline := time.Now().Add(time.Second)
	for fleet.Status().RefreshWaiters != 1 {
		if time.Now().After(deadline) {
			t.Fatal("coalesced waiter did not occupy the declared bound")
		}
		runtime.Gosched()
	}
	close(sleeper.release)
	for fleet.Status().LastRefreshFailure != FleetFailureWaiterLimit {
		if time.Now().After(deadline) {
			t.Fatalf("background waiter saturation was not observable: %#v", fleet.Status())
		}
		runtime.Gosched()
	}
	if err := fleet.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("physical refresh shutdown error = %v", err)
	}
	if err := <-waiter; !errors.Is(err, context.Canceled) {
		t.Fatalf("coalesced refresh shutdown error = %v", err)
	}
}

func TestFleetShutdownJoinsBootstrapCacheWorkAndPreventsLateStart(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "42", Provenance: "provider", SourceTime: now,
	}}}
	cache := &fleetBlockingCache{entered: make(chan struct{}), release: make(chan struct{})}
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.Cache = cache
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	startDone := make(chan error, 1)
	go func() {
		_, startErr := fleet.Start(context.Background())
		startDone <- startErr
	}()
	<-cache.entered
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- fleet.Shutdown(context.Background()) }()
	deadline := time.Now().Add(time.Second)
	for fleet.Status().State != FleetStopped {
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not stop fleet")
		}
		runtime.Gosched()
	}
	close(cache.release)
	if err := <-startDone; !errors.Is(err, ErrFleetStopped) {
		t.Fatalf("start racing shutdown = %v", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.activate(loader.candidates[0], true); !errors.Is(err, ErrFleetStopped) {
		t.Fatalf("activation after shutdown = %v", err)
	}
	if _, err := fleet.load(context.Background()); !errors.Is(err, ErrFleetStopped) {
		t.Fatalf("provider load after shutdown = %v", err)
	}
}

func TestFleetBootstrapConcurrencyIsBoundedByLoadSlotsAndCallerDeadline(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	loader := &fleetBlockingLoader{
		candidate: SnapshotCandidate{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
			Revision: "42", Provenance: "provider", SourceTime: now,
		},
		entered: make(chan struct{}, 1), release: make(chan struct{}),
	}
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.MaxConcurrentProviderLoads = 1
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, bootstrapErr := fleet.Bootstrap(context.Background())
		firstDone <- bootstrapErr
	}()
	<-loader.entered
	deadline, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fleet.Bootstrap(deadline); !errors.Is(err, context.Canceled) {
		t.Fatalf("bounded concurrent bootstrap = %v", err)
	}
	close(loader.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestFleetPeriodicRefreshFailureIsObservableAndDegradesStaleState(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	providerErr := errors.New("provider unavailable")
	loader := &fleetTestLoader{
		candidates: []SnapshotCandidate{{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
			Revision: "42", Provenance: "provider", SourceTime: now,
		}, {}},
		errors: []error{nil, providerErr},
	}
	sleeper := &fleetOneRefreshSleeper{entered: make(chan struct{}), release: make(chan struct{})}
	config := validFleetConfig(clock, loader)
	config.Sleeper = sleeper
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-sleeper.entered
	clock.Set(now.Add(config.FreshFor + time.Nanosecond))
	close(sleeper.release)
	deadline := time.Now().Add(time.Second)
	for {
		status := fleet.Status()
		if status.LastRefreshFailure == FleetFailureProvider && status.State == FleetDegraded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("periodic refresh failure not observable: %#v", status)
		}
		runtime.Gosched()
	}
	if err := fleet.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFleetFailureCodesRemainBoundedAndDeterministic(t *testing.T) {
	tests := []struct {
		err  error
		want FleetFailureCode
	}{
		{nil, FleetFailureNone},
		{ErrMalformedSnapshot, FleetFailureInvalidSnapshot},
		{ErrNoUsableSnapshot, FleetFailureInvalidSnapshot},
		{ErrSnapshotReordered, FleetFailureInvalidSnapshot},
		{ErrSnapshotStale, FleetFailureStaleSnapshot},
		{ErrSnapshotFuture, FleetFailureInvalidSnapshot},
		{ErrRefreshLoadLimit, FleetFailureLoadLimit},
		{ErrRefreshWaiterLimit, FleetFailureWaiterLimit},
		{context.Canceled, FleetFailureCancelled},
		{context.DeadlineExceeded, FleetFailureCancelled},
		{ErrFleetStopped, FleetFailureStopped},
		{errors.New("provider detail must not escape"), FleetFailureProvider},
	}
	for _, test := range tests {
		if got := classifyFleetFailure(test.err); got != test.want {
			t.Fatalf("failure code for %v = %q, want %q", test.err, got, test.want)
		}
	}
}

func TestFleetUnknownDegradedPolicyFailsClosed(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	config := validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{})
	config.Policies = map[string]FlagPolicy{"flag": {Mode: DegradedFailClosed}}
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the defensive boundary against in-package state corruption; the
	// public constructor rejects this mode before it can become active.
	fleet.config.Policies["flag"] = FlagPolicy{Mode: DegradedMode(255)}
	if _, err := fleet.Boolean("flag", Context{Tenant: "tenant-a"}); !errors.Is(err, ErrSnapshotStale) {
		t.Fatalf("unknown degraded policy did not fail closed: %v", err)
	}
}

func TestFleetOperationalCountersSaturateInsteadOfWrapping(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "42", Provenance: "provider", SourceTime: now,
	}}}
	fleet, err := NewFleet(validFleetConfig(clock, loader))
	if err != nil {
		t.Fatal(err)
	}
	fleet.providerLoads.Store(math.MaxUint64)
	if _, err := fleet.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	fleet.mu.Lock()
	fleet.invalidationGaps = math.MaxUint64
	fleet.mu.Unlock()
	if _, err := fleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "valkey", Sequence: 2,
		Revision: "42", ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	status := fleet.Status()
	if status.ProviderLoads != math.MaxUint64 || status.InvalidationGaps != math.MaxUint64 {
		t.Fatalf("fleet counters wrapped: %#v", status)
	}
}
