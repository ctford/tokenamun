package content

import "strings"

// A wrapper is a command whose job is to run another command: `mise run
// check`, `pnpm exec vitest`, `npx tsc`. CommandPath stops at the subcommand,
// so every target a wrapper ran collapses into one leaf, and on a real week
// most of the cost the tree could not open up sat behind exactly that.
//
// The obvious fix is a list of wrapper names. It would work today and it is
// wrong twice: this repository does not hardcode a tool's *role*, only its
// identity, and whichever runner a team adopts next would not be on the list.
//
// So the wrapper is identified by measurement. Behind a leaf, look at the
// next token of every command line that produced it, and ask whether those
// tokens behave like a vocabulary of commands or like arguments.
//
// Two tests, and the second is the one that matters. The tokens must look
// like commands rather than paths -- LooksLikeCommand is already the
// predicate for that, and it is what keeps `go test ./...` and `cat
// docs/x.md` out. And they must *repeat*: a wrapper's targets are a small
// fixed set run over and over, where an argument is close to unique per
// call. Without the second test the rule opens up `grep`, whose second tokens
// are patterns -- high-cardinality, and "group", "air" and "and" all look
// like commands. That produced 59 children under grep once before, each
// holding one retrieval and none of them a thing anybody could act on, which
// is why hasSubcommands exists and excludes grep by hand. Repetition is the
// property that tells the two apart, and it is in the data.

// WrapperEvidence is how many calls must sit behind a leaf before its next
// tokens are evidence of anything.
//
// Below this, "the targets repeat" is a statement about three command lines.
// A leaf that small is also not the leaf anybody is trying to open.
const WrapperEvidence = 4

// WrapperRepeats is how often a wrapper's targets must recur, on average,
// across the calls behind the leaf.
//
// Two: each distinct target used at least twice. That is the difference
// between a runner's task list -- `check`, `test`, `lint`, each run many
// times -- and an argument, which is close to unique per call. A repository's
// targets are a small fixed set; a grep pattern is not.
const WrapperRepeats = 2

// Wrappers is which command prefixes a corpus of command lines showed to be
// wrappers. The zero value opens nothing, which is the right answer for a
// corpus nobody has looked at.
type Wrappers struct {
	deeper map[string]bool
}

// ObserveWrappers reads every command line in a session and decides which
// leaves are worth descending a token into.
//
// A property of the set, not of any one command line: "these tokens repeat"
// cannot be read off a single call. Observed once per report and passed down,
// so the tree and anything else asking the same question get the same answer.
func ObserveWrappers(commands []string) Wrappers {
	type evidence struct {
		calls  int
		tokens map[string]int
	}
	seen := map[string]*evidence{}
	for _, cmd := range commands {
		leaf, next, ok := leafAndNext(cmd)
		if !ok {
			continue
		}
		e := seen[leaf]
		if e == nil {
			e = &evidence{tokens: map[string]int{}}
			seen[leaf] = e
		}
		e.calls++
		// A token that does not look like a command is counted against the
		// leaf without being counted as a target, so one `mise --help` does
		// not make mise a wrapper and one path argument spoils nothing that
		// the repetition test would not already have caught.
		if LooksLikeCommand(next) {
			e.tokens[strings.ToLower(next)]++
		}
	}

	w := Wrappers{deeper: map[string]bool{}}
	for leaf, e := range seen {
		distinct := len(e.tokens)
		var named int
		for _, n := range e.tokens {
			named += n
		}
		switch {
		case e.calls < WrapperEvidence:
		case distinct < 2:
			// One target is not a split. The level would hold a single child
			// repeating its parent, which collapseEmptyLevels removes anyway.
		case named < e.calls:
			// Some of the next tokens were not commands. A wrapper's are.
		case named < distinct*WrapperRepeats:
			// A vocabulary that does not repeat is an argument list.
		default:
			w.deeper[strings.ToLower(leaf)] = true
		}
	}
	return w
}

// leafAndNext is the name CommandPath would give a command, and the token
// just past it.
func leafAndNext(cmd string) (leaf, next string, ok bool) {
	p := CommandPath(cmd)
	if len(p) == 0 {
		return "", "", false
	}
	words := commandWords(cmd, len(p)+1)
	if len(words) <= len(p) {
		return "", "", false
	}
	return p[len(p)-1], words[len(p)], true
}

// Path is CommandPath with one more level where the leaf is a wrapper, so
// `mise run` opens into `mise run check` and `npx` into `npx tsc`.
func (w Wrappers) Path(cmd string) []string {
	p := CommandPath(cmd)
	if len(p) == 0 || !w.Opens(p[len(p)-1]) {
		return p
	}
	leaf, next, ok := leafAndNext(cmd)
	if !ok || !LooksLikeCommand(next) {
		// A wrapper invoked with nothing recognisable after it stays at the
		// wrapper's own level, as a sibling of the targets it ran.
		return p
	}
	return append(p, leaf+" "+next)
}

// Opens reports whether a leaf name is one the corpus opened up. Exported so
// a caller can say why a level is there.
func (w Wrappers) Opens(leaf string) bool {
	return w.deeper[strings.ToLower(leaf)]
}
