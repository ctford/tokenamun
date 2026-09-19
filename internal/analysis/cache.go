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
	// The two halves the net is made of, both as positive magnitudes:
	// LongerTTLSavedEIT is what the avoided rewrites stop costing, and
	// LongerTTLPremiumEIT is what the writes you still make cost extra at
	// 2.0x. Net is the premium minus the saving.
	//
	// Reported separately because a single net figure asks to be trusted and
	// these two can be checked. They are also the only way to see *why* the
	// answer came out the way it did: a session with a big saving and a
	// bigger premium looks identical, in the net, to one with neither.
	LongerTTLSavedEIT   float64 `json:"longer_ttl_saved_eit"`
	LongerTTLPremiumEIT float64 `json:"longer_ttl_premium_eit"`
}

// ttlBucket accumulates the counterfactual's inputs for one pricing.
//
// One bucket per set of weights, not one per session. A session switches model
// whenever `opusplan` toggles plan mode, and the 5.1 generation reads cache at
// 0.025x against 0.1x -- which is one of the two terms the break-even is made
// of. Summing everybody's tokens and pricing the total at the first model seen
// is the mistake cost.PerCall exists to avoid, and it survived in here until
// the weights were bucketed.
type ttlBucket struct {
	w         cost.Weights
	writes5m  int64
	avoidable int64
}

// ttlBuckets keeps pricings in the order they were first seen, so that summing
// over them is deterministic. Map iteration order is not, and these are
// floats.
type ttlBuckets []ttlBucket

// at returns the bucket for a model's pricing, creating it if new. The pointer
// is valid until the next call, which is as long as any caller holds it.
func (b *ttlBuckets) at(modelID string) *ttlBucket {
	w := cost.For(modelID)
	for i := range *b {
		if (*b)[i].w == w {
			return &(*b)[i]
		}
	}
	*b = append(*b, ttlBucket{w: w})
	return &(*b)[len(*b)-1]
}

// longerTTL prices a switch from the 5-minute lifetime to the 1-hour one.
//
//	old = every 5m write at 1.25
//	new = the writes you still make at 2.0, plus the ones you avoid as reads at 0.1
//
// A session that never idles past five minutes avoids nothing and pays double
// for every write, so this comes out positive, which is the point of computing
// it rather than assuming.
//
// Break-even at the standard rates is avoided writes at 39.5% of all writes:
// 0.75W = 1.9a, from a premium of (2.0 - 1.25) on the writes that survive
// against a saving of (1.25 - 0.1) on the ones that do not. This comment used
// to say 37.5%, which is 0.75/2.0 -- the answer you get by treating the
// avoided rewrite as free, which is the error the paragraph above it warns
// about. On the 5.1 generation the cheaper read makes each avoided write
// worth more and brings break-even down to 38.0%, which is the reason each
// pricing is priced on its own.
func longerTTL(r *CacheReport, buckets ttlBuckets) {
	if r.Writes5m == 0 {
		return
	}
	for _, b := range buckets {
		if b.writes5m == 0 {
			// Already on the 1-hour lifetime for this pricing, so there is
			// nothing here for a switch to buy.
			continue
		}
		avoidable := float64(b.avoidable)
		if avoidable > float64(b.writes5m) {
			// Cannot avoid more than was written; a defensive clamp rather
			// than a silent negative.
			avoidable = float64(b.writes5m)
		}
		r.AvoidableTokens += int64(avoidable)
		// The avoided rewrite does not vanish: the prefix is still sent, as a
		// cache read, so the saving is the difference between the two rates
		// rather than the whole write.
		r.LongerTTLSavedEIT += avoidable * (b.w.CacheWrite5m - b.w.CacheRead)
		// Every write you still make reprices.
		r.LongerTTLPremiumEIT += (float64(b.writes5m) - avoidable) *
			(b.w.CacheWrite1h - b.w.CacheWrite5m)
	}
	r.LongerTTLNetEIT = r.LongerTTLPremiumEIT - r.LongerTTLSavedEIT
	if r.TotalCostEIT > 0 {
		r.LongerTTLShare = r.LongerTTLNetEIT / r.TotalCostEIT
	}
}

// CauseAgg totals one cause.
//
// The avoidable part is a count, not a flag. It used to be a bool ORed across
// the group, so one avoidable miss among twenty marked the whole row as
// something a longer lifetime would have fixed. Expiry is the only cause that
// can be avoidable at all, and even there it is only the gaps under an hour --
// which on real sessions is most of the tokens and not all of them.
type CauseAgg struct {
	Calls           int     `json:"calls"`
	Tokens          int64   `json:"rebuilt_tokens"`
	CostEIT         float64 `json:"cost_eit"`
	Share           float64 `json:"share_of_prompt_cost"`
	AvoidableCalls  int     `json:"avoidable_by_longer_ttl_calls"`
	AvoidableTokens int64   `json:"avoidable_by_longer_ttl_tokens"`
}

