package featureflags

import (
	"context"
	"testing"
	"time"
)

func TestMemoryProviderCleanupPurgesExplicitlySelectedState(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	created, err := provider.Create(context.Background(), "tenant-a", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
	}, "alice")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	cutoff := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	if _, err := provider.StageUpdate(context.Background(), "tenant-a", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive,
	}, created.Version, cutoff.Add(-time.Hour), "alice"); err != nil {
		t.Fatalf("StageUpdate() error = %v", err)
	}
	if _, err := provider.Delete(context.Background(), "tenant-a", "flag", created.Version, "alice"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	report, err := provider.Cleanup(context.Background(), "tenant-a", CleanupOptions{
		PurgeDeleted: true, DiscardStagesBefore: cutoff, KeepAudit: 1,
	})
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if report.DeletedFeatures != 1 || report.DiscardedStages != 1 || report.DiscardedAudit == 0 {
		t.Fatalf("Cleanup() report = %#v", report)
	}
}
