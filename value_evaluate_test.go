package featureflags

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestSnapshotEvaluatesEveryNativeValueTypeWithoutCoercion(t *testing.T) {
	t.Parallel()

	snapshot, err := NewSnapshot([]Definition{
		{Key: "text", Type: TypeString, Default: StringValue("control"), Lifecycle: LifecycleActive},
		{Key: "count", Type: TypeInteger, Default: IntegerValue(42), Lifecycle: LifecycleActive},
		{Key: "ratio", Type: TypeFloat, Default: FloatValue(0.25), Lifecycle: LifecycleActive},
		{Key: "price", Type: TypeDecimal, Default: DecimalValue("19.990"), Lifecycle: LifecycleActive},
		{Key: "payload", Type: TypeStructured, Default: StructuredValue(json.RawMessage(`{"mode":"safe"}`)), Lifecycle: LifecycleActive},
	}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}

	text, err := snapshot.String("text", Context{})
	if err != nil || text.Value != "control" {
		t.Fatalf("String() = (%q, %v), want (control, nil)", text.Value, err)
	}
	count, err := snapshot.Integer("count", Context{})
	if err != nil || count.Value != 42 {
		t.Fatalf("Integer() = (%d, %v), want (42, nil)", count.Value, err)
	}
	ratio, err := snapshot.Float("ratio", Context{})
	if err != nil || ratio.Value != 0.25 {
		t.Fatalf("Float() = (%f, %v), want (0.25, nil)", ratio.Value, err)
	}
	price, err := snapshot.Decimal("price", Context{})
	if err != nil || price.Value != "19.990" {
		t.Fatalf("Decimal() = (%q, %v), want (19.990, nil)", price.Value, err)
	}
	payload, err := snapshot.Structured("payload", Context{})
	if err != nil || !bytes.Equal(payload.Value, json.RawMessage(`{"mode":"safe"}`)) {
		t.Fatalf("Structured() = (%s, %v), want ({\"mode\":\"safe\"}, nil)", payload.Value, err)
	}
}
