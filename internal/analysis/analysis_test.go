package analysis

import (
	"math"
	"testing"
	"time"

	"github.com/ctford/tokenamun/internal/model"
)

// inv builds an invocation for a table-driven test.
func inv(seq int, at time.Duration, modelID, version, effort string, in, read, create int64) model.ModelInvocation {
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	return model.ModelInvocation{
		Seq: seq, Model: modelID, Version: version, Effort: effort,
		Timestamp: base.Add(at),
		Usage: model.TokenUsage{
			Input: in, CacheRead: read,
			CacheCreation: create, CacheCreation5m: create,
		},
	}
}

func session(invs ...model.ModelInvocation) *model.Session {
	return &model.Session{Invocations: invs}
}

func TestMissCausesAreAttributedToObservableCausesFirst(t *testing.T) {
	// Each of these misses would have happened under any TTL, so none of them
	// may be blamed on expiry even though the gap is long.
	long := 30 * time.Minute
	cases := []struct {
		name      string
		prev, cur model.ModelInvocation
		want      string
	}{
		{"model switch",
			inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 50_000, 1000),
			inv(1, long, "claude-sonnet-5", "2.1.246", "high", 1, 0, 60_000),
			CauseModelSwitch},
		{"claude code upgrade",
			inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 50_000, 1000),
			inv(1, long, "claude-opus-5", "2.1.251", "high", 1, 0, 60_000),
			CauseUpgrade},
		{"effort change",
			inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 50_000, 1000),
			inv(1, long, "claude-opus-5", "2.1.246", "max", 1, 0, 60_000),
			CauseEffortChange},
		{"ttl expiry only when nothing else explains it",
			inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 50_000, 1000),
			inv(1, long, "claude-opus-5", "2.1.246", "high", 1, 0, 60_000),
			CauseTTLExpiry},
		{"a short gap with no other cause is unexplained, not expiry",
			inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 50_000, 1000),
			inv(1, 10*time.Second, "claude-opus-5", "2.1.246", "high", 1, 0, 60_000),
			CauseUnexplained},
	}
	for _, c := range cases {
		r := Cache(session(c.prev, c.cur))
		var found string
		for _, m := range r.Misses {
			if m.Seq == 1 {
				found = m.Cause
			}
		}
		if found != c.want {
			t.Errorf("%s: cause = %q, want %q", c.name, found, c.want)
		}
	}
}

func TestCompactionIsNotMistakenForExpiry(t *testing.T) {
	// A sharp fall in prompt size is a rebuilt, shorter history -- not a cache
	// that timed out.
	r := Cache(session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 200_000, 1000),
		inv(1, 20*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 0, 30_000),
	))
	for _, m := range r.Misses {
		if m.Seq == 1 && m.Cause != CauseCompaction {
			t.Errorf("cause = %q, want compaction", m.Cause)
		}
	}
}

func TestSmallWritesAreNotMisses(t *testing.T) {
	// Ordinary growth writes a little to cache on every call. Calling that a
	// miss would make the expiry figure meaningless.
	r := Cache(session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 100_000, 1500),
		inv(1, time.Minute, "claude-opus-5", "2.1.246", "high", 1, 101_500, 1200),
	))
	if len(r.Misses) != 0 {
		t.Fatalf("expected no misses, got %+v", r.Misses)
	}
}

func TestExpiryShareIsPricedNotCounted(t *testing.T) {
	s := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 30_000),
		inv(1, time.Minute, "claude-opus-5", "2.1.246", "high", 1, 30_000, 500),
		inv(2, 20*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 0, 40_000),
	)
	r := Cache(s)
	if r.ExpiryCostEIT <= 0 {
		t.Fatal("the expiry should have a cost")
	}
	// Priced at the write multiplier, not counted as raw tokens.
	if math.Abs(r.ExpiryCostEIT-40_000*1.25) > 1 {
		t.Errorf("expiry cost = %.0f, want 40000 x 1.25", r.ExpiryCostEIT)
	}
	if r.ExpiryShare <= 0 || r.ExpiryShare > 1 {
		t.Errorf("expiry share = %.3f, must be a fraction of prompt cost", r.ExpiryShare)
	}
}

