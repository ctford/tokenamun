package report

import (
	"testing"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/model"
)

func treeFixture() *model.Session {
	return &model.Session{
		Invocations: []model.ModelInvocation{
			{Seq: 0, Model: "claude-opus-5", Usage: model.TokenUsage{
				CacheRead: 10_000, Output: 600, Thinking: 200}},
			{Seq: 1, Model: "claude-opus-5", Usage: model.TokenUsage{
				CacheRead: 11_000, Output: 400, Thinking: 100}},
			{Seq: 2, Model: "claude-opus-5", Usage: model.TokenUsage{
				CacheRead: 12_000, Output: 300}},
			{Seq: 3, Model: "claude-opus-5", Usage: model.TokenUsage{
				CacheRead: 13_000, Output: 200}},
		},
		ToolCalls: []model.ToolCall{
			{Seq: 0, ID: "t0", Name: "Bash", InvocationSeq: 0, InputBytes: 400,
				Command: "cat docs/decisions/a.md"},
			{Seq: 1, ID: "t1", Name: "Bash", InvocationSeq: 1, InputBytes: 300,
				Command: "cd /repo && git status -sb"},
			{Seq: 2, ID: "t2", Name: "Read", InvocationSeq: 2, InputBytes: 100},
			{Seq: 3, ID: "t3", Name: "mcp__github__list_issues", InvocationSeq: 3, InputBytes: 80},
		},
		PromptEntries: []model.PromptEntry{{Bytes: 500, InvocationSeq: 0}},
		ProseBytes:    1200,
		Retrievals: []model.RetrievedContent{
			{Seq: 0, ToolID: "t0", Tool: "Bash", Channel: model.ChanShell,
				CommandClass: "cat / sed / head", CommandDetail: "cat",
				Category: model.CatADR, Path: "docs/decisions/a.md",
				Bytes: 4000, Tokens: 1000, InvocationSeq: 0},
			{Seq: 1, ToolID: "t1", Tool: "Bash", Channel: model.ChanShell,
				CommandClass: "git", CommandDetail: "git status",
				Category: model.CatToolOutput, Bytes: 9000, Tokens: 2500, InvocationSeq: 1},
			{Seq: 2, ToolID: "t2", Tool: "Read", Channel: model.ChanFileRead,
				Category: model.CatSourceCode, Path: "internal/pay/charge.go",
				Bytes: 6000, Tokens: 1600, InvocationSeq: 2},
			{Seq: 3, ToolID: "t3", Tool: "mcp__github__list_issues", Channel: model.ChanMCP,
				Category: model.CatMCPOutput, Bytes: 2000, Tokens: 550, InvocationSeq: 3},
		},
		Estimator: model.TokenEstimator{BytesPerToken: 3.6, Calibrated: true},
	}
}

func built(t *testing.T) *Node {
	t.Helper()
	s := treeFixture()
	return BuildTree(s, analysis.Carry(s, analysis.Cache(s, analysis.TTL5m)))
}

func child(t *testing.T, n *Node, name string) *Node {
	t.Helper()
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	var got []string
	for _, c := range n.Children {
		got = append(got, c.Name)
	}
	t.Fatalf("no child %q under %q (have %v)", name, n.Name, got)
	return nil
}

// One axis at the top: who or what put the tokens there, because each branch
// is a different conversation -- with the harness, with yourself, with the
// model, or with the environment.
func TestTopLevelIsWhoPutTheTokensThere(t *testing.T) {
	tree := built(t)
	for _, name := range []string{
		"preamble", "your prompts", "writing output", "output carried", "tool results",
	} {
		child(t, tree, name)
	}
}

