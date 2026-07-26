// Package openfeature exposes the native engine through the OpenFeature Go
// provider contract without making OpenFeature the product boundary.
package openfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	featureflags "github.com/faustbrian/golib/pkg/feature-flags"
	of "github.com/open-feature/go-sdk/openfeature"
)

type Options struct {
	Hooks       []of.Hook
	OwnProvider bool
}

// Provider is an optional OpenFeature adapter bound to exactly one tenant.
type Provider struct {
	native featureflags.Provider
	tenant string
	hooks  []of.Hook
	own    bool
	events chan of.Event
	once   sync.Once
}

var _ of.FeatureProvider = (*Provider)(nil)
var _ of.ContextAwareStateHandler = (*Provider)(nil)
var _ of.EventHandler = (*Provider)(nil)

func New(native featureflags.Provider, tenant string, options Options) (*Provider, error) {
	if native == nil || tenant == "" {
		return nil, fmt.Errorf("native provider and tenant are required")
	}

	return &Provider{
		native: native,
		tenant: tenant,
		hooks:  append([]of.Hook(nil), options.Hooks...),
		own:    options.OwnProvider,
		events: make(chan of.Event, 1),
	}, nil
}

func (*Provider) Metadata() of.Metadata { return of.Metadata{Name: "go-feature-flags"} }

func (provider *Provider) Hooks() []of.Hook { return append([]of.Hook(nil), provider.hooks...) }

func (provider *Provider) EventChannel() <-chan of.Event { return provider.events }

func (provider *Provider) Init(of.EvaluationContext) error {
	return provider.InitWithContext(context.Background(), of.EvaluationContext{})
}

func (provider *Provider) InitWithContext(ctx context.Context, _ of.EvaluationContext) error {
	health := provider.native.Health(ctx)
	if !health.Healthy {
		return fmt.Errorf("native provider is not ready: %s", health.Code)
	}

	return nil
}

func (provider *Provider) Shutdown() { _ = provider.ShutdownWithContext(context.Background()) }

func (provider *Provider) ShutdownWithContext(ctx context.Context) error {
	var err error
	provider.once.Do(func() {
		if provider.own {
			err = provider.native.Close(ctx)
		}
		close(provider.events)
	})

	return err
}

func (provider *Provider) BooleanEvaluation(
	ctx context.Context,
	flag string,
	defaultValue bool,
	flat of.FlattenedContext,
) of.BoolResolutionDetail {
	snapshot, nativeContext, err := provider.snapshotAndContext(ctx, flat)
	if err != nil {
		return of.BoolResolutionDetail{Value: defaultValue, ProviderResolutionDetail: errorDetail(err)}
	}
	detail, err := snapshot.Boolean(flag, nativeContext)
	if err != nil {
		return of.BoolResolutionDetail{Value: defaultValue, ProviderResolutionDetail: errorDetail(err)}
	}

	return of.BoolResolutionDetail{Value: detail.Value, ProviderResolutionDetail: mapDetail(detail.Variant, detail.Reason, detail.MatchedStrategy, detail.Version)}
}

func (provider *Provider) StringEvaluation(
	ctx context.Context,
	flag, defaultValue string,
	flat of.FlattenedContext,
) of.StringResolutionDetail {
	snapshot, nativeContext, err := provider.snapshotAndContext(ctx, flat)
	if err != nil {
		return of.StringResolutionDetail{Value: defaultValue, ProviderResolutionDetail: errorDetail(err)}
	}
	detail, err := snapshot.String(flag, nativeContext)
	if err != nil {
		return of.StringResolutionDetail{Value: defaultValue, ProviderResolutionDetail: errorDetail(err)}
	}

	return of.StringResolutionDetail{Value: detail.Value, ProviderResolutionDetail: mapDetail(detail.Variant, detail.Reason, detail.MatchedStrategy, detail.Version)}
}

