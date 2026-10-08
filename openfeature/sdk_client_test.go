package openfeature

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	featureflags "github.com/faustbrian/go-feature-flags/v2"
	of "github.com/open-feature/go-sdk/openfeature"
)

type sdkShutdownContextKey struct{}

type sdkConsumerNative struct {
	featureflags.Provider
	closes       atomic.Int64
	snapshots    atomic.Int64
	closeContext atomic.Value
	unhealthy    bool
}

func (native *sdkConsumerNative) Health(ctx context.Context) featureflags.ProviderHealth {
	if native.unhealthy {
		return featureflags.ProviderHealth{Healthy: false, Code: "fixture-unhealthy"}
	}
	return native.Provider.Health(ctx)
}

func (native *sdkConsumerNative) Snapshot(ctx context.Context, tenant string) (featureflags.Snapshot, error) {
	native.snapshots.Add(1)
	return native.Provider.Snapshot(ctx, tenant)
}

func (native *sdkConsumerNative) Close(ctx context.Context) error {
	native.closes.Add(1)
	if value, ok := ctx.Value(sdkShutdownContextKey{}).(string); ok {
		native.closeContext.Store(value)
	}
	return native.Provider.Close(ctx)
}

type sdkConsumerHook struct {
	of.UnimplementedHook
	mu     sync.Mutex
	calls  int
	value  any
	tenant any
	target string
	reason of.Reason
}

func (hook *sdkConsumerHook) After(_ context.Context, ctx of.HookContext, details of.InterfaceEvaluationDetails, _ of.HookHints) error {
	hook.mu.Lock()
	defer hook.mu.Unlock()
	hook.calls++
	hook.value = details.Value
	hook.tenant = ctx.EvaluationContext().Attribute("tenant")
	hook.target = ctx.EvaluationContext().TargetingKey()
	hook.reason = details.Reason
	return nil
}

