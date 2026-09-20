package main

// The help text, as a table rather than a block of prose.
//
// It is the whole of the documentation an agent driving this tool has: there
// is no README in its context and it cannot read docs/. Two things follow.
// The index says what question each command answers, rather than what the
// command is, because choosing between fifteen commands is the step an agent
// has to get right first. And every command has a page of its own, reachable
// as `tokenamun help <command>`, so the index can stay short enough to read.
//
// A page lists the flags that command takes and no others. Go's flag package
// prints every flag in the binary for -h, so `tokenamun tree -h` offered
// --max-complexity and --cost, both of which tree refuses -- a list of
// options that are not options is worse than no list.

import (
	"fmt"
	"sort"
	"strings"
)

// command is one entry in the index and the page behind it.
type command struct {
	name     string
	also     []string // accepted aliases
	args     string
	answers  string   // the question it answers, for the index
	detail   string   // the page, when the one-liner is not enough
	flags    []string // flags it takes, beyond the common ones
	common   []string // overrides commonFlags where a command reads no session
	takesAll bool     // whether the "all" selector composes here
	examples []string
}

// selectAllTakers names the commands that accept "all", built from the table
// so the help and the refusal message cannot disagree about which they are.
func selectAllTakers() []string {
	var names []string
	for _, c := range commands {
		if c.takesAll {
			names = append(names, c.name)
		}
	}
	return names
}

