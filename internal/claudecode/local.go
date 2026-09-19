package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// ProjectsDir is where Claude Code keeps its own transcripts. Assumption,
// verified against Claude Code 2.1.x: one directory per project, named by
// slugifying the absolute working directory, containing one JSONL file per
// session named by session ID.
func ProjectsDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".claude", "projects")
	}
	return ""
}

// slug converts an absolute path to Claude Code's project directory name by
// replacing separators with hyphens, so /a/b becomes -a-b.
func slug(dir string) string {
	return strings.ReplaceAll(dir, string(filepath.Separator), "-")
}

// CurrentSessionID returns the session Tokenamun is running inside, if any.
// Claude Code exports it, which lets the tool profile the session that is
// invoking it.
func CurrentSessionID() string { return os.Getenv("CLAUDE_CODE_SESSION_ID") }

// DiscoverLocal lists Claude Code sessions recorded for dir.
//
// The slugified directory name is tried first. If that misses -- Claude Code's
// slug rules could change, and the caller may pass a path that differs from
// the recorded one -- every project directory is scanned and matched on the
// cwd each transcript records, which is observed rather than guessed.
func DiscoverLocal(dir string) ([]model.SessionRef, error) {
	root := ProjectsDir()
	if root == "" {
		return nil, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	if refs, err := sessionsIn(filepath.Join(root, slug(abs)), abs); err == nil && len(refs) > 0 {
		return refs, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil
	}
	var out []model.SessionRef
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		refs, err := sessionsIn(filepath.Join(root, e.Name()), abs)
		if err != nil {
			continue
		}
		out = append(out, refs...)
	}
	return out, nil
}

// sessionsIn lists the transcripts in one project directory whose recorded cwd
// matches want. An empty want accepts everything.
func sessionsIn(projectDir, want string) ([]model.SessionRef, error) {
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return nil, err
	}
	current := CurrentSessionID()
	var out []model.SessionRef
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(projectDir, e.Name())
		if want != "" {
			cwd, err := transcriptCWD(path)
			if err == nil && cwd != "" && cwd != want {
				continue
			}
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		ref := model.SessionRef{
			ID:         id,
			Transcript: path,
			Origin:     model.FromLocal,
			Repo:       want,
			Current:    id == current,
		}
		if info, err := e.Info(); err == nil {
			ref.Modified = info.ModTime()
		}
		out = append(out, ref)
	}
	return out, nil
}

// transcriptCWD reads the working directory a transcript records, looking only
// at its first lines so that matching does not cost a full parse.
func transcriptCWD(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }() // read-only: nothing to flush, nothing to lose

	dec := json.NewDecoder(f)
	for i := 0; i < 50; i++ {
		var e Entry
		if err := dec.Decode(&e); err != nil {
			return "", err
		}
		if e.CWD != "" {
			return e.CWD, nil
		}
	}
	return "", nil
}
