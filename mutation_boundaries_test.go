package featureflags

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type maintenanceResultProvider struct {
	Provider
	applyResult   []Definition
	cleanupResult CleanupReport
	err           error
}

func (provider maintenanceResultProvider) ApplyScheduled(context.Context, string, time.Time, string) ([]Definition, error) {
	return provider.applyResult, provider.err
}

func (provider maintenanceResultProvider) Cleanup(context.Context, string, CleanupOptions) (CleanupReport, error) {
	return provider.cleanupResult, provider.err
}

func TestContextAcceptsEveryExactResourceBoundary(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxContextValueBytes = 2
	limits.MaxContextKeyBytes = 2
	limits.MaxStringBytes = 2
	limits.MaxAttributes = 2
	limits.MaxFacts = 2
	value := Context{
		Subject: "ss", Tenant: "tt", Environment: "ee",
		Attributes: map[string]string{"a1": "v1", "a2": "v2"},
		Facts:      map[string]Value{"f1": StringValue("v1"), "f2": StringValue("v2")},
	}
	if err := value.validate(limits); err != nil {
		t.Fatalf("validate(exact limits) error = %v", err)
	}
}

func TestDefinitionAcceptsEveryExactResourceBoundary(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxKeyBytes = 2
	limits.MaxStringBytes = 2
	limits.MaxVariants = 2
	limits.MaxMetadata = 2
	limits.MaxTags = 2
	limits.MaxStrategies = 2
	limits.MaxDependencies = 2
	limits.MaxGroups = 2
	definition := Definition{
		Key: "ff", Type: TypeBoolean, Default: BooleanValue(false), Owner: "oo",
		Variants: map[string]Value{"on": BooleanValue(true), "no": BooleanValue(false)},
		Metadata: map[string]string{"m1": "v1", "m2": "v2"},
		Tags:     []string{"t1", "t2"},
		Dependencies: []Dependency{
			{FeatureKey: "d1", RequiredVariant: "on"},
			{FeatureKey: "d2", RequiredVariant: "no"},
		},
		Groups: []string{"g1", "g2"},
		Strategies: []Strategy{
			ExactTargetStrategy{Name: "s1", Variant: "on"},
			ExactTargetStrategy{Name: "s2", Variant: "no"},
		},
	}
	if err := definition.Validate(limits); err != nil {
		t.Fatalf("Validate(exact limits) error = %v", err)
	}
}

