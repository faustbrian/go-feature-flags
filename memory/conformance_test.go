package memory_test

import (
	"testing"

	featureflags "github.com/faustbrian/golib/pkg/feature-flags"
	"github.com/faustbrian/golib/pkg/feature-flags/featureflagstest"
)

func TestProviderConformance(t *testing.T) {
	featureflagstest.RunProvider(t, func(t *testing.T) featureflags.Provider {
		t.Helper()
		return featureflags.NewMemoryProvider(featureflags.DefaultLimits())
	})
}
