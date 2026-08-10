package featureflags

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fleetSharedOverloadExecutor struct {
	limit      int64
	active     atomic.Int64
	maximum    atomic.Int64
	rejected   atomic.Int64
	rejections chan<- struct{}
	release    <-chan struct{}
	err        error
}

func (executor *fleetSharedOverloadExecutor) Execute(
	ctx context.Context,
	operation RefreshOperation,
) (SnapshotCandidate, error) {
	for {
		active := executor.active.Load()
		if active >= executor.limit {
			executor.rejected.Add(1)
			executor.rejections <- struct{}{}
			return SnapshotCandidate{}, executor.err
		}
		if executor.active.CompareAndSwap(active, active+1) {
			for maximum := executor.maximum.Load(); active+1 > maximum; maximum = executor.maximum.Load() {
				if executor.maximum.CompareAndSwap(maximum, active+1) {
					break
				}
			}
			break
		}
	}
	defer executor.active.Add(-1)
	select {
	case <-executor.release:
		return operation(ctx)
	case <-ctx.Done():
		return SnapshotCandidate{}, ctx.Err()
	}
}

func TestFleetKubernetesColdScaleRollingSplitInvalidationLossAndHPA(t *testing.T) {
	const pods = 64
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	target := fleetBooleanSnapshot(t, "tenant-a", "checkout", true)
	jitter := DeterministicFleetJitter{}
	delays := make(map[time.Duration]struct{}, pods)
	fleets := make([]*Fleet, 0, pods)
	clocks := make([]*fleetTestClock, 0, pods)
	loaders := make([]*fleetTestLoader, 0, pods)
	revisionCounts := map[string]int{}

	for index := range pods {
		clock := &fleetTestClock{now: now}
		initialRevision := "rollout-old"
		initialValue := false
		if index%2 == 0 {
			initialRevision = "rollout-new"
			initialValue = true
		}
		loader := &fleetTestLoader{candidates: []SnapshotCandidate{
			{
				Snapshot: fleetBooleanSnapshot(t, "tenant-a", "checkout", initialValue),
				Revision: initialRevision, Provenance: "postgres", SourceTime: now,
			},
			{
				Snapshot: target, Revision: "rollout-target", Provenance: "postgres", SourceTime: now.Add(70 * time.Second),
			},
		}}
		config := validFleetConfig(clock, loader)
		config.ReplicaID = fmt.Sprintf("pod-%03d", index)
		fleet, err := NewFleet(config)
		if err != nil {
			t.Fatal(err)
		}
		active, err := fleet.Bootstrap(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		revisionCounts[active.Revision]++
		delay, err := jitter.Delay(config.ReplicaID, 1, config.MaxRefreshJitter)
		if err != nil || delay < 0 || delay > config.MaxRefreshJitter {
			t.Fatalf("pod %d jitter = %s, %v", index, delay, err)
		}
		delays[delay] = struct{}{}
		fleets = append(fleets, fleet)
		clocks = append(clocks, clock)
		loaders = append(loaders, loader)
	}
	if revisionCounts["rollout-old"] != pods/2 || revisionCounts["rollout-new"] != pods/2 {
		t.Fatalf("rolling split = %#v", revisionCounts)
	}
	if len(delays) < pods-2 {
		t.Fatalf("replica jitter synchronized too many pods: %d unique delays", len(delays))
	}

	for index, fleet := range fleets {
		delay, _ := jitter.Delay(fmt.Sprintf("pod-%03d", index), 1, 10*time.Second)
		refreshAt := now.Add(time.Minute + delay)
		clocks[index].Set(refreshAt)
		if index%2 == 0 {
			result, err := fleet.Invalidate(context.Background(), Invalidation{
				Tenant: "tenant-a", Stream: "valkey", Sequence: 1,
				Revision: "rollout-target", ObservedAt: refreshAt,
			})
			if err != nil || result.Disposition != InvalidationRefreshed {
				t.Fatalf("pod %d invalidation = %#v, %v", index, result, err)
			}
		} else {
			if _, err := fleet.Refresh(context.Background()); err != nil {
				t.Fatalf("pod %d periodic recovery = %v", index, err)
			}
		}
		active, _ := fleet.Current()
		if active.Revision != "rollout-target" || refreshAt.After(now.Add(2*time.Minute)) {
			t.Fatalf("pod %d convergence = %#v at %s", index, active, refreshAt)
		}
		if loaders[index].calls != 2 {
			t.Fatalf("pod %d provider amplification = %d", index, loaders[index].calls)
		}
	}
}

func TestFleetKubernetesProviderOutagePreservesSecurityPolicyAndReportsBreach(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	providerErr := errors.New("postgres failover")
	loader := &fleetTestLoader{
		candidates: []SnapshotCandidate{{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "secure", false),
			Revision: "41", Provenance: "postgres", SourceTime: now,
		}},
		errors: []error{nil, providerErr},
	}
	config := validFleetConfig(clock, loader)
	config.Policies = map[string]FlagPolicy{
		"secure": {Mode: DegradedLastKnownGood, MaxStaleness: 5 * time.Minute, SecuritySensitive: true},
	}
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Set(now.Add(3 * time.Minute))
	result, err := fleet.Invalidate(context.Background(), Invalidation{
		Tenant: "tenant-a", Stream: "valkey", Sequence: 2,
		Revision: "42", ObservedAt: now,
	})
	if !errors.Is(err, providerErr) || result.Disposition != InvalidationPending || !result.Gap {
		t.Fatalf("outage invalidation = %#v, %v", result, err)
	}
	detail, err := fleet.Boolean("secure", Context{Tenant: "tenant-a"})
	if err != nil || detail.Value || detail.Reason != ReasonDegradedLastKnownGood {
		t.Fatalf("security policy changed during outage: %#v, %v", detail, err)
	}
	clock.Set(now.Add(5*time.Minute + time.Nanosecond))
	if status := fleet.Status(); status.State != FleetDegraded || !status.ConvergenceBreached ||
		status.LastRefreshFailure != FleetFailureProvider {
		t.Fatalf("outage status = %#v", status)
	}
}