func TestLongerTTLOnlyClaimsGapsItWouldHaveCovered(t *testing.T) {
	// A two-hour gap expires under a one-hour TTL too, so it must not be
	// counted as avoidable.
	s := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 10_000),
		inv(1, 20*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 0, 50_000),
		inv(2, 2*time.Hour+20*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 0, 60_000),
	)
	r := Cache(s)
	var avoidable, notAvoidable int
	for _, m := range r.Misses {
		if m.Cause != CauseTTLExpiry {
			continue
		}
		if m.AvoidableByTTL {
			avoidable++
		} else {
			notAvoidable++
		}
	}
	if avoidable != 1 || notAvoidable != 1 {
		t.Fatalf("expected one avoidable and one not, got %d and %d", avoidable, notAvoidable)
	}
}

func TestObservedTTLComesFromTheAPISplit(t *testing.T) {
	s := session(inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 10_000))
	if got := Cache(s).ObservedTTL; got != "5m" {
		t.Errorf("observed TTL = %q, want 5m", got)
	}

	oneHour := inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 10_000)
	oneHour.Usage.CacheCreation5m = 0
	oneHour.Usage.CacheCreation1h = 10_000
	if got := Cache(session(oneHour)).ObservedTTL; got != "1h" {
		t.Errorf("observed TTL = %q, want 1h", got)
	}
}

func TestCarryPricesResidencyNotSize(t *testing.T) {
	// The point of the whole analysis: an item that arrives early costs more
	// than an identical item that arrives late.
	s := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 10_000, 100),
		inv(1, time.Minute, "claude-opus-5", "2.1.246", "high", 1, 20_000, 100),
		inv(2, 2*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 30_000, 100),
		inv(3, 3*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 40_000, 100),
	)
	s.Retrievals = []model.RetrievedContent{
		{Seq: 0, Tool: "Read", Path: "early.go", Bytes: 3600, Tokens: 1000, InvocationSeq: 0},
		{Seq: 1, Tool: "Read", Path: "late.go", Bytes: 3600, Tokens: 1000, InvocationSeq: 2},
	}
	r := Carry(s, Cache(s))

	if len(r.Items) != 2 {
		t.Fatalf("expected both items, got %d", len(r.Items))
	}
	if r.Items[0].Path != "early.go" {
		t.Fatalf("ranking should put the early item first, got %q", r.Items[0].Path)
	}
	if r.Items[0].ResidentFor <= r.Items[1].ResidentFor {
		t.Error("the earlier item must be resident for more calls")
	}
	if r.Items[0].CarryEIT <= r.Items[1].CarryEIT {
		t.Error("identical content arriving earlier must cost more to carry")
	}
}

func TestCarryNeverExceedsTheSessionsPromptCost(t *testing.T) {
	// Accounting invariant from AGENTS.md: you cannot carry more than you paid.
	s := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 10_000, 500),
		inv(1, time.Minute, "claude-opus-5", "2.1.246", "high", 1, 20_000, 500),
		inv(2, 2*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 30_000, 500),
	)
	s.Retrievals = []model.RetrievedContent{
		{Tool: "Read", Path: "a.go", Bytes: 3600, Tokens: 1000, InvocationSeq: 0},
	}
	r := Carry(s, Cache(s))
	var total float64
	for _, it := range r.Items {
		total += it.CarryEIT
	}
	if total+r.PreambleCarryEIT > r.PromptCostEIT*1.05 {
		t.Fatalf("attributed carry %.0f exceeds the session's prompt cost %.0f",
			total+r.PreambleCarryEIT, r.PromptCostEIT)
	}
}

