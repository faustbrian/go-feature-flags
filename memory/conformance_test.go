package memory_test

import (
	"testing"

	featureflags "github.com/faustbrian/go-feature-flags"
	"github.com/faustbrian/go-feature-flags/featureflagstest"
)

func TestProviderConformance(t *testing.T) {
	featureflagstest.RunProvider(t, func(t *testing.T) featureflags.Provider {
		t.Helper()
		return featureflags.NewMemoryProvider(featureflags.DefaultLimits())
	})
	featureflagstest.RunFleet(t, func(t *testing.T) featureflags.Provider {
		t.Helper()
		return featureflags.NewMemoryProvider(featureflags.DefaultLimits())
	})
}