func TestFleetKubernetesConcurrentColdPodsBoundSharedOverloadAndRecover(t *testing.T) {
	const (
		pods          = 64
		providerLimit = 8
	)
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseProvider := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseProvider()
	overload := errors.New("shared provider overload")
	rejections := make(chan struct{}, pods)
	executor := &fleetSharedOverloadExecutor{
		limit: providerLimit, rejections: rejections, release: release, err: overload,
	}
	fleets := make([]*Fleet, pods)
	start := make(chan struct{})
	results := make(chan error, pods)
	var wait sync.WaitGroup
	for index := range pods {
		config := validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{candidates: []SnapshotCandidate{{
			Snapshot: fleetBooleanSnapshot(t, "tenant-a", "secure", false),
			Revision: "42", Provenance: "postgres", SourceTime: now,
		}}})
		config.ReplicaID = fmt.Sprintf("cold-pod-%03d", index)
		config.Executor = executor
		config.FailureClassifier = FleetFailureClassifyFunc(func(err error) FleetFailureCode {
			if errors.Is(err, overload) {
				return FleetFailureThrottled
			}
			return FleetFailureProvider
		})
		config.Policies = map[string]FlagPolicy{
			"secure": {Mode: DegradedFailClosed, SecuritySensitive: true},
		}
		fleet, err := NewFleet(config)
		if err != nil {
			t.Fatal(err)
		}
		fleets[index] = fleet
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := fleet.Bootstrap(context.Background())
			results <- err
		}()
	}
	close(start)
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for range pods - providerLimit {
		select {
		case <-rejections:
		case <-timer.C:
			t.Fatalf("cold-pod overload did not saturate deterministically: active=%d rejected=%d", executor.active.Load(), executor.rejected.Load())
		}
	}
	releaseProvider()
	wait.Wait()
	close(results)
	succeeded := 0
	rejected := 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, overload):
			rejected++
		default:
			t.Fatalf("cold-pod bootstrap error = %v", err)
		}
	}
	if succeeded != providerLimit || rejected != pods-providerLimit || executor.maximum.Load() > providerLimit {
		t.Fatalf("cold-pod bounds: succeeded=%d rejected=%d maximum=%d", succeeded, rejected, executor.maximum.Load())
	}

	for _, fleet := range fleets {
		if _, active := fleet.Current(); active {
			continue
		}
		status := fleet.Status()
		if status.LastRefreshFailure != FleetFailureThrottled || status.ProviderLoads != 0 {
			t.Fatalf("overload classification = %#v", status)
		}
		if _, err := fleet.Boolean("secure", Context{Tenant: "tenant-a"}); !errors.Is(err, ErrSnapshotStale) {
			t.Fatalf("cold security fallback = %v", err)
		}
		if _, err := fleet.Bootstrap(context.Background()); err != nil {
			t.Fatalf("cold-pod recovery = %v", err)
		}
	}
	for _, fleet := range fleets {
		active, ok := fleet.Current()
		if !ok || active.Revision != "42" || fleet.Status().ProviderLoads != 1 {
			t.Fatalf("recovered fleet = %#v, %#v", active, fleet.Status())
		}
	}
}
