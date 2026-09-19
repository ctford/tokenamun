package report

// The hypothetical: what a change to part of the tree would have been worth.
// Split from report_test.go when that file reached its length budget, along
// the seam the budget exposed -- these change when the counterfactual does,
// and the rest when a report's shape does.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ctford/tokenamun/internal/analysis"
)

// The unknown section must always render, because it is the thing that stops
// a counterfactual being read as a measurement. It survived the deletion of
// the named interventions: the one hypothetical that is left is held to it.
func TestAHypotheticalAlwaysRendersItsUnknowns(t *testing.T) {
	s := carrySession(t)
	carry := analysis.Carry(s, analysis.Cache(s))
	o, err := ParseOptimisation([]string{"cli output"}, []float64{0.5},
		[]string{"A guess, not a measurement."}, "proxy")
	if err != nil {
		t.Fatal(err)
	}
	h, err := BuildHypotheticalFrom(BuildTree(s, carry), sessionInfo(s), o)
	if err != nil {
		t.Fatal(err)
	}

	var text bytes.Buffer
	if err := RenderHypothetical(&text, h); err != nil {
		t.Fatal(err)
	}
	out := text.String()
	for _, want := range []string{
		"Unknown",
		// The figure is the caller's, and the report has to say so where the
		// number is, not only in the docs.
		"not a measurement",
		// And the outcome caveat, which is true of every counterfactual: an
		// agent that fails the task consumes the fewest tokens of all.
		"came out right",
		// The caller's own reason, printed beside their number.
		"A guess, not a measurement.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendered hypothetical is missing %q:\n%s", want, out)
		}
	}
	if len(h.Unknown) == 0 {
		t.Error("a hypothetical with no unknowns is the claim this tool exists to avoid")
	}
}

// A real proposal is several changes at once, and composing it by hand across
// separate runs of this command does not work: each run reports its impact
// against the untouched session, so the impacts cannot be added, and the
// savings only add where the parts do not overlap.
func TestSeveralPartsComposeIntoOneAnswer(t *testing.T) {
	s := carrySession(t)
	tree := BuildTree(s, analysis.Carry(s, analysis.Cache(s)))

	single := func(at string, becomes float64) Hypothetical {
		t.Helper()
		o, err := ParseOptimisation([]string{at}, []float64{becomes}, []string{"A guess."}, "")
		if err != nil {
			t.Fatal(err)
		}
		h, err := BuildHypotheticalFrom(tree, sessionInfo(s), o)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	a := single("cli output", 0.5)
	b := single("file content", 0)

	o, err := ParseOptimisation(
		[]string{"cli output", "file content"},
		[]float64{0.5, 0},
		[]string{"quieter test output", "read less of each file"}, "both")
	if err != nil {
		t.Fatal(err)
	}
	both, err := BuildHypotheticalFrom(tree, sessionInfo(s), o)
	if err != nil {
		t.Fatal(err)
	}

	if len(both.Parts) != 2 {
		t.Fatalf("expected two parts, got %d", len(both.Parts))
	}
	if got, want := both.Saving, a.Saving+b.Saving; got != want {
		t.Errorf("savings over disjoint parts must add: %.2f, want %.2f", got, want)
	}
	if got, want := both.Addressable, a.Addressable+b.Addressable; got != want {
		t.Errorf("addressable must add: %.2f, want %.2f", got, want)
	}
	// Impacts do not add, which is the reason this cannot be done by hand:
	// two 5% reductions are not a 10% one.
	if both.Impact >= a.Impact || both.Impact >= b.Impact {
		t.Errorf("the combined impact %.4f should be below either alone (%.4f, %.4f)",
			both.Impact, a.Impact, b.Impact)
	}
	// And the composition rule still holds over the combined part.
	want := 1 - both.AddressableShare*(1-both.Becomes)
	if diff := both.Impact - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("impact %.9f does not compose to %.9f", both.Impact, want)
	}
	// Each part keeps its own reason. One caveat covering two unrelated
	// changes is not a reason for either of them.
	for i, caveat := range []string{"quieter test output", "read less of each file"} {
		if both.Parts[i].Caveat != caveat {
			t.Errorf("part %d caveat is %q, want %q", i, both.Parts[i].Caveat, caveat)
		}
	}

	var text bytes.Buffer
	if err := RenderHypothetical(&text, both); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Parts", "cli output", "file content",
		"quieter test output", "read less of each file"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("the rendered hypothetical is missing %q:\n%s", want, text.String())
		}
	}
}

