package openfeature

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	featureflags "github.com/faustbrian/go-feature-flags"
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
	snapshots   int
}

type countingJSONMarshaler struct {
	called *bool
}

type pointerJSONMarshaler struct{}

func (*pointerJSONMarshaler) MarshalJSON() ([]byte, error) { return []byte(`null`), nil }

type pointerTextMarshaler struct{}

func (*pointerTextMarshaler) MarshalText() ([]byte, error) { return []byte("value"), nil }

func (marshaler countingJSONMarshaler) MarshalJSON() ([]byte, error) {
	*marshaler.called = true

	return []byte(`"value"`), nil
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
	provider.snapshots++
	if provider.snapshotErr != nil {
		return featureflags.Snapshot{}, provider.snapshotErr
	}
	return provider.Provider.Snapshot(ctx, tenant)
}

func TestProviderRejectsHostileContextBeforeNativeSnapshot(t *testing.T) {
	t.Parallel()

	native := &lifecycleProvider{Provider: featureflags.NewMemoryProvider(featureflags.DefaultLimits())}
	provider, err := New(native, "tenant-a", Options{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	deepValue := any(true)
	for range featureflags.DefaultLimits().MaxEvaluationDepth + 1 {
		deepValue = []any{deepValue}
	}
	cyclicValue := map[string]any{}
	cyclicValue["self"] = cyclicValue
	contexts := map[string]of.FlattenedContext{
		"too many facts": func() of.FlattenedContext {
			contextValue := make(of.FlattenedContext, featureflags.DefaultLimits().MaxFacts+1)
			for index := range featureflags.DefaultLimits().MaxFacts + 1 {
				contextValue["fact."+strconv.Itoa(index)] = true
			}
			return contextValue
		}(),
		"oversized key": {
			string(make([]byte, featureflags.DefaultLimits().MaxContextKeyBytes+1)): true,
		},
		"oversized value": {
			"plan": string(make([]byte, featureflags.DefaultLimits().MaxContextValueBytes+1)),
		},
		"oversized targeting key": {
			of.TargetingKey: string(make([]byte, featureflags.DefaultLimits().MaxContextValueBytes+1)),
		},
		"invalid environment type": {"environment": 1},
		"oversized structured value": {
			"claims": json.RawMessage(`"` + string(make([]byte, featureflags.DefaultLimits().MaxStructuredBytes+1)) + `"`),
		},
		"excessive structured depth": {"claims": deepValue},
		"cyclic structured value":    {"claims": cyclicValue},
	}
	for name, contextValue := range contexts {
		t.Run(name, func(t *testing.T) {
			detail := provider.BooleanEvaluation(t.Context(), "flag", true, contextValue)
			if !detail.Value || detail.Error() == nil || detail.ResolutionDetail().ErrorCode != of.InvalidContextCode {
				t.Fatalf("BooleanEvaluation() = %#v, want default with invalid-context error", detail)
			}
		})
	}
	if native.snapshots != 0 {
		t.Fatalf("native provider received %d snapshot call(s) for hostile contexts", native.snapshots)
	}
}

func TestProviderRejectsContextCardinalityBeforeInspectingEntries(t *testing.T) {
	t.Parallel()

	provider := &Provider{limits: featureflags.DefaultLimits()}
	contextValue := make(of.FlattenedContext, provider.limits.MaxFacts+5)
	for index := range provider.limits.MaxFacts + 5 {
		key := strings.Repeat("k", provider.limits.MaxContextKeyBytes+1) + strconv.Itoa(index)
		contextValue[key] = true
	}

	_, err := provider.mapContext(contextValue)
	if err == nil || err.Error() != "context entries exceed configured bounds: evaluation context exceeds limit" {
		t.Fatalf("mapContext() error = %v, want cardinality rejection before entry inspection", err)
	}
}

func TestNewRejectsTypedNilNativeProvider(t *testing.T) {
	t.Parallel()

	var native *lifecycleProvider
	if _, err := New(native, "tenant-a", Options{}); err == nil {
		t.Fatal("New() accepted a typed-nil native provider")
	}
	if isNil(1) {
		t.Fatal("isNil() classified a concrete scalar as nil")
	}
	if _, err := New(featureflags.NewMemoryProvider(featureflags.DefaultLimits()), strings.Repeat("t", featureflags.DefaultLimits().MaxKeyBytes), Options{}); err != nil {
		t.Fatalf("New() exact tenant limit error = %v", err)
	}
	if _, err := New(featureflags.NewMemoryProvider(featureflags.DefaultLimits()), strings.Repeat("t", featureflags.DefaultLimits().MaxKeyBytes+1), Options{}); !errors.Is(err, featureflags.ErrContextLimit) {
		t.Fatalf("New() oversized tenant error = %v, want ErrContextLimit", err)
	}
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

func TestProviderMapsReservedContextOnlyToDedicatedFields(t *testing.T) {
	t.Parallel()

	evaluationTime := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	provider := &Provider{tenant: "tenant-a"}
	contextValue, err := provider.mapContext(of.FlattenedContext{
		of.TargetingKey: "subject-a",
		"tenant":        "tenant-a",
		"environment":   "production",
		"time":          evaluationTime,
		"plan":          "enterprise",
	})
	if err != nil {
		t.Fatalf("mapContext() error = %v", err)
	}
	if contextValue.Subject != "subject-a" || contextValue.Tenant != "tenant-a" ||
		contextValue.Environment != "production" || !contextValue.Time.Equal(evaluationTime) {
		t.Fatalf("mapContext() reserved fields = %#v", contextValue)
	}
	for _, key := range []string{string(of.TargetingKey), "tenant", "environment", "time"} {
		if _, exists := contextValue.Facts[key]; exists {
			t.Fatalf("mapContext() included reserved fact %q", key)
		}
		if _, exists := contextValue.Attributes[key]; exists {
			t.Fatalf("mapContext() included reserved attribute %q", key)
		}
	}
	if fact, exists := contextValue.Facts["plan"]; !exists {
		t.Fatal("mapContext() omitted ordinary fact plan")
	} else if value, ok := fact.String(); !ok || value != "enterprise" {
		t.Fatalf("mapContext() plan fact = (%q, %t)", value, ok)
	}
	if contextValue.Attributes["plan"] != "enterprise" {
		t.Fatalf("mapContext() plan attribute = %q", contextValue.Attributes["plan"])
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
	for _, value := range []float64{math.NaN(), math.Inf(1)} {
		if _, err := mapFact(value); !errors.Is(err, featureflags.ErrContextLimit) {
			t.Fatalf("mapFact(%v) error = %v, want ErrContextLimit", value, err)
		}
	}
	if _, err := mapFact(float32(math.Inf(-1))); !errors.Is(err, featureflags.ErrContextLimit) {
		t.Fatalf("mapFact(float32 -Inf) error = %v, want ErrContextLimit", err)
	}
	if _, err := mapFact(json.RawMessage(`{`)); !errors.Is(err, featureflags.ErrContextLimit) {
		t.Fatalf("mapFact(invalid raw JSON) error = %v, want ErrContextLimit", err)
	}
	if _, err := mapFact(make(chan int)); err == nil {
		t.Fatal("mapFact(channel) succeeded")
	}
	maximumSigned := uint64(math.MaxInt64)
	if strconv.IntSize == 64 {
		fact, err := mapFact(uint(maximumSigned))
		if err != nil {
			t.Fatalf("mapFact(max int64 as uint) error = %v", err)
		}
		if value, ok := fact.Integer(); !ok || value != math.MaxInt64 {
			t.Fatalf("mapFact(max int64 as uint) = (%d, %t)", value, ok)
		}
	}
	fact, err := mapFact(maximumSigned)
	if err != nil {
		t.Fatalf("mapFact(max int64 as uint64) error = %v", err)
	}
	if value, ok := fact.Integer(); !ok || value != math.MaxInt64 {
		t.Fatalf("mapFact(max int64 as uint64) = (%d, %t)", value, ok)
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
	if detail.FlagMetadata["matchedStrategy"] != "rollout" {
		t.Fatalf("mapDetail(strategy) = %#v", detail.FlagMetadata)
	}
	boundaryDetail := mapDetail("enabled", featureflags.ReasonDefault, "", math.MaxInt64)
	if version, ok := boundaryDetail.FlagMetadata["version"].(int64); !ok || version != math.MaxInt64 {
		t.Fatalf("mapDetail(max int64 version) = %#v", boundaryDetail.FlagMetadata)
	}
	if _, exists := boundaryDetail.FlagMetadata["matchedStrategy"]; exists {
		t.Fatalf("mapDetail(empty strategy) = %#v", boundaryDetail.FlagMetadata)
	}
}

func TestStructuredFactPreflightBoundsWorkBeforeEncoding(t *testing.T) {
	t.Parallel()

	limits := featureflags.DefaultLimits()
	if err := validateStructuredInput("value", limits, 0, &structuredBudget{}); err != nil {
		t.Fatalf("validateStructuredInput(string) error = %v", err)
	}
	limits.MaxStructuredBytes = 2
	if err := validateStructuredInput(map[string]any{"oversized": true}, limits, 0, &structuredBudget{}); !errors.Is(err, featureflags.ErrContextLimit) {
		t.Fatalf("validateStructuredInput(bytes) error = %v, want ErrContextLimit", err)
	}
	limits.MaxStructuredBytes = 1
	if err := validateStructuredInput([]any{true}, limits, 0, &structuredBudget{}); !errors.Is(err, featureflags.ErrContextLimit) {
		t.Fatalf("validateStructuredInput(nodes) error = %v, want ErrContextLimit", err)
	}
	limits.MaxStructuredBytes = 4
	if _, err := mapFactWithLimits(struct{ Value string }{Value: "oversized"}, limits); !errors.Is(err, featureflags.ErrContextLimit) {
		t.Fatalf("mapFactWithLimits(encoded size) error = %v, want ErrContextLimit", err)
	}

	type namedMap map[string]any
	cyclic := namedMap{}
	cyclic["self"] = cyclic
	limits = featureflags.DefaultLimits()
	if _, err := mapFactWithLimits(cyclic, limits); !errors.Is(err, featureflags.ErrContextLimit) {
		t.Fatalf("mapFactWithLimits(named cycle) error = %v, want ErrContextLimit", err)
	}

	called := false
	if _, err := mapFactWithLimits(map[string]any{
		"custom": countingJSONMarshaler{called: &called},
	}, limits); !errors.Is(err, featureflags.ErrContextLimit) {
		t.Fatalf("mapFactWithLimits(custom marshaler) error = %v, want ErrContextLimit", err)
	}
	if called {
		t.Fatal("mapFactWithLimits invoked a custom JSON marshaler during bounded preflight")
	}

	if _, err := mapFactWithLimits(map[string]any{
		"raw": json.RawMessage(`{`),
	}, limits); !errors.Is(err, featureflags.ErrContextLimit) {
		t.Fatalf("mapFactWithLimits(nested invalid raw JSON) error = %v, want ErrContextLimit", err)
	}
	if _, err := mapFactWithLimits(map[string]any{
		"raw": json.RawMessage(`"safe"`),
	}, limits); !errors.Is(err, featureflags.ErrContextLimit) {
		t.Fatalf("mapFactWithLimits(nested raw JSON) error = %v, want ErrContextLimit", err)
	}
}

func TestStructuredFactPreflightRejectsHostileShapesAtEachBudgetBoundary(t *testing.T) {
	t.Parallel()

	defaults := featureflags.DefaultLimits()
	assertContextLimit := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, featureflags.ErrContextLimit) {
			t.Fatalf("error = %v, want ErrContextLimit", err)
		}
	}

	assertContextLimit(t, validateStructuredValue(reflect.ValueOf(true), defaults, 0, &structuredBudget{nodes: defaults.MaxStructuredBytes}))
	nodeLimits := defaults
	nodeLimits.MaxStructuredBytes = 2
	assertContextLimit(t, validateStructuredInput([]any{true}, nodeLimits, 0, &structuredBudget{}))
	if err := validateStructuredInput(nil, defaults, 0, &structuredBudget{}); err != nil {
		t.Fatalf("validateStructuredInput(nil) error = %v", err)
	}
	var nilInterface any
	if err := validateStructuredValue(reflect.ValueOf(&nilInterface).Elem(), defaults, 0, &structuredBudget{}); err != nil {
		t.Fatalf("validateStructuredValue(nil interface) error = %v", err)
	}
	var nilPointer *int
	if err := validateStructuredInput(nilPointer, defaults, 0, &structuredBudget{}); err != nil {
		t.Fatalf("validateStructuredInput(nil pointer) error = %v", err)
	}
	var pointerCycle any
	pointerCycle = &pointerCycle
	assertContextLimit(t, validateStructuredInput(pointerCycle, defaults, 0, &structuredBudget{}))

	assertContextLimit(t, validateStructuredInput(map[int]string{1: "value"}, defaults, 0, &structuredBudget{}))
	var nilMap map[string]any
	if err := validateStructuredInput(nilMap, defaults, 0, &structuredBudget{}); err != nil {
		t.Fatalf("validateStructuredInput(nil map) error = %v", err)
	}
	mapLimits := defaults
	mapLimits.MaxStructuredBytes = 1
	assertContextLimit(t, validateStructuredInput(map[string]any{}, mapLimits, 0, &structuredBudget{}))
	mapLimits.MaxStructuredBytes = 4
	assertContextLimit(t, validateStructuredInput(map[string]any{"long": true}, mapLimits, 0, &structuredBudget{}))
	mapLimits.MaxStructuredBytes = 5
	assertContextLimit(t, validateStructuredInput(map[string]any{"": true}, mapLimits, 0, &structuredBudget{}))
	mapLimits.MaxStructuredBytes = 8
	assertContextLimit(t, validateStructuredInput(map[string]any{"": true}, mapLimits, 0, &structuredBudget{}))

	var nilSlice []any
	if err := validateStructuredInput(nilSlice, defaults, 0, &structuredBudget{}); err != nil {
		t.Fatalf("validateStructuredInput(nil slice) error = %v", err)
	}
	byteLimits := defaults
	byteLimits.MaxStructuredBytes = 1
	assertContextLimit(t, validateStructuredInput([]byte{}, byteLimits, 0, &structuredBudget{}))
	byteLimits.MaxStructuredBytes = 4
	assertContextLimit(t, validateStructuredInput(make([]byte, 3), byteLimits, 0, &structuredBudget{}))
	byteLimits.MaxStructuredBytes = 5
	if err := validateStructuredInput(make([]byte, 6), byteLimits, 0, &structuredBudget{}); err == nil || !strings.Contains(err.Error(), "encoding exceeds") {
		t.Fatalf("validateStructuredInput(byte group limit) error = %v", err)
	}
	byteLimits.MaxStructuredBytes = 6
	if err := validateStructuredInput(make([]byte, 3), byteLimits, 0, &structuredBudget{}); err != nil {
		t.Fatalf("validateStructuredInput(exact base64 group) error = %v", err)
	}
	byteLimits.MaxStructuredBytes = 10
	if err := validateStructuredInput(make([]byte, 4), byteLimits, 0, &structuredBudget{}); err != nil {
		t.Fatalf("validateStructuredInput(base64 remainder) error = %v", err)
	}
	cyclicSlice := make([]any, 1)
	cyclicSlice[0] = cyclicSlice
	assertContextLimit(t, validateStructuredInput(cyclicSlice, defaults, 0, &structuredBudget{}))
	arrayLimits := defaults
	arrayLimits.MaxStructuredBytes = 1
	assertContextLimit(t, validateStructuredInput([0]bool{}, arrayLimits, 0, &structuredBudget{}))
	arrayLimits.MaxStructuredBytes = 7
	assertContextLimit(t, validateStructuredInput([2]bool{true, true}, arrayLimits, 0, &structuredBudget{}))
	arrayBudget := &structuredBudget{}
	if err := validateStructuredInput([3]bool{true, false, true}, defaults, 0, arrayBudget); err != nil {
		t.Fatalf("validateStructuredInput(array) error = %v", err)
	}
	const conservativeArrayBytes = 2 + 3*5 + 2
	if arrayBudget.bytes != conservativeArrayBytes {
		t.Fatalf("array encoded byte budget = %d, want %d", arrayBudget.bytes, conservativeArrayBytes)
	}
	depthLimits := defaults
	depthLimits.MaxEvaluationDepth = 1
	if err := validateStructuredValue(reflect.ValueOf(true), depthLimits, depthLimits.MaxEvaluationDepth, &structuredBudget{}); err != nil {
		t.Fatalf("validateStructuredValue(exact depth) error = %v", err)
	}
	value := 1
	pointer := &value
	assertContextLimit(t, validateStructuredInput(&pointer, depthLimits, 0, &structuredBudget{}))
	depthLimits.MaxEvaluationDepth = 0
	assertContextLimit(t, validateStructuredInput(map[string]any{"nested": map[string]any{}}, depthLimits, 0, &structuredBudget{}))

	for _, value := range []any{int64(1), uint64(1), float64(1)} {
		if err := validateStructuredInput(value, defaults, 0, &structuredBudget{}); err != nil {
			t.Fatalf("validateStructuredInput(%T) error = %v", value, err)
		}
	}
	assertContextLimit(t, validateStructuredInput(math.NaN(), defaults, 0, &structuredBudget{}))
	if implementsCustomEncoding(reflect.TypeOf((*int)(nil))) {
		t.Fatal("plain pointer type implements custom encoding")
	}
	if !implementsCustomEncoding(reflect.TypeOf(pointerJSONMarshaler{})) {
		t.Fatal("pointer-receiver marshaler was not detected")
	}
	if !implementsCustomEncoding(reflect.TypeOf(pointerTextMarshaler{})) {
		t.Fatal("pointer-receiver text marshaler was not detected")
	}

	stringLimits := defaults
	stringLimits.MaxStructuredBytes = 2
	if err := addStructuredString("", stringLimits, &structuredBudget{}); err != nil {
		t.Fatalf("addStructuredString(exact empty string) error = %v", err)
	}
	stringLimits.MaxStructuredBytes = 1
	assertContextLimit(t, addStructuredString("", stringLimits, &structuredBudget{}))
	for value, wantBytes := range map[string]int{
		"a": 3, "\xff": 8, "\x00": 8, "<": 8, ">": 8, "&": 8,
		"\u2028": 8, "\u2029": 8, `"`: 4, `\`: 4,
	} {
		budget := &structuredBudget{}
		if err := addStructuredString(value, defaults, budget); err != nil {
			t.Fatalf("addStructuredString(%q) error = %v", value, err)
		}
		if budget.bytes != wantBytes {
			t.Fatalf("addStructuredString(%q) byte budget = %d, want %d", value, budget.bytes, wantBytes)
		}
	}
	stringLimits.MaxStructuredBytes = 2
	exactLengthBudget := &structuredBudget{}
	assertContextLimit(t, addStructuredString("aa", stringLimits, exactLengthBudget))
	if exactLengthBudget.bytes != 2 {
		t.Fatalf("exact-length string budget = %d, want opening and closing quote budget", exactLengthBudget.bytes)
	}
	assertContextLimit(t, addStructuredString("a", stringLimits, &structuredBudget{}))
	assertContextLimit(t, addStructuredBytes(-1, defaults, &structuredBudget{}))
	assertContextLimit(t, addStructuredBytes(0, defaults, &structuredBudget{bytes: defaults.MaxStructuredBytes + 1}))
	assertContextLimit(t, addStructuredBytes(2, defaults, &structuredBudget{bytes: defaults.MaxStructuredBytes - 1}))
	for name, test := range map[string]struct {
		count  int
		budget *structuredBudget
	}{
		"zero":        {count: 0, budget: &structuredBudget{}},
		"exact total": {count: 0, budget: &structuredBudget{bytes: defaults.MaxStructuredBytes}},
		"exact room":  {count: 1, budget: &structuredBudget{bytes: defaults.MaxStructuredBytes - 1}},
	} {
		if err := addStructuredBytes(test.count, defaults, test.budget); err != nil {
			t.Fatalf("addStructuredBytes(%s) error = %v", name, err)
		}
	}

	marshalError := errors.New("marshal failed")
	if _, err := mapFactWithLimitsAndMarshal(map[string]any{}, defaults, func(any) ([]byte, error) {
		return nil, marshalError
	}); !errors.Is(err, marshalError) {
		t.Fatalf("mapFactWithLimitsAndMarshal(error) = %v, want marshal error", err)
	}
	encodedLimits := defaults
	encodedLimits.MaxStructuredBytes = 2
	if fact, err := mapFactWithLimitsAndMarshal(map[string]any{}, encodedLimits, func(any) ([]byte, error) {
		return []byte("{}"), nil
	}); err != nil {
		t.Fatalf("mapFactWithLimitsAndMarshal(exact) error = %v", err)
	} else if raw, ok := fact.Structured(); !ok || string(raw) != "{}" {
		t.Fatalf("mapFactWithLimitsAndMarshal(exact) = (%q, %t)", raw, ok)
	}
	if _, err := mapFactWithLimitsAndMarshal(map[string]any{}, encodedLimits, func(any) ([]byte, error) {
		return []byte("oversized"), nil
	}); !errors.Is(err, featureflags.ErrContextLimit) {
		t.Fatalf("mapFactWithLimitsAndMarshal(oversized) = %v, want ErrContextLimit", err)
	}
}

func TestContextShapeAcceptsExactLimitsAndRejectsEachIndependentOverflow(t *testing.T) {
	t.Parallel()

	limits := featureflags.DefaultLimits()
	limits.MaxFacts = 2
	limits.MaxAttributes = 1
	limits.MaxContextKeyBytes = len(string(of.TargetingKey))
	limits.MaxContextValueBytes = 2
	provider := &Provider{limits: limits}
	exact := of.FlattenedContext{
		of.TargetingKey: "aa",
		"environment":   "aa",
		"tenant":        "tenant-a",
		"time":          time.Time{},
		strings.Repeat("k", limits.MaxContextKeyBytes): "aa",
		"b": true,
	}
	if err := provider.validateContextShape(exact, limits); err != nil {
		t.Fatalf("validateContextShape(exact) error = %v", err)
	}
	for name, contextValue := range map[string]of.FlattenedContext{
		"total entries": func() of.FlattenedContext {
			value := mapsClone(exact)
			value["c"] = true
			return value
		}(),
		"key bytes":       {strings.Repeat("k", limits.MaxContextKeyBytes+1): true},
		"identity bytes":  {of.TargetingKey: "abc"},
		"fact count":      {"a": true, "b": true, "c": true},
		"attribute count": {"a": "a", "b": "b"},
	} {
		t.Run(name, func(t *testing.T) {
			asserted := provider.validateContextShape(contextValue, limits)
			if !errors.Is(asserted, featureflags.ErrContextLimit) {
				t.Fatalf("validateContextShape() error = %v, want ErrContextLimit", asserted)
			}
		})
	}

	stringLimits := limits
	stringLimits.MaxStringBytes = 2
	stringLimits.MaxContextValueBytes = 3
	if _, err := mapFactWithLimits("aa", stringLimits); err != nil {
		t.Fatalf("mapFactWithLimits(exact string) error = %v", err)
	}
	if _, err := mapFactWithLimits("aaa", stringLimits); !errors.Is(err, featureflags.ErrContextLimit) {
		t.Fatalf("mapFactWithLimits(string limit) error = %v", err)
	}
	stringLimits.MaxStringBytes = 4
	if _, err := mapFactWithLimits("aaa", stringLimits); err != nil {
		t.Fatalf("mapFactWithLimits(exact context string) error = %v", err)
	}
	if _, err := mapFactWithLimits("aaaa", stringLimits); !errors.Is(err, featureflags.ErrContextLimit) {
		t.Fatalf("mapFactWithLimits(context string limit) error = %v", err)
	}

	rawLimits := limits
	rawLimits.MaxStructuredBytes = 4
	if _, err := mapFactWithLimits(json.RawMessage(`null`), rawLimits); err != nil {
		t.Fatalf("mapFactWithLimits(exact raw JSON) error = %v", err)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`false`), json.RawMessage(`nope`)} {
		if _, err := mapFactWithLimits(raw, rawLimits); !errors.Is(err, featureflags.ErrContextLimit) {
			t.Fatalf("mapFactWithLimits(raw JSON %q) error = %v", raw, err)
		}
	}
}

func mapsClone(source of.FlattenedContext) of.FlattenedContext {
	clone := make(of.FlattenedContext, len(source))
	for key, value := range source {
		clone[key] = value
	}

	return clone
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
