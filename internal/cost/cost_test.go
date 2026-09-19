package cost

import (
	"math"
	"testing"

	"github.com/ctford/tokenamun/internal/model"
)

func eq(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestInputPricesEachClassSeparately(t *testing.T) {
	u := model.TokenUsage{Input: 100, CacheRead: 1000, CacheCreation: 200, CacheCreation5m: 200}
	// 100*1.0 + 1000*0.1 + 200*1.25 = 100 + 100 + 250
	eq(t, Default.PromptCost(u), 450)
}

func TestCacheReadIsAnOrderOfMagnitudeCheaper(t *testing.T) {
	// The property that makes volume-based ranking wrong: the same token count
	// costs 10x less when it is served from cache.
	read := model.TokenUsage{CacheRead: 1_000_000}
	fresh := model.TokenUsage{Input: 1_000_000}
	if Default.PromptCost(fresh) != 10*Default.PromptCost(read) {
		t.Fatalf("expected fresh input to cost 10x a cache read, got %v vs %v",
			Default.PromptCost(fresh), Default.PromptCost(read))
	}
}

func TestOneHourWritesCostMoreThanFiveMinute(t *testing.T) {
	short := model.TokenUsage{CacheCreation: 1000, CacheCreation5m: 1000}
	long := model.TokenUsage{CacheCreation: 1000, CacheCreation1h: 1000}
	if Default.PromptCost(long) <= Default.PromptCost(short) {
		t.Fatal("a 1h cache write must cost more than a 5m one, or the TTL " +
			"counterfactual can never come out negative")
	}
}

func TestMissingTTLSplitFallsBackToTheCheaperWriteRate(t *testing.T) {
	// Conservative: an unknown split must not inflate the reported cost.
	noSplit := model.TokenUsage{CacheCreation: 1000}
	eq(t, Default.PromptCost(noSplit), 1250)

	inconsistent := model.TokenUsage{CacheCreation: 1000, CacheCreation5m: 10, CacheCreation1h: 10}
	if inconsistent.TTLSplitConsistent() {
		t.Fatal("split should be detected as inconsistent")
	}
	eq(t, Default.PromptCost(inconsistent), 1250)
}

func TestTheCheapCacheReadBelongsToAGenerationNotAFamily(t *testing.T) {
	// Published rates: claude-fable-5-1 and claude-mythos-5-1 read cache at
	// $0.25/MTok against $10 input; claude-fable-5 and claude-mythos-5 read
	// at $1/MTok, the standard 0.1x. Matching the family substring gave
	// Fable 5 the cheaper rate and under-stated its cache reads fourfold --
	// on the class that is most of prompt volume. Each boundary is named,
	// because the earlier test only ever asked about 5.1.
	cheap := []string{"claude-fable-5-1", "claude-mythos-5-1"}
	standard := []string{
		"claude-fable-5", "claude-mythos-5",
		"claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5", "something-new",
	}
	for _, id := range cheap {
		if For(id).CacheRead != 0.025 {
			t.Errorf("%s reads cache at 0.025x, got %v", id, For(id).CacheRead)
		}
	}
	for _, id := range standard {
		if For(id) != Default {
			t.Errorf("%s uses the default weights, got %+v", id, For(id))
		}
	}

	// And the discount has to actually reach the arithmetic.
	u := model.TokenUsage{CacheRead: 1_000_000}
	if For("claude-fable-5-1").PromptCost(u) >= For("claude-opus-5").PromptCost(u) {
		t.Error("the cheaper read rate did not reach PromptCost")
	}
}

func TestReferenceDatasetRatio(t *testing.T) {
	// The measured headline from docs/METHODOLOGY.md section 3: raw prompt volume
	// overstates cost by ~6x. Guards the weights against silent edits.
	u := model.TokenUsage{
		Input:           4_058,
		CacheRead:       807_112_468,
		CacheCreation:   49_284_083,
		CacheCreation5m: 49_284_083,
	}
	raw := float64(u.PromptTokens())
	eit := Default.PromptCost(u)
	if ratio := raw / eit; ratio < 5.9 || ratio > 6.1 {
		t.Fatalf("expected raw/EIT ratio ~6.0, got %.2f", ratio)
	}
}
