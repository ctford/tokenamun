package claudecode

import (
	"encoding/json"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"time"
)

// Claude Code deletes its own transcripts. Everything this tool knows about
// local sessions comes from files that Claude Code itself removes after a
// while, so `--since 90d` over a 30-day disk is not an error and not an empty
// result -- it is 30 days of data reported as though it were 90. That silence
// is the problem this file exists to break.
//
// Two numbers are needed to say anything useful, and they are different kinds
// of thing. How far back the files on disk actually go is a measurement. How
// far back Claude Code intends to keep them is configuration, read from the
// same settings file Claude Code reads. Reported separately, because the gap
// between them is what tells you which of two situations you are in.

// DefaultCleanupPeriodDays is Claude Code's retention default, applied when
// settings.json does not set cleanupPeriodDays.
//
// Configuration, not measurement: it is Claude Code's documented default, and
// a version of Claude Code that changed it would make this wrong without
// anything here failing. The observed age is measured either way, so a stale
// default misreads the policy and never the disk.
const DefaultCleanupPeriodDays = 30

// atLimitMarginDays is how close the oldest file has to sit to the retention
// period before the horizon is treated as imposed rather than incidental.
//
// One day, because cleanup runs on a schedule rather than continuously: a
// transcript can be a few hours past the period and still be on disk waiting
// for the next sweep. Anything tighter would flap between runs.
const atLimitMarginDays = 1

// Horizon is how far back Claude Code's transcripts go here, and the policy
// that bounds them.
type Horizon struct {
	// Sessions counts top-level transcripts across every project, not just
	// the current directory. Retention is a property of the whole
	// ~/.claude/projects tree, and a directory you started using yesterday
	// would otherwise report a one-day horizon that says nothing about
	// pruning.
	Sessions int `json:"sessions"`
	// Subagents counts the nested transcripts under
	// <project>/<session>/subagents. They are deleted on the same schedule,
	// so they belong in the horizon, but they are not sessions and summing
	// the two would overstate how much history there is.
	Subagents int `json:"subagents"`
	// Oldest and Newest are file modification times. Zero when nothing was
	// found.
	Oldest time.Time `json:"oldest,omitempty"`
	Newest time.Time `json:"newest,omitempty"`
	// CleanupPeriodDays is what Claude Code will enforce, whether it was set
	// or defaulted. CleanupConfigured distinguishes the two, because "you
	// chose 7" and "nobody chose, so 30" want different advice.
	CleanupPeriodDays int  `json:"cleanup_period_days"`
	CleanupConfigured bool `json:"cleanup_configured"`
}

// MeasureHorizon reads the modification times under ~/.claude/projects and
// the retention setting that governs them.
//
// Modification time is an approximation of session age in two directions, and
// both are worth knowing before trusting the number. A session that started
// three weeks ago and was resumed this morning has a modification time of
// this morning, so it counts as recent and survives cleanup -- which is
// correct for predicting deletion and wrong for describing the history. And
// copying ~/.claude to a new machine rewrites every modification time, making
// the horizon look far younger than the history really is.
//
// It is still the right thing to measure, because it is what cleanup itself
// keys on. Assumption, matching Claude Code 2.1.x behaviour: transcripts are
// deleted by age from last modification, not from session start.
func MeasureHorizon() Horizon {
	h := Horizon{CleanupPeriodDays: DefaultCleanupPeriodDays}
	if days, ok := cleanupPeriodDays(); ok {
		h.CleanupPeriodDays, h.CleanupConfigured = days, true
	}

	root := ProjectsDir()
	if root == "" {
		return h
	}
	// Walked rather than listed. A first version read each project directory
	// one level deep and missed a third of the transcripts on this machine:
	// subagent transcripts are nested at
	// <project>/<session>/subagents/agent-*.jsonl, and a horizon that does
	// not see them describes only part of what cleanup will delete.
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(d.Name()) != ".jsonl" {
			// A directory that cannot be read is skipped, not fatal: a
			// partial horizon is worth more than none.
			return nil //nolint:nilerr // unreadable entries are skipped by design
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if filepath.Dir(filepath.Dir(path)) == root {
			h.Sessions++
		} else {
			h.Subagents++
		}
		mod := info.ModTime()
		if h.Oldest.IsZero() || mod.Before(h.Oldest) {
			h.Oldest = mod
		}
		if mod.After(h.Newest) {
			h.Newest = mod
		}
		return nil
	})
	return h
}

// OldestAgeDays is how far back the transcripts on disk reach, in days.
// Reported as a whole number of days because that is the unit the retention
// period is expressed in, and comparing the two is the entire point.
func (h Horizon) OldestAgeDays() int {
	if h.Oldest.IsZero() {
		return 0
	}
	return int(math.Floor(time.Since(h.Oldest).Hours() / 24))
}

// AtLimit reports whether the horizon looks imposed by cleanup rather than by
// how long Claude Code has been used here.
//
// It cannot be certain, and does not pretend to be: a machine whose first
// session was exactly one retention period ago is indistinguishable from one
// that has been pruned for a year. Both warrant the same caution about
// `--since`, which is why one answer serves for both.
func (h Horizon) AtLimit() bool {
	return h.Sessions+h.Subagents > 0 && h.OldestAgeDays() >= h.CleanupPeriodDays-atLimitMarginDays
}

// cleanupPeriodDays reads Claude Code's retention setting.
//
// Only the user-level file is read. Cleanup is one process sweeping the whole
// projects tree, so a per-project override would not describe what happens to
// the other projects in it -- and the horizon this reports spans all of them.
func cleanupPeriodDays() (int, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return 0, false
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return 0, false
	}
	var s struct {
		CleanupPeriodDays *int `json:"cleanupPeriodDays"`
	}
	if err := json.Unmarshal(raw, &s); err != nil || s.CleanupPeriodDays == nil {
		return 0, false
	}
	return *s.CleanupPeriodDays, true
}
