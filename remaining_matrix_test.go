package featureflags

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

func TestFactAndPercentageStrategiesCoverMissingInput(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	for name, strategy := range map[string]FactStrategy{
		"fact":    {Equals: BooleanValue(true)},
		"equals":  {Fact: "eligible"},
		"invalid": {Fact: "eligible", Equals: StructuredValue(json.RawMessage(`x`))},
	} {
		t.Run(name, func(t *testing.T) {
			if err := strategy.ValidateStrategy(limits); err == nil {
				t.Fatal("ValidateStrategy() succeeded")
			}
		})
	}
	strategy := FactStrategy{Fact: "eligible", Equals: BooleanValue(true)}
	result, err := strategy.EvaluateStrategy(StrategyInput{})
	if err != nil || result.Match || len(result.Diagnostics) != 1 {
		t.Fatalf("EvaluateStrategy(missing fact) = (%#v, %v)", result, err)
	}
	percentage := PercentageStrategy{Threshold: 50_000}
	result, err = percentage.EvaluateStrategy(StrategyInput{})
	if err != nil || result.Match || len(result.Diagnostics) != 1 {
		t.Fatalf("PercentageStrategy(missing identity) = (%#v, %v)", result, err)
	}
	if got := formatOptionalTime(time.Time{}); got != "" {
		t.Fatalf("formatOptionalTime(zero) = %q", got)
	}
}

func TestEveryTypedEvaluatorRejectsWrongDefinitionType(t *testing.T) {
	t.Parallel()

	snapshot, err := NewSnapshot([]Definition{{
		Key: "boolean", Type: TypeBoolean, Default: BooleanValue(true),
	}}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}
	checks := []func() error{
		func() error { _, err := snapshot.String("boolean", Context{}); return err },
		func() error { _, err := snapshot.Integer("boolean", Context{}); return err },
		func() error { _, err := snapshot.Float("boolean", Context{}); return err },
		func() error { _, err := snapshot.Decimal("boolean", Context{}); return err },
		func() error { _, err := snapshot.Structured("boolean", Context{}); return err },
	}
	for index, check := range checks {
		if err := check(); err == nil {
			t.Fatalf("typed evaluator %d accepted a boolean definition", index)
		}
	}
}

func TestValueValidationCoversEveryInvalidRepresentation(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxStringBytes = 2
	limits.MaxStructuredBytes = 2
	values := []Value{
		StringValue("long"), FloatValue(math.NaN()), FloatValue(math.Inf(1)),
		DecimalValue("01"), DecimalValue("123"),
		StructuredValue(json.RawMessage(`{}` + " ")),
		StructuredValue(json.RawMessage(`x`)), Value{typ: Type("future")},
	}
	for index, value := range values {
		if err := value.validate(limits); err == nil {
			t.Fatalf("validate(invalid %d) succeeded", index)
		}
	}
}

func TestCodecPropagatesNestedValueAndStrategyErrors(t *testing.T) {
	t.Parallel()

	if _, err := encodeDefinition(Definition{
		Key: "flag", Strategies: []Strategy{diagnosticStrategy{}},
	}); err == nil {
		t.Fatal("encodeDefinition(custom strategy) succeeded")
	}
	if _, err := encodeGroup(GroupDefinition{
		Key: "group", Strategies: []Strategy{diagnosticStrategy{}},
	}); err == nil {
		t.Fatal("encodeGroup(custom strategy) succeeded")
	}
	invalidDefault := definitionWire{Key: "flag", Default: valueWire{Type: TypeBoolean}}
	if _, err := decodeDefinition(invalidDefault); err == nil {
		t.Fatal("decodeDefinition(invalid default) succeeded")
	}
	validBoolean := true
	invalidVariant := definitionWire{
		Key: "flag", Type: TypeBoolean,
		Default:  valueWire{Type: TypeBoolean, Boolean: &validBoolean},
		Variants: map[string]valueWire{"on": {Type: TypeString}},
	}
	if _, err := decodeDefinition(invalidVariant); err == nil {
		t.Fatal("decodeDefinition(invalid variant) succeeded")
	}
	invalidStrategy := invalidVariant
	invalidStrategy.Variants = nil
	invalidStrategy.Strategies = []strategyWire{{Kind: "future"}}
	if _, err := decodeDefinition(invalidStrategy); err == nil {
		t.Fatal("decodeDefinition(invalid strategy) succeeded")
	}
	if _, err := decodeGroup(groupWire{
		Key: "group", Strategies: []strategyWire{{Kind: "future"}},
	}); err == nil {
		t.Fatal("decodeGroup(invalid strategy) succeeded")
	}
	if _, err := Export([]Definition{{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(true),
		Variants:   map[string]Value{"enabled": BooleanValue(true)},
		Strategies: []Strategy{diagnosticStrategy{}},
	}}, nil, DefaultLimits()); err == nil {
		t.Fatal("Export(custom strategy) succeeded")
	}
	if _, err := Export(nil, []GroupDefinition{{
		Key: "group", Strategies: []Strategy{diagnosticStrategy{}},
	}}, DefaultLimits()); err == nil {
		t.Fatal("Export(custom group strategy) succeeded")
	}
	if _, err := Export([]Definition{{}}, nil, DefaultLimits()); err == nil {
		t.Fatal("Export(invalid definition) succeeded")
	}
	if err := (Definition{
		Key: "flag", Type: TypeString, Default: BooleanValue(true),
	}).Validate(DefaultLimits()); err == nil {
		t.Fatal("Validate(default type mismatch) succeeded")
	}
}
