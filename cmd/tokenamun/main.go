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
  tokenamun tree [session]        where the tokens went, one level at a time;
                                  drill in with --at. The HTML viewer as text.
  tokenamun interventions         what what-if can be asked, built-in and installed
  tokenamun what-if <name> [session]
                                  would an optimisation have helped, and by how much
  tokenamun what-if --all [session]
                                  every intervention's bottom line, ranked
  tokenamun treemap [session]     standalone HTML viewer: drill down from how
                                  content was obtained to the individual files.
                                  --json prints the viewer's own payload.
  tokenamun series <file>...      probe runs from an experiment: median, range, payback
  tokenamun version

Session selector:
  a session-id prefix, or "current" for the session invoking this tool,
  or "latest" (the default) for the most recently active one.

Interventions for what-if:
  run "tokenamun interventions", which also lists any you have installed.
  Your own go in ~/.config/tokenamun/interventions or are named with
  --intervention PATH; see docs/interventions.md for the protocol.

Flags:
  --json          machine-readable output
  --dir PATH      directory to look in (default: working directory)
  --source SRC    entire | local | any (default: any)
  --ratio N       assumed surviving fraction for compression (default 0.5)
  --replay-with C pipe this session's own content through a real compressor
                  instead of assuming a ratio; C reads stdin, writes stdout
  -o FILE         output file (treemap; default tokenamun-treemap.html)
  --title TEXT    heading for the treemap, e.g. "Hyper Agentic App"
  --at PATH       which node of the tree to show, e.g. "cli output/version control".
                  Names come from the level above; matching is case-insensitive.
  --mode MODE     tree pricing: carry (as billed) | uncached (as if nothing
                  cached). The difference is what prompt caching was worth.
  --all           what-if: run every intervention and rank them
  --cut FRACTION  what-if: an ad-hoc intervention, removing this fraction of
                  --at. Needs --why, and takes --name for the report. Every
                  intervention that shrinks content is a slice and a
                  fraction, so you can ask about one without the tool
                  knowing the vendor:
                    tokenamun what-if --at "cli output" --cut 0.5 \
                      --name caveman --why "Vendor figure, not measured here."
  --why TEXT      caveat for --cut, required, max 64 characters
  --name TEXT     what to call a --cut intervention in the report
  --cost N        measured intervention cost in EIT, for series payback
  --scan PATH     tree to scan for code metrics (hotspots; default --dir).
                  Point this at a checkout of the branch the session ran on.
  --intervention PATH
                  an intervention script to load, repeatable

