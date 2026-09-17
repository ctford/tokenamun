package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// capture calls run() with stdout redirected, so the dispatch layer is
// exercised in process.
//
// main_test.go drives the built binary instead, which is the honest test of
// what a user gets. Both are worth having: that one would catch a broken
// build, and this one covers the argument handling, where the failure mode is
// a flag silently doing nothing rather than a crash.
func capture(t *testing.T, args ...string) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	runErr := run(args)
	os.Stdout = saved
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out, runErr
}

// localFixture lays out a ~/.claude/projects tree for a session, the way
// Claude Code records one, and points HOME at it.
func localFixture(t *testing.T, transcript string) string {
	t.Helper()
	home := t.TempDir()
	// The repository is the one the fixture transcript records as its cwd.
	// Discovery matches on that, so a fixture placed under some other path is
	// correctly refused, which is the behaviour we want and not what we are
	// testing here.
	repo := "/repo"
	slugged := strings.ReplaceAll(repo, string(filepath.Separator), "-")
	dir := filepath.Join(home, ".claude", "projects", slugged)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "internal", "ingest", "testdata", transcript))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture-session.jsonl"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	// An intervention script left in a developer's config directory must not
	// change what the tests measure.
	t.Setenv("TOKENAMUN_INTERVENTIONS", t.TempDir())
	return repo
}

func TestNoArgumentsPrintsUsageRatherThanFailing(t *testing.T) {
	out, err := capture(t)
	if err != nil {
		t.Fatalf("bare invocation should not be an error: %v", err)
	}
	if !strings.Contains(out, "tokenamun sessions") {
		t.Errorf("usage should list the commands, got %q", out)
	}
}

func TestUnknownCommandSaysWhatToTry(t *testing.T) {
	_, err := capture(t, "wat")
	if err == nil {
		t.Fatal("an unknown command must be an error")
	}
	if !strings.Contains(err.Error(), "tokenamun help") {
		t.Errorf("the error should point somewhere useful: %v", err)
	}
}

func TestFlagsMayFollowThePositionalArgument(t *testing.T) {
	// Go's flag package stops at the first positional, which would make
	// `profile latest --json` print the human report and silently ignore the
	// flag. For a CLI an agent drives, a dropped flag is the worst failure
	// mode there is, so this is asserted rather than assumed.
	repo := localFixture(t, "carry.jsonl")
	out, err := capture(t, "profile", "--dir", repo, "fixture", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("--json after the selector was ignored: %v\n%s", err, out)
	}
	if _, ok := doc["schema_version"]; !ok {
		t.Error("the JSON report should declare its schema version")
	}
}

func TestSessionSelectorResolution(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")

	if _, err := capture(t, "profile", "--dir", repo, "nosuchsession"); err == nil {
		t.Error("an unmatched selector must be an error, not the latest session")
	}
	// "current" means the session invoking the tool, which requires the
	// environment variable Claude Code exports.
	if _, err := capture(t, "profile", "--dir", repo, "current"); err == nil {
		t.Error("current must fail when not running inside Claude Code")
	}
	if _, err := capture(t, "sessions", "--dir", repo, "--json"); err != nil {
		t.Errorf("sessions should list what it found: %v", err)
	}
}

func TestEveryReportRunsOverAFixtureSession(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	for _, cmd := range []string{"profile", "retrieval", "carry", "cache"} {
		t.Run(cmd, func(t *testing.T) {
			for _, form := range [][]string{{cmd, "--dir", repo}, {cmd, "--dir", repo, "--json"}} {
				out, err := capture(t, form...)
				if err != nil {
					t.Fatalf("%v: %v", form, err)
				}
				if strings.TrimSpace(out) == "" {
					t.Errorf("%v produced nothing", form)
				}
			}
		})
	}
}

