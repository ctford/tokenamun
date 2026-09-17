package model

import (
	"testing"
	"time"
)

func TestWindowFormsAnExperimentIsDescribedIn(t *testing.T) {
	// Three forms, because an experiment is described in whichever is to
	// hand: a date, a date and time, or an age.
	for _, s := range []string{"2026-09-16", "2026-09-16 14:30", "7d", "36h", "2w", "90m"} {
		if _, err := ParseWindow(s, ""); err != nil {
			t.Errorf("ParseWindow(%q) = %v", s, err)
		}
	}
	if _, err := ParseWindow("last tuesday", ""); err == nil {
		t.Error("an unparseable date must be refused, not silently ignored")
	}

	// A backwards window is refused up front, rather than matching nothing
	// and looking like an empty repository.
	if _, err := ParseWindow("2026-09-17", "2026-09-01"); err == nil {
		t.Error("a window that ends before it starts is empty by construction")
	}
}

func TestWindowBoundariesAreStated(t *testing.T) {
	w, err := ParseWindow("2026-09-15", "2026-09-17")
	if err != nil {
		t.Fatal(err)
	}
	at := func(day, hour int) time.Time {
		return time.Date(2026, 9, day, hour, 0, 0, 0, time.Local)
	}
	// --since is inclusive and --until exclusive, which is a choice; the
	// only wrong thing would be not saying which.
	for _, tc := range []struct {
		t    time.Time
		want bool
	}{
		{at(14, 23), false},
		{at(15, 0), true},
		{at(16, 12), true},
		{at(17, 0), false},
	} {
		if got := w.Includes(tc.t); got != tc.want {
			t.Errorf("Includes(%s) = %v, want %v", tc.t.Format(time.RFC3339), got, tc.want)
		}
	}
	if w.String() != "2026-09-15 to 2026-09-17" {
		t.Errorf("a report has to print the window it covers, got %q", w.String())
	}

	// A zero window is everything, which is what every command did before
	// there was one.
	var all Window
	if !all.Empty() || !all.Includes(at(1, 0)) || all.String() != "all sessions" {
		t.Error("the zero window must include everything and say so")
	}
}

func TestInWindowFiltersWholeSessions(t *testing.T) {
	// Whole sessions, never halves. Cost is attributed by residency within a
	// session, so truncating one would leave content that entered before the
	// window being carried through it, with no honest way to split the cost.
	refs := []SessionRef{
		{ID: "old", Modified: time.Date(2026, 9, 10, 9, 0, 0, 0, time.Local)},
		{ID: "in", Modified: time.Date(2026, 9, 16, 9, 0, 0, 0, time.Local)},
		{ID: "new", Modified: time.Date(2026, 9, 20, 9, 0, 0, 0, time.Local)},
	}
	w, err := ParseWindow("2026-09-15", "2026-09-17")
	if err != nil {
		t.Fatal(err)
	}
	got := InWindow(refs, w)
	if len(got) != 1 || got[0].ID != "in" {
		t.Errorf("got %+v, want just the session inside", got)
	}
	if len(InWindow(refs, Window{})) != 3 {
		t.Error("the zero window must not filter")
	}
}
