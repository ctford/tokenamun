package report

import (
	"strings"
	"testing"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/cost/costtest"
)

// The mistake these tests exist for was made four times independently, so
// they all run against one fixture: a session whose first call is on one
// pricing and whose remaining calls are on another, with cache reads
// dominating. A test written beside each fix would have covered that site
// and let the next copy in.

// naivePromptCost is the figure a surface produces when it chooses its
// weights once, from the first model it sees. It is what these tests must
// not match.
func naivePromptCost(t *testing.T) (naive, perCall float64) {
	t.Helper()
	s := costtest.MixedPricingSession()
	w := cost.For(s.Models()[0])
	perCall, _ = cost.SessionCost(s.Invocations)
	naive = w.PromptCost(s.Usage())
	if naive == perCall {
		t.Fatal("the fixture no longer discriminates: both ways of pricing it agree")
	}
	return naive, perCall
}

func TestProfileTotalCostPricesEachCallAtItsOwnModel(t *testing.T) {
	naive, wantPrompt := naivePromptCost(t)
	s := costtest.MixedPricingSession()
	_, wantOutput := cost.SessionCost(s.Invocations)

	p := BuildProfile(s)
	if got := p.Usage.PromptCost.Value; got != wantPrompt {
		t.Errorf("prompt cost %v, want the per-call sum %v (the first model's weights give %v)",
			got, wantPrompt, naive)
	}
	if got := p.Usage.TotalCost.Value; got != wantPrompt+wantOutput {
		t.Errorf("total cost %v, want %v", got, wantPrompt+wantOutput)
	}
	// The headline number is the one a reader quotes, and on this session
	// the first model's weights overstate it by most of the cache-read bill.
	if p.Usage.PromptCost.Value == naive {
		t.Error("prompt cost is still the first model's weights over the session's totals")
	}
}

func TestProfileWriteShareSubtractsPerCallRatesNotSessionTotals(t *testing.T) {
	s := costtest.MixedPricingSession()
	promptCost, _ := cost.SessionCost(s.Invocations)
	want := cost.WriteCost(s.Invocations) / promptCost

	// What the subtraction gives when the input and cache-read terms are
	// taken off a per-call total at the first model's rates: a share of a
	// denominator the numerator does not belong to.
	w := cost.For(s.Models()[0])
	u := s.Usage()
	naive := (promptCost - float64(u.Input)*w.Input - float64(u.CacheRead)*w.CacheRead) /
		promptCost

	got := BuildProfile(s).Caching.WriteShareOfCost.Value
	if got != want {
		t.Errorf("write share %v, want %v", got, want)
	}
	if got == naive {
		t.Errorf("write share %v is still the session-total subtraction", got)
	}
	// A share of the bill, so it cannot be outside the bill.
	if got < 0 || got > 1 {
		t.Errorf("write share %v is not a share", got)
	}
}

func TestGenerationIsPricedPerCall(t *testing.T) {
	// This site was never observably wrong: Output is 5.0x for every model
	// published so far, so the first model's rate happened to be every
	// model's rate. It is fixed anyway, because "correct by coincidence"
	// stops being correct the day a model ships with a different output
	// multiple, and nothing would catch it then either -- the arithmetic is
	// what this pins.
	s := costtest.MixedPricingSession()
	_, wantOutput := cost.SessionCost(s.Invocations)

	thinking, rest := generationCost(s)
	if thinking+rest != wantOutput {
		t.Errorf("generation totals %v, want the per-call output sum %v",
			thinking+rest, wantOutput)
	}

	var wantThinking float64
	for _, inv := range s.Invocations {
		wantThinking += float64(inv.Usage.Thinking) * cost.For(inv.Model).Output
	}
	if thinking != wantThinking {
		t.Errorf("thinking generation %v, want %v", thinking, wantThinking)
	}
}

func TestUnattributedCeilingsUseTheDearestCacheReadInTheSession(t *testing.T) {
	s := costtest.MixedPricingSession()
	carry := analysis.Carry(s, analysis.Cache(s))

	dearest := cost.MaxCacheRead(s.Invocations)
	if dearest != cost.For(costtest.Expensive).CacheRead {
		t.Fatalf("the dearest read in the fixture is %v, want the expensive model's %v",
			dearest, cost.For(costtest.Expensive).CacheRead)
	}

	more := unattributedMore(s, carry, 1000)
	want := num(int(float64(carry.ThinkingTokens) * carry.AssistantRoundTrips * dearest))
	if !strings.Contains(more, want) {
		t.Errorf("the thinking ceiling is not priced at the dearest read %v: %s",
			dearest, more)
	}
	// A ceiling computed at the cheapest rate in the session is not a
	// ceiling, and here it would be four times too low.
	cheap := num(int(float64(carry.ThinkingTokens) * carry.AssistantRoundTrips *
		cost.For(costtest.Cheap).CacheRead))
	if cheap != want && strings.Contains(more, cheap) {
		t.Errorf("the ceiling is priced at the cheapest read in the session: %s", more)
	}
}
