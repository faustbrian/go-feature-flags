package featureflags

import (
	"context"
	"errors"
	"testing"
	"time"
)

type cancelAfterFirstCheck struct{ checks int }

func (*cancelAfterFirstCheck) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*cancelAfterFirstCheck) Done() <-chan struct{}       { return nil }
func (*cancelAfterFirstCheck) Value(any) any               { return nil }
func (ctx *cancelAfterFirstCheck) Err() error {
	ctx.checks++
	if ctx.checks > 1 {
		return context.Canceled
	}
	return nil
}

func TestImportConflictPoliciesCoverFeaturesAndGroups(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	document, err := Export([]Definition{{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(true),
		Lifecycle: LifecycleActive, Variants: map[string]Value{"on": BooleanValue(true)},
		Groups: []string{"beta"},
	}}, []GroupDefinition{{Key: "beta"}}, limits)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	for _, policy := range []ConflictPolicy{ConflictFail, ConflictSkip, ConflictReplace} {
		policy := policy
		t.Run(string(policy), func(t *testing.T) {
			t.Parallel()
			provider := NewMemoryProvider(limits)
			group, createErr := provider.CreateGroup(t.Context(), "tenant", GroupDefinition{Key: "beta"}, "alice")
			if createErr != nil {
				t.Fatalf("CreateGroup() error = %v", createErr)
			}
			feature, createErr := provider.Create(t.Context(), "tenant", Definition{
				Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
				Lifecycle: LifecycleActive, Variants: map[string]Value{"on": BooleanValue(true)},
				Groups: []string{"beta"},
			}, "alice")
			if createErr != nil {
				t.Fatalf("Create() error = %v", createErr)
			}
			report, importErr := provider.ImportDocument(
				t.Context(), "tenant", document, ImportOptions{ConflictPolicy: policy}, "importer",
			)
			switch policy {
			case ConflictFail:
				if !errors.Is(importErr, ErrImportConflict) || len(report.Conflicts) != 2 || report.Skipped != 2 {
					t.Fatalf("ImportDocument(fail) = (%#v, %v)", report, importErr)
				}
			case ConflictSkip:
				if importErr != nil || report.Skipped != 2 {
					t.Fatalf("ImportDocument(skip) = (%#v, %v)", report, importErr)
				}
			case ConflictReplace:
				if importErr != nil || report.UpdatedFeatures != 1 || report.UpdatedGroups != 1 {
					t.Fatalf("ImportDocument(replace) = (%#v, %v)", report, importErr)
				}
				snapshot, snapshotErr := provider.Snapshot(t.Context(), "tenant")
				if snapshotErr != nil {
					t.Fatalf("Snapshot() error = %v", snapshotErr)
				}
				detail, evaluationErr := snapshot.Boolean("flag", Context{Tenant: "tenant"})
				if evaluationErr != nil || !detail.Value || detail.Version != feature.Version+1 {
					t.Fatalf("Boolean() = (%#v, %v)", detail, evaluationErr)
				}
				audit, auditErr := provider.Audit(t.Context(), "tenant", group.Key)
				if auditErr != nil || audit[len(audit)-1].Action != AuditImportUpdate {
					t.Fatalf("Audit(group) = (%#v, %v)", audit, auditErr)
				}
			}
		})
	}

	provider := NewMemoryProvider(limits)
	report, err := provider.ImportDocument(
		t.Context(), "tenant", document, ImportOptions{ConflictPolicy: ConflictFail}, "importer",
	)
	if err != nil || report.CreatedFeatures != 1 || report.CreatedGroups != 1 {
		t.Fatalf("ImportDocument(create) = (%#v, %v)", report, err)
	}
	if _, err := provider.ImportDocument(
		t.Context(), "tenant", document, ImportOptions{ConflictPolicy: "unknown"}, "importer",
	); err == nil {
		t.Fatal("ImportDocument(unknown policy) succeeded")
	}
	if _, err := provider.ImportDocument(
		t.Context(), "tenant", []byte(`{}`), ImportOptions{}, "importer",
	); err == nil {
		t.Fatal("ImportDocument(invalid document) succeeded")
	}
	ctx := &cancelAfterFirstCheck{}
	if _, err := provider.ImportDocument(
		ctx, "tenant", document, ImportOptions{DryRun: true}, "importer",
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("ImportDocument(cancelled after decode) error = %v", err)
	}
}

func TestImportRejectsMergedGroupTargetMismatchAtomically(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	group, err := provider.CreateGroup(t.Context(), "tenant", GroupDefinition{Key: "beta"}, "alice")
	if err != nil {
		t.Fatalf("CreateGroup() error = %v", err)
	}
	if _, err := provider.Create(t.Context(), "tenant", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false),
		Variants: map[string]Value{"on": BooleanValue(true)}, Groups: []string{"beta"},
	}, "alice"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	document, err := Export(nil, []GroupDefinition{{
		Key: group.Key, Strategies: []Strategy{ExactTargetStrategy{
			Name: "bad-target", Variant: "missing",
		}},
	}}, DefaultLimits())
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if _, err := provider.ImportDocument(
		t.Context(), "tenant", document, ImportOptions{ConflictPolicy: ConflictReplace}, "importer",
	); err == nil {
		t.Fatal("ImportDocument(merged invalid group) succeeded")
	}
	audit, err := provider.Audit(t.Context(), "tenant", group.Key)
	if err != nil || len(audit) != 1 {
		t.Fatalf("Audit() after rejected import = (%#v, %v)", audit, err)
	}
}
