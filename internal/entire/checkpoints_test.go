package entire

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ctford/tokenamun/internal/model"
)

// The checkpoint reader is the path that makes a clone useful: .entire/metadata
// is not committed, so on a fresh checkout the refs are the only history there
// is. It shells out to git, which is why it had no tests -- and why these are
// worth the subprocesses.
//
// git's plumbing is the stable half of its interface, so a fixture built from
// hash-object, mktree, commit-tree and update-ref stays valid: no working
// tree, no branch, no commit, and nothing read from the developer's own
// gitconfig.

// requireGit skips rather than failing where git is absent. The tool shells
// out to git in production too, so this is a dependency the package already
// has rather than one the tests introduce.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH, and the checkpoint reader shells out to it")
	}
}

// git runs one plumbing command against the fixture, with the ambient
// configuration neutralised: a developer's commit.gpgsign or init.defaultBranch
// must not decide whether these tests pass.
func git(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(string(out))
}

// cpSession is one session's slice inside a checkpoint.
type cpSession struct {
	id         string
	createdAt  string // RFC 3339
	transcript string
}

// checkpoint is one Entire checkpoint ref.
type checkpoint struct {
	ulid     string
	sessions []cpSession
}

// checkpointRepo builds a repository whose refs have the shape Entire writes:
// refs/entire/checkpoints/<last-2-of-ulid>/<ulid>, each commit's tree holding a
// roll-up metadata.json at the root and one numbered directory per session with
// metadata.json and full.jsonl inside it.
//
// The root roll-up matters to the fixture: it is a metadata.json with no
// full.jsonl beside it, so it is the thing a reader that pairs them carelessly
// would count as an extra session.
func checkpointRepo(t *testing.T, cps ...checkpoint) string {
	t.Helper()
	requireGit(t)
	repo := t.TempDir()
	git(t, repo, "", "init", "-q", ".")

	for _, cp := range cps {
		entries := []string{}
		rollUp := git(t, repo, fmt.Sprintf(`{"checkpoint":%q}`, cp.ulid),
			"hash-object", "-w", "--stdin")
		entries = append(entries, "100644 blob "+rollUp+"\tmetadata.json")

		for i, s := range cp.sessions {
			meta := fmt.Sprintf(`{"session_id":%q,"model":"claude-opus-5","created_at":%q}`,
				s.id, s.createdAt)
			metaBlob := git(t, repo, meta, "hash-object", "-w", "--stdin")
			fullBlob := git(t, repo, s.transcript, "hash-object", "-w", "--stdin")
			sub := git(t, repo,
				"100644 blob "+fullBlob+"\tfull.jsonl\n"+
					"100644 blob "+metaBlob+"\tmetadata.json\n",
				"mktree")
			entries = append(entries, fmt.Sprintf("040000 tree %s\t%d", sub, i))
		}

		tree := git(t, repo, strings.Join(entries, "\n")+"\n", "mktree")
		commit := git(t, repo, "", "commit-tree", tree, "-m", "Checkpoint: "+cp.ulid)
		ref := "refs/entire/checkpoints/" + cp.ulid[len(cp.ulid)-2:] + "/" + cp.ulid
		git(t, repo, "", "update-ref", ref, commit)
	}
	return repo
}

func TestCheckpointsAreReadOutOfGitRefs(t *testing.T) {
	repo := checkpointRepo(t, checkpoint{
		ulid: "01JAAAAAAAAAAAAAAAAAAAAAAB",
		sessions: []cpSession{
			{id: "session-a", createdAt: "2026-09-01T10:00:00Z", transcript: "{\"a\":1}\n"},
			{id: "session-b", createdAt: "2026-09-01T11:00:00Z", transcript: "{\"b\":1}\n{\"b\":2}\n"},
		},
	})

	refs, err := DiscoverCheckpoints(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected the two sessions in the checkpoint, got %d: %+v", len(refs), refs)
	}
	// Most recent first, and the roll-up metadata.json at the tree root is not
	// one of them.
	if refs[0].ID != "session-b" || refs[1].ID != "session-a" {
		t.Errorf("sessions should be newest first, got %q then %q", refs[0].ID, refs[1].ID)
	}
	for _, r := range refs {
		if !r.InGit {
			t.Errorf("%s: a checkpoint transcript is a git object, not a path", r.ID)
		}
		if r.Repo == "" {
			t.Errorf("%s: the loader needs the repository to ask git for the blob", r.ID)
		}
		if !strings.Contains(r.Transcript, ":") {
			t.Errorf("%s: transcript %q is not a <ref>:<path> spec", r.ID, r.Transcript)
		}
	}
}

func TestTheFullestSnapshotOfASessionWinsNotTheLatest(t *testing.T) {
	// full.jsonl is cumulative, and a resumed session gives a later checkpoint
	// a shorter transcript. Taking the most recent would silently report the
	// smaller slice as the whole session.
	long := strings.Repeat("{\"line\":\"content\"}\n", 40)
	short := "{\"line\":\"content\"}\n"

	repo := checkpointRepo(t,
		checkpoint{
			ulid:     "01JBBBBBBBBBBBBBBBBBBBBBBC",
			sessions: []cpSession{{id: "s", createdAt: "2026-09-01T10:00:00Z", transcript: long}},
		},
		checkpoint{
			ulid:     "01JCCCCCCCCCCCCCCCCCCCCCCD",
			sessions: []cpSession{{id: "s", createdAt: "2026-09-02T10:00:00Z", transcript: short}},
		},
	)

	refs, err := DiscoverCheckpoints(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("one session across two checkpoints, got %d: %+v", len(refs), refs)
	}

	body := readBlob(t, refs[0])
	if len(body) != len(long) {
		t.Errorf("got the %d-byte snapshot, want the fullest at %d bytes",
			len(body), len(long))
	}
}

func TestOpenBlobStreamsATranscriptThatIsNotOnDisk(t *testing.T) {
	transcript := "{\"type\":\"assistant\"}\n{\"type\":\"user\"}\n"
	repo := checkpointRepo(t, checkpoint{
		ulid:     "01JDDDDDDDDDDDDDDDDDDDDDDE",
		sessions: []cpSession{{id: "s", createdAt: "2026-09-01T10:00:00Z", transcript: transcript}},
	})

	refs, err := DiscoverCheckpoints(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected one session, got %d", len(refs))
	}
	if got := string(readBlob(t, refs[0])); got != transcript {
		t.Errorf("streamed %q, want %q", got, transcript)
	}
}

// readBlob streams a discovered transcript back out of the repository, which
// is how ingest reads one that lives in a checkpoint rather than on disk.
func readBlob(t *testing.T, ref model.SessionRef) []byte {
	t.Helper()
	rc, err := OpenBlob(ref.Repo, ref.Transcript)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
