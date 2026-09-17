package report

import (
	"encoding/json"
	"strings"
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
				CommandClass: "cat / sed / head", CommandDetail: "cat", CommandBinary: "cat", Path: "docs/decisions/a.md",
				Bytes: 4000, Tokens: 1000, InvocationSeq: 0},
			{Seq: 1, ToolID: "t1", Tool: "Bash", Channel: model.ChanShell,
				CommandClass: "git", CommandDetail: "git status", CommandBinary: "git", Bytes: 9000, Tokens: 2500, InvocationSeq: 1},
			{Seq: 2, ToolID: "t2", Tool: "Read", Channel: model.ChanFileRead, Path: "internal/pay/charge.go",
				Bytes: 6000, Tokens: 1600, InvocationSeq: 2},
			{Seq: 3, ToolID: "t3", Tool: "mcp__github__list_issues", Channel: model.ChanMCP, Bytes: 2000, Tokens: 550, InvocationSeq: 3},
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
		"file content", "cli output",
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
	// The tooltip says only the writing is priced; why is in the long form,
	// where there is room for it.
	if !contains(thinking.Detail, "writing is priced") {
		t.Errorf("thinking must say only the writing is priced, got %q", thinking.Detail)
	}
	if !contains(thinking.DetailMore, "not knowable") {
		t.Errorf("thinking must say its carry is unknowable, got %q", thinking.DetailMore)
	}
	// Prose and tool inputs each combine what they cost to write with what
	// they cost to keep, since they are the same text.
	child(t, out, "replies")
	child(t, out, "tool inputs")
}

// "In tool calls, I expected to see which tools."
func TestToolArgumentsBreakDownByTool(t *testing.T) {
	tree := built(t)
	args := child(t, child(t, tree, "model output"), "tool inputs")

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
		CommandBinary: "cat", CommandDetail: "cat", Path: "docs/decisions", Bytes: 3000, Tokens: 830, InvocationSeq: 0,
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
	cli := child(t, tree, "cli output")
	// Tools are grouped by what they are -- git is version control in every
	// codebase -- and the binary is a level inside that. This fixture runs
	// only git, so its group holds one tool and is collapsed away; the
	// grouping itself is covered by TestALevelThatTeachesNothingIsRemoved.
	child(t, cli, "git")
	child(t, tree, "mcp output")

	// File content must not be filed under cli output.
	for _, c := range cli.Children {
		if c.Name == "file content" {
			t.Error("file content is its own mechanism, not a CLI command family")
		}
	}
}

