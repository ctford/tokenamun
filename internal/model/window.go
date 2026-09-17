package model

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Window is a period to scope an analysis to.
//
// It exists for the before-and-after question: what did sessions cost after
// we added the thing, against before. A zero Window includes everything,
// which is what every command did before there was one.
type Window struct {
	Since time.Time
	Until time.Time
}

// Includes reports whether an instant falls in the window.
func (w Window) Includes(t time.Time) bool {
	if !w.Since.IsZero() && t.Before(w.Since) {
		return false
	}
	if !w.Until.IsZero() && !t.Before(w.Until) {
		return false
	}
	return true
}

// Empty reports whether the window constrains anything.
func (w Window) Empty() bool { return w.Since.IsZero() && w.Until.IsZero() }

// String describes the window the way a report should print it.
func (w Window) String() string {
	switch {
	case w.Empty():
		return "all sessions"
	case w.Until.IsZero():
		return "since " + w.Since.Format(time.DateOnly)
	case w.Since.IsZero():
		return "before " + w.Until.Format(time.DateOnly)
	default:
		return w.Since.Format(time.DateOnly) + " to " + w.Until.Format(time.DateOnly)
	}
}

// Flags renders the window back as the flags that would reproduce it, so a
// report can print a command that re-runs it.
//
// Always absolute, never the age form the caller may have typed: "--since
// 7d" means a different week next week, and a command printed in a report is
// read later by definition.
//
// A whole day prints as a date, which is what somebody reading it expects to
// see. Anything else prints as RFC3339, because an age resolves to an instant
// mid-day and rounding it down to the date would hand back a *wider* window
// than the one the numbers above it were computed from.
//
// Empty where the window constrains nothing, so it composes into a selector
// by concatenation.
func (w Window) Flags() string {
	var out string
	if !w.Since.IsZero() {
		out += " --since " + instantFlag(w.Since)
	}
	if !w.Until.IsZero() {
		out += " --until " + instantFlag(w.Until)
	}
	return out
}

func instantFlag(t time.Time) string {
	if t.Equal(t.Truncate(24*time.Hour)) || t.Format("15:04:05.999999999") == "00:00:00" {
		return t.Format(time.DateOnly)
	}
	return t.Format(time.RFC3339)
}

// ParseWindow reads --since and --until.
//
// Three forms, because an experiment is described in whichever is to hand: a
// date, a date and time, or an age like "7d". Parsed in local time, since the
// person naming a day means their day.
//
// --until is exclusive at day granularity: "--until 2026-09-16" means work
// before the 16th, not including it. Inclusive would be the other reasonable
// choice, and the only wrong thing is not saying which.
func ParseWindow(since, until string) (Window, error) {
	var w Window
	var err error
	if since != "" {
		if w.Since, err = parseInstant(since); err != nil {
			return w, fmt.Errorf("--since %q: %w", since, err)
		}
	}
	if until != "" {
		if w.Until, err = parseInstant(until); err != nil {
			return w, fmt.Errorf("--until %q: %w", until, err)
		}
	}
	if !w.Since.IsZero() && !w.Until.IsZero() && !w.Since.Before(w.Until) {
		return w, fmt.Errorf("--since %s is not before --until %s, so the window is empty",
			w.Since.Format(time.DateOnly), w.Until.Format(time.DateOnly))
	}
	return w, nil
}

func parseInstant(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	// An age: 7d, 36h, 90m. Relative to now, which is what "the last week"
	// means when you type it.
	if d, err := parseAge(s); err == nil {
		return time.Now().Add(-d), nil
	}
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		time.DateOnly,
	} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("want a date (2026-09-16), a date and time, or an age (7d)")
}

func parseAge(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("not an age")
	}
	unit := s[len(s)-1]
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 0 {
		return 0, fmt.Errorf("not an age")
	}
	switch unit {
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'm':
		return time.Duration(n) * time.Minute, nil
	}
	return 0, fmt.Errorf("not an age")
}

// InWindow keeps the session refs whose last activity falls in the window.
//
// Filtered by session rather than by call. A session is the unit that has a
// context, and cost is attributed by residency within one: truncating a
// session to a window would leave content that entered before it being
// carried through it, with no honest way to say how much of that cost belongs
// inside. Whole sessions compose; halves do not.
func InWindow(refs []SessionRef, w Window) []SessionRef {
	if w.Empty() {
		return refs
	}
	var out []SessionRef
	for _, r := range refs {
		if w.Includes(r.Modified) {
			out = append(out, r)
		}
	}
	return out
}
