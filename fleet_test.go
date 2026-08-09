package featureflags

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fleetTestClock struct {
	mu  sync.RWMutex
	now time.Time
}

func (clock *fleetTestClock) Now() time.Time {
	clock.mu.RLock()
	defer clock.mu.RUnlock()
	return clock.now
}

func (clock *fleetTestClock) Set(now time.Time) {
	clock.mu.Lock()
	clock.now = now
	clock.mu.Unlock()
}

type fleetTestLoader struct {
	candidates []SnapshotCandidate
	errors     []error
	calls      int
}

func (loader *fleetTestLoader) Load(context.Context, string) (SnapshotCandidate, error) {
	index := loader.calls
	loader.calls++
	if index < len(loader.errors) && loader.errors[index] != nil {
		return SnapshotCandidate{}, loader.errors[index]
	}
	return loader.candidates[index], nil
}

type fleetTestCache struct {
	candidate SnapshotCandidate
	found     bool
	loadErr   error
	storeErr  error
	stored    []SnapshotCandidate
}

type fleetDeadlineCache struct {
	loadHadDeadline  bool
	storeHadDeadline bool
}

type fleetRefreshCancellationCache struct {
	calls   atomic.Uint64
	entered chan struct{}
}

func (*fleetRefreshCancellationCache) Load(context.Context, string) (SnapshotCandidate, bool, error) {
	return SnapshotCandidate{}, false, nil
}

func (cache *fleetRefreshCancellationCache) Store(ctx context.Context, _ string, _ SnapshotCandidate) error {
	if cache.calls.Add(1) == 1 {
		return nil
	}
	close(cache.entered)
	<-ctx.Done()
	return ctx.Err()
}

func (cache *fleetDeadlineCache) Load(ctx context.Context, _ string) (SnapshotCandidate, bool, error) {
	_, cache.loadHadDeadline = ctx.Deadline()
	return SnapshotCandidate{}, false, nil
}

func (cache *fleetDeadlineCache) Store(ctx context.Context, _ string, _ SnapshotCandidate) error {
	_, cache.storeHadDeadline = ctx.Deadline()
	return nil
}

type fleetTestSleeper struct {
	delays  chan time.Duration
	release chan struct{}
}

func (sleeper *fleetTestSleeper) Sleep(ctx context.Context, delay time.Duration) error {
	select {
	case sleeper.delays <- delay:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-sleeper.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type fleetSequenceLoader struct {
	mu         sync.Mutex
	candidates []SnapshotCandidate
	calls      int
}

type fleetBlockingLoader struct {
	candidate SnapshotCandidate
	entered   chan struct{}
	release   chan struct{}
	calls     atomic.Uint64
}

type fleetShutdownLoader struct {
	first        SnapshotCandidate
	entered      chan struct{}
	release      chan struct{}
	ignoreCancel bool
	calls        atomic.Uint64
}

func (loader *fleetShutdownLoader) Load(ctx context.Context, _ string) (SnapshotCandidate, error) {
	if loader.calls.Add(1) == 1 {
		return loader.first, nil
	}
	close(loader.entered)
	if loader.ignoreCancel {
		<-loader.release
		return SnapshotCandidate{}, ctx.Err()
	}
	<-ctx.Done()
	return SnapshotCandidate{}, ctx.Err()
}

func (loader *fleetBlockingLoader) Load(ctx context.Context, _ string) (SnapshotCandidate, error) {
	loader.calls.Add(1)
	loader.entered <- struct{}{}
	select {
	case <-loader.release:
		return loader.candidate, nil
	case <-ctx.Done():
		return SnapshotCandidate{}, ctx.Err()
	}
}

func (loader *fleetSequenceLoader) Load(context.Context, string) (SnapshotCandidate, error) {
	loader.mu.Lock()
	defer loader.mu.Unlock()
	candidate := loader.candidates[loader.calls]
	loader.calls++
	return candidate, nil
}

func (cache *fleetTestCache) Load(context.Context, string) (SnapshotCandidate, bool, error) {
	return cache.candidate, cache.found, cache.loadErr
}

func (cache *fleetTestCache) Store(_ context.Context, _ string, candidate SnapshotCandidate) error {
	cache.stored = append(cache.stored, candidate)
	return cache.storeErr
}

func fleetBooleanSnapshot(t testing.TB, tenant, key string, value bool) Snapshot {
	t.Helper()
	provider := NewMemoryProvider(DefaultLimits())
	_, err := provider.Create(context.Background(), tenant, Definition{
		Key:       key,
		Type:      TypeBoolean,
		Default:   BooleanValue(value),
		Lifecycle: LifecycleActive,
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := provider.Snapshot(context.Background(), tenant)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func validFleetConfig(clock CacheClock, loader SnapshotLoader) FleetConfig {
	return FleetConfig{
		Tenant:                     "tenant-a",
		ReplicaID:                  "pod-a",
		Loader:                     loader,
		Clock:                      clock,
		RefreshInterval:            time.Minute,
		MinRefreshInterval:         time.Second,
		MaxRefreshJitter:           10 * time.Second,
		LoadTimeout:                5 * time.Second,
		FreshFor:                   2 * time.Minute,
		MaxStaleness:               10 * time.Minute,
		MaxFutureSkew:              10 * time.Second,
		ConvergenceWindow:          2 * time.Minute,
		MaxWaiters:                 4,
		MaxProviderLoads:           3,
		MaxConcurrentProviderLoads: 1,
		MaxInvalidationStreams:     2,
		MaxPolicies:                32,
		AllowEmptyBootstrap:        false,
		AllowStaleBootstrap:        true,
	}
}

func TestFleetBootstrapUsesValidatedPrimarySnapshot(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot:   fleetBooleanSnapshot(t, "tenant-a", "checkout", true),
		Revision:   "42",
		Provenance: "postgres-primary",
		SourceTime: now.Add(-time.Second),
	}}}
	cache := &fleetTestCache{}
	config := validFleetConfig(clock, loader)
	config.Cache = cache
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}

	active, err := fleet.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if active.Revision != "42" || active.Provenance != "postgres-primary" || active.Age(now) != time.Second {
		t.Fatalf("unexpected active metadata: %#v", active)
	}
	detail, err := fleet.Boolean("checkout", Context{Tenant: "tenant-a"})
	if err != nil || !detail.Value || detail.Reason != ReasonDefault {
		t.Fatalf("unexpected evaluation: %#v, %v", detail, err)
	}
	status := fleet.Status()
	if status.State != FleetReady || status.Revision != "42" || status.ProviderLoads != 1 {
		t.Fatalf("unexpected fleet status: %#v", status)
	}
	if len(cache.stored) != 1 || cache.stored[0].Revision != "42" || status.LastCacheFailure != FleetFailureNone {
		t.Fatalf("validated primary was not cached: %#v, %#v", cache.stored, status)
	}
}

func TestFleetBootstrapFallsBackToBoundedStaleCache(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	providerErr := errors.New("postgres unavailable")
	loader := &fleetTestLoader{errors: []error{providerErr}}
	cache := &fleetTestCache{found: true, candidate: SnapshotCandidate{
		Snapshot:   fleetBooleanSnapshot(t, "tenant-a", "checkout", false),
		Revision:   "cached-41",
		Provenance: "valkey-cache",
		SourceTime: now.Add(-5 * time.Minute),
	}}
	config := validFleetConfig(clock, loader)
	config.Cache = cache
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}

	active, err := fleet.Bootstrap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if active.Revision != "cached-41" || fleet.Status().State != FleetDegraded {
		t.Fatalf("unexpected cached bootstrap: %#v %#v", active, fleet.Status())
	}
	if fleet.Status().LastRefreshFailure != FleetFailureProvider {
		t.Fatalf("provider failure was not observable: %#v", fleet.Status())
	}
}

