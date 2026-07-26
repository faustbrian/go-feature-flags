package featureflags

import "testing"

type diagnosticStrategy struct{}

func (diagnosticStrategy) StrategyName() string          { return "custom" }
func (diagnosticStrategy) TargetVariant() string         { return "enabled" }
func (diagnosticStrategy) ValidateStrategy(Limits) error { return nil }
func (diagnosticStrategy) SnapshotStrategy() Strategy    { return diagnosticStrategy{} }
func (diagnosticStrategy) EvaluateStrategy(StrategyInput) (StrategyResult, error) {
	return StrategyResult{Match: true, Diagnostics: []Diagnostic{
		{Code: "first-diagnostic-code", Message: "first diagnostic message"},
		{Code: "second", Message: "second"},
	}}, nil
}

func TestCustomStrategyDiagnosticsAreBounded(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	limits.MaxDiagnostics = 1
	limits.MaxDiagnosticBytes = 8
	snapshot, err := NewSnapshot([]Definition{{
		Key:        "custom.flag",
		Type:       TypeBoolean,
		Default:    BooleanValue(false),
		Lifecycle:  LifecycleActive,
		Variants:   map[string]Value{"enabled": BooleanValue(true)},
		Strategies: []Strategy{diagnosticStrategy{}},
	}}, limits)
	if err != nil {
		t.Fatalf("NewSnapshot() error = %v", err)
	}

	detail, err := snapshot.Boolean("custom.flag", Context{})
	if err != nil {
		t.Fatalf("Boolean() error = %v", err)
	}
	if len(detail.Diagnostics) != 1 {
		t.Fatalf("Boolean() diagnostics = %d, want 1", len(detail.Diagnostics))
	}
	if len(detail.Diagnostics[0].Code) > 8 || len(detail.Diagnostics[0].Message) > 8 {
		t.Fatalf("Boolean() diagnostic = %#v, want fields bounded to 8 bytes", detail.Diagnostics[0])
	}
}
