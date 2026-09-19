package claudecode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// aged writes a transcript and backdates it, because the horizon is measured
// from modification time and nothing else.
func aged(t *testing.T, dir, name string, daysOld int) {
	t.Helper()
	transcript(t, dir, name, "/work/project")
	when := time.Now().AddDate(0, 0, -daysOld)
	if err := os.Chtimes(filepath.Join(dir, name), when, when); err != nil {
		t.Fatal(err)
	}
}

// settings writes a user-level Claude Code settings file.
func settings(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHorizonSpansEveryProjectNotJustOne(t *testing.T) {
	// Retention is a property of the whole projects tree. Measuring only the
	// current directory would report a one-day horizon for a directory
	// started yesterday, which says nothing at all about pruning.
	root := projects(t)
	aged(t, filepath.Join(root, "-work-old"), "aaa.jsonl", 25)
	aged(t, filepath.Join(root, "-work-new"), "bbb.jsonl", 2)

	h := MeasureHorizon()
	if h.Sessions != 2 {
		t.Errorf("both projects count toward the horizon, got %d", h.Sessions)
	}
	if h.OldestAgeDays() != 25 {
		t.Errorf("the horizon is the oldest file anywhere, got %d days", h.OldestAgeDays())
	}
}

func TestHorizonDefaultsTheRetentionPeriodWhenUnset(t *testing.T) {
	// The common case: nobody sets cleanupPeriodDays, so Claude Code's
	// default is what will actually delete things.
	root := projects(t)
	aged(t, filepath.Join(root, "-work-project"), "aaa.jsonl", 3)

	h := MeasureHorizon()
	if h.CleanupPeriodDays != DefaultCleanupPeriodDays {
		t.Errorf("an unset period falls back to the default, got %d", h.CleanupPeriodDays)
	}
	if h.CleanupConfigured {
		t.Error("a defaulted period must not be reported as one somebody chose")
	}
}

func TestHorizonReadsAConfiguredRetentionPeriod(t *testing.T) {
	root := projects(t)
	aged(t, filepath.Join(root, "-work-project"), "aaa.jsonl", 3)
	settings(t, `{"cleanupPeriodDays": 7}`)

	h := MeasureHorizon()
	if h.CleanupPeriodDays != 7 || !h.CleanupConfigured {
		t.Errorf("a configured period should be read and marked as chosen, got %d configured=%v",
			h.CleanupPeriodDays, h.CleanupConfigured)
	}
}

func TestHorizonAtLimitDistinguishesPruningFromShortHistory(t *testing.T) {
	// The whole point of reporting two numbers. Five days of transcripts
	// under a 30-day policy means five days of use; 30 days of transcripts
	// under a 30-day policy means everything before that has been deleted.
	// Only the second makes a longer --since a lie.
	root := projects(t)
	aged(t, filepath.Join(root, "-work-project"), "aaa.jsonl", 5)
	if MeasureHorizon().AtLimit() {
		t.Error("a short history is not a pruned one")
	}

	aged(t, filepath.Join(root, "-work-project"), "bbb.jsonl", 30)
	if !MeasureHorizon().AtLimit() {
		t.Error("an oldest file at the retention period means pruning is biting")
	}
}

func TestHorizonAtLimitToleratesTheCleanupSchedule(t *testing.T) {
	// Cleanup sweeps periodically rather than continuously, so a transcript
	// can sit a little short of the period and still be the oldest one
	// there will ever be. Without the margin this flaps between runs.
	root := projects(t)
	aged(t, filepath.Join(root, "-work-project"), "aaa.jsonl", 29)

	if !MeasureHorizon().AtLimit() {
		t.Error("one day short of the period is at the limit, not comfortably inside it")
	}
}

func TestHorizonWithNoTranscriptsClaimsNothing(t *testing.T) {
	// An empty disk has no horizon to report. Zero days would read as "your
	// history was deleted this morning", which is a different and alarming
	// claim from "there is nothing here".
	projects(t)

	h := MeasureHorizon()
	switch {
	case h.Sessions != 0 || h.Subagents != 0:
		t.Errorf("nothing was written, got %d sessions and %d subagents",
			h.Sessions, h.Subagents)
	case h.AtLimit():
		t.Error("no transcripts cannot mean pruning is biting")
	case !h.Oldest.IsZero():
		t.Error("the oldest time should stay zero so callers can tell it is unset")
	}
}

func TestHorizonIgnoresMalformedSettings(t *testing.T) {
	// A settings file the tool cannot parse is not a reason to refuse to
	// measure the disk. The observed age is still true; only the policy
	// falls back.
	root := projects(t)
	aged(t, filepath.Join(root, "-work-project"), "aaa.jsonl", 4)
	settings(t, `{not json`)

	h := MeasureHorizon()
	if h.CleanupPeriodDays != DefaultCleanupPeriodDays || h.CleanupConfigured {
		t.Errorf("unparseable settings fall back to the default, got %d configured=%v",
			h.CleanupPeriodDays, h.CleanupConfigured)
	}
	if h.OldestAgeDays() != 4 {
		t.Errorf("the measurement does not depend on the settings, got %d", h.OldestAgeDays())
	}
}

func TestHorizonCountsNestedSubagentTranscripts(t *testing.T) {
	// Claude Code nests subagent transcripts under
	// <project>/<session>/subagents. A scan one level deep missed a third of
	// the files on the machine this was written on, and they are deleted on
	// the same schedule as everything else -- so they are part of the
	// horizon, counted apart from sessions because they are not sessions.
	root := projects(t)
	project := filepath.Join(root, "-work-project")
	aged(t, project, "aaa.jsonl", 3)
	aged(t, filepath.Join(project, "aaa", "subagents"), "agent-bbb.jsonl", 9)

	h := MeasureHorizon()
	if h.Sessions != 1 || h.Subagents != 1 {
		t.Errorf("a session and a subagent are counted apart, got %d and %d",
			h.Sessions, h.Subagents)
	}
	if h.OldestAgeDays() != 9 {
		t.Errorf("a nested transcript can be the oldest thing there is, got %d days",
			h.OldestAgeDays())
	}
}
