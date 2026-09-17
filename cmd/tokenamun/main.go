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
  tokenamun profile [session]     where the tokens went, and what they cost
  tokenamun retrieval [session]   what content entered the context, and from where
  tokenamun carry [session]       what it cost to keep content, not to fetch it
  tokenamun cache [session]       why the prompt cache was rebuilt, and what it cost
  tokenamun scan [path]           code properties: size, complexity, duplication
  tokenamun hotspots [session]    code properties joined against session cost
  tokenamun version

Session selector:
  a session-id prefix, or "current" for the session invoking this tool,
  or "latest" (the default) for the most recently active one.

Flags:
  --json          machine-readable output
  --dir PATH      directory to look in (default: working directory)
  --source SRC    entire | local | any (default: any)
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

	switch cmd {
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
		return cmdScan(*dir, selector, *asJSON)
	case "hotspots":
		return cmdHotspots(*dir, *source, selector, *asJSON)
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

func cmdHotspots(dir, source, selector string, asJSON bool) error {
	s, err := loadSelected(dir, source, selector)
	if err != nil {
		return err
	}
	scan, err := codescan.Scan(dir, codescan.DefaultOptions())
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
