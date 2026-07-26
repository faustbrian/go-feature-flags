package featureflags

import (
	"errors"
	"testing"
)

func TestEvaluationRejectsContextBeyondConfiguredCardinality(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxFacts = 1
	snapshot, err := NewSnapshot([]Definition{{
		Key:       "checkout.redesign",
		Type:      TypeBoolean,
		Default:   BooleanValue(false),
		Lifecycle: LifecycleActive,
	}}, limits)
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}

	_, err = snapshot.Boolean("checkout.redesign", Context{Facts: map[string]Value{
		"account.age": IntegerValue(10),
		"cart.total":  DecimalValue("20.00"),
	}})
	if !errors.Is(err, ErrContextLimit) {
		t.Fatalf("Boolean() error = %v, want ErrContextLimit", err)
	}
}

func TestEvaluationRejectsOversizedContextKeys(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxContextKeyBytes = 4
	snapshot, err := NewSnapshot([]Definition{{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
	}}, limits)
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}

	_, err = snapshot.Boolean("flag", Context{Attributes: map[string]string{"long-key": "safe"}})
	if !errors.Is(err, ErrContextLimit) {
		t.Fatalf("Boolean() error = %v, want ErrContextLimit", err)
	}
}
