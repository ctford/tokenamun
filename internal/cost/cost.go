// Package cost converts token volume into cost. Volume is not cost: Anthropic
// prompt caching prices cache reads at a tenth of fresh input and cache writes
// above it, so on real sessions raw prompt volume overstates cost by 6x to 8x.
// See METHODOLOGY.md section 3.
package cost

import (
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// Weights are per-class multiples of a model's own input price.
//
// The resulting unit -- the cost-weighted token -- is therefore MODEL-RELATIVE,
// and that is a limit, not a feature. One cost-weighted token means "one
// full-price input token of this model", so a total that spans two models adds
// quantities of different sizes. An earlier version of this comment claimed
// the opposite -- that the unit was comparable across models "in a way that
// dollars are not" -- and that claim is how a week of twelve Opus sessions and
// six Sonnet ones came to be summed into one figure without anyone noticing.
//
// Within one model it is exact and needs no price list, which is the reason to
// have it. Across models there is nothing here that makes the total
// commensurable: an earlier version of this comment sent the reader to a
// Prices type that does not exist. Reports say when a set spans two pricings;
// converting to money would need a price table, which is configuration rather
// than measurement and is deliberately not read.
//
// These are published Claude rates and are configuration, not measurement.
// Check them against your own bill.
type Weights struct {
	Input        float64 `json:"input"`
	CacheRead    float64 `json:"cache_read"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	Output       float64 `json:"output"`
}

// Default applies to the Opus, Sonnet and Haiku families.
var Default = Weights{
	Input:        1.0,
	CacheRead:    0.1,
	CacheWrite5m: 1.25,
	CacheWrite1h: 2.0,
	Output:       5.0,
}

// cheapCacheRead applies to the models that read cache at 0.025x rather than
// 0.1x, which moves every break-even.
var cheapCacheRead = Weights{
	Input:        1.0,
	CacheRead:    0.025,
	CacheWrite5m: 1.25,
	CacheWrite1h: 2.0,
	Output:       5.0,
}

// For returns the weights for a model ID. Unknown models get Default, which is
// the right failure mode: it is the common case, not a guess about a new
// model, and 0.1x over-states the cost of a model that turns out to be
// cheaper rather than under-stating it.
//
// The 0.025x read is a property of the 5.1 generation, NOT of the Fable
// family. Published rates: claude-fable-5-1 and claude-mythos-5-1 read at
// $0.25/MTok against $10 input; claude-fable-5 and claude-mythos-5 read at
// $1/MTok, which is the standard 0.1x. Matching on "fable" therefore priced a
// Fable 5 session's cache reads at a quarter of what they cost -- on the
// class that is ~97% of prompt volume, so very nearly a fourfold
// under-statement of the session.
func For(modelID string) Weights {
	id := strings.ToLower(modelID)
	switch {
	case strings.Contains(id, "fable-5-1"), strings.Contains(id, "mythos-5-1"):
		return cheapCacheRead
	default:
		return Default
	}
}

// PromptCost returns the cost of a usage's prompt side, in EIT.
//
// When the 5m/1h split is missing or inconsistent, the whole cache-creation
// total is charged at the 5-minute rate. That is the cheaper of the two write
// rates, so the estimate is conservative rather than flattering.
func (w Weights) PromptCost(u model.TokenUsage) float64 {
	c := float64(u.Input)*w.Input + float64(u.CacheRead)*w.CacheRead
	if u.TTLSplitConsistent() && (u.CacheCreation5m > 0 || u.CacheCreation1h > 0) {
		return c + float64(u.CacheCreation5m)*w.CacheWrite5m +
			float64(u.CacheCreation1h)*w.CacheWrite1h
	}
	return c + float64(u.CacheCreation)*w.CacheWrite5m
}

// OutputCost returns the cost of generated tokens, in EIT.
func (w Weights) OutputCost(u model.TokenUsage) float64 {
	return float64(u.Output) * w.Output
}

// PerCall prices one invocation using its own model's weights.
//
// Weights used to be chosen once per session, from the first model seen, and
// applied to every call in it. That is wrong the moment a session switches
// model -- which `opusplan` does on every plan-mode toggle -- and badly wrong
// for a model whose cache reads are priced differently: Fable reads at 0.025x
// against 0.1x, a fourfold error on the class that is 98% of the volume.
//
// The model is recorded on every invocation, so there is no reason to guess.
func PerCall(inv model.ModelInvocation) (prompt, output float64) {
	w := For(inv.Model)
	return w.PromptCost(inv.Usage), w.OutputCost(inv.Usage)
}

// SessionCost totals a session, pricing each call with its own model.
func SessionCost(invocations []model.ModelInvocation) (prompt, output float64) {
	for _, inv := range invocations {
		p, o := PerCall(inv)
		prompt += p
		output += o
	}
	return prompt, output
}

// Mixed reports whether more than one pricing applies across these calls.
//
// Worth knowing before quoting a total: within one model the unit is exact,
// and across models it is a sum of differently-sized things.
func Mixed(invocations []model.ModelInvocation) bool {
	var first *Weights
	for _, inv := range invocations {
		if !inv.IsRealCall() {
			continue
		}
		w := For(inv.Model)
		if first == nil {
			first = &w
			continue
		}
		if w != *first {
			return true
		}
	}
	return false
}
