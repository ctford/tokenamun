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
				CommandClass: "cat / sed / head", CommandDetail: "cat", CommandBinary: "cat",
				Category: model.CatADR, Path: "docs/decisions/a.md",
				Bytes: 4000, Tokens: 1000, InvocationSeq: 0},
			{Seq: 1, ToolID: "t1", Tool: "Bash", Channel: model.ChanShell,
				CommandClass: "git", CommandDetail: "git status", CommandBinary: "git",
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
	// File reading is the first thing anyone looks for, so it is top-level
	// rather than nested under an authorship parent.
	for _, name := range []string{
		"preamble", "your prompts", "model output",
		"file content", "CLI output",
	} {
		child(t, tree, name)
	}
}

// Thinking is observed within the output total, so it gets its own block --
// but whether it is re-read afterwards is not knowable from a transcript, and
// the tree must not imply otherwise.
func TestThinkingIsSplitOutOfOutputButNotCarried(t *testing.T) {
	tree := built(t)
	out := child(t, tree, "model output")
	thinking := child(t, out, "thinking")

	if thinking.Tokens != 300 {
		t.Errorf("thinking tokens = %v, want the observed 300", thinking.Tokens)
	}
	if thinking.Detail == "" || !contains(thinking.Detail, "not knowable") {
		t.Errorf("thinking must say its carry is unknowable, got %q", thinking.Detail)
	}
	// Prose and tool arguments each combine what they cost to write with what
	// they cost to keep, since they are the same text.
	child(t, out, "prose")
	child(t, out, "tool arguments")
}

// "In tool calls, I expected to see which tools."
func TestToolArgumentsBreakDownByTool(t *testing.T) {
	tree := built(t)
	args := child(t, child(t, tree, "model output"), "tool arguments")

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

// "In file reading, I expected to see which files." Nested by directory, the
// way a disk-usage viewer does, so an area of the tree can be read before
// drilling to individual files.
func TestFileContentNestsByDirectory(t *testing.T) {
	tree := built(t)
	files := child(t, tree, "file content")

	// However it was read: the Read tool and cat both land here. Chains of
	// single-child directories collapse, so this is docs/decisions rather
	// than docs, then decisions.
	child(t, child(t, files, "docs/decisions"), "a.md")
	child(t, child(t, files, "internal/pay"), "charge.go")
}

// A path attributed to a directory rather than a file means the command used
// a glob, and the view has to say so: otherwise a directory sits beside files
// looking like one of them.
func TestDirectoryReadsAreLabelledAsDirectories(t *testing.T) {
	s := treeFixture()
	s.Retrievals = append(s.Retrievals, model.RetrievedContent{
		Seq: 9, ToolID: "t9", Tool: "Bash", Channel: model.ChanShell,
		CommandBinary: "cat", CommandDetail: "cat", Category: model.CatADR,
		Path: "docs/decisions", Bytes: 3000, Tokens: 830, InvocationSeq: 0,
	})
	tree := BuildTree(s, analysis.Carry(s, analysis.Cache(s, analysis.TTL5m)))
	decisions := child(t, child(t, tree, "file content"), "docs/decisions")

	var found bool
	for _, c := range decisions.Children {
		if contains(c.Name, "read as a directory") {
			found = true
		}
	}
	if !found {
		t.Errorf("a glob read of a directory must be labelled, got %v", names(decisions))
	}
}

func names(n *Node) []string {
	var out []string
	for _, c := range n.Children {
		out = append(out, c.Name)
	}
	return out
}

// git is tool invocation, not file reading, and CLI is separated from MCP
// because that is the axis the MCP-versus-CLI argument turns on. Tool groups
// are by identity, which is industry-stable, never by role in a project,
// which is not.
func TestCLIAndMCPOutputAreSeparateMechanisms(t *testing.T) {
	tree := built(t)
	cli := child(t, tree, "CLI output")
	// Tools are grouped by what they are -- git is version control in every
	// codebase -- and the binary is a level inside that.
	child(t, child(t, cli, "version control"), "git")
	child(t, tree, "MCP output")

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
	git := child(t, child(t, child(t, tree, "CLI output"), "version control"), "git")

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
	if child(t, tree, "file content").Unscaled {
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

// A level can hold both leaves and branches: file content holds individual
// files alongside a "path not attributed" branch. Collapsing only the first
// kind left eighteen separate rows all called "sed" in a branch that was
// never reached.
func TestRepeatedLeavesMergeEvenBesideBranches(t *testing.T) {
	s := treeFixture()
	// Three unattributed sed reads and two of the same file, at one level.
	s.Retrievals = append(s.Retrievals,
		model.RetrievedContent{Seq: 4, ToolID: "t4", Tool: "Bash", Channel: model.ChanShell,
			CommandBinary: "sed", CommandDetail: "sed", Category: model.CatToolOutput,
			Bytes: 1000, Tokens: 280, InvocationSeq: 0},
		model.RetrievedContent{Seq: 5, ToolID: "t5", Tool: "Bash", Channel: model.ChanShell,
			CommandBinary: "sed", CommandDetail: "sed", Category: model.CatToolOutput,
			Bytes: 2000, Tokens: 550, InvocationSeq: 1},
		model.RetrievedContent{Seq: 6, ToolID: "t6", Tool: "Bash", Channel: model.ChanShell,
			CommandBinary: "sed", CommandDetail: "sed", Category: model.CatToolOutput,
			Bytes: 3000, Tokens: 830, InvocationSeq: 2},
		model.RetrievedContent{Seq: 7, ToolID: "t7", Tool: "Read", Channel: model.ChanFileRead,
			Category: model.CatADR, Path: "docs/decisions/a.md",
			Bytes: 4000, Tokens: 1000, InvocationSeq: 2},
	)
	tree := BuildTree(s, analysis.Carry(s, analysis.Cache(s, analysis.TTL5m)))
	files := child(t, tree, "file content")

	// The branch is still there and its repeated leaves have merged.
	unattributed := child(t, files, "path not attributed")
	sed := child(t, unattributed, "sed")
	if sed.Items != 3 {
		t.Errorf("sed rows = %d, want one row covering 3 retrievals", sed.Items)
	}
	// And the files beside that branch merged too, before being nested.
	adr := child(t, child(t, files, "docs/decisions"), "a.md")
	if adr.Items != 2 {
		t.Errorf("repeated file rows = %d, want one row covering 2 retrievals", adr.Items)
	}
}
