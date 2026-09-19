package report

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
)

// twoModelSession is a session that switches model, which is the case the
// whole money conversion exists for. Synthetic and hand-written: the token
// counts are round so the expected dollars can be worked out on paper.
//
// claude-opus-5   $5/MTok in, reads at 0.1x
// claude-fable-5-1 $10/MTok in, reads at 0.025x
//
// Per call, in EIT and then in dollars:
//
//	opus     1,000 + 100,000x0.1   + 5,000x1.25 + 2,000x5 = 27,250 -> $0.13625
//	fable    1,000 + 100,000x0.025 + 5,000x1.25 + 2,000x5 = 19,750 -> $0.19750
//	                                                                 $0.33375
func twoModelSession() *model.Session {
	usage := model.TokenUsage{
		Input: 1000, CacheRead: 100000,
		CacheCreation: 5000, CacheCreation5m: 5000,
		Output: 2000,
	}
	return &model.Session{
		Ref: model.SessionRef{ID: "two-model", Origin: model.FromLocal},
		Invocations: []model.ModelInvocation{
			{Seq: 1, RequestID: "a", Model: "claude-opus-5", Usage: usage, Entries: 1},
			{Seq: 2, RequestID: "b", Model: "claude-fable-5-1", Usage: usage, Entries: 1},
		},
	}
}

const twoModelUSD = 0.33375

// TestMoneyAddsAcrossModelsWhereEITDoesNot is the claim the flag makes.
//
// The two calls are identical in tokens and differ only in model, so a
// conversion that used one price for the session would give both the same
// dollars. They are not the same: fable costs half again as much here, and
// pricing the pair at either model's rate is wrong by a fifth. That is the
// error a session-level price would make, and opusplan switches model on
// every plan-mode toggle.
func TestMoneyAddsAcrossModelsWhereEITDoesNot(t *testing.T) {
	info, err := WithPrices(SessionInfo{ID: "two-model"}, twoModelSession())
	if err != nil {
		t.Fatal(err)
	}
	if info.Prices == nil {
		t.Fatal("no prices attached")
	}
	if got := info.Prices.Total.Value; math.Abs(got-twoModelUSD) > 1e-9 {
		t.Errorf("total $%.5f, want $%.5f", got, twoModelUSD)
	}
	if got := info.Prices.Prompt.Value + info.Prices.Output.Value; math.Abs(got-twoModelUSD) > 1e-9 {
		t.Errorf("prompt + output = $%.5f, does not make the total $%.5f", got, twoModelUSD)
	}

	// What the session's first model alone would have said. Distinct, or the
	// test above would pass against the bug it exists to exclude.
	first, _ := cost.InputPrice("claude-opus-5")
	p, o := cost.SessionCost(twoModelSession().Invocations)
	if atFirstModel := (p + o) * first; math.Abs(atFirstModel-twoModelUSD) < 1e-9 {
		t.Error("pricing everything at the first model gives the same answer, so this test proves nothing")
	}

	for _, q := range []model.Quantity{info.Prices.Prompt, info.Prices.Output, info.Prices.Total} {
		if q.Unit != model.USD || q.Prov != model.Derived {
			t.Errorf("quantity %v is %s/%s, want usd/derived", q.Value, q.Unit, q.Prov)
		}
	}
	if info.Prices.Catalog == "" {
		t.Error("no catalog pin; the pin is the only thing that makes the figure checkable")
	}
}

// TestMoneyRefusesAModelItCannotPrice is the other half. A total that drops
// the calls it could not price is a bill missing a model, and reads as a bill.
func TestMoneyRefusesAModelItCannotPrice(t *testing.T) {
	s := twoModelSession()
	s.Invocations[1].Model = "claude-nonesuch-9"
	info, err := WithPrices(SessionInfo{ID: "two-model"}, s)
	if err == nil {
		t.Fatalf("priced an unknown model at $%.5f", info.Prices.Total.Value)
	}
	if !strings.Contains(err.Error(), "claude-nonesuch-9") {
		t.Errorf("error does not name the model: %v", err)
	}
	if !strings.Contains(err.Error(), "refresh-prices.sh") {
		t.Errorf("error does not say how to fix it: %v", err)
	}
	if info.Prices != nil {
		t.Error("figures attached alongside the refusal")
	}
}

// TestEveryRendererThatPrintsDollarsPrintsThePin is the rule stated once and
// checked everywhere, because the failure is per renderer: a dollar figure
// whose source is not stated is the number this tool exists not to produce.
func TestEveryRendererThatPrintsDollarsPrintsThePin(t *testing.T) {
	s := twoModelSession()
	info, err := WithPrices(SessionOf(s), s)
	if err != nil {
		t.Fatal(err)
	}
	pin := cost.CatalogPin()

	p := BuildProfile(s)
	p.Session = info
	view, err := BuildTreeViewFrom(BuildTree(s, analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))),
		info, nil, ModeCarry)
	if err != nil {
		t.Fatal(err)
	}
	cacheReport := BuildCacheOf(info, analysis.Cache(s, analysis.TTL5m))

	fan := crossModelFanOut()
	fanned, err := WithProfilePrices(BuildProfile(fan), fan)
	if err != nil {
		t.Fatal(err)
	}

	rendered := map[string]func(b *bytes.Buffer) error{
		"profile":   func(b *bytes.Buffer) error { return RenderText(b, p) },
		"cache":     func(b *bytes.Buffer) error { return RenderCache(b, cacheReport) },
		"tree":      func(b *bytes.Buffer) error { return RenderTreeView(b, view) },
		"subagents": func(b *bytes.Buffer) error { return RenderText(b, fanned) },
	}
	for name, render := range rendered {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			if err := render(&b); err != nil {
				t.Fatal(err)
			}
			out := b.String()
			if !strings.Contains(out, "$") {
				t.Fatal("printed no dollars at all, so the pin rule is untested here")
			}
			if !strings.Contains(out, pin) {
				t.Errorf("prints dollars without the catalog pin %q", pin)
			}
		})
	}
}

