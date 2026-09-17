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

func TestTreeIsNavigableWithoutABrowser(t *testing.T) {
	// The whole point: an agent must be able to learn what the HTML viewer
	// shows a person. That means reaching every level by name.
	repo := localFixture(t, "carry.jsonl")

	out, err := capture(t, "tree", "--dir", repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "At session") {
		t.Errorf("the root level should say where it is:\n%s", out)
	}

	// The JSON form is what an agent reads, and it has to be navigable rather
	// than merely readable: every branch carries the value for --at.
	var v struct {
		Path     []string `json:"path"`
		Children []struct {
			Name     string `json:"name"`
			At       string `json:"at"`
			Children int    `json:"children"`
		} `json:"children"`
		Drill []string `json:"drill_in"`
	}
	jsonOut, err := capture(t, "tree", "--dir", repo, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(jsonOut), &v); err != nil {
		t.Fatalf("the tree JSON is what an agent reads: %v", err)
	}
	if len(v.Children) == 0 {
		t.Fatal("the fixture session has content")
	}

	// Walk in using only what the previous level said, which is exactly what
	// an agent has to do.
	var walked int
	for _, c := range v.Children {
		if c.Children == 0 {
			continue
		}
		if _, err := capture(t, "tree", "--dir", repo, "--at", c.At, "--json"); err != nil {
			t.Errorf("--at %q, taken from the level above, failed: %v", c.At, err)
		}
		walked++
	}
	if walked == 0 {
		t.Skip("the fixture has no branches to walk into")
	}
}

func TestTreeRejectsAnUnknownNodeAndAnUnknownMode(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	_, err := capture(t, "tree", "--dir", repo, "--at", "nowhere")
	if err == nil {
		t.Fatal("an unknown node must be an error, not the root")
	}
	if !strings.Contains(err.Error(), "it contains:") {
		t.Errorf("the error is how a caller discovers the real names: %v", err)
	}
	if _, err := capture(t, "tree", "--dir", repo, "--mode", "sideways"); err == nil {
		t.Error("an unknown cost mode must be an error, not silently priced as billed")
	}
}

func TestTreeUncachedModeIsReachableFromTheCLI(t *testing.T) {
	// The viewer has two cost buttons. If only one of them is reachable here,
	// what caching was worth is a question only a human can ask.
	repo := localFixture(t, "carry.jsonl")
	billed, err := capture(t, "tree", "--dir", repo, "--mode", "carry")
	if err != nil {
		t.Fatal(err)
	}
	uncached, err := capture(t, "tree", "--dir", repo, "--mode", "uncached")
	if err != nil {
		t.Fatal(err)
	}
	if billed == uncached {
		t.Error("the two cost modes produced identical output")
	}
	if !strings.Contains(uncached, "nothing cached") {
		t.Errorf("the uncached mode must say what it is showing:\n%s", uncached)
	}
}

func TestWhatIfAllRanksEveryInterventionInOneCall(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	out, err := capture(t, "what-if", "--all", "--dir", repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "INTERVENTION") {
		t.Errorf("the summary should be a table:\n%s", out)
	}

	var doc struct {
		Rows []struct {
			Name       string `json:"name"`
			Applicable bool   `json:"applicable"`
			Detail     string `json:"detail_command"`
		} `json:"interventions"`
		Notes []string `json:"notes"`
	}
	jsonOut, err := capture(t, "what-if", "--all", "--dir", repo, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(jsonOut), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Rows) < 8 {
		t.Errorf("expected every intervention, got %d", len(doc.Rows))
	}
	for _, r := range doc.Rows {
		if r.Detail == "" {
			t.Errorf("%s does not say how to see the full result", r.Name)
		}
	}
}

