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
	"path/filepath"

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
		return nil, nil
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
	return out, nil
}
