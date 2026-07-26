package featureflags

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestEvaluationGuardsRejectCorruptInternalGraphs(t *testing.T) {
	t.Parallel()

	definition := Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
		Lifecycle: LifecycleActive,
		Variants:  map[string]Value{"on": BooleanValue(true)},
	}
	snapshot := Snapshot{
		definitions: map[string]Definition{"flag": definition},
		groups:      map[string]GroupDefinition{},
		limits:      DefaultLimits(),
	}
	if _, err := snapshot.evaluateKey("flag", Context{}, nil, snapshot.limits.MaxEvaluationDepth+1); err == nil {
		t.Fatal("evaluateKey(depth) succeeded")
	}
	if _, err := snapshot.evaluateKey("flag", Context{}, map[string]bool{"flag": true}, 0); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("evaluateKey(cycle) error = %v", err)
	}
	if _, err := snapshot.evaluateKey("missing", Context{}, nil, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("evaluateKey(missing) error = %v", err)
	}
	inactive := definition
	inactive.Lifecycle = LifecycleInactive
	snapshot.definitions["inactive"] = inactive
	if result, err := snapshot.evaluateKey("inactive", Context{}, make(map[string]bool), 0); err != nil || result.reason != ReasonInactive {
		t.Fatalf("evaluateKey(inactive) = (%#v, %v)", result, err)
	}
	dependent := definition
	dependent.Dependencies = []Dependency{{FeatureKey: "missing", RequiredVariant: "on"}}
	snapshot.definitions["dependent"] = dependent
	if _, err := snapshot.evaluateKey("dependent", Context{}, make(map[string]bool), 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("evaluateKey(corrupt dependency) error = %v", err)
	}
	grouped := definition
	grouped.Groups = []string{"bad"}
	snapshot.definitions["grouped"] = grouped
	snapshot.groups["bad"] = GroupDefinition{Key: "bad", Strategies: []Strategy{failingStrategy{}}}
	if _, err := snapshot.evaluateKey("grouped", Context{}, make(map[string]bool), 0); err == nil {
		t.Fatal("evaluateKey(group strategy failure) succeeded")
	}
	if _, _, err := snapshot.evaluateGroup(definition, "bad", Context{}, nil, snapshot.limits.MaxGroupDepth+1); err == nil {
		t.Fatal("evaluateGroup(depth) succeeded")
	}
	if _, matched, err := snapshot.evaluateGroup(
		definition, "bad", Context{}, map[string]bool{"bad": true}, 0,
	); err != nil || matched {
		t.Fatalf("evaluateGroup(visited) = (%t, %v)", matched, err)
	}
}

func TestImportPropagatesNestedDocumentFailures(t *testing.T) {
	t.Parallel()

	validBoolean := true
	documents := []documentWire{
		{
			Format: documentFormat, Version: documentVersion,
			Features: []definitionWire{{Key: "flag", Default: valueWire{Type: TypeBoolean}}},
		},
		{
			Format: documentFormat, Version: documentVersion,
			Groups: []groupWire{{Key: "group", Strategies: []strategyWire{{Kind: "future"}}}},
		},
		{
			Format: documentFormat, Version: documentVersion,
			Features: []definitionWire{{
				Key: "flag", Type: TypeString,
				Default: valueWire{Type: TypeBoolean, Boolean: &validBoolean},
			}},
		},
	}
	for index, document := range documents {
		data, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("json.Marshal(%d) error = %v", index, err)
		}
		if _, _, err := Import(data, DefaultLimits()); err == nil {
			t.Fatalf("Import(invalid %d) succeeded", index)
		}
	}
}

func TestCodecSortsDependenciesAndGroupsDeterministically(t *testing.T) {
	t.Parallel()

	definition := Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
		Dependencies: []Dependency{
			{FeatureKey: "b", RequiredVariant: "z"},
			{FeatureKey: "a", RequiredVariant: "z"},
			{FeatureKey: "a", RequiredVariant: "a"},
		},
	}
	wire, err := encodeDefinition(definition)
	if err != nil {
		t.Fatalf("encodeDefinition() error = %v", err)
	}
	if wire.Dependencies[0].RequiredVariant != "a" || wire.Dependencies[2].FeatureKey != "b" {
		t.Fatalf("dependency order = %#v", wire.Dependencies)
	}
	decoded, err := decodeDefinition(wire)
	if err != nil || len(decoded.Dependencies) != 3 {
		t.Fatalf("decodeDefinition() = (%#v, %v)", decoded, err)
	}
	data, err := Export(nil, []GroupDefinition{{Key: "z"}, {Key: "a"}}, DefaultLimits())
	if err != nil {
		t.Fatalf("Export(groups) error = %v", err)
	}
	if string(data) == "" {
		t.Fatal("Export(groups) returned no data")
	}
	invalidEquals := valueWire{Type: TypeBoolean}
	if _, err := decodeStrategy(strategyWire{Kind: "fact", Equals: &invalidEquals}); err == nil {
		t.Fatal("decodeStrategy(invalid fact value) succeeded")
	}
}