Code budgets (scan). Given a limit, the scan exits non-zero when it is
exceeded, which is how it is used as a CI gate:
  --max-file-lines N        fail on a file longer than N lines
  --max-complexity N        fail on a function above complexity N
  --max-duplication PCT     fail above PCT% of duplicated code lines
  --skip-duplicates-in S    exclude paths containing S from the duplication
                            measure, repeatable
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
	title := fs.String("title", "", "heading for the treemap report")
	var extraInterventions repeatable
	fs.Var(&extraInterventions, "intervention", "path to an intervention script (repeatable)")
	at := fs.String("at", "", "drill to a node in the tree, e.g. \"cli output/git\"")
	mode := fs.String("mode", "carry", "cost mode for the tree: carry | uncached")
	all := fs.Bool("all", false, "run every intervention and summarise")
	cut := fs.Float64("cut", 0, "fraction of --at to remove, for an ad-hoc intervention")
	why := fs.String("why", "", "caveat for an ad-hoc intervention, required with --cut")
	label := fs.String("name", "", "name for an ad-hoc intervention in the report")
	maxFileLines := fs.Int("max-file-lines", 0, "fail the scan on a file longer than this")
	maxComplexity := fs.Int("max-complexity", 0, "fail the scan on a function above this complexity")
	maxDuplication := fs.Float64("max-duplication", 0, "fail the scan above this % of duplicated code lines")
	var skipDuplicatesIn repeatable
	fs.Var(&skipDuplicatesIn, "skip-duplicates-in", "exclude paths containing this from the duplication measure (repeatable)")
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

	// Interventions supplied from outside the binary are registered before
	// anything reads the list, so a script is a first-class row everywhere:
	// the what-if report, the treemap's table and the JSON output.
	loaded, errs := whatif.Discover(extraInterventions)
	for _, i := range loaded {
		whatif.Register(i)
	}
	for _, e := range errs {
		// Reported, not fatal: one broken script must not stop a report that
		// has six working interventions in it.
		fmt.Fprintf(os.Stderr, "tokenamun: ignoring an intervention: %v\n", e)
	}

	switch cmd {
	case "interventions":
		return cmdInterventions(*asJSON)
	case "sessions":
		return cmdSessions(*dir, *source, *asJSON)
	case "profile":
		return cmdProfile(*dir, *source, selector, *asJSON)
	case "retrieval":
		return cmdRetrieval(*dir, *source, selector, *asJSON)
	case "carry":
		return cmdCarry(*dir, *source, selector, *asJSON)
	case "cache":
		return cmdCache(*dir, *source, selector, *asJSON)
	case "scan":
		return cmdScan(*dir, selector, codescan.Budget{
			MaxFileLines:          *maxFileLines,
			MaxFunctionComplexity: *maxComplexity,
			MaxDuplicationPercent: *maxDuplication,
			SkipDuplicatesIn:      skipDuplicatesIn,
		}, *asJSON)
	case "hotspots":
		return cmdHotspots(*dir, *scanDir, *source, selector, *asJSON)
	case "compare":
		return cmdCompare(*dir, *source, selector, second, *asJSON)
	case "series":
		return cmdSeries(positional, *interventionCost, *asJSON)
	case "tree":
		return cmdTree(*dir, *source, selector, *at, *mode, *asJSON)
	case "treemap":
		return cmdTreemap(*dir, *source, selector, *title, *out, *asJSON)
	case "what-if", "whatif":
		return dispatchWhatIf(whatIfArgs{
			dir: *dir, source: *source, selector: selector, name: second,
			all: *all, at: *at, cut: *cut, why: *why, label: *label,
			ratio: *ratio, replayWith: *replayWith, asJSON: *asJSON,
		})
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
		// "Entire is set up here but has not recorded anything" is a
		// different problem from "Entire is not set up", and it is the one a
		// reader is most likely to be surprised by: the directory is there,
		// the settings are there, and there is still nothing to read.
		if n := entire.Checkpoints(dir); n > 0 {
			fmt.Println()
			fmt.Printf("Entire has %d checkpoints here but no transcripts. Checkpoints are\n", n)
			fmt.Println("git refs, so a clone brings them; the transcripts are files under")
			fmt.Println(".entire/metadata that are not committed and stay on the machine")
			fmt.Println("that recorded them. Every token in this tool comes from a")
			fmt.Println("transcript, so there is nothing here to account for.")
		} else if entire.Configured(dir) {
			fmt.Println()
			fmt.Println("Entire is configured in this repository but has recorded nothing")
			fmt.Println("yet. Recordings appear once a session runs with it enabled; a")
			fmt.Println("fresh clone does not carry them, because they are not committed.")
		}
		fmt.Println()
		fmt.Println("Claude Code's own transcripts need no setup at all: run this from")
		fmt.Println("inside a session and `tokenamun profile current` will work.")
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

func cmdProfile(dir, source, selector string, asJSON bool) error {
	s, err := loadSelected(dir, source, selector)
	if err != nil {
		return err
	}
	p := report.BuildProfile(s)
	if asJSON {
		return writeJSON(p)
	}
	return report.RenderText(os.Stdout, p)
}

func cmdRetrieval(dir, source, selector string, asJSON bool) error {
	s, err := loadSelected(dir, source, selector)
	if err != nil {
		return err
	}
	r := report.BuildRetrieval(s)
	if asJSON {
		return writeJSON(r)
	}
	return report.RenderRetrieval(os.Stdout, r)
}

func cmdCarry(dir, source, selector string, asJSON bool) error {
	s, err := loadSelected(dir, source, selector)
	if err != nil {
		return err
	}
	r := report.BuildCarry(s, analysis.Carry(s, analysis.Cache(s, analysis.TTL5m)))
	if asJSON {
		return writeJSON(r)
	}
	return report.RenderCarry(os.Stdout, r)
}

func cmdCache(dir, source, selector string, asJSON bool) error {
	s, err := loadSelected(dir, source, selector)
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

// cmdTree serves one level of the drill-down the HTML viewer draws.
//
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

// cmdWhatIfAll runs every intervention and prints the summary table, which is
// the one thing the HTML report showed that no command produced.
func cmdWhatIfAll(dir, source, selector string, ratio float64, replayWith string, asJSON bool) error {
	s, ctx, err := whatIfContext(dir, source, selector, ratio, replayWith)
	if err != nil {
		return err
	}
	out := report.BuildWhatIfAll(s, ctx)
	if asJSON {
		return writeJSON(out)
	}
	return report.RenderWhatIfAll(os.Stdout, out)
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
	return runIntervention(intervention, dir, source, selector, ratio, replayWith, asJSON)
}

// whatIfArgs is what the what-if command was asked for. A struct because the
// command has three forms and passing eleven positional arguments to each of
// them is how they drift apart.
type whatIfArgs struct {
	dir, source, selector, name string
	all                         bool
	at                          string
	cut                         float64
	why, label                  string
	ratio                       float64
	replayWith                  string
	asJSON                      bool
}

// dispatchWhatIf picks between the three forms: an ad-hoc slice, the summary
// of everything, and one named intervention.
func dispatchWhatIf(a whatIfArgs) error {
	// An ad-hoc intervention: a slice of the tree and how much of it goes
	// away. Everything that shrinks content is that shape, so an agent can
	// ask about one without the tool modelling the vendor.
	if a.cut > 0 {
		slice, err := report.ParseSlice(a.at, a.cut, a.label, a.why)
		if err != nil {
			return err
		}
		whatif.Register(slice)
		if a.all {
			return cmdWhatIfAll(a.dir, a.source, a.selector, a.ratio, a.replayWith, a.asJSON)
		}
		// Estimated directly rather than looked up by name: an ad-hoc
		// intervention may borrow a built-in's name, and `--name caveman`
		// should then report the slice you described rather than the
		// built-in that happens to share the label.
		return runIntervention(slice, a.dir, a.source, a.selector, a.ratio, a.replayWith, a.asJSON)
	}
	if a.all {
		return cmdWhatIfAll(a.dir, a.source, a.selector, a.ratio, a.replayWith, a.asJSON)
	}
	return cmdWhatIf(a.dir, a.source, a.selector, a.name, a.ratio, a.replayWith, a.asJSON)
}

// runIntervention estimates one intervention and renders it.
func runIntervention(i whatif.Intervention, dir, source, selector string,
	ratio float64, replayWith string, asJSON bool) error {
	s, ctx, err := whatIfContext(dir, source, selector, ratio, replayWith)
	if err != nil {
		return err
	}
	out := report.BuildWhatIf(s, i.Estimate(ctx))
	if asJSON {
		return writeJSON(out)
	}
	return report.RenderWhatIf(os.Stdout, out)
}

// whatIfContext assembles the evidence every intervention reasons over. One
// place, so a single intervention and the whole summary cannot be computed
// against different inputs.
func whatIfContext(dir, source, selector string, ratio float64, replayWith string) (
	*model.Session, whatif.Context, error) {
	if selector == "" {
		selector = "latest"
	}
	refs, err := discover(dir, source)
	if err != nil {
		return nil, whatif.Context{}, err
	}
	ref, err := selectSession(refs, selector)
	if err != nil {
		return nil, whatif.Context{}, err
	}
	s, err := ingest.Load(ref)
	if err != nil {
		return nil, whatif.Context{}, err
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
	ctx.Total = ctx.Carry.PromptCostEIT + ctx.Weights.OutputCost(s.Usage())
	if replayWith != "" {
		// Two populations, measured separately: tool output and file content
		// compress differently, and a ratio is only valid over the set it was
		// measured on. Either may legitimately be empty for a session, so
		// neither failure is fatal on its own.
		toolOutput, toolErr := whatif.Replay(ref, replayWith)
		files, fileErr := whatif.ReplayFiles(ref, replayWith)
		if toolErr != nil && fileErr != nil {
			return nil, whatif.Context{}, fmt.Errorf("replay failed: %w", toolErr)
		}
		if toolErr == nil {
			ctx.Replay = toolOutput
		}
		if fileErr == nil {
			ctx.FileReplay = files
		}
	}
	return s, ctx, nil
}

// loadSelected resolves a selector and parses the transcript it names.
func loadSelected(dir, source, selector string) (*model.Session, error) {
	refs, err := discover(dir, source)
	if err != nil {
		return nil, err
	}
	ref, err := selectSession(refs, selector)
	if err != nil {
		return nil, err
	}
	return ingest.Load(ref)
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

// repeatable is a flag that may be given more than once, collecting each
// value. Go's flag package has no such type, and an intervention path is
// exactly the kind of thing you want several of.
type repeatable []string

func (r *repeatable) String() string { return strings.Join(*r, ",") }

func (r *repeatable) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// cmdInterventions lists what can be asked of `what-if`.
//
// It exists for the agent case: something driving this tool needs to find out
// what questions it can ask without a human reading the usage text, and the
// answer changes when someone installs a script.
func cmdInterventions(asJSON bool) error {
	type row struct {
		Name    string `json:"name"`
		Targets string `json:"targets"`
		Source  string `json:"source"`
	}
	builtin := map[string]bool{}
	for _, i := range whatif.Builtin() {
		builtin[i.Name()] = true
	}
	var rows []row
	for _, i := range whatif.All() {
		src := "external"
		if builtin[i.Name()] {
			src = "built-in"
		}
		if e, ok := i.(*whatif.External); ok {
			src = e.Path
		}
		rows = append(rows, row{Name: i.Name(), Targets: i.Describe(), Source: src})
	}
	if asJSON {
		return writeJSON(rows)
	}
	fmt.Println("TOKENAMUN  interventions")
	fmt.Println()
	for _, r := range rows {
		fmt.Printf("  %-22s %s\n", r.Name, r.Targets)
		fmt.Printf("  %-22s %s\n", "", r.Source)
	}
	fmt.Println()
	fmt.Printf("Scripts are loaded from %s and from --intervention PATH.\n",
		strings.Join(whatif.SearchPath(), ", "))
	fmt.Println("See docs/interventions.md for the protocol.")
	return nil
}
