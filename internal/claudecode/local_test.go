package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// projects lays out a ~/.claude/projects tree and points HOME at it.
func projects(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	return filepath.Join(home, ".claude", "projects")
}

// transcript writes a one-line transcript recording a cwd.
func transcript(t *testing.T, dir, name, cwd string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","cwd":"` + cwd + `","message":{"id":"m","usage":{"input_tokens":1}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverLocalFindsSessionsBySlugifiedPath(t *testing.T) {
	root := projects(t)
	transcript(t, filepath.Join(root, slug("/work/project")), "aaa.jsonl", "/work/project")

	refs, err := DiscoverLocal("/work/project")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one session, got %d", len(refs))
	}
	if refs[0].ID != "aaa" {
		t.Errorf("the session id is the filename, got %q", refs[0].ID)
	}
	if refs[0].Origin != "local" {
		t.Errorf("origin should record where this came from, got %q", refs[0].Origin)
	}
	if refs[0].Modified.IsZero() {
		t.Error("the modification time is what the latest selector sorts on")
	}
}

func TestDiscoverLocalFallsBackToTheRecordedCWD(t *testing.T) {
	// Claude Code's slug rules could change, and the caller may pass a path
	// that differs from the recorded one. Each transcript records its own cwd,
	// which is observed rather than guessed, so that is the fallback.
	root := projects(t)
	transcript(t, filepath.Join(root, "some-other-naming-scheme"), "bbb.jsonl", "/work/project")

	refs, err := DiscoverLocal("/work/project")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != "bbb" {
		t.Fatalf("expected to find bbb by its recorded cwd, got %+v", refs)
	}
}

func TestDiscoverLocalRefusesSessionsFromAnotherRepository(t *testing.T) {
	root := projects(t)
	transcript(t, filepath.Join(root, slug("/work/project")), "mine.jsonl", "/work/project")
	transcript(t, filepath.Join(root, slug("/work/other")), "theirs.jsonl", "/work/other")

	refs, err := DiscoverLocal("/work/project")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range refs {
		if r.ID == "theirs" {
			t.Error("a session recorded in another repository must not be offered")
		}
	}
	if len(refs) != 1 {
		t.Fatalf("expected only this repository's session, got %+v", refs)
	}
}

func TestDiscoverLocalIsQuietWhenThereIsNothingThere(t *testing.T) {
	// The common case on a machine that has never run Claude Code. A missing
	// directory is not an error, or every other source would be unreachable.
	projects(t)
	refs, err := DiscoverLocal("/work/project")
	if err != nil {
		t.Errorf("a missing projects directory is not an error: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("expected nothing, got %+v", refs)
	}
}

func TestCurrentSessionIsTheOneInvokingTheTool(t *testing.T) {
	root := projects(t)
	transcript(t, filepath.Join(root, slug("/work/project")), "running.jsonl", "/work/project")
	transcript(t, filepath.Join(root, slug("/work/project")), "older.jsonl", "/work/project")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "running")

	if got := CurrentSessionID(); got != "running" {
		t.Fatalf("CurrentSessionID reads the variable Claude Code exports, got %q", got)
	}
	refs, err := DiscoverLocal("/work/project")
	if err != nil {
		t.Fatal(err)
	}
	var marked []string
	for _, r := range refs {
		if r.Current {
			marked = append(marked, r.ID)
		}
	}
	if len(marked) != 1 || marked[0] != "running" {
		t.Errorf("exactly the invoking session should be marked current, got %v", marked)
	}
}

func TestProjectsDirIsUnderTheHomeDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := ProjectsDir()
	if !strings.HasPrefix(got, home) || !strings.HasSuffix(got, filepath.Join(".claude", "projects")) {
		t.Errorf("unexpected projects directory %q", got)
	}
}

func TestSlugMatchesClaudeCodesNaming(t *testing.T) {
	// Verified against Claude Code 2.1.x: the absolute path with separators
	// replaced by hyphens, so the leading separator becomes a leading hyphen.
	if got := slug("/a/b"); got != "-a-b" {
		t.Errorf("slug(/a/b) = %q, want -a-b", got)
	}
}
