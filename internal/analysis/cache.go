// Package analysis turns a parsed session into the findings worth reporting.
package analysis

import (
	"time"

	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
)

// Miss causes. TTL expiry is only one of about nine things that invalidate a
// Claude Code prompt cache, and several of the others are observable, so they
// are tested first: those misses would have happened under any TTL.
const (
	CauseSessionStart = "session_start"
	CauseModelSwitch  = "model_switch"
	CauseUpgrade      = "claude_code_upgrade"
	CauseCompaction   = "compaction_or_reset"
	CauseEffortChange = "effort_change"
	CauseTTLExpiry    = "ttl_expiry"
	CauseUnexplained  = "unexplained"
)

// Thresholds for detecting a large cache miss and an expired prefix. Stated
// rather than buried: the classification is derived, not observed, and a
// reader is entitled to know what produced it.
const (
	// MissShare is the fraction of a call's prompt that must be freshly
	// written for the call to count as a large miss.
	MissShare = 0.5
	// MissFloor stops small writes from being called misses at all.
	MissFloor = 2000
	// ResetDrop is the prompt-size fall that indicates compaction rather than
	// a cache miss.
	ResetDrop = 0.6
	// TTL5m is the default cache lifetime. The clock runs from request start,
	// and a read refreshes it, so calls starting closer together than this
	// keep the prefix warm indefinitely.
	TTL5m = 5 * time.Minute
	// TTL1h is the longer lifetime, available via promptCacheTtl.
	TTL1h = time.Hour
)

// Miss is one call that paid to rebuild part or all of its prefix.
type Miss struct {
	Seq       int           `json:"seq"`
	Cause     string        `json:"cause"`
	Rebuilt   int64         `json:"rebuilt_tokens"`
	CostEIT   float64       `json:"cost_eit"`
	Gap       time.Duration `json:"-"`
	GapSecond float64       `json:"gap_seconds"`
	// AvoidableByTTL is true only for expiry, and only when a longer lifetime
	// would actually have covered the gap.
	AvoidableByTTL bool `json:"avoidable_by_longer_ttl"`
}

// CacheReport is what cache behaviour cost, attributed to causes.
type CacheReport struct {
	// ObservedTTL is read from the API's own 5m/1h split, not inferred.
	ObservedTTL     string              `json:"observed_ttl"`
	Writes5m        int64               `json:"cache_writes_5m"`
	Writes1h        int64               `json:"cache_writes_1h"`
	TotalCostEIT    float64             `json:"total_prompt_cost_eit"`
	Misses          []Miss              `json:"misses"`
	ByCause         map[string]CauseAgg `json:"by_cause"`
	ExpiryCostEIT   float64             `json:"expiry_cost_eit"`
	ExpiryShare     float64             `json:"expiry_share_of_prompt_cost"`
	UnexplainedCost float64             `json:"unexplained_cost_eit"`
	// AvoidableTokens is the expiry rewriting that a 1-hour lifetime would
	// have covered: expiry only, and only where the gap was under an hour.
	// Gaps longer than that expire under either TTL.
	AvoidableTokens int64 `json:"avoidable_by_1h_tokens"`
	// LongerTTLNetEIT is what switching to the 1-hour TTL would have cost,
	// net. Negative is a saving.
	//
	// This is a counterfactual, and it is in a measurement report because
	// every input to it is observed: which TTL each call used, which misses
	// were expiry, the gap lengths, and the published multipliers. There is
	// no assumed parameter anywhere in it.
	//
	// It is here because it has to be computed rather than reasoned about.
	// Done by hand it is easy to treat the avoided rewrite as free, when it
	// becomes a cache read at a tenth of input price, and easy to forget that
	// every *other* write is repriced from 1.25x to 2.0x. Both mistakes push
	// the answer the same way: this session's real figure is -6.3% and a hand
	// calculation that made both came out at 11.1%.
	LongerTTLNetEIT float64 `json:"longer_ttl_net_eit"`
	LongerTTLShare  float64 `json:"longer_ttl_net_share_of_prompt_cost"`
}

