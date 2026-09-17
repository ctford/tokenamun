package report

import (
	"testing"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/model"
)

func treeFixture() *model.Session {
	return &model.Session{
		Retrievals: []model.RetrievedContent{
			// Shell output that could be attributed to a file.
			{Seq: 0, Tool: "Bash", Channel: model.ChanShell, CommandClass: "cat / sed / head",
				Category: model.CatADR, Path: "docs/decisions/a.md", Bytes: 4000, Tokens: 1000, InvocationSeq: 0},
			// The same file again: should collapse into one rectangle.
			{Seq: 1, Tool: "Bash", Channel: model.ChanShell, CommandClass: "cat / sed / head",
				Category: model.CatADR, Path: "docs/decisions/a.md", Bytes: 4000, Tokens: 1000, InvocationSeq: 1},
			// Shell output with no path.
			{Seq: 2, Tool: "Bash", Channel: model.ChanShell, CommandClass: "tests",
				Category: model.CatToolOutput, Bytes: 9000, Tokens: 2500, InvocationSeq: 2},
			// A direct file read: a different channel.
			{Seq: 3, Tool: "Read", Channel: model.ChanFileRead,
				Category: model.CatSourceCode, Path: "internal/pay/charge.go", Bytes: 6000, Tokens: 1600, InvocationSeq: 3},
		},
	}
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

// The top level answers "how was this obtained", because that is the level a
// reader can act on. What it turned out to be is one level down.
func TestTopLevelIsAcquisitionChannel(t *testing.T) {
	tree := BuildTree(treeFixture(), analysis.CarryReport{})
	retrieved := child(t, tree, "retrieved content")

	shell := child(t, retrieved, "shell output")
	if _ = child(t, retrieved, "Read tool"); shell.Tokens != 4500 {
		t.Errorf("shell output tokens = %v, want 4500", shell.Tokens)
	}
	// Shell output splits by what the command was doing; "shell output" with
	// no further structure is the least useful answer available.
	reading := child(t, shell, "cat / sed / head")
	child(t, shell, "tests")
	if reading.Tokens != 2000 {
		t.Errorf("file reading tokens = %v, want 2000", reading.Tokens)
	}
	// Other channels split by content category.
	direct := child(t, retrieved, "Read tool")
	child(t, direct, string(model.CatSourceCode))
}

func TestRepeatedFilesCollapseIntoOneRectangle(t *testing.T) {
	// A treemap of forty identical slivers hides the thing worth seeing.
	tree := BuildTree(treeFixture(), analysis.CarryReport{})
	reading := child(t, child(t, child(t, tree, "retrieved content"), "shell output"), "cat / sed / head")

	if len(reading.Children) != 1 {
		t.Fatalf("expected the two reads of one file to collapse, got %d rectangles",
			len(reading.Children))
	}
	leaf := reading.Children[0]
	if leaf.Items != 2 {
		t.Errorf("items = %d, want 2", leaf.Items)
	}
	if leaf.Tokens != 2000 {
		t.Errorf("tokens = %v, want both reads summed", leaf.Tokens)
	}
	if leaf.Detail == "" || !contains(leaf.Detail, "2 retrievals") {
		t.Errorf("a collapsed rectangle must say how many retrievals it is, got %q", leaf.Detail)
	}
}

func TestUnattributedOutputIsNamedAsABucket(t *testing.T) {
	tree := BuildTree(treeFixture(), analysis.CarryReport{})
	tests := child(t, child(t, child(t, tree, "retrieved content"), "shell output"), "tests")
	name := tests.Children[0].Name
	if !contains(name, "unattributed") {
		t.Errorf("name = %q; a merged bucket must not look like one result", name)
	}
}

func TestTotalsRollUpAndMatchTheRetrievals(t *testing.T) {
	tree := BuildTree(treeFixture(), analysis.CarryReport{})
	retrieved := child(t, tree, "retrieved content")
	if retrieved.Items != 4 {
		t.Errorf("retrieved items = %d, want 4", retrieved.Items)
	}
	if retrieved.Tokens != 6100 {
		t.Errorf("retrieved tokens = %v, want 6100", retrieved.Tokens)
	}
	var sum float64
	for _, ch := range retrieved.Children {
		sum += ch.Tokens
	}
	if sum != retrieved.Tokens {
		t.Errorf("children sum to %v but the branch says %v", sum, retrieved.Tokens)
	}
}

// A viewer of retrieved content alone answers a narrower question than "where
// did the tokens go". The parts that cannot be decomposed have to be present,
// or every percentage in the view is inflated.
func TestTreeAccountsForTheWholePromptCost(t *testing.T) {
	carry := analysis.CarryReport{
		PromptCostEIT:    1000,
		PreambleCarryEIT: 200,
		Items: []analysis.CarriedItem{
			{RetrievalSeq: 0, CarryEIT: 100},
			{RetrievalSeq: 3, CarryEIT: 50},
		},
	}
	tree := BuildTree(treeFixture(), carry)

	child(t, tree, "session preamble")
	rest := child(t, tree, "unattributed")
	// 1000 total - 150 retrieval - 200 preamble = 650 left over.
	if rest.Carry != 650 {
		t.Errorf("remainder = %v, want 650", rest.Carry)
	}
	// Every block that is not a retrieval breakdown must explain itself,
	// rather than looking like an omission or a shrug.
	for _, name := range []string{"session preamble", "unattributed"} {
		if d := child(t, tree, name).Detail; d == "" {
			t.Errorf("%s: no explanation", name)
		}
	}
	if got := child(t, tree, "retrieved content").Carry; got != 150 {
		t.Errorf("retrieval carry = %v, want 150", got)
	}
	if tree.Carry != 1000 {
		t.Errorf("root carry = %v; the tree should account for the whole prompt cost", tree.Carry)
	}
}

func TestEveryLevelIsSortedLargestFirst(t *testing.T) {
	tree := BuildTree(treeFixture(), analysis.CarryReport{})
	var check func(*Node)
	check = func(n *Node) {
		for i := 1; i < len(n.Children); i++ {
			if n.Children[i-1].Tokens < n.Children[i].Tokens {
				t.Errorf("%q: children out of order", n.Name)
			}
		}
		for _, c := range n.Children {
			check(c)
		}
	}
	check(tree)
}

// The model re-reading its own output is a first-class cost, and on real
// sessions the largest single one. Leaving it inside a vague remainder hid it.
func TestTheModelsOwnOutputAndToolCallsAreCarriedSeparately(t *testing.T) {
	s := treeFixture()
	s.Invocations = []model.ModelInvocation{
		{Seq: 0, Model: "claude-opus-5", Usage: model.TokenUsage{CacheRead: 10_000, Output: 500}},
		{Seq: 1, Model: "claude-opus-5", Usage: model.TokenUsage{CacheRead: 11_000, Output: 400}},
		{Seq: 2, Model: "claude-opus-5", Usage: model.TokenUsage{CacheRead: 12_000, Output: 300}},
	}
	s.ToolCalls = []model.ToolCall{{Seq: 0, InvocationSeq: 0, InputBytes: 3600}}
	s.Estimator = model.TokenEstimator{BytesPerToken: 3.6, Calibrated: true}

	carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))
	if carry.AssistantCarryEIT <= 0 {
		t.Fatal("output written early is re-sent later and must be priced")
	}
	if carry.ToolInputCarryEIT <= 0 {
		t.Fatal("the arguments of a tool call sit in the conversation like its results do")
	}

	tree := BuildTree(s, carry)
	replies := child(t, tree, "model replies")
	if replies.Carry != carry.AssistantCarryEIT {
		t.Errorf("replies block = %v, want %v", replies.Carry, carry.AssistantCarryEIT)
	}
	child(t, tree, "tool calls")
}

func TestCarryJoinsOntoLeaves(t *testing.T) {
	carry := analysis.CarryReport{Items: []analysis.CarriedItem{
		{RetrievalSeq: 0, CarryEIT: 500, ResidentFor: 3},
		{RetrievalSeq: 3, CarryEIT: 90, ResidentFor: 1},
	}}
	tree := BuildTree(treeFixture(), carry)
	if got := child(t, tree, "retrieved content").Carry; got != 590 {
		t.Errorf("retrieval carry = %v, want 590", got)
	}
	// The ramp needs a per-token rate on every node it colours.
	if tree.CarryPerToken <= 0 {
		t.Error("root should have a carry-per-token rate")
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
