package featureflags

import (
	"errors"
	"testing"
)

type failingStrategy struct{}

func (failingStrategy) StrategyName() string          { return "failure" }
func (failingStrategy) TargetVariant() string         { return "on" }
func (failingStrategy) ValidateStrategy(Limits) error { return nil }
func (failingStrategy) SnapshotStrategy() Strategy    { return failingStrategy{} }
func (failingStrategy) EvaluateStrategy(StrategyInput) (StrategyResult, error) {
	return StrategyResult{}, errors.New("strategy failure")
}

type reasonlessStrategy struct{ match bool }

func (reasonlessStrategy) StrategyName() string          { return "reasonless" }
func (reasonlessStrategy) TargetVariant() string         { return "on" }
func (reasonlessStrategy) ValidateStrategy(Limits) error { return nil }
func (strategy reasonlessStrategy) SnapshotStrategy() Strategy {
	return strategy
}
func (strategy reasonlessStrategy) EvaluateStrategy(StrategyInput) (StrategyResult, error) {
	return StrategyResult{Match: strategy.match}, nil
}

func TestSnapshotConstructionRejectsEveryGraphFailure(t *testing.T) {
	t.Parallel()

	base := Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
		Variants: map[string]Value{"on": BooleanValue(true)},
	}
	limits := DefaultLimits()
	limits.MaxFeatures = 1
	if _, err := NewSnapshot([]Definition{base, {Key: "other", Type: TypeBoolean, Default: BooleanValue(false)}}, limits); err == nil {
		t.Fatal("NewSnapshot(over feature limit) succeeded")
	}
	if _, err := NewSnapshot([]Definition{base, base}, DefaultLimits()); err == nil {
		t.Fatal("NewSnapshot(duplicate) succeeded")
	}
	invalid := base
	invalid.Key = ""
	if _, err := NewSnapshot([]Definition{invalid}, DefaultLimits()); err == nil {
		t.Fatal("NewSnapshot(invalid definition) succeeded")
	}
	missingDependency := base
	missingDependency.Dependencies = []Dependency{{FeatureKey: "missing", RequiredVariant: "on"}}
	if _, err := NewSnapshot([]Definition{missingDependency}, DefaultLimits()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("NewSnapshot(missing dependency) error = %v", err)
	}
	target := base
	target.Key = "target"
	unknownVariant := base
	unknownVariant.Dependencies = []Dependency{{FeatureKey: target.Key, RequiredVariant: "missing"}}
	if _, err := NewSnapshot([]Definition{unknownVariant, target}, DefaultLimits()); err == nil {
		t.Fatal("NewSnapshot(unknown dependency variant) succeeded")
	}
	depthLimits := DefaultLimits()
	depthLimits.MaxEvaluationDepth = 0
	dependent := base
	dependent.Dependencies = []Dependency{{FeatureKey: target.Key, RequiredVariant: "on"}}
	if _, err := NewSnapshot([]Definition{dependent, target}, depthLimits); err == nil {
		t.Fatal("NewSnapshot(dependency depth) succeeded")
	}
}

func TestGroupConstructionRejectsEveryInvalidShape(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxGroups = 1
	limits.MaxStrategies = 1
	tests := map[string][]GroupDefinition{
		"count":     {{Key: "a"}, {Key: "b"}},
		"empty key": {{}},
		"duplicate": {{Key: "a"}, {Key: "a"}},
		"strategies": {{Key: "a", Strategies: []Strategy{
			ExactTargetStrategy{Name: "a"}, ExactTargetStrategy{Name: "b"},
		}}},
		"nil strategy": {{Key: "a", Strategies: []Strategy{nil}}},
		"invalid strategy": {{Key: "a", Strategies: []Strategy{
			TimeWindowStrategy{Name: "time"},
		}}},
		"missing parent": {{Key: "a", Parent: "missing"}},
	}
	for name, groups := range tests {
		t.Run(name, func(t *testing.T) {
			testLimits := limits
			if name != "count" {
				testLimits.MaxGroups = 10
			}
			if _, err := NewSnapshotWithGroups(nil, groups, testLimits); err == nil {
				t.Fatal("NewSnapshotWithGroups() succeeded")
			}
		})
	}
	depthLimits := DefaultLimits()
	depthLimits.MaxGroupDepth = 0
	if _, err := NewSnapshotWithGroups(nil, []GroupDefinition{
		{Key: "parent"}, {Key: "child", Parent: "parent"},
	}, depthLimits); err == nil {
		t.Fatal("NewSnapshotWithGroups(depth) succeeded")
	}
	definition := Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
		Variants: map[string]Value{"on": BooleanValue(true)}, Groups: []string{"missing"},
	}
	if _, err := NewSnapshotWithGroups([]Definition{definition}, nil, DefaultLimits()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("NewSnapshotWithGroups(missing membership) error = %v", err)
	}
}

func TestEvaluationCoversStrategyErrorsDefaultsAndTypeFailures(t *testing.T) {
	t.Parallel()

	base := Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
		Lifecycle: LifecycleActive, Variants: map[string]Value{"on": BooleanValue(true)},
	}
	for name, strategy := range map[string]Strategy{
		"failure":    failingStrategy{},
		"unmatched":  reasonlessStrategy{},
		"reasonless": reasonlessStrategy{match: true},
	} {
		t.Run(name, func(t *testing.T) {
			definition := base
			definition.Strategies = []Strategy{strategy}
			snapshot, err := NewSnapshot([]Definition{definition}, DefaultLimits())
			if err != nil {
				t.Fatalf("NewSnapshot() error = %v", err)
			}
			detail, evaluationErr := snapshot.Boolean("flag", Context{})
			if name == "failure" {
				if evaluationErr == nil {
					t.Fatal("Boolean() succeeded")
				}
				return
			}
			if evaluationErr != nil {
				t.Fatalf("Boolean() error = %v", evaluationErr)
			}
			if name == "unmatched" && detail.Reason != ReasonDefault {
				t.Fatalf("Boolean(unmatched) reason = %q", detail.Reason)
			}
			if name == "reasonless" && detail.Reason != ReasonTargetingMatch {
				t.Fatalf("Boolean(reasonless) reason = %q", detail.Reason)
			}
		})
	}
	snapshot, err := NewSnapshot([]Definition{base}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}
	if _, err := snapshot.String("flag", Context{}); err == nil {
		t.Fatal("String(boolean flag) succeeded")
	}
	if _, err := snapshot.Batch(Context{}, []EvaluationRequest{{Key: "missing", Type: TypeBoolean}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Batch(missing) error = %v", err)
	}
}

func TestDiagnosticUTF8TruncationBoundaries(t *testing.T) {
	t.Parallel()

	if got := truncateUTF8("value", 0); got != "" {
		t.Fatalf("truncateUTF8(zero) = %q", got)
	}
	if got := truncateUTF8("ok", 4); got != "ok" {
		t.Fatalf("truncateUTF8(short) = %q", got)
	}
	if got := truncateUTF8("a€b", 3); got != "a" {
		t.Fatalf("truncateUTF8(multibyte) = %q", got)
	}
	if diagnostics := boundDiagnostics([]Diagnostic{{Code: "x"}}, Limits{}); diagnostics != nil {
		t.Fatalf("boundDiagnostics(zero) = %#v", diagnostics)
	}
}