func TestResidencyStopsAtAContextReset(t *testing.T) {
	// Content does not survive compaction, so it must not be charged for
	// calls after one.
	s := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 100_000, 500),
		inv(1, time.Minute, "claude-opus-5", "2.1.246", "high", 1, 110_000, 500),
		inv(2, 2*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 5_000, 500),
		inv(3, 3*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 6_000, 500),
	)
	s.Retrievals = []model.RetrievedContent{
		{Tool: "Read", Path: "a.go", Bytes: 3600, Tokens: 1000, InvocationSeq: 0},
	}
	r := Carry(s, Cache(s))
	if len(r.Resets) == 0 {
		t.Fatal("the sharp drop at call 2 should be detected as a reset")
	}
	if got := r.Items[0].ResidentFor; got != 1 {
		t.Fatalf("resident for %d calls, want 1 (stopped by the reset at call 2)", got)
	}
}

func TestAPIErrorCallsAreNotReadAsResets(t *testing.T) {
	// A zero-prompt call is an error entry, not a context that collapsed.
	s := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 100_000, 500),
		inv(1, time.Minute, "<synthetic>", "2.1.246", "high", 0, 0, 0),
		inv(2, 2*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 110_000, 500),
	)
	r := Carry(s, Cache(s))
	if len(r.Resets) != 0 {
		t.Fatalf("expected no resets, got %v", r.Resets)
	}
}

func TestPreambleIsObservedAndCarriedByEveryCall(t *testing.T) {
	s := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 28_000),
		inv(1, time.Minute, "claude-opus-5", "2.1.246", "high", 1, 28_000, 500),
		inv(2, 2*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 28_500, 500),
	)
	r := Carry(s, Cache(s))
	if r.Preamble != 28_001 {
		t.Fatalf("preamble = %d, want the first call's whole prompt", r.Preamble)
	}
	if r.PreambleCarryEIT <= 0 {
		t.Error("carrying the preamble across the session has a cost")
	}
	if r.PreambleShare <= 0 || r.PreambleShare > 1 {
		t.Errorf("preamble share = %.3f, must be a fraction", r.PreambleShare)
	}
}

func TestErrorEntriesAreNotTreatedAsThePreviousCall(t *testing.T) {
	// Regression: a <synthetic> entry stands in for a failed request. Reading
	// it as the predecessor made the next real call look like a model switch,
	// which misattributed a genuine TTL expiry -- and pointed at the wrong fix.
	s := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 50_000, 1000),
		inv(1, time.Minute, model.SyntheticModel, "2.1.246", "high", 0, 0, 0),
		inv(2, 40*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 11_000, 350_000),
	)
	r := Cache(s)

	var got string
	for _, m := range r.Misses {
		if m.Seq == 2 {
			got = m.Cause
		}
	}
	if got != CauseTTLExpiry {
		t.Fatalf("cause = %q, want ttl_expiry: the model did not change, an error entry sat in between", got)
	}
	if r.ByCause[CauseModelSwitch].Calls != 0 {
		t.Error("no model switch occurred and none should be reported")
	}
	// The gap must be measured from the last real request, not the error entry.
	for _, m := range r.Misses {
		if m.Seq == 2 && m.GapSecond < 60*40 {
			t.Errorf("gap = %.0fs, want the interval since the last real call", m.GapSecond)
		}
	}
}

func TestRebuildAfterCompactionIsAttributedToTheCompaction(t *testing.T) {
	// The prompt collapses on the summarising call, but the new prefix is
	// written on the one after it. That rebuild belongs to the compaction
	// rather than being left unexplained.
	s := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 100_000, 500),
		inv(1, time.Minute, "claude-opus-5", "2.1.246", "high", 1, 6_000, 0),
		inv(2, 90*time.Second, "claude-opus-5", "2.1.246", "high", 1, 400, 6_100),
	)
	r := Cache(s)
	for _, m := range r.Misses {
		if m.Seq == 2 && m.Cause != CauseCompaction {
			t.Fatalf("cause = %q, want compaction", m.Cause)
		}
	}
	if r.ByCause[CauseUnexplained].Calls != 0 {
		t.Error("nothing should be left unexplained here")
	}
}

