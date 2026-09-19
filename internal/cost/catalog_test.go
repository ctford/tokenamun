package cost

import (
	"math"
	"strings"
	"testing"
)

// TestWeightsMatchPublishedRates checks every constant in this package against
// a published rate, for every model in the pinned extract.
//
// The weights are ratios to a model's own input price, so each published pair
// is one assertion: cache_read / input must equal Weights.CacheRead, and so on
// for both write rates and output. Before this existed the constants were
// checked against nothing, and one of them was wrong for a year -- matching on
// "fable" gave Fable 5 the 0.025x cache read that belongs to the 5.1
// generation, a fourfold under-statement on the class that is ~97% of prompt
// volume. Reintroducing that bug fails this test on claude-fable-5,
// claude-mythos-5 and claude-mythos-preview, which is the only evidence that
// the test is worth having.
//
// It does not catch the adjacent bug, and must not be described as if it did:
// verifying that a model's weights are right is a different thing from
// verifying that a call is priced with its own model's weights. A caller that
// prices a whole session at cost.For(firstModel) passes this test with every
// constant correct. Neither test catches the other's bug.
func TestWeightsMatchPublishedRates(t *testing.T) {
	c := catalog()
	if c.PriceUnit != "usd_per_token" {
		t.Fatalf("price_unit = %q, want usd_per_token", c.PriceUnit)
	}
	if c.Upstream.Commit == "" {
		t.Fatal("extract carries no upstream commit; a price with no pin is a rumour")
	}

	for id, rates := range c.Models {
		t.Run(id, func(t *testing.T) {
			if rates.Input <= 0 {
				t.Fatalf("input price %v is not a price", rates.Input)
			}
			w := For(id)
			if w.Input != 1.0 {
				t.Errorf("Input weight = %v, want 1.0: the unit is defined as a multiple of this model's input price", w.Input)
			}
			ratio := func(rate float64) float64 { return rate / rates.Input }
			assertWeight(t, "cache_read", ratio(rates.CacheRead), w.CacheRead)
			assertWeight(t, "cache_write_5m", ratio(rates.CacheWrite5m), w.CacheWrite5m)
			assertWeight(t, "output", ratio(rates.Output), w.Output)
			if rates.CacheWrite1h == nil {
				// Upstream omits it on a couple of legacy aliases. Skipping is
				// right: there is no published rate here to check against.
				t.Log("no published 1h cache-write rate upstream")
				return
			}
			assertWeight(t, "cache_write_1h", ratio(*rates.CacheWrite1h), w.CacheWrite1h)
		})
	}
}

// assertWeight compares a published ratio with a constant. The tolerance is
// for float division, not for disagreement: 0.12 against 0.1 is a wrong
// constant, not a rounding error.
func assertWeight(t *testing.T, class string, published, constant float64) {
	t.Helper()
	if math.Abs(published-constant) > 1e-9 {
		t.Errorf("%s: published rate is %vx input, cost.For says %vx", class, published, constant)
	}
}

// TestCatalogCoversTheModelsWeSpecialCase guards the other direction. For()
// carries three special cases; if a refresh drops the models they name, the
// table test above still passes while checking nothing about them.
func TestCatalogCoversTheModelsWeSpecialCase(t *testing.T) {
	c := catalog()
	for _, id := range []string{
		"claude-fable-5", "claude-fable-5-1",
		"claude-mythos-5", "claude-mythos-5-1",
		"claude-3-haiku-20240307",
	} {
		if _, ok := c.Models[id]; !ok {
			t.Errorf("%s is special-cased in For() but absent from the extract", id)
		}
	}
}

// TestInputPriceKnowsTheModelsWeProfile is the cheerful half. EIT is defined
// as a multiple of a model's input price, so this one number is the whole
// conversion to money.
func TestInputPriceKnowsTheModelsWeProfile(t *testing.T) {
	for _, id := range []string{
		"claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5",
		"claude-fable-5-1", "claude-mythos-5-1",
	} {
		price, ok := InputPrice(id)
		if !ok {
			t.Errorf("%s: no price", id)
			continue
		}
		if price <= 0 || price > 1e-3 {
			t.Errorf("%s: %v USD/token is not a plausible input price", id, price)
		}
	}
	if got, _ := InputPrice("CLAUDE-OPUS-5"); got == 0 {
		t.Error("case should not decide whether a model has a price")
	}
}

// TestInputPriceRefusesWhatItDoesNotKnow is the half that matters. A default
// price is a wrong bill that looks like a right one, and silent fall-through
// is how the fable bug survived as long as it did.
func TestInputPriceRefusesWhatItDoesNotKnow(t *testing.T) {
	for _, id := range []string{
		"",
		"gpt-4o",
		"claude-opus-6",
		// Resold, at the reseller's rates. Not a spelling of the bare key.
		"eu.anthropic.claude-opus-5",
		"bedrock/us-gov-east-1/anthropic.claude-opus-5",
		"databricks/claude-opus-5",
	} {
		if price, ok := InputPrice(id); ok {
			t.Errorf("%q priced at %v; an unknown model must have no price, not a default one", id, price)
		}
	}
}

// TestCatalogPinNamesTheCommit guards the line that has to appear beside every
// dollar figure. "From LiteLLM" is not a source: published rates move, so the
// answer has to name the commit they were read from.
func TestCatalogPinNamesTheCommit(t *testing.T) {
	pin := CatalogPin()
	c := catalog()
	for _, want := range []string{c.Upstream.Repo, c.Upstream.Commit[:12], c.Upstream.Retrieved} {
		if want == "" || !strings.Contains(pin, want) {
			t.Errorf("pin %q does not name %q", pin, want)
		}
	}
}
