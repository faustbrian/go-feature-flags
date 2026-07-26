package featureflags

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestDefinitionValidationRejectsEveryBoundedCollectionAndReference(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxKeyBytes = 4
	limits.MaxVariants = 1
	limits.MaxMetadata = 1
	limits.MaxTags = 1
	limits.MaxStrategies = 1
	limits.MaxDependencies = 1
	limits.MaxGroups = 1
	limits.MaxStringBytes = 4
	base := Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
		Variants: map[string]Value{"on": BooleanValue(true)},
	}
	tests := map[string]func() Definition{
		"long key":   func() Definition { value := base; value.Key = "flags"; return value },
		"long owner": func() Definition { value := base; value.Owner = "owner"; return value },
		"too many variants": func() Definition {
			value := base
			value.Variants = map[string]Value{"on": BooleanValue(true), "no": BooleanValue(false)}
			return value
		},
		"too much metadata": func() Definition { value := base; value.Metadata = map[string]string{"a": "b", "c": "d"}; return value },
		"too many tags":     func() Definition { value := base; value.Tags = []string{"a", "b"}; return value },
		"too many strategies": func() Definition {
			value := base
			value.Strategies = []Strategy{ExactTargetStrategy{Name: "a", Variant: "on"}, ExactTargetStrategy{Name: "b", Variant: "on"}}
			return value
		},
		"too many dependencies": func() Definition {
			value := base
			value.Dependencies = []Dependency{{FeatureKey: "a", RequiredVariant: "on"}, {FeatureKey: "b", RequiredVariant: "on"}}
			return value
		},
		"too many groups":  func() Definition { value := base; value.Groups = []string{"a", "b"}; return value },
		"empty dependency": func() Definition { value := base; value.Dependencies = []Dependency{{}}; return value },
		"long group":       func() Definition { value := base; value.Groups = []string{"group"}; return value },
		"empty variant": func() Definition {
			value := base
			value.Variants = map[string]Value{"": BooleanValue(true)}
			return value
		},
		"invalid variant": func() Definition {
			value := base
			value.Variants = map[string]Value{"on": FloatValue(math.Inf(1))}
			value.Type = TypeFloat
			value.Default = FloatValue(1)
			return value
		},
		"nil strategy": func() Definition { value := base; value.Strategies = []Strategy{nil}; return value },
		"unnamed strategy": func() Definition {
			value := base
			value.Strategies = []Strategy{ExactTargetStrategy{Variant: "on"}}
			return value
		},
		"unknown target": func() Definition {
			value := base
			value.Strategies = []Strategy{ExactTargetStrategy{Name: "rule", Variant: "off"}}
			return value
		},
		"invalid strategy": func() Definition {
			value := base
			value.Strategies = []Strategy{PercentageStrategy{Name: "rule", Variant: "on", Threshold: 100_001}}
			return value
		},
		"empty metadata key":  func() Definition { value := base; value.Metadata = map[string]string{"": "a"}; return value },
		"long metadata value": func() Definition { value := base; value.Metadata = map[string]string{"key": "value"}; return value },
		"empty tag":           func() Definition { value := base; value.Tags = []string{""}; return value },
	}
	for name, build := range tests {
		t.Run(name, func(t *testing.T) {
			if err := build().Validate(limits); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestContextValidationRejectsEveryCardinalityAndSizeBound(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxContextValueBytes = 2
	limits.MaxContextKeyBytes = 2
	limits.MaxAttributes = 1
	limits.MaxFacts = 1
	contexts := map[string]Context{
		"subject":         {Subject: "abc"},
		"tenant":          {Tenant: "abc"},
		"environment":     {Environment: "abc"},
		"attribute count": {Attributes: map[string]string{"a": "1", "b": "2"}},
		"fact count":      {Facts: map[string]Value{"a": BooleanValue(true), "b": BooleanValue(true)}},
		"attribute key":   {Attributes: map[string]string{"abc": "1"}},
		"attribute value": {Attributes: map[string]string{"a": "123"}},
		"fact key":        {Facts: map[string]Value{"abc": BooleanValue(true)}},
		"fact value":      {Facts: map[string]Value{"a": StructuredValue(json.RawMessage(`x`))}},
	}
	for name, contextValue := range contexts {
		t.Run(name, func(t *testing.T) {
			if err := contextValue.validate(limits); err == nil {
				t.Fatal("validate() succeeded")
			}
		})
	}
}

func TestStrategyValidationAndScheduleTruthTable(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxAttributes = 1
	limits.MaxContextKeyBytes = 2
	limits.MaxContextValueBytes = 2
	limits.MaxScheduleWindows = 1
	invalid := map[string]Strategy{
		"attribute count": ExactTargetStrategy{Attributes: map[string]string{"a": "1", "b": "2"}},
		"attribute key":   ExactTargetStrategy{Attributes: map[string]string{"abc": "1"}},
		"percentage":      PercentageStrategy{Threshold: 100_001},
		"empty time":      TimeWindowStrategy{},
		"reversed time":   TimeWindowStrategy{NotBefore: time.Unix(2, 0), NotAfter: time.Unix(1, 0)},
		"empty location":  ScheduleStrategy{},
		"bad location":    ScheduleStrategy{Location: "Mars/Olympus", Windows: []WeeklyWindow{{Weekday: time.Monday, EndMinute: 1}}},
		"empty windows":   ScheduleStrategy{Location: "UTC"},
		"too many windows": ScheduleStrategy{Location: "UTC", Windows: []WeeklyWindow{
			{Weekday: time.Monday, EndMinute: 1}, {Weekday: time.Tuesday, EndMinute: 1},
		}},
		"weekday": ScheduleStrategy{Location: "UTC", Windows: []WeeklyWindow{{Weekday: 8, EndMinute: 1}}},
		"minutes": ScheduleStrategy{Location: "UTC", Windows: []WeeklyWindow{{Weekday: time.Monday, StartMinute: 2, EndMinute: 2}}},
	}
	for name, strategy := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := strategy.ValidateStrategy(limits); err == nil {
				t.Fatal("ValidateStrategy() succeeded")
			}
		})
	}

	window := WeeklyWindow{Weekday: time.Saturday, StartMinute: 23 * 60, EndMinute: 60}
	truth := []struct {
		weekday time.Weekday
		minute  int
		match   bool
	}{
		{time.Saturday, 23 * 60, true},
		{time.Sunday, 30, true},
		{time.Sunday, 60, false},
		{time.Friday, 23 * 60, false},
	}
	for _, test := range truth {
		if got := weeklyWindowMatches(window, test.weekday, test.minute); got != test.match {
			t.Fatalf("weeklyWindowMatches(%s, %d) = %t", test.weekday, test.minute, got)
		}
	}
	strategy := ScheduleStrategy{Location: "Mars/Olympus"}
	if _, err := strategy.EvaluateStrategy(StrategyInput{Context: Context{Time: time.Now()}}); err == nil {
		t.Fatal("EvaluateStrategy(invalid location) succeeded")
	}
}