func (provider *Provider) FloatEvaluation(
	ctx context.Context,
	flag string,
	defaultValue float64,
	flat of.FlattenedContext,
) of.FloatResolutionDetail {
	snapshot, nativeContext, err := provider.snapshotAndContext(ctx, flat)
	if err != nil {
		return of.FloatResolutionDetail{Value: defaultValue, ProviderResolutionDetail: errorDetail(err)}
	}
	detail, err := snapshot.Float(flag, nativeContext)
	if err != nil {
		return of.FloatResolutionDetail{Value: defaultValue, ProviderResolutionDetail: errorDetail(err)}
	}

	return of.FloatResolutionDetail{Value: detail.Value, ProviderResolutionDetail: mapDetail(detail.Variant, detail.Reason, detail.MatchedStrategy, detail.Version)}
}

func (provider *Provider) IntEvaluation(
	ctx context.Context,
	flag string,
	defaultValue int64,
	flat of.FlattenedContext,
) of.IntResolutionDetail {
	snapshot, nativeContext, err := provider.snapshotAndContext(ctx, flat)
	if err != nil {
		return of.IntResolutionDetail{Value: defaultValue, ProviderResolutionDetail: errorDetail(err)}
	}
	detail, err := snapshot.Integer(flag, nativeContext)
	if err != nil {
		return of.IntResolutionDetail{Value: defaultValue, ProviderResolutionDetail: errorDetail(err)}
	}

	return of.IntResolutionDetail{Value: detail.Value, ProviderResolutionDetail: mapDetail(detail.Variant, detail.Reason, detail.MatchedStrategy, detail.Version)}
}

func (provider *Provider) ObjectEvaluation(
	ctx context.Context,
	flag string,
	defaultValue any,
	flat of.FlattenedContext,
) of.InterfaceResolutionDetail {
	snapshot, nativeContext, err := provider.snapshotAndContext(ctx, flat)
	if err != nil {
		return of.InterfaceResolutionDetail{Value: defaultValue, ProviderResolutionDetail: errorDetail(err)}
	}
	detail, err := snapshot.Structured(flag, nativeContext)
	if err != nil {
		return of.InterfaceResolutionDetail{Value: defaultValue, ProviderResolutionDetail: errorDetail(err)}
	}
	value, _ := decodeObject(detail.Value)

	return of.InterfaceResolutionDetail{Value: value, ProviderResolutionDetail: mapDetail(detail.Variant, detail.Reason, detail.MatchedStrategy, detail.Version)}
}

func decodeObject(data json.RawMessage) (any, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}

	return value, nil
}

func (provider *Provider) snapshotAndContext(
	ctx context.Context,
	flat of.FlattenedContext,
) (featureflags.Snapshot, featureflags.Context, error) {
	nativeContext, err := provider.mapContext(flat)
	if err != nil {
		return featureflags.Snapshot{}, featureflags.Context{}, err
	}
	snapshot, err := provider.native.Snapshot(ctx, provider.tenant)
	if err != nil {
		return featureflags.Snapshot{}, featureflags.Context{}, err
	}

	return snapshot, nativeContext, nil
}

func (provider *Provider) mapContext(flat of.FlattenedContext) (featureflags.Context, error) {
	contextValue := featureflags.Context{
		Tenant:     provider.tenant,
		Attributes: make(map[string]string),
		Facts:      make(map[string]featureflags.Value),
	}
	if value, exists := flat[string(of.TargetingKey)]; exists {
		subject, ok := value.(string)
		if !ok {
			return featureflags.Context{}, fmt.Errorf("targeting key must be a string")
		}
		contextValue.Subject = subject
	}
	for key, value := range flat {
		switch key {
		case string(of.TargetingKey):
			continue
		case "tenant":
			tenant, ok := value.(string)
			if !ok || tenant != provider.tenant {
				return featureflags.Context{}, featureflags.ErrTenantMismatch
			}
			continue
		case "environment":
			environment, ok := value.(string)
			if !ok {
				return featureflags.Context{}, fmt.Errorf("environment must be a string")
			}
			contextValue.Environment = environment
			continue
		case "time":
			evaluationTime, ok := value.(time.Time)
			if !ok {
				return featureflags.Context{}, fmt.Errorf("time must be time.Time")
			}
			contextValue.Time = evaluationTime
			continue
		}
		fact, err := mapFact(value)
		if err != nil {
			return featureflags.Context{}, fmt.Errorf("attribute %q: %w", key, err)
		}
		contextValue.Facts[key] = fact
		if text, ok := value.(string); ok {
			contextValue.Attributes[key] = text
		}
	}

	return contextValue, nil
}

