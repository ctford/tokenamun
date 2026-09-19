package parsecache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ctford/tokenamun/internal/ingest"
	"github.com/ctford/tokenamun/internal/model"
)

// The fixture is hand-written, and the one number a test reads -- output
// tokens -- is two digits in every variant, so that changing what the
// transcript says does not change how long it is. That is the point: it lets
// a test hold size and mtime fixed while the bytes underneath differ, which
// is the only way to tell a cache hit from a fast re-parse.
func transcript(outputTokens string) string {
	return `{"type": "user", "sessionId": "cache-fixture", "timestamp": "2026-09-01T10:00:00.000Z", "message": {"role": "user", "content": "hello"}}
{"type": "assistant", "sessionId": "cache-fixture", "requestId": "r1", "version": "2.1.246", "timestamp": "2026-09-01T10:00:01.000Z", "message": {"id": "m1", "role": "assistant", "model": "claude-opus-5", "content": [{"type": "text", "text": "hello back"}], "usage": {"input_tokens": 1, "cache_creation_input_tokens": 2000, "cache_read_input_tokens": 5000, "output_tokens": ` + outputTokens + `}}}
`
}

// frozen is a fixed modification time, so a test controls invalidation
// instead of racing the filesystem clock.
var frozen = time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)

func writeTranscript(t *testing.T, path, outputTokens string, when time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte(transcript(outputTokens)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) (*Cache, model.SessionRef, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cache-fixture.jsonl")
	writeTranscript(t, path, "60", frozen)
	c := At(filepath.Join(t.TempDir(), "parse"), "build-one")
	if c == nil {
		t.Fatal("At returned no cache")
	}
	return c, model.SessionRef{ID: "cache-fixture", Transcript: path, Origin: model.FromLocal}, path
}

func output(t *testing.T, c *Cache, ref model.SessionRef) int64 {
	t.Helper()
	s, err := c.Load(ref)
	if err != nil {
		t.Fatal(err)
	}
	return s.Usage().Output
}

func TestASecondLoadComesFromTheCache(t *testing.T) {
	c, ref, path := fixture(t)
	if got := output(t, c, ref); got != 60 {
		t.Fatalf("first load read %d output tokens, want 60", got)
	}
	// Same length, same mtime, different bytes. Only a cache hit can still
	// report 60.
	writeTranscript(t, path, "99", frozen)
	if got := output(t, c, ref); got != 60 {
		t.Errorf("second load read %d, want the cached 60", got)
	}
}

func TestANilCacheAlwaysParses(t *testing.T) {
	_, ref, path := fixture(t)
	var c *Cache
	if got := output(t, c, ref); got != 60 {
		t.Fatalf("read %d, want 60", got)
	}
	writeTranscript(t, path, "99", frozen)
	if got := output(t, c, ref); got != 99 {
		t.Errorf("read %d, want the transcript's 99", got)
	}
}

// Invalidation is tested harder than the hit path, because a miss costs a
// re-parse and a bad hit costs a wrong number that nothing downstream can
// notice.
func TestTheCacheIsInvalidatedBy(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, path string)
	}{
		{"more bytes", func(t *testing.T, path string) {
			writeTranscript(t, path, "99", frozen)
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteString("\n"); err != nil {
				t.Fatal(err)
			}
			_ = f.Close()
			if err := os.Chtimes(path, frozen, frozen); err != nil {
				t.Fatal(err)
			}
		}},
		{"a newer modification time", func(t *testing.T, path string) {
			writeTranscript(t, path, "99", frozen.Add(time.Second))
		}},
		{"an older modification time", func(t *testing.T, path string) {
			writeTranscript(t, path, "99", frozen.Add(-time.Hour))
		}},
		{"a subagent transcript appearing beside it", func(t *testing.T, path string) {
			writeTranscript(t, path, "99", frozen)
			sub := filepath.Join(filepath.Dir(path), "cache-fixture", "subagents")
			if err := os.MkdirAll(sub, 0o750); err != nil {
				t.Fatal(err)
			}
			writeTranscript(t, filepath.Join(sub, "agent-1.jsonl"), "11", frozen)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, ref, path := fixture(t)
			if got := output(t, c, ref); got != 60 {
				t.Fatalf("first load read %d, want 60", got)
			}
			tc.change(t, path)
			if got := output(t, c, ref); got != 99 {
				t.Errorf("read the stale 60 after %s; want a fresh 99", tc.name)
			}
		})
	}
}

// The transcript being appended to right now is the one where a stale parse
// is most likely, so it is never kept at all.
func TestTheCurrentSessionIsNeverCached(t *testing.T) {
	c, ref, path := fixture(t)
	ref.Current = true
	if got := output(t, c, ref); got != 60 {
		t.Fatalf("read %d, want 60", got)
	}
	writeTranscript(t, path, "99", frozen)
	if got := output(t, c, ref); got != 99 {
		t.Errorf("read the cached %d; the current session must not be cached", got)
	}
}