func TestFleetRejectsUnsafeSecurityPolicy(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	config := validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{})
	config.Policies = map[string]FlagPolicy{
		"admin-bypass": {Mode: DegradedFailOpen, SecuritySensitive: true},
	}
	if _, err := NewFleet(config); !errors.Is(err, ErrUnsafeFlagPolicy) {
		t.Fatalf("expected unsafe policy error, got %v", err)
	}
}

func TestFleetRefreshNeverPartiallyActivatesMalformedReplacement(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{
			Snapshot:   fleetBooleanSnapshot(t, "tenant-a", "checkout", false),
			Revision:   "41",
			Provenance: "postgres-primary",
			SourceTime: now,
		},
		{
			Snapshot:   Snapshot{},
			Revision:   "42",
			Provenance: "postgres-primary",
			SourceTime: now.Add(2 * time.Second),
		},
	}}
	fleet, err := NewFleet(validFleetConfig(clock, loader))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Set(now.Add(2 * time.Second))

	if _, err := fleet.Refresh(context.Background()); !errors.Is(err, ErrMalformedSnapshot) {
		t.Fatalf("expected malformed snapshot error, got %v", err)
	}
	active, ok := fleet.Current()
	if !ok || active.Revision != "41" {
		t.Fatalf("malformed replacement changed active snapshot: %#v, %t", active, ok)
	}
	detail, err := fleet.Boolean("checkout", Context{Tenant: "tenant-a"})
	if err != nil || detail.Value {
		t.Fatalf("last-known-good evaluation changed: %#v, %v", detail, err)
	}
	if fleet.Status().LastRefreshFailure != FleetFailureInvalidSnapshot {
		t.Fatalf("refresh failure is not observable: %#v", fleet.Status())
	}
}

func TestFleetRefreshRejectsStaleReplacementAndKeepsLastKnownGood(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", false), Revision: "41", Provenance: "provider", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", true), Revision: "42", Provenance: "cache", SourceTime: now},
	}}
	fleet, err := NewFleet(validFleetConfig(clock, loader))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Set(now.Add(3 * time.Minute))
	if _, err := fleet.Refresh(context.Background()); !errors.Is(err, ErrSnapshotStale) {
		t.Fatalf("stale refresh = %v", err)
	}
	active, _ := fleet.Current()
	if active.Revision != "41" {
		t.Fatalf("stale replacement activated: %#v", active)
	}
}

func TestFleetRejectsOlderSourceTimeWhenRevisionRepeats(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", false), Revision: "42", Provenance: "provider", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true), Revision: "42", Provenance: "provider", SourceTime: now.Add(-time.Second)},
	}}
	fleet, err := NewFleet(validFleetConfig(clock, loader))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Set(now.Add(2 * time.Second))
	if _, err := fleet.Refresh(context.Background()); !errors.Is(err, ErrSnapshotReordered) {
		t.Fatalf("same-revision older source = %v", err)
	}
	detail, err := fleet.Boolean("flag", Context{Tenant: "tenant-a"})
	if err != nil || detail.Value {
		t.Fatalf("same-revision older source changed evaluation: %#v, %v", detail, err)
	}
}

