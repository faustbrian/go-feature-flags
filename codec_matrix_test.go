package featureflags

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestCodecRoundTripsEveryNativeValueAndStrategy(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	definitions := []Definition{
		{
			Key: "boolean", Type: TypeBoolean, Default: BooleanValue(false),
			Lifecycle: LifecycleActive, Variants: map[string]Value{"on": BooleanValue(true)},
			Strategies: []Strategy{
				ExactTargetStrategy{Name: "exact", Variant: "on", Tenants: []string{"tenant"}, Subjects: []string{"subject"}, Environments: []string{"production"}, Attributes: map[string]string{"plan": "pro"}},
				PercentageStrategy{Name: "rollout", Variant: "on", Seed: "v1", Threshold: 10_000},
				SetStrategy{Name: "set", Variant: "on", AllowTenants: []string{"tenant"}, DenyTenants: []string{"blocked"}, AllowSubjects: []string{"subject"}, DenySubjects: []string{"blocked"}},
				TimeWindowStrategy{Name: "window", Variant: "on", NotBefore: start, NotAfter: start.Add(time.Hour)},
				ScheduleStrategy{Name: "schedule", Variant: "on", Location: "UTC", Windows: []WeeklyWindow{{Weekday: time.Monday, StartMinute: 9 * 60, EndMinute: 10 * 60}}},
				FactStrategy{Name: "fact", Variant: "on", Fact: "eligible", Equals: BooleanValue(true)},
			},
		},
		{Key: "string", Type: TypeString, Default: StringValue("text"), Lifecycle: LifecycleActive},
		{Key: "integer", Type: TypeInteger, Default: IntegerValue(42), Lifecycle: LifecycleActive},
		{Key: "float", Type: TypeFloat, Default: FloatValue(1.25), Lifecycle: LifecycleActive},
		{Key: "decimal", Type: TypeDecimal, Default: DecimalValue("10.50"), Lifecycle: LifecycleActive},
		{Key: "structured", Type: TypeStructured, Default: StructuredValue(json.RawMessage(`{"enabled":true}`)), Lifecycle: LifecycleActive},
	}
	groups := []GroupDefinition{{
		Key: "release", Owner: "platform", Metadata: map[string]string{"scope": "checkout"},
		Tags: []string{"stable"}, Version: 3,
	}}
	data, err := Export(definitions, groups, DefaultLimits())
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	decodedDefinitions, decodedGroups, err := Import(data, DefaultLimits())
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if len(decodedDefinitions) != len(definitions) || len(decodedGroups) != 1 {
		t.Fatalf("Import() counts = (%d, %d)", len(decodedDefinitions), len(decodedGroups))
	}
	if len(decodedDefinitions[0].Strategies) != 6 {
		t.Fatalf("decoded strategies = %d, want 6", len(decodedDefinitions[0].Strategies))
	}
	for _, definition := range decodedDefinitions {
		switch definition.Type {
		case TypeBoolean:
			_, _ = definition.Default.Boolean()
		case TypeString:
			_, _ = definition.Default.String()
		case TypeInteger:
			value, ok := definition.Default.Integer()
			if !ok || value != 42 {
				t.Fatalf("Integer() = (%d, %t)", value, ok)
			}
		case TypeFloat:
			value, ok := definition.Default.Float()
			if !ok || value != 1.25 {
				t.Fatalf("Float() = (%f, %t)", value, ok)
			}
		case TypeDecimal:
			value, ok := definition.Default.Decimal()
			if !ok || value != "10.50" {
				t.Fatalf("Decimal() = (%q, %t)", value, ok)
			}
		case TypeStructured:
			value, ok := definition.Default.Structured()
			if !ok || string(value) != `{"enabled":true}` {
				t.Fatalf("Structured() = (%s, %t)", value, ok)
			}
		}
	}
}

func TestCodecRejectsMalformedValuesStrategiesAndDocuments(t *testing.T) {
	t.Parallel()

	for name, wire := range map[string]valueWire{
		"boolean":    {Type: TypeBoolean},
		"string":     {Type: TypeString},
		"integer":    {Type: TypeInteger},
		"float":      {Type: TypeFloat},
		"decimal":    {Type: TypeDecimal},
		"structured": {Type: TypeStructured},
		"unknown":    {Type: Type("future")},
	} {
		t.Run("value "+name, func(t *testing.T) {
			if _, err := decodeValue(wire); !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("decodeValue() error = %v", err)
			}
		})
	}
	for name, wire := range map[string]strategyWire{
		"unknown":   {Kind: "future"},
		"fact":      {Kind: "fact", Name: "fact"},
		"notBefore": {Kind: "time_window", NotBefore: "invalid"},
		"notAfter":  {Kind: "time_window", NotAfter: "invalid"},
	} {
		t.Run("strategy "+name, func(t *testing.T) {
			if _, err := decodeStrategy(wire); err == nil {
				t.Fatal("decodeStrategy() succeeded")
			}
		})
	}
	if _, _, err := Import([]byte(`{"format":"go-feature-flags","version":1,"features":[]} trailing`), DefaultLimits()); err == nil {
		t.Fatal("Import(trailing data) succeeded")
	}
	if _, _, err := Import([]byte(`{"format":"other","version":1,"features":[]}`), DefaultLimits()); err == nil {
		t.Fatal("Import(unknown format) succeeded")
	}
	if _, _, err := Import([]byte(`{"format":"go-feature-flags","version":1,"features":[],"unknown":true}`), DefaultLimits()); err == nil {
		t.Fatal("Import(unknown field) succeeded")
	}
	limits := DefaultLimits()
	limits.MaxImportBytes = 1
	if _, _, err := Import([]byte(`{}`), limits); !errors.Is(err, ErrImportLimit) {
		t.Fatalf("Import(oversized) error = %v", err)
	}
}