func TestValueAccessAndEqualityCoverEveryType(t *testing.T) {
	t.Parallel()

	values := []Value{
		BooleanValue(true), StringValue("value"), IntegerValue(1), FloatValue(1.5),
		DecimalValue("1.50"), StructuredValue(json.RawMessage(`{"ok":true}`)),
	}
	for _, value := range values {
		if !value.equal(value.clone()) {
			t.Fatalf("equal(%s) = false", value.Type())
		}
		if value.equal(Value{}) {
			t.Fatalf("equal(zero) for %s = true", value.Type())
		}
	}
	if (Value{}).equal(Value{}) {
		t.Fatal("two unknown values compared equal")
	}
	structured := values[len(values)-1]
	first, _ := structured.Structured()
	first[0] = 'x'
	second, _ := structured.Structured()
	if strings.HasPrefix(string(second), "x") {
		t.Fatal("Structured() returned aliased bytes")
	}
	if _, ok := BooleanValue(true).Integer(); ok {
		t.Fatal("Integer() accepted a boolean")
	}
	if _, ok := BooleanValue(true).Float(); ok {
		t.Fatal("Float() accepted a boolean")
	}
	if _, ok := BooleanValue(true).Decimal(); ok {
		t.Fatal("Decimal() accepted a boolean")
	}
	if _, ok := BooleanValue(true).Structured(); ok {
		t.Fatal("Structured() accepted a boolean")
	}
}

func TestTargetingAndGroupDefinitionsEnforceCompleteBounds(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxTargetValues = 1
	limits.MaxKeyBytes = 4
	limits.MaxStringBytes = 4
	limits.MaxContextValueBytes = 4
	limits.MaxMetadata = 1
	limits.MaxTags = 1
	for name, strategy := range map[string]Strategy{
		"exact count": ExactTargetStrategy{Tenants: []string{"a", "b"}},
		"exact value": ExactTargetStrategy{Subjects: []string{"value"}},
		"set count":   SetStrategy{AllowTenants: []string{"a"}, DenyTenants: []string{"b"}},
		"set value":   SetStrategy{AllowSubjects: []string{"value"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := strategy.ValidateStrategy(limits); err == nil {
				t.Fatal("ValidateStrategy() succeeded")
			}
		})
	}
	groups := map[string]GroupDefinition{
		"key":            {Key: "group"},
		"parent":         {Key: "ok", Parent: "group"},
		"owner":          {Key: "ok", Owner: "owner"},
		"metadata":       {Key: "ok", Metadata: map[string]string{"a": "b", "c": "d"}},
		"metadata value": {Key: "ok", Metadata: map[string]string{"a": "value"}},
		"tags":           {Key: "ok", Tags: []string{"a", "b"}},
		"tag value":      {Key: "ok", Tags: []string{"value"}},
		"strategy name": {Key: "ok", Strategies: []Strategy{
			ExactTargetStrategy{Variant: "on"},
		}},
		"strategy validation": {Key: "ok", Strategies: []Strategy{
			ExactTargetStrategy{Name: "rule", Variant: "on", Subjects: []string{"value"}},
		}},
	}
	for name, group := range groups {
		t.Run("group "+name, func(t *testing.T) {
			if _, err := NewSnapshotWithGroups(nil, []GroupDefinition{group}, limits); err == nil {
				t.Fatal("NewSnapshotWithGroups() succeeded")
			}
		})
	}
}
