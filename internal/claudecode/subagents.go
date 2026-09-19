package claudecode

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Subagent spend is not unmeasurable, which is what this tool used to say
// about it. When a session calls Agent, Claude Code records the subagent's own
// context in its own transcript, with its own usage objects, in a directory
// next to the parent's:
//
//	<project>/<session-id>.jsonl                          the parent
//	<project>/<session-id>/subagents/agent-<id>.jsonl     one per subagent
//
// Nothing in the parent transcript carries those tokens, so a session that
// fans out to subagents reports less than it cost -- 4.7% less on the session
// this was found on, and the figure was labelled `observed`. The warning that
// fired instead said subagent usage "is not in this transcript", which was
// true and read as a limit of the data rather than a file nobody opened.

// subagentsDir is the directory Claude Code nests subagent transcripts in.
const subagentsDir = "subagents"

// SubagentTranscripts lists the subagent transcripts belonging to a session,
// given the path of the session's own transcript.
//
// Returns nothing for a transcript that has no subagent directory, which is
// most of them: the directory appears only once Agent has been called.
// Sorted by name so a profile of the same session twice reports its subagents
// in the same order.
func SubagentTranscripts(transcript string) []string {
	dir := filepath.Join(
		filepath.Dir(transcript),
		strings.TrimSuffix(filepath.Base(transcript), ".jsonl"),
		subagentsDir,
	)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out
}

// SubagentID names a subagent by its transcript filename, which is the only
// identifier available: the file is agent-<id>.jsonl and nothing inside it
// repeats the id.
func SubagentID(transcript string) string {
	return strings.TrimSuffix(filepath.Base(transcript), ".jsonl")
}