// TestNoPricesMeansNoMoneyAnywhere. EIT is the default unit permanently, so
// the flag being off has to leave every surface exactly as it was.
func TestNoPricesMeansNoMoneyAnywhere(t *testing.T) {
	s := twoModelSession()
	var b bytes.Buffer
	if err := RenderText(&b, BuildProfile(s)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "$") {
		t.Error("money printed without being asked for")
	}
}

// TestPricedQuantitiesAreLabelled runs the contract check over a profile that
// carries money, since the labelled-quantity walk elsewhere runs over one
// that does not.
func TestPricedQuantitiesAreLabelled(t *testing.T) {
	s := twoModelSession()
	info, err := WithPrices(SessionOf(s), s)
	if err != nil {
		t.Fatal(err)
	}
	p := BuildProfile(s)
	p.Session = info
	walkQuantities(t, "", mustTree(t, p))
}

// TestTheSubagentBlockCarriesItsOwnPin. The whole-output check above passes
// on a report whose money block has the pin and whose subagent block does
// not, and a reader quoting the combined figure is reading the second one.
func TestTheSubagentBlockCarriesItsOwnPin(t *testing.T) {
	s := crossModelFanOut()
	p, err := WithProfilePrices(BuildProfile(s), s)
	if err != nil {
		t.Fatal(err)
	}
	if p.Subagents.Prices == nil {
		t.Fatal("--prices stopped at the boundary the whole plan is about")
	}
	if p.Subagents.Prices.Catalog != cost.CatalogPin() {
		t.Errorf("subagent money pinned at %q, want %q",
			p.Subagents.Prices.Catalog, cost.CatalogPin())
	}

	var b bytes.Buffer
	if err := RenderText(&b, p); err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(b.String(), "Subagents (")
	if !ok {
		t.Fatal("no subagent block in the rendered report")
	}
	if !strings.Contains(block, "$") {
		t.Fatal("no dollars in the subagent block, so this proves nothing")
	}
	if !strings.Contains(block, cost.CatalogPin()) {
		t.Error("the subagent block prints dollars without the catalog pin")
	}
}

// TestCombinedDollarsAddWhereCombinedEITDoesNot is the claim the caveat on
// combined_cost makes: there is a sound number, and this is it.
//
// The parent and the subagent cost the same in EIT here and different
// amounts in money, because fable-5-1 costs twice opus-5 per input token
// and reads cache at a quarter the multiple. A combined figure that were
// really EIT in disguise would come out symmetric.
func TestCombinedDollarsAddWhereCombinedEITDoesNot(t *testing.T) {
	s := crossModelFanOut()
	p, err := WithProfilePrices(BuildProfile(s), s)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Subagents.CombinedMixedPricing {
		t.Fatal("fixture no longer spans two pricings, so this proves nothing")
	}

	own := p.Session.Prices.Total.Value
	sub := p.Subagents.Prices.Total.Value
	if math.Abs(p.Subagents.Prices.Combined.Value-(own+sub)) > 1e-12 {
		t.Errorf("combined $%.6f is not own $%.6f plus subagents $%.6f",
			p.Subagents.Prices.Combined.Value, own, sub)
	}
	if math.Abs(own-sub) < 1e-12 {
		t.Error("the two halves cost the same in dollars as in EIT, so the " +
			"conversion could be a rescaled EIT total and this proves nothing")
	}
	for _, q := range []model.Quantity{p.Subagents.Prices.Total, p.Subagents.Prices.Combined} {
		if q.Unit != model.USD || q.Prov != model.Derived {
			t.Errorf("quantity %v is %s/%s, want usd/derived", q.Value, q.Unit, q.Prov)
		}
	}
}

// TestSubagentMoneyRefusesAModelItCannotPrice. The refusal has to reach the
// subagents too: a combined total missing a subagent's model is a bill
// missing a model just as much as the session's own is.
func TestSubagentMoneyRefusesAModelItCannotPrice(t *testing.T) {
	s := crossModelFanOut()
	s.Subagents[0].Invocations[0].Model = "claude-nonesuch-9"
	p, err := WithProfilePrices(BuildProfile(s), s)
	if err == nil {
		t.Fatalf("priced an unknown subagent model at $%.5f", p.Subagents.Prices.Combined.Value)
	}
	if !strings.Contains(err.Error(), "claude-nonesuch-9") {
		t.Errorf("error does not name the model: %v", err)
	}
}

// TestSubagentMoneyIsOptOutLikeTheRest. EIT is the default unit, and a
// report nobody asked money of has none in it anywhere.
func TestSubagentMoneyIsOptOutLikeTheRest(t *testing.T) {
	var b bytes.Buffer
	if err := RenderText(&b, BuildProfile(crossModelFanOut())); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "$") {
		t.Error("money printed in the subagent block without being asked for")
	}
}
