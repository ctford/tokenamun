package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/codescan"
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
	s, err := loadSelected(dir, source, selector)
	if err != nil {
		return err
	}
	var path []string
	if at != "" {
		path = strings.Split(at, "/")
	}
	v, err := report.BuildTreeView(s, analysis.Carry(s, analysis.Cache(s, analysis.TTL5m)),
		path, mode)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(v)
	}
	return report.RenderTreeView(os.Stdout, v)
}

func cmdTreemap(dir, source, selector, title, outPath string, asJSON bool) error {
	s, err := loadSelected(dir, source, selector)
	if err != nil {
		return err
	}
	payload := report.BuildTreemapTitled(s,
		analysis.Carry(s, analysis.Cache(s, analysis.TTL5m)), title)

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
	fmt.Printf("wrote %s (%d retrievals)\n", outPath, len(s.Retrievals))
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
func cmdScan(dir, selector string, budget codescan.Budget, asJSON bool) error {
	root := dir
	// scan takes a path rather than a session, so a positional argument here
	// is a directory.
	if selector != "" && selector != "latest" {
		root = selector
	}
	r, err := codescan.Scan(root, codescan.DefaultOptions())
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

// cmdPeriod sums every session in the window.
//
// The unit a before-and-after question needs: 73 sessions in a day is not
// something anybody reads one at a time.
func cmdPeriod(dir, source string, asJSON bool) error {
	refs, err := discover(dir, source)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return fmt.Errorf("no sessions in %s", window)
	}
	sessions, failed := report.Readable(refs, func(r model.SessionRef) (*model.Session, error) {
		return ingest.Load(r)
	})
	if len(sessions) == 0 {
		return fmt.Errorf("none of the %d sessions in %s could be read", len(refs), window)
	}

	out := report.BuildPeriod(sessions, window.String(), failed)
	if asJSON {
		return writeJSON(out)
	}
	return report.RenderPeriod(os.Stdout, out)
}