// Cache attributes every large cache miss in a session to a cause and prices
// it. Everything except the final elimination step is observed.
func Cache(s *model.Session, ttl time.Duration) CacheReport {
	r := CacheReport{ByCause: map[string]CauseAgg{}}
	var buckets ttlBuckets

	var usage model.TokenUsage
	for _, inv := range s.Invocations {
		usage = usage.Add(inv.Usage)
		callPrompt, _ := cost.PerCall(inv)
		r.TotalCostEIT += callPrompt
		buckets.at(inv.Model).writes5m += inv.Usage.CacheCreation5m
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
			// Priced at this call's own model, not the session's first.
			CostEIT: cost.For(inv.Model).PromptCost(model.TokenUsage{
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
			if m.AvoidableByTTL {
				buckets.at(inv.Model).avoidable += m.Rebuilt
			}
		} else {
			m.Cause = CauseSessionStart
		}

		r.Misses = append(r.Misses, m)
		agg := r.ByCause[m.Cause]
		agg.Calls++
		agg.Tokens += m.Rebuilt
		agg.CostEIT += m.CostEIT
		if m.AvoidableByTTL {
			agg.AvoidableCalls++
			agg.AvoidableTokens += m.Rebuilt
		}
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
	longerTTL(&r, buckets)
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
	prev, at, ok := lastRealCallAt(invs, i)
	if !ok {
		return false
	}
	before, _, ok := lastRealCallAt(invs, at)
	if !ok {
		return false
	}
	return float64(prev.Usage.PromptTokens()) < float64(before.Usage.PromptTokens())*ResetDrop
}

// lastRealCall finds the most recent actual API request before index i,
// stepping over error entries. The cache state the current call met was left
// by that request, not by a placeholder.
func lastRealCall(invs []model.ModelInvocation, i int) (model.ModelInvocation, bool) {
	inv, _, ok := lastRealCallAt(invs, i)
	return inv, ok
}

// lastRealCallAt also returns where it found it, so a caller stepping back
// twice has an index to step from.
//
// Walking back a second time used to pass the first result's Seq as the
// index. That works only because ingest happens to number invocations by
// their position, which is an invariant of another package established three
// packages away -- and an off-by-one waiting for the day something drops an
// invocation after numbering it.
func lastRealCallAt(invs []model.ModelInvocation, i int) (model.ModelInvocation, int, bool) {
	for k := i - 1; k >= 0; k-- {
		if invs[k].IsRealCall() {
			return invs[k], k, true
		}
	}
	return model.ModelInvocation{}, 0, false
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

// Merge adds cache reports together, for a period or a team.
//
// Cost is additive across sessions, so the totals add: each report was
// computed against its own session's model weights and its own gaps. What is
// not additive is a session-shaped narrative -- the individual misses, whose
// sequence numbers mean nothing once two sessions are in one list -- so they
// are dropped rather than concatenated into a list that reads like one
// session's history.
//
// The TTL, by contrast, has to be reconciled rather than summed: a set where
// some sessions ran the 5-minute lifetime and others the 1-hour one is a
// different situation from either, and saying "5m" would hide it.
func Merge(reports []CacheReport) CacheReport {
	out := CacheReport{ByCause: map[string]CauseAgg{}}
	ttls := map[string]bool{}
	for _, r := range reports {
		out.Writes5m += r.Writes5m
		out.Writes1h += r.Writes1h
		out.TotalCostEIT += r.TotalCostEIT
		out.ExpiryCostEIT += r.ExpiryCostEIT
		out.UnexplainedCost += r.UnexplainedCost
		out.AvoidableTokens += r.AvoidableTokens
		out.LongerTTLNetEIT += r.LongerTTLNetEIT
		out.LongerTTLSavedEIT += r.LongerTTLSavedEIT
		out.LongerTTLPremiumEIT += r.LongerTTLPremiumEIT
		if r.ObservedTTL != "" {
			ttls[r.ObservedTTL] = true
		}
		for cause, agg := range r.ByCause {
			cur := out.ByCause[cause]
			cur.Calls += agg.Calls
			cur.Tokens += agg.Tokens
			cur.CostEIT += agg.CostEIT
			cur.AvoidableCalls += agg.AvoidableCalls
			cur.AvoidableTokens += agg.AvoidableTokens
			out.ByCause[cause] = cur
		}
	}
	switch len(ttls) {
	case 0:
	case 1:
		for t := range ttls {
			out.ObservedTTL = t
		}
	default:
		out.ObservedTTL = "mixed across sessions"
	}
	if out.TotalCostEIT > 0 {
		out.ExpiryShare = out.ExpiryCostEIT / out.TotalCostEIT
		out.LongerTTLShare = out.LongerTTLNetEIT / out.TotalCostEIT
		for cause, agg := range out.ByCause {
			agg.Share = agg.CostEIT / out.TotalCostEIT
			out.ByCause[cause] = agg
		}
	}
	return out
}