// "If it's possible to drill down from git to git status, that'd be great."
func TestCLIOutputOpensUpBySubcommand(t *testing.T) {
	tree := built(t)
	git := child(t, child(t, tree, "cli output"), "git")

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

func TestOnlyBlocksWithNoRoundTripFigureAreOffTheRamp(t *testing.T) {
	// Grey means one thing: we cannot say how many round trips this content
	// made. It does not mean "leaf", and it does not mean "no cost" --
	// conflating those was what made a drillable "tool inputs" box grey.
	//
	// The preamble, what you typed and the model's own words all have
	// observable residency, so all three are on the ramp. Thinking is not:
	// Claude Code records thinking blocks with empty text, so whether they go
	// round again is not in the transcript.
	tree := built(t)
	for _, name := range []string{"preamble", "your prompts", "file content"} {
		n := child(t, tree, name)
		if n.Unscaled {
			t.Errorf("%s has an observable residency and belongs on the ramp", name)
		}
		if n.RoundTrips <= 0 {
			t.Errorf("%s is on the ramp but has no round-trip figure", name)
		}
	}

	// And nothing on the ramp may be missing the quantity the ramp encodes,
	// anywhere in the tree, or it would draw at the palest step and read as
	// "went round once".
	var walk func(*Node)
	walk = func(n *Node) {
		if !n.Unscaled && n.Tokens > 0 && n.RoundTrips <= 0 {
			t.Errorf("%q is on the ramp with no round trips", n.Name)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(tree)
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
			CommandBinary: "sed", CommandDetail: "sed", Bytes: 1000, Tokens: 280, InvocationSeq: 0},
		model.RetrievedContent{Seq: 5, ToolID: "t5", Tool: "Bash", Channel: model.ChanShell,
			CommandBinary: "sed", CommandDetail: "sed", Bytes: 2000, Tokens: 550, InvocationSeq: 1},
		model.RetrievedContent{Seq: 6, ToolID: "t6", Tool: "Bash", Channel: model.ChanShell,
			CommandBinary: "sed", CommandDetail: "sed", Bytes: 3000, Tokens: 830, InvocationSeq: 2},
		model.RetrievedContent{Seq: 7, ToolID: "t7", Tool: "Read", Channel: model.ChanFileRead, Path: "docs/decisions/a.md",
			Bytes: 4000, Tokens: 1000, InvocationSeq: 2},
	)
	tree := BuildTree(s, analysis.Carry(s, analysis.Cache(s, analysis.TTL5m)))
	files := child(t, tree, "file content")

	// The branch is still there and its repeated leaves have merged.
	unidentified := child(t, files, "unidentified files")
	sed := child(t, unidentified, "read via sed")
	if sed.Items != 3 {
		t.Errorf("sed rows = %d, want one row covering 3 retrievals", sed.Items)
	}
	// And the files beside that branch merged too, before being nested.
	adr := child(t, child(t, files, "docs/decisions"), "a.md")
	if adr.Items != 2 {
		t.Errorf("repeated file rows = %d, want one row covering 2 retrievals", adr.Items)
	}
}

func TestAGroupWithTooFewToolsIsHoistedAway(t *testing.T) {
	// A group's job is to stop a handful of small rows crowding out a big
	// one. Three rows is not a crowd: "language toolchains" holding pnpm, go
	// and npm was a word you clicked through to learn that you ran pnpm.
	root := &Node{Name: "cli output", Kind: "mechanism", Children: []*Node{
		{Name: "language toolchains", Kind: "group", Children: []*Node{
			{Name: "pnpm", Kind: "command", Carry: 7, Items: 1},
			{Name: "go", Kind: "command", Carry: 3, Items: 1},
			{Name: "npm", Kind: "command", Carry: 1, Items: 1},
		}},
		{Name: "standard unix tools", Kind: "group", Children: []*Node{
			{Name: "grep", Kind: "command", Carry: 5, Items: 1},
			{Name: "find", Kind: "command", Carry: 4, Items: 1},
			{Name: "head", Kind: "command", Carry: 3, Items: 1},
			{Name: "tail", Kind: "command", Carry: 2, Items: 1},
		}},
	}}
	collapseEmptyLevels(root)

	var names []string
	for _, c := range root.Children {
		names = append(names, c.Name)
	}
	// The small group's members are hoisted; the group that earns its place
	// survives whole.
	want := []string{"pnpm", "go", "npm", "standard unix tools"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", names, want)
	}
	if got := len(root.Children[3].Children); got != 4 {
		t.Errorf("standard unix tools should keep its four tools, got %d", got)
	}
	// Nothing is lost by hoisting: the members are still there, at the level
	// above, and the branch still totals what its leaves do.
	rollUp(root)
	if root.Carry != 7+3+1+5+4+3+2 {
		t.Errorf("hoisting changed the total: %.0f", root.Carry)
	}
}

func TestALevelThatTeachesNothingIsRemoved(t *testing.T) {
	// The tool-identity taxonomy is fixed and industry-wide, but whether a
	// given session exercised enough of a group for the group to be worth
	// showing is a property of that session. "version control" containing
	// nothing but git is a level you click through to learn a word you
	// already knew, and it pushes the drill-down that matters -- git, then
	// git status -- one click further away.
	root := &Node{Name: "session", Kind: "root", Children: []*Node{{
		Name: "cli output", Kind: "mechanism", Children: []*Node{
			{Name: "version control", Kind: "group", Children: []*Node{
				{Name: "git", Kind: "command", Carry: 10, Children: []*Node{
					{Name: "status", Kind: "command", Carry: 4, Items: 1},
					{Name: "log", Kind: "command", Carry: 6, Items: 1},
				}},
			}},
			{Name: "standard unix tools", Kind: "group", Children: []*Node{
				{Name: "grep", Kind: "command", Carry: 3, Items: 1},
				{Name: "sed", Kind: "command", Carry: 2, Items: 1},
			}},
			// An unrecognised binary sits at the same level and is a tool,
			// not a group. Removing it because it only ran one thing would
			// lose the fact that the one thing was run through it.
			{Name: "npx", Kind: "command", Children: []*Node{
				{Name: "vitest", Kind: "command", Carry: 1, Items: 1},
			}},
		},
	}}}
	collapseEmptyLevels(root)

	cli := root.Children[0]
	var names []string
	for _, c := range cli.Children {
		names = append(names, c.Name)
	}
	want := []string{"git", "grep", "sed", "npx"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("child %d: got %q, want %q", i, names[i], w)
		}
	}
	// The collapse must keep what was inside, not just the heading.
	if got := len(cli.Children[0].Children); got != 2 {
		t.Errorf("git should still hold its subcommands, got %d", got)
	}
	if cli.Children[1].Name != "grep" {
		t.Error("a group below the threshold is hoisted, members and all")
	}

	// The other kind: a node whose only child repeats its name is the same
	// row twice, not a hierarchy.
	dup := &Node{Name: "cli output", Kind: "mechanism", Children: []*Node{
		{Name: "git add", Kind: "command", Children: []*Node{
			{Name: "git add", Kind: "item", Carry: 9, Items: 73},
		}},
	}}
	collapseEmptyLevels(dup)
	only := dup.Children[0]
	if len(only.Children) != 0 {
		t.Errorf("git add should be a leaf, it has %d children", len(only.Children))
	}
	if only.Items != 73 {
		t.Errorf("the collapse lost the retrievals: %d", only.Items)
	}
}

