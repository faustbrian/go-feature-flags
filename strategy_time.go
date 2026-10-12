package featureflags

import (
	"cmp"
	"fmt"
	"time"
)

// TimeWindowStrategy matches an explicit half-open interval. A zero boundary
// is unbounded; a zero context time never matches. Supplying only NotAfter
// provides deterministic time-bomb behavior.
type TimeWindowStrategy struct {
	// Name is the caller-visible match identity.
	Name string
	// Variant names the feature variant selected on a match.
	Variant string
	// NotBefore is the inclusive instant; zero is unbounded.
	NotBefore time.Time
	// NotAfter is the exclusive instant; zero is unbounded.
	NotAfter time.Time
}

// TimeBombStrategy is a time window normally configured with only NotAfter.
type TimeBombStrategy = TimeWindowStrategy

// StrategyName returns the caller-visible policy name.
func (s TimeWindowStrategy) StrategyName() string { return s.Name }

// TargetVariant returns the variant selected on a match.
func (s TimeWindowStrategy) TargetVariant() string { return s.Variant }

// ValidateStrategy checks policy bounds; feature validation checks target existence.
func (s TimeWindowStrategy) ValidateStrategy(Limits) error {
	if s.NotBefore.IsZero() && s.NotAfter.IsZero() {
		return fmt.Errorf("at least one time boundary is required")
	}
	if !s.NotBefore.IsZero() && !s.NotAfter.IsZero() && !s.NotBefore.Before(s.NotAfter) {
		return fmt.Errorf("not-before must precede not-after")
	}

	return nil
}

// EvaluateStrategy evaluates explicit borrowed context without ambient clock or request
// state.
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

// SnapshotStrategy isolates mutable policy state for concurrent snapshot evaluation.
func (s TimeWindowStrategy) SnapshotStrategy() Strategy { return s }

// WeeklyWindow is a local-time half-open interval. When EndMinute is less than
// StartMinute the window continues into the following weekday.
type WeeklyWindow struct {
	// Weekday is the local starting weekday, Sunday through Saturday.
	Weekday time.Weekday
	// StartMinute is the inclusive local minute in [0,1440).
	StartMinute int
	// EndMinute is exclusive in [0,1440]; a lower value crosses into the next day.
	EndMinute int
}

// ScheduleStrategy evaluates recurring weekly windows in an explicit IANA
// time zone.
type ScheduleStrategy struct {
	// Name is the caller-visible match identity.
	Name string
	// Variant names the feature variant selected on a match.
	Variant string
	// Location is resolved by time.LoadLocation; deployment tzdata affects results.
	Location string
	// Windows is copied on snapshot; seconds are ignored by local-minute matching.
	Windows []WeeklyWindow
}

// StrategyName returns the caller-visible policy name.
func (s ScheduleStrategy) StrategyName() string { return s.Name }

// TargetVariant returns the variant selected on a match.
func (s ScheduleStrategy) TargetVariant() string { return s.Variant }

// ValidateStrategy checks policy bounds; feature validation checks target existence.
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

// EvaluateStrategy evaluates explicit borrowed context without ambient clock or request
// state.
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

// SnapshotStrategy isolates mutable policy state for concurrent snapshot evaluation.
func (s ScheduleStrategy) SnapshotStrategy() Strategy {
	s.Windows = append([]WeeklyWindow(nil), s.Windows...)

	return s
}

func weeklyWindowMatches(window WeeklyWindow, weekday time.Weekday, minute int) bool {
	switch cmp.Compare(window.StartMinute, window.EndMinute) {
	case -1:
		return weekday == window.Weekday && minute >= window.StartMinute && minute < window.EndMinute
	case 0:
		return false
	}
	if weekday == window.Weekday && minute >= window.StartMinute {
		return true
	}
	next := (window.Weekday + 1) % 7

	return weekday == next && minute < window.EndMinute
}
