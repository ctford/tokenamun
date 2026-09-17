package entire

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfiguredTellsSetUpFromRecorded(t *testing.T) {
	// Three states, and the middle one is the surprising one: a .entire
	// directory, a settings file, and still nothing to read. That is a fresh
	// clone, or a repository where nobody has run a session with it enabled,
	// and the recordings are deliberately not committed. A reader who sees
	// the directory and an empty report deserves to be told which they have.
	t.Run("not set up", func(t *testing.T) {
		if Configured(t.TempDir()) {
			t.Error("an empty directory is not configured")
		}
	})

	t.Run("set up but empty", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, MetadataDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, MetadataDir, "settings.json"),
			[]byte(`{"enabled":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if !Configured(dir) {
			t.Error("settings.json means Entire is set up, recordings or not")
		}
		// FindRepo looks for the recordings, which is the thing that is
		// missing here. The two must not be conflated.
		if FindRepo(dir) != "" {
			t.Error("there are no recordings to find")
		}
	})

	t.Run("found from a subdirectory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, MetadataDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, MetadataDir, "settings.json"),
			[]byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		deep := filepath.Join(dir, "apps", "web", "src")
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		if !Configured(deep) {
			t.Error("a subdirectory of a configured repository is configured")
		}
	})
}

func TestDiagnoseFindsTheBrokenLink(t *testing.T) {
	// "No sessions found" was the answer to four different problems, and the
	// difference between them is the whole of what a reader needs. Each case
	// here is a state a real repository was in.
	setup := func(t *testing.T, do func(root string)) []Check {
		t.Helper()
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, MetadataDir), 0o755); err != nil {
			t.Fatal(err)
		}
		do(root)
		return Diagnose(root)
	}
	find := func(checks []Check, name string) Check {
		t.Helper()
		for _, c := range checks {
			if c.Name == name {
				return c
			}
		}
		t.Fatalf("no check called %q", name)
		return Check{}
	}

	t.Run("not set up at all", func(t *testing.T) {
		checks := Diagnose(t.TempDir())
		if len(checks) != 1 || checks[0].Name != "repository" {
			t.Fatalf("expected a single repository check, got %+v", checks)
		}
		// And it must say Entire is optional rather than implying the tool
		// cannot work without it.
		if !strings.Contains(checks[0].Fix, "no setup") {
			t.Errorf("it should point at the source that needs no setup: %q", checks[0].Fix)
		}
	})

	t.Run("settings present, recording off", func(t *testing.T) {
		checks := setup(t, func(root string) {
			write(t, filepath.Join(root, MetadataDir, "settings.json"), `{"enabled":false}`)
		})
		c := find(checks, "settings")
		if c.OK {
			t.Error("enabled: false is not set up to record")
		}
		if !strings.Contains(c.Found, "false") {
			t.Errorf("it should say what it found: %q", c.Found)
		}
	})

	t.Run("settings present, hooks missing", func(t *testing.T) {
		// The state that prompted this: a settings file, and nothing
		// recording, because the hooks that do the recording were absent.
		checks := setup(t, func(root string) {
			write(t, filepath.Join(root, MetadataDir, "settings.json"), `{"enabled":true}`)
			if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(root, ".claude", "settings.json"),
				`{"hooks":{"PostToolUse":[{"hooks":[{"command":"something-else"}]}]}}`)
		})
		if find(checks, "settings").OK != true {
			t.Error("the settings themselves are fine")
		}
		hooks := find(checks, "claude code hooks")
		if hooks.OK {
			t.Error("hooks that do not invoke Entire will never record")
		}
		if !strings.Contains(hooks.Fix, "entire init") {
			t.Errorf("the fix should name the command: %q", hooks.Fix)
		}
	})

	t.Run("recording, with transcripts", func(t *testing.T) {
		checks := setup(t, func(root string) {
			write(t, filepath.Join(root, MetadataDir, "settings.json"), `{"enabled":true}`)
			if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(root, ".claude", "settings.json"),
				`{"hooks":{"Stop":[{"hooks":[{"command":"entire hook"}]}]}}`)
			if err := os.MkdirAll(filepath.Join(root, MetadataDir, "logs"), 0o755); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(root, MetadataDir, "logs", "entire.log"), "{}")
			dir := filepath.Join(root, MetadataDir, "metadata", "a-session")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(dir, "full.jsonl"), "{}")
		})
		for _, name := range []string{"settings", "claude code hooks", "has run", "transcripts"} {
			if c := find(checks, name); !c.OK {
				t.Errorf("%s should pass on a working repository: %q", name, c.Found)
			}
		}
		// Checkpoints need a git repository, so that one is allowed to fail
		// here; the transcripts are what this tool actually reads.
	})
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointTreeLinesAreParsed(t *testing.T) {
	// The shape of `git ls-tree -r --long`, which is how a checkpoint's
	// contents and their sizes are read in one call.
	b, ok := parseTreeLine("refs/entire/checkpoints/AB/01X",
		"100644 blob ffc83d4763f68ce89f788ab345c9f6f260d5cba3   548676\t0/full.jsonl")
	if !ok {
		t.Fatal("a blob line should parse")
	}
	if b.spec != "refs/entire/checkpoints/AB/01X:0/full.jsonl" {
		t.Errorf("spec = %q", b.spec)
	}
	// The directory spec is how a full.jsonl finds the metadata.json beside
	// it, which is the only place the session id lives.
	if b.dirSpec != "refs/entire/checkpoints/AB/01X:0/" {
		t.Errorf("dirSpec = %q", b.dirSpec)
	}
	if b.name != "full.jsonl" || b.size != 548676 {
		t.Errorf("name = %q, size = %d", b.name, b.size)
	}

	if _, ok := parseTreeLine("ref", "040000 tree 0a1b2c3d       -\t0"); ok {
		t.Error("a tree is not a blob")
	}
	if _, ok := parseTreeLine("ref", "nonsense"); ok {
		t.Error("a line with no tab is not a blob")
	}
}

func TestCheckpointDiscoveryIsQuietOutsideAGitRepository(t *testing.T) {
	// A directory with no git repository above it must not be an error: the
	// other source still works, and Entire is optional.
	refs, err := DiscoverCheckpoints(t.TempDir())
	if err != nil {
		t.Errorf("expected silence, got %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("expected no sessions, got %d", len(refs))
	}
}
