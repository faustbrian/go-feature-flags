package featureflags

import "testing"

func TestDefinitionValidationRejectsVariantWithWrongType(t *testing.T) {
	t.Parallel()

	definition := Definition{
		Key:     "checkout.redesign",
		Type:    TypeBoolean,
		Default: BooleanValue(false),
		Variants: map[string]Value{
			"enabled": StringValue("yes"),
		},
	}

	err := definition.Validate(DefaultLimits())
	if err == nil {
		t.Fatal("Validate() error = nil, want variant type mismatch")
	}
	if got, want := err.Error(), `feature "checkout.redesign": variant "enabled" has type string, want boolean`; got != want {
		t.Fatalf("Validate() error = %q, want %q", got, want)
	}
}

func TestDefinitionValidationEnforcesMetadataBounds(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxTags = 1
	err := (Definition{
		Key:     "checkout.redesign",
		Type:    TypeBoolean,
		Default: BooleanValue(false),
		Tags:    []string{"checkout", "experiment"},
	}).Validate(limits)
	if err == nil {
		t.Fatal("Validate() error = nil, want tag limit error")
	}
}
