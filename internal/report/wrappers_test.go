package report

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/ingest"
	"github.com/ctford/tokenamun/internal/model"
)

// The fixture runs one task runner with three targets, four times each, and
// gives one run of one target a result far larger than the rest.
func wrapperTree(t *testing.T) *Node {
	t.Helper()
	s, err := ingest.Load(model.SessionRef{
		ID:         "wrappers",
		Transcript: filepath.Join("..", "ingest", "testdata", "wrappers.jsonl"),
		Origin:     model.FromLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	return BuildTree(s, analysis.Carry(s, analysis.Cache(s)))
}

func at(t *testing.T, tree *Node, path ...string) *Node {
	t.Helper()
	n, _, err := resolve(tree, path)
	if err != nil {
		t.Fatalf("%v: %v", path, err)
	}
	return n
}

// Everything a wrapper ran used to collapse into the wrapper. That is the
// largest single thing the tree could not open on a real week, and the answer
// is a measurement -- the targets repeat and look like commands -- rather
// than a list of runner names, which would be right until somebody adopted a
// different runner.
func TestAWrapperOpensIntoTheTargetsItRan(t *testing.T) {
	run := at(t, wrapperTree(t), "cli output", "mise", "mise run")
	var names []string
	for _, c := range run.Children {
		names = append(names, c.Name)
	}
	for _, want := range []string{"mise run check", "mise run test", "mise run lint"} {
		if !contains(strings.Join(names, "|"), want) {
			t.Errorf("the runner did not open into %q; it holds %v", want, names)
		}
	}
	// And the whole of the runner's cost is still under it: opening a leaf
	// must move cost down a level, never create or lose any.
	var sum float64
	for _, c := range run.Children {
		sum += c.Carry
	}
	if diff := sum - run.Carry; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("the targets come to %.4f under a runner costing %.4f", sum, run.Carry)
	}
}

// The distribution has to follow the split. A p95 or a maximum still
// reporting against the old grouping is a number that looks right and is not,
// which is the failure this repository cares most about.
func TestTheDistributionFollowsTheSplit(t *testing.T) {
	tree := wrapperTree(t)
	check := at(t, tree, "cli output", "mise", "mise run", "mise run check")
	lint := at(t, tree, "cli output", "mise", "mise run", "mise run lint")
	run := at(t, tree, "cli output", "mise", "mise run")

	if check.MaxPerRetrieval <= lint.MaxPerRetrieval {
		t.Errorf("the target with the outlier run should have the larger maximum: "+
			"%.0f against %.0f", check.MaxPerRetrieval, lint.MaxPerRetrieval)
	}
	// The runner's own maximum is its worst target's, since that retrieval is
	// still underneath it.
	if run.MaxPerRetrieval != check.MaxPerRetrieval {
		t.Errorf("the runner's maximum is %.0f and its worst target's is %.0f",
			run.MaxPerRetrieval, check.MaxPerRetrieval)
	}
	// And the quiet target's maximum is its own, not the runner's. Before the
	// split every target read as the worst one.
	if lint.MaxPerRetrieval >= run.MaxPerRetrieval {
		t.Errorf("a quiet target is reporting the runner's maximum: %.0f",
			lint.MaxPerRetrieval)
	}
}

// A target's leaf must not sit inside a node of a longer name: `mise run
// check` holding one leaf called `mise run` is the same row twice, with the
// less specific label on the inner one.
func TestATargetIsALeafRatherThanANodeRepeatingItsRunner(t *testing.T) {
	check := at(t, wrapperTree(t), "cli output", "mise", "mise run", "mise run check")
	if len(check.Children) != 0 {
		var names []string
		for _, c := range check.Children {
			names = append(names, c.Name)
		}
		t.Errorf("the target should be a leaf, it holds %v", names)
	}
	if check.Items != 4 {
		t.Errorf("the target holds %d retrievals, want the 4 the fixture ran", check.Items)
	}
}

// A leaf that is several retrievals says so. Otherwise it reads exactly like
// one that is a single atom, and the difference is whether the tool has run
// out of structure or the content has.
func TestAnAggregateLeafSaysHowManyRetrievalsAreInIt(t *testing.T) {
	tree := wrapperTree(t)
	v, err := BuildTreeViewFrom(tree, SessionInfo{ID: "wrappers"},
		[]string{"cli output", "mise", "mise run", "mise run check"}, ModeCarry)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Children) != 0 {
		t.Fatal("expected a leaf")
	}
	if !contains(v.Here.Detail, "4 retrievals under one name") {
		t.Errorf("the leaf does not say it is an aggregate: %q", v.Here.Detail)
	}
	if !contains(v.Here.Detail, aggregateReason) {
		t.Errorf("the leaf does not say why it stops there: %q", v.Here.Detail)
	}

	// Said once, not twice, however many presenters have been over the tree.
	noteAggregates(tree)
	again, err := BuildTreeViewFrom(tree, SessionInfo{ID: "wrappers"},
		[]string{"cli output", "mise", "mise run", "mise run check"}, ModeCarry)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(again.Here.Detail, aggregateReason); n != 1 {
		t.Errorf("the note appears %d times: %q", n, again.Here.Detail)
	}
}
