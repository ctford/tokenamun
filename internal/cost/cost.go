// Package cost converts token volume into cost. Volume is not cost: Anthropic
// prompt caching prices cache reads at a tenth of fresh input and cache writes
// above it, so on real sessions raw prompt volume overstates cost by ~6x.
// See METHODOLOGY.md section 3.
package cost

import (
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// Weights are per-class multiples of a model's own input price. Because they
// are relative, the resulting unit -- the effective input-equivalent token
// (EIT) -- is comparable across models in a mixed session in a way that
// dollars are not.
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

// fable reads cache at 0.025x rather than 0.1x, which moves every break-even.
var fable = Weights{
	Input:        1.0,
	CacheRead:    0.025,
	CacheWrite5m: 1.25,
	CacheWrite1h: 2.0,
	Output:       5.0,
}

// For returns the weights for a model ID. Unknown models get Default, which is
// the right failure mode: it is the common case, not a guess about a new model.
func For(modelID string) Weights {
	id := strings.ToLower(modelID)
	switch {
	case strings.Contains(id, "fable"), strings.Contains(id, "mythos"):
		return fable
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