func TestTreemapJSONIsTheViewersOwnPayload(t *testing.T) {
	// The strongest parity guarantee available: --json prints what the HTML is
	// given, so nothing can reach the picture without reaching the CLI.
	repo := localFixture(t, "carry.jsonl")
	jsonOut, err := capture(t, "treemap", "--dir", repo, "--json", "--title", "T")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Title         string  `json:"title"`
		Tree          any     `json:"tree"`
		Interventions []any   `json:"interventions"`
		RampMax       float64 `json:"rampMax"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatalf("--json must print the payload: %v", err)
	}
	if payload.Title != "T" || payload.Tree == nil || len(payload.Interventions) == 0 {
		t.Errorf("the payload is missing pieces the viewer draws: %+v", payload)
	}

	// And --json must not also write a file: an agent asking for data has not
	// asked for an artefact on disk.
	path := filepath.Join(t.TempDir(), "should-not-exist.html")
	if _, err := capture(t, "treemap", "--dir", repo, "--json", "-o", path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("--json wrote an HTML file as well")
	}
}

func TestAdHocInterventionNeedsNoVendorSupport(t *testing.T) {
	// The generic form: name a slice of the tree and how much of it goes
	// away. Everything that shrinks content is that shape, so an agent can
	// ask about a technique this tool has never heard of.
	repo := localFixture(t, "carry.jsonl")
	out, err := capture(t, "what-if", "--dir", repo, "--at", "cli output",
		"--cut", "0.5", "--name", "some-proxy", "--why", "Vendor figure, not measured.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "some-proxy") {
		t.Errorf("the caller names the row:\n%s", out)
	}
	if !strings.Contains(out, "Vendor figure, not measured.") {
		t.Error("the caller's caveat must be printed with the number")
	}

	// It must not be possible to get a number without saying why it is
	// plausible, or what it applies to.
	for _, args := range [][]string{
		{"what-if", "--dir", repo, "--cut", "0.5", "--why", "x."},
		{"what-if", "--dir", repo, "--at", "cli output", "--cut", "0.5"},
		{"what-if", "--dir", repo, "--at", "cli output", "--cut", "9", "--why", "x."},
	} {
		if _, err := capture(t, args...); err == nil {
			t.Errorf("%v should have been refused", args[3:])
		}
	}
}

func TestAdHocInterventionJoinsTheSummaryTable(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	out, err := capture(t, "what-if", "--all", "--dir", repo, "--at", "file content",
		"--cut", "0.3", "--name", "trim-the-docs", "--why", "A guess, not a measurement.")
	if err != nil {
		t.Fatal(err)
	}
	// Ranked among the built-ins, with no way to tell it apart structurally.
	if !strings.Contains(out, "trim-the-docs") {
		t.Errorf("an ad-hoc intervention must rank with the rest:\n%s", out)
	}
	if !strings.Contains(out, "ADDRESSABLE") || !strings.Contains(out, "OPTIMISATION") {
		t.Error("the summary must show the decomposition, not just the product")
	}
}

func TestWindowScopesToAPeriod(t *testing.T) {
	// The before-and-after question: what did sessions cost after we added
	// the thing, against before.
	repo := localFixture(t, "carry.jsonl")

	// The fixture's transcript was written now, so a window in the past
	// excludes it and a window covering today includes it.
	if out, err := capture(t, "sessions", "--dir", repo, "--since", "2020-01-01",
		"--until", "2020-02-01"); err != nil {
		t.Fatal(err)
	} else if strings.Contains(out, "fixture-session") {
		t.Error("a session outside the window must not be listed")
	}
	out, err := capture(t, "sessions", "--dir", repo, "--since", "7d")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "fixture-session") {
		t.Errorf("a session inside the window must be listed:\n%s", out)
	}

	// A window that matches nothing says so, rather than reporting an empty
	// repository: those are different problems.
	_, err = capture(t, "profile", "--dir", repo, "--since", "2020-01-01", "--until", "2020-02-01")
	if err == nil {
		t.Fatal("an empty window must be an error")
	}
	if !strings.Contains(err.Error(), "--since") {
		t.Errorf("the error should point at the flags: %v", err)
	}

	// And a backwards window is refused up front rather than silently
	// matching nothing.
	if _, err := capture(t, "sessions", "--dir", repo,
		"--since", "2026-09-17", "--until", "2026-09-01"); err == nil {
		t.Error("a window that ends before it starts must be refused")
	}
	if _, err := capture(t, "sessions", "--dir", repo, "--since", "last tuesday"); err == nil {
		t.Error("an unparseable date must be refused, not ignored")
	}
}

func TestPeriodSumsEverySessionInTheWindow(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	out, err := capture(t, "period", "--dir", repo, "--since", "7d")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Sessions", "API calls", "OF TOTAL", "TRIPS"} {
		if !strings.Contains(out, want) {
			t.Errorf("the period report is missing %q:\n%s", want, out)
		}
	}

	var doc struct {
		Window   string `json:"window"`
		Sessions int    `json:"sessions"`
		Calls    int    `json:"api_calls"`
		Tree     struct {
			Carry    float64 `json:"carry"`
			Children []struct {
				Name string `json:"name"`
			} `json:"children"`
		} `json:"tree"`
		Notes []string `json:"notes"`
	}
	jsonOut, err := capture(t, "period", "--dir", repo, "--since", "7d", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(jsonOut), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Sessions != 1 || doc.Calls == 0 || doc.Tree.Carry <= 0 {
		t.Errorf("unexpected period totals: %+v", doc)
	}
	if doc.Window == "" {
		t.Error("the report must state the window it covers")
	}
	// It must say that cost adds and residency does not, because that is the
	// one thing a reader could get wrong about a summed report.
	var explained bool
	for _, n := range doc.Notes {
		explained = explained || strings.Contains(n, "residency is not")
	}
	if !explained {
		t.Error("a summed report must say what is additive and what is not")
	}
}
