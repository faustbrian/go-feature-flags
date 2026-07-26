package featureflags

import (
	"fmt"
	"time"
)

// Context contains only caller-supplied evaluation data. The engine never
// scrapes process, request, or global state.
type Context struct {
	Subject     string
	Tenant      string
	Environment string
	Attributes  map[string]string
	Facts       map[string]Value
	Time        time.Time
}

func (c Context) validate(limits Limits) error {
	if len(c.Subject) > limits.MaxContextValueBytes ||
		len(c.Tenant) > limits.MaxContextValueBytes ||
		len(c.Environment) > limits.MaxContextValueBytes {
		return fmt.Errorf("context identity exceeds %d bytes: %w", limits.MaxContextValueBytes, ErrContextLimit)
	}
	if len(c.Attributes) > limits.MaxAttributes {
		return fmt.Errorf("attributes count %d exceeds %d: %w", len(c.Attributes), limits.MaxAttributes, ErrContextLimit)
	}
	if len(c.Facts) > limits.MaxFacts {
		return fmt.Errorf("facts count %d exceeds %d: %w", len(c.Facts), limits.MaxFacts, ErrContextLimit)
	}
	for key, value := range c.Attributes {
		if len(key) > limits.MaxContextKeyBytes || len(value) > limits.MaxContextValueBytes {
			return fmt.Errorf("attribute size exceeds configured bounds: %w", ErrContextLimit)
		}
	}
	for key, value := range c.Facts {
		if len(key) > limits.MaxContextKeyBytes {
			return fmt.Errorf("fact key exceeds %d bytes: %w", limits.MaxContextKeyBytes, ErrContextLimit)
		}
		if err := value.validate(limits); err != nil {
			return fmt.Errorf("fact value: %w", err)
		}
	}

	return nil
}
