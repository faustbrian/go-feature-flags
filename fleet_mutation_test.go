package featureflags

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFleetAcceptsExactIdentityTimeResourceAndPolicyBounds(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	provider := NewMemoryProvider(DefaultLimits())
	identity := strings.Repeat("i", maxFleetIdentityLength)
	if _, err := NewProviderSnapshotLoader(provider, clock, identity); err != nil {
		t.Fatalf("NewProviderSnapshotLoader(exact provenance bound) error = %v", err)
	}
	if delay, err := (DeterministicFleetJitter{}).Delay(identity, 1, 0); err != nil || delay != 0 {
		t.Fatalf("Delay(exact replica bound) = (%s, %v)", delay, err)
	}

	config := validFleetConfig(clock, &fleetTestLoader{})
	config.Tenant = identity
	config.ReplicaID = identity
	config.RefreshInterval = time.Nanosecond
	config.MinRefreshInterval = time.Nanosecond
	config.MaxRefreshJitter = 0
	config.LoadTimeout = time.Nanosecond
	config.FreshFor = time.Nanosecond
	config.MaxStaleness = time.Nanosecond
	config.MaxFutureSkew = 0
	config.ConvergenceWindow = 2 * time.Nanosecond
	config.MaxWaiters = maxFleetWaiters
	config.MaxProviderLoads = maxFleetProviderLoads
	config.MaxConcurrentProviderLoads = maxFleetConcurrentProviderLoads
	config.MaxInvalidationStreams = maxFleetInvalidationStreams
	config.MaxPolicies = DefaultLimits().MaxFeatures
	config.Policies = map[string]FlagPolicy{
		strings.Repeat("k", DefaultLimits().MaxKeyBytes): {
			Mode: DegradedLastKnownGood, MaxStaleness: time.Nanosecond,
		},
	}
	if _, err := NewFleet(config); err != nil {
		t.Fatalf("NewFleet(exact bounds) error = %v", err)
	}

	exactPolicies := validFleetConfig(clock, &fleetTestLoader{})
	exactPolicies.MaxPolicies = 2
	exactPolicies.Policies = map[string]FlagPolicy{
		"a": {Mode: DegradedFailClosed},
		"b": {Mode: DegradedLastKnownGood, MaxStaleness: exactPolicies.MaxStaleness},
	}
	if _, err := NewFleet(exactPolicies); err != nil {
		t.Fatalf("NewFleet(exact policy count) error = %v", err)
	}

	shortConvergence := config
	shortConvergence.ConvergenceWindow--
	if _, err := NewFleet(shortConvergence); err == nil {
		t.Fatal("NewFleet(convergence omitting exact load timeout) succeeded")
	}
}

func TestFleetCandidateMetadataFieldsAreValidatedIndependently(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	base := SnapshotCandidate{
		Snapshot:   fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision:   strings.Repeat("r", maxFleetIdentityLength),
		Provenance: strings.Repeat("p", maxFleetIdentityLength),
		SourceTime: now,
	}
	config := validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{candidates: []SnapshotCandidate{base}})
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap(exact metadata bounds) error = %v", err)
	}

	invalid := map[string]func(*SnapshotCandidate){
		"empty revision":   func(candidate *SnapshotCandidate) { candidate.Revision = "" },
		"long revision":    func(candidate *SnapshotCandidate) { candidate.Revision += "r" },
		"empty provenance": func(candidate *SnapshotCandidate) { candidate.Provenance = "" },
		"long provenance":  func(candidate *SnapshotCandidate) { candidate.Provenance += "p" },
		"zero source time": func(candidate *SnapshotCandidate) { candidate.SourceTime = time.Time{} },
		"nil definitions":  func(candidate *SnapshotCandidate) { candidate.Snapshot.definitions = nil },
		"nil groups":       func(candidate *SnapshotCandidate) { candidate.Snapshot.groups = nil },
		"wrong snapshot tenant": func(candidate *SnapshotCandidate) {
			candidate.Snapshot.tenant = "tenant-b"
		},
	}
	for name, mutate := range invalid {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			candidateConfig := validFleetConfig(
				&fleetTestClock{now: now},
				&fleetTestLoader{candidates: []SnapshotCandidate{candidate}},
			)
			candidateFleet, err := NewFleet(candidateConfig)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := candidateFleet.Bootstrap(context.Background()); !errors.Is(err, ErrMalformedSnapshot) {
				t.Fatalf("Bootstrap() error = %v", err)
			}
		})
	}
}

