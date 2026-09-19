package content

import (
	"strings"
	"testing"
)

// repeat is a corpus of one command line run n times, which is how a task
// runner's targets actually appear.
func repeat(n int, cmds ...string) []string {
	var out []string
	for range n {
		out = append(out, cmds...)
	}
	return out
}

func TestAWrapperIsRecognisedFromItsTargetsRepeating(t *testing.T) {
	cases := []struct {
		name   string
		corpus []string
		leaf   string
		opens  bool
		why    string
	}{
		{
			name:   "a task runner",
			corpus: repeat(3, "mise run check", "mise run test", "mise run lint"),
			leaf:   "mise run", opens: true,
			why: "three targets, each run three times: a vocabulary",
		},
		{
			name:   "a runner with no subcommand of its own",
			corpus: repeat(3, "npx tsc", "npx vitest", "npx eslint"),
			leaf:   "npx", opens: true,
			why: "npx has no subcommands, so the target is its very next token",
		},
		{
			name: "grep, whose second token is a pattern",
			corpus: []string{"grep group x", "grep air x", "grep and x", "grep now x",
				"grep then x", "grep alpha x"},
			leaf: "grep", opens: false,
			why: "six patterns over six calls, each used once: an argument, not a target",
		},
		{
			name:   "a command whose next tokens are paths",
			corpus: repeat(4, "go test ./internal/report", "go test ./internal/content"),
			leaf:   "go test", opens: false,
			why: "the next tokens are paths, and LooksLikeCommand refuses them",
		},
		{
			name:   "too few calls to be evidence of anything",
			corpus: []string{"mise run check", "mise run test", "mise run lint"},
			leaf:   "mise run", opens: false,
			why: "three calls is three command lines, not a pattern",
		},
		{
			name:   "one target, run many times",
			corpus: repeat(8, "mise run check"),
			leaf:   "mise run", opens: false,
			why: "a level with one child repeating its parent is not a level",
		},
		{
			name: "targets that mostly repeat but include a stray path",
			corpus: append(repeat(4, "pnpm exec vitest", "pnpm exec tsc"),
				"pnpm exec ./scripts/thing.sh"),
			leaf: "pnpm exec", opens: false,
			why: "a next token that is not a command means these are not all targets",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := ObserveWrappers(c.corpus)
			if got := w.Opens(c.leaf); got != c.opens {
				t.Errorf("Opens(%q) is %v, want %v -- %s", c.leaf, got, c.opens, c.why)
			}
		})
	}
}

func TestAWrapperOpensIntoItsTarget(t *testing.T) {
	w := ObserveWrappers(repeat(3, "mise run check", "mise run test", "mise run lint"))
	got := w.Path("mise run check")
	want := []string{"mise", "mise run", "mise run check"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("path is %v, want %v", got, want)
	}
	// Unchanged for anything the corpus did not identify.
	if got := w.Path("git status"); strings.Join(got, "|") != "git|git status" {
		t.Errorf("git should be untouched, got %v", got)
	}
	// And a wrapper invoked with nothing usable after it stays at its own
	// level rather than inventing one.
	if got := w.Path("mise run"); strings.Join(got, "|") != "mise|mise run" {
		t.Errorf("a bare wrapper should not descend, got %v", got)
	}
}

// The zero value is what a caller that never observed anything gets, and it
// must open nothing: descending on no evidence is the failure this is built
// to avoid.
func TestAnUnobservedCorpusOpensNothing(t *testing.T) {
	var w Wrappers
	if w.Opens("mise run") {
		t.Error("the zero value opened a leaf")
	}
	if got := w.Path("mise run check"); strings.Join(got, "|") != "mise|mise run" {
		t.Errorf("the zero value should behave as CommandPath, got %v", got)
	}
	if got := ObserveWrappers(nil).Path("mise run check"); strings.Join(got, "|") != "mise|mise run" {
		t.Errorf("an empty corpus should behave as CommandPath, got %v", got)
	}
}

// The corpus is a session's command lines, which are compound shells with
// pipes and heredocs in them. The wrapper has to be found in the stage that
// produced the output, the same way CommandPath finds it.
func TestWrappersAreFoundInsideCompoundCommands(t *testing.T) {
	w := ObserveWrappers(repeat(3,
		"cd /repo && mise run check",
		"time mise run test 2>&1",
		"mise run lint | head -20",
	))
	if !w.Opens("mise run") {
		t.Error("a wrapper inside a compound command should still be found")
	}
}
