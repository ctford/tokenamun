package claudecode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSubagentTranscriptsFindsTheNestedDirectory(t *testing.T) {
	// The layout that went unread: a session's subagents live in a
	// directory named after the session, beside the session's transcript.
	project := t.TempDir()
	parent := filepath.Join(project, "s1.jsonl")
	if err := os.WriteFile(parent, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(project, "s1", "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"agent-bbb.jsonl", "agent-aaa.jsonl", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got := SubagentTranscripts(parent)
	if len(got) != 2 {
		t.Fatalf("two transcripts and one stray file: got %d", len(got))
	}
	// Sorted, so profiling the same session twice reports the same order.
	if SubagentID(got[0]) != "agent-aaa" || SubagentID(got[1]) != "agent-bbb" {
		t.Errorf("results should be sorted by name, got %v", got)
	}
}

func TestSubagentTranscriptsIsEmptyForAnOrdinarySession(t *testing.T) {
	// Most sessions never call Agent, so the directory is simply absent.
	project := t.TempDir()
	parent := filepath.Join(project, "s1.jsonl")
	if err := os.WriteFile(parent, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := SubagentTranscripts(parent); got != nil {
		t.Errorf("no directory means no subagents, got %v", got)
	}
}