func TestToolArgumentsAreSeparateFromWhatToolsPrintedBack(t *testing.T) {
	// Two sides of the same tool call, and they are different token pools.
	// What the model wrote to invoke a tool is generated at the output rate
	// and then re-read; what the tool printed back is input only. Filing the
	// arguments beside cli output would group by "anything to do with tools"
	// and cross the authorship axis the top level is built on.
	tree := built(t)
	args := child(t, child(t, tree, "model output"), "tool inputs")
	cli := child(t, tree, "cli output")

	for _, c := range cli.Children {
		if c.Name == "tool inputs" {
			t.Error("tool inputs belong to the model, not to the environment")
		}
	}
	if args.Tokens <= 0 || cli.Tokens <= 0 {
		t.Fatal("both sides should have content in this fixture")
	}
	// And they must not be the same tokens counted twice. The root
	// reconciles against the measured bill, so an overlap would show up
	// there; this asserts the pools directly.
	if args.Tokens == cli.Tokens {
		t.Error("the two sides have identical token counts, which suggests one pool")
	}
}

func TestEachToolsArgumentsCarryTheirOwnResidency(t *testing.T) {
	// Arguments are apportioned out of the model's output by byte share, but
	// their residency is not: it comes from the call that wrote them,
	// weighted by argument bytes. Inheriting one session-wide average put the
	// same number on every row, which is a shade that says nothing.
	s := treeFixture()
	carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))
	tree := BuildTree(s, carry)

	args := child(t, child(t, tree, "model output"), "tool inputs")
	if len(args.Children) < 2 {
		t.Skip("the fixture uses fewer than two tools")
	}
	seen := map[float64]bool{}
	for _, c := range args.Children {
		if c.RoundTrips <= 0 {
			t.Errorf("%s has no round-trip figure", c.Name)
		}
		seen[c.RoundTrips] = true
	}
	if len(seen) == 1 {
		t.Error("every tool reported the same residency, so it is not per tool")
	}
}

