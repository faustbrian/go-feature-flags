package featureflags

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryProviderImportDryRunReportsConflictsWithoutMutation(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	_, err := provider.Create(context.Background(), "tenant-a", Definition{
		Key: "existing", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
	}, "alice")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	document, err := Export([]Definition{
		{Key: "existing", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive},
		{Key: "new", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive},
	}, nil, DefaultLimits())
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	report, err := provider.ImportDocument(context.Background(), "tenant-a", document, ImportOptions{
		DryRun: true, ConflictPolicy: ConflictFail,
	}, "alice")
	if err != nil {
		t.Fatalf("ImportDocument(dry-run) error = %v", err)
	}
	if len(report.Conflicts) != 1 || report.CreatedFeatures != 1 {
		t.Fatalf("ImportDocument(dry-run) report = %#v", report)
	}
	snapshot, err := provider.Snapshot(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	existing, err := snapshot.Boolean("existing", Context{Tenant: "tenant-a"})
	if err != nil || existing.Value {
		t.Fatalf("Boolean(existing) = (%t, %v), want (false, nil)", existing.Value, err)
	}
	if _, err := snapshot.Boolean("new", Context{Tenant: "tenant-a"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Boolean(new) error = %v, want ErrNotFound", err)
	}

	report, err = provider.ImportDocument(context.Background(), "tenant-a", document, ImportOptions{
		ConflictPolicy: ConflictReplace,
	}, "alice")
	if err != nil {
		t.Fatalf("ImportDocument(replace) error = %v", err)
	}
	if report.UpdatedFeatures != 1 || report.CreatedFeatures != 1 {
		t.Fatalf("ImportDocument(replace) report = %#v", report)
	}
	snapshot, err = provider.Snapshot(context.Background(), "tenant-a")
	if err != nil {
		t.Fatalf("Snapshot(after replace) error = %v", err)
	}
	existing, err = snapshot.Boolean("existing", Context{Tenant: "tenant-a"})
	if err != nil || !existing.Value || existing.Version != 2 {
		t.Fatalf("Boolean(existing after replace) = (%t, v%d, %v), want (true, v2, nil)", existing.Value, existing.Version, err)
	}
	created, err := snapshot.Boolean("new", Context{Tenant: "tenant-a"})
	if err != nil || !created.Value || created.Version != 1 {
		t.Fatalf("Boolean(new after replace) = (%t, v%d, %v), want (true, v1, nil)", created.Value, created.Version, err)
	}
}
