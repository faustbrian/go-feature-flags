package featureflags

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestFleetConfigurationRejectsEveryUnboundedOrUnsafeShape(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	base := validFleetConfig(&fleetTestClock{now: now}, &fleetTestLoader{})
	tests := map[string]func(*FleetConfig){
		"empty tenant":              func(config *FleetConfig) { config.Tenant = "" },
		"long tenant":               func(config *FleetConfig) { config.Tenant = strings.Repeat("t", maxFleetIdentityLength+1) },
		"empty replica":             func(config *FleetConfig) { config.ReplicaID = "" },
		"long replica":              func(config *FleetConfig) { config.ReplicaID = strings.Repeat("r", maxFleetIdentityLength+1) },
		"nil loader":                func(config *FleetConfig) { config.Loader = nil },
		"nil clock":                 func(config *FleetConfig) { config.Clock = nil },
		"zero refresh interval":     func(config *FleetConfig) { config.RefreshInterval = 0 },
		"zero minimum interval":     func(config *FleetConfig) { config.MinRefreshInterval = 0 },
		"minimum exceeds refresh":   func(config *FleetConfig) { config.MinRefreshInterval = 2 * config.RefreshInterval },
		"negative jitter":           func(config *FleetConfig) { config.MaxRefreshJitter = -1 },
		"negative future skew":      func(config *FleetConfig) { config.MaxFutureSkew = -1 },
		"zero load timeout":         func(config *FleetConfig) { config.LoadTimeout = 0 },
		"zero freshness":            func(config *FleetConfig) { config.FreshFor = 0 },
		"staleness below freshness": func(config *FleetConfig) { config.MaxStaleness = config.FreshFor - 1 },
		"short convergence":         func(config *FleetConfig) { config.ConvergenceWindow = time.Second },
		"overflow refresh bound": func(config *FleetConfig) {
			config.RefreshInterval = time.Duration(math.MaxInt64)
			config.MinRefreshInterval = time.Nanosecond
			config.MaxRefreshJitter = time.Nanosecond
			config.LoadTimeout = time.Nanosecond
			config.FreshFor = time.Nanosecond
			config.MaxStaleness = time.Nanosecond
			config.ConvergenceWindow = time.Duration(math.MaxInt64)
		},
		"overflow load bound": func(config *FleetConfig) {
			config.RefreshInterval = time.Duration(math.MaxInt64 - 1)
			config.MinRefreshInterval = time.Nanosecond
			config.MaxRefreshJitter = time.Nanosecond
			config.LoadTimeout = time.Nanosecond
			config.FreshFor = time.Nanosecond
			config.MaxStaleness = time.Nanosecond
			config.ConvergenceWindow = time.Duration(math.MaxInt64)
		},
		"zero waiters":              func(config *FleetConfig) { config.MaxWaiters = 0 },
		"zero provider loads":       func(config *FleetConfig) { config.MaxProviderLoads = 0 },
		"zero provider concurrency": func(config *FleetConfig) { config.MaxConcurrentProviderLoads = 0 },
		"zero invalidation streams": func(config *FleetConfig) { config.MaxInvalidationStreams = 0 },
		"zero policies":             func(config *FleetConfig) { config.MaxPolicies = 0 },
		"excess waiters":            func(config *FleetConfig) { config.MaxWaiters = 65_537 },
		"excess provider loads":     func(config *FleetConfig) { config.MaxProviderLoads = 1_025 },
		"excess provider concurrency": func(config *FleetConfig) {
			config.MaxConcurrentProviderLoads = 1_025
		},
		"excess invalidation streams": func(config *FleetConfig) { config.MaxInvalidationStreams = 10_001 },
		"excess policy bound":         func(config *FleetConfig) { config.MaxPolicies = 10_001 },
		"too many policies": func(config *FleetConfig) {
			config.MaxPolicies = 1
			config.Policies = map[string]FlagPolicy{"a": {Mode: DegradedFailClosed}, "b": {Mode: DegradedFailClosed}}
		},
		"empty policy key": func(config *FleetConfig) { config.Policies = map[string]FlagPolicy{"": {Mode: DegradedFailClosed}} },
		"long policy key": func(config *FleetConfig) {
			config.Policies = map[string]FlagPolicy{strings.Repeat("k", DefaultLimits().MaxKeyBytes+1): {Mode: DegradedFailClosed}}
		},
		"unknown policy": func(config *FleetConfig) { config.Policies = map[string]FlagPolicy{"flag": {Mode: DegradedMode(255)}} },
		"unsafe security default": func(config *FleetConfig) {
			config.Policies = map[string]FlagPolicy{"flag": {Mode: DegradedDefault, SecuritySensitive: true}}
		},
		"excess policy staleness": func(config *FleetConfig) {
			config.Policies = map[string]FlagPolicy{"flag": {Mode: DegradedLastKnownGood, MaxStaleness: config.MaxStaleness + 1}}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			config := base
			mutate(&config)
			if _, err := NewFleet(config); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}

	maximumDuration := base
	maximumDuration.RefreshInterval = time.Duration(math.MaxInt64 - 2)
	maximumDuration.MinRefreshInterval = time.Nanosecond
	maximumDuration.MaxRefreshJitter = time.Nanosecond
	maximumDuration.LoadTimeout = time.Nanosecond
	maximumDuration.FreshFor = time.Nanosecond
	maximumDuration.MaxStaleness = time.Nanosecond
	maximumDuration.ConvergenceWindow = time.Duration(math.MaxInt64)
	if _, err := NewFleet(maximumDuration); err != nil {
		t.Fatalf("exact maximum duration bound = %v", err)
	}

	config := base
	config.Policies = map[string]FlagPolicy{"flag": {Mode: DegradedLastKnownGood}}
	fleet, err := NewFleet(config)
	if err != nil {
		t.Fatal(err)
	}
	if fleet.config.Policies["flag"].MaxStaleness != config.MaxStaleness {
		t.Fatalf("default last-known-good bound = %s", fleet.config.Policies["flag"].MaxStaleness)
	}
}

func TestFleetAdaptersAndSystemSchedulingValidateBoundaryFailures(t *testing.T) {
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	clock := &fleetTestClock{now: now}
	provider := NewMemoryProvider(DefaultLimits())
	for name, build := range map[string]func() (Provider, CacheClock, string){
		"provider":   func() (Provider, CacheClock, string) { return nil, clock, "provider" },
		"clock":      func() (Provider, CacheClock, string) { return provider, nil, "provider" },
		"provenance": func() (Provider, CacheClock, string) { return provider, clock, "" },
		"long": func() (Provider, CacheClock, string) {
			return provider, clock, strings.Repeat("p", maxFleetIdentityLength+1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			native, candidateClock, provenance := build()
			if _, err := NewProviderSnapshotLoader(native, candidateClock, provenance); err == nil {
				t.Fatal("expected provider loader error")
			}
		})
	}
	failing := &failingSnapshotProvider{Provider: provider, fail: true}
	loader, err := NewProviderSnapshotLoader(failing, clock, "provider")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loader.Load(context.Background(), "tenant-a"); !errors.Is(err, errProviderUnavailable) {
		t.Fatalf("provider loader error = %v", err)
	}

	grouped := NewMemoryProvider(DefaultLimits())
	group, err := grouped.CreateGroup(context.Background(), "tenant-a", GroupDefinition{Key: "operators"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	groupLoader, err := NewProviderSnapshotLoader(grouped, clock, "provider")
	if err != nil {
		t.Fatal(err)
	}
	firstGroupCandidate, err := groupLoader.Load(context.Background(), "tenant-a")
	if err != nil || len(firstGroupCandidate.Snapshot.groups) != 1 {
		t.Fatalf("grouped candidate = %#v, %v", firstGroupCandidate, err)
	}
	group.Metadata = map[string]string{"generation": "two"}
	if _, err := grouped.UpdateGroup(context.Background(), "tenant-a", group, group.Version, "test"); err != nil {
		t.Fatal(err)
	}
	secondGroupCandidate, err := groupLoader.Load(context.Background(), "tenant-a")
	if err != nil || secondGroupCandidate.Revision == firstGroupCandidate.Revision {
		t.Fatalf("group revision did not change: %q, %q, %v", firstGroupCandidate.Revision, secondGroupCandidate.Revision, err)
	}
	groupConfig := validFleetConfig(clock, &fleetTestLoader{candidates: []SnapshotCandidate{secondGroupCandidate}})
	groupConfig.AllowEmptyBootstrap = true
	groupFleet, err := NewFleet(groupConfig)
	if err != nil {
		t.Fatal(err)
	}
	if active, err := groupFleet.Bootstrap(context.Background()); err != nil || len(active.Snapshot.groups) != 1 {
		t.Fatalf("grouped fleet bootstrap = %#v, %v", active, err)
	}

	custom := NewMemoryProvider(DefaultLimits())
	if _, err := custom.Create(context.Background(), "tenant-a", Definition{
		Key: "custom", Type: TypeBoolean, Default: BooleanValue(false),
		Variants: map[string]Value{"on": BooleanValue(true)}, Lifecycle: LifecycleActive,
		Strategies: []Strategy{failingStrategy{}},
	}, "test"); err != nil {
		t.Fatal(err)
	}
	customLoader, err := NewProviderSnapshotLoader(custom, clock, "provider")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := customLoader.Load(context.Background(), "tenant-a"); !errors.Is(err, ErrUnsupportedStrategy) {
		t.Fatalf("custom strategy revision error = %v", err)
	}

	jitter := DeterministicFleetJitter{}
	for _, input := range []struct {
		replica  string
		sequence uint64
		maximum  time.Duration
	}{
		{"", 1, time.Second}, {strings.Repeat("r", maxFleetIdentityLength+1), 1, time.Second},
		{"pod", 0, time.Second}, {"pod", 1, -1},
	} {
		if _, err := jitter.Delay(input.replica, input.sequence, input.maximum); err == nil {
			t.Fatalf("jitter input accepted: %#v", input)
		}
	}
	if delay, err := jitter.Delay("pod", 1, 0); err != nil || delay != 0 {
		t.Fatalf("zero jitter = %s, %v", delay, err)
	}
	if delay, err := jitter.Delay("pod-a", 1, 10*time.Second); err != nil || delay != 3*time.Second+969573932*time.Nanosecond {
		t.Fatalf("pinned deterministic jitter = %s, %v", delay, err)
	}

	sleeper := systemFleetSleeper{}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleeper.Sleep(cancelled, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled sleep = %v", err)
	}
	if err := sleeper.Sleep(context.Background(), 0); err != nil {
		t.Fatalf("zero sleep = %v", err)
	}
}