func TestTreemapWritesAStandaloneFileWithTheGivenTitle(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	path := filepath.Join(t.TempDir(), "out.html")
	if _, err := capture(t, "treemap", "--dir", repo, "-o", path, "--title", "A Given Title"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	if !strings.Contains(html, "A Given Title") {
		t.Error("--title must reach the report: the calling agent names the session")
	}
	if strings.Contains(html, "__TOKENAMUN_DATA__") {
		t.Error("the payload placeholder was not substituted")
	}
}

func TestInterventionsListsBuiltInsAndSaysWhereScriptsGo(t *testing.T) {
	localFixture(t, "carry.jsonl")
	out, err := capture(t, "interventions")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cache-ttl", "repeated-retrieval", "clear-on-new-task", "file-compression"} {
		if !strings.Contains(out, name) {
			t.Errorf("the listing is missing %q", name)
		}
	}
	if !strings.Contains(out, "interventions") {
		t.Error("it should say where scripts are loaded from")
	}

	var rows []struct {
		Name    string `json:"name"`
		Targets string `json:"targets"`
		Source  string `json:"source"`
	}
	jsonOut, err := capture(t, "interventions", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(jsonOut), &rows); err != nil {
		t.Fatalf("the machine-readable listing is what an agent reads: %v", err)
	}
	for _, r := range rows {
		if r.Name == "" || r.Targets == "" || r.Source == "" {
			t.Errorf("every row needs a name, a target and a source: %+v", r)
		}
	}
}

func TestWhatIfNeedsAnInterventionAndNamesThemWhenAsked(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	_, err := capture(t, "what-if", "--dir", repo)
	if err == nil {
		t.Fatal("what-if with no intervention must be an error")
	}
	if !strings.Contains(err.Error(), "cache-ttl") {
		t.Errorf("the error should list what can be asked: %v", err)
	}
	if _, err := capture(t, "what-if", "no-such-thing", "--dir", repo); err == nil {
		t.Error("an unknown intervention must be an error")
	}
}

func TestWhatIfRunsEveryBuiltInOverAFixture(t *testing.T) {
	// Each intervention has its own unit tests; this asserts that none of them
	// panics or fails on a real parsed session, which is how the report is
	// actually produced.
	repo := localFixture(t, "carry.jsonl")
	for _, name := range []string{
		"cache-ttl", "repeated-retrieval", "clear-on-new-task",
		"output-compression", "file-compression", "caveman", "rtk", "mcp-to-cli",
	} {
		t.Run(name, func(t *testing.T) {
			out, err := capture(t, "what-if", name, "--dir", repo, "--json")
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			var doc struct {
				Result struct {
					Unknown []string `json:"unknown"`
				} `json:"result"`
				Unknown []string `json:"unknown"`
			}
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("%s produced unparseable JSON: %v", name, err)
			}
			// The rule that holds for every intervention, checked through the
			// command rather than in the package, so a report cannot ship
			// without it.
			if len(doc.Unknown) == 0 && len(doc.Result.Unknown) == 0 {
				t.Errorf("%s reported nothing it cannot know", name)
			}
		})
	}
}