func TestBranchesAreNamedForWhatCameBackNotForTheSource(t *testing.T) {
	// "cli output" rather than "CLI", because the branch holds one half of a
	// tool call: what the tool printed. The command that caused it is the
	// model's, and it is under model output. A branch named for the source
	// would claim both halves.
	//
	// The rule is not a suffix -- "subagent reports" and "edit confirmations"
	// name what came back without one. It is that the name must not be the
	// name of the thing that produced it.
	sources := map[string]string{
		"CLI":           "cli output",
		"web":           "web content",
		"harness tools": "harness output",
		"MCP":           "mcp output",
		"subagents":     "subagent reports",
	}
	tree := built(t)
	var walk func(*Node)
	walk = func(n *Node) {
		if replacement, bad := sources[n.Name]; bad {
			t.Errorf("%q names the source, not what came back; expected %q",
				n.Name, replacement)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(tree)
}

func TestTooltipsStaySomethingAPersonWillRead(t *testing.T) {
	// A paragraph on a box you are hovering over competes with the box for
	// your attention, and loses. The remainder's tooltip was 547 characters
	// and nobody read it. The bar is "what you typed, carried for the rest of
	// the session" -- one clause. The argument belongs in `tokenamun tree`,
	// where a reader has asked for it.
	const limit = 120
	tree := built(t)
	var walk func(*Node)
	walk = func(n *Node) {
		if len([]rune(n.Detail)) > limit {
			t.Errorf("%q has a %d-character tooltip; put the argument in DetailMore",
				n.Name, len([]rune(n.Detail)))
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(tree)
}

func TestTheArgumentSurvivesBeingMovedOutOfTheTooltip(t *testing.T) {
	// Shortening a tooltip must not lose what it said, only relocate it.
	// The carry fixture overshoots, so it has a remainder to explain.
	s := carrySession(t)
	carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))
	tree := BuildTree(s, carry)
	for _, name := range []string{"unattributed", "preamble"} {
		n := child(t, tree, name)
		if n.Detail == "" {
			t.Errorf("%s should still say what it is", name)
		}
		if n.DetailMore == "" {
			t.Errorf("%s lost its explanation rather than moving it", name)
		}
	}
	// And DetailMore must not be in the payload the viewer reads: it is
	// there for the terminal, and shipping it would only make the HTML
	// bigger for text nothing renders.
	raw, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "detailMore") ||
		strings.Contains(string(raw), "DetailMore") {
		t.Error("the long form should not be serialised into the viewer's payload")
	}
}

func TestShellArgumentsOpenUpByCommand(t *testing.T) {
	// A shell call's arguments are a command, so they open up the same way
	// its output does. Without this, Bash arguments are one opaque box --
	// 15% of a team's week on one reference repository -- and the obvious
	// question about it, whether the content is a pattern or different every
	// time, has no answer in the tool.
	s := treeFixture()
	carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))
	tree := BuildTree(s, carry)

	args := child(t, child(t, tree, "model output"), "tool inputs")
	bash := child(t, args, "Bash")
	if len(bash.Children) == 0 {
		t.Fatal("shell arguments should open up by command")
	}
	// The split apportions the tool's own cost, so it must reconcile.
	var sum float64
	for _, c := range bash.Children {
		sum += c.Carry
		if c.RoundTrips <= 0 {
			t.Errorf("%s has no round-trip figure", c.Name)
		}
	}
	if diff := sum - bash.Carry; diff > 0.001 || diff < -0.001 {
		t.Errorf("the commands sum to %.4f but Bash costs %.4f", sum, bash.Carry)
	}

	// A tool whose calls carry no command line stays a leaf: there is nothing
	// to split it by, and inventing a level would be worse than not having
	// one.
	for _, c := range args.Children {
		if c.Name == "Read" && len(c.Children) > 0 {
			t.Error("Read takes no command line, so it has nothing to open up into")
		}
	}
}