func TestFleetAppliesTypedExplicitDefaultsWithoutSnapshot(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	config := validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{})
	config.Policies = map[string]FlagPolicy{
		"bool":    {Mode: DegradedFailOpen},
		"string":  {Mode: DegradedDefault, Default: StringValue("safe")},
		"integer": {Mode: DegradedDefault, Default: IntegerValue(7)},
		"float":   {Mode: DegradedDefault, Default: FloatValue(1.5)},
		"decimal": {Mode: DegradedDefault, Default: DecimalValue("1.25")},
		"json":    {Mode: DegradedDefault, Default: StructuredValue(json.RawMessage(`{"safe":true}`))},
	}
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx := Context{Tenant: "tenant-a"}
	if detail, err := fleet.Boolean("bool", ctx); err != nil || !detail.Value || detail.Reason != ReasonDegradedFailOpen {
		t.Fatalf("boolean fallback = %#v, %v", detail, err)
	}
	if detail, err := fleet.String("string", ctx); err != nil || detail.Value != "safe" || detail.Reason != ReasonDegradedDefault {
		t.Fatalf("string fallback = %#v, %v", detail, err)
	}
	if detail, err := fleet.Integer("integer", ctx); err != nil || detail.Value != 7 {
		t.Fatalf("integer fallback = %#v, %v", detail, err)
	}
	if detail, err := fleet.Float("float", ctx); err != nil || detail.Value != 1.5 {
		t.Fatalf("float fallback = %#v, %v", detail, err)
	}
	if detail, err := fleet.Decimal("decimal", ctx); err != nil || detail.Value != "1.25" {
		t.Fatalf("decimal fallback = %#v, %v", detail, err)
	}
	first, err := fleet.Structured("json", ctx)
	if err != nil || string(first.Value) != `{"safe":true}` {
		t.Fatalf("structured fallback = %#v, %v", first, err)
	}
	first.Value[0] = '['
	second, err := fleet.Structured("json", ctx)
	if err != nil || string(second.Value) != `{"safe":true}` {
		t.Fatalf("structured fallback aliases caller memory: %#v, %v", second, err)
	}
}

func TestFleetInvalidationClassifiesGapsDuplicatesReorderingAndRevisions(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", false), Revision: "41", Provenance: "postgres", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", true), Revision: "42", Provenance: "postgres", SourceTime: now.Add(time.Second)},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", false), Revision: "43", Provenance: "postgres", SourceTime: now.Add(3 * time.Second)},
	}}
	fleet, err := NewFleet(validFleetConfig(clock, loader))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}

	clock.Set(now.Add(time.Second))
	gap, err := fleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "valkey", Sequence: 4, Revision: "42", ObservedAt: now.Add(time.Second),
	})
	if err != nil || gap.Disposition != InvalidationRefreshed || !gap.Gap || gap.ActiveRevision != "42" {
		t.Fatalf("gap invalidation = %#v, %v", gap, err)
	}
	loads := loader.calls
	duplicate, err := fleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "valkey", Sequence: 4, Revision: "42", ObservedAt: now.Add(2 * time.Second),
	})
	if err != nil || duplicate.Disposition != InvalidationDuplicate || loader.calls != loads {
		t.Fatalf("duplicate invalidation = %#v, %v, loads %d", duplicate, err, loader.calls)
	}
	reordered, err := fleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "valkey", Sequence: 3, Revision: "old", ObservedAt: now,
	})
	if err != nil || reordered.Disposition != InvalidationReordered || loader.calls != loads {
		t.Fatalf("reordered invalidation = %#v, %v, loads %d", reordered, err, loader.calls)
	}
	current, err := fleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "valkey", Sequence: 5, Revision: "42", ObservedAt: now,
	})
	if err != nil || current.Disposition != InvalidationCurrent || current.Delay != time.Second || loader.calls != loads {
		t.Fatalf("delayed current invalidation = %#v, %v, loads %d", current, err, loader.calls)
	}
	clock.Set(now.Add(3 * time.Second))
	crossRevision, err := fleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "valkey", Sequence: 6, Revision: "43", ObservedAt: now.Add(3 * time.Second),
	})
	if err != nil || crossRevision.Disposition != InvalidationRefreshed || crossRevision.ActiveRevision != "43" {
		t.Fatalf("cross-revision invalidation = %#v, %v", crossRevision, err)
	}
	if status := fleet.Status(); status.InvalidationGaps != 1 || status.ConvergenceDeadline.IsZero() {
		t.Fatalf("invalidation status = %#v", status)
	}
}

func TestFleetConvergenceDeadlineUsesBoundedLocalReceiptTime(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", false), Revision: "41", Provenance: "provider", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true), Revision: "42", Provenance: "provider", SourceTime: now},
	}}
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	result, err := fleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "valkey", Sequence: 1,
		Revision: "42", ObservedAt: now.Add(time.Hour),
	})
	if err != nil || result.Disposition != InvalidationRefreshed {
		t.Fatalf("future-clock invalidation = %#v, %v", result, err)
	}
	wantDeadline := now.Add(config.ConvergenceWindow)
	if result.Delay != 0 || result.ConvergenceDeadline != wantDeadline || fleet.Status().ConvergenceDeadline != wantDeadline {
		t.Fatalf("unbounded convergence deadline = %#v, %#v", result, fleet.Status())
	}
}

func TestFleetStartJittersRefreshAndShutdownJoinsRefresher(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	loader := &fleetSequenceLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", false), Revision: "41", Provenance: "postgres", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", true), Revision: "42", Provenance: "postgres", SourceTime: now.Add(70 * time.Second)},
	}}
	sleeper := &fleetTestSleeper{delays: make(chan time.Duration, 1), release: make(chan struct{}, 1)}
	config := validFleetConfig(clock, loader)
	config.Sleeper = sleeper
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	delay := <-sleeper.delays
	if delay < config.RefreshInterval || delay > config.RefreshInterval+config.MaxRefreshJitter {
		t.Fatalf("refresh delay %s is outside configured jitter bound", delay)
	}
	clock.Set(now.Add(70 * time.Second))
	sleeper.release <- struct{}{}
	deadline := time.Now().Add(time.Second)
	for {
		active, _ := fleet.Current()
		if active.Revision == "42" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background refresh did not activate revision 42")
		}
		runtime.Gosched()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fleet.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if fleet.Status().State != FleetStopped {
		t.Fatalf("fleet did not stop: %#v", fleet.Status())
	}
	if _, err := fleet.Refresh(context.Background()); !errors.Is(err, ErrFleetStopped) {
		t.Fatalf("refresh after shutdown = %v", err)
	}
}