// longerTTL prices a switch from the 5-minute lifetime to the 1-hour one.
//
//	old = every 5m write at 1.25
//	new = the writes you still make at 2.0, plus the ones you avoid as reads at 0.1
//
// A session that never idles past five minutes avoids nothing and pays double
// for every write, so this comes out positive, which is the point of computing
// it rather than assuming. Break-even is avoided writes above 37.5% of all
// writes.
func longerTTL(r *CacheReport, w cost.Weights) {
	if r.Writes5m == 0 {
		return
	}
	for _, m := range r.Misses {
		if m.AvoidableByTTL {
			r.AvoidableTokens += m.Rebuilt
		}
	}
	avoidable := float64(r.AvoidableTokens)
	if avoidable > float64(r.Writes5m) {
		// Cannot avoid more than was written; a defensive clamp rather than a
		// silent negative.
		avoidable = float64(r.Writes5m)
	}
	old := float64(r.Writes5m) * w.CacheWrite5m
	// The avoided rewrite does not vanish: the prefix is still sent, as a
	// cache read.
	now := (float64(r.Writes5m)-avoidable)*w.CacheWrite1h + avoidable*w.CacheRead
	r.LongerTTLNetEIT = now - old
	if r.TotalCostEIT > 0 {
		r.LongerTTLShare = r.LongerTTLNetEIT / r.TotalCostEIT
	}
}

// CauseAgg totals one cause.
type CauseAgg struct {
	Calls    int     `json:"calls"`
	Tokens   int64   `json:"rebuilt_tokens"`
	CostEIT  float64 `json:"cost_eit"`
	Share    float64 `json:"share_of_prompt_cost"`
	TTLFixes bool    `json:"avoidable_by_longer_ttl"`
}

// Cache attributes every large cache miss in a session to a cause and prices
// it. Everything except the final elimination step is observed.
func Cache(s *model.Session, ttl time.Duration) CacheReport {
	r := CacheReport{ByCause: map[string]CauseAgg{}}
	w := weightsFor(s)

	var usage model.TokenUsage
	for _, inv := range s.Invocations {
		usage = usage.Add(inv.Usage)
		callPrompt, _ := cost.PerCall(inv)
		r.TotalCostEIT += callPrompt
	}
	r.Writes5m, r.Writes1h = usage.CacheCreation5m, usage.CacheCreation1h
	switch {
	case r.Writes1h > 0 && r.Writes5m > 0:
		r.ObservedTTL = "mixed 5m and 1h"
	case r.Writes1h > 0:
		r.ObservedTTL = "1h"
	case r.Writes5m > 0:
		r.ObservedTTL = "5m"
	default:
		r.ObservedTTL = "none observed"
	}

	for i, inv := range s.Invocations {
		prompt := inv.Usage.PromptTokens()
		if prompt == 0 {
			continue // an API error entry, excluded from cost
		}
		if float64(inv.Usage.CacheCreation)/float64(prompt) < MissShare ||
			inv.Usage.CacheCreation < MissFloor {
			continue
		}

		m := Miss{
			Seq:     inv.Seq,
			Rebuilt: inv.Usage.CacheCreation,
			CostEIT: w.PromptCost(model.TokenUsage{
				CacheCreation:   inv.Usage.CacheCreation,
				CacheCreation5m: inv.Usage.CacheCreation5m,
				CacheCreation1h: inv.Usage.CacheCreation1h,
			}),
		}
		if prev, ok := lastRealCall(s.Invocations, i); ok {
			m.Gap = gapBetween(prev, inv)
			m.GapSecond = m.Gap.Seconds()
			m.Cause = causeOf(prev, inv, prompt, m.Gap, ttl)
			// Compaction shrinks the prompt on the call that summarises, but
			// the new prefix is written on the call after it. Attributing
			// that rebuild to the compaction rather than leaving it
			// unexplained is the difference between naming a cause and
			// shrugging at a large line in the report.
			if m.Cause == CauseUnexplained && followsReset(s.Invocations, i) {
				m.Cause = CauseCompaction
			}
			m.AvoidableByTTL = m.Cause == CauseTTLExpiry && m.Gap <= TTL1h
		} else {
			m.Cause = CauseSessionStart
		}

		r.Misses = append(r.Misses, m)
		agg := r.ByCause[m.Cause]
		agg.Calls++
		agg.Tokens += m.Rebuilt
		agg.CostEIT += m.CostEIT
		agg.TTLFixes = agg.TTLFixes || m.AvoidableByTTL
		r.ByCause[m.Cause] = agg

		if m.Cause == CauseTTLExpiry {
			r.ExpiryCostEIT += m.CostEIT
		}
		if m.Cause == CauseUnexplained {
			r.UnexplainedCost += m.CostEIT
		}
	}

	if r.TotalCostEIT > 0 {
		r.ExpiryShare = r.ExpiryCostEIT / r.TotalCostEIT
		for cause, agg := range r.ByCause {
			agg.Share = agg.CostEIT / r.TotalCostEIT
			r.ByCause[cause] = agg
		}
	}
	longerTTL(&r, w)
	return r
}

