package featureflags

import (
	"testing"
	"time"
)

func TestTimeWindowStrategyUsesExplicitHalfOpenBoundaries(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	strategy := TimeWindowStrategy{Name: "launch", Variant: "enabled", NotBefore: start, NotAfter: end}

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "missing explicit time", want: false},
		{name: "before", now: start.Add(-time.Nanosecond), want: false},
		{name: "start inclusive", now: start, want: true},
		{name: "inside", now: start.Add(time.Hour), want: true},
		{name: "end exclusive", now: end, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := strategy.EvaluateStrategy(StrategyInput{Context: Context{Time: test.now}})
			if err != nil {
				t.Fatalf("EvaluateStrategy() error = %v", err)
			}
			if result.Match != test.want {
				t.Fatalf("EvaluateStrategy() match = %t, want %t", result.Match, test.want)
			}
		})
	}
}

func TestScheduleStrategyEvaluatesInConfiguredTimeZone(t *testing.T) {
	t.Parallel()

	strategy := ScheduleStrategy{
		Name:     "business-hours",
		Variant:  "enabled",
		Location: "Europe/Helsinki",
		Windows: []WeeklyWindow{{
			Weekday:     time.Monday,
			StartMinute: 9 * 60,
			EndMinute:   17 * 60,
		}},
	}

	for _, test := range []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "local start inclusive", now: time.Date(2026, 7, 20, 6, 0, 0, 0, time.UTC), want: true},
		{name: "local end exclusive", now: time.Date(2026, 7, 20, 14, 0, 0, 0, time.UTC), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := strategy.EvaluateStrategy(StrategyInput{Context: Context{Time: test.now}})
			if err != nil {
				t.Fatalf("EvaluateStrategy() error = %v", err)
			}
			if result.Match != test.want {
				t.Fatalf("EvaluateStrategy() match = %t, want %t", result.Match, test.want)
			}
		})
	}
}

func TestScheduleStrategyRequiresExplicitTime(t *testing.T) {
	t.Parallel()

	result, err := (ScheduleStrategy{Location: "UTC"}).EvaluateStrategy(StrategyInput{})
	if err != nil || result.Match || len(result.Diagnostics) != 1 {
		t.Fatalf("EvaluateStrategy() = (%#v, %v)", result, err)
	}
}
