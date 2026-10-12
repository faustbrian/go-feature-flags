package featureflags

import (
	"fmt"
	"time"
)

// Context contains only caller-supplied evaluation data. The engine never
// scrapes process, request, or global state. Its maps are borrowed during
// evaluation; callers and custom strategies must not mutate them concurrently.
type Context struct {
	// Subject is the explicit bucketing/targeting identity; empty is allowed.
	Subject string
	// Tenant is the required match for provider-bound snapshots, not authentication.
	Tenant string
	// Environment is an explicit exact-target label; no ambient environment is read.
	Environment string
	// Attributes is borrowed string targeting data; do not mutate during evaluation.
	Attributes map[string]string
	// Facts is borrowed typed targeting data; custom strategies must not mutate it.
	Facts map[string]Value
	// Time is the explicit evaluation instant; zero makes time strategies not match.
	Time time.Time
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
