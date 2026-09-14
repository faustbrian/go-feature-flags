// Package openfeature exposes the native engine through the OpenFeature Go
// provider contract without making OpenFeature the product boundary.
package openfeature

import (
	"context"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"sync"
	"time"

	featureflags "github.com/faustbrian/go-feature-flags"
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
	limits featureflags.Limits
}

var _ of.FeatureProvider = (*Provider)(nil)
var _ of.ContextAwareStateHandler = (*Provider)(nil)
var _ of.EventHandler = (*Provider)(nil)

func New(native featureflags.Provider, tenant string, options Options) (*Provider, error) {
	limits := featureflags.DefaultLimits()
	if isNil(native) || tenant == "" {
		return nil, fmt.Errorf("native provider and tenant are required")
	}
	if len(tenant) > limits.MaxKeyBytes {
		return nil, fmt.Errorf("tenant exceeds %d bytes: %w", limits.MaxKeyBytes, featureflags.ErrContextLimit)
	}

	return &Provider{
		native: native,
		tenant: tenant,
		hooks:  append([]of.Hook(nil), options.Hooks...),
		own:    options.OwnProvider,
		events: make(chan of.Event, 1),
		limits: limits,
	}, nil
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
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
	limits := provider.configuredLimits()
	if err := provider.validateContextShape(flat, limits); err != nil {
		return featureflags.Context{}, err
	}
	contextValue := featureflags.Context{
		Tenant:     provider.tenant,
		Attributes: make(map[string]string),
		Facts:      make(map[string]featureflags.Value),
	}
	if value, exists := flat[string(of.TargetingKey)]; exists {
		subject, ok := value.(string)
		if !ok {
			return featureflags.Context{}, fmt.Errorf("targeting key must be a string: %w", featureflags.ErrContextLimit)
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
				return featureflags.Context{}, fmt.Errorf("environment must be a string: %w", featureflags.ErrContextLimit)
			}
			contextValue.Environment = environment
			continue
		case "time":
			evaluationTime, ok := value.(time.Time)
			if !ok {
				return featureflags.Context{}, fmt.Errorf("time must be time.Time: %w", featureflags.ErrContextLimit)
			}
			contextValue.Time = evaluationTime
			continue
		}
		fact, err := mapFactWithLimits(value, limits)
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

func (provider *Provider) configuredLimits() featureflags.Limits {
	if provider.limits.MaxContextKeyBytes <= 0 {
		return featureflags.DefaultLimits()
	}

	return provider.limits
}

func (provider *Provider) validateContextShape(flat of.FlattenedContext, limits featureflags.Limits) error {
	const reservedContextEntries = 4
	if len(flat) > limits.MaxFacts+reservedContextEntries {
		return fmt.Errorf("context entries exceed configured bounds: %w", featureflags.ErrContextLimit)
	}

	facts := 0
	attributes := 0
	for key, value := range flat {
		if len(key) > limits.MaxContextKeyBytes {
			return fmt.Errorf("context key exceeds %d bytes: %w", limits.MaxContextKeyBytes, featureflags.ErrContextLimit)
		}
		switch key {
		case string(of.TargetingKey), "environment":
			if text, ok := value.(string); ok && len(text) > limits.MaxContextValueBytes {
				return fmt.Errorf("context identity exceeds %d bytes: %w", limits.MaxContextValueBytes, featureflags.ErrContextLimit)
			}
		case "tenant", "time":
		default:
			facts++
			if _, ok := value.(string); ok {
				attributes++
			}
			if facts > limits.MaxFacts || attributes > limits.MaxAttributes {
				return fmt.Errorf("context entries exceed configured bounds: %w", featureflags.ErrContextLimit)
			}
		}
	}

	return nil
}

func mapFact(value any) (featureflags.Value, error) {
	return mapFactWithLimits(value, featureflags.DefaultLimits())
}

func mapFactWithLimits(value any, limits featureflags.Limits) (featureflags.Value, error) {
	return mapFactWithLimitsAndMarshal(value, limits, json.Marshal)
}

func mapFactWithLimitsAndMarshal(
	value any,
	limits featureflags.Limits,
	marshal func(any) ([]byte, error),
) (featureflags.Value, error) {
	switch typed := value.(type) {
	case bool:
		return featureflags.BooleanValue(typed), nil
	case string:
		if len(typed) > limits.MaxStringBytes || len(typed) > limits.MaxContextValueBytes {
			return featureflags.Value{}, fmt.Errorf("string fact exceeds configured bounds: %w", featureflags.ErrContextLimit)
		}
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
		if math.IsNaN(float64(typed)) || math.IsInf(float64(typed), 0) {
			return featureflags.Value{}, fmt.Errorf("float fact must be finite: %w", featureflags.ErrContextLimit)
		}
		return featureflags.FloatValue(float64(typed)), nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return featureflags.Value{}, fmt.Errorf("float fact must be finite: %w", featureflags.ErrContextLimit)
		}
		return featureflags.FloatValue(typed), nil
	case json.RawMessage:
		if len(typed) > limits.MaxStructuredBytes || !json.Valid(typed) {
			return featureflags.Value{}, fmt.Errorf("structured fact exceeds configured bounds or is invalid: %w", featureflags.ErrContextLimit)
		}
		return featureflags.StructuredValue(typed), nil
	default:
		if err := validateStructuredInput(value, limits, 0, &structuredBudget{}); err != nil {
			return featureflags.Value{}, err
		}
		encoded, err := marshal(value)
		if err != nil {
			return featureflags.Value{}, fmt.Errorf("encode structured fact: %w", err)
		}
		if len(encoded) > limits.MaxStructuredBytes {
			return featureflags.Value{}, fmt.Errorf("structured fact exceeds %d bytes: %w", limits.MaxStructuredBytes, featureflags.ErrContextLimit)
		}
		return featureflags.StructuredValue(encoded), nil
	}
}

