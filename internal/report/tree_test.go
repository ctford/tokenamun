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
			{Seq: 0, Tool: "Bash", Channel: model.ChanShell, CommandClass: "file reading",
				Category: model.CatADR, Path: "docs/decisions/a.md", Bytes: 4000, Tokens: 1000, InvocationSeq: 0},
			// The same file again: should collapse into one rectangle.
			{Seq: 1, Tool: "Bash", Channel: model.ChanShell, CommandClass: "file reading",
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

	shell := child(t, tree, "shell output")
	if _ = child(t, tree, "file reading"); shell.Tokens != 4500 {
		t.Errorf("shell output tokens = %v, want 4500", shell.Tokens)
	}
	// Shell output splits by what the command was doing; "shell output" with
	// no further structure is the least useful answer available.
	reading := child(t, shell, "file reading")
	child(t, shell, "tests")
	if reading.Tokens != 2000 {
		t.Errorf("file reading tokens = %v, want 2000", reading.Tokens)
	}
	// Other channels split by content category.
	direct := child(t, tree, "file reading")
	child(t, direct, string(model.CatSourceCode))
}

func TestRepeatedFilesCollapseIntoOneRectangle(t *testing.T) {
	// A treemap of forty identical slivers hides the thing worth seeing.
	tree := BuildTree(treeFixture(), analysis.CarryReport{})
	reading := child(t, child(t, tree, "shell output"), "file reading")

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
	tests := child(t, child(t, tree, "shell output"), "tests")
	name := tests.Children[0].Name
	if !contains(name, "unattributed") {
		t.Errorf("name = %q; a merged bucket must not look like one result", name)
	}
}

func TestTotalsRollUpAndMatchTheRetrievals(t *testing.T) {
	tree := BuildTree(treeFixture(), analysis.CarryReport{})
	if tree.Items != 4 {
		t.Errorf("root items = %d, want 4", tree.Items)
	}
	if tree.Tokens != 6100 {
		t.Errorf("root tokens = %v, want 6100", tree.Tokens)
	}
	var sum float64
	for _, ch := range tree.Children {
		sum += ch.Tokens
	}
	if sum != tree.Tokens {
		t.Errorf("children sum to %v but root says %v", sum, tree.Tokens)
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

func TestCarryJoinsOntoLeaves(t *testing.T) {
	carry := analysis.CarryReport{Items: []analysis.CarriedItem{
		{RetrievalSeq: 0, CarryEIT: 500, ResidentFor: 3},
		{RetrievalSeq: 3, CarryEIT: 90, ResidentFor: 1},
	}}
	tree := BuildTree(treeFixture(), carry)
	if tree.Carry != 590 {
		t.Errorf("root carry = %v, want 590", tree.Carry)
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