func TestDeterministicFleetJitterIsReplicaStableAndBounded(t *testing.T) {
	jitter := DeterministicFleetJitter{}
	first, err := jitter.Delay("pod-a", 7, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := jitter.Delay("pod-a", 7, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	other, err := jitter.Delay("pod-b", 7, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if first != repeated || first < 0 || first > 10*time.Second || first == other {
		t.Fatalf("unexpected deterministic jitter: first=%s repeated=%s other=%s", first, repeated, other)
	}
}

func TestProviderSnapshotLoaderDerivesStableContentRevision(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	provider := NewMemoryProvider(DefaultLimits())
	created, err := provider.Create(context.Background(), "tenant-a", Definition{
		Key: "checkout", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
	}, "test")
	if err != nil {
		t.Fatal(err)
	}
	loader, err := NewProviderSnapshotLoader(provider, clock, "memory-provider")
	if err != nil {
		t.Fatal(err)
	}
	first, err := loader.Load(context.Background(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := loader.Load(context.Background(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision == "" || first.Revision != repeated.Revision || first.Provenance != "memory-provider" || first.SourceTime != now {
		t.Fatalf("unstable provider candidate: %#v %#v", first, repeated)
	}
	created.Default = BooleanValue(true)
	if _, err := provider.Update(context.Background(), "tenant-a", created, created.Version, "test"); err != nil {
		t.Fatal(err)
	}
	changed, err := loader.Load(context.Background(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if changed.Revision == first.Revision {
		t.Fatal("content revision did not change with provider snapshot")
	}
}

func TestFleetRefreshCoalescesProviderLoadAndBoundsWaiters(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	loader := &fleetBlockingLoader{
		candidate: SnapshotCandidate{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", true),
			Revision: "42", Provenance: "postgres", SourceTime: now,
		},
		entered: make(chan struct{}, 1), release: make(chan struct{}),
	}
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.MaxWaiters = 1
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	type refreshOutcome struct {
		result RefreshResult
		err    error
	}
	first := make(chan refreshOutcome, 1)
	go func() {
		result, err := fleet.Refresh(context.Background())
		first <- refreshOutcome{result: result, err: err}
	}()
	<-loader.entered
	second := make(chan refreshOutcome, 1)
	go func() {
		result, err := fleet.Refresh(context.Background())
		second <- refreshOutcome{result: result, err: err}
	}()
	deadline := time.Now().Add(time.Second)
	for fleet.Status().RefreshWaiters != 1 {
		if time.Now().After(deadline) {
			t.Fatal("coalesced waiter did not register")
		}
		runtime.Gosched()
	}
	if _, err := fleet.Refresh(context.Background()); !errors.Is(err, ErrRefreshWaiterLimit) {
		t.Fatalf("excess waiter = %v", err)
	}
	close(loader.release)
	firstResult := <-first
	secondResult := <-second
	if firstResult.err != nil || secondResult.err != nil || secondResult.result.Coalesced != true {
		t.Fatalf("refresh results = %#v %#v", firstResult, secondResult)
	}
	if loader.calls.Load() != 1 || fleet.Status().RefreshWaiters != 0 {
		t.Fatalf("provider calls=%d status=%#v", loader.calls.Load(), fleet.Status())
	}
}

func TestFleetExecutorCannotExceedProviderLoadBudget(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	var calls atomic.Uint64
	loader := SnapshotLoadFunc(func(context.Context, string) (SnapshotCandidate, error) {
		calls.Add(1)
		return SnapshotCandidate{}, errors.New("provider unavailable")
	})
	executor := RefreshExecuteFunc(func(ctx context.Context, operation RefreshOperation) (SnapshotCandidate, error) {
		var err error
		for range 4 {
			if _, err = operation(ctx); errors.Is(err, ErrRefreshLoadLimit) {
				return SnapshotCandidate{}, err
			}
		}
		return SnapshotCandidate{}, err
	})
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.Executor = executor
	config.MaxProviderLoads = 3
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); !errors.Is(err, ErrRefreshLoadLimit) {
		t.Fatalf("bootstrap error = %v", err)
	}
	if calls.Load() != 3 || fleet.Status().ProviderLoads != 3 {
		t.Fatalf("provider loads escaped budget: calls=%d status=%#v", calls.Load(), fleet.Status())
	}
}

func TestFleetClassifiesCallerOwnedResilienceRejectionsWithoutRetainingErrors(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	circuitErr := errors.New("circuit open with provider detail")
	var fleet *Fleet
	config := validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{errors: []error{circuitErr}})
	config.FailureClassifier = FleetFailureClassifyFunc(func(err error) FleetFailureCode {
		_ = fleet.Status()
		if errors.Is(err, circuitErr) {
			return FleetFailureCircuitOpen
		}
		return FleetFailureCode("unbounded-caller-value")
	})
	var err error
	fleet, err = NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); !errors.Is(err, circuitErr) {
		t.Fatalf("classified bootstrap error = %v", err)
	}
	if status := fleet.Status(); status.LastRefreshFailure != FleetFailureCircuitOpen {
		t.Fatalf("circuit rejection classification = %#v", status)
	}
	for _, code := range []FleetFailureCode{
		FleetFailureProvider,
		FleetFailureRetryExhausted,
		FleetFailureBulkhead,
		FleetFailureThrottled,
		FleetFailureConcurrency,
		FleetFailureBudgetExhausted,
	} {
		classifiedConfig := validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{errors: []error{circuitErr}})
		classifiedConfig.FailureClassifier = FleetFailureClassifyFunc(func(error) FleetFailureCode { return code })
		classifiedFleet, err := NewFleet(classifiedConfig)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := classifiedFleet.Bootstrap(context.Background()); !errors.Is(err, circuitErr) {
			t.Fatalf("classified %q bootstrap error = %v", code, err)
		}
		if status := classifiedFleet.Status(); status.LastRefreshFailure != code {
			t.Fatalf("classified %q status = %#v", code, status)
		}
	}

	unknown := validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{errors: []error{errors.New("unknown")}})
	unknown.FailureClassifier = FleetFailureClassifyFunc(func(error) FleetFailureCode {
		return FleetFailureCode("unbounded-caller-value")
	})
	unknownFleet, err := NewFleet(unknown)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unknownFleet.Bootstrap(context.Background()); err == nil {
		t.Fatal("unknown provider failure unexpectedly bootstrapped")
	}
	if status := unknownFleet.Status(); status.LastRefreshFailure != FleetFailureProvider {
		t.Fatalf("unknown classification escaped bounded status: %#v", status)
	}

	staleConfig := validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "stale", Provenance: "provider", SourceTime: now.Add(-3 * time.Minute),
	}}})
	staleConfig.AllowStaleBootstrap = false
	staleConfig.FailureClassifier = FleetFailureClassifyFunc(func(error) FleetFailureCode {
		return FleetFailureCircuitOpen
	})
	staleFleet, err := NewFleet(staleConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := staleFleet.Bootstrap(context.Background()); !errors.Is(err, ErrSnapshotStale) {
		t.Fatalf("stale bootstrap = %v", err)
	}
	if status := staleFleet.Status(); status.LastRefreshFailure != FleetFailureStaleSnapshot {
		t.Fatalf("classifier overrode fleet invariant: %#v", status)
	}
}

func TestFleetBoundsConcurrentPhysicalLoadsInsideExecutor(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	candidate := SnapshotCandidate{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "42", Provenance: "provider", SourceTime: now,
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{}, 2)
	loader := SnapshotLoadFunc(func(context.Context, string) (SnapshotCandidate, error) {
		entered <- struct{}{}
		<-release
		return candidate, nil
	})
	executor := RefreshExecuteFunc(func(ctx context.Context, operation RefreshOperation) (SnapshotCandidate, error) {
		results := make(chan SnapshotCandidate, 2)
		errorsSeen := make(chan error, 2)
		for range 2 {
			go func() {
				result, err := operation(ctx)
				results <- result
				errorsSeen <- err
			}()
		}
		var result SnapshotCandidate
		for range 2 {
			result = <-results
			if err := <-errorsSeen; err != nil {
				return SnapshotCandidate{}, err
			}
		}
		return result, nil
	})
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.Executor = executor
	config.MaxProviderLoads = 2
	config.MaxConcurrentProviderLoads = 1
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapDone := make(chan error, 1)
	go func() {
		_, bootstrapErr := fleet.Bootstrap(context.Background())
		bootstrapDone <- bootstrapErr
	}()
	<-entered
	select {
	case <-entered:
		t.Fatal("executor exceeded the physical provider concurrency bound")
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	<-entered
	release <- struct{}{}
	if err := <-bootstrapDone; err != nil {
		t.Fatal(err)
	}
}

func TestFleetExecutorCannotBypassOperationContext(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	for name, operationContext := range map[string]func() context.Context{
		"nil": func() context.Context { return nil },
		"cancelled": func() context.Context {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx
		},
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Uint64
			loader := SnapshotLoadFunc(func(context.Context, string) (SnapshotCandidate, error) {
				calls.Add(1)
				return SnapshotCandidate{}, errors.New("loader must not run")
			})
			config := validFleetConfig(&fleetTestClock{now: now}, loader)
			config.Executor = RefreshExecuteFunc(func(_ context.Context, operation RefreshOperation) (SnapshotCandidate, error) {
				return operation(operationContext())
			})
			fleet, err := NewFleet(config)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fleet.Bootstrap(context.Background()); err == nil {
				t.Fatal("invalid operation context succeeded")
			}
			if calls.Load() != 0 {
				t.Fatalf("invalid operation context reached provider %d times", calls.Load())
			}
		})
	}
}

