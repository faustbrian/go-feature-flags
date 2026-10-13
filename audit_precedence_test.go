package featureflags

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestPortableRoundTripPreservesGroupPrecedence(t *testing.T) {
	first, groups := auditPrecedenceGraph([]string{"b", "a"})
	native, err := NewSnapshotWithGroups([]Definition{first}, groups, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	auditAssertGroupChoice(t, native, "b")
	encoded, err := Export([]Definition{first}, groups, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	definitions, importedGroups, err := Import(encoded, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := NewSnapshotWithGroups(definitions, importedGroups, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	auditAssertGroupChoice(t, roundTrip, "b")
	second, _ := auditPrecedenceGraph([]string{"a", "b"})
	other, err := Export([]Definition{second}, groups, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(encoded, other) {
		t.Error("different native precedence collapsed into identical portable bytes")
	}
	groups[0], groups[1] = groups[1], groups[0]
	reorderedCatalog, err := Export([]Definition{first}, groups, DefaultLimits())
	if err != nil || !bytes.Equal(encoded, reorderedCatalog) {
		t.Errorf("nonsemantic catalog order changed portable bytes: %v", err)
	}
}

func TestProviderLoaderRevisionDistinguishesGroupPrecedence(t *testing.T) {
	clock := &manualCacheClock{now: time.Unix(1_700_000_000, 0).UTC()}
	var revisions []string
	for _, order := range [][]string{{"b", "a"}, {"a", "b"}} {
		definition, groups := auditPrecedenceGraph(order)
		snapshot, err := NewSnapshotWithGroups([]Definition{definition}, groups, DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
		loader, err := NewProviderSnapshotLoader(auditFixedSnapshotProvider{snapshot: snapshot}, clock, "ordered-native-graph")
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := loader.Load(t.Context(), "tenant-a")
		if err != nil {
			t.Fatal(err)
		}
		auditAssertGroupChoice(t, candidate.Snapshot, order[0])
		repeated, err := loader.Load(t.Context(), "tenant-a")
		if err != nil || repeated.Revision != candidate.Revision {
			t.Errorf("unchanged graph revision is unstable: %v", err)
		}
		revisions = append(revisions, candidate.Revision)
	}
	if revisions[0] == revisions[1] {
		t.Error("loader reused a revision for differently ordered native graphs")
	}
}

func TestDurableRoundTripPreservesGroupPrecedence(t *testing.T) {
	backend := newFakeDocumentBackend()
	provider := NewDurableProvider(backend, DefaultLimits())
	definition, groups := auditPrecedenceGraph([]string{"b", "a"})
	for _, group := range groups {
		if _, err := provider.CreateGroup(t.Context(), "tenant-a", group, "test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := provider.Create(t.Context(), "tenant-a", definition, "test"); err != nil {
		t.Fatal(err)
	}
	// A new provider forces the persisted tenant document through decoding,
	// rather than observing a retained in-memory definition.
	reloaded := NewDurableProvider(backend, DefaultLimits())
	snapshot, err := reloaded.Snapshot(t.Context(), "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	auditAssertGroupChoice(t, snapshot, "b")
}

func auditPrecedenceGraph(order []string) (Definition, []GroupDefinition) {
	return Definition{Key: "flag", Type: TypeString, Default: StringValue("control"), Lifecycle: LifecycleActive,
			Variants: map[string]Value{"a": StringValue("a"), "b": StringValue("b")}, Groups: order, Version: 7},
		[]GroupDefinition{
			{Key: "a", Version: 3, Strategies: []Strategy{ExactTargetStrategy{Name: "choose-a", Variant: "a"}}},
			{Key: "b", Version: 3, Strategies: []Strategy{ExactTargetStrategy{Name: "choose-b", Variant: "b"}}},
		}
}

func auditAssertGroupChoice(t *testing.T, snapshot Snapshot, want string) {
	t.Helper()
	detail, err := snapshot.String("flag", Context{Tenant: "tenant-a"})
	if err != nil || detail.Value != want || detail.Variant != want || detail.MatchedStrategy != "group:"+want+"/choose-"+want {
		t.Errorf("group precedence outcome = %#v, %v; want %s", detail, err, want)
	}
}

type auditFixedSnapshotProvider struct {
	Provider
	snapshot Snapshot
}

func (provider auditFixedSnapshotProvider) Snapshot(context.Context, string) (Snapshot, error) {
	return provider.snapshot, nil
}
