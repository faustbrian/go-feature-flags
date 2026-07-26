// Package featureflagstest provides the shared provider conformance suite.
package featureflagstest

import (
	"context"
	"errors"
	"testing"
	"time"

	featureflags "github.com/faustbrian/golib/pkg/feature-flags"
)

// Factory returns an isolated provider for one conformance test.
type Factory func(*testing.T) featureflags.Provider

// RunProvider verifies semantics required from every management provider.
func RunProvider(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("capabilities", func(t *testing.T) {
		capabilities := factory(t).Capabilities()
		if !capabilities.OptimisticConcurrency || !capabilities.AtomicMutations ||
			!capabilities.Snapshots || !capabilities.Audit || !capabilities.Groups ||
			!capabilities.ImportExport {
			t.Fatalf("Capabilities() = %#v, want complete native contract", capabilities)
		}
	})

	t.Run("optimistic concurrency", func(t *testing.T) {
		provider := factory(t)
		created, err := provider.Create(t.Context(), "tenant-a", definition("flag", false), "tester")
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		updated := definition("flag", true)
		if _, err := provider.Update(t.Context(), "tenant-a", updated, created.Version, "tester"); err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		if _, err := provider.Update(t.Context(), "tenant-a", updated, created.Version, "tester"); !errors.Is(err, featureflags.ErrConflict) {
			t.Fatalf("stale Update() error = %v, want ErrConflict", err)
		}
	})

	t.Run("tenant isolation", func(t *testing.T) {
		provider := factory(t)
		if _, err := provider.Create(t.Context(), "tenant-a", definition("flag", true), "tester"); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		snapshot, err := provider.Snapshot(t.Context(), "tenant-b")
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if _, err := snapshot.Boolean("flag", featureflags.Context{Tenant: "tenant-b"}); !errors.Is(err, featureflags.ErrNotFound) {
			t.Fatalf("cross-tenant Boolean() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("immutable snapshots and audit", func(t *testing.T) {
		provider := factory(t)
		created, err := provider.Create(t.Context(), "tenant-a", definition("flag", false), "creator")
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		before, err := provider.Snapshot(t.Context(), "tenant-a")
		if err != nil {
			t.Fatalf("Snapshot(before) error = %v", err)
		}
		updated, err := provider.Update(
			t.Context(), "tenant-a", definition("flag", true), created.Version, "updater",
		)
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
		after, err := provider.Snapshot(t.Context(), "tenant-a")
		if err != nil {
			t.Fatalf("Snapshot(after) error = %v", err)
		}
		beforeDetail, err := before.Boolean("flag", featureflags.Context{Tenant: "tenant-a"})
		if err != nil {
			t.Fatalf("Boolean(before) error = %v", err)
		}
		afterDetail, err := after.Boolean("flag", featureflags.Context{Tenant: "tenant-a"})
		if err != nil {
			t.Fatalf("Boolean(after) error = %v", err)
		}
		if beforeDetail.Value || beforeDetail.Version != created.Version ||
			!afterDetail.Value || afterDetail.Version != updated.Version {
			t.Fatalf("snapshot generations mixed: before=%#v after=%#v", beforeDetail, afterDetail)
		}
		audit, err := provider.Audit(t.Context(), "tenant-a", "flag")
		if err != nil {
			t.Fatalf("Audit() error = %v", err)
		}
		if len(audit) != 2 || audit[0].Action != featureflags.AuditCreate ||
			audit[0].Actor != "creator" || audit[1].Action != featureflags.AuditUpdate ||
			audit[1].Actor != "updater" {
			t.Fatalf("Audit() = %#v, want bounded create/update history", audit)
		}
	})

	t.Run("management validation", func(t *testing.T) {
		provider := factory(t)
		if _, err := provider.Create(t.Context(), "tenant-a", featureflags.Definition{}, "tester"); err == nil {
			t.Fatal("Create(invalid definition) succeeded")
		}
		if _, err := provider.Create(t.Context(), "tenant-a", definition("flag", true), "tester"); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if _, err := provider.Create(t.Context(), "tenant-a", definition("flag", false), "tester"); !errors.Is(err, featureflags.ErrAlreadyExists) {
			t.Fatalf("Create(duplicate) error = %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("groups and portable import", func(t *testing.T) {
		provider := factory(t)
		created, err := provider.Create(t.Context(), "tenant-a", featureflags.Definition{
			Key: "flag", Type: featureflags.TypeBoolean,
			Default: featureflags.BooleanValue(false), Lifecycle: featureflags.LifecycleActive,
			Variants: map[string]featureflags.Value{"enabled": featureflags.BooleanValue(true)},
		}, "tester")
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		group, err := provider.CreateGroup(t.Context(), "tenant-a", featureflags.GroupDefinition{
			Key: "early-access",
			Strategies: []featureflags.Strategy{featureflags.ExactTargetStrategy{
				Name: "alice", Variant: "enabled", Subjects: []string{"alice"},
			}},
		}, "tester")
		if err != nil {
			t.Fatalf("CreateGroup() error = %v", err)
		}
		_, err = provider.AssignGroup(
			t.Context(), "tenant-a", "flag", group.Key, created.Version, "tester",
		)
		if err != nil {
			t.Fatalf("AssignGroup() error = %v", err)
		}
		if _, err := provider.DeleteGroup(
			t.Context(), "tenant-a", group.Key, group.Version, "tester",
		); !errors.Is(err, featureflags.ErrGroupInUse) {
			t.Fatalf("DeleteGroup(in use) error = %v, want ErrGroupInUse", err)
		}
		document, err := provider.ExportDocument(t.Context(), "tenant-a")
		if err != nil {
			t.Fatalf("ExportDocument() error = %v", err)
		}
		if _, err := provider.ImportDocument(
			t.Context(), "tenant-b", document,
			featureflags.ImportOptions{ConflictPolicy: featureflags.ConflictFail}, "tester",
		); err != nil {
			t.Fatalf("ImportDocument() error = %v", err)
		}
		snapshot, err := provider.Snapshot(t.Context(), "tenant-b")
		if err != nil {
			t.Fatalf("Snapshot(imported) error = %v", err)
		}
		detail, err := snapshot.Boolean("flag", featureflags.Context{Tenant: "tenant-b", Subject: "alice"})
		if err != nil {
			t.Fatalf("Boolean(imported) error = %v", err)
		}
		if !detail.Value || detail.Reason != featureflags.ReasonGroupMatch || detail.Version == 0 {
			t.Fatalf("Boolean(imported) = %#v, want imported group decision", detail)
		}
	})

	t.Run("delete and restore", func(t *testing.T) {
		provider := factory(t)
		created, err := provider.Create(t.Context(), "tenant-a", definition("flag", true), "tester")
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		deleted, err := provider.Delete(t.Context(), "tenant-a", "flag", created.Version, "tester")
		if err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		restored, err := provider.Restore(t.Context(), "tenant-a", "flag", deleted.Version, "tester")
		if err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
		if restored.Lifecycle != featureflags.LifecycleInactive {
			t.Fatalf("Restore() lifecycle = %s, want inactive", restored.Lifecycle)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		provider := factory(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := provider.Snapshot(ctx, "tenant-a"); !errors.Is(err, context.Canceled) {
			t.Fatalf("Snapshot() error = %v, want context.Canceled", err)
		}
	})

	t.Run("scheduled stage", func(t *testing.T) {
		provider := factory(t)
		created, err := provider.Create(t.Context(), "tenant-a", definition("flag", false), "tester")
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		at := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
		if _, err := provider.StageUpdate(
			t.Context(), "tenant-a", definition("flag", true), created.Version, at, "tester",
		); err != nil {
			t.Fatalf("StageUpdate() error = %v", err)
		}
		applied, err := provider.ApplyScheduled(t.Context(), "tenant-a", at, "scheduler")
		if err != nil || len(applied) != 1 || applied[0].Version != 2 {
			t.Fatalf("ApplyScheduled() = (%#v, %v)", applied, err)
		}
	})
}

func definition(key string, enabled bool) featureflags.Definition {
	return featureflags.Definition{
		Key:       key,
		Type:      featureflags.TypeBoolean,
		Default:   featureflags.BooleanValue(enabled),
		Lifecycle: featureflags.LifecycleActive,
	}
}
