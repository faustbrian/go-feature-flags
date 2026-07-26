package featureflags

import "testing"

func TestSetStrategyDenyListsOverrideAllowLists(t *testing.T) {
	t.Parallel()

	strategy := SetStrategy{
		Name:          "beta-cohort",
		Variant:       "enabled",
		AllowTenants:  []string{"tenant-a"},
		AllowSubjects: []string{"alice", "bob"},
		DenySubjects:  []string{"bob"},
	}

	tests := []struct {
		name    string
		context Context
		want    bool
	}{
		{name: "allowed", context: Context{Tenant: "tenant-a", Subject: "alice"}, want: true},
		{name: "subject denied", context: Context{Tenant: "tenant-a", Subject: "bob"}, want: false},
		{name: "tenant not allowed", context: Context{Tenant: "tenant-b", Subject: "alice"}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := strategy.EvaluateStrategy(StrategyInput{Context: test.context})
			if err != nil {
				t.Fatalf("EvaluateStrategy() error = %v", err)
			}
			if result.Match != test.want {
				t.Fatalf("EvaluateStrategy() match = %t, want %t", result.Match, test.want)
			}
		})
	}
}