func TestFleetQueuedPhysicalLoadHonorsAttemptCancellation(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	candidate := SnapshotCandidate{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "42", Provenance: "provider", SourceTime: now,
	}
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var providerCalls atomic.Uint64
	loader := SnapshotLoadFunc(func(context.Context, string) (SnapshotCandidate, error) {
		if providerCalls.Add(1) == 1 {
			close(firstEntered)
			<-releaseFirst
		}
		return candidate, nil
	})
	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	secondQueued := make(chan struct{})
	secondResult := make(chan error, 1)
	secondCancelled := make(chan struct{})
	executor := RefreshExecuteFunc(func(ctx context.Context, operation RefreshOperation) (SnapshotCandidate, error) {
		firstResult := make(chan SnapshotCandidate, 1)
		firstError := make(chan error, 1)
		go func() {
			result, err := operation(ctx)
			firstResult <- result
			firstError <- err
		}()
		<-firstEntered
		go func() {
			close(secondQueued)
			_, err := operation(secondCtx)
			secondResult <- err
		}()
		if err := <-secondResult; !errors.Is(err, context.Canceled) {
			return SnapshotCandidate{}, fmt.Errorf("queued operation cancellation: %w", err)
		}
		close(secondCancelled)
		result := <-firstResult
		return result, <-firstError
	})
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.Executor = executor
	config.MaxProviderLoads = 2
	config.MaxConcurrentProviderLoads = 1
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	bootstrapDone := make(chan error, 1)
	go func() {
		_, bootstrapErr := fleet.Bootstrap(context.Background())
		bootstrapDone <- bootstrapErr
	}()
	<-secondQueued
	time.Sleep(20 * time.Millisecond)
	cancelSecond()
	<-secondCancelled
	close(releaseFirst)
	if err := <-bootstrapDone; err != nil {
		t.Fatal(err)
	}
	if providerCalls.Load() != 1 {
		t.Fatalf("cancelled queued attempt reached provider %d times", providerCalls.Load())
	}
}

