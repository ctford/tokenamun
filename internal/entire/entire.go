// Package entire adapts Entire's on-disk layout. It is one of only two
// packages permitted to know a field name from someone else's format.
//
// Format assumptions, read off Entire CLI 0.10.2 by inspecting real data:
//
//   - Sessions live in <repo>/.entire/metadata/<session-uuid>/, each holding
//     full.jsonl (Claude Code's native transcript) and prompt.txt.
//   - Checkpoints are git refs under refs/entire/checkpoints/**, not files.
//   - Per-checkpoint token_usage is NOT safe to sum: in the reference dataset
//     it is a delta in 28 of 41 checkpoints and cumulative from session start
//     in the other 13, with the same cli_version and no field distinguishing
//     them. All token accounting therefore comes from the transcript.
//
// The Entire CLI is not required. Only its data is read.
package entire

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// MetadataDir is the directory Entire writes session data into.
const MetadataDir = ".entire"

// FindRepo walks up from dir looking for a repository with Entire data,
// returning "" when there is none.
func FindRepo(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for {
		if info, err := os.Stat(filepath.Join(abs, MetadataDir, "metadata")); err == nil && info.IsDir() {
			return abs
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return ""
		}
		abs = parent
	}
}

// Discover lists the Entire-recorded sessions in the repository containing dir.
func Discover(dir string) ([]model.SessionRef, error) {
	repo := FindRepo(dir)
	if repo == "" {
		// No transcripts on disk, but a clone still carries them inside its
		// checkpoint commits. Looking only at .entire/metadata reported four
		// days of recorded work as nothing to analyse.
		return DiscoverCheckpoints(dir)
	}
	root := filepath.Join(repo, MetadataDir, "metadata")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var out []model.SessionRef
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(root, e.Name(), "full.jsonl")
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		out = append(out, model.SessionRef{
			ID:         e.Name(),
			Transcript: path,
			Origin:     model.FromEntire,
			Repo:       repo,
			Modified:   info.ModTime(),
		})
	}

	// Checkpoints can hold sessions the metadata directory does not: another
	// machine's, or this machine's before .entire was cleared.
	onDisk := map[string]bool{}
	for _, r := range out {
		onDisk[r.ID] = true
	}
	inGit, err := DiscoverCheckpoints(dir)
	if err != nil {
		return out, nil
	}
	for _, r := range inGit {
		if !onDisk[r.ID] {
			out = append(out, r)
		}
	}
	return out, nil
}

// Configured reports whether Entire is set up in a repository, whether or not
// it has recorded anything.
//
// The two states need telling apart. "Entire is not installed" is a decision
// somebody has to make; "Entire is installed and there is nothing here" is a
// fresh clone, or a repository where nobody has run a session yet, and the
// recordings are deliberately not committed. A reader looking at a .entire
// directory and an empty report deserves to be told which one they have.
// It walks up from dir on its own rather than using FindRepo, which looks
// for .entire/metadata -- the thing that is missing in exactly the case this
// function exists to detect.
func Configured(dir string) bool {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, MetadataDir, "settings.json")); err == nil {
			return true
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return false
		}
		abs = parent
	}
}

// Checkpoints counts the checkpoint refs in a repository.
//
// A third state, and the one the reference repository was in:
// 988 checkpoints and no transcripts. Checkpoints are git refs, so a clone
// brings them; the transcripts are files under .entire/metadata that are not
// committed and stay on the machine that recorded them. Every token in this
// tool comes from a transcript, so checkpoints alone are worth saying out
// loud rather than reporting as "nothing found".
func Checkpoints(dir string) int {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	cmd := exec.Command("git", "-C", abs, "for-each-ref", "--format=%(refname)",
		"refs/entire/checkpoints/**")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(out)))
}
