package featureflags

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeDocumentBackend struct {
	mu       sync.Mutex
	data     map[string][]byte
	revision map[string]uint64
}

type failingDocumentBackend struct {
	loadData  []byte
	loadRev   uint64
	loadFound bool
	loadErr   error
	casErr    error
	conflicts int
}

func (backend *failingDocumentBackend) Load(context.Context, string) ([]byte, uint64, bool, error) {
	return backend.loadData, backend.loadRev, backend.loadFound, backend.loadErr
}

func (backend *failingDocumentBackend) CompareAndSwap(context.Context, string, uint64, []byte) error {
	if backend.conflicts > 0 {
		backend.conflicts--
		return ErrStorageConflict
	}
	return backend.casErr
}

func (*failingDocumentBackend) Health(context.Context) ProviderHealth {
	return ProviderHealth{Code: "unavailable"}
}

func (backend *failingDocumentBackend) Close(context.Context) error { return backend.casErr }

func newFakeDocumentBackend() *fakeDocumentBackend {
	return &fakeDocumentBackend{data: make(map[string][]byte), revision: make(map[string]uint64)}
}

func (backend *fakeDocumentBackend) Load(_ context.Context, tenant string) ([]byte, uint64, bool, error) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	data, exists := backend.data[tenant]
	return append([]byte(nil), data...), backend.revision[tenant], exists, nil
}

func (backend *fakeDocumentBackend) CompareAndSwap(
	_ context.Context,
	tenant string,
	expected uint64,
	data []byte,
) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.revision[tenant] != expected {
		return ErrStorageConflict
	}
	backend.revision[tenant]++
	backend.data[tenant] = append([]byte(nil), data...)
	return nil
}

func (*fakeDocumentBackend) Health(context.Context) ProviderHealth {
	return ProviderHealth{Healthy: true, Code: "ready"}
}

func (*fakeDocumentBackend) Close(context.Context) error { return nil }

func TestDurableProviderSharesAtomicStateAcrossInstances(t *testing.T) {
	t.Parallel()

	backend := newFakeDocumentBackend()
	first := NewDurableProvider(backend, DefaultLimits())
	second := NewDurableProvider(backend, DefaultLimits())
	created, err := first.Create(context.Background(), "tenant-a", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(false), Lifecycle: LifecycleActive,
	}, "alice")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	updated := Definition{Key: "flag", Type: TypeBoolean, Default: BooleanValue(true), Lifecycle: LifecycleActive}
	if _, err := second.Update(context.Background(), "tenant-a", updated, created.Version, "bob"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if _, err := first.Update(context.Background(), "tenant-a", updated, created.Version, "alice"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale Update() error = %v, want ErrConflict", err)
	}
}

func TestDurableProviderBoundsStorageRetriesAndMapsBackendFailures(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxStorageRetries = 1
	backend := &failingDocumentBackend{conflicts: 1}
	provider := NewDurableProvider(backend, limits)
	definition := Definition{Key: "flag", Type: TypeBoolean, Default: BooleanValue(true)}
	if _, err := provider.Create(t.Context(), "tenant", definition, "alice"); err != nil {
		t.Fatalf("Create(after retry) error = %v", err)
	}
	backend.conflicts = 2
	if _, err := provider.Create(t.Context(), "tenant", definition, "alice"); !errors.Is(err, ErrStorageConflict) {
		t.Fatalf("Create(retry exhausted) error = %v", err)
	}
	boom := errors.New("backend unavailable")
	backend.conflicts = 0
	backend.loadErr = boom
	if _, err := provider.Snapshot(t.Context(), "tenant"); !errors.Is(err, boom) {
		t.Fatalf("Snapshot(load failure) error = %v", err)
	}
	if _, err := provider.Audit(t.Context(), "tenant", "flag"); !errors.Is(err, boom) {
		t.Fatalf("Audit(load failure) error = %v", err)
	}
	if _, err := provider.ExportDocument(t.Context(), "tenant"); !errors.Is(err, boom) {
		t.Fatalf("ExportDocument(load failure) error = %v", err)
	}
	if _, err := provider.StagedChanges(t.Context(), "tenant"); !errors.Is(err, boom) {
		t.Fatalf("StagedChanges(load failure) error = %v", err)
	}
	backend.loadErr = nil
	backend.casErr = boom
	if _, err := provider.Create(t.Context(), "tenant", definition, "alice"); !errors.Is(err, boom) {
		t.Fatalf("Create(CAS failure) error = %v", err)
	}
	if health := provider.Health(t.Context()); health.Code != "unavailable" {
		t.Fatalf("Health() = %#v", health)
	}
	if err := provider.Close(t.Context()); !errors.Is(err, boom) {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestDurableStateDecoderRejectsCorruptionAndBounds(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	tests := map[string]string{
		"invalid JSON":    `{`,
		"trailing JSON":   `{"version":1} {}`,
		"unknown field":   `{"version":1,"unknown":true}`,
		"unknown version": `{"version":2}`,
		"duplicate feature": `{"version":1,"records":[` +
			`{"definition":{"key":"flag","type":"boolean","default":{"type":"boolean","boolean":true}}},` +
			`{"definition":{"key":"flag","type":"boolean","default":{"type":"boolean","boolean":true}}}]}`,
		"audit bound":      `{"version":1,"audit":[{},{}]}`,
		"stage bound":      `{"version":1,"staged":[{"id":1},{"id":2}],"next_stage":2}`,
		"invalid stage id": `{"version":1,"staged":[{"id":2,"definition":{"key":"flag","type":"boolean","default":{"type":"boolean","boolean":true}}}],"next_stage":1}`,
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			testLimits := limits
			if name == "audit bound" {
				testLimits.MaxAuditEntries = 1
			}
			if name == "stage bound" {
				testLimits.MaxStagedChanges = 1
			}
			if _, err := unmarshalTenantState([]byte(document), "tenant", testLimits); err == nil {
				t.Fatal("unmarshalTenantState() succeeded")
			}
		})
	}
	limits.MaxStateBytes = 1
	if _, err := unmarshalTenantState([]byte(`{}`), "tenant", limits); err == nil {
		t.Fatal("unmarshalTenantState(oversized) succeeded")
	}
}