type structuredBudget struct {
	bytes int
	nodes int
	seen  map[structuredVisit]struct{}
}

func validateStructuredInput(value any, limits featureflags.Limits, depth int, budget *structuredBudget) error {
	return validateStructuredValue(reflect.ValueOf(value), limits, depth, budget)
}

var (
	jsonMarshalerType = reflect.TypeFor[json.Marshaler]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
	rawMessageType    = reflect.TypeFor[json.RawMessage]()
)

type structuredVisit struct {
	typeOf  reflect.Type
	pointer uintptr
}

func validateStructuredValue(value reflect.Value, limits featureflags.Limits, depth int, budget *structuredBudget) error {
	if depth > limits.MaxEvaluationDepth {
		return fmt.Errorf("structured fact depth exceeds %d: %w", limits.MaxEvaluationDepth, featureflags.ErrContextLimit)
	}
	if budget.nodes >= limits.MaxStructuredBytes {
		return fmt.Errorf("structured fact complexity exceeds configured bounds: %w", featureflags.ErrContextLimit)
	}
	budget.nodes++
	if !value.IsValid() {
		return addStructuredBytes(4, limits, budget)
	}
	for value.Kind() == reflect.Interface {
		if value.IsNil() {
			return addStructuredBytes(4, limits, budget)
		}
		value = value.Elem()
	}
	if value.Type() == rawMessageType {
		return fmt.Errorf("nested raw JSON is unsupported: %w", featureflags.ErrContextLimit)
	}
	if implementsCustomEncoding(value.Type()) {
		return fmt.Errorf("custom structured encoders are unsupported: %w", featureflags.ErrContextLimit)
	}

	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return addStructuredBytes(4, limits, budget)
		}
		leave, err := enterStructuredValue(value, budget)
		if err != nil {
			return err
		}
		defer leave()

		return validateStructuredValue(value.Elem(), limits, depth+1, budget)
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("structured map keys must be strings: %w", featureflags.ErrContextLimit)
		}
		if value.IsNil() {
			return addStructuredBytes(4, limits, budget)
		}
		leave, err := enterStructuredValue(value, budget)
		if err != nil {
			return err
		}
		defer leave()
		if err := addStructuredBytes(2, limits, budget); err != nil {
			return err
		}
		iterator := value.MapRange()
		for iterator.Next() {
			if err := addStructuredString(iterator.Key().String(), limits, budget); err != nil {
				return err
			}
			if err := addStructuredBytes(2, limits, budget); err != nil {
				return err
			}
			if err := validateStructuredValue(iterator.Value(), limits, depth+1, budget); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if value.IsNil() {
			return addStructuredBytes(4, limits, budget)
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			if err := addStructuredBytes(2, limits, budget); err != nil {
				return err
			}
			groups := value.Len() / 3
			if groups > limits.MaxStructuredBytes/4 {
				return fmt.Errorf("structured byte slice encoding exceeds %d bytes: %w", limits.MaxStructuredBytes, featureflags.ErrContextLimit)
			}
			if err := addStructuredBytes(groups*4, limits, budget); err != nil {
				return err
			}
			if value.Len()%3 == 0 {
				return nil
			}

			return addStructuredBytes(4, limits, budget)
		}
		leave, err := enterStructuredValue(value, budget)
		if err != nil {
			return err
		}
		defer leave()
		fallthrough
	case reflect.Array:
		if err := addStructuredBytes(2, limits, budget); err != nil {
			return err
		}
		for index := range value.Len() {
			if index > 0 {
				if err := addStructuredBytes(1, limits, budget); err != nil {
					return err
				}
			}
			if err := validateStructuredValue(value.Index(index), limits, depth+1, budget); err != nil {
				return err
			}
		}
	case reflect.String:
		return addStructuredString(value.String(), limits, budget)
	case reflect.Bool:
		return addStructuredBytes(5, limits, budget)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return addStructuredBytes(20, limits, budget)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return addStructuredBytes(20, limits, budget)
	case reflect.Float32, reflect.Float64:
		if number := value.Float(); math.IsNaN(number) || math.IsInf(number, 0) {
			return fmt.Errorf("structured float must be finite: %w", featureflags.ErrContextLimit)
		}

		return addStructuredBytes(32, limits, budget)
	default:
		return fmt.Errorf("structured value type is unsupported: %w", featureflags.ErrContextLimit)
	}

	return nil
}

