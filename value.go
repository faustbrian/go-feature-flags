package featureflags

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
)

var decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// Type identifies the representation of a feature value.
type Type string

const (
	TypeBoolean    Type = "boolean"
	TypeString     Type = "string"
	TypeInteger    Type = "integer"
	TypeFloat      Type = "float"
	TypeDecimal    Type = "decimal"
	TypeStructured Type = "structured"
)

// Value is an immutable, explicitly typed feature value.
type Value struct {
	typ        Type
	boolean    bool
	text       string
	integer    int64
	floating   float64
	structured json.RawMessage
}

func BooleanValue(value bool) Value { return Value{typ: TypeBoolean, boolean: value} }

func StringValue(value string) Value { return Value{typ: TypeString, text: value} }

func IntegerValue(value int64) Value { return Value{typ: TypeInteger, integer: value} }

func FloatValue(value float64) Value { return Value{typ: TypeFloat, floating: value} }

// DecimalValue stores a caller-supplied canonical decimal string without
// introducing binary floating-point rounding.
func DecimalValue(value string) Value { return Value{typ: TypeDecimal, text: value} }

// StructuredValue copies JSON bytes so later caller mutation cannot change an
// evaluation snapshot.
func StructuredValue(value json.RawMessage) Value {
	return Value{typ: TypeStructured, structured: append(json.RawMessage(nil), value...)}
}

func (v Value) Type() Type { return v.typ }

// Boolean returns the value and true only when its native type is boolean.
func (v Value) Boolean() (bool, bool) { return v.booleanValue() }

// String returns the value and true only for native string values.
func (v Value) String() (string, bool) { return v.stringValue(TypeString) }

// Integer returns the value and true only for native integer values.
func (v Value) Integer() (int64, bool) { return v.integerValue() }

// Float returns the value and true only for native float values.
func (v Value) Float() (float64, bool) { return v.floatValue() }

// Decimal returns the exact text and true only for native decimal values.
func (v Value) Decimal() (string, bool) { return v.stringValue(TypeDecimal) }

// Structured returns owned JSON bytes and true only for structured values.
func (v Value) Structured() (json.RawMessage, bool) { return v.structuredValue() }

func (v Value) booleanValue() (bool, bool) {
	return v.boolean, v.typ == TypeBoolean
}

func (v Value) stringValue(expected Type) (string, bool) {
	return v.text, v.typ == expected
}

func (v Value) integerValue() (int64, bool) {
	return v.integer, v.typ == TypeInteger
}

func (v Value) floatValue() (float64, bool) {
	return v.floating, v.typ == TypeFloat
}

func (v Value) structuredValue() (json.RawMessage, bool) {
	return append(json.RawMessage(nil), v.structured...), v.typ == TypeStructured
}

func (v Value) clone() Value {
	v.structured = append(json.RawMessage(nil), v.structured...)

	return v
}

func (v Value) equal(other Value) bool {
	if v.typ != other.typ {
		return false
	}
	switch v.typ {
	case TypeBoolean:
		return v.boolean == other.boolean
	case TypeString, TypeDecimal:
		return v.text == other.text
	case TypeInteger:
		return v.integer == other.integer
	case TypeFloat:
		return v.floating == other.floating
	case TypeStructured:
		return bytes.Equal(v.structured, other.structured)
	default:
		return false
	}
}

func (v Value) validate(limits Limits) error {
	switch v.typ {
	case TypeBoolean, TypeInteger:
		return nil
	case TypeString:
		if len(v.text) > limits.MaxStringBytes {
			return fmt.Errorf("string exceeds %d bytes: %w", limits.MaxStringBytes, ErrInvalidValue)
		}
	case TypeFloat:
		if math.IsNaN(v.floating) || math.IsInf(v.floating, 0) {
			return fmt.Errorf("float must be finite: %w", ErrInvalidValue)
		}
	case TypeDecimal:
		if len(v.text) > limits.MaxStringBytes || !decimalPattern.MatchString(v.text) {
			return fmt.Errorf("decimal is not canonical: %w", ErrInvalidValue)
		}
	case TypeStructured:
		if len(v.structured) > limits.MaxStructuredBytes {
			return fmt.Errorf("structured value exceeds %d bytes: %w", limits.MaxStructuredBytes, ErrInvalidValue)
		}
		if !json.Valid(v.structured) {
			return fmt.Errorf("structured value is not valid JSON: %w", ErrInvalidValue)
		}
	default:
		return fmt.Errorf("unknown type %q: %w", v.typ, ErrInvalidValue)
	}

	return nil
}