// Seq is a label on an invocation, not its position in the slice. Stepping
// back twice used to pass the first result's Seq where an index was wanted,
// which is correct only for as long as ingest keeps numbering invocations by
// position. Numbered from ten, the old code indexed past the end.
func TestCompactionIsFoundWhenSeqIsNotTheSliceIndex(t *testing.T) {
	s := session(
		inv(10, 0, "claude-opus-5", "2.1.246", "high", 1, 100_000, 500),
		inv(11, time.Minute, "claude-opus-5", "2.1.246", "high", 1, 6_000, 0),
		inv(12, 90*time.Second, "claude-opus-5", "2.1.246", "high", 1, 400, 6_100),
	)
	r := Cache(s)
	for _, m := range r.Misses {
		if m.Seq == 12 && m.Cause != CauseCompaction {
			t.Errorf("cause = %q, want compaction", m.Cause)
		}
	}
	if r.ByCause[CauseUnexplained].Calls != 0 {
		t.Error("nothing should be left unexplained here")
	}
}

func TestMergeAddsCostsAndReconcilesTheTTL(t *testing.T) {
	// Cost is additive across sessions: each report was computed against its
	// own session's weights and its own gaps, so the totals add.
	a := CacheReport{
		ObservedTTL: "5m", Writes5m: 1000, TotalCostEIT: 5000,
		ExpiryCostEIT: 1000, AvoidableTokens: 800, LongerTTLNetEIT: -400,
		ByCause: map[string]CauseAgg{"ttl_expiry": {Calls: 2, Tokens: 800, CostEIT: 1000}},
		// Misses are session-shaped: their sequence numbers mean nothing once
		// two sessions are in one list.
		Misses: []Miss{{Seq: 3, Cause: CauseTTLExpiry}},
	}
	b := CacheReport{
		ObservedTTL: "1h", Writes1h: 500, TotalCostEIT: 3000,
		ExpiryCostEIT: 300, AvoidableTokens: 100, LongerTTLNetEIT: 200,
		ByCause: map[string]CauseAgg{
			"ttl_expiry":   {Calls: 1, Tokens: 100, CostEIT: 300},
			"model_switch": {Calls: 1, Tokens: 50, CostEIT: 60},
		},
		Misses: []Miss{{Seq: 3, Cause: CauseModelSwitch}},
	}

	m := Merge([]CacheReport{a, b})
	if m.TotalCostEIT != 8000 || m.ExpiryCostEIT != 1300 {
		t.Errorf("costs did not add: %.0f total, %.0f expiry", m.TotalCostEIT, m.ExpiryCostEIT)
	}
	if m.AvoidableTokens != 900 || m.LongerTTLNetEIT != -200 {
		t.Errorf("the TTL figures did not add: %d avoidable, %.0f net",
			m.AvoidableTokens, m.LongerTTLNetEIT)
	}
	// A set where some sessions ran 5m and others 1h is a different situation
	// from either, and reporting "5m" would hide it.
	if m.ObservedTTL != "mixed across sessions" {
		t.Errorf("observed TTL = %q, want it to admit the mixture", m.ObservedTTL)
	}
	// Causes combine by name; shares are recomputed against the new total.
	if got := m.ByCause["ttl_expiry"]; got.Calls != 3 || got.CostEIT != 1300 {
		t.Errorf("ttl_expiry merged to %+v", got)
	}
	if got := m.ByCause["ttl_expiry"].Share; got < 0.162 || got > 0.163 {
		t.Errorf("share = %.4f, want 1300/8000", got)
	}
	// And the session-shaped narrative is dropped rather than concatenated
	// into a list that reads like one session's history.
	if len(m.Misses) != 0 {
		t.Errorf("individual misses must not survive a merge, got %d", len(m.Misses))
	}

	// One report in, the same report out.
	if one := Merge([]CacheReport{a}); one.ObservedTTL != "5m" ||
		one.TotalCostEIT != a.TotalCostEIT {
		t.Errorf("merging one report changed it: %+v", one)
	}
}

