package featureflags

import "testing"

var _ Provider = (*MemoryProvider)(nil)

func TestMemoryProviderReportsNativeCapabilities(t *testing.T) {
	t.Parallel()

	capabilities := NewMemoryProvider(DefaultLimits()).Capabilities()
	if !capabilities.OptimisticConcurrency || !capabilities.AtomicMutations ||
		!capabilities.Snapshots || !capabilities.Audit ||
		!capabilities.Groups || !capabilities.ImportExport {
		t.Fatalf("Capabilities() = %#v, want all native capabilities", capabilities)
	}
}
