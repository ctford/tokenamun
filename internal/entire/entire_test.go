package entire

import (
	"os"
	"path/filepath"
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