func TestDefinitionRejectsEachMissingOrOversizedReferenceIndependently(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxKeyBytes = 2
	limits.MaxStringBytes = 2
	base := Definition{
		Key: "ff", Type: TypeBoolean, Default: BooleanValue(false),
		Variants: map[string]Value{"on": BooleanValue(true)},
	}
	tests := map[string]func() Definition{
		"missing dependency feature": func() Definition {
			value := base
			value.Dependencies = []Dependency{{RequiredVariant: "on"}}
			return value
		},
		"missing dependency variant": func() Definition {
			value := base
			value.Dependencies = []Dependency{{FeatureKey: "d1"}}
			return value
		},
		"long dependency feature": func() Definition {
			value := base
			value.Dependencies = []Dependency{{FeatureKey: "ddd", RequiredVariant: "on"}}
			return value
		},
		"long dependency variant": func() Definition {
			value := base
			value.Dependencies = []Dependency{{FeatureKey: "d1", RequiredVariant: "onn"}}
			return value
		},
		"long metadata key": func() Definition {
			value := base
			value.Metadata = map[string]string{"mmm": "v1"}
			return value
		},
		"long metadata value": func() Definition {
			value := base
			value.Metadata = map[string]string{"m1": "vvv"}
			return value
		},
	}
	for name, build := range tests {
		t.Run(name, func(t *testing.T) {
			if err := build().Validate(limits); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestStrategyValidationAcceptsExactBoundsAndRejectsMissingIdentityParts(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxAttributes = 2
	limits.MaxContextKeyBytes = 2
	limits.MaxContextValueBytes = 2
	limits.MaxTargetValues = 2
	exact := ExactTargetStrategy{
		Tenants: []string{"t1"}, Subjects: []string{"s1"},
		Attributes: map[string]string{"a1": "v1", "a2": "v2"},
	}
	if err := exact.ValidateStrategy(limits); err != nil {
		t.Fatalf("ValidateStrategy(exact limits) error = %v", err)
	}
	if err := (PercentageStrategy{Threshold: uint32(bucketPrecision)}).ValidateStrategy(limits); err != nil {
		t.Fatalf("ValidateStrategy(exact percentage) error = %v", err)
	}
	percentage := PercentageStrategy{Seed: "seed", Threshold: uint32(bucketPrecision)}
	for name, evaluationContext := range map[string]Context{
		"missing tenant":  {Subject: "subject"},
		"missing subject": {Tenant: "tenant"},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := percentage.EvaluateStrategy(StrategyInput{FeatureKey: "flag", Context: evaluationContext})
			if err != nil || result.Match || len(result.Diagnostics) != 1 {
				t.Fatalf("EvaluateStrategy() = (%#v, %v)", result, err)
			}
		})
	}
}

func TestTimeAndScheduleStrategiesAcceptAndEvaluateEveryBoundary(t *testing.T) {
	instant := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	for name, strategy := range map[string]TimeWindowStrategy{
		"only lower": {NotBefore: instant},
		"only upper": {NotAfter: instant},
	} {
		t.Run(name, func(t *testing.T) {
			if err := strategy.ValidateStrategy(DefaultLimits()); err != nil {
				t.Fatalf("ValidateStrategy() error = %v", err)
			}
		})
	}
	if err := (TimeWindowStrategy{NotBefore: instant, NotAfter: instant}).ValidateStrategy(DefaultLimits()); err == nil {
		t.Fatal("ValidateStrategy(equal bounds) succeeded")
	}
	limits := DefaultLimits()
	limits.MaxScheduleWindows = 2
	windows := []WeeklyWindow{
		{Weekday: time.Sunday, StartMinute: 0, EndMinute: 24 * 60},
		{Weekday: time.Saturday, StartMinute: 24*60 - 1, EndMinute: 0},
	}
	if err := (ScheduleStrategy{Location: "UTC", Windows: windows}).ValidateStrategy(limits); err != nil {
		t.Fatalf("ValidateStrategy(exact schedule bounds) error = %v", err)
	}
	for name, window := range map[string]WeeklyWindow{
		"negative start": {Weekday: time.Monday, StartMinute: -1, EndMinute: 1},
		"late start":     {Weekday: time.Monday, StartMinute: 24 * 60, EndMinute: 1},
		"negative end":   {Weekday: time.Monday, StartMinute: 1, EndMinute: -1},
		"late end":       {Weekday: time.Monday, StartMinute: 1, EndMinute: 24*60 + 1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := (ScheduleStrategy{Location: "UTC", Windows: []WeeklyWindow{window}}).ValidateStrategy(limits); err == nil {
				t.Fatal("ValidateStrategy() succeeded")
			}
		})
	}
	for _, test := range []struct {
		window  WeeklyWindow
		weekday time.Weekday
		minute  int
		want    bool
	}{
		{WeeklyWindow{Weekday: time.Monday, StartMinute: 10, EndMinute: 20}, time.Monday, 9, false},
		{WeeklyWindow{Weekday: time.Monday, StartMinute: 10, EndMinute: 20}, time.Monday, 10, true},
		{WeeklyWindow{Weekday: time.Monday, StartMinute: 10, EndMinute: 20}, time.Monday, 19, true},
		{WeeklyWindow{Weekday: time.Monday, StartMinute: 10, EndMinute: 20}, time.Monday, 20, false},
		{WeeklyWindow{Weekday: time.Monday, StartMinute: 10, EndMinute: 10}, time.Monday, 10, false},
		{WeeklyWindow{Weekday: time.Monday, StartMinute: 20, EndMinute: 10}, time.Monday, 19, false},
		{WeeklyWindow{Weekday: time.Monday, StartMinute: 20, EndMinute: 10}, time.Monday, 20, true},
		{WeeklyWindow{Weekday: time.Monday, StartMinute: 20, EndMinute: 10}, time.Tuesday, 9, true},
		{WeeklyWindow{Weekday: time.Monday, StartMinute: 20, EndMinute: 10}, time.Tuesday, 10, false},
	} {
		if got := weeklyWindowMatches(test.window, test.weekday, test.minute); got != test.want {
			t.Fatalf("weeklyWindowMatches(%#v, %s, %d) = %t, want %t", test.window, test.weekday, test.minute, got, test.want)
		}
	}
	schedule := ScheduleStrategy{
		Location: "UTC",
		Windows:  []WeeklyWindow{{Weekday: time.Monday, StartMinute: 9 * 60, EndMinute: 10 * 60}},
	}
	for _, test := range []struct {
		at   time.Time
		want bool
	}{
		{time.Date(2026, 8, 10, 9, 30, 0, 0, time.UTC), true},
		{time.Date(2026, 8, 10, 8, 30, 0, 0, time.UTC), false},
	} {
		result, err := schedule.EvaluateStrategy(StrategyInput{Context: Context{Time: test.at}})
		if err != nil || result.Match != test.want {
			t.Fatalf("EvaluateStrategy(%s) = (%#v, %v), want match %t", test.at, result, err, test.want)
		}
	}
}

func TestValueValidationAcceptsExactByteBounds(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxStringBytes = 2
	limits.MaxStructuredBytes = 2
	for name, value := range map[string]Value{
		"string":     StringValue("ab"),
		"decimal":    DecimalValue("12"),
		"structured": StructuredValue(json.RawMessage(`{}`)),
	} {
		t.Run(name, func(t *testing.T) {
			if err := value.validate(limits); err != nil {
				t.Fatalf("validate(exact bound) error = %v", err)
			}
		})
	}
}

func TestCleanupSeparatesPurgeCutoffAndAuditBoundaries(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxAuditEntries = 3
	provider := NewMemoryProvider(limits)
	if _, err := provider.Cleanup(t.Context(), "tenant", CleanupOptions{KeepAudit: 3}); err != nil {
		t.Fatalf("Cleanup(exact audit limit) error = %v", err)
	}
	created, err := provider.Create(t.Context(), "tenant", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
	}, "a")
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	if _, err := provider.StageUpdate(t.Context(), "tenant", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive,
	}, created.Version, cutoff, "a"); err != nil {
		t.Fatal(err)
	}
	report, err := provider.Cleanup(t.Context(), "tenant", CleanupOptions{DiscardStagesBefore: cutoff, KeepAudit: 3})
	if err != nil || report.DiscardedStages != 0 {
		t.Fatalf("Cleanup(exact cutoff) = (%#v, %v)", report, err)
	}
	report, err = provider.Cleanup(t.Context(), "tenant", CleanupOptions{DiscardStagesBefore: cutoff.Add(time.Nanosecond), KeepAudit: 3})
	if err != nil || report.DiscardedStages != 1 {
		t.Fatalf("Cleanup(after cutoff) = (%#v, %v)", report, err)
	}
	provider.staged["tenant"][99] = StagedChange{ID: 99, Definition: Definition{Key: "flag"}, ApplyAt: time.Time{}}
	provider.staged["tenant"][100] = StagedChange{ID: 100, Definition: Definition{Key: "flag"}, ApplyAt: cutoff}
	provider.staged["tenant"][101] = StagedChange{
		ID: 101, Definition: Definition{Key: "flag"}, ApplyAt: time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	report, err = provider.Cleanup(t.Context(), "tenant", CleanupOptions{DiscardStagesBefore: time.Time{}, KeepAudit: 3})
	if err != nil || report.DiscardedStages != 0 || len(provider.staged["tenant"]) != 3 {
		t.Fatalf("Cleanup(zero cutoff) = (%#v, %v), stages = %#v", report, err, provider.staged["tenant"])
	}
	report, err = provider.Cleanup(t.Context(), "tenant", CleanupOptions{DiscardStagesBefore: cutoff.Add(time.Nanosecond), KeepAudit: 3})
	if err != nil || report.DiscardedStages != 2 {
		t.Fatalf("Cleanup(zero apply time) = (%#v, %v)", report, err)
	}
	if _, exists := provider.staged["tenant"][99]; !exists {
		t.Fatal("zero-time staged change was discarded")
	}
	auditBefore := append([]AuditEntry(nil), provider.audit["tenant"]...)
	report, err = provider.Cleanup(t.Context(), "tenant", CleanupOptions{KeepAudit: 0})
	if err != nil || report.DiscardedAudit != 0 || len(provider.audit["tenant"]) != len(auditBefore) {
		t.Fatalf("Cleanup(zero audit retention) = (%#v, %v)", report, err)
	}
	report, err = provider.Cleanup(t.Context(), "tenant", CleanupOptions{KeepAudit: len(provider.audit["tenant"])})
	if err != nil || report.DiscardedAudit != 0 {
		t.Fatalf("Cleanup(exact retained audit) = (%#v, %v)", report, err)
	}
	provider.audit["tenant"] = []AuditEntry{{Version: 1}, {Version: 2}, {Version: 3}}
	report, err = provider.Cleanup(t.Context(), "tenant", CleanupOptions{KeepAudit: 2})
	if err != nil || report.DiscardedAudit != 1 || len(provider.audit["tenant"]) != 2 || provider.audit["tenant"][0].Version != 2 {
		t.Fatalf("Cleanup(one excess audit) = (%#v, %v), audit = %#v", report, err, provider.audit["tenant"])
	}
}

