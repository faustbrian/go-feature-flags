package featureflags

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDurableProviderCoversImportAndMutationFailureBranches(t *testing.T) {
	t.Parallel()

	backend := newFakeDocumentBackend()
	provider := NewDurableProvider(backend, DefaultLimits())
	document, err := Export([]Definition{{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(true),
	}}, nil, DefaultLimits())
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	report, err := provider.ImportDocument(
		t.Context(), "tenant", document, ImportOptions{}, "actor",
	)
	if err != nil || report.CreatedFeatures != 1 {
		t.Fatalf("ImportDocument() = (%#v, %v)", report, err)
	}
	if _, err := provider.Create(t.Context(), "tenant", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(true),
	}, "actor"); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("Create(duplicate) error = %v", err)
	}
	if _, err := provider.Create(t.Context(), "", Definition{}, "actor"); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("Create(empty tenant) error = %v", err)
	}
	if _, _, err := provider.load(t.Context(), ""); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("load(empty tenant) error = %v", err)
	}

	boom := errors.New("load failure")
	failing := &failingDocumentBackend{loadErr: boom}
	failingProvider := NewDurableProvider(failing, DefaultLimits())
	if _, err := failingProvider.Create(t.Context(), "tenant", Definition{
		Key: "flag", Type: TypeBoolean, Default: BooleanValue(true),
	}, "actor"); !errors.Is(err, boom) {
		t.Fatalf("Create(load failure) error = %v", err)
	}
	if _, err := failingProvider.ImportDocument(
		t.Context(), "tenant", document, ImportOptions{DryRun: true}, "actor",
	); !errors.Is(err, boom) {
		t.Fatalf("ImportDocument(dry-run load failure) error = %v", err)
	}

	custom := Definition{
		Key: "custom", Type: TypeBoolean, Default: BooleanValue(false),
		Variants:   map[string]Value{"enabled": BooleanValue(true)},
		Strategies: []Strategy{diagnosticStrategy{}},
	}
	if _, err := provider.Create(t.Context(), "custom", custom, "actor"); !errors.Is(err, ErrUnsupportedStrategy) {
		t.Fatalf("Create(custom strategy) error = %v", err)
	}
	corruptBackend := &failingDocumentBackend{
		loadData: []byte(`{"version":2}`), loadFound: true,
	}
	if _, err := NewDurableProvider(corruptBackend, DefaultLimits()).Snapshot(
		t.Context(), "tenant",
	); err == nil {
		t.Fatal("Snapshot(corrupt durable state) succeeded")
	}
}

func TestDurableStateCodecPropagatesNestedFailures(t *testing.T) {
	t.Parallel()

	provider := NewMemoryProvider(DefaultLimits())
	provider.tenants["tenant"] = map[string]memoryRecord{"custom": {
		definition: Definition{
			Key: "custom", Type: TypeBoolean, Default: BooleanValue(false),
			Strategies: []Strategy{diagnosticStrategy{}},
		},
	}}
	if _, err := marshalTenantState(provider, "tenant"); !errors.Is(err, ErrUnsupportedStrategy) {
		t.Fatalf("marshalTenantState(feature strategy) error = %v", err)
	}
	provider = NewMemoryProvider(DefaultLimits())
	provider.groups["tenant"] = map[string]GroupDefinition{"custom": {
		Key: "custom", Strategies: []Strategy{diagnosticStrategy{}},
	}}
	if _, err := marshalTenantState(provider, "tenant"); !errors.Is(err, ErrUnsupportedStrategy) {
		t.Fatalf("marshalTenantState(group strategy) error = %v", err)
	}
	provider = NewMemoryProvider(DefaultLimits())
	provider.staged["tenant"] = map[uint64]StagedChange{2: {
		ID: 2, Definition: Definition{
			Key: "custom", Type: TypeBoolean, Default: BooleanValue(false),
			Strategies: []Strategy{diagnosticStrategy{}},
		},
	}, 1: {
		ID: 1, Definition: Definition{Key: "valid", Type: TypeBoolean, Default: BooleanValue(false)},
	}}
	if _, err := marshalTenantState(provider, "tenant"); !errors.Is(err, ErrUnsupportedStrategy) {
		t.Fatalf("marshalTenantState(stage strategy) error = %v", err)
	}
	provider = NewMemoryProvider(DefaultLimits())
	provider.tenants["tenant"] = map[string]memoryRecord{"invalid": {
		definition: Definition{
			Key: "invalid", Type: TypeStructured,
			Default: Value{typ: TypeStructured, structured: json.RawMessage(`{`)},
		},
	}}
	if _, err := marshalTenantState(provider, "tenant"); err == nil {
		t.Fatal("marshalTenantState(invalid JSON) succeeded")
	}
	provider = NewMemoryProvider(DefaultLimits())
	provider.limits.MaxStateBytes = 1
	if _, err := marshalTenantState(provider, "tenant"); !errors.Is(err, ErrStateLimit) {
		t.Fatalf("marshalTenantState(oversized) error = %v", err)
	}
}

func TestDurableStateDecoderRejectsNestedRecordGroupAndStageFailures(t *testing.T) {
	t.Parallel()

	documents := map[string]string{
		"record decode":   `{"version":1,"records":[{"definition":{"key":"flag","type":"boolean","default":{"type":"boolean"}}}]}`,
		"record validate": `{"version":1,"records":[{"definition":{"key":"","type":"boolean","default":{"type":"boolean","boolean":true}}}]}`,
		"group decode":    `{"version":1,"groups":[{"key":"group","strategies":[{"kind":"future"}]}]}`,
		"group validate":  `{"version":1,"groups":[{"key":"group","parent":"missing"}]}`,
		"stage decode":    `{"version":1,"staged":[{"id":1,"definition":{"key":"flag","type":"boolean","default":{"type":"boolean"}}}],"next_stage":1}`,
		"stage validate":  `{"version":1,"staged":[{"id":1,"definition":{"key":"","type":"boolean","default":{"type":"boolean","boolean":true}}}],"next_stage":1}`,
		"duplicate stage": `{"version":1,"staged":[` +
			`{"id":1,"definition":{"key":"a","type":"boolean","default":{"type":"boolean","boolean":true}}},` +
			`{"id":1,"definition":{"key":"b","type":"boolean","default":{"type":"boolean","boolean":true}}}],"next_stage":1}`,
	}
	for name, document := range documents {
		t.Run(name, func(t *testing.T) {
			if _, err := unmarshalTenantState(
				[]byte(document), "tenant", DefaultLimits(),
			); err == nil {
				t.Fatal("unmarshalTenantState() succeeded")
			}
		})
	}
}
