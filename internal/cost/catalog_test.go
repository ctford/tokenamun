package cost

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

// catalogPath is the pinned LiteLLM extract. scripts/refresh-prices.sh writes
// it; nothing at run time fetches it.
const catalogPath = "litellm-prices.json"

// catalogRates are one model's published rates, in USD per token.
//
// CacheWrite1h is a pointer because upstream omits it on a few legacy aliases
// and an omitted rate is not a free one.
type catalogRates struct {
	Input        float64  `json:"input"`
	CacheRead    float64  `json:"cache_read"`
	CacheWrite5m float64  `json:"cache_write_5m"`
	CacheWrite1h *float64 `json:"cache_write_1h"`
	Output       float64  `json:"output"`
}

type catalogFile struct {
	Upstream struct {
		Repo      string `json:"repo"`
		File      string `json:"file"`
		Commit    string `json:"commit"`
		Retrieved string `json:"retrieved"`
	} `json:"upstream"`
	PriceUnit string                  `json:"price_unit"`
	Models    map[string]catalogRates `json:"models"`
}

func loadCatalog(t *testing.T) catalogFile {
	t.Helper()
	raw, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read %s: %v", catalogPath, err)
	}
	var c catalogFile
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parse %s: %v", catalogPath, err)
	}
	if len(c.Models) == 0 {
		t.Fatalf("%s has no models", catalogPath)
	}
	return c
}

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
	c := loadCatalog(t)
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
	c := loadCatalog(t)
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
