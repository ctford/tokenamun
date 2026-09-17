// Command tokenamun profiles coding-agent token usage.
//
// It reads transcripts that already exist: Entire's recordings inside a
// repository, and Claude Code's own session files under ~/.claude/projects.
// It records nothing itself.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/claudecode"
	"github.com/ctford/tokenamun/internal/codescan"
	"github.com/ctford/tokenamun/internal/content"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/entire"
	"github.com/ctford/tokenamun/internal/ingest"
	"github.com/ctford/tokenamun/internal/model"
	"github.com/ctford/tokenamun/internal/report"
	"github.com/ctford/tokenamun/internal/whatif"
)

// version is overridden at build time with -X main.version.
var version = "dev"

const usage = `tokenamun - a profiler for coding-agent token usage

Usage:
  tokenamun sessions              list the sessions it can see
  tokenamun profile [session]     where the tokens went, and what they cost
  tokenamun retrieval [session]   what content entered the context, and from where
  tokenamun carry [session]       what it cost to keep content, not to fetch it
  tokenamun cache [session]       why the prompt cache was rebuilt, and what it cost
  tokenamun scan [path]           code properties: size, complexity, duplication
  tokenamun hotspots [session]    code properties joined against session cost
  tokenamun compare <a> <b>       two sessions side by side
  tokenamun what-if <name> [session]
                                  would an optimisation have helped, and by how much
  tokenamun treemap [session]     standalone HTML viewer: drill down from how
                                  content was obtained to the individual files
  tokenamun series <file>...      probe runs from an experiment: median, range, payback
  tokenamun version

Session selector:
  a session-id prefix, or "current" for the session invoking this tool,
  or "latest" (the default) for the most recently active one.

Interventions for what-if:
  cache-ttl            5-minute prompt cache to 1-hour
  repeated-retrieval   fetch byte-identical content once
  output-compression   compress tool output before it enters context
  caveman              Caveman-style compression
  mcp-to-cli           put an MCP server behind a CLI

Flags:
  --json          machine-readable output
  --dir PATH      directory to look in (default: working directory)
  --source SRC    entire | local | any (default: any)
  --ratio N       assumed surviving fraction for compression (default 0.5)
  --replay-with C pipe this session's own content through a real compressor
                  instead of assuming a ratio; C reads stdin, writes stdout
  -o FILE         output file (treemap; default tokenamun-treemap.html)
  --title TEXT    heading for the treemap, e.g. "Hyper Agentic App"
  --cost N        measured intervention cost in EIT, for series payback
  --scan PATH     tree to scan for code metrics (hotspots; default --dir).
                  Point this at a checkout of the branch the session ran on.
  --config PATH   classification config. Defaults to .tokenamun.json found by
                  walking up from --dir; without one, directory-naming
                  heuristics are used and reports say so.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tokenamun:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	cmd, rest := args[0], args[1:]

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	dir := fs.String("dir", ".", "directory to look in")
	source := fs.String("source", "any", "entire | local | any")
	ratio := fs.Float64("ratio", 0.5, "assumed surviving fraction for compression")
	replayWith := fs.String("replay-with", "", "command to replay content through")
	out := fs.String("o", "tokenamun-treemap.html", "output file for the treemap")
	interventionCost := fs.Float64("cost", 0, "measured intervention cost in EIT, for payback")
	scanDir := fs.String("scan", "", "tree to scan for code metrics (default: --dir)")
	configPath := fs.String("config", "", "classification config (default: .tokenamun.json, found upwards)")
	title := fs.String("title", "", "heading for the treemap report")
	// Go's flag package stops parsing at the first positional argument, which
	// would make `tokenamun profile current --json` silently ignore --json.
	// For a CLI agents invoke, silently dropping a flag is the worst failure
	// mode available, so flags and positionals are allowed to intersperse.
	positional, err := parseInterspersed(fs, rest)
	if err != nil {
		return err
	}
	selector := "latest"
	if len(positional) > 0 {
		selector = positional[0]
	}
	second := ""
	if len(positional) > 1 {
		second = positional[1]
	}

	switch cmd {
	case "sessions":
		return cmdSessions(*dir, *source, *asJSON)
	case "profile":
		return cmdProfile(*dir, *source, selector, *configPath, *asJSON)
	case "retrieval":
		return cmdRetrieval(*dir, *source, selector, *configPath, *asJSON)
	case "carry":
		return cmdCarry(*dir, *source, selector, *configPath, *asJSON)
	case "cache":
		return cmdCache(*dir, *source, selector, *configPath, *asJSON)
	case "scan":
		return cmdScan(*dir, selector, *asJSON)
	case "hotspots":
		return cmdHotspots(*dir, *scanDir, *source, selector, *asJSON)
	case "compare":
		return cmdCompare(*dir, *source, selector, second, *asJSON)
	case "series":
		return cmdSeries(positional, *interventionCost, *asJSON)
	case "treemap":
		return cmdTreemap(*dir, *source, selector, *configPath, *title, *out)
	case "what-if", "whatif":
		return cmdWhatIf(*dir, *source, selector, second, *ratio, *replayWith, *asJSON)
	case "version":
		fmt.Printf("tokenamun %s\n", version)
		fmt.Println("validated against Entire CLI 0.10.2 and Claude Code 2.1.x transcripts")
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q; try `tokenamun help`", cmd)
	}
}

// parseInterspersed parses flags that may appear before, after or between
// positional arguments, returning the positionals in order.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			break
		}
		positional = append(positional, args[0])
		args = args[1:]
	}
	return positional, nil
}

// discover lists candidate sessions from the requested sources, most recently
// active first.
func discover(dir, source string) ([]model.SessionRef, error) {
	var refs []model.SessionRef
	if source == "any" || source == "entire" {
		found, err := entire.Discover(dir)
		if err != nil && source == "entire" {
			return nil, err
		}
		refs = append(refs, found...)
	}
	if source == "any" || source == "local" {
		found, err := claudecode.DiscoverLocal(dir)
		if err != nil && source == "local" {
			return nil, err
		}
		refs = append(refs, found...)
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Current != refs[j].Current {
			return refs[i].Current
		}
		return refs[i].Modified.After(refs[j].Modified)
	})
	return refs, nil
}

func cmdSessions(dir, source string, asJSON bool) error {
	refs, err := discover(dir, source)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(map[string]any{
			"schema_version": report.SchemaVersion,
			"sessions":       refs,
		})
	}
	if len(refs) == 0 {
		fmt.Println("No sessions found.")
		fmt.Println()
		fmt.Println("Tokenamun reads transcripts that already exist. It looks for")
		fmt.Println("Entire recordings in .entire/metadata/ and Claude Code sessions")
		fmt.Println("in ~/.claude/projects/. Neither was found for this directory.")
		return nil
	}
	fmt.Printf("%-38s %-8s %-20s %s\n", "SESSION", "SOURCE", "LAST ACTIVE", "")
	for _, r := range refs {
		marker := ""
		if r.Current {
			marker = "<- this session"
		}
		fmt.Printf("%-38s %-8s %-20s %s\n", r.ID, r.Origin,
			r.Modified.Format("2006-01-02 15:04"), marker)
	}
	return nil
}

func cmdProfile(dir, source, selector, configPath string, asJSON bool) error {
	s, err := loadSelectedWith(dir, source, selector, configPath)
	if err != nil {
		return err
	}
	p := report.BuildProfile(s)
	if asJSON {
		return writeJSON(p)
	}
	return report.RenderText(os.Stdout, p)
}

func cmdRetrieval(dir, source, selector, configPath string, asJSON bool) error {
	s, err := loadSelectedWith(dir, source, selector, configPath)
	if err != nil {
		return err
	}
	r := report.BuildRetrieval(s)
	if asJSON {
		return writeJSON(r)
	}
	return report.RenderRetrieval(os.Stdout, r)
}

func cmdCarry(dir, source, selector, configPath string, asJSON bool) error {
	s, err := loadSelectedWith(dir, source, selector, configPath)
	if err != nil {
		return err
	}
	r := report.BuildCarry(s, analysis.Carry(s, analysis.Cache(s, analysis.TTL5m)))
	if asJSON {
		return writeJSON(r)
	}
	return report.RenderCarry(os.Stdout, r)
}

func cmdCache(dir, source, selector, configPath string, asJSON bool) error {
	s, err := loadSelectedWith(dir, source, selector, configPath)
	if err != nil {
		return err
	}
	r := report.BuildCache(s, analysis.Cache(s, analysis.TTL5m))
	if asJSON {
		return writeJSON(r)
	}
	return report.RenderCache(os.Stdout, r)
}

// cmdScan measures code properties without reference to any session.
func cmdScan(dir, selector string, asJSON bool) error {
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
		return writeJSON(out)
	}
	return report.RenderScan(os.Stdout, out)
}

// cmdHotspots joins code metrics onto session cost.
//
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

// cmdSeries aggregates previously-emitted profile JSON files.
//
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

func cmdTreemap(dir, source, selector, configPath, title, outPath string) error {
	s, err := loadSelectedWith(dir, source, selector, configPath)
	if err != nil {
		return err
	}
	payload := report.BuildTreemapTitled(s,
		analysis.Carry(s, analysis.Cache(s, analysis.TTL5m)), title)

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := report.RenderTreemap(f, payload); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d retrievals)\n", outPath, len(payload.Items))
	fmt.Println("Area is observed retrieved-content size, not a picture of the context window.")
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

// cmdWhatIf takes the intervention name first, then an optional session.
func cmdWhatIf(dir, source, name, selector string, ratio float64, replayWith string, asJSON bool) error {
	if name == "" || name == "latest" {
		var names []string
		for _, i := range whatif.All() {
			names = append(names, i.Name())
		}
		return fmt.Errorf("what-if needs an intervention: %v", names)
	}
	intervention, err := whatif.Find(name)
	if err != nil {
		return err
	}
	if selector == "" {
		selector = "latest"
	}

	refs, err := discover(dir, source)
	if err != nil {
		return err
	}
	ref, err := selectSession(refs, selector)
	if err != nil {
		return err
	}
	s, err := ingest.Load(ref)
	if err != nil {
		return err
	}

	cache := analysis.Cache(s, analysis.TTL5m)
	ctx := whatif.Context{
		Session:          s,
		Cache:            cache,
		Carry:            analysis.Carry(s, cache),
		Weights:          cost.Default,
		CompressionRatio: ratio,
	}
	if ms := s.Models(); len(ms) > 0 {
		ctx.Weights = cost.For(ms[0])
	}
	if replayWith != "" {
		replay, err := whatif.Replay(ref, replayWith)
		if err != nil {
			return fmt.Errorf("replay failed: %w", err)
		}
		ctx.Replay = replay
	}

	out := report.BuildWhatIf(s, intervention.Estimate(ctx))
	if asJSON {
		return writeJSON(out)
	}
	return report.RenderWhatIf(os.Stdout, out)
}

// loadSelected resolves a selector and parses the transcript it names.
func loadSelected(dir, source, selector string) (*model.Session, error) {
	return loadSelectedWith(dir, source, selector, "")
}

// loadSelectedWith parses a transcript using a declared classification config
// where one is available.
func loadSelectedWith(dir, source, selector, configPath string) (*model.Session, error) {
	refs, err := discover(dir, source)
	if err != nil {
		return nil, err
	}
	ref, err := selectSession(refs, selector)
	if err != nil {
		return nil, err
	}
	classifier, err := classifierFor(dir, configPath)
	if err != nil {
		return nil, err
	}
	return ingest.LoadWith(ref, ingest.Options{Classifier: classifier})
}

// classifierFor resolves the classification config: an explicit path if given,
// otherwise .tokenamun.json found by walking up from dir, otherwise the
// built-in naming heuristics.
func classifierFor(dir, configPath string) (content.Classifier, error) {
	if configPath != "" {
		return content.LoadConfigFile(configPath)
	}
	return content.LoadConfig(dir)
}

// selectSession resolves a selector against the discovered sessions.
func selectSession(refs []model.SessionRef, selector string) (model.SessionRef, error) {
	if len(refs) == 0 {
		return model.SessionRef{}, fmt.Errorf(
			"no sessions found; run `tokenamun sessions` to see where it looked")
	}
	switch selector {
	case "latest", "":
		return refs[0], nil
	case "current":
		for _, r := range refs {
			if r.Current {
				return r, nil
			}
		}
		return model.SessionRef{}, fmt.Errorf(
			"no current session: CLAUDE_CODE_SESSION_ID is not set, so this is not running inside Claude Code")
	}
	var matches []model.SessionRef
	for _, r := range refs {
		if strings.HasPrefix(r.ID, selector) {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return model.SessionRef{}, fmt.Errorf("no session matches %q", selector)
	case 1:
		return matches[0], nil
	default:
		return model.SessionRef{}, fmt.Errorf("%q matches %d sessions; use a longer prefix",
			selector, len(matches))
	}
}

func writeJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
