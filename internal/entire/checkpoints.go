package entire

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/ctford/tokenamun/internal/model"
)

// DiscoverCheckpoints finds transcripts stored inside Entire's checkpoint
// commits, as against the ones on disk under .entire/metadata.
//
// This is where a cloned repository's history actually is, and missing it was
// a real gap: a clone of a repository with four days of agentic work in it
// reported nothing to analyse, because .entire/metadata is not committed and
// that was the only place we looked. The checkpoints are git refs, so they
// travel -- and each one carries a full.jsonl in its tree.
//
// full.jsonl is cumulative: on one reference repository a session had
// checkpoints holding 4,609 lines and 11,116 lines of the same transcript. So
// the fullest snapshot of a session is the largest full.jsonl across its
// checkpoints, and that is what this returns. Taking the most recent by date
// would be wrong -- a later checkpoint can cover a shorter transcript when a
// session was resumed.
func DiscoverCheckpoints(dir string) ([]model.SessionRef, error) {
	repo := gitRoot(dir)
	if repo == "" {
		return nil, nil
	}
	refs, err := checkpointRefs(repo)
	if err != nil || len(refs) == 0 {
		return nil, err
	}

	// Every blob in every checkpoint tree, with its size, in one git call.
	// Per-ref calls would be a thousand processes to answer one question.
	blobs, err := treeBlobs(repo, refs)
	if err != nil {
		return nil, err
	}

	// Session identity is in each checkpoint's metadata.json, so read those
	// in a single batch too.
	metaSpecs := make([]string, 0, len(blobs))
	for _, b := range blobs {
		if b.name == "metadata.json" {
			metaSpecs = append(metaSpecs, b.spec)
		}
	}
	metas, err := batchRead(repo, metaSpecs)
	if err != nil {
		return nil, err
	}

	return fullestPerSession(repo, blobs, metas), nil
}

// fullestPerSession picks one transcript per session and shapes the refs.
//
// Everything above this is git; everything in it is the decision, which is
// why it is a function of its inputs and nothing else -- it is the part with
// a rule in it rather than a subprocess.
//
// The rule: full.jsonl is cumulative, so a session's fullest snapshot is its
// largest across every checkpoint. Most recent would be wrong, because a
// resumed session gives a later checkpoint a shorter transcript, and taking
// it would report the smaller slice as the whole session.
//
// A full.jsonl with no readable metadata.json beside it is skipped rather
// than guessed at: the session id is the only thing that makes two snapshots
// the same session, and without it there is nothing to compare.
func fullestPerSession(repo string, blobs []blobRef, metas map[string][]byte) []model.SessionRef {
	type best struct {
		spec string
		size int64
		when time.Time
	}
	bySession := map[string]best{}
	for _, b := range blobs {
		if b.name != "full.jsonl" {
			continue
		}
		meta, ok := metas[b.dirSpec+"metadata.json"]
		if !ok {
			continue
		}
		var m checkpointMeta
		if err := json.Unmarshal(meta, &m); err != nil || m.SessionID == "" {
			continue
		}
		if cur, seen := bySession[m.SessionID]; seen && cur.size >= b.size {
			continue
		}
		bySession[m.SessionID] = best{spec: b.spec, size: b.size, when: m.CreatedAt}
	}

	out := make([]model.SessionRef, 0, len(bySession))
	for id, b := range bySession {
		out = append(out, model.SessionRef{
			ID:         id,
			Transcript: b.spec,
			Origin:     model.FromEntire,
			Repo:       repo,
			Modified:   b.when,
			// A git object rather than a file, so the loader knows to ask git
			// for it instead of opening a path that does not exist.
			InGit: true,
		})
	}
	// Newest first, and stable, so two checkpoints sharing a timestamp keep
	// the order git listed them in rather than a random one.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out
}

// checkpointMeta is the part of a checkpoint's metadata.json we need to
// identify the session it belongs to.
//
// Deliberately not its token_usage: that field is a delta in some checkpoints
// and cumulative in others, with nothing distinguishing them, so it is not
// safe to sum. All token accounting comes from the transcript. See the
// package comment.
type checkpointMeta struct {
	SessionID string    `json:"session_id"`
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
}

// blobRef is one file inside one checkpoint tree.
type blobRef struct {
	// spec is what git cat-file takes: "<ref>:<path>".
	spec string
	// dirSpec is the spec of the containing directory, so a full.jsonl can
	// find the metadata.json beside it.
	dirSpec string
	name    string
	size    int64
}