func TestFleetFreshnessBoundaryControlsActivationStatusEvaluationAndStop(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	config := validFleetConfig(clock, &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: "exact", Provenance: "provider", SourceTime: now.Add(-2 * time.Minute),
	}}})
	config.AllowStaleBootstrap = false
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := fleet.Status(); status.State != FleetReady || status.Age != config.FreshFor {
		t.Fatalf("exact freshness status = %#v", status)
	}
	if detail, err := fleet.Boolean("flag", Context{Tenant: "tenant-a"}); err != nil || !detail.Value || detail.Reason != ReasonDefault {
		t.Fatalf("exact freshness evaluation = (%#v, %v)", detail, err)
	}
	clock.Set(now.Add(time.Nanosecond))
	if status := fleet.Status(); status.State != FleetDegraded {
		t.Fatalf("expired freshness status = %#v", status)
	}
	if _, err := fleet.Boolean("flag", Context{Tenant: "tenant-a"}); !errors.Is(err, ErrSnapshotStale) {
		t.Fatalf("unconfigured degraded evaluation error = %v", err)
	}
	if err := fleet.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if status := fleet.Status(); status.State != FleetStopped {
		t.Fatalf("stale stopped status = %#v", status)
	}
}

func TestFleetRefreshFailureBoundariesAndSameRevisionDisposition(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	providerErr := errors.New("provider unavailable")
	newFailureFleet := func(t *testing.T, age time.Duration) *Fleet {
		t.Helper()
		clock := &fleetTestClock{now: now}
		loader := &fleetTestLoader{
			candidates: []SnapshotCandidate{{
				Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
				Revision: "42", Provenance: "provider", SourceTime: now,
			}},
			errors: []error{nil, providerErr},
		}
		fleet, err := NewFleet(validFleetConfig(clock, loader))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fleet.Bootstrap(context.Background()); err != nil {
			t.Fatal(err)
		}
		clock.Set(now.Add(age))
		if _, err := fleet.Refresh(context.Background()); !errors.Is(err, providerErr) {
			t.Fatalf("Refresh() error = %v", err)
		}
		return fleet
	}
	if status := newFailureFleet(t, 2*time.Minute).Status(); status.State != FleetReady || status.LastRefreshFailure != FleetFailureProvider {
		t.Fatalf("exact freshness failure status = %#v", status)
	}
	if status := newFailureFleet(t, 2*time.Minute+time.Nanosecond).Status(); status.State != FleetDegraded || status.LastRefreshFailure != FleetFailureProvider {
		t.Fatalf("stale failure status = %#v", status)
	}

	cancelledFleet, err := NewFleet(validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{}))
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cancelledFleet.Refresh(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Refresh(cancelled) error = %v", err)
	}
	if status := cancelledFleet.Status(); status.LastRefreshFailure != FleetFailureCancelled {
		t.Fatalf("cancelled refresh status = %#v", status)
	}

	clock := &fleetTestClock{now: now}
	loader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", false), Revision: "same", Provenance: "provider", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", false), Revision: "same", Provenance: "provider", SourceTime: now.Add(time.Second)},
	}}
	sameFleet, err := NewFleet(validFleetConfig(clock, loader))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sameFleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Set(now.Add(time.Second))
	result, err := sameFleet.Refresh(context.Background())
	defaultValue, validDefault := result.Active.Snapshot.definitions["flag"].Default.Boolean()
	if err != nil || result.Disposition != RefreshUnchanged || !validDefault || defaultValue {
		t.Fatalf("same revision refresh = (%#v, %v)", result, err)
	}

	differentClock := &fleetTestClock{now: now}
	differentLoader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", false), Revision: "old", Provenance: "provider", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true), Revision: "new", Provenance: "provider", SourceTime: now.Add(time.Second)},
	}}
	differentFleet, err := NewFleet(validFleetConfig(differentClock, differentLoader))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := differentFleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	differentClock.Set(now.Add(time.Second))
	if result, err := differentFleet.Refresh(context.Background()); err != nil || result.Disposition != RefreshActivated || result.Active.Revision != "new" {
		t.Fatalf("different revision refresh = (%#v, %v)", result, err)
	}
}