func TestLongerTTLPricesEachModelsWritesAtItsOwnRate(t *testing.T) {
	// A session that switches model, which `opusplan` does on every plan-mode
	// toggle. The 5.1 generation reads cache at 0.025x against 0.1x, so
	// pricing the whole session at whichever model came first gets the
	// counterfactual wrong -- and which way depends on the order, which is
	// the tell that it was never a rounding matter.
	s := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 100_000),
		inv(1, 20*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 0, 100_000),
		inv(2, 25*time.Minute, "claude-fable-5-1", "2.1.246", "high", 1, 0, 100_000),
		inv(3, 45*time.Minute, "claude-fable-5-1", "2.1.246", "high", 1, 0, 100_000),
	)
	r := Cache(s)

	if got := r.AvoidableTokens; got != 200_000 {
		t.Fatalf("avoidable = %d, want 200000 (one expiry under each pricing)", got)
	}

	// Standard pricing: 200k written at 1.25 becomes 100k at 2.0 plus 100k
	// read at 0.1, so -40,000. The 5.1 pricing differs only in the read,
	// 0.025, so -47,500.
	const want = -87_500
	if math.Abs(r.LongerTTLNetEIT-want) > 1 {
		t.Errorf("net = %.0f, want %d", r.LongerTTLNetEIT, want)
	}
	// Pricing it all at the first model seen gives -80,000, and all at the
	// last -95,000. Either would pass a tolerance wide enough to be useless.
	for _, wrong := range []float64{-80_000, -95_000} {
		if math.Abs(r.LongerTTLNetEIT-wrong) < 1 {
			t.Errorf("net = %.0f, which is the single-pricing answer", r.LongerTTLNetEIT)
		}
	}
}

func TestAvoidabilityIsCountedPerCallNotFlaggedPerCause(t *testing.T) {
	// Two expiries in one cause row: one gap a longer lifetime would have
	// covered, one it would not. A bool here said "avoidable" about both.
	s := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 10_000),
		inv(1, 20*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 0, 50_000),
		inv(2, 2*time.Hour+20*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 0, 60_000),
	)
	agg := Cache(s).ByCause[CauseTTLExpiry]

	if agg.Calls != 2 {
		t.Fatalf("expiry calls = %d, want 2", agg.Calls)
	}
	if agg.AvoidableCalls != 1 {
		t.Errorf("avoidable calls = %d, want 1", agg.AvoidableCalls)
	}
	if agg.AvoidableTokens != 50_000 {
		t.Errorf("avoidable tokens = %d, want 50000 (not the row's 110,000)", agg.AvoidableTokens)
	}
}

func TestLongerTTLNetIsThePremiumMinusTheSaving(t *testing.T) {
	// The identity that makes the two halves worth printing. If they do not
	// reconstruct the net, the breakdown is decoration.
	for _, tc := range []struct {
		name string
		s    *model.Session
	}{
		{"idles, so there is something to avoid", session(
			inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 10_000),
			inv(1, 20*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 0, 50_000),
		)},
		{"short bursts, so there is not", session(
			inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 10_000),
			inv(1, time.Minute, "claude-opus-5", "2.1.246", "high", 1, 0, 50_000),
		)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Cache(tc.s)
			if got := r.LongerTTLPremiumEIT - r.LongerTTLSavedEIT; math.Abs(got-r.LongerTTLNetEIT) > 1e-6 {
				t.Errorf("premium %.1f - saved %.1f = %.1f, but net is %.1f",
					r.LongerTTLPremiumEIT, r.LongerTTLSavedEIT, got, r.LongerTTLNetEIT)
			}
			if r.LongerTTLSavedEIT < 0 || r.LongerTTLPremiumEIT < 0 {
				t.Errorf("halves must be magnitudes, got saved %.1f premium %.1f",
					r.LongerTTLSavedEIT, r.LongerTTLPremiumEIT)
			}
		})
	}
}