func TestFleetBootstrapDefinesEmptyStaleMalformedAndUnavailableSources(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	t.Run("stale primary requires explicit startup authority", func(t *testing.T) {
		loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", true),
			Revision: "stale", Provenance: "durable", SourceTime: now.Add(-3 * time.Minute),
		}}}
		config := validFleetConfig(&fleetTestClock{now: now}, loader)
		config.AllowStaleBootstrap = false
		fleet, err := NewFleet(config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Bootstrap(context.Background()); !errors.Is(err, ErrSnapshotStale) {
			t.Fatalf("stale bootstrap = %v", err)
		}
	})

	t.Run("empty provider can be an explicit ready state", func(t *testing.T) {
		provider := NewMemoryProvider(DefaultLimits())
		empty, err := provider.Snapshot(context.Background(), "tenant-a")
		if err != nil {
			t.Fatal(err)
		}
		loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
			Snapshot: empty, Revision: "empty", Provenance: "provider", SourceTime: now,
		}}}
		config := validFleetConfig(&fleetTestClock{now: now}, loader)
		config.AllowEmptyBootstrap = true
		fleet, err := NewFleet(config)
		if err != nil {
			t.Fatal(err)
		}
		if active, err := fleet.Bootstrap(context.Background()); err != nil || active.Revision != "empty" {
			t.Fatalf("empty bootstrap = %#v, %v", active, err)
		}
	})

	t.Run("empty provider is rejected without explicit authority", func(t *testing.T) {
		provider := NewMemoryProvider(DefaultLimits())
		empty, err := provider.Snapshot(context.Background(), "tenant-a")
		if err != nil {
			t.Fatal(err)
		}
		loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
			Snapshot: empty, Revision: "empty", Provenance: "provider", SourceTime: now,
		}}}
		fleet, err := NewFleet(validFleetConfig(&fleetTestClock{now: now}, loader))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Bootstrap(context.Background()); !errors.Is(err, ErrNoUsableSnapshot) {
			t.Fatalf("empty bootstrap = %v", err)
		}
	})

	t.Run("internally inconsistent snapshot is revalidated", func(t *testing.T) {
		inconsistent := Snapshot{
			definitions: map[string]Definition{"broken": {Key: "broken"}},
			groups:      map[string]GroupDefinition{}, limits: DefaultLimits(), tenant: "tenant-a",
		}
		loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
			Snapshot: inconsistent, Revision: "broken", Provenance: "provider", SourceTime: now,
		}}}
		fleet, err := NewFleet(validFleetConfig(&fleetTestClock{now: now}, loader))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Bootstrap(context.Background()); !errors.Is(err, ErrMalformedSnapshot) {
			t.Fatalf("inconsistent bootstrap = %v", err)
		}
	})

	t.Run("malformed primary falls back to valid cache", func(t *testing.T) {
		loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
			Snapshot: Snapshot{}, Revision: "bad", Provenance: "provider", SourceTime: now,
		}}}
		cache := &fleetTestCache{found: true, candidate: SnapshotCandidate{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", false),
			Revision: "cached", Provenance: "cache", SourceTime: now,
		}}
		config := validFleetConfig(&fleetTestClock{now: now}, loader)
		config.Cache = cache
		fleet, err := NewFleet(config)
		if err != nil {
			t.Fatal(err)
		}
		if active, err := fleet.Bootstrap(context.Background()); err != nil || active.Revision != "cached" ||
			fleet.Status().LastRefreshFailure != FleetFailureInvalidSnapshot {
			t.Fatalf("malformed fallback = %#v, %v, %#v", active, err, fleet.Status())
		}
	})

	t.Run("unavailable provider cannot revive cache beyond maximum staleness", func(t *testing.T) {
		providerErr := errors.New("provider unavailable")
		loader := &fleetTestLoader{errors: []error{providerErr}}
		cache := &fleetTestCache{found: true, candidate: SnapshotCandidate{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", false),
			Revision: "expired", Provenance: "cache", SourceTime: now.Add(-11 * time.Minute),
		}}
		config := validFleetConfig(&fleetTestClock{now: now}, loader)
		config.Cache = cache
		fleet, err := NewFleet(config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Bootstrap(context.Background()); !errors.Is(err, providerErr) || !errors.Is(err, ErrSnapshotStale) || !errors.Is(err, ErrNoUsableSnapshot) {
			t.Fatalf("unavailable stale bootstrap = %v", err)
		}
		if status := fleet.Status(); status.LastRefreshFailure != FleetFailureProvider || status.LastCacheFailure != FleetFailureStaleSnapshot {
			t.Fatalf("provider and stale cache failures were conflated: %#v", status)
		}
	})

	t.Run("unavailable cache remains independently observable", func(t *testing.T) {
		providerErr := errors.New("provider unavailable")
		cacheErr := errors.New("cache unavailable")
		config := validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{errors: []error{providerErr}})
		config.Cache = &fleetTestCache{loadErr: cacheErr}
		fleet, err := NewFleet(config)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Bootstrap(context.Background()); !errors.Is(err, providerErr) || !errors.Is(err, cacheErr) {
			t.Fatalf("unavailable provider and cache = %v", err)
		}
		if status := fleet.Status(); status.LastCacheFailure != FleetFailureCacheLoad {
			t.Fatalf("cache load failure not observable: %#v", status)
		}
	})
}

