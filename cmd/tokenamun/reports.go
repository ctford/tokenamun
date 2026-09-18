package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/codescan"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/ingest"
	"github.com/ctford/tokenamun/internal/model"
	"github.com/ctford/tokenamun/internal/report"
)

// The commands that produce a report over a session or a tree, as against
// the dispatch and session-selection machinery in main.go. Split when main.go
// hit the file-length budget, and along the seam the budget exposed: this
// half changes when a report changes, the other when the CLI's shape does.

// The viewer needs a browser and a mouse. This is the same tree, reachable by
// name, so an agent can answer "where did the tokens go" without a person
// reading a picture to it.
func cmdTree(dir, source, selector, at, mode string, asJSON bool) error {
	tree, info, err := loadTree(dir, source, selector)
	if err != nil {
		return err
	}
	var path []string
	if at != "" {
		path = strings.Split(at, "/")
	}
	v, err := report.BuildTreeViewFrom(tree, info, path, mode)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(v)
	}
	return report.RenderTreeView(os.Stdout, v)
}

func cmdReport(dir, source, selector, title, outPath string, asJSON bool) error {
	tree, info, err := loadTree(dir, source, selector)
	if err != nil {
		return err
	}
	payload := report.BuildTreemapFrom(tree,
		report.TreemapSession(info.ID, info.Calls, string(info.Origin)), title)

	// --json prints the report's own payload: byte for byte what the HTML
	// viewer is given. It is the guarantee that the two views cannot diverge,
	// and it is how something automating this gets the whole hierarchy in one
	// call instead of walking `tree --at` down every branch.
	if asJSON {
		return writeJSON(payload)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := report.RenderTreemap(f, payload); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d retrievals)\n", outPath, payload.Tree.Items)
	fmt.Println("Area is cost-weighted tokens. It is not a picture of the context window.")
	return nil
}

func cmdCompare(dir, source, a, b string, asJSON bool) error {
	if a == "" || b == "" {
		return fmt.Errorf("compare needs two sessions: tokenamun compare <a> <b>")
	}
	sa, err := loadSelected(dir, source, a)
	if err != nil {
		return fmt.Errorf("session a: %w", err)
	}
	sb, err := loadSelected(dir, source, b)
	if err != nil {
		return fmt.Errorf("session b: %w", err)
	}
	out := report.BuildCompare(
		report.BuildProfile(sa), report.BuildProfile(sb),
		report.BuildRetrieval(sa), report.BuildRetrieval(sb))
	if asJSON {
		return writeJSON(out)
	}
	return report.RenderCompare(os.Stdout, out)
}

// Runs of the same step share a filename prefix up to the last hyphen, so a
// driver script needs no manifest: step-07-probe-1.json and
// step-07-probe-2.json are two runs of one step.
func cmdSeries(files []string, interventionCost float64, asJSON bool) error {
	if len(files) == 0 {
		return fmt.Errorf("series needs profile JSON files: tokenamun series step-*.json")
	}
	s, err := analysis.LoadSeries(files, interventionCost)
	if err != nil {
		return err
	}
	out := report.BuildSeries(s)
	if asJSON {
		return writeJSON(out)
	}
	return report.RenderSeries(os.Stdout, out)
}

// cmdScan measures code properties without reference to any session.
func cmdScan(dir, selector string, skipDuplicatesIn []string, budget codescan.Budget,
	asJSON bool) error {
	root := dir
	// scan takes a path rather than a session, so a positional argument here
	// is a directory.
	if selector != "" && selector != "latest" {
		root = selector
	}
	opts := codescan.DefaultOptions()
	opts.SkipDuplicatesIn = skipDuplicatesIn
	r, err := codescan.Scan(root, opts)
	if err != nil {
		return err
	}
	out := report.BuildScan(r)
	if asJSON {
		if err := writeJSON(out); err != nil {
			return err
		}
	} else if err := report.RenderScan(os.Stdout, out); err != nil {
		return err
	}

	// Budgets turn the report into a check. Printed to stderr and returned as
	// an error, so this works as a CI gate with or without --json.
	breaches := codescan.Check(r, budget)
	if len(breaches) == 0 {
		return nil
	}
	for _, b := range breaches {
		fmt.Fprintf(os.Stderr, "over budget: %s\n", b)
	}
	return fmt.Errorf("%d code budgets exceeded", len(breaches))
}

