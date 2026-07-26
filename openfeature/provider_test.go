package openfeature

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"

	featureflags "github.com/faustbrian/golib/pkg/feature-flags"
	of "github.com/open-feature/go-sdk/openfeature"
)

func TestProviderMapsTypedContextWithoutChangingTenantSemantics(t *testing.T) {
	t.Parallel()

	native := featureflags.NewMemoryProvider(featureflags.DefaultLimits())
	_, err := native.Create(context.Background(), "tenant-a", featureflags.Definition{
		Key:       "checkout.redesign",
		Type:      featureflags.TypeBoolean,
		Default:   featureflags.BooleanValue(false),
		Lifecycle: featureflags.LifecycleActive,
		Variants:  map[string]featureflags.Value{"enabled": featureflags.BooleanValue(true)},
		Strategies: []featureflags.Strategy{featureflags.FactStrategy{
			Name: "mature-account", Variant: "enabled", Fact: "account.age", Equals: featureflags.IntegerValue(10),
		}},
	}, "alice")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	provider, err := New(native, "tenant-a", Options{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	detail := provider.BooleanEvaluation(context.Background(), "checkout.redesign", false, of.FlattenedContext{
		of.TargetingKey: "user-123",
		"tenant":        "tenant-a",
		"account.age":   int64(10),
	})
	if !detail.Value || detail.Reason != of.TargetingMatchReason {
		t.Fatalf("BooleanEvaluation() = (%t, %q), want (true, TARGETING_MATCH)", detail.Value, detail.Reason)
	}

	detail = provider.BooleanEvaluation(context.Background(), "checkout.redesign", false, of.FlattenedContext{
		"tenant": "tenant-b",
	})
	if detail.Value || detail.Error() == nil {
		t.Fatalf("cross-tenant BooleanEvaluation() = (%t, %v), want default and error", detail.Value, detail.Error())
	}
}

type lifecycleProvider struct {
	featureflags.Provider
	health      featureflags.ProviderHealth
	closed      bool
	snapshotErr error
}

func (provider *lifecycleProvider) Health(context.Context) featureflags.ProviderHealth {
	return provider.health
}

func (provider *lifecycleProvider) Close(context.Context) error {
	provider.closed = true
	return nil
}

func (provider *lifecycleProvider) Snapshot(
	ctx context.Context,
	tenant string,
) (featureflags.Snapshot, error) {
	if provider.snapshotErr != nil {
		return featureflags.Snapshot{}, provider.snapshotErr
	}
	return provider.Provider.Snapshot(ctx, tenant)
}

func TestProviderExposesEveryCompatibleTypeAndLifecycle(t *testing.T) {
	t.Parallel()

	native := featureflags.NewMemoryProvider(featureflags.DefaultLimits())
	definitions := []featureflags.Definition{
		{Key: "string", Type: featureflags.TypeString, Default: featureflags.StringValue("value"), Lifecycle: featureflags.LifecycleActive},
		{Key: "float", Type: featureflags.TypeFloat, Default: featureflags.FloatValue(1.25), Lifecycle: featureflags.LifecycleActive},
		{Key: "integer", Type: featureflags.TypeInteger, Default: featureflags.IntegerValue(42), Lifecycle: featureflags.LifecycleActive},
		{Key: "object", Type: featureflags.TypeStructured, Default: featureflags.StructuredValue(json.RawMessage(`{"enabled":true}`)), Lifecycle: featureflags.LifecycleActive},
	}
	for _, definition := range definitions {
		if _, err := native.Create(t.Context(), "tenant-a", definition, "alice"); err != nil {
			t.Fatalf("Create(%s) error = %v", definition.Key, err)
		}
	}
	owned := &lifecycleProvider{
		Provider: native,
		health:   featureflags.ProviderHealth{Healthy: true, Code: "ready"},
	}
	provider, err := New(owned, "tenant-a", Options{OwnProvider: true, Hooks: []of.Hook{nil}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if provider.Metadata().Name != "go-feature-flags" || len(provider.Hooks()) != 1 {
		t.Fatalf("provider metadata or hooks are incomplete")
	}
	if provider.EventChannel() == nil {
		t.Fatal("EventChannel() is nil")
	}
	select {
	case event, open := <-provider.EventChannel():
		t.Fatalf("EventChannel() before shutdown = (%#v, %t), want no synthesized event", event, open)
	default:
	}
	if err := provider.Init(of.EvaluationContext{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if err := provider.InitWithContext(t.Context(), of.EvaluationContext{}); err != nil {
		t.Fatalf("InitWithContext() error = %v", err)
	}
	flat := of.FlattenedContext{
		of.TargetingKey: "subject", "tenant": "tenant-a", "environment": "production",
		"time": time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC), "plan": "pro",
	}
	if detail := provider.StringEvaluation(t.Context(), "string", "fallback", flat); detail.Value != "value" {
		t.Fatalf("StringEvaluation() = %#v", detail)
	}
	if detail := provider.FloatEvaluation(t.Context(), "float", 0, flat); detail.Value != 1.25 {
		t.Fatalf("FloatEvaluation() = %#v", detail)
	}
	if detail := provider.IntEvaluation(t.Context(), "integer", 0, flat); detail.Value != 42 {
		t.Fatalf("IntEvaluation() = %#v", detail)
	}
	if detail := provider.ObjectEvaluation(t.Context(), "object", nil, flat); detail.Value.(map[string]any)["enabled"] != true {
		t.Fatalf("ObjectEvaluation() = %#v", detail)
	}
	if detail := provider.StringEvaluation(t.Context(), "missing", "fallback", flat); detail.Value != "fallback" || detail.Error() == nil {
		t.Fatalf("missing StringEvaluation() = %#v", detail)
	}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 16)
	for index := range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if index == 0 {
				provider.Shutdown()
				return
			}
			if shutdownErr := provider.ShutdownWithContext(t.Context()); shutdownErr != nil {
				errorsSeen <- shutdownErr
			}
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for shutdownErr := range errorsSeen {
		t.Fatalf("ShutdownWithContext() error = %v", shutdownErr)
	}
	if !owned.closed {
		t.Fatal("Shutdown() did not close the owned native provider")
	}
	if _, open := <-provider.EventChannel(); open {
		t.Fatal("EventChannel() remained open after shutdown")
	}
}

func TestProviderMakesDecimalCapabilityLossExplicit(t *testing.T) {
	t.Parallel()

	native := featureflags.NewMemoryProvider(featureflags.DefaultLimits())
	if _, err := native.Create(t.Context(), "tenant-a", featureflags.Definition{
		Key: "price", Type: featureflags.TypeDecimal,
		Default:   featureflags.DecimalValue("19.99"),
		Lifecycle: featureflags.LifecycleActive,
	}, "alice"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	provider, err := New(native, "tenant-a", Options{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	detail := provider.StringEvaluation(t.Context(), "price", "fallback", nil)
	if detail.Value != "fallback" || detail.Error() == nil || detail.Reason != of.ErrorReason {
		t.Fatalf("StringEvaluation(decimal) = %#v, want explicit defaulted error", detail)
	}
}

func TestProviderRejectsInvalidContextAndUnhealthyInitialization(t *testing.T) {
	t.Parallel()

	native := &lifecycleProvider{
		Provider: featureflags.NewMemoryProvider(featureflags.DefaultLimits()),
		health:   featureflags.ProviderHealth{Code: "unavailable"},
	}
	provider, err := New(native, "tenant-a", Options{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := provider.InitWithContext(t.Context(), of.EvaluationContext{}); err == nil {
		t.Fatal("InitWithContext() accepted an unhealthy native provider")
	}
	for name, flat := range map[string]of.FlattenedContext{
		"targeting key": {of.TargetingKey: 10},
		"tenant type":   {"tenant": 10},
		"tenant value":  {"tenant": "tenant-b"},
		"environment":   {"environment": 10},
		"time":          {"time": "now"},
		"overflow":      {"count": uint64(math.MaxUint64)},
		"unsupported":   {"callback": func() {}},
	} {
		t.Run(name, func(t *testing.T) {
			detail := provider.BooleanEvaluation(t.Context(), "flag", true, flat)
			if !detail.Value || detail.Error() == nil {
				t.Fatalf("BooleanEvaluation() = %#v, want default with error", detail)
			}
		})
	}
	if _, err := New(nil, "tenant-a", Options{}); err == nil {
		t.Fatal("New(nil) succeeded")
	}
	if _, err := New(native, "", Options{}); err == nil {
		t.Fatal("New(empty tenant) succeeded")
	}
}

func TestFactAndReasonMappingsAreComplete(t *testing.T) {
	t.Parallel()

	values := []any{
		true, "value", int(1), int8(1), int16(1), int32(1), int64(1),
		uint(1), uint8(1), uint16(1), uint32(1), uint64(1),
		float32(1), float64(1), json.RawMessage(`{"ok":true}`),
		map[string]any{"ok": true},
	}
	for _, value := range values {
		if _, err := mapFact(value); err != nil {
			t.Fatalf("mapFact(%T) error = %v", value, err)
		}
	}
	if _, err := mapFact(uint64(math.MaxUint64)); err == nil {
		t.Fatal("mapFact(max uint64) succeeded")
	}
	if _, err := mapFact(uint(math.MaxUint64)); strconv.IntSize == 64 && err == nil {
		t.Fatal("mapFact(max uint) succeeded")
	}
	for reason, want := range map[featureflags.Reason]of.Reason{
		featureflags.ReasonDefault:          of.DefaultReason,
		featureflags.ReasonDependencyFailed: of.DefaultReason,
		featureflags.ReasonInactive:         of.DisabledReason,
		featureflags.ReasonRollout:          of.SplitReason,
		featureflags.ReasonTargetingMatch:   of.TargetingMatchReason,
		featureflags.ReasonSchedule:         of.TargetingMatchReason,
		featureflags.ReasonGroupMatch:       of.TargetingMatchReason,
		featureflags.Reason("future"):       of.UnknownReason,
	} {
		if got := mapReason(reason); got != want {
			t.Fatalf("mapReason(%q) = %q, want %q", reason, got, want)
		}
	}
	if detail := errorDetail(featureflags.ErrNotFound); detail.ResolutionDetail().ErrorCode != of.FlagNotFoundCode {
		t.Fatalf("errorDetail(not found) = %#v", detail)
	}
	if detail := errorDetail(featureflags.ErrContextLimit); detail.ResolutionDetail().ErrorCode != of.InvalidContextCode {
		t.Fatalf("errorDetail(context) = %#v", detail)
	}
	if detail := errorDetail(errors.New("storage")); detail.ResolutionDetail().ErrorCode != of.GeneralCode {
		t.Fatalf("errorDetail(general) = %#v", detail)
	}
	detail := mapDetail("enabled", featureflags.ReasonRollout, "rollout", math.MaxUint64)
	if detail.FlagMetadata["version"] != strconv.FormatUint(math.MaxUint64, 10) {
		t.Fatalf("mapDetail(max version) = %#v", detail.FlagMetadata)
	}
}

func TestEveryEvaluationMethodPreservesDefaultsOnNativeErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("snapshot unavailable")
	native := &lifecycleProvider{
		Provider: featureflags.NewMemoryProvider(featureflags.DefaultLimits()),
		health:   featureflags.ProviderHealth{Healthy: true, Code: "ready"},
	}
	provider, err := New(native, "tenant-a", Options{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	assertDefaults := func(t *testing.T) {
		t.Helper()
		if detail := provider.BooleanEvaluation(t.Context(), "missing", true, nil); !detail.Value || detail.Error() == nil {
			t.Fatalf("BooleanEvaluation() = %#v", detail)
		}
		if detail := provider.StringEvaluation(t.Context(), "missing", "fallback", nil); detail.Value != "fallback" || detail.Error() == nil {
			t.Fatalf("StringEvaluation() = %#v", detail)
		}
		if detail := provider.FloatEvaluation(t.Context(), "missing", 1.5, nil); detail.Value != 1.5 || detail.Error() == nil {
			t.Fatalf("FloatEvaluation() = %#v", detail)
		}
		if detail := provider.IntEvaluation(t.Context(), "missing", 42, nil); detail.Value != 42 || detail.Error() == nil {
			t.Fatalf("IntEvaluation() = %#v", detail)
		}
		fallback := map[string]any{"fallback": true}
		if detail := provider.ObjectEvaluation(t.Context(), "missing", fallback, nil); detail.Value.(map[string]any)["fallback"] != true || detail.Error() == nil {
			t.Fatalf("ObjectEvaluation() = %#v", detail)
		}
	}
	assertDefaults(t)
	native.snapshotErr = boom
	assertDefaults(t)
	if _, err := decodeObject(json.RawMessage(`x`)); err == nil {
		t.Fatal("decodeObject(invalid JSON) succeeded")
	}
}
