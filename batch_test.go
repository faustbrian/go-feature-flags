package featureflags

import (
	"errors"
	"testing"
)

func TestSnapshotBatchEvaluatesMixedTypesWithinConfiguredBound(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxBatchSize = 2
	snapshot, err := NewSnapshot([]Definition{
		{Key: "enabled", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive},
		{Key: "label", Type: TypeString, Default: StringValue("stable"), Lifecycle: LifecycleActive},
	}, limits)
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}

	details, err := snapshot.Batch(Context{}, []EvaluationRequest{
		{Key: "enabled", Type: TypeBoolean},
		{Key: "label", Type: TypeString},
	})
	if err != nil {
		t.Fatalf("Batch() error = %v", err)
	}
	if value, ok := details[0].Value.Boolean(); !ok || !value {
		t.Fatalf("Batch()[0] = (%t, %t), want (true, true)", value, ok)
	}
	if value, ok := details[1].Value.String(); !ok || value != "stable" {
		t.Fatalf("Batch()[1] = (%q, %t), want (stable, true)", value, ok)
	}

	_, err = snapshot.Batch(Context{}, []EvaluationRequest{
		{Key: "enabled", Type: TypeBoolean},
		{Key: "label", Type: TypeString},
		{Key: "enabled", Type: TypeBoolean},
	})
	if !errors.Is(err, ErrBatchLimit) {
		t.Fatalf("oversized Batch() error = %v, want ErrBatchLimit", err)
	}
}