// Overlapping parts are the same tokens counted twice, and the tool is the
// only party that can see the containment.
func TestPartsThatContainOneAnotherAreRefused(t *testing.T) {
	s := carrySession(t)
	tree := BuildTree(s, analysis.Carry(s, analysis.Cache(s)))
	for _, paths := range [][]string{
		{"cli output", "cli output"},
		// Matching is case-insensitive everywhere else in the tree, so the
		// containment check has to be too, or the refusal is bypassed by
		// typing a capital letter.
		{"cli output", "CLI Output"},
	} {
		o, err := ParseOptimisation(paths, []float64{0.5, 0.5},
			[]string{"a reason.", "another."}, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := BuildHypotheticalFrom(tree, sessionInfo(s), o); err == nil {
			t.Errorf("%v overlap and should have been refused", paths)
		}
	}
}

func TestMismatchedOptimiseColumnsAreRefused(t *testing.T) {
	cases := []struct {
		at      []string
		becomes []float64
		why     []string
	}{
		{[]string{"a", "b"}, []float64{0.5}, []string{"x.", "y."}},
		{[]string{"a", "b"}, []float64{0.5, 0.5}, []string{"x."}},
		{[]string{"a"}, []float64{0.5, 0.5}, []string{"x.", "y."}},
	}
	for _, c := range cases {
		if _, err := ParseOptimisation(c.at, c.becomes, c.why, ""); err == nil {
			t.Errorf("%d at, %d optimise, %d why should not pair up",
				len(c.at), len(c.becomes), len(c.why))
		}
	}
}

// The two commands report against different totals. Both print both, with the
// base on the line, because a reader collecting one of each into a column
// compared quantities that are not comparable and nothing looked wrong.
func TestBothDenominatorsArePrintedByBothCommands(t *testing.T) {
	s := carrySession(t)
	carry := analysis.Carry(s, analysis.Cache(s))
	tree := BuildTree(s, carry)

	o, err := ParseOptimisation([]string{"cli output"}, []float64{0.5},
		[]string{"A guess."}, "")
	if err != nil {
		t.Fatal(err)
	}
	h, err := BuildHypotheticalFrom(tree, sessionInfo(s), o)
	if err != nil {
		t.Fatal(err)
	}
	if h.AddressableShare <= 0 || h.AddressableShareOfPrompt <= 0 {
		t.Fatalf("both shares should be reported: %.4f of session, %.4f of prompt",
			h.AddressableShare, h.AddressableShareOfPrompt)
	}
	// Prompt cost is the smaller total, so the share against it is the larger
	// number. That is the whole reason the two cannot be put in one column.
	if h.AddressableShareOfPrompt <= h.AddressableShare {
		t.Errorf("a share of prompt cost should exceed the same share of the total: "+
			"%.4f against %.4f", h.AddressableShareOfPrompt, h.AddressableShare)
	}
	var text bytes.Buffer
	if err := RenderHypothetical(&text, h); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Share of session", "Share of prompt"} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("optimise should name its denominators, missing %q", want)
		}
	}

	c := BuildCache(s, analysis.Cache(s))
	if c.SessionCost.Value <= c.PromptCost.Value {
		t.Errorf("the session total should exceed prompt cost: %.0f against %.0f",
			c.SessionCost.Value, c.PromptCost.Value)
	}
	if c.Expiry.Share.Value <= c.Expiry.ShareOfSession.Value {
		t.Errorf("expiry's share of prompt cost should exceed its share of the total: "+
			"%.4f against %.4f", c.Expiry.Share.Value, c.Expiry.ShareOfSession.Value)
	}
	for _, row := range c.Causes {
		if row.Share.Value <= row.ShareOfSession.Value {
			t.Errorf("%s: share of prompt %.4f should exceed share of total %.4f",
				row.Cause, row.Share.Value, row.ShareOfSession.Value)
		}
	}
	var cacheText bytes.Buffer
	if err := RenderCache(&cacheText, c); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"% PROMPT", "% TOTAL", "Session cost"} {
		if !strings.Contains(cacheText.String(), want) {
			t.Errorf("cache should name its denominators, missing %q", want)
		}
	}
}
