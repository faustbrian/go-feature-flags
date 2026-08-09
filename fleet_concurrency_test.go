package featureflags

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fleetTickingClock struct{ nanoseconds atomic.Int64 }

func newFleetTickingClock(start time.Time) *fleetTickingClock {
	clock := &fleetTickingClock{}
	clock.nanoseconds.Store(start.UnixNano())
	return clock
}

func (clock *fleetTickingClock) Now() time.Time {
	return time.Unix(0, clock.nanoseconds.Add(1)).UTC()
}

func TestFleetConcurrentEvaluationActivationAndInvalidation(t *testing.T) {
	start := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := newFleetTickingClock(start)
	falseSnapshot := fleetBooleanSnapshot(t, "tenant-a", "flag", false)
	trueSnapshot := fleetBooleanSnapshot(t, "tenant-a", "flag", true)
	var loads atomic.Uint64
	loader := SnapshotLoadFunc(func(context.Context, string) (SnapshotCandidate, error) {
		load := loads.Add(1)
		snapshot := falseSnapshot
		if load%2 == 0 {
			snapshot = trueSnapshot
		}
		return SnapshotCandidate{
			Snapshot: snapshot, Revision: fmt.Sprintf("revision-%d", load),
			Provenance: "race-provider", SourceTime: clock.Now(),
		}, nil
	})
	config := validFleetConfig(clock, loader)
	config.MinRefreshInterval = time.Nanosecond
	config.MaxWaiters = 64
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fleet.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}

	const iterations = 100
	errorsSeen := make(chan error, 16)
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range iterations {
				if _, err := fleet.Boolean("flag", Context{Tenant: "tenant-a"}); err != nil {
					errorsSeen <- err
					return
				}
			}
		}()
	}
	for range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range iterations {
				if _, err := fleet.Refresh(context.Background()); err != nil {
					errorsSeen <- err
					return
				}
			}
		}()
	}
	var sequence atomic.Uint64
	for range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range iterations {
				next := sequence.Add(1)
				_, err := fleet.Invalidate(context.Background(), Invalidation{
					Tenant: "tenant-a", Stream: "race", Sequence: next,
					Revision: fmt.Sprintf("revision-%d", next+1), ObservedAt: clock.Now(),
				})
				if err != nil {
					errorsSeen <- err
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
	if err := fleet.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
