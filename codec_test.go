package featureflags

import (
	"bytes"
	"testing"
)

func TestExportIsDeterministicAndRoundTripsNativeDefinitions(t *testing.T) {
	t.Parallel()

	definitions := []Definition{
		{Key: "zeta", Type: TypeString, Default: StringValue("control"), Lifecycle: LifecycleActive},
		{
			Key:       "alpha",
			Type:      TypeBoolean,
			Default:   BooleanValue(false),
			Lifecycle: LifecycleActive,
			Variants:  map[string]Value{"enabled": BooleanValue(true)},
			Strategies: []Strategy{ExactTargetStrategy{
				Name: "selected", Variant: "enabled", Subjects: []string{"user-123"},
			}},
		},
	}

	first, err := Export(definitions, nil, DefaultLimits())
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	reversed := []Definition{definitions[1], definitions[0]}
	second, err := Export(reversed, nil, DefaultLimits())
	if err != nil {
		t.Fatalf("Export(reversed) error = %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("Export() changed with input order:\n%s\n%s", first, second)
	}

	importedDefinitions, importedGroups, err := Import(first, DefaultLimits())
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	snapshot, err := NewSnapshotWithGroups(importedDefinitions, importedGroups, DefaultLimits())
	if err != nil {
		t.Fatalf("NewSnapshotWithGroups() error = %v", err)
	}
	detail, err := snapshot.Boolean("alpha", Context{Subject: "user-123"})
	if err != nil {
		t.Fatalf("Boolean() error = %v", err)
	}
	if !detail.Value || detail.MatchedStrategy != "selected" {
		t.Fatalf("Boolean() = (%t, %q), want (true, selected)", detail.Value, detail.MatchedStrategy)
	}
}