func implementsCustomEncoding(typeOf reflect.Type) bool {
	if typeOf.Implements(jsonMarshalerType) || typeOf.Implements(textMarshalerType) {
		return true
	}
	if typeOf.Kind() != reflect.Pointer {
		pointerType := reflect.PointerTo(typeOf)

		return pointerType.Implements(jsonMarshalerType) || pointerType.Implements(textMarshalerType)
	}

	return false
}

func enterStructuredValue(value reflect.Value, budget *structuredBudget) (func(), error) {
	visit := structuredVisit{typeOf: value.Type(), pointer: value.Pointer()}
	if budget.seen == nil {
		budget.seen = make(map[structuredVisit]struct{})
	}
	if _, exists := budget.seen[visit]; exists {
		return nil, fmt.Errorf("structured fact contains a cycle: %w", featureflags.ErrContextLimit)
	}
	budget.seen[visit] = struct{}{}

	return func() { delete(budget.seen, visit) }, nil
}

func addStructuredString(value string, limits featureflags.Limits, budget *structuredBudget) error {
	return addStructuredStringWithEncoder(value, limits, budget, func(value string) []byte {
		encoded, _ := json.Marshal(value)

		return encoded
	})
}

func addStructuredStringWithEncoder(
	value string,
	limits featureflags.Limits,
	budget *structuredBudget,
	encode func(string) []byte,
) error {
	if len(value) > limits.MaxStructuredBytes {
		return fmt.Errorf("structured fact exceeds %d input bytes: %w", limits.MaxStructuredBytes, featureflags.ErrContextLimit)
	}

	return addStructuredBytes(len(encode(value)), limits, budget)
}

func addStructuredBytes(count int, limits featureflags.Limits, budget *structuredBudget) error {
	if count < 0 || budget.bytes > limits.MaxStructuredBytes || count > limits.MaxStructuredBytes-budget.bytes {
		return fmt.Errorf("structured fact exceeds %d input bytes: %w", limits.MaxStructuredBytes, featureflags.ErrContextLimit)
	}
	budget.bytes += count

	return nil
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