func checkpointRefs(repo string) ([]string, error) {
	out, err := gitOutput(repo, "for-each-ref", "--format=%(refname)",
		"refs/entire/checkpoints/**")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// treeBlobs lists every blob in every checkpoint tree, with sizes.
func treeBlobs(repo string, refs []string) ([]blobRef, error) {
	// `ls-tree` over several refs does not say which ref a line came from, so
	// they are listed one at a time. Still one process per ref for this step,
	// which is the price of knowing the ref -- and it only runs when a
	// repository actually has checkpoints.
	//
	// A batched call over every ref used to run first and have its output
	// thrown away by this loop: a git process per scan doing nothing, and an
	// error path that could fail the whole read where the loop below is
	// deliberately tolerant of one bad ref.
	var blobs []blobRef
	for _, ref := range refs {
		out, err := gitOutput(repo, "ls-tree", "-r", "--long", ref)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(out, "\n") {
			b, ok := parseTreeLine(ref, line)
			if ok {
				blobs = append(blobs, b)
			}
		}
	}
	return blobs, nil
}

// parseTreeLine reads one `git ls-tree -r --long` line:
//
//	100644 blob <sha> <size>\t<path>
func parseTreeLine(ref, line string) (blobRef, bool) {
	tab := strings.IndexByte(line, '\t')
	if tab < 0 {
		return blobRef{}, false
	}
	fields := strings.Fields(line[:tab])
	if len(fields) < 4 || fields[1] != "blob" {
		return blobRef{}, false
	}
	var size int64
	if _, err := fmt.Sscanf(fields[3], "%d", &size); err != nil {
		return blobRef{}, false
	}
	path := line[tab+1:]
	name := path
	dir := ""
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		name, dir = path[i+1:], path[:i+1]
	}
	return blobRef{
		spec: ref + ":" + path, dirSpec: ref + ":" + dir, name: name, size: size,
	}, true
}

// batchRead reads many blobs in one git process.
func batchRead(repo string, specs []string) (map[string][]byte, error) {
	out := map[string][]byte{}
	if len(specs) == 0 {
		return out, nil
	}
	cmd := exec.Command("git", "-C", repo, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(specs, "\n") + "\n")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() { _ = cmd.Wait() }()

	r := bufio.NewReader(stdout)
	for _, spec := range specs {
		header, err := r.ReadString('\n')
		if err != nil {
			break
		}
		fields := strings.Fields(header)
		if len(fields) < 3 {
			continue // "missing", and the next header follows
		}
		var size int
		if _, err := fmt.Sscanf(fields[2], "%d", &size); err != nil {
			break
		}
		body := make([]byte, size+1) // git writes a trailing newline
		if _, err := io.ReadFull(r, body); err != nil {
			break
		}
		out[spec] = body[:size]
	}
	return out, nil
}

// OpenBlob streams a git object, for a transcript that lives in a checkpoint
// rather than on disk.
//
// Streamed rather than extracted to a temp file: transcripts reach 9 MB, and
// nothing else in this tool loads a whole one.
func OpenBlob(repo, spec string) (io.ReadCloser, error) {
	cmd := exec.Command("git", "-C", repo, "cat-file", "-p", spec)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &blobReader{ReadCloser: stdout, cmd: cmd}, nil
}

type blobReader struct {
	io.ReadCloser
	cmd *exec.Cmd
}

func (b *blobReader) Close() error {
	err := b.ReadCloser.Close()
	_ = b.cmd.Wait()
	return err
}

func gitRoot(dir string) string {
	out, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.Output()
	return string(out), err
}

// RemoteCheckpoints counts the checkpoint refs the remote has, so a report
// can say when a repository is holding somebody else's history back.
//
// This is the failure that cost the most time: a repository had a small
// fraction of its checkpoints locally and the rest on the remote, so every
// figure measured from it was one person's share of a whole team's week --
// and nothing said so, because a low checkpoint count is not an error.
// Entire's refs are outside the default fetch refspec, so a clone and a pull
// both leave them behind.
//
// Network, so it is best-effort and silent on failure: a profiler that hangs
// or errors because a remote is unreachable is worse than one that omits a
// hint.
func RemoteCheckpoints(dir string) int {
	out, err := gitOutput(dir, "ls-remote", "--refs", "origin", "refs/entire/*")
	if err != nil {
		return 0
	}
	var n int
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "refs/entire/checkpoints/") {
			n++
		}
	}
	return n
}

// FetchCheckpoints is the command that brings them down.
const FetchCheckpoints = "git fetch origin 'refs/entire/*:refs/entire/*'"