// Thinking is observed within the output total, so it gets its own block --
// but whether it is re-read afterwards is not knowable from a transcript, and
// the tree must not imply otherwise.
func TestThinkingIsSplitOutOfOutputButNotCarried(t *testing.T) {
	tree := built(t)
	writing := child(t, tree, "writing output")
	thinking := child(t, writing, "thinking")

	if thinking.Tokens != 300 {
		t.Errorf("thinking tokens = %v, want the observed 300", thinking.Tokens)
	}
	if thinking.Detail == "" || !contains(thinking.Detail, "not knowable") {
		t.Errorf("thinking must say its carry is unknowable, got %q", thinking.Detail)
	}
	// The carried side excludes it: 1500 output - 300 thinking = 1200 carried.
	carried := child(t, tree, "output carried")
	child(t, carried, "prose")
	child(t, carried, "tool arguments")
	if carried.Carry >= writing.Carry*6 {
		t.Error("carried output should not include the thinking tokens")
	}
}

// "In tool calls, I expected to see which tools."
func TestToolArgumentsBreakDownByTool(t *testing.T) {
	tree := built(t)
	args := child(t, child(t, tree, "output carried"), "tool arguments")

	bash := child(t, args, "Bash")
	child(t, args, "Read")
	child(t, args, "mcp__github__list_issues")
	if bash.Items != 2 {
		t.Errorf("Bash calls = %d, want 2", bash.Items)
	}
	// Bash wrote the most argument bytes, so it must carry the most cost.
	if bash.Carry <= child(t, args, "Read").Carry {
		t.Error("cost should follow argument bytes")
	}
}

// "In file reading, I expected to see which files."
func TestFileContentBreaksDownByFile(t *testing.T) {
	tree := built(t)
	files := child(t, child(t, tree, "tool results"), "file content")

	// However it was read: the Read tool and cat both land here.
	child(t, files, "docs/decisions/a.md")
	child(t, files, "internal/pay/charge.go")
}

// git is tool invocation, not file reading, and CLI is separated from MCP
// because that is the axis the MCP-versus-CLI argument turns on.
func TestCLIAndMCPOutputAreSeparateMechanisms(t *testing.T) {
	tree := built(t)
	results := child(t, tree, "tool results")

	cli := child(t, results, "CLI output")
	child(t, cli, "git")
	child(t, results, "MCP output")

	// File content must not be filed under CLI output.
	for _, c := range cli.Children {
		if c.Name == "file content" {
			t.Error("file content is its own mechanism, not a CLI command family")
		}
	}
}

// "If it's possible to drill down from git to git status, that'd be great."
func TestCLIOutputOpensUpBySubcommand(t *testing.T) {
	tree := built(t)
	git := child(t, child(t, child(t, tree, "tool results"), "CLI output"), "git")

	// The compound command was `cd /repo && git status -sb`, so the level is
	// the git subcommand, not cd and not the flag.
	child(t, git, "git status")
}

func TestTotalsRollUpFromTheLeaves(t *testing.T) {
	tree := built(t)
	var sum float64
	for _, c := range tree.Children {
		sum += c.Carry
	}
	if sum != tree.Carry {
		t.Errorf("children sum to %v but root says %v", sum, tree.Carry)
	}
	if tree.Carry <= 0 {
		t.Fatal("the tree should have a cost")
	}
}

func TestEveryLevelIsSortedLargestFirst(t *testing.T) {
	var check func(*Node)
	check = func(n *Node) {
		for i := 1; i < len(n.Children); i++ {
			if n.Children[i-1].Carry < n.Children[i].Carry {
				t.Errorf("%q: children out of order", n.Name)
			}
		}
		for _, c := range n.Children {
			check(c)
		}
	}
	check(built(t))
}

func TestBlocksThatCannotBeScaledAreMarked(t *testing.T) {
	// The colour ramp measures carry per token. A block with cost but no
	// attributable token count has no such rate, and rendering it at the
	// palest step would read as "cheap to keep" -- a claim we cannot make.
	tree := built(t)
	if !child(t, tree, "preamble").Unscaled {
		t.Error("the preamble has no per-token rate and must be off the ramp")
	}
	if child(t, child(t, tree, "tool results"), "file content").Unscaled {
		t.Error("file content has a real per-token rate and belongs on the ramp")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