var commands = []command{{
	name:    "doctor",
	answers: "can it read anything here? start here",
	common:  []string{"json", "dir"},
	detail: "Says which of the two sources is set up to record in this " +
		"directory, and how far back the transcripts reach. " +
		"\"No sessions found\" is the answer to four different problems; " +
		"this is what tells them apart.",
}, {
	name:    "sessions",
	answers: "what sessions are there, and what each one cost",
	flags:   []string{"sort"},
	examples: []string{
		"tokenamun sessions --sort cost",
	},
}, {
	name:     "profile",
	args:     "[session]",
	answers:  "where did the tokens go, and what did they cost",
	flags:    []string{"prices"},
	takesAll: true,
	examples: []string{
		"tokenamun profile current",
		"tokenamun profile all --since 7d --prices",
	},
}, {
	name:     "tree",
	args:     "[session]",
	answers:  "where did the tokens go, one level at a time",
	detail:   "The HTML report as text. Drill into a node with --at.",
	flags:    []string{"at", "mode", "prices"},
	takesAll: true,
	examples: []string{
		"tokenamun tree",
		`tokenamun tree --at "cli output/version control"`,
		"tokenamun tree all --since 7d",
	},
}, {
	name:    "retrieval",
	args:    "[session]",
	answers: "what content entered the context, and from where",
}, {
	name:     "carry",
	args:     "[session]",
	answers:  "which retrievals cost the most to keep, worst first",
	detail:   "Carry is what a retrieval cost over every later call that re-sent it.",
	takesAll: true,
}, {
	name:     "cache",
	args:     "[session]",
	answers:  "why was the prompt cache rebuilt, and what did that cost",
	flags:    []string{"prices"},
	takesAll: true,
}, {
	name:    "length",
	answers: "does a call cost more in a longer session",
	detail: "Bins every session in the window by how many calls it made. No " +
		"selector: one session has no distribution in it.",
}, {
	name:     "compare",
	args:     "<a> <b>",
	answers:  "two sessions side by side",
	detail:   "Each is an id prefix, which `tokenamun sessions` lists.",
	examples: []string{"tokenamun compare 3f9a 71c0"},
}, {
	name:    "scan",
	args:    "[path]",
	answers: "code properties: size, complexity, duplication",
	detail: "Given a budget flag, exits non-zero when it is exceeded, which " +
		"is how it is used as a CI gate.",
	flags: []string{"max-file-lines", "max-complexity", "max-duplication",
		"skip-duplicates-in"},
	common: []string{"json", "dir"},
	examples: []string{
		"tokenamun scan . --max-file-lines 800 --max-complexity 25",
	},
}, {
	name:    "hotspots",
	args:    "[session]",
	answers: "which code is both expensive to read and hard to read",
	detail:  "Joins scan's code properties onto what the session spent reading each file.",
	flags:   []string{"scan"},
}, {
	name:     "report",
	also:     []string{"treemap"},
	args:     "[session]",
	answers:  "a standalone HTML report of the same tree",
	detail:   "Boxes or a table, drilling down to individual files. --json prints its payload.",
	flags:    []string{"o", "title"},
	takesAll: true,
	examples: []string{
		`tokenamun report all --since 7d -o week.html --title "Last week"`,
	},
}, {
	name:     "optimise",
	also:     []string{"what-if", "whatif"},
	args:     "[session]",
	answers:  "what would a hypothetical change have been worth",
	takesAll: true,
	detail: "Name a part of the tree and what it becomes. The part is " +
		"measured; the figure is yours, and so is the reason it is " +
		"plausible. Repeat the trio to price several changes at once -- " +
		"they read as columns of one table, and the parts must not " +
		"contain one another.",
	flags: []string{"at", "optimise", "why", "name"},
	examples: []string{
		`tokenamun optimise --at "cli output" --optimise 0.5 --why "trim the diffs"`,
		`tokenamun optimise --at "cli output" --optimise 0.5 --why "..." \`,
		`                   --at "your prompts" --optimise 0.8 --why "..."`,
	},
}, {
	name:    "series",
	args:    "<file>...",
	answers: "probe runs from an experiment: median, range, payback",
	detail:  "The files are profile JSON, one per probe run: `tokenamun profile --json > step-1.json`.",
	flags:   []string{"cost"},
	common:  []string{"json"},
}, {
	name:    "version",
	answers: "the version, and the formats the adapters were read off",
	common:  []string{},
}}

// commonFlags are taken by every command that reads a transcript, so they are
// listed once in the index rather than on fifteen pages.
var commonFlags = []string{"json", "dir", "source", "since", "until", "no-cache"}

var flagHelp = map[string]string{
	"json": "machine-readable output. It has a schema_version and golden " +
		"tests, which is what makes it the interface to script against.",
	"prices": "also total it in money, from a pinned published catalog. For " +
		"a total that spans two models, where EIT adds different-sized things.",
	"no-cache": "re-read every transcript, ignoring the parse cache",
	"sort":     "order for sessions: recent (default) | cost | calls",
	"dir":      "directory to look in (default: working directory)",
	"source":   "entire | local | any (default: any)",
	"o":        "output file (default tokenamun-report.html)",
	"title":    `heading for the report, e.g. "Payments service, last week"`,
	"since": "only sessions active on or after WHEN: a date (2026-09-16), a " +
		"date and time, or an age (7d, 36h)",
	"until": "only sessions active before WHEN, exclusive",
	"at": `which node of the tree, e.g. "cli output/version control". Names ` +
		"come from the level above; matching is case-insensitive.",
	"mode": "tree pricing: carry (as billed) | uncached (as if nothing " +
		"cached). The difference is what prompt caching was worth.",
	"optimise": "what the matching --at becomes: 0.5 halves it, 0 removes it, " +
		"1.1 is a change for the worse. Needs --why. Repeatable.",
	"why": "why that figure is plausible. Required, one per --optimise, max " +
		"64 characters: a number without it is what this tool exists to avoid.",
	"name": "what to call the hypothetical in the report",
	"cost": "measured intervention cost in EIT, for series payback",
	"scan": "tree to scan for code metrics (default --dir). Point it at a " +
		"checkout of the branch the session ran on.",
	"max-file-lines":     "fail on a file longer than N lines",
	"max-complexity":     "fail on a function above complexity N",
	"max-duplication":    "fail above PCT% of duplicated code lines",
	"skip-duplicates-in": "exclude paths containing S from the duplication measure; repeatable",
}

// flagArg names the value a flag takes, where it takes one.
var flagArg = map[string]string{
	"sort": "ORDER", "dir": "PATH", "source": "SRC", "o": "FILE",
	"title": "TEXT", "since": "WHEN", "until": "WHEN", "at": "PATH",
	"mode": "MODE", "optimise": "N", "why": "TEXT", "name": "TEXT",
	"cost": "N", "scan": "PATH", "max-file-lines": "N",
	"max-complexity": "N", "max-duplication": "PCT", "skip-duplicates-in": "S",
}

// usage is the index: one line per command, then the things that are true of
// all of them. Everything command-specific lives on the command's own page,
// because an index nobody finishes reading has chosen nothing for anybody.
func usage() string {
	var b strings.Builder
	b.WriteString(`tokenamun - a profiler for coding-agent token usage.

It reads transcripts that Entire and Claude Code already wrote. It records
nothing itself, so there is nothing to set up.

  tokenamun <command> [session] [flags]
  tokenamun help <command>      how to use one command: its flags and examples

Commands
`)
	for _, c := range commands {
		b.WriteString(entry("  "+c.name+" "+c.args, c.answers, 26))
	}
	b.WriteString(`
Session, where a command takes one
  latest (default) | current (the session invoking this tool) | an id prefix,
  which ` + "`tokenamun sessions`" + ` lists. "all" sums every session discovered and
  is taken by: ` + strings.Join(selectAllTakers(), ", ") + `.

Common flags, taken by every command that reads a transcript
`)
	for _, f := range commonFlags {
		b.WriteString(entry("  "+flagName(f), flagHelp[f], 26))
	}
	b.WriteString(`
Reading the output
`)
	b.WriteString(entry("  EIT", "cost-weighted tokens: one full-price input "+
		"token of that model. A cache read costs 0.1, an output token 5. "+
		"Volume is not cost, so rank on EIT and never on raw tokens.", 26))
	b.WriteString(entry("  [label]", "where a number came from: observed, "+
		"derived, derived-approx, inferred, counterfactual, given. Carry the "+
		"label when you quote the number.", 26))
	b.WriteString("\n" + entry("  ", "Retrieved-content tokens and billed "+
		"tokens are different quantities, and are never added together. "+
		`"Not measurable from this data" is an answer rather than a failure: `+
		"it means the evidence is not in the transcript.", 2))
	return b.String()
}

// helpFor is one command's page.
func helpFor(name string) (string, bool) {
	c, ok := lookup(name)
	if !ok {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n  %s\n", strings.TrimSpace("tokenamun "+c.name+" "+c.args), c.answers)
	var notes []string
	if c.detail != "" {
		notes = append(notes, c.detail)
	}
	if c.takesAll {
		notes = append(notes, `Takes the "all" selector: `+
			"`tokenamun "+c.name+" all` sums every session discovered.")
	}
	for _, n := range notes {
		b.WriteString("\n" + entry("  ", n, 2))
	}
	if len(c.also) > 0 {
		fmt.Fprintf(&b, "\nAlso accepted as: %s\n", strings.Join(c.also, ", "))
	}
	if len(c.flags) > 0 {
		b.WriteString("\nFlags\n")
		for _, f := range c.flags {
			b.WriteString(entry("  "+flagName(f), flagHelp[f], 26))
		}
	}
	// Which common flags apply is part of the page, because a command that
	// reads no session honours none of them and offering --since there would
	// be a filter that silently does nothing.
	if common := c.commonFlags(); len(common) > 0 {
		b.WriteString("\n" + entry("  ", "It also takes "+
			strings.Join(namesOf(common), " ")+", described by `tokenamun help`.", 2))
	}
	if len(c.examples) > 0 {
		b.WriteString("\nExamples\n")
		for _, e := range c.examples {
			b.WriteString("  " + e + "\n")
		}
	}
	return b.String(), true
}

// commonFlags is the shared set, or the narrower one a command declares.
func (c command) commonFlags() []string {
	if c.common != nil {
		return c.common
	}
	return commonFlags
}

// lookup resolves a command name or one of its aliases.
func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
		for _, a := range c.also {
			if a == name {
				return c, true
			}
		}
	}
	return command{}, false
}

// commandNames lists every name and alias, sorted, for the unknown-command
// error. An agent that guessed wrong is one line away from guessing right.
func commandNames() []string {
	var names []string
	for _, c := range commands {
		names = append(names, c.name)
	}
	sort.Strings(names)
	return names
}

func namesOf(flags []string) []string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		out = append(out, "--"+f)
	}
	return out
}

// flagName renders a flag with the value it takes. -o is the one short flag.
func flagName(f string) string {
	dash := "--"
	if len(f) == 1 {
		dash = "-"
	}
	if arg, ok := flagArg[f]; ok {
		return dash + f + " " + arg
	}
	return dash + f
}

// helpWidth is where the text wraps. 78 so that a copied line survives a
// terminal at 80 and a diff that indents it.
const helpWidth = 78

// entry renders one two-column row: a label, then text broken to fit, with
// continuation lines aligned under the first. A label wider than the column
// takes a line of its own rather than pushing the text off the right edge.
func entry(label, text string, col int) string {
	var b strings.Builder
	pad := strings.Repeat(" ", col)
	if strings.TrimSpace(label) != "" && len(label) >= col {
		b.WriteString(label + "\n")
		label = ""
	}
	line := label + pad[len(label):]
	for _, word := range strings.Fields(text) {
		if len(line)+1+len(word) > helpWidth && len(strings.TrimSpace(line)) > 0 {
			b.WriteString(strings.TrimRight(line, " ") + "\n")
			line = pad
		}
		line += word + " "
	}
	return b.String() + strings.TrimRight(line, " ") + "\n"
}
