package report

import (
	"strings"
	"testing"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
	"github.com/ctford/tokenamun/internal/whatif"
)

func sliceContext(t *testing.T) whatif.Context {
	t.Helper()
	s := carrySession(t)
	cache := analysis.Cache(s, analysis.TTL5m)
	carry := analysis.Carry(s, cache)
	w := cost.For(firstModel(s))
	return whatif.Context{
		Session: s, Cache: cache, Carry: carry, Weights: w,
		CompressionRatio: 0.5,
		Total:            carry.PromptCostEIT + w.OutputCost(s.Usage()),
	}
}

func TestSliceIsAnInterventionLikeAnyOther(t *testing.T) {
	// The point of the generic form: everything that shrinks content is a
	// slice of the tree and a fraction, so an agent can ask about one
	// without the tool modelling the vendor.
	sl, err := ParseSlice("CLI output", 0.5, "caveman", "Vendor figure, not measured here.")
	if err != nil {
		t.Fatal(err)
	}
	var i whatif.Intervention = sl
	if i.Name() != "caveman" {
		t.Errorf("the caller names it, got %q", i.Name())
	}

	c := sliceContext(t)
	r := i.Estimate(c)
	if !r.Applicable {
		t.Fatalf("CLI output has cost in this fixture: %+v", r)
	}
	if r.Acts != whatif.AxisVolume {
		t.Errorf("a slice removes content, so it acts on volume, got %q", r.Acts)
	}
	// Amdahl: the answer is the slice's share times the cut, and it has to
	// agree with the tree the viewer draws.
	node, _, err := resolve(BuildTree(c.Session, c.Carry), []string{"CLI output"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Addressable == nil || r.Addressable.CostEIT != node.Carry {
		t.Errorf("the addressable cost must be the node's own: %+v vs %.2f",
			r.Addressable, node.Carry)
	}
	if want := -node.Carry * 0.5; r.Headline.Quantity.Value != want {
		t.Errorf("effect %.2f, want %.2f", r.Headline.Quantity.Value, want)
	}
	if got, want := r.Addressable.Share*r.Reduction,
		r.Headline.Quantity.Value/c.Total; got != want {
		t.Errorf("share x reduction = %.9f but the effect is %.9f of the session", got, want)
	}
	// Every rule the built-ins are held to.
	if err := whatif.Validate(&r, whatif.Manifest{
		Name: r.Intervention, Description: r.Description,
	}); err != nil {
		t.Errorf("a slice must satisfy the same contract as a built-in: %v", err)
	}
}

func TestSliceRefusesParametersThatWouldProduceAnUnquotableNumber(t *testing.T) {
	cases := []struct {
		name, at string
		cut      float64
		why      string
		wants    string
	}{
		{name: "no node", at: "", cut: 0.5, why: "x.", wants: "--at is required"},
		{name: "no cut", at: "CLI output", cut: 0, why: "x.", wants: "--cut must be"},
		{name: "cut over one", at: "CLI output", cut: 1.5, why: "x.", wants: "--cut must be"},
		{name: "no caveat", at: "CLI output", cut: 0.5, why: "  ", wants: "--why is required"},
		{name: "essay for a caveat", at: "CLI output", cut: 0.5,
			why:   strings.Repeat("x", whatif.CaveatLimit+1),
			wants: "is a column in a table"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSlice(tc.at, tc.cut, "n", tc.why)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Errorf("expected %q, got %v", tc.wants, err)
			}
		})
	}
}

func TestSliceNamesWhatIsThereWhenTheNodeIsWrong(t *testing.T) {
	// The tree is the vocabulary. A caller who guesses a node name should be
	// told the real ones rather than getting an empty result.
	sl, err := ParseSlice("nowhere", 0.5, "n", "Made up.")
	if err != nil {
		t.Fatal(err)
	}
	r := sl.Estimate(sliceContext(t))
	if r.Applicable {
		t.Fatal("an unknown node is not an applicable finding")
	}
	if !strings.Contains(r.NotMeasurable, "it contains:") {
		t.Errorf("the failure should list the real node names: %q", r.NotMeasurable)
	}
}

func TestSliceCanAddressADeepNode(t *testing.T) {
	// "CLI output/git" rather than only top-level branches: the whole value
	// of naming the slice after the tree is that you can point at any level
	// the viewer can.
	c := sliceContext(t)
	tree := BuildTree(c.Session, c.Carry)
	cli, _, err := resolve(tree, []string{"CLI output"})
	if err != nil || len(cli.Children) == 0 {
		t.Skip("the fixture has no nested CLI output")
	}
	deep := "CLI output/" + cli.Children[0].Name

	sl, err := ParseSlice(deep, 0.25, "n", "Made up.")
	if err != nil {
		t.Fatal(err)
	}
	r := sl.Estimate(c)
	if !r.Applicable {
		t.Fatalf("%s should be addressable: %s", deep, r.NotMeasurable)
	}
	if r.Addressable.Share >= 1 {
		t.Error("a nested node cannot be the whole session")
	}
	if r.Addressable.CostEIT > cli.Carry {
		t.Error("a child cannot cost more than its parent")
	}
}

func TestSliceUnitsAreLabelledHonestly(t *testing.T) {
	sl, _ := ParseSlice("CLI output", 0.5, "n", "Made up.")
	r := sl.Estimate(sliceContext(t))
	for _, f := range r.Counterfact {
		if f.Quantity != nil && f.Quantity.Prov != model.Counterfactual {
			t.Errorf("%q is in the counterfactual section but labelled %q",
				f.Label, f.Quantity.Prov)
		}
	}
	for _, f := range r.Observed {
		if f.Quantity != nil && f.Quantity.Prov != model.Observed {
			t.Errorf("%q is in the observed section but labelled %q",
				f.Label, f.Quantity.Prov)
		}
	}
}