func TestAVanishedTranscriptHasNoKey(t *testing.T) {
	if k := Key(model.SessionRef{Transcript: filepath.Join(t.TempDir(), "gone.jsonl")}); k != "" {
		t.Errorf("keyed a transcript that cannot be stat'd: %q", k)
	}
	if k := Key(model.SessionRef{}); k != "" {
		t.Errorf("keyed a ref with no transcript: %q", k)
	}
}

// An Entire recording is named by a checkpoint ref, which is content
// addressed. Nothing on disk has to be consulted, and the key cannot go stale
// because the bytes it names cannot change.
func TestAnEntireCheckpointIsKeyedOnItsImmutableRef(t *testing.T) {
	ref := model.SessionRef{
		ID: "s1", InGit: true, Repo: "/repo",
		Transcript: "0000000000000000000000000000000000000000:.entire/x/full.jsonl",
	}
	key := Key(ref)
	if key == "" {
		t.Fatal("a checkpoint ref should be keyable")
	}
	if Key(ref) != key {
		t.Error("the same checkpoint ref keyed two different ways")
	}
	other := ref
	other.Transcript = "1111111111111111111111111111111111111111:.entire/x/full.jsonl"
	if Key(other) == key {
		t.Error("two checkpoints shared a key")
	}
	bare := ref
	bare.Repo = ""
	if Key(bare) != "" {
		t.Error("keyed a checkpoint with no repository to read it from")
	}
}

func TestAnotherBuildsEntriesAreNeitherReadNorKept(t *testing.T) {
	root := filepath.Join(t.TempDir(), "parse")
	dir := t.TempDir()
	path := filepath.Join(dir, "cache-fixture.jsonl")
	writeTranscript(t, path, "60", frozen)
	ref := model.SessionRef{ID: "cache-fixture", Transcript: path, Origin: model.FromLocal}

	old := At(root, "build-one")
	if got := output(t, old, ref); got != 60 {
		t.Fatalf("read %d, want 60", got)
	}
	writeTranscript(t, path, "99", frozen)

	fresh := At(root, "build-two")
	if got := output(t, fresh, ref); got != 99 {
		t.Errorf("a new build read %d from the old build's entry; want a fresh 99", got)
	}
	if _, err := os.Stat(filepath.Join(root, "build-one")); !os.IsNotExist(err) {
		t.Error("the previous build's entries were left to accumulate")
	}
}

func TestAnUnreadableEntryIsAMissRatherThanAnError(t *testing.T) {
	c, ref, _ := fixture(t)
	if got := output(t, c, ref); got != 60 {
		t.Fatalf("read %d, want 60", got)
	}
	if err := os.WriteFile(c.path(Key(ref)), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := output(t, c, ref); got != 60 {
		t.Errorf("read %d after corrupting the entry; want a re-parsed 60", got)
	}

	// An entry whose stored key disagrees with the one asked for is not this
	// session's parse, whatever the filename says.
	raw, err := json.Marshal(entry{Key: "somebody else's key", Session: &model.Session{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.path(Key(ref)), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := output(t, c, ref); got != 60 {
		t.Errorf("read %d from an entry keyed for something else", got)
	}
}

// The cache is only worth having if what comes out of it is what ingest would
// have produced. Compared as JSON, which is the form every report is built
// from and the form the entry is stored in: a field that does not survive the
// round trip is a field a report would lose.
func TestACachedParseIsTheParse(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"repeated-request.jsonl", "retrieval.jsonl", "carry.jsonl"} {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("..", "ingest", "testdata", name))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, src, 0o600); err != nil {
				t.Fatal(err)
			}
			ref := model.SessionRef{ID: name, Transcript: path, Origin: model.FromLocal}
			fresh, err := ingest.Load(ref)
			if err != nil {
				t.Fatal(err)
			}
			c := At(filepath.Join(t.TempDir(), "parse"), "build-one")
			if _, err := c.Load(ref); err != nil { // populate
				t.Fatal(err)
			}
			cached, err := c.Load(ref)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(fresh)
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(cached)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Error("a cached parse differs from a fresh one")
			}
		})
	}
}

func TestACacheWithNowhereToLiveIsSimplyAbsent(t *testing.T) {
	if c := At("", "build-one"); c != nil {
		t.Error("built a cache with no directory")
	}
	if c := At(t.TempDir(), ""); c != nil {
		t.Error("built a cache with no fingerprint; an unidentified build must not reuse entries")
	}
	// A path that cannot be a directory, because a file is already there.
	file := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if c := At(file, "build-one"); c != nil {
		t.Error("built a cache where no directory could be made")
	}
}

func TestFingerprintNamesThisBuild(t *testing.T) {
	fp := Fingerprint()
	if fp == "" {
		t.Skip("no executable to fingerprint here")
	}
	if fp != Fingerprint() {
		t.Error("the same build fingerprinted two different ways")
	}
}

func TestOpenUsesTheUsersCacheDirectory(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	c := Open()
	if c == nil {
		t.Skip("no user cache directory or no executable here")
	}
	if _, err := os.Stat(c.dir); err != nil {
		t.Errorf("the cache directory was not created: %v", err)
	}
}
