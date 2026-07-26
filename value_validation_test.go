package featureflags

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestDefinitionValidationRejectsInvalidNativeValues(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		typ   Type
		value Value
	}{
		{name: "non-finite float", typ: TypeFloat, value: FloatValue(math.Inf(1))},
		{name: "non-canonical decimal", typ: TypeDecimal, value: DecimalValue("01.20")},
		{name: "malformed structured JSON", typ: TypeStructured, value: StructuredValue(json.RawMessage(`{"broken"`))},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := (Definition{Key: "feature", Type: test.typ, Default: test.value}).Validate(DefaultLimits())
			if !errors.Is(err, ErrInvalidValue) {
				t.Fatalf("Validate() error = %v, want ErrInvalidValue", err)
			}
		})
	}
}
