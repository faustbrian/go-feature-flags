package featureflags

import (
	"context"
	"testing"
	"time"
)

func TestMemoryProviderAppliesScheduledStageAtExplicitTime(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	created, err := provider.Create(context.Background(), "tenant-a", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleDraft,
	}, "alice")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	activation := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	stage, err := provider.StageUpdate(context.Background(), "tenant-a", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive,
	}, created.Version, activation, "alice")
	if err != nil {
		t.Fatalf("StageUpdate() error = %v", err)
	}
	if stage.ID == 0 || stage.ApplyAt != activation {
		t.Fatalf("StageUpdate() = %#v", stage)
	}

	applied, err := provider.ApplyScheduled(context.Background(), "tenant-a", activation.Add(-time.Nanosecond), "scheduler")
	if err != nil || len(applied) != 0 {
		t.Fatalf("ApplyScheduled(before) = (%d, %v), want (0, nil)", len(applied), err)
	}
	applied, err = provider.ApplyScheduled(context.Background(), "tenant-a", activation, "scheduler")
	if err != nil {
		t.Fatalf("ApplyScheduled(at boundary) error = %v", err)
	}
	if len(applied) != 1 || applied[0].Version != 2 || applied[0].Lifecycle != LifecycleActive {
		t.Fatalf("ApplyScheduled() = %#v, want one active v2 definition", applied)
	}
}