func TestFleetDegradedPoliciesPreserveSecurityAndStalenessBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	provider := NewMemoryProvider(DefaultLimits())
	for _, definition := range []Definition{
		{Key: "secure", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive},
		{Key: "closed", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive},
	} {
		if _, err := provider.Create(context.Background(), "tenant-a", definition, "test"); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := provider.Snapshot(context.Background(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: snapshot, Revision: "41", Provenance: "provider", SourceTime: now,
	}}}
	config := validFleetConfig(clock, loader)
	config.Policies = map[string]FlagPolicy{
		"secure":  {Mode: DegradedLastKnownGood, MaxStaleness: 5 * time.Minute, SecuritySensitive: true},
		"closed":  {Mode: DegradedFailClosed, SecuritySensitive: true},
		"wrong":   {Mode: DegradedDefault, Default: StringValue("not-a-boolean")},
		"open":    {Mode: DegradedFailOpen},
		"invalid": {Mode: DegradedDefault, Default: StructuredValue(json.RawMessage("{"))},
	}
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Set(now.Add(3 * time.Minute))
	secure, err := fleet.Boolean("secure", Context{Tenant: "tenant-a"})
	if err != nil || !secure.Value || secure.Reason != ReasonDegradedLastKnownGood {
		t.Fatalf("security last-known-good = %#v, %v", secure, err)
	}
	clock.Set(now.Add(-time.Minute))
	secure, err = fleet.Boolean("secure", Context{Tenant: "tenant-a"})
	if err != nil || !secure.Value || secure.Reason != ReasonDegradedLastKnownGood || fleet.Status().Age != 3*time.Minute {
		t.Fatalf("clock rollback revived freshness: %#v, %v, %#v", secure, err, fleet.Status())
	}
	clock.Set(now.Add(3 * time.Minute))
	if _, err := fleet.Boolean("closed", Context{Tenant: "tenant-a"}); !errors.Is(err, ErrSnapshotStale) {
		t.Fatalf("fail-closed stale evaluation = %v", err)
	}
	if _, err := fleet.Boolean("wrong", Context{Tenant: "tenant-a"}); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("wrong typed default = %v", err)
	}
	if _, err := fleet.String("open", Context{Tenant: "tenant-a"}); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("non-boolean fail-open = %v", err)
	}
	if _, err := fleet.Structured("invalid", Context{Tenant: "tenant-a"}); !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("invalid degraded default = %v", err)
	}
	if _, err := fleet.String("secure", Context{Tenant: "tenant-a"}); err == nil {
		t.Fatalf("last-known-good snapshot type error = %v", err)
	}
	if _, err := fleet.Boolean("secure", Context{Tenant: "tenant-b"}); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("cross-tenant degraded evaluation = %v", err)
	}
	clock.Set(now.Add(5 * time.Minute))
	if detail, err := fleet.Boolean("secure", Context{Tenant: "tenant-a"}); err != nil || !detail.Value {
		t.Fatalf("last-known-good maximum staleness boundary = %#v, %v", detail, err)
	}
	clock.Set(now.Add(5*time.Minute + time.Nanosecond))
	if _, err := fleet.Boolean("secure", Context{Tenant: "tenant-a"}); !errors.Is(err, ErrSnapshotStale) {
		t.Fatalf("expired last-known-good = %v", err)
	}
}

func TestFleetFreshnessAndRefreshBoundsAreInclusiveAtTheirLimits(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	config := validFleetConfig(clock, &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "fresh-boundary", Provenance: "provider", SourceTime: now.Add(-2 * time.Minute),
	}}})
	config.AllowStaleBootstrap = false
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if active, err := fleet.Bootstrap(context.Background()); err != nil || active.Revision != "fresh-boundary" {
		t.Fatalf("freshness boundary bootstrap = %#v, %v", active, err)
	}

	staleConfig := validFleetConfig(clock, &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "stale-boundary", Provenance: "cache", SourceTime: now.Add(-10 * time.Minute),
	}}})
	staleFleet, err := NewFleet(staleConfig)
	if err != nil {
		t.Fatal(err)
	}
	if active, err := staleFleet.Bootstrap(context.Background()); err != nil || active.Revision != "stale-boundary" {
		t.Fatalf("maximum staleness boundary bootstrap = %#v, %v", active, err)
	}

	refreshLoader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", false), Revision: "1", Provenance: "provider", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true), Revision: "2", Provenance: "provider", SourceTime: now.Add(time.Second)},
	}}
	refreshFleet, err := NewFleet(validFleetConfig(clock, refreshLoader))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := refreshFleet.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Set(now.Add(time.Second))
	if result, err := refreshFleet.Refresh(context.Background()); err != nil || result.Active.Revision != "2" {
		t.Fatalf("minimum refresh boundary = %#v, %v", result, err)
	}
}

func TestFleetRejectsSourceTimeBeyondExplicitFutureSkew(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	strict := validFleetConfig(clock, &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "strict", Provenance: "provider", SourceTime: now,
	}}})
	strict.MaxFutureSkew = 0
	strictFleet, err := NewFleet(strict)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strictFleet.Bootstrap(context.Background()); err != nil {
		t.Fatalf("strict future skew rejected current source time: %v", err)
	}

	config := validFleetConfig(clock, &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "future", Provenance: "provider", SourceTime: now.Add(5*time.Second + time.Nanosecond),
	}}})
	config.MaxFutureSkew = 5 * time.Second
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); !errors.Is(err, ErrSnapshotFuture) {
		t.Fatalf("future source time = %v", err)
	}

	config.Loader = &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "future-boundary", Provenance: "provider", SourceTime: now.Add(5 * time.Second),
	}}}
	fleet, err = NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if active, err := fleet.Bootstrap(context.Background()); err != nil || active.Revision != "future-boundary" {
		t.Fatalf("future skew boundary = %#v, %v", active, err)
	}
}