func TestSwitchingCostsMoneyWhenNothingIdles(t *testing.T) {
	// The result the whole calculation exists to be able to produce: a
	// session of short bursts buys a lifetime it never uses and pays the 2.0x
	// write premium on everything.
	s := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 10_000),
		inv(1, time.Minute, "claude-opus-5", "2.1.246", "high", 1, 0, 50_000),
		inv(2, 2*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 0, 50_000),
	)
	r := Cache(s)

	if r.LongerTTLSavedEIT != 0 {
		t.Errorf("saved = %.1f, want 0: nothing expired", r.LongerTTLSavedEIT)
	}
	// 110,000 written, all of it repriced by 0.75.
	if want := 82_500.0; math.Abs(r.LongerTTLPremiumEIT-want) > 1 {
		t.Errorf("premium = %.1f, want %.1f", r.LongerTTLPremiumEIT, want)
	}
	if r.LongerTTLNetEIT <= 0 {
		t.Errorf("net = %.1f, want positive: switching should cost money here", r.LongerTTLNetEIT)
	}
}

func TestGapBoundariesAreConsistentAtBothLifetimes(t *testing.T) {
	// A cache entry's clock runs from request start, so a call arriving at
	// exactly the lifetime is the last one still warm. Both boundaries have
	// to agree on that or the counterfactual contradicts the attribution:
	// expiry needs gap > 5m, and staying warm at an hour needs gap <= 1h.
	for _, tc := range []struct {
		name      string
		gap       time.Duration
		cause     string
		avoidable bool
	}{
		{"a second inside 5m", TTL5m - time.Second, CauseUnexplained, false},
		{"exactly 5m", TTL5m, CauseUnexplained, false},
		{"a second past 5m", TTL5m + time.Second, CauseTTLExpiry, true},
		{"a second inside an hour", TTL1h - time.Second, CauseTTLExpiry, true},
		{"exactly an hour", TTL1h, CauseTTLExpiry, true},
		{"a second past an hour", TTL1h + time.Second, CauseTTLExpiry, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := session(
				inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 10_000),
				inv(1, tc.gap, "claude-opus-5", "2.1.246", "high", 1, 0, 50_000),
			)
			r := Cache(s)
			var got Miss
			for _, m := range r.Misses {
				if m.Seq == 1 {
					got = m
				}
			}
			if got.Cause != tc.cause {
				t.Errorf("cause = %q, want %q", got.Cause, tc.cause)
			}
			if got.AvoidableByTTL != tc.avoidable {
				t.Errorf("avoidable = %v, want %v", got.AvoidableByTTL, tc.avoidable)
			}
		})
	}
}

func TestBreakEvenIsWhereTheAvoidedReadIsPaidFor(t *testing.T) {
	// 0.75W = 1.9a, so break-even is a/W = 39.47%, not the 0.75/2.0 = 37.5%
	// you get by treating an avoided rewrite as free. Here W is 1,900,000 and
	// the avoidable part 750,000, which lands exactly on it.
	s := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 1_150_000),
		inv(1, 20*time.Minute, "claude-opus-5", "2.1.246", "high", 1, 0, 750_000),
	)
	r := Cache(s)

	if r.Writes5m != 1_900_000 || r.AvoidableTokens != 750_000 {
		t.Fatalf("setup drifted: writes %d avoidable %d", r.Writes5m, r.AvoidableTokens)
	}
	if math.Abs(r.LongerTTLNetEIT) > 1 {
		t.Errorf("net = %.1f at break-even, want 0", r.LongerTTLNetEIT)
	}
	// Under the 37.5% reading the same session would look like a saving,
	// because it treats 750,000 as 39.5% of the way to a free lunch.
	if r.LongerTTLNetEIT < -1 {
		t.Errorf("net = %.1f, which is the avoided-rewrite-is-free answer", r.LongerTTLNetEIT)
	}
}

