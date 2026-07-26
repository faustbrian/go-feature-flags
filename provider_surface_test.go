package featureflags

import (
	"context"
	"testing"
	"time"
)

func TestProviderWrappersExposeCompleteManagementSurface(t *testing.T) {
	t.Parallel()

	clock := &manualCacheClock{now: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)}
	cached, err := NewCachedProvider(NewMemoryProvider(DefaultLimits()), CacheConfig{
		Clock: clock, MaxStaleness: time.Minute, MaxOutageStaleness: 2 * time.Minute,
		FailurePolicy: FailClosed, MaxTenants: 2,
	})
	if err != nil {
		t.Fatalf("NewCachedProvider() error = %v", err)
	}

	providers := map[string]Provider{
		"cached":  cached,
		"durable": NewDurableProvider(newFakeDocumentBackend(), DefaultLimits()),
	}
	for name, provider := range providers {
		provider := provider
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			exerciseProviderSurface(t, provider)
		})
	}
}

func exerciseProviderSurface(t *testing.T, provider Provider) {
	t.Helper()
	ctx := context.Background()
	tenant := "tenant-surface"
	if capabilities := provider.Capabilities(); !capabilities.AtomicMutations || !capabilities.Groups {
		t.Fatalf("Capabilities() = %#v", capabilities)
	}
	if health := provider.Health(ctx); !health.Healthy {
		t.Fatalf("Health() = %#v", health)
	}

	definition := Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
		Lifecycle: LifecycleActive,
		Variants:  map[string]Value{"enabled": BooleanValue(true)},
	}
	feature, err := provider.Create(ctx, tenant, definition, "alice")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	group, err := provider.CreateGroup(ctx, tenant, GroupDefinition{
		Key: "beta", Strategies: []Strategy{ExactTargetStrategy{
			Name: "alice", Variant: "enabled", Subjects: []string{"alice"},
		}},
	}, "alice")
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	group.Owner = "platform"
	group, err = provider.UpdateGroup(ctx, tenant, group, group.Version, "alice")
	if err != nil {
		t.Fatalf("UpdateGroup() error = %v", err)
	}
	feature, err = provider.AssignGroup(ctx, tenant, feature.Key, group.Key, feature.Version, "alice")
	if err != nil {
		t.Fatalf("AssignGroup() error = %v", err)
	}
	snapshot, err := provider.Snapshot(ctx, tenant)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if detail, evaluationErr := snapshot.Boolean(feature.Key, Context{
		Tenant: tenant, Subject: "alice",
	}); evaluationErr != nil || !detail.Value {
		t.Fatalf("Boolean() = (%#v, %v)", detail, evaluationErr)
	}

	stagedDefinition := cloneDefinition(feature)
	stagedDefinition.Default = BooleanValue(true)
	stage, err := provider.StageUpdate(
		ctx, tenant, stagedDefinition, feature.Version, time.Time{}, "alice",
	)
	if err != nil {
		t.Fatalf("StageUpdate() error = %v", err)
	}
	stages, err := provider.StagedChanges(ctx, tenant)
	if err != nil || len(stages) != 1 {
		t.Fatalf("StagedChanges() = (%#v, %v)", stages, err)
	}
	feature, err = provider.ApplyStage(ctx, tenant, stage.ID, "scheduler")
	if err != nil {
		t.Fatalf("ApplyStage() error = %v", err)
	}
	feature, err = provider.Deactivate(ctx, tenant, feature.Key, feature.Version, "alice")
	if err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	feature, err = provider.Activate(ctx, tenant, feature.Key, feature.Version, "alice")
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	feature, err = provider.RemoveGroup(ctx, tenant, feature.Key, group.Key, feature.Version, "alice")
	if err != nil {
		t.Fatalf("RemoveGroup() error = %v", err)
	}
	if _, err := provider.DeleteGroup(ctx, tenant, group.Key, group.Version, "alice"); err != nil {
		t.Fatalf("DeleteGroup() error = %v", err)
	}

	document, err := provider.ExportDocument(ctx, tenant)
	if err != nil {
		t.Fatalf("ExportDocument() error = %v", err)
	}
	report, err := provider.ImportDocument(
		ctx, tenant, document, ImportOptions{DryRun: true, ConflictPolicy: ConflictSkip}, "alice",
	)
	if err != nil || report.Skipped != 1 {
		t.Fatalf("ImportDocument(dry-run) = (%#v, %v)", report, err)
	}
	audit, err := provider.Audit(ctx, tenant, feature.Key)
	if err != nil || len(audit) == 0 {
		t.Fatalf("Audit() = (%#v, %v)", audit, err)
	}

	at := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	stagedDefinition = cloneDefinition(feature)
	stagedDefinition.Default = BooleanValue(false)
	if _, err := provider.StageUpdate(
		ctx, tenant, stagedDefinition, feature.Version, at, "alice",
	); err != nil {
		t.Fatalf("StageUpdate(scheduled) error = %v", err)
	}
	applied, err := provider.ApplyScheduled(ctx, tenant, at, "scheduler")
	if err != nil || len(applied) != 1 {
		t.Fatalf("ApplyScheduled() = (%#v, %v)", applied, err)
	}
	feature = applied[0]
	deleted, err := provider.Delete(ctx, tenant, feature.Key, feature.Version, "alice")
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	restored, err := provider.Restore(ctx, tenant, feature.Key, deleted.Version, "alice")
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	deleted, err = provider.Delete(ctx, tenant, feature.Key, restored.Version, "alice")
	if err != nil {
		t.Fatalf("Delete(after restore) error = %v", err)
	}
	reportCleanup, err := provider.Cleanup(ctx, tenant, CleanupOptions{
		PurgeDeleted: true, KeepAudit: 2,
	})
	if err != nil || reportCleanup.DeletedFeatures != 1 {
		t.Fatalf("Cleanup() = (%#v, %v)", reportCleanup, err)
	}
	if err := provider.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