func TestCachedProviderEnforcesFreshOutageAndCapacityBoundaries(t *testing.T) {
	native := NewMemoryProvider(DefaultLimits())
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		if _, err := native.Create(t.Context(), tenant, Definition{
			Key: "flag", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive,
		}, "actor"); err != nil {
			t.Fatal(err)
		}
	}
	underlying := &failingSnapshotProvider{Provider: native}
	clock := &manualCacheClock{now: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)}
	cached, err := NewCachedProvider(underlying, CacheConfig{
		Clock: clock, MaxStaleness: time.Minute, MaxOutageStaleness: 2 * time.Minute,
		FailurePolicy: FailOpen, MaxTenants: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cached.Refresh(t.Context(), "tenant-a"); err != nil {
		t.Fatal(err)
	}
	freshClosed, err := NewCachedProvider(underlying, CacheConfig{
		Clock: clock, MaxStaleness: time.Minute, MaxOutageStaleness: 2 * time.Minute,
		FailurePolicy: FailClosed, MaxTenants: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	freshClosed.entries["tenant-a"] = cachedSnapshot{snapshot: cached.entries["tenant-a"].snapshot, fetched: clock.now}
	underlying.fail = true
	clock.now = clock.now.Add(time.Minute)
	if _, err := freshClosed.Snapshot(t.Context(), "tenant-a"); err != nil {
		t.Fatalf("Snapshot(exact freshness, fail closed) error = %v", err)
	}
	if _, err := cached.Snapshot(t.Context(), "tenant-a"); err != nil {
		t.Fatalf("Snapshot(exact freshness) error = %v", err)
	}
	clock.now = clock.now.Add(time.Minute)
	if _, err := cached.Snapshot(t.Context(), "tenant-a"); err != nil {
		t.Fatalf("Snapshot(exact outage staleness) error = %v", err)
	}
	clock.now = clock.now.Add(time.Nanosecond)
	if _, err := cached.Snapshot(t.Context(), "tenant-a"); !errors.Is(err, errProviderUnavailable) {
		t.Fatalf("Snapshot(after outage bound) error = %v", err)
	}
	closed, err := NewCachedProvider(underlying, CacheConfig{
		Clock: clock, MaxStaleness: time.Minute, MaxOutageStaleness: 2 * time.Minute,
		FailurePolicy: FailClosed, MaxTenants: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	closed.entries = map[string]cachedSnapshot{
		"tenant-a": {snapshot: cached.entries["tenant-a"].snapshot, fetched: clock.now.Add(-90 * time.Second)},
	}
	if _, err := closed.Snapshot(t.Context(), "tenant-a"); !errors.Is(err, errProviderUnavailable) {
		t.Fatalf("Snapshot(fail closed) error = %v", err)
	}
	underlying.fail = false
	clock.now = clock.now.Add(time.Minute)
	if _, err := cached.Refresh(t.Context(), "tenant-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := cached.Refresh(t.Context(), "tenant-b"); err != nil {
		t.Fatal(err)
	}
	before := cached.entries["tenant-b"]
	if _, err := cached.Refresh(t.Context(), "tenant-a"); err != nil {
		t.Fatal(err)
	}
	if len(cached.entries) != 2 || cached.entries["tenant-b"].fetched != before.fetched {
		t.Fatalf("replacement evicted another tenant: %#v", cached.entries)
	}
	cached.entries["tenant-a"] = cachedSnapshot{fetched: clock.now}
	cached.entries["tenant-b"] = cachedSnapshot{fetched: clock.now}
	cached.config.MaxTenants = 2
	cached.evictOldestLocked()
	if _, exists := cached.entries["tenant-a"]; exists {
		t.Fatalf("equal-time eviction did not use lexical tenant order: %#v", cached.entries)
	}
}

func TestCachedProviderInvalidatesOnlyForMaterialSuccessfulMaintenance(t *testing.T) {
	native := NewMemoryProvider(DefaultLimits())
	clock := &manualCacheClock{now: time.Now()}
	cached, err := NewCachedProvider(native, CacheConfig{
		Clock: clock, MaxStaleness: time.Minute, MaxOutageStaleness: time.Minute,
		FailurePolicy: FailClosed, MaxTenants: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	cached.entries["tenant"] = cachedSnapshot{fetched: clock.now}
	if applied, err := cached.ApplyScheduled(t.Context(), "tenant", clock.now, "actor"); err != nil || len(applied) != 0 {
		t.Fatalf("ApplyScheduled(empty) = (%#v, %v)", applied, err)
	}
	if _, exists := cached.entries["tenant"]; !exists {
		t.Fatal("empty scheduled application invalidated cache")
	}
	report, err := cached.Cleanup(t.Context(), "tenant", CleanupOptions{})
	if err != nil || report.DeletedFeatures != 0 {
		t.Fatalf("Cleanup(empty) = (%#v, %v)", report, err)
	}
	if _, exists := cached.entries["tenant"]; !exists {
		t.Fatal("empty cleanup invalidated cache")
	}
	maintenanceErr := errors.New("maintenance failed")
	failing := maintenanceResultProvider{
		Provider:      native,
		applyResult:   []Definition{{Key: "changed"}},
		cleanupResult: CleanupReport{DeletedFeatures: 1},
		err:           maintenanceErr,
	}
	failedCache, err := NewCachedProvider(failing, cached.config)
	if err != nil {
		t.Fatal(err)
	}
	failedCache.entries["tenant"] = cachedSnapshot{fetched: clock.now}
	if _, err := failedCache.ApplyScheduled(t.Context(), "tenant", clock.now, "actor"); !errors.Is(err, maintenanceErr) {
		t.Fatalf("ApplyScheduled(error) = %v", err)
	}
	if _, exists := failedCache.entries["tenant"]; !exists {
		t.Fatal("failed scheduled application invalidated cache")
	}
	if _, err := failedCache.Cleanup(t.Context(), "tenant", CleanupOptions{}); !errors.Is(err, maintenanceErr) {
		t.Fatalf("Cleanup(error) = %v", err)
	}
	if _, exists := failedCache.entries["tenant"]; !exists {
		t.Fatal("failed cleanup invalidated cache")
	}
}

func TestDocumentImportAcceptsExactByteLimit(t *testing.T) {
	data, err := Export(nil, nil, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	limits := DefaultLimits()
	limits.MaxImportBytes = len(data)
	if _, _, err := Import(data, limits); err != nil {
		t.Fatalf("Import(exact byte limit) error = %v", err)
	}
	limits.MaxImportBytes--
	if _, _, err := Import(data, limits); !errors.Is(err, ErrImportLimit) {
		t.Fatalf("Import(over byte limit) error = %v", err)
	}
	if strings.Contains(string(data), "\n") {
		t.Fatal("deterministic document unexpectedly contains formatting whitespace")
	}
	malformedTrailing := append(append([]byte(nil), data...), '{')
	if _, _, err := Import(malformedTrailing, DefaultLimits()); err == nil || !strings.Contains(err.Error(), "decode trailing data") {
		t.Fatalf("Import(malformed trailing data) error = %v", err)
	}
}

func TestDurableStateAcceptsExactByteAndCollectionBounds(t *testing.T) {
	limits := DefaultLimits()
	provider := NewMemoryProvider(limits)
	created, err := provider.Create(t.Context(), "tenant", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
	}, "actor")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.StageUpdate(t.Context(), "tenant", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive,
	}, created.Version, time.Now(), "actor"); err != nil {
		t.Fatal(err)
	}
	data, err := marshalTenantState(provider, "tenant")
	if err != nil {
		t.Fatal(err)
	}
	provider.limits.MaxStateBytes = len(data)
	if _, err := marshalTenantState(provider, "tenant"); err != nil {
		t.Fatalf("marshalTenantState(exact byte limit) error = %v", err)
	}
	exact := limits
	exact.MaxStateBytes = len(data)
	exact.MaxAuditEntries = len(provider.audit["tenant"])
	exact.MaxStagedChanges = len(provider.staged["tenant"])
	restored, err := unmarshalTenantState(data, "tenant", exact)
	if err != nil {
		t.Fatalf("unmarshalTenantState(exact limits) error = %v", err)
	}
	stages, err := restored.StagedChanges(t.Context(), "tenant")
	if err != nil || len(stages) != 1 || stages[0].ID != 1 {
		t.Fatalf("StagedChanges(restored) = (%#v, %v)", stages, err)
	}
}

func TestSnapshotGraphAndDiagnosticExactBoundaries(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxFeatures = 2
	limits.MaxEvaluationDepth = 1
	limits.MaxGroups = 2
	limits.MaxGroupDepth = 1
	limits.MaxDiagnostics = 2
	limits.MaxDiagnosticBytes = 2
	definitions := []Definition{
		{
			Key: "base", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
			Variants:   map[string]Value{"on": BooleanValue(true)},
			Strategies: []Strategy{ExactTargetStrategy{Name: "base", Variant: "on"}},
		},
		{
			Key: "leaf", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
			Dependencies: []Dependency{{FeatureKey: "base", RequiredVariant: "on"}},
		},
	}
	snapshot, err := NewSnapshot(definitions, limits)
	if err != nil {
		t.Fatalf("NewSnapshot(exact feature and depth limits) error = %v", err)
	}
	if detail, err := snapshot.Boolean("leaf", Context{}); err != nil || detail.Reason != ReasonDefault {
		t.Fatalf("Boolean(exact dependency depth) = (%#v, %v)", detail, err)
	}
	snapshot.limits.MaxEvaluationDepth = 0
	if _, err := snapshot.Boolean("leaf", Context{}); err == nil {
		t.Fatal("Boolean(dependency beyond reduced snapshot depth) succeeded")
	}
	groups := []GroupDefinition{
		{Key: "parent", Strategies: []Strategy{ExactTargetStrategy{Name: "group", Variant: "on"}}},
		{Key: "child", Parent: "parent"},
	}
	grouped := []Definition{{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
		Variants: map[string]Value{"on": BooleanValue(true)}, Groups: []string{"child"},
	}}
	groupSnapshot, err := NewSnapshotWithGroups(grouped, groups, limits)
	if err != nil {
		t.Fatalf("NewSnapshotWithGroups(exact group depth) error = %v", err)
	}
	if detail, err := groupSnapshot.Boolean("flag", Context{}); err != nil || !detail.Value || detail.Reason != ReasonGroupMatch {
		t.Fatalf("Boolean(exact group depth) = (%#v, %v)", detail, err)
	}
	groupSnapshot.limits.MaxGroupDepth = 0
	if _, err := groupSnapshot.Boolean("flag", Context{}); err == nil {
		t.Fatal("Boolean(group beyond reduced snapshot depth) succeeded")
	}
	diagnostics := boundDiagnostics([]Diagnostic{
		{Code: "ab", Message: "éx"},
		{Code: "cd", Message: "ok"},
	}, limits)
	if len(diagnostics) != 2 || diagnostics[0].Code != "ab" || diagnostics[0].Message != "é" {
		t.Fatalf("boundDiagnostics(exact limits) = %#v", diagnostics)
	}
	if got := truncateUTF8("ab", 1); got != "a" {
		t.Fatalf("truncateUTF8(one byte) = %q", got)
	}
}

func TestGroupDefinitionsAcceptExactBoundsAndRejectIndependentInvalidFields(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxGroups = 2
	limits.MaxKeyBytes = 2
	limits.MaxStringBytes = 2
	limits.MaxMetadata = 2
	limits.MaxTags = 2
	limits.MaxStrategies = 2
	groups := []GroupDefinition{
		{
			Key: "g1", Owner: "o1", Metadata: map[string]string{"m1": "v1", "m2": "v2"},
			Tags: []string{"t1", "t2"}, Strategies: []Strategy{
				ExactTargetStrategy{Name: "s1", Variant: "on"},
				ExactTargetStrategy{Name: "s2", Variant: "no"},
			},
		},
		{Key: "g2", Parent: "g1"},
	}
	if _, err := NewSnapshotWithGroups(nil, groups, limits); err != nil {
		t.Fatalf("NewSnapshotWithGroups(exact group limits) error = %v", err)
	}
	invalid := map[string]GroupDefinition{
		"long metadata key":    {Key: "g1", Metadata: map[string]string{"mmm": "v1"}},
		"long metadata value":  {Key: "g1", Metadata: map[string]string{"m1": "vvv"}},
		"missing target":       {Key: "g1", Strategies: []Strategy{ExactTargetStrategy{Name: "s1"}}},
		"long strategy target": {Key: "g1", Strategies: []Strategy{ExactTargetStrategy{Name: "s1", Variant: "onn"}}},
	}
	for name, group := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := NewSnapshotWithGroups(nil, []GroupDefinition{group}, limits); err == nil {
				t.Fatal("NewSnapshotWithGroups() succeeded")
			}
		})
	}
}

func TestImportSkipContinuesToLaterResourcesAndReplaceIncrementsVersions(t *testing.T) {
	provider := NewMemoryProvider(DefaultLimits())
	if _, err := provider.CreateGroup(t.Context(), "tenant", GroupDefinition{Key: "a"}, "actor"); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Create(t.Context(), "tenant", Definition{
		Key: "a", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
	}, "actor"); err != nil {
		t.Fatal(err)
	}
	document, err := Export([]Definition{
		{Key: "a", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive},
		{Key: "b", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive},
	}, []GroupDefinition{{Key: "a"}, {Key: "b"}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	report, err := provider.ImportDocument(t.Context(), "tenant", document, ImportOptions{ConflictPolicy: ConflictSkip}, "actor")
	if err != nil || report.Skipped != 2 || report.CreatedFeatures != 1 || report.CreatedGroups != 1 {
		t.Fatalf("ImportDocument(skip and continue) = (%#v, %v)", report, err)
	}
	snapshot, err := provider.Snapshot(t.Context(), "tenant")
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := snapshot.definitions["b"]; !exists {
		t.Fatal("feature after skipped conflict was not imported")
	}
	if _, exists := snapshot.groups["b"]; !exists {
		t.Fatal("group after skipped conflict was not imported")
	}
	report, err = provider.ImportDocument(t.Context(), "tenant", document, ImportOptions{ConflictPolicy: ConflictReplace}, "actor")
	if err != nil || report.UpdatedFeatures != 2 || report.UpdatedGroups != 2 {
		t.Fatalf("ImportDocument(replace) = (%#v, %v)", report, err)
	}
	snapshot, err = provider.Snapshot(t.Context(), "tenant")
	if err != nil || snapshot.definitions["a"].Version != 2 || snapshot.groups["a"].Version != 2 {
		t.Fatalf("Snapshot(replaced versions) = (%#v, %v)", snapshot, err)
	}
}

func TestMemoryAndStagingPreserveOrderCapacityAndRemainingAssignments(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxStagedChanges = 2
	limits.MaxAuditEntries = 2
	provider := NewMemoryProvider(limits)
	for _, key := range []string{"a", "b"} {
		created, err := provider.Create(t.Context(), "tenant", Definition{
			Key: key, Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
		}, "actor")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := provider.StageUpdate(t.Context(), "tenant", Definition{
			Key: key, Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive,
		}, created.Version, time.Now(), "actor"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := provider.Create(t.Context(), "tenant", Definition{
		Key: "c", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
	}, "actor"); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.StageUpdate(t.Context(), "tenant", Definition{
		Key: "c", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive,
	}, 1, time.Now(), "actor"); err == nil {
		t.Fatal("StageUpdate(over exact capacity) succeeded")
	}
	stages, err := provider.StagedChanges(t.Context(), "tenant")
	if err != nil || len(stages) != 2 || stages[0].ID != 1 || stages[1].ID != 2 {
		t.Fatalf("StagedChanges(order) = (%#v, %v)", stages, err)
	}
	if len(provider.audit["tenant"]) != 2 || provider.audit["tenant"][0].FeatureKey != "b" || provider.audit["tenant"][1].FeatureKey != "c" {
		t.Fatalf("bounded audit order = %#v", provider.audit["tenant"])
	}
	provider.groups["tenant"] = map[string]GroupDefinition{"g1": {Key: "g1"}, "g2": {Key: "g2"}}
	record := provider.tenants["tenant"]["a"]
	record.definition.Groups = []string{"g1", "g2"}
	provider.tenants["tenant"]["a"] = record
	updated, err := provider.RemoveGroup(t.Context(), "tenant", "a", "g1", record.definition.Version, "actor")
	if err != nil || len(updated.Groups) != 1 || updated.Groups[0] != "g2" {
		t.Fatalf("RemoveGroup(preserve remaining) = (%#v, %v)", updated, err)
	}
}