func TestFleetRefreshLoopAcceptsInclusiveJitterBounds(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	for name, delay := range map[string]time.Duration{"zero": 0, "maximum": 10 * time.Second} {
		t.Run(name, func(t *testing.T) {
			sleeper := &fleetOneRefreshSleeper{entered: make(chan struct{}), release: make(chan struct{})}
			config := validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{candidates: []SnapshotCandidate{{
				Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
				Revision: "42", Provenance: "provider", SourceTime: now,
			}}})
			config.Jitter = invalidFleetJitter{delay: delay}
			config.Sleeper = sleeper
			fleet, err := NewFleet(config)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fleet.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case <-sleeper.entered:
			case <-time.After(time.Second):
				t.Fatal("inclusive jitter bound stopped before scheduling")
			}
			if status := fleet.Status(); status.State == FleetStopped || status.LastRefreshFailure == FleetFailureScheduler {
				t.Fatalf("inclusive jitter status = %#v", status)
			}
			if err := fleet.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFleetStatusConvergenceTruthTable(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	fleet, err := NewFleet(validFleetConfig(clock, &fleetTestLoader{}))
	if err != nil {
		t.Fatal(err)
	}
	if fleet.Status().ConvergenceBreached {
		t.Fatal("zero convergence deadline breached")
	}
	fleet.mu.Lock()
	fleet.convergence = now
	fleet.convergenceRevision = "target"
	fleet.mu.Unlock()
	if fleet.Status().ConvergenceBreached {
		t.Fatal("exact convergence deadline breached")
	}
	clock.Set(now.Add(time.Nanosecond))
	if !fleet.Status().ConvergenceBreached {
		t.Fatal("expired convergence without active snapshot did not breach")
	}
	fleet.mu.Lock()
	fleet.hasActive = true
	fleet.active.Revision = "other"
	fleet.mu.Unlock()
	if !fleet.Status().ConvergenceBreached {
		t.Fatal("expired convergence with wrong revision did not breach")
	}
	fleet.mu.Lock()
	fleet.active.Revision = "target"
	fleet.mu.Unlock()
	if fleet.Status().ConvergenceBreached {
		t.Fatal("converged revision reported a breach")
	}
}

func TestFleetInvalidationExactBoundsAndEveryCausalHistory(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	exactRevision := strings.Repeat("r", maxFleetIdentityLength)
	exactStream := strings.Repeat("s", maxFleetIdentityLength)
	exactFleet, err := NewFleet(validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true),
		Revision: exactRevision, Provenance: "provider", SourceTime: now,
	}}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exactFleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result, err := exactFleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: exactStream, Sequence: 1, Revision: exactRevision, ObservedAt: now,
	}); err != nil || result.Disposition != InvalidationCurrent {
		t.Fatalf("exact invalidation bounds = (%#v, %v)", result, err)
	}

	historyLoader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true), Revision: "active", Provenance: "provider", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true), Revision: "active", Provenance: "provider", SourceTime: now},
	}}
	historyFleet, err := NewFleet(validFleetConfig(&fleetTestClock{now: now}, historyLoader))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := historyFleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, sequence := range []uint64{1, 2} {
		result, err := historyFleet.Invalidate(context.Background(), Invalidation{
			Tenant: "tenant-a", Stream: "history", Sequence: sequence, Revision: "active", ObservedAt: now,
		})
		if err != nil || result.Gap || result.Disposition != InvalidationCurrent {
			t.Fatalf("contiguous invalidation %d = (%#v, %v)", sequence, result, err)
		}
	}
	gap, err := historyFleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "history", Sequence: 4, Revision: "active", ObservedAt: now,
	})
	if err != nil || !gap.Gap || gap.Disposition != InvalidationRefreshed {
		t.Fatalf("gapped invalidation = (%#v, %v)", gap, err)
	}
	duplicate, err := historyFleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "history", Sequence: 4, Revision: "active", ObservedAt: now,
	})
	if err != nil || duplicate.Disposition != InvalidationDuplicate {
		t.Fatalf("duplicate invalidation = (%#v, %v)", duplicate, err)
	}
	reordered, err := historyFleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "history", Sequence: 3, Revision: "active", ObservedAt: now,
	})
	if err != nil || reordered.Disposition != InvalidationReordered {
		t.Fatalf("reordered invalidation = (%#v, %v)", reordered, err)
	}

	noActiveFleet, err := NewFleet(validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{candidates: []SnapshotCandidate{{
		Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true), Revision: "target", Provenance: "provider", SourceTime: now,
	}}}))
	if err != nil {
		t.Fatal(err)
	}
	if result, err := noActiveFleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "stream", Sequence: 1, Revision: "target", ObservedAt: now,
	}); err != nil || result.Disposition != InvalidationRefreshed {
		t.Fatalf("no-active invalidation = (%#v, %v)", result, err)
	}

	mismatchLoader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", false), Revision: "old", Provenance: "provider", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true), Revision: "other", Provenance: "provider", SourceTime: now},
	}}
	mismatchFleet, err := NewFleet(validFleetConfig(&fleetTestClock{now: now}, mismatchLoader))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mismatchFleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result, err := mismatchFleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "stream", Sequence: 1, Revision: "target", ObservedAt: now,
	}); err != nil || result.Disposition != InvalidationPending || result.ActiveRevision != "other" {
		t.Fatalf("cross-revision mismatch = (%#v, %v)", result, err)
	}

	deferredClock := &fleetTestClock{now: now}
	deferredLoader := &fleetTestLoader{candidates: []SnapshotCandidate{
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", false), Revision: "old", Provenance: "provider", SourceTime: now},
		{Snapshot: fleetBooleanSnapshot(t, "tenant-a", "flag", true), Revision: "current", Provenance: "provider", SourceTime: now.Add(time.Second)},
	}}
	deferredFleet, err := NewFleet(validFleetConfig(deferredClock, deferredLoader))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deferredFleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	deferredClock.Set(now.Add(time.Second))
	if _, err := deferredFleet.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if result, err := deferredFleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "stream", Sequence: 1, Revision: "future", ObservedAt: now,
	}); err != nil || result.Disposition != InvalidationPending || deferredLoader.calls != 2 {
		t.Fatalf("deferred invalidation = (%#v, %v), calls=%d", result, err, deferredLoader.calls)
	}
}
