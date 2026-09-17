package entire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Check is one thing that has to be true for a session to be recorded.
type Check struct {
	Name string `json:"name"`
	// OK is false when this is the step that is missing.
	OK bool `json:"ok"`
	// Found is what was actually there, so a reader can tell "absent" from
	// "present and wrong".
	Found string `json:"found"`
	// Fix is what to do about it. Empty when nothing is wrong.
	Fix string `json:"fix,omitempty"`
}

// Diagnose reports whether Entire is set up to record in a repository.
//
// It exists because "no sessions found" was the answer to four different
// problems, and the difference between them is the whole of what a reader
// needs. Entire records through Claude Code hooks, so the chain is: a
// settings file, hooks that actually invoke it, evidence it has run, and
// transcripts on disk. Each link can be missing on its own, and only the
// last one produces anything this tool can read.
//
// Checked by looking at the same files Entire itself uses, rather than by
// running it: a diagnostic that needs the thing it is diagnosing to work is
// not much of a diagnostic.
func Diagnose(dir string) []Check {
	root := repoRoot(dir)
	if root == "" {
		return []Check{{
			Name:  "repository",
			Found: "no .entire directory in this directory or any parent",
			Fix: "Entire is not set up here. It is optional: Claude Code's own " +
				"transcripts need no setup, and `tokenamun profile current` reads them.",
		}}
	}

	var out []Check
	settings := filepath.Join(root, MetadataDir, "settings.json")
	enabled, settingsFound := readEnabled(settings)
	out = append(out, Check{
		Name: "settings", OK: settingsFound && enabled,
		Found: settingsFound_(settingsFound, enabled),
		Fix: fixIf(!settingsFound,
			"run `entire init` in this repository",
			fixIf(!enabled, "set enabled to true in .entire/settings.json", "")),
	})

	hooked, hookFound := entireHooks(root)
	out = append(out, Check{
		Name: "claude code hooks", OK: hooked,
		Found: hookDescription(hooked, hookFound),
		Fix: fixIf(!hooked, "Entire records through Claude Code hooks, and none of "+
			"the ones in .claude/settings.json invoke it. `entire init` installs "+
			"them; a repository cloned from someone who ran it may have the "+
			"settings file without them.", ""),
	})

	logged := exists(filepath.Join(root, MetadataDir, "logs", "entire.log"))
	out = append(out, Check{
		Name: "has run", OK: logged,
		Found: foundIf(logged, "a log in .entire/logs", "no log in .entire/logs"),
		Fix: fixIf(!logged, "Entire has never run in this repository. Start a Claude "+
			"Code session here; the hooks fire on the first tool call.", ""),
	})

	refs := Checkpoints(root)
	out = append(out, Check{
		Name: "checkpoints", OK: refs > 0,
		Found: foundIf(refs > 0, fmt.Sprintf("%d checkpoint refs", refs),
			"no refs under refs/entire/checkpoints"),
		Fix: fixIf(refs == 0, "Checkpoints appear as work is committed. They are git "+
			"refs, so they travel with a clone.", ""),
	})

	// The refs a clone leaves behind. Checked after the local count so the
	// report reads as "you have N, there are M".
	if remote := RemoteCheckpoints(root); remote > refs {
		out = append(out, Check{
			Name: "checkpoints on origin", OK: false,
			Found: fmt.Sprintf("%d on origin against %d here", remote, refs),
			Fix: "Entire's refs are outside the default fetch refspec, so a clone and " +
				"a pull both leave them behind. Until you fetch them you are profiling " +
				"your own sessions and calling it the team's:\n    " + FetchCheckpoints,
		})
	}

	sessions := sessionDirs(root)
	out = append(out, Check{
		Name: "transcripts", OK: sessions > 0,
		Found: foundIf(sessions > 0, fmt.Sprintf("%d session directories", sessions),
			"no .entire/metadata directory"),
		Fix: fixIf(sessions == 0, "This is the one that matters: every token in this "+
			"tool comes from a transcript. Transcripts are not committed, so a clone "+
			"brings the checkpoints and leaves them behind on the machine that "+
			"recorded them.", ""),
	})
	return out
}

// repoRoot finds the repository by its .entire directory, present or empty.
func repoRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for {
		if exists(filepath.Join(abs, MetadataDir)) {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}

// readEnabled reads the enabled flag, which is what Entire itself keys on.
func readEnabled(path string) (enabled, found bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	var s struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return false, true
	}
	// Absent is treated as on, matching how a settings file with only a
	// redaction block behaves.
	return s.Enabled == nil || *s.Enabled, true
}

// entireHooks reports whether any Claude Code hook invokes Entire.
//
// This is the link that was missing on the repository that prompted this:
// a settings file, checkpoints pulled down with the clone, and nothing
// recording, because the hooks that do the recording were not installed.
func entireHooks(root string) (hooked, settingsFound bool) {
	for _, name := range []string{"settings.json", "settings.local.json"} {
		raw, err := os.ReadFile(filepath.Join(root, ".claude", name))
		if err != nil {
			continue
		}
		settingsFound = true
		if strings.Contains(strings.ToLower(string(raw)), "entire") {
			hooked = true
		}
	}
	return hooked, settingsFound
}

// sessionDirs counts recorded sessions.
func sessionDirs(root string) int {
	entries, err := os.ReadDir(filepath.Join(root, MetadataDir, "metadata"))
	if err != nil {
		return 0
	}
	var n int
	for _, e := range entries {
		if e.IsDir() {
			n++
		}
	}
	return n
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func foundIf(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}

func fixIf(broken bool, fix, otherwise string) string {
	if broken {
		return fix
	}
	return otherwise
}

func settingsFound_(found, enabled bool) string {
	switch {
	case !found:
		return "no .entire/settings.json"
	case !enabled:
		return "settings.json says enabled: false"
	default:
		return "settings.json, recording enabled"
	}
}

func hookDescription(hooked, settingsFound bool) string {
	switch {
	case hooked:
		return "a hook in .claude/settings.json invokes Entire"
	case settingsFound:
		return ".claude/settings.json has hooks, none of them Entire's"
	default:
		return "no .claude/settings.json"
	}
}