func TestFleetShutdownCancelsAndJoinsActiveProviderLoad(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	loader := &fleetShutdownLoader{
		first: SnapshotCandidate{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", true),
			Revision: "41", Provenance: "provider", SourceTime: now,
		},
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	sleeper := &fleetTestSleeper{delays: make(chan time.Duration, 1), release: make(chan struct{}, 1)}
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.Sleeper = sleeper
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-sleeper.delays
	sleeper.release <- struct{}{}
	<-loader.entered
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fleet.Shutdown(shutdownCtx); err != nil {
		t.Fatal(err)
	}
	if status := fleet.Status(); status.State != FleetStopped || status.LastRefreshFailure != FleetFailureNone || status.Refreshing {
		t.Fatalf("shutdown status = %#v", status)
	}
}

func TestFleetShutdownReportsLoaderThatIgnoresCancellation(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	loader := &fleetShutdownLoader{
		first: SnapshotCandidate{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", true),
			Revision: "41", Provenance: "provider", SourceTime: now,
		},
		entered: make(chan struct{}), release: make(chan struct{}), ignoreCancel: true,
	}
	sleeper := &fleetTestSleeper{delays: make(chan time.Duration, 1), release: make(chan struct{}, 1)}
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.Sleeper = sleeper
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-sleeper.delays
	sleeper.release <- struct{}{}
	<-loader.entered
	deadlineCtx, cancelDeadline := context.WithCancel(context.Background())
	cancelDeadline()
	if err := fleet.Shutdown(deadlineCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown with stuck loader = %v", err)
	}
	close(loader.release)
	joinCtx, cancelJoin := context.WithTimeout(context.Background(), time.Second)
	defer cancelJoin()
	if err := fleet.Shutdown(joinCtx); err != nil {
		t.Fatal(err)
	}
}

func TestFleetCacheStoreFailureDoesNotUndoValidatedActivation(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", false), Revision: "41", Provenance: "provider", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", true), Revision: "42", Provenance: "provider", SourceTime: now.Add(2 * time.Second)},
	}}
	cacheErr := errors.New("cache unavailable")
	cache := &fleetTestCache{storeErr: cacheErr}
	config := validFleetConfig(clock, loader)
	config.Cache = cache
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if active, err := fleet.Bootstrap(context.Background()); err != nil || active.Revision != "41" {
		t.Fatalf("bootstrap activation = %#v, %v", active, err)
	}
	if fleet.Status().LastCacheFailure != FleetFailureCacheStore || fleet.Status().LastRefreshFailure != FleetFailureNone {
		t.Fatalf("bootstrap cache error status = %#v", fleet.Status())
	}
	clock.Set(now.Add(2 * time.Second))
	result, err := fleet.Refresh(context.Background())
	if err != nil || result.Active.Revision != "42" {
		t.Fatalf("refresh activation = %#v, %v", result, err)
	}
	detail, err := fleet.Boolean("checkout", Context{Tenant: "tenant-a"})
	if err != nil || !detail.Value {
		t.Fatalf("activated evaluation = %#v, %v", detail, err)
	}
	if fleet.Status().LastCacheFailure != FleetFailureCacheStore {
		t.Fatalf("refresh cache error status = %#v", fleet.Status())
	}
	cache.storeErr = nil
	loader.candidates = append(loader.candidates, SnapshotCandidate{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", false),
		Revision: "43", Provenance: "provider", SourceTime: now.Add(4 * time.Second),
	})
	clock.Set(now.Add(4 * time.Second))
	if result, err := fleet.Refresh(context.Background()); err != nil || result.Active.Revision != "43" {
		t.Fatalf("cache recovery refresh = %#v, %v", result, err)
	}
	if fleet.Status().LastCacheFailure != FleetFailureNone {
		t.Fatalf("successful cache write did not clear status: %#v", fleet.Status())
	}
}

func TestFleetBoundsCacheOperationsWithDerivedDeadlines(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	cache := &fleetDeadlineCache{}
	loader := &fleetTestLoader{
		candidates: []SnapshotCandidate{{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
			Revision: "42", Provenance: "provider", SourceTime: now,
		}, {}},
		errors: []error{nil, errors.New("provider unavailable")},
	}
	config := validFleetConfig(&fleetTestClock{now: now}, loader)
	config.Cache = cache
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !cache.storeHadDeadline {
		t.Fatal("bootstrap cache store did not receive a bounded context")
	}
	if _, err := fleet.Bootstrap(context.Background()); err == nil {
		t.Fatal("unavailable provider and empty cache unexpectedly bootstrapped")
	}
	if !cache.loadHadDeadline {
		t.Fatal("bootstrap cache load did not receive a bounded context")
	}
}

func TestFleetShutdownCancelsAndJoinsRefreshCacheWork(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	cache := &fleetRefreshCancellationCache{entered: make(chan struct{})}
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", false), Revision: "41", Provenance: "provider", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true), Revision: "42", Provenance: "provider", SourceTime: now.Add(2 * time.Second)},
	}}
	config := validFleetConfig(clock, loader)
	config.Cache = cache
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Set(now.Add(2 * time.Second))
	refreshDone := make(chan error, 1)
	go func() {
		_, refreshErr := fleet.Refresh(context.Background())
		refreshDone <- refreshErr
	}()
	<-cache.entered
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := fleet.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown did not cancel cache work: %v", err)
	}
	if err := <-refreshDone; err != nil {
		t.Fatalf("validated refresh was undone by cache cancellation: %v", err)
	}
}

func TestFleetTypedEvaluationSurfacesSnapshotErrors(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "42", Provenance: "provider", SourceTime: now,
	}}}
	fleet, err := NewFleet(validFleetConfig(&fleetTestClock{now: now}, loader))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	context := Context{Tenant: "tenant-a"}
	checks := map[string]func() error{
		"integer":    func() error { _, err := fleet.Integer("flag", context); return err },
		"float":      func() error { _, err := fleet.Float("flag", context); return err },
		"decimal":    func() error { _, err := fleet.Decimal("flag", context); return err },
		"structured": func() error { _, err := fleet.Structured("flag", context); return err },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); err == nil {
				t.Fatalf("typed evaluation error = %v", err)
			}
		})
	}
	invalidContext := Context{Tenant: "tenant-a", Subject: string(make([]byte, DefaultLimits().MaxContextValueBytes+1))}
	if _, err := fleet.Boolean("flag", invalidContext); !errors.Is(err, ErrContextLimit) {
		t.Fatalf("invalid context = %v", err)
	}
}