// This serial test owns the application SDK singleton; other adapter tests do
// not install SDK providers and can continue exercising direct methods.
func TestSDKClientsPreserveTenantHooksAndOwnedShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	shutdown := func(ctx context.Context) error {
		done := make(chan error, 1)
		go func() { done <- of.ShutdownWithContext(ctx) }()
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if err := shutdown(cleanupCtx); err != nil {
			t.Errorf("SDK cleanup shutdown: %v", err)
		}
	})

	newNative := func(tenant string, targeted bool) *sdkConsumerNative {
		native := &sdkConsumerNative{Provider: featureflags.NewMemoryProvider(featureflags.DefaultLimits())}
		definition := featureflags.Definition{
			Key: "sdk.feature", Type: featureflags.TypeBoolean, Default: featureflags.BooleanValue(false),
			Lifecycle: featureflags.LifecycleActive,
		}
		if targeted {
			definition.Variants = map[string]featureflags.Value{"enabled": featureflags.BooleanValue(true)}
			definition.Strategies = []featureflags.Strategy{featureflags.FactStrategy{
				Name: "mature-account", Variant: "enabled", Fact: "account.age", Equals: featureflags.IntegerValue(10),
			}}
		}
		if _, err := native.Create(ctx, tenant, definition, "fixture"); err != nil {
			t.Fatalf("create SDK fixture: %v", err)
		}
		return native
	}
	owned := newNative("tenant-a", true)
	borrowed := newNative("tenant-b", false)
	hook := &sdkConsumerHook{}
	providerA, err := New(owned, "tenant-a", Options{OwnProvider: true, Hooks: []of.Hook{hook}})
	if err != nil {
		t.Fatal(err)
	}
	providerB, err := New(borrowed, "tenant-b", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := of.SetProviderWithContextAndWait(ctx, providerA); err != nil {
		t.Fatalf("register default SDK provider: %v", err)
	}
	if err := of.SetNamedProviderWithContextAndWait(ctx, "tenant-b-client", providerB); err != nil {
		t.Fatalf("register named SDK provider: %v", err)
	}
	clientA := of.NewClient("")
	clientB := of.NewClient("tenant-b-client")
	if clientA.State() != of.ReadyState || clientB.State() != of.ReadyState {
		t.Fatalf("healthy SDK provider states: %v / %v", clientA.State(), clientB.State())
	}
	details, err := clientA.BooleanValueDetails(ctx, "sdk.feature", false, of.NewEvaluationContext("user-123", map[string]any{
		"tenant": "tenant-a", "account.age": int64(10),
	}))
	if err != nil || !details.Value || details.Reason != of.TargetingMatchReason || details.Variant != "enabled" ||
		details.FlagMetadata["version"] != int64(1) || details.FlagMetadata["matchedStrategy"] != "mature-account" {
		t.Fatalf("SDK targeted evaluation: %#v, %v", details, err)
	}
	hook.mu.Lock()
	if hook.calls != 1 || hook.value != true || hook.tenant != "tenant-a" || hook.target != "user-123" || hook.reason != of.TargetingMatchReason {
		t.Errorf("SDK did not dispatch provider hook with targeted result/context: %#v", hook)
	}
	hook.mu.Unlock()
	details, err = clientB.BooleanValueDetails(ctx, "sdk.feature", true, of.NewEvaluationContext("user-123", map[string]any{"tenant": "tenant-b"}))
	if err != nil || details.Value || details.Reason != of.DefaultReason {
		t.Fatalf("named SDK tenant evaluation: %#v, %v", details, err)
	}
	before := owned.snapshots.Load()
	details, err = clientA.BooleanValueDetails(ctx, "sdk.feature", false, of.NewEvaluationContext("user-123", map[string]any{"tenant": "tenant-b"}))
	if err == nil || details.Value || details.ErrorCode != of.InvalidContextCode || owned.snapshots.Load() != before {
		t.Fatalf("SDK cross-tenant context reached native snapshot: %#v, %v", details, err)
	}
	details, err = clientA.BooleanValueDetails(ctx, "sdk.feature", true, of.NewEvaluationContext("user-123", map[string]any{"environment": 1}))
	if err == nil || !details.Value || details.ErrorCode != of.InvalidContextCode || owned.snapshots.Load() != before {
		t.Fatalf("SDK hostile context reached native snapshot: %#v, %v", details, err)
	}
	unhealthy, err := New(&sdkConsumerNative{Provider: featureflags.NewMemoryProvider(featureflags.DefaultLimits()), unhealthy: true}, "tenant-unhealthy", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := of.SetNamedProviderWithContextAndWait(ctx, "unhealthy-client", unhealthy); err == nil {
		t.Fatal("SDK registration accepted an unhealthy native provider")
	}
	select {
	case event := <-providerA.EventChannel():
		t.Fatalf("adapter unexpectedly emitted event: %#v", event)
	default:
	}
	shutdownCtx := context.WithValue(ctx, sdkShutdownContextKey{}, "sdk-application-shutdown")
	if err := shutdown(shutdownCtx); err != nil {
		t.Fatalf("bounded SDK shutdown: %v", err)
	}
	if owned.closes.Load() != 1 || owned.closeContext.Load() != "sdk-application-shutdown" || borrowed.closes.Load() != 0 {
		t.Fatalf("SDK shutdown ownership/context: owned=%d context=%v borrowed=%d", owned.closes.Load(), owned.closeContext.Load(), borrowed.closes.Load())
	}
	for _, provider := range []*Provider{providerA, providerB} {
		select {
		case _, ok := <-provider.EventChannel():
			if ok {
				t.Error("adapter event channel remained open after SDK shutdown")
			}
		default:
			t.Error("adapter event channel did not close after SDK shutdown")
		}
		if err := provider.ShutdownWithContext(shutdownCtx); err != nil {
			t.Errorf("repeat adapter shutdown: %v", err)
		}
	}
	if owned.closes.Load() != 1 {
		t.Fatalf("repeat shutdown closed owned provider %d times", owned.closes.Load())
	}
	if _, err := borrowed.Snapshot(ctx, "tenant-b"); err != nil || !borrowed.Health(ctx).Healthy {
		t.Fatalf("SDK shutdown invalidated borrowed native provider: %v", err)
	}
}
