package report

import (
	"testing"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/model"
)

func TestMergeAddsCostsAndKeepsResidencyPerSession(t *testing.T) {
	// The design of a period report in one assertion. Cost is additive
	// across sessions, so the totals add. Residency is not: content in one
	// session is not resident during the next, so a round-trip figure has to
	// stay true to the session it came from and is averaged over tokens
	// rather than recomputed across the join.
	a := &Node{Name: "session", Kind: "root", Children: []*Node{
		{Name: "cli output", Kind: "mechanism", Children: []*Node{
			{Name: "git", Kind: "command", Tokens: 100, Carry: 1000, Items: 1,
				tokenCalls: 100 * 50},
		}},
	}}
	b := &Node{Name: "session", Kind: "root", Children: []*Node{
		{Name: "cli output", Kind: "mechanism", Children: []*Node{
			{Name: "git", Kind: "command", Tokens: 300, Carry: 9000, Items: 2,
				tokenCalls: 300 * 10},
		}},
		{Name: "file content", Kind: "mechanism", Children: []*Node{
			{Name: "a.go", Kind: "item", Tokens: 50, Carry: 500, Items: 1,
				tokenCalls: 50 * 20},
		}},
	}}

	merged := MergeTrees([]*Node{a, b})
	if merged.Carry != 10500 {
		t.Errorf("total cost = %.0f, want 10500", merged.Carry)
	}
	// Branches present in only one session still appear.
	if len(merged.Children) != 2 {
		t.Fatalf("expected both branches, got %d", len(merged.Children))
	}
	git := child(t, child(t, merged, "cli output"), "git")
	if git.Carry != 10000 || git.Items != 3 || git.Tokens != 400 {
		t.Errorf("git merged to carry=%.0f items=%d tokens=%.0f", git.Carry, git.Items, git.Tokens)
	}
	// Token-weighted: 100 tokens at 50 trips and 300 at 10 is 20, not the
	// unweighted 30. A long session weighs more, which is what you want when
	// the question is where the money went.
	if want := (100.0*50 + 300*10) / 400; git.RoundTrips != want {
		t.Errorf("git round trips = %.2f, want %.2f (token-weighted)", git.RoundTrips, want)
	}
}

func TestMergeKeepsANodeOnTheRampIfAnySessionCouldScaleIt(t *testing.T) {
	// One session with no token count for a branch must not drag the merged
	// version off the ramp, or a period report would grey out branches that
	// most of its sessions could shade.
	scalable := &Node{Name: "session", Kind: "root", Children: []*Node{
		{Name: "x", Kind: "item", Tokens: 10, Carry: 100, Items: 1, tokenCalls: 10 * 5},
	}}
	not := &Node{Name: "session", Kind: "root", Children: []*Node{
		{Name: "x", Kind: "item", Carry: 100, Items: 1, Unscaled: true},
	}}

	for _, order := range [][]*Node{{scalable, not}, {not, scalable}} {
		merged := MergeTrees(order)
		x := child(t, merged, "x")
		if x.Unscaled {
			t.Error("a node one session could scale must stay on the ramp")
		}
		if x.RoundTrips <= 0 {
			t.Error("and it must keep a round-trip figure")
		}
	}
}

func TestPeriodSaysWhatItCouldNotRead(t *testing.T) {
	// A total over an unknown number of sessions is not a total.
	s := carrySession(t)
	p := BuildPeriod([]*model.Session{s}, "since 2026-09-17",
		[]string{"bad-session: unexpected end of JSON input"})

	if p.Sessions != 1 || p.Calls != len(s.Invocations) {
		t.Errorf("counted %d sessions and %d calls", p.Sessions, p.Calls)
	}
	if len(p.Failed) != 1 {
		t.Error("an unreadable session must be reported, not dropped")
	}
	if p.Window != "since 2026-09-17" {
		t.Errorf("the window must be stated: %q", p.Window)
	}
	// The summed tree must reconcile with the session it came from.
	carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))
	// Compared with a tolerance: the merge re-sums the leaves, so the branch
	// totals are the same additions in a different order and float addition
	// is not associative. A tolerance is the honest comparison, not a looser
	// assertion.
	one := BuildTree(s, carry)
	if diff := p.Tree.Carry - one.Carry; diff > 0.001 || diff < -0.001 {
		t.Errorf("one session summed to %.6f, alone it is %.6f", p.Tree.Carry, one.Carry)
	}
}
