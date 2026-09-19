package analysis

import (
	"testing"
	"time"

	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/cost/costtest"
	"github.com/ctford/tokenamun/internal/model"
)

// The fourth and last copy of the same mistake: choose the weights once,
// from the first model in the session, and apply them to every send. Carry
// is where it mattered most, because carry is nearly all cache reads and the
// cache read is the only weight that differs between models.

// A residency span that crosses a model switch is billed at both models,
// which is the whole reason the span had to be walked rather than counted.
func TestCarryPricesEachSendAtTheModelOfTheCallItWentOutOn(t *testing.T) {
	// Two calls on the expensive pricing, then one on the cheap. Content
	// that arrives on the first call is written on the second and read on
	// the third, so its cost is one model's write and the other's read.
	expensive, cheap := cost.For(costtest.Expensive), cost.For(costtest.Cheap)
	s := &model.Session{
		Invocations: []model.ModelInvocation{
			inv(0, 0, costtest.Expensive, "2.1.246", "high", 1, 0, 10_000),
			inv(1, time.Minute, costtest.Expensive, "2.1.246", "high", 1, 10_000, 0),
			inv(2, 2*time.Minute, costtest.Cheap, "2.1.246", "high", 1, 10_000, 0),
		},
		Retrievals: []model.RetrievedContent{{
			Seq: 0, Tool: "Read", Path: "a.go", InvocationSeq: 0,
			Bytes: 3_600, Tokens: 1_000,
		}},
	}

	r := Carry(s, Cache(s, TTL5m))
	if len(r.Items) != 1 {
		t.Fatalf("expected one carried item, got %d", len(r.Items))
	}
	got := r.Items[0].CarryEIT

	want := 1_000 * (expensive.CacheWrite5m + cheap.CacheRead)
	if got != want {
		t.Errorf("carry %v, want %v: the write on the expensive model and the "+
			"read on the cheap one", got, want)
	}
	// What the session-wide choice of weights gave: the cheap model's read
	// priced at the expensive model's rate, four times over.
	naive := 1_000 * (expensive.CacheWrite5m + expensive.CacheRead)
	if got == naive {
		t.Errorf("carry %v is still the first model's weights over the whole span", got)
	}
}

// The first send is the first call of the span, not a subtraction from the
// warm total. They agree unless that call was itself cold, where the old
// arithmetic charged a write for the cold call and a second for the
// substitution.
func TestAColdFirstSendIsOneWriteNotTwo(t *testing.T) {
	w := cost.For(costtest.Expensive)
	// Call 1 rebuilds the prefix, so it is cold; call 2 reads it.
	s := &model.Session{
		Invocations: []model.ModelInvocation{
			inv(0, 0, costtest.Expensive, "2.1.246", "high", 1, 0, 10_000),
			inv(1, 30*time.Minute, costtest.Expensive, "2.1.246", "high", 1, 0, 20_000),
			inv(2, 31*time.Minute, costtest.Expensive, "2.1.246", "high", 1, 20_000, 0),
		},
		Retrievals: []model.RetrievedContent{{
			Seq: 0, Tool: "Read", Path: "a.go", InvocationSeq: 0,
			Bytes: 3_600, Tokens: 1_000,
		}},
	}
	report := Cache(s, TTL5m)
	if !ColdCalls(report)[1] {
		t.Fatal("the fixture needs call 1 to be a cache miss")
	}

	got := Carry(s, report).Items[0].CarryEIT
	want := 1_000 * (w.CacheWrite5m + w.CacheRead)
	if got != want {
		t.Errorf("carry %v, want %v: one write for the cold call the content "+
			"arrived into, then a read", got, want)
	}
}

// Carry over the shared fixture must agree with pricing every call itself,
// and the accounting invariant holds whichever weights apply.
func TestCarryOverTheMixedFixtureNeverExceedsWhatWasBilled(t *testing.T) {
	s := costtest.MixedPricingSession()
	r := Carry(s, Cache(s, TTL5m))

	wantPrompt, _ := cost.SessionCost(s.Invocations)
	if r.PromptCostEIT != wantPrompt {
		t.Errorf("prompt cost %v, want the per-call sum %v", r.PromptCostEIT, wantPrompt)
	}
	// AGENTS.md's invariant, as a property rather than a number: what it
	// cost to keep the preamble cannot exceed what the prompts cost.
	if r.PreambleCarryEIT > r.PromptCostEIT {
		t.Errorf("preamble carry %v exceeds the whole prompt bill %v",
			r.PreambleCarryEIT, r.PromptCostEIT)
	}
	var items float64
	for _, it := range r.Items {
		items += it.CarryEIT
	}
	if items > r.PromptCostEIT {
		t.Errorf("carried items come to %v against a prompt bill of %v",
			items, r.PromptCostEIT)
	}
}

// cache.go was fixed first and is the one surface that already passed this,
// by bucketing its counterfactual per pricing. The fixture is pointed at it
// too, so that the bucketing cannot quietly go away.
func TestTheTTLCounterfactualKeepsBothPricingsApart(t *testing.T) {
	s := costtest.MixedPricingSession()
	rates := Cache(s, TTL5m).ReadRates
	if len(rates) != 2 {
		t.Fatalf("read rates %v, want both pricings in the fixture", rates)
	}
	for _, want := range []float64{
		cost.For(costtest.Expensive).CacheRead, cost.For(costtest.Cheap).CacheRead,
	} {
		var found bool
		for _, r := range rates {
			found = found || r == want
		}
		if !found {
			t.Errorf("read rate %v is missing from %v", want, rates)
		}
	}
}
