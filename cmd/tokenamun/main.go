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
	"github.com/ctford/tokenamun/internal/entire"
	"github.com/ctford/tokenamun/internal/ingest"
	"github.com/ctford/tokenamun/internal/model"
	"github.com/ctford/tokenamun/internal/report"
)

// version is overridden at build time with -X main.version.
var version = "dev"

const usage = `tokenamun - a profiler for coding-agent token usage

Usage:
  tokenamun sessions              list the sessions it can see
  tokenamun doctor                whether either source is set up to record here
  tokenamun profile [session]     where the tokens went, and what they cost
  tokenamun retrieval [session]   what content entered the context, and from where
  tokenamun carry [session]       what it cost to keep content, not to fetch it
  tokenamun cache [session]       why the prompt cache was rebuilt, and what it cost
  tokenamun scan [path]           code properties: size, complexity, duplication
  tokenamun hotspots [session]    code properties joined against session cost
  tokenamun compare <a> <b>       two sessions side by side
  tokenamun tree [session]        where the tokens went, one level at a time;
                                  drill in with --at. The HTML viewer as text.
  tokenamun report [session]      a standalone HTML report: the same tree as
                                  "tree", as boxes or as a table, drilling
                                  down to individual files. --json prints the
                                  report's own payload.
  tokenamun optimise [session]    what a hypothetical optimisation of part of
                                  the tree would have been worth
  tokenamun series <file>...      probe runs from an experiment: median, range, payback
  tokenamun version

Session selector:
  "all"      every session discovered, summed. With Entire this is the
             whole team's history, which is what Entire is for. Taken by
             tree, report, profile, cache and optimise; the others report
             on one session.
  "current"  the session invoking this tool
  "latest"   the most recently active (the default)
  or a session-id prefix.

Profiling a period, which is what an experiment needs:
  tokenamun tree all --since 7d           the team's last week
  tokenamun tree all --since 2026-09-16   since we changed the thing
  tokenamun tree all --until 2026-09-16   before we changed it
  tokenamun report all --since 7d -o week.html --title "Last week"

Hypothetical optimisations:
  tokenamun optimise --at "cli output" --optimise 0.5 --why "..."
  Name a part of the tree and what it becomes. The part is measured; the
  figure is yours, and so is the reason it is plausible.

Flags:
  --json          machine-readable output
  --dir PATH      directory to look in (default: working directory)
  --source SRC    entire | local | any (default: any)
  -o FILE         output file (report; default tokenamun-report.html)
  --title TEXT    heading for the report, e.g. "Payments service, last week"
  --since WHEN    only sessions active on or after WHEN: a date (2026-09-16),
                  a date and time, or an age (7d, 36h). For the before-and-
                  after question, which is what an experiment is.
  --until WHEN    only sessions active before WHEN, exclusive
  --at PATH       which node of the tree to show, e.g. "cli output/version control".
                  Names come from the level above; matching is case-insensitive.
  --mode MODE     tree pricing: carry (as billed) | uncached (as if nothing
                  cached). The difference is what prompt caching was worth.
  --optimise N    what --at becomes: 0.5 halves it, 0 removes it, 1.1 is a
                  change for the worse. Needs --why.
  --why TEXT      why that figure is plausible. Required, max 64 characters:
                  a number without it is what this tool exists to avoid.
  --name TEXT     what to call the hypothetical in the report
  --cost N        measured intervention cost in EIT, for series payback
  --scan PATH     tree to scan for code metrics (hotspots; default --dir).
                  Point this at a checkout of the branch the session ran on.

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
	out := fs.String("o", "tokenamun-report.html", "output file for the report")
	interventionCost := fs.Float64("cost", 0, "measured intervention cost in EIT, for payback")
	scanDir := fs.String("scan", "", "tree to scan for code metrics (default: --dir)")
	title := fs.String("title", "", "heading for the report")
	since := fs.String("since", "", "only sessions active on or after this date, time or age (7d)")
	until := fs.String("until", "", "only sessions active before this date, time or age")
	at := fs.String("at", "", "drill to a node in the tree, e.g. \"cli output/git\"")
	mode := fs.String("mode", "carry", "cost mode for the tree: carry | uncached")
	optimise := fs.Float64("optimise", 0, "what --at becomes: 0.5 halves it")
	why := fs.String("why", "", "caveat, required with --optimise")
	label := fs.String("name", "", "name for the hypothetical in the report")
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
	// A period to scope to, for the before-and-after question. Held in a
	// package variable rather than threaded through every command: it
	// narrows discovery, which every command shares, and passing it to each
	// of fifteen signatures would be the same global with more typing.
	if window, err = model.ParseWindow(*since, *until); err != nil {
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
	case "doctor":
		return cmdDoctor(*dir, *asJSON)
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
	// "report" names the artifact; "treemap" named one of its two views, and
	// the other one is a table. Kept as an alias because it is in muscle
	// memory and in older notes.
	case "report", "treemap":
		return cmdReport(*dir, *source, selector, *title, *out, *asJSON)
	case "what-if", "whatif", "optimise":
		// Whether --optimise was given, not just its value: zero is a
		// meaningful figure here, so the flag's default is indistinguishable
		// from the flag being forgotten. See ParseOptimisation.
		var becomes *float64
		if given(fs)["optimise"] {
			becomes = optimise
		}
		return cmdOptimise(*dir, *source, selector, optimiseArgs{
			at: *at, becomes: becomes, why: *why, label: *label,
		}, *asJSON)
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

// given names the flags that were actually passed.
//
// For most flags the zero value is answer enough: an empty --at was not
// given. --optimise is the exception, because 0 is a figure somebody might
// mean, and the whole point of the command is that the figure is the
// caller's. FlagSet.Visit accumulates across the repeated Parse calls that
// parseInterspersed makes, so it can be read once afterwards.
func given(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
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
// window scopes discovery to a period. Zero means everything.
var window model.Window

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
	refs = dedupe(refs)
	refs = model.InWindow(refs, window)
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
	p, err := profileOf(dir, source, selector)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(p)
	}
	return report.RenderText(os.Stdout, p)
}

// profileOf profiles one session or the whole set.
//
// Summed from finished per-session profiles rather than from one synthetic
// session made by concatenating them: residency does not compose across
// sessions, and a concatenated session would look like a single context to
// every analysis downstream. See report.MergeProfiles.
func profileOf(dir, source, selector string) (report.Profile, error) {
	if selector != SelectAll {
		s, err := loadSelected(dir, source, selector)
		if err != nil {
			return report.Profile{}, err
		}
		return report.BuildProfile(s), nil
	}
	sessions, info, err := loadSessions(dir, source)
	if err != nil {
		return report.Profile{}, err
	}
	profiles := make([]report.Profile, 0, len(sessions))
	for _, s := range sessions {
		profiles = append(profiles, report.BuildProfile(s))
	}
	return report.MergeProfiles(profiles, info), nil
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
	// "all" parses each transcript once in this process. Summing it by
	// shelling out per session took long enough on 131 sessions that I gave
	// up waiting, which is its own argument for the selector being
	// everywhere rather than only where it was convenient.
	if selector == SelectAll {
		sessions, info, err := loadSessions(dir, source)
		if err != nil {
			return err
		}
		var reports []analysis.CacheReport
		for _, s := range sessions {
			reports = append(reports, analysis.Cache(s, analysis.TTL5m))
		}
		r := report.BuildCacheOf(info, analysis.Merge(reports))
		if asJSON {
			return writeJSON(r)
		}
		return report.RenderCache(os.Stdout, r)
	}

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

// cmdHotspots joins code metrics onto session cost.
//

// cmdSeries aggregates previously-emitted profile JSON files.
//

// cmdTree serves one level of the drill-down the HTML viewer draws.
//

// loadSelected resolves a selector and parses the transcript it names.
func loadSelected(dir, source, selector string) (*model.Session, error) {
	// "all" is a set, and these commands answer about one session. Said
	// plainly, with what to use instead: the previous message was "no
	// session matches \"all\"", which reads as though the selector were a
	// typo when in fact the help text offers it for every command.
	if selector == SelectAll {
		return nil, fmt.Errorf("%q is a set of sessions, and this command reports on one. "+
			"Over a set: `tokenamun tree all` for where the tokens went, "+
			"`tokenamun profile all` for what they cost, `tokenamun cache all` for the "+
			"prompt cache, `tokenamun optimise all` for a hypothetical. Or name one "+
			"session: `tokenamun sessions` lists them", SelectAll)
	}
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
		if !window.Empty() {
			return model.SessionRef{}, fmt.Errorf(
				"no sessions in %s; widen --since/--until, or drop them to see everything",
				window)
		}
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

// cmdDoctor says whether the two sources are set up to record.
//
// "No sessions found" was the answer to four different problems, and the
// difference between them is the whole of what a reader needs.
func cmdDoctor(dir string, asJSON bool) error {
	checks := entire.Diagnose(dir)
	local, localErr := claudecode.DiscoverLocal(dir)

	if asJSON {
		return writeJSON(map[string]any{
			"schema_version":    1,
			"entire":            checks,
			"local_transcripts": len(local),
		})
	}

	fmt.Println("TOKENAMUN  can it read anything here?")
	fmt.Println()
	fmt.Printf("Claude Code transcripts   %s\n", localState(local, localErr))
	fmt.Println("  No setup needed: Claude Code writes these itself. This is the")
	fmt.Println("  source that works from a cold start.")
	fmt.Println()
	fmt.Println("Entire")
	for _, c := range checks {
		mark := "x"
		if c.OK {
			mark = "ok"
		}
		fmt.Printf("  %-4s %-20s %s\n", mark, c.Name, c.Found)
	}
	for _, c := range checks {
		if c.Fix != "" {
			fmt.Println()
			fmt.Printf("%s: %s\n", c.Name, c.Fix)
		}
	}
	fmt.Println()
	return nil
}

// localState says how many Claude Code transcripts were found.
func localState(refs []model.SessionRef, err error) string {
	switch {
	case err != nil:
		return "could not look: " + err.Error()
	case len(refs) == 0:
		return "none for this directory"
	case len(refs) == 1:
		return "1 session"
	default:
		return fmt.Sprintf("%d sessions", len(refs))
	}
}

// optimiseArgs is a hypothetical as the command line describes it.
type optimiseArgs struct {
	at string
	// becomes is nil when --optimise was not passed, which is not the same
	// as zero: zero removes the part entirely.
	becomes *float64
	why     string
	label   string
}

// cmdOptimise prices a hypothetical optimisation of part of the tree.
//
// All that is left of what was a table of named interventions. Everything
// that shrinks content is a part of the session and a change to it, so the
// tool measures the part and the caller names the change -- and the reason it
// is plausible, which is what --why is for.
func cmdOptimise(dir, source, selector string, a optimiseArgs, asJSON bool) error {
	o, err := report.ParseOptimisation(a.at, a.becomes, a.label, a.why)
	if err != nil {
		return err
	}
	tree, info, err := loadTree(dir, source, selector)
	if err != nil {
		return err
	}
	h, err := report.BuildHypotheticalFrom(tree, info, o)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(h)
	}
	return report.RenderHypothetical(os.Stdout, h)
}

// dedupe keeps one ref per session id.
//
// A repository with Entire recordings AND local Claude Code transcripts has
// both copies of the same session, and `sessions` listed each of them twice
// without anyone questioning it. That was cosmetic until `period` started
// summing: eight of seventeen sessions in one repository appeared twice, so
// the week's total was inflated by counting them both.
//
// The local transcript wins. Both are the same session, but Entire's copy is
// a snapshot taken at a checkpoint while the local file is appended to until
// the session ends, so local is the more complete of the two. Where Entire is
// the only source -- a clone, where the local transcripts stayed on the
// machine that recorded them -- it is used unchanged.
func dedupe(refs []model.SessionRef) []model.SessionRef {
	best := map[string]model.SessionRef{}
	var order []string
	for _, r := range refs {
		prev, seen := best[r.ID]
		if !seen {
			best[r.ID] = r
			order = append(order, r.ID)
			continue
		}
		if prev.Origin != model.FromLocal && r.Origin == model.FromLocal {
			best[r.ID] = r
		}
	}
	out := make([]model.SessionRef, 0, len(order))
	for _, id := range order {
		out = append(out, best[id])
	}
	return out
}
