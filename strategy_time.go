package featureflags

import (
	"fmt"
	"time"
)

// TimeWindowStrategy matches an explicit half-open interval. A zero boundary
// is unbounded; a zero context time never matches. Supplying only NotAfter
// provides deterministic time-bomb behavior.
type TimeWindowStrategy struct {
	Name      string
	Variant   string
	NotBefore time.Time
	NotAfter  time.Time
}

// TimeBombStrategy is a time window normally configured with only NotAfter.
type TimeBombStrategy = TimeWindowStrategy

func (s TimeWindowStrategy) StrategyName() string { return s.Name }

func (s TimeWindowStrategy) TargetVariant() string { return s.Variant }

func (s TimeWindowStrategy) ValidateStrategy(Limits) error {
	if s.NotBefore.IsZero() && s.NotAfter.IsZero() {
		return fmt.Errorf("at least one time boundary is required")
	}
	if !s.NotBefore.IsZero() && !s.NotAfter.IsZero() && !s.NotBefore.Before(s.NotAfter) {
		return fmt.Errorf("not-before must precede not-after")
	}

	return nil
}

func (s TimeWindowStrategy) EvaluateStrategy(input StrategyInput) (StrategyResult, error) {
	now := input.Context.Time
	if now.IsZero() {
		return StrategyResult{
			Reason: ReasonSchedule,
			Diagnostics: []Diagnostic{{
				Code:    "missing_evaluation_time",
				Message: "time strategy requires explicit context time",
			}},
		}, nil
	}
	match := (s.NotBefore.IsZero() || !now.Before(s.NotBefore)) &&
		(s.NotAfter.IsZero() || now.Before(s.NotAfter))

	return StrategyResult{Match: match, Reason: ReasonSchedule}, nil
}

func (s TimeWindowStrategy) SnapshotStrategy() Strategy { return s }

// WeeklyWindow is a local-time half-open interval. When EndMinute is less than
// StartMinute the window continues into the following weekday.
type WeeklyWindow struct {
	Weekday     time.Weekday
	StartMinute int
	EndMinute   int
}

// ScheduleStrategy evaluates recurring weekly windows in an explicit IANA
// time zone.
type ScheduleStrategy struct {
	Name     string
	Variant  string
	Location string
	Windows  []WeeklyWindow
}

func (s ScheduleStrategy) StrategyName() string { return s.Name }

func (s ScheduleStrategy) TargetVariant() string { return s.Variant }

func (s ScheduleStrategy) ValidateStrategy(limits Limits) error {
	if s.Location == "" {
		return fmt.Errorf("location is required")
	}
	if _, err := time.LoadLocation(s.Location); err != nil {
		return fmt.Errorf("load location: %w", err)
	}
	if len(s.Windows) == 0 {
		return fmt.Errorf("at least one weekly window is required")
	}
	if len(s.Windows) > limits.MaxScheduleWindows {
		return fmt.Errorf("weekly windows exceed limit %d", limits.MaxScheduleWindows)
	}
	for index, window := range s.Windows {
		if window.Weekday < time.Sunday || window.Weekday > time.Saturday {
			return fmt.Errorf("window %d has invalid weekday %d", index, window.Weekday)
		}
		if window.StartMinute < 0 || window.StartMinute >= 24*60 ||
			window.EndMinute < 0 || window.EndMinute > 24*60 ||
			window.StartMinute == window.EndMinute {
			return fmt.Errorf("window %d has invalid minute range", index)
		}
	}

	return nil
}

func (s ScheduleStrategy) EvaluateStrategy(input StrategyInput) (StrategyResult, error) {
	if input.Context.Time.IsZero() {
		return StrategyResult{
			Reason: ReasonSchedule,
			Diagnostics: []Diagnostic{{
				Code:    "missing_evaluation_time",
				Message: "schedule requires explicit context time",
			}},
		}, nil
	}
	location, err := time.LoadLocation(s.Location)
	if err != nil {
		return StrategyResult{Reason: ReasonSchedule}, fmt.Errorf("load location: %w", err)
	}
	local := input.Context.Time.In(location)
	minute := local.Hour()*60 + local.Minute()
	for _, window := range s.Windows {
		if weeklyWindowMatches(window, local.Weekday(), minute) {
			return StrategyResult{Match: true, Reason: ReasonSchedule}, nil
		}
	}

	return StrategyResult{Reason: ReasonSchedule}, nil
}

func (s ScheduleStrategy) SnapshotStrategy() Strategy {
	s.Windows = append([]WeeklyWindow(nil), s.Windows...)

	return s
}

func weeklyWindowMatches(window WeeklyWindow, weekday time.Weekday, minute int) bool {
	if window.StartMinute < window.EndMinute {
		return weekday == window.Weekday && minute >= window.StartMinute && minute < window.EndMinute
	}
	if weekday == window.Weekday && minute >= window.StartMinute {
		return true
	}
	next := (window.Weekday + 1) % 7

	return weekday == next && minute < window.EndMinute
}
