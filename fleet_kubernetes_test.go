package featureflags

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

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
