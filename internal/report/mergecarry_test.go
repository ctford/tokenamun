package report

import (
	"testing"

	"github.com/ctford/tokenamun/internal/model"
)

// synthCarry is a hand-written carry report for one session, with the fields
// the merge reads and nothing else.
func synthCarry(id string, promptCost, preamble, preambleCarry, residual float64,
	items ...CarryItem) Carry {
	return Carry{
		SchemaVersion: SchemaVersion,
		Session:       SessionInfo{ID: id, Selector: id},
		Context: ContextReport{
			Peak:       model.Obs(preamble*3, model.Tokens),
			Final:      quantity(model.Obs(preamble*2, model.Tokens)),
			PromptCost: model.Der(promptCost, model.EIT),
			Resets:     []int{7},
		},
		Preamble: PreambleReport{
			Tokens: model.Obs(preamble, model.Tokens),
			Carry:  model.Der(preambleCarry, model.EIT),
			Share:  model.Der(preambleCarry/promptCost, model.Ratio),
			Note:   "the preamble note",
		},
		Items:        items,
		Unattributed: model.Der(residual, model.Ratio),
		Notes:        []string{"a note every carry report carries"},
	}
}

func synthItem(path string, carryEIT float64) CarryItem {
	return CarryItem{
		Tool:        "Read",
		Path:        path,
		Tokens:      model.Quantity{Value: carryEIT / 10, Unit: model.Tokens, Prov: model.DerivedApprox},
		EnteredAt:   3,
		ResidentFor: model.Obs(10, model.Calls),
		ColdCalls:   model.Der(1, model.Calls),
		CarryCost:   model.Der(carryEIT, model.EIT),
	}
}

// The item ranking is the thing `all` was wanted for, and it only works if a
// row from a cheap session can outrank the worst row of an expensive one.
func TestCarryOverASetRanksAcrossSessionsAndSaysWhichIsWhich(t *testing.T) {
	a := synthCarry("session-a", 1000, 100, 200, 0.1,
		synthItem("a/small.go", 5), synthItem("a/large.go", 90))
	b := synthCarry("session-b", 3000, 300, 400, 0.2,
		synthItem("b/middling.go", 50))

	got := MergeCarries([]Carry{a, b}, SessionInfo{ID: "2 sessions"})

	want := []struct{ path, session string }{
		{"a/large.go", "session-a"},
		{"b/middling.go", "session-b"},
		{"a/small.go", "session-a"},
	}
	if len(got.Items) != len(want) {
		t.Fatalf("merged %d items, want %d", len(got.Items), len(want))
	}
	for i, w := range want {
		if got.Items[i].Path != w.path {
			t.Errorf("rank %d is %q, want %q", i, got.Items[i].Path, w.path)
		}
		if got.Items[i].Session != w.session {
			t.Errorf("%s says session %q, want %q", w.path, got.Items[i].Session, w.session)
		}
	}
}

func TestCarryOverASetSumsWhatComposesAndDropsWhatDoesNot(t *testing.T) {
	a := synthCarry("session-a", 1000, 100, 200, 0.1)
	b := synthCarry("session-b", 3000, 300, 400, 0.2)

	got := MergeCarries([]Carry{a, b}, SessionInfo{ID: "2 sessions"})

	if got.Sessions != 2 {
		t.Errorf("merged %d sessions, want 2", got.Sessions)
	}
	if got.Context.PromptCost.Value != 4000 {
		t.Errorf("prompt cost %v, want the sum 4000", got.Context.PromptCost.Value)
	}
	if got.Preamble.Carry.Value != 600 {
		t.Errorf("preamble carry %v, want the sum 600", got.Preamble.Carry.Value)
	}
	if got.Preamble.Tokens.Value != 400 {
		t.Errorf("preamble tokens %v, want the sum 400: each session pays its own",
			got.Preamble.Tokens.Value)
	}
	// Peak is a maximum of maxima, never a sum: no session ever held both.
	if got.Context.Peak.Value != 900 {
		t.Errorf("peak %v, want the largest single session's 900", got.Context.Peak.Value)
	}
	// Recomputed against the totals, not averaged over sessions: 600/4000,
	// not the mean of 0.2 and 0.133.
	if got.Preamble.Share.Value != 0.15 {
		t.Errorf("preamble share %v, want 0.15 recomputed against the totals",
			got.Preamble.Share.Value)
	}
	// Weighted by what each session spent, so the big session counts for more.
	if want := (0.1*1000 + 0.2*3000) / 4000; got.Unattributed.Value != want {
		t.Errorf("unattributed share %v, want %v weighted by prompt cost",
			got.Unattributed.Value, want)
	}

	// Call indices index into one session. Kept, they would read as though
	// the set had reset at call 7 and ended at a prompt one member happened
	// to finish on.
	if got.Context.Final != nil {
		t.Error("a set of sessions has no final prompt")
	}
	if got.Context.Resets != nil {
		t.Error("a set of sessions has no reset call numbers")
	}
}

func TestCarryOverASetCutsTheRankingAfterMerging(t *testing.T) {
	// Cut per session and then ranked, a week's worst retrieval could be
	// missing because its session had fifteen worse ones.
	var carries []Carry
	for s := 0; s < 4; s++ {
		var items []CarryItem
		for i := 0; i < 10; i++ {
			items = append(items, synthItem("f.go", float64(s*10+i)))
		}
		carries = append(carries, synthCarry("session", 100, 10, 10, 0.1, items...))
	}
	got := MergeCarries(carries, SessionInfo{ID: "4 sessions"})
	if len(got.Items) != CarryItemsShown {
		t.Fatalf("listed %d items, want %d", len(got.Items), CarryItemsShown)
	}
	if got.Items[0].CarryCost.Value != 39 {
		t.Errorf("worst row is %v, want the worst of the whole set, 39",
			got.Items[0].CarryCost.Value)
	}
}

func TestCarryOverAnEmptySetIsStillALabelledReport(t *testing.T) {
	// Every number the renderer touches must carry a provenance label, even
	// when there is nothing to report: an unlabelled zero is the one thing
	// the renderer refuses to print.
	got := MergeCarries(nil, SessionInfo{ID: "no sessions"})
	walkQuantities(t, "carry", mustTree(t, got))
}