// causeOf attributes a miss, testing observable causes before falling back to
// expiry by elimination.
//
// MCP server changes, plugin toggles and tool-deny rules also invalidate the
// cache and are NOT observable in a transcript, so they land in unexplained
// rather than being attributed to a TTL that may be innocent.
func causeOf(prev, cur model.ModelInvocation, prompt int64, gap, ttl time.Duration) string {
	switch {
	case prev.Model != cur.Model:
		return CauseModelSwitch
	case prev.Version != cur.Version && cur.Version != "":
		return CauseUpgrade
	case float64(prompt) < float64(prev.Usage.PromptTokens())*ResetDrop:
		return CauseCompaction
	case prev.Effort != cur.Effort:
		return CauseEffortChange
	case gap > ttl:
		return CauseTTLExpiry
	default:
		return CauseUnexplained
	}
}

// followsReset reports whether the previous real call was the one where the
// prompt collapsed, which is the signature of a compaction.
func followsReset(invs []model.ModelInvocation, i int) bool {
	prev, ok := lastRealCall(invs, i)
	if !ok {
		return false
	}
	before, ok := lastRealCall(invs, prev.Seq)
	if !ok {
		return false
	}
	return float64(prev.Usage.PromptTokens()) < float64(before.Usage.PromptTokens())*ResetDrop
}

// lastRealCall finds the most recent actual API request before index i,
// stepping over error entries. The cache state the current call met was left
// by that request, not by a placeholder.
func lastRealCall(invs []model.ModelInvocation, i int) (model.ModelInvocation, bool) {
	for k := i - 1; k >= 0; k-- {
		if invs[k].IsRealCall() {
			return invs[k], true
		}
	}
	return model.ModelInvocation{}, false
}

// gapBetween is the start-to-start interval, which is what the TTL clock
// measures. An unknown timestamp yields zero, which cannot be mistaken for an
// expiry.
func gapBetween(prev, cur model.ModelInvocation) time.Duration {
	if prev.Timestamp.IsZero() || cur.Timestamp.IsZero() {
		return 0
	}
	return cur.Timestamp.Sub(prev.Timestamp)
}

func weightsFor(s *model.Session) cost.Weights {
	if ms := s.Models(); len(ms) > 0 {
		return cost.For(ms[0])
	}
	return cost.Default
}

// ColdCalls returns the set of call sequence numbers that rebuilt their
// prefix, which carry analysis needs: content resident across a cold call was
// re-created at the write rate rather than read at the cheap one.
func ColdCalls(r CacheReport) map[int]bool {
	cold := map[int]bool{}
	for _, m := range r.Misses {
		cold[m.Seq] = true
	}
	return cold
}