// The tree to scan is a separate input from where the sessions live, because
// they routinely differ: a session recorded on one branch is profiled from a
// checkout on another, and then none of its files exist to measure.
func cmdHotspots(dir, scanDir, source, selector string, asJSON bool) error {
	s, err := loadSelected(dir, source, selector)
	if err != nil {
		return err
	}
	if scanDir == "" {
		scanDir = dir
	}
	scan, err := codescan.Scan(scanDir, codescan.DefaultOptions())
	if err != nil {
		return err
	}
	carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))
	out := report.BuildHotspots(s, analysis.Hotspots(s, scan, carry))
	if asJSON {
		return writeJSON(out)
	}
	return report.RenderHotspots(os.Stdout, out)
}

// SelectAll is the session selector that means every session discovered.
//
// A magic selector rather than a separate command, because profiling a team
// is the point of Entire mode and not a special case of profiling one
// session. `tokenamun tree all`, `treemap all`, `optimise all` -- every
// level, percentage and drill-in works unchanged.
const SelectAll = "all"

// loadAll merges every discovered session into one tree.
//
// Trees are merged rather than sessions concatenated: cost is additive across
// sessions, residency is not, since each session has its own context. See
// report.BuildPeriod.
func loadAll(dir, source string) (*report.Node, report.SessionInfo, error) {
	sessions, info, err := loadSessions(dir, source)
	if err != nil {
		return nil, report.SessionInfo{}, err
	}
	p := report.BuildPeriod(sessions, window.String(), nil)
	return p.Tree, info, nil
}

// loadSessions parses every discovered session once, in this process.
//
// Shared by every command that takes "all", so a team-wide report reads each
// transcript a single time. Summing by invoking the binary per session was
// slow enough on 131 sessions to be unusable.
func loadSessions(dir, source string) ([]*model.Session, report.SessionInfo, error) {
	refs, err := discover(dir, source)
	if err != nil {
		return nil, report.SessionInfo{}, err
	}
	if len(refs) == 0 {
		return nil, report.SessionInfo{}, fmt.Errorf("no sessions in %s", window)
	}
	sessions, failed := report.Readable(refs, func(r model.SessionRef) (*model.Session, error) {
		return ingest.Load(r)
	})
	if len(sessions) == 0 {
		return nil, report.SessionInfo{}, fmt.Errorf(
			"none of the %d sessions in %s could be read", len(refs), window)
	}
	for _, f := range failed {
		fmt.Fprintf(os.Stderr, "tokenamun: skipping %s\n", f)
	}

	var models []string
	var calls, prompts int
	var engaged time.Duration
	origins := map[model.Origin]bool{}
	seen := map[string]bool{}
	mixed := false
	for _, s := range sessions {
		calls += s.RealCalls()
		prompts += s.Prompts
		// Summed, not spanned. The wall-clock distance from the first
		// session's start to the last one's end counts the nights in
		// between; adding each session's own duration counts the time
		// somebody was working, which is the quantity a reader means.
		engaged += s.Duration()
		origins[s.Ref.Origin] = true
		for _, m := range s.Models() {
			if m != model.SyntheticModel && !seen[m] {
				seen[m] = true
				models = append(models, m)
			}
		}
		mixed = mixed || cost.Mixed(s.Invocations)
	}
	sort.Strings(models)
	// More than one model across the set is the same problem as within one
	// session: the unit is relative to a model's own input price.
	mixed = mixed || len(models) > 1
	return sessions, report.SessionInfo{
		ID: fmt.Sprintf("%d sessions, %s", len(sessions), window),
		// The label above reads well and is not a selector. Commands printed
		// for an agent to run need this one.
		Selector: SelectAll + window.Flags(),
		Calls:    calls,
		Prompts:  prompts,
		Duration: engaged.Round(time.Second).String(),
		// Only when the whole set agrees; a mixture is not either of them.
		Origin:       onlyOrigin(origins),
		Models:       models,
		MixedPricing: mixed,
	}, nil
}

// onlyOrigin reports the origin when every session shares one.
func onlyOrigin(origins map[model.Origin]bool) model.Origin {
	if len(origins) != 1 {
		return ""
	}
	for o := range origins {
		return o
	}
	return ""
}

// loadTree resolves a selector to a tree, which is "all" or one session.
func loadTree(dir, source, selector string) (*report.Node, report.SessionInfo, error) {
	if selector == SelectAll {
		return loadAll(dir, source)
	}
	s, err := loadSelected(dir, source, selector)
	if err != nil {
		return nil, report.SessionInfo{}, err
	}
	carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))
	return report.BuildTree(s, carry), report.SessionOf(s), nil
}