func mapFact(value any) (featureflags.Value, error) {
	switch typed := value.(type) {
	case bool:
		return featureflags.BooleanValue(typed), nil
	case string:
		return featureflags.StringValue(typed), nil
	case int:
		return featureflags.IntegerValue(int64(typed)), nil
	case int8:
		return featureflags.IntegerValue(int64(typed)), nil
	case int16:
		return featureflags.IntegerValue(int64(typed)), nil
	case int32:
		return featureflags.IntegerValue(int64(typed)), nil
	case int64:
		return featureflags.IntegerValue(typed), nil
	case uint:
		if uint64(typed) > math.MaxInt64 {
			return featureflags.Value{}, fmt.Errorf("unsigned integer exceeds int64")
		}
		return featureflags.IntegerValue(int64(typed)), nil
	case uint8:
		return featureflags.IntegerValue(int64(typed)), nil
	case uint16:
		return featureflags.IntegerValue(int64(typed)), nil
	case uint32:
		return featureflags.IntegerValue(int64(typed)), nil
	case uint64:
		if typed > math.MaxInt64 {
			return featureflags.Value{}, fmt.Errorf("unsigned integer exceeds int64")
		}
		return featureflags.IntegerValue(int64(typed)), nil
	case float32:
		return featureflags.FloatValue(float64(typed)), nil
	case float64:
		return featureflags.FloatValue(typed), nil
	case json.RawMessage:
		return featureflags.StructuredValue(typed), nil
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return featureflags.Value{}, fmt.Errorf("encode structured fact: %w", err)
		}
		return featureflags.StructuredValue(encoded), nil
	}
}

func mapDetail(variant string, reason featureflags.Reason, strategy string, version uint64) of.ProviderResolutionDetail {
	var versionMetadata any = int64(version)
	if version > math.MaxInt64 {
		versionMetadata = strconv.FormatUint(version, 10)
	}
	metadata := of.FlagMetadata{"version": versionMetadata}
	if strategy != "" {
		metadata["matchedStrategy"] = strategy
	}

	return of.ProviderResolutionDetail{
		Reason: mapReason(reason), Variant: variant, FlagMetadata: metadata,
	}
}

func mapReason(reason featureflags.Reason) of.Reason {
	switch reason {
	case featureflags.ReasonDefault, featureflags.ReasonDependencyFailed:
		return of.DefaultReason
	case featureflags.ReasonInactive:
		return of.DisabledReason
	case featureflags.ReasonRollout:
		return of.SplitReason
	case featureflags.ReasonTargetingMatch, featureflags.ReasonSchedule, featureflags.ReasonGroupMatch:
		return of.TargetingMatchReason
	default:
		return of.UnknownReason
	}
}

func errorDetail(err error) of.ProviderResolutionDetail {
	var resolution of.ResolutionError
	switch {
	case errors.Is(err, featureflags.ErrNotFound):
		resolution = of.NewFlagNotFoundResolutionError("flag not found")
	case errors.Is(err, featureflags.ErrTenantMismatch), errors.Is(err, featureflags.ErrContextLimit):
		resolution = of.NewInvalidContextResolutionError("invalid evaluation context")
	default:
		resolution = of.NewGeneralResolutionError("native evaluation failed")
	}

	return of.ProviderResolutionDetail{Reason: of.ErrorReason, ResolutionError: resolution}
}