func TestMixedPricingIsVisibleInTheCacheReport(t *testing.T) {
	// The cache report's headline is a share, and a mixed denominator is
	// harder to spot than a mixed total. The set of read rates is what says
	// so: the read is the term that differs between the pricings here, and
	// it is also the class that dominates prompt cost.
	same := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 10_000),
		inv(1, 20*time.Minute, "claude-sonnet-5", "2.1.246", "high", 1, 0, 50_000),
	)
	if got := Cache(same).ReadRates; len(got) != 1 {
		t.Errorf("two models on one pricing gave rates %v, want one", got)
	}

	mixed := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 1, 0, 10_000),
		inv(1, 20*time.Minute, "claude-fable-5-1", "2.1.246", "high", 1, 0, 50_000),
	)
	got := Cache(mixed).ReadRates
	if len(got) != 2 || got[0] != 0.025 || got[1] != 0.1 {
		t.Errorf("rates = %v, want [0.025 0.1] sorted and distinct", got)
	}
}

// onHour builds a call whose cache write the API reported as an
// ephemeral_1h write, which is what a session running under
// promptCacheTtl=1h looks like in a transcript.
func onHour(seq int, at time.Duration, read, create int64) model.ModelInvocation {
	m := inv(seq, at, "claude-opus-5", "2.1.246", "high", 0, read, create)
	m.Usage.CacheCreation5m, m.Usage.CacheCreation1h = 0, create
	return m
}

func TestExpiryIsJudgedAgainstTheLifetimeTheWriteAskedFor(t *testing.T) {
	// The 5m/1h split is observed per write, so the lifetime a prefix was
	// given is not something to assume. Twelve minutes is past one lifetime
	// and well inside the other, so the same gap has to be read differently
	// depending on which was in force -- and both readings are observed.
	twelve := 12 * time.Minute

	onFive := session(
		inv(0, 0, "claude-opus-5", "2.1.246", "high", 0, 0, 50_000),
		inv(1, twelve, "claude-opus-5", "2.1.246", "high", 0, 0, 60_000))
	if got := missAt(Cache(onFive), 1).Cause; got != CauseTTLExpiry {
		t.Errorf("on the 5-minute lifetime a twelve-minute gap is expiry, got %q", got)
	}

	// The same gap on the 1-hour lifetime. The prefix was warm, so whatever
	// rebuilt it was not expiry: saying it was points the reader at a TTL
	// they are already using, and hides the cause that actually applies.
	onOne := session(onHour(0, 0, 0, 50_000), onHour(1, twelve, 0, 60_000))
	r := Cache(onOne)
	if r.ObservedTTL != "1h" {
		t.Fatalf("observed TTL = %q, want 1h", r.ObservedTTL)
	}
	m := missAt(r, 1)
	if m.Cause != CauseUnexplained {
		t.Errorf("cause = %q, want %q: the prefix had not expired",
			m.Cause, CauseUnexplained)
	}
	if m.AvoidableByTTL {
		t.Error("a session already on the longer lifetime cannot be helped by it")
	}
	if r.ExpiryCostEIT != 0 {
		t.Errorf("expiry cost = %.0f, want 0", r.ExpiryCostEIT)
	}
	if r.UnexplainedCost == 0 {
		t.Error("the cost has to land somewhere, and unexplained is where it belongs")
	}
}

func TestAPrefixWrittenAtBothLifetimesExpiresWithItsShortestPart(t *testing.T) {
	// A cached prefix is a chain: invalidating an early segment invalidates
	// everything after it, so a prefix holding a 5-minute segment is warm
	// only as long as that segment is. The longer part of the write does not
	// protect it.
	both := inv(0, 0, "claude-opus-5", "2.1.246", "high", 0, 0, 50_000)
	both.Usage.CacheCreation1h = 20_000
	both.Usage.CacheCreation5m = 30_000
	s := session(both, onHour(1, 12*time.Minute, 0, 60_000))
	if got := missAt(Cache(s), 1).Cause; got != CauseTTLExpiry {
		t.Errorf("cause = %q, want %q: the 5-minute segment governs", got, CauseTTLExpiry)
	}
}

func missAt(r CacheReport, seq int) Miss {
	for _, m := range r.Misses {
		if m.Seq == seq {
			return m
		}
	}
	return Miss{}
}