func TestScanBudgetsFailTheCommandRatherThanJustPrinting(t *testing.T) {
	// A budget nobody enforces is a budget that gets raised, so the exit code
	// is the feature.
	dir := t.TempDir()
	var big strings.Builder
	big.WriteString("package x\n\nfunc F(n int) int {\n")
	for i := 0; i < 40; i++ {
		big.WriteString("\tif n > 0 {\n\t\tn--\n\t}\n")
	}
	big.WriteString("\treturn n\n}\n")
	if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(big.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := capture(t, "scan", dir); err != nil {
		t.Fatalf("with no budget the scan only reports: %v", err)
	}
	_, err := capture(t, "scan", dir, "--max-complexity", "10")
	if err == nil {
		t.Fatal("a breached complexity budget must fail the command")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Errorf("the error should say what happened: %v", err)
	}
	if _, err := capture(t, "scan", dir, "--max-file-lines", "10"); err == nil {
		t.Error("a breached file-length budget must fail the command")
	}
	if _, err := capture(t, "scan", dir, "--max-complexity", "1000", "--max-file-lines", "1000"); err != nil {
		t.Errorf("budgets that are not breached must pass: %v", err)
	}
}

func TestVersionSaysWhatItWasValidatedAgainst(t *testing.T) {
	out, err := capture(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	// The formats this tool reads are someone else's, and they move. A version
	// that does not say which ones it was checked against is not much use when
	// the output looks wrong.
	if !strings.Contains(out, "Entire") || !strings.Contains(out, "Claude Code") {
		t.Errorf("version should name the formats it was validated against: %q", out)
	}
}

func TestHotspotsJoinsCodeMetricsOntoSessionCost(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	// The tree to scan is a separate input from where the sessions live,
	// because they routinely differ: a session recorded on one branch is
	// profiled from a checkout on another.
	scanDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(scanDir, "a.go"),
		[]byte("package a\n\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, form := range [][]string{
		{"hotspots", "--dir", repo, "--scan", scanDir},
		{"hotspots", "--dir", repo, "--scan", scanDir, "--json"},
	} {
		out, err := capture(t, form...)
		if err != nil {
			t.Fatalf("%v: %v", form, err)
		}
		if strings.TrimSpace(out) == "" {
			t.Errorf("%v produced nothing", form)
		}
	}
}

func TestCompareNeedsTwoSessions(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	if _, err := capture(t, "compare", "--dir", repo, "fixture-session"); err == nil {
		t.Error("compare with one session must say what it needs")
	}
	// The same session twice is a degenerate but valid comparison, and it
	// exercises the whole path without a second fixture.
	out, err := capture(t, "compare", "--dir", repo, "fixture-session", "fixture-session")
	if err != nil {
		t.Fatalf("comparing a session with itself: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("compare produced nothing")
	}
}

func TestSeriesReadsProbeRunsFromProfileOutput(t *testing.T) {
	// Experiment support: several runs of the same probe, summarised as a
	// median and a range, because one run of a stochastic process is an
	// anecdote. The input is this tool's own `profile --json`, so the probe
	// files are produced rather than hand-written -- a hand-written fixture
	// here would drift from the format it claims to be.
	repo := localFixture(t, "carry.jsonl")
	profile, err := capture(t, "profile", "--dir", repo, "--json")
	if err != nil {
		t.Fatal(err)
	}
	// Runs are grouped by filename stem, so `before-1.json` and `after-1.json`
	// are two steps rather than six runs of one thing.
	dir := t.TempDir()
	var files []string
	write := func(name string, scale float64) {
		path := filepath.Join(dir, name)
		body := strings.Replace(profile,
			`"total_cost"`, `"total_cost_original"`, 1)
		// Re-state the one figure series reads, scaled, so the two steps
		// differ and the effect is not zero.
		body = strings.Replace(body, `"usage": {`, fmt.Sprintf(
			`"usage": {"total_cost": {"value": %.0f, "unit": "eit", "provenance": "derived"},`,
			scale), 1)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, path)
	}
	for i := 0; i < 3; i++ {
		write(fmt.Sprintf("a-before-%d.json", i), 10000+float64(i)*500)
	}
	for i := 0; i < 3; i++ {
		write(fmt.Sprintf("b-after-%d.json", i), 8000+float64(i)*500)
	}

	out, err := capture(t, append([]string{"series"}, files...)...)
	if err != nil {
		t.Fatalf("series over six probe runs: %v", err)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("series produced nothing")
	}

	// A payback figure needs a measured cost to pay back; without --cost the
	// report must not invent one.
	if strings.Contains(out, "Intervention cost") {
		t.Error("payback was reported without a measured intervention cost")
	}
	withCost, err := capture(t, append([]string{"series", "--cost", "5000"}, files...)...)
	if err != nil {
		t.Fatalf("series with a measured intervention cost: %v", err)
	}
	if !strings.Contains(withCost, "Intervention cost") {
		t.Errorf("--cost is what payback is computed against, and it was not reported:\n%s", withCost)
	}
}

func TestNamedInterventionScriptIsLoadedAndAppearsInTheListing(t *testing.T) {
	localFixture(t, "carry.jsonl")
	script := filepath.Join(t.TempDir(), "example")
	body := `#!/bin/sh
case "$1" in
describe) printf '{"name":"example","description":"an example"}' ;;
esac
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := capture(t, "interventions", "--intervention", script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "example") {
		t.Errorf("a script named with --intervention must be listed: %q", out)
	}
	if !strings.Contains(out, script) {
		t.Error("the listing should say which file an intervention came from")
	}
}
