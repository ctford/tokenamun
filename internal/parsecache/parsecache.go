// Package parsecache remembers parsed sessions between invocations.
//
// Parsing is where the time goes. A week of transcripts is tens of megabytes
// of JSON, and re-reading all of it to answer a second question about data
// that has not changed is what stops an exploratory command being explored
// with. The parse is deterministic over its inputs, so the result can be kept
// and the second question answered at once.
//
// A stale entry here is worse than no cache at all. This tool exists to keep
// confidently wrong numbers away from people, and serving yesterday's parse
// for a transcript that has since grown produces exactly one -- silently, and
// with every provenance label still saying `observed`. So a miss is the
// default on every uncertainty: an entry that will not decode, a transcript
// that will not stat, a session still being written to, a cache directory
// that will not open, a build of this tool that is not the one that wrote the
// entry. Nothing in here ever fails a command; the worst it does is decline
// to help.
package parsecache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ctford/tokenamun/internal/ingest"
	"github.com/ctford/tokenamun/internal/model"
)

// Cache is a directory of parsed sessions. A nil *Cache is a working cache
// that never hits, which is what every failure to set one up returns: the
// caller has one code path, and it is the one that parses.
type Cache struct {
	dir string
}

// Open returns a cache under the user's cache directory.
//
// Nil when there is nowhere to put one, or when this build of the tool cannot
// identify itself -- see Fingerprint.
func Open() *Cache {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil
	}
	return At(filepath.Join(base, "tokenamun", "parse"), Fingerprint())
}

// At returns a cache rooted at dir, holding entries written by fingerprint.
//
// The fingerprint is a directory level rather than part of each entry's name,
// so that entries from a build that is no longer installed can be recognised
// and dropped instead of accumulating for ever.
func At(dir, fingerprint string) *Cache {
	if dir == "" || fingerprint == "" {
		return nil
	}
	mine := filepath.Join(dir, fingerprint)
	if err := os.MkdirAll(mine, 0o750); err != nil {
		return nil
	}
	sweep(dir, fingerprint)
	return &Cache{dir: mine}
}

// Fingerprint identifies the build of the tool that is doing the parsing.
//
// Everything downstream of ingest is arithmetic over the parse, so a change
// to what a transcript means has to empty this cache. A format number
// declared in the source would do it, and the failure mode of forgetting to
// bump one is a wrong number rather than a slow command -- which is the wrong
// way round. The executable is the thing that decides what a parse means, so
// its own identity is used: path, size and modification time. Rebuilding the
// tool invalidates every entry, and `go run`, which links a fresh binary each
// time, never hits the cache at all. Both err towards parsing again.
//
// Empty when the executable cannot be located, which disables the cache.
func Fingerprint() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	fi, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return sum("exe", exe, itoa64(fi.Size()), itoa64(fi.ModTime().UnixNano()))
}

// Load parses a session, using a cached parse when one certainly applies.
//
// The signature is ingest.Load's, so a caller that does not want a cache
// passes nil and nothing else changes.
func (c *Cache) Load(ref model.SessionRef) (*model.Session, error) {
	key := Key(ref)
	if s := c.get(key); s != nil {
		return s, nil
	}
	s, err := ingest.Load(ref)
	if err != nil {
		return nil, err
	}
	c.put(key, s)
	return s, nil
}

// Key identifies every input the parse of a ref depends on, or "" when they
// cannot all be pinned down -- in which case there is nothing safe to keep.
//
// Three cases.
//
// The session the tool is running inside is being appended to while it is
// read, so its parse is out of date before it is written. It is never cached.
//
// An Entire recording is named by a checkpoint ref: a git object spec, which
// is content-addressed and so names bytes that cannot change under it. That
// is strictly better evidence than an mtime, and it needs no stat.
//
// A local transcript is keyed on size and modification time, and not only its
// own: a session's parse also reads the subagent transcripts beside it, and a
// subagent that finishes after its parent's last write would otherwise be
// invisible to the key. ingest.Sources lists them, so the two cannot drift.
func Key(ref model.SessionRef) string {
	if ref.Current {
		return ""
	}
	if ref.InGit {
		if ref.Repo == "" || ref.Transcript == "" {
			return ""
		}
		return sum("git", ref.Repo, ref.Transcript)
	}
	sources := ingest.Sources(ref)
	if len(sources) == 0 {
		return ""
	}
	parts := []string{"files"}
	for _, p := range sources {
		fi, err := os.Stat(p)
		if err != nil {
			return ""
		}
		parts = append(parts, p, itoa64(fi.Size()), itoa64(fi.ModTime().UnixNano()))
	}
	return sum(parts...)
}

// entry is a cached parse with the key it was written under.
//
// The key is stored as well as hashed into the filename so that a hit is
// confirmed against what was asked for rather than assumed from a filename.
// A truncated or half-written file decodes to nothing and is a miss.
type entry struct {
	Key     string         `json:"key"`
	Session *model.Session `json:"session"`
}

func (c *Cache) get(key string) *model.Session {
	if c == nil || key == "" {
		return nil
	}
	f, err := os.Open(c.path(key))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }() // read-only: nothing to flush
	// Decoded from the stream rather than read whole: a busy session's parse
	// is megabytes of JSON, and the rule against slurping a transcript is the
	// same rule here.
	var e entry
	if err := json.NewDecoder(f).Decode(&e); err != nil {
		return nil
	}
	if e.Key != key || e.Session == nil {
		return nil
	}
	return e.Session
}

// put writes an entry, and says nothing when it cannot. A cache that fails to
// write is a slow tool; a cache that fails loudly is a broken one.
func (c *Cache) put(key string, s *model.Session) {
	if c == nil || key == "" || s == nil {
		return
	}
	// Written to a temporary file and renamed, so a reader never sees half of
	// one. Two invocations racing on the same session write the same bytes,
	// and the loser's rename replaces an identical file.
	tmp, err := os.CreateTemp(c.dir, "parse-*.tmp")
	if err != nil {
		return
	}
	err = json.NewEncoder(tmp).Encode(entry{Key: key, Session: s})
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), c.path(key)); err != nil {
		_ = os.Remove(tmp.Name())
	}
}

func (c *Cache) path(key string) string {
	return filepath.Join(c.dir, key+".json")
}

// sweep drops the entries of every other build.
//
// Without it the cache grows by a session's parse for every build of the tool
// that ever ran, none of which will be read again. Best-effort: a directory
// that will not list or will not delete is left alone.
func sweep(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() != keep {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// sum hashes the parts of a key with a separator that cannot appear in them,
// so that two different keys cannot run together into one string.
func sum(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:])
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
