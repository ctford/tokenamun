package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ctford/tokenamun/internal/model"
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

func TestTreeIsNavigableWithoutABrowser(t *testing.T) {
	// The whole point: an agent must be able to learn what the HTML viewer
	// shows a person. That means reaching every level by name.
	repo := localFixture(t, "carry.jsonl")

	out, err := capture(t, "tree", "--dir", repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "At everything") {
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
	if payload.Title != "T" || payload.Tree == nil || payload.RampMax <= 0 {
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

func TestAllSumsEverySessionInTheWindow(t *testing.T) {
	// "all" is a selector rather than a command, so every level, percentage
	// and drill-in works over a team's whole history exactly as it does over
	// one session. That is the point: with Entire, profiling a team is the
	// normal case and one session is the special one.
	repo := localFixture(t, "carry.jsonl")
	out, err := capture(t, "tree", "all", "--dir", repo, "--since", "7d")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sessions", "OF LEVEL", "TRIPS"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summed tree is missing %q:\n%s", want, out)
		}
	}

	var v struct {
		Session struct {
			ID    string `json:"id"`
			Calls int    `json:"api_calls"`
		} `json:"session"`
		Total    float64 `json:"session_total"`
		Children []struct {
			Name string `json:"name"`
		} `json:"children"`
	}
	jsonOut, err := capture(t, "tree", "all", "--dir", repo, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(jsonOut), &v); err != nil {
		t.Fatal(err)
	}
	if v.Total <= 0 || len(v.Children) == 0 || v.Session.Calls == 0 {
		t.Errorf("unexpected summed totals: %+v", v)
	}
	// The header says what it covers, since "session" would be a lie.
	if !strings.Contains(v.Session.ID, "session") {
		t.Errorf("the id should describe the set, got %q", v.Session.ID)
	}

	// And it drills in and optimises like any other selector.
	if _, err := capture(t, "treemap", "all", "--dir", repo,
		"-o", filepath.Join(t.TempDir(), "all.html")); err != nil {
		t.Errorf("treemap all: %v", err)
	}
	if _, err := capture(t, "optimise", "all", "--dir", repo, "--at", "cli output",
		"--optimise", "0.5", "--why", "A guess."); err != nil {
		t.Errorf("optimise all: %v", err)
	}
}

func TestSessionsAreNotListedTwiceWhenBothSourcesHaveThem(t *testing.T) {
	// A repository with Entire recordings AND local Claude Code transcripts
	// has both copies of the same session. Listing each twice was cosmetic;
	// summing them in `period` was not -- eight of seventeen sessions in one
	// repository appeared twice, so a week's total counted them both.
	refs := []model.SessionRef{
		{ID: "a", Origin: model.FromEntire},
		{ID: "a", Origin: model.FromLocal},
		{ID: "b", Origin: model.FromEntire},
		{ID: "c", Origin: model.FromLocal},
	}
	got := dedupe(refs)
	if len(got) != 3 {
		t.Fatalf("expected 3 unique sessions, got %d: %+v", len(got), got)
	}
	byID := map[string]model.SessionRef{}
	for _, r := range got {
		byID[r.ID] = r
	}
	// The local transcript wins the overlap: both are the same session, but
	// Entire's copy is a checkpoint snapshot while the local file is appended
	// to until the session ends.
	if byID["a"].Origin != model.FromLocal {
		t.Errorf("the more complete copy should win, got %q", byID["a"].Origin)
	}
	// And an Entire-only session must survive. This is the team case: a clone
	// carries everybody's checkpoints and none of their local transcripts, so
	// preferring local must never mean discarding what only Entire has.
	if byID["b"].Origin != model.FromEntire {
		t.Error("an Entire-only session must be kept")
	}
	if _, ok := byID["c"]; !ok {
		t.Error("a local-only session must be kept")
	}
	// Order is preserved, so "latest" still means what it did.
	if got[0].ID != "a" || got[2].ID != "c" {
		t.Errorf("discovery order changed: %v", []string{got[0].ID, got[1].ID, got[2].ID})
	}
}

// --prices is opt-in and, where it is not honoured, an error. A flag that
// silently does nothing is the failure mode this CLI is built to avoid: an
// agent reads the output as an answer to the question it asked.
func TestPricesFlagIsHonouredOrRefused(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")

	for _, cmd := range []string{"profile", "cache", "tree"} {
		t.Run(cmd, func(t *testing.T) {
			plain, err := capture(t, cmd, "--dir", repo, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(plain, "$") {
				t.Error("money printed without --prices; EIT is the default unit")
			}
			priced, err := capture(t, cmd, "--dir", repo, "fixture", "--prices")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(priced, "$") {
				t.Fatalf("--prices printed no money:\n%s", priced)
			}
			if !strings.Contains(priced, "litellm@") {
				t.Errorf("--prices printed money with no catalog pin:\n%s", priced)
			}
		})
	}

	for _, cmd := range []string{"retrieval", "carry", "report", "optimise", "sessions", "length"} {
		if _, err := capture(t, cmd, "--dir", repo, "fixture", "--prices"); err == nil {
			t.Errorf("%s accepted --prices and did nothing with it", cmd)
		}
	}
}

// The JSON contract carries the same figures, in their own unit, with the pin.
func TestPricesReachTheJSONContract(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	out, err := capture(t, "profile", "--dir", repo, "fixture", "--prices", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Session struct {
			Prices *struct {
				Total struct {
					Value float64 `json:"value"`
					Unit  string  `json:"unit"`
					Prov  string  `json:"provenance"`
				} `json:"total_cost"`
				Catalog string `json:"catalog"`
			} `json:"prices"`
		} `json:"session"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	p := doc.Session.Prices
	if p == nil {
		t.Fatal("--prices produced no prices block")
	}
	if p.Total.Unit != "usd" || p.Total.Prov != "derived" {
		t.Errorf("total is %s/%s, want usd/derived", p.Total.Unit, p.Total.Prov)
	}
	if p.Total.Value <= 0 {
		t.Errorf("total is %v", p.Total.Value)
	}
	if !strings.Contains(p.Catalog, "litellm@") {
		t.Errorf("catalog pin is %q", p.Catalog)
	}
}
