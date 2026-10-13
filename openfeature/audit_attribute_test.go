package openfeature

import (
	"testing"

	featureflags "github.com/faustbrian/go-feature-flags/v2"
	of "github.com/open-feature/go-sdk/openfeature"
)

func TestOpenFeatureEmptyAttributeRequiresPresence(t *testing.T) {
	native := featureflags.NewMemoryProvider(featureflags.DefaultLimits())
	if _, err := native.Create(t.Context(), "tenant-a", featureflags.Definition{
		Key: "flag", Type: featureflags.TypeBoolean, Default: featureflags.BooleanValue(false), Lifecycle: featureflags.LifecycleActive,
		Variants: map[string]featureflags.Value{"selected": featureflags.BooleanValue(true)},
		Strategies: []featureflags.Strategy{featureflags.ExactTargetStrategy{
			Name: "empty-region", Variant: "selected", Attributes: map[string]string{"region": ""},
		}},
	}, "test"); err != nil {
		t.Fatal(err)
	}
	provider, err := New(native, "tenant-a", Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name  string
		input of.FlattenedContext
		want  bool
	}{
		{"missing", of.FlattenedContext{"tenant": "tenant-a"}, false},
		{"explicit empty", of.FlattenedContext{"tenant": "tenant-a", "region": ""}, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			result := provider.BooleanEvaluation(t.Context(), "flag", false, scenario.input)
			if result.Error() != nil || result.Value != scenario.want {
				t.Errorf("OpenFeature empty attribute = %t, %v; want %t", result.Value, result.Error(), scenario.want)
			}
		})
	}
}
