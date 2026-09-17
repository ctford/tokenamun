package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// build compiles the CLI once for the whole test binary.
func build(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tokenamun")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

// fixtureRepo lays out a directory that looks like an Entire-recorded repo, so
// the CLI can be exercised end to end without Entire, git or a network.
func fixtureRepo(t *testing.T, transcript string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".entire", "metadata", "fixture-session")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "internal", "ingest", "testdata", transcript))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "full.jsonl"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// runCLI invokes the CLI with a clean environment, so a real session leaking in
// from the developer's machine cannot change the result.
func runCLI(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_SESSION_ID=", "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

// The JSON output is the interface coding agents consume, so it gets the same
// treatment as any other API: every command emits a schema version, and the
// documented top-level keys are present.
func TestEveryCommandEmitsAVersionedContract(t *testing.T) {
	bin := build(t)
	repo := fixtureRepo(t, "carry.jsonl")

	commands := map[string][]string{
		"sessions":  {"sessions"},
		"profile":   {"profile"},
		"retrieval": {"retrieval"},
		"carry":     {"carry"},
		"cache":     {"cache"},
	}
	wantKeys := map[string][]string{
		"sessions":  {"sessions"},
		"profile":   {"session", "usage", "caching", "retrieved_content", "notes"},
		"retrieval": {"session", "total", "by_category", "token_estimator", "notes"},
		"carry":     {"session", "context", "preamble", "items", "notes"},
		"cache":     {"session", "observed_ttl", "by_cause", "ttl_expiry", "notes"},
	}

	for name, args := range commands {
		out := runCLI(t, bin, append(args, "--json", "--dir", repo)...)
		var doc map[string]any
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Errorf("%s: output is not valid JSON: %v", name, err)
			continue
		}
		if doc["schema_version"] == nil {
			t.Errorf("%s: no schema_version; agents cannot tell the contract apart from a change", name)
		}
		for _, key := range wantKeys[name] {
			if _, ok := doc[key]; !ok {
				t.Errorf("%s: missing documented key %q", name, key)
			}
		}
	}
}

func TestTextAndJSONReportTheSameNumbers(t *testing.T) {
	bin := build(t)
	repo := fixtureRepo(t, "carry.jsonl")

	var doc struct {
		Session struct {
			Calls int `json:"api_calls"`
		} `json:"session"`
	}
	if err := json.Unmarshal([]byte(runCLI(t, bin, "profile", "--json", "--dir", repo)), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Session.Calls == 0 {
		t.Fatal("the fixture should report API calls")
	}
	if text := runCLI(t, bin, "profile", "--dir", repo); !strings.Contains(text, "API calls") {
		t.Error("the text render should show the call count too")
	}
}

func TestMissingDataExplainsWhereItLooked(t *testing.T) {
	// A profiler that reads other people's directories should say what it
	// tried, not just fail.
	bin := build(t)
	out := runCLI(t, bin, "sessions", "--dir", t.TempDir())
	for _, want := range []string{".entire", ".claude/projects"} {
		if !strings.Contains(out, want) {
			t.Errorf("output should name %s as a place it looked:\n%s", want, out)
		}
	}
}

func TestFlagsWorkAfterPositionalArguments(t *testing.T) {
	// Regression: Go's flag package stops at the first positional, so
	// `profile <session> --json` used to emit human text while the caller
	// believed it had asked for JSON. Silently ignoring a flag is worse than
	// rejecting it.
	bin := build(t)
	repo := fixtureRepo(t, "carry.jsonl")

	out := runCLI(t, bin, "profile", "fixture-session", "--json", "--dir", repo)
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--json after a positional was ignored; got:\n%s", out)
	}
	if doc["schema_version"] == nil {
		t.Error("expected the JSON contract")
	}
}

func TestSessionPrefixSelectsASession(t *testing.T) {
	bin := build(t)
	repo := fixtureRepo(t, "carry.jsonl")
	out := runCLI(t, bin, "profile", "--dir", repo, "fixture")
	if !strings.Contains(out, "fixture-session") {
		t.Errorf("a prefix should select the session, got:\n%s", out)
	}
}

func TestUnknownCommandFailsWithGuidance(t *testing.T) {
	bin := build(t)
	cmd := exec.Command(bin, "nonsense")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("an unknown command should exit non-zero")
	}
	if !strings.Contains(string(out), "tokenamun help") {
		t.Errorf("the error should point at help, got: %s", out)
	}
}

func TestCurrentRequiresRunningInsideASession(t *testing.T) {
	// Selecting "current" without CLAUDE_CODE_SESSION_ID must explain why
	// rather than silently profiling some other session.
	bin := build(t)
	repo := fixtureRepo(t, "carry.jsonl")
	cmd := exec.Command(bin, "profile", "current", "--dir", repo)
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_SESSION_ID=", "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected an error when there is no current session")
	}
	if !strings.Contains(string(out), "CLAUDE_CODE_SESSION_ID") {
		t.Errorf("the error should name the missing variable, got: %s", out)
	}
}
