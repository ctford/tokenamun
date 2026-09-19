package analysis

import (
	"sort"

	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
)

// CarryReport explains what it cost to keep content in the context, as
// opposed to what it cost to fetch it.
//
// The model has no memory between calls, so everything resident is re-sent
// every time. Content is priced once and carried many times, which is why the
// size of a retrieval is the wrong thing to rank on and when it arrived is the
// right one.
type CarryReport struct {
	Calls int `json:"api_calls"`
	// Preamble is the first call's prompt: everything that existed before any
	// work happened. Observed exactly, and paid again on every call.
	Preamble int64 `json:"preamble_tokens"`
	// PreambleCarryEIT is what carrying it cost across the session. It cannot
	// be decomposed into system prompt versus tool schemas versus instruction
	// files, because none of those are in the transcript.
	PreambleCarryEIT float64 `json:"preamble_carry_eit"`
	// PreambleCarryUncachedEIT is the same residency priced as though nothing
	// cached. Every part of the prompt needs one of these, not just the
	// retrieved content: the preamble, what you typed and the model's own
	// words are all re-sent as input, and all three would be at full price
	// without the cache.
	PreambleCarryUncachedEIT float64 `json:"preamble_carry_uncached_eit"`
	// PreambleRoundTrips is how many calls carried the preamble: how much of
	// the back and forth it was part of.
	PreambleRoundTrips float64 `json:"preamble_round_trips"`
	PreambleShare      float64 `json:"preamble_share_of_prompt_cost"`
	PromptCostEIT      float64 `json:"prompt_cost_eit"`
	// PromptCostUncachedEIT is the whole prompt bill with no caching: the same
	// tokens, sent the same number of times, at full input price. It is the
	// denominator the no-caching view needs, and the gap against PromptCostEIT
	// is what prompt caching was worth on this session.
	PromptCostUncachedEIT float64 `json:"prompt_cost_uncached_eit"`

	// Peak and Final describe the context trajectory.
	Peak  int64 `json:"peak_prompt_tokens"`
	Final int64 `json:"final_prompt_tokens"`
	// Resets are calls where the prompt shrank sharply: compaction, or a
	// context that was rebuilt. They truncate every open residency span.
	Resets []int `json:"reset_calls"`

	// PromptCarryEIT is what re-sending what you typed cost.
	PromptCarryEIT float64 `json:"prompt_carry_eit"`
	// PromptCarryUncachedEIT is the same, priced as though nothing cached.
	PromptCarryUncachedEIT float64 `json:"prompt_carry_uncached_eit"`
	// PromptRoundTrips is the token-weighted mean over what you typed. Early
	// prompts go round far more often than late ones, so an unweighted mean
	// over prompts would describe nobody's experience.
	PromptRoundTrips float64 `json:"prompt_round_trips"`
	// ThinkingTokens is observed: the model's reasoning, billed as output.
	// Its carry is deliberately absent -- Claude Code records thinking blocks
	// with empty text, so whether they are re-sent is not knowable from a
	// transcript, and guessing would be the thing this tool refuses to do.
	ThinkingTokens int64 `json:"thinking_tokens"`

	// AssistantCarryEIT is what re-sending the model's own words cost,
	// excluding thinking. Output is billed once at the output rate when
	// generated, then carried as input on every later call; this is the second
	// part, which is invisible if you only look at output tokens.
	AssistantCarryEIT float64 `json:"assistant_carry_eit"`
	// AssistantCarryUncachedEIT is the re-sends at full input price. The
	// generation itself is not in either figure and is not affected by
	// caching: output is billed at the output rate whatever the cache does.
	AssistantCarryUncachedEIT float64 `json:"assistant_carry_uncached_eit"`
	// AssistantRoundTrips is the token-weighted mean over the model's own
	// words, excluding thinking for the same reason its carry is excluded.
	AssistantRoundTrips float64 `json:"assistant_round_trips"`
	// Items ranks retrievals by what carrying them cost.
	Items []CarriedItem `json:"items"`
	// RoundTripsByCall is how many calls re-sent whatever arrived on a given
	// call, indexed by invocation sequence. Anything attributed to a call
	// rather than to a retrieval -- the arguments the model wrote into a tool
	// call, say -- takes its residency from here.
	RoundTripsByCall map[int]int `json:"-"`
	// Unattributed is the share of observed growth the content could not
	// explain: system reminders, attachments, envelopes. Reported rather than
	// distributed across items.
	Unattributed float64 `json:"unattributed_growth_share"`
}

// CarriedItem is one retrieval priced by residency rather than by size.
type CarriedItem struct {
	// RetrievalSeq identifies which retrieval this is, so other analyses can
	// join onto it exactly instead of matching on a path that may be absent.
	RetrievalSeq int     `json:"retrieval_seq"`
	Tool         string  `json:"tool"`
	Path         string  `json:"path,omitempty"`
	Bytes        int     `json:"bytes"`
	Tokens       float64 `json:"tokens"`
	// EnteredAt is the call that carried it into the context.
	EnteredAt int `json:"entered_at_call"`
	// ResidentFor is how many later calls re-sent it.
	ResidentFor int `json:"resident_for_calls"`
	// WarmCalls read it from cache; ColdCalls rebuilt it at the write rate.
	WarmCalls int `json:"warm_calls"`
	ColdCalls int `json:"cold_calls"`
	// CarryEIT is the cost of the re-sends as actually billed: cache reads at
	// a tenth of input price, rebuilt prefixes at the write rate.
	CarryEIT float64 `json:"carry_eit"`
	// CarryUncachedEIT is the same residency priced as though nothing cached.
	// The gap between the two is what prompt caching was worth on this
	// content, which is not visible from either number alone.
	CarryUncachedEIT float64 `json:"carry_uncached_eit"`
}

// Carry computes residency costs for a session.
//
// Everything a piece of content costs is attributed to that content: the call
// that first carried it in, and every later call that re-sent it. So a file
// read once and then carried for thirty calls shows one number covering all
// thirty-one sends, rather than the first send landing somewhere else.
//
// The first send is priced at the cache *write* rate, because new content is
// what a request writes to the cache; later sends are reads at a tenth of
// input price, or writes again on any call that rebuilt its prefix. Pricing
// the first send as a read understated late-arriving content by more than an
// order of magnitude -- content that arrives near the end of a session is
// written once and barely re-read, so the write is nearly all of its cost.
//
// Per-item cache class is not directly observable -- the API reports one split
// per call, not per block -- so residency is priced by call. Caching is
// prefix-based and retrievals sit in the prefix, so this follows the mechanism
// rather than guessing.
//
// By call means by that call's model as well as its cache class. A span can
// cross a model switch, and the cache read is the one weight that differs
// between models: 0.025x on the 5.1 generation against 0.1x elsewhere. Since
// carry is nearly all cache reads, pricing a span at whichever model came
// first was close to a fourfold error on any session that mixed them.
func Carry(s *model.Session, cacheReport CacheReport) CarryReport {
	return CarryWith(s, cacheReport, nil)
}

// CarryWith prices residency as though the context had also been cleared at
// each of extraResets.
//
// It exists so that "what if the context were cleared at each new task" is
// answered by the same arithmetic as the real session rather than by a
// separate model of it. A clear and a compaction do the same thing to carry
// cost -- they truncate every open residency span -- so the counterfactual is
// the observed session with more resets in it.
// spanPricer prices a piece of content's residency call by call.
//
// Carry is the residency of one item over a span of calls, and those calls
// can span models, so pricing the span at one rate is wrong the moment a
// session mixes the 5.1 generation with anything else: its cache reads are
// 0.025x against 0.1x, and carry is nearly all cache reads. A bucket is not
// enough here, because a bucket loses the order and the order is what says
// which send was the write. So the span is walked.
//
// Only the cache read actually differs between the models published so far.
// The walk prices every class per call anyway, because the next difference
// will not announce itself.
type spanPricer struct {
	weights []cost.Weights
	cold    map[int]bool
}

func newSpanPricer(s *model.Session, cold map[int]bool) spanPricer {
	w := make([]cost.Weights, len(s.Invocations))
	for i, inv := range s.Invocations {
		w[i] = cost.For(inv.Model)
	}
	return spanPricer{weights: w, cold: cold}
}

// at is the pricing of one call, by sequence. Out of range -- an item that
// entered on the first call looks back past it -- falls back to the default
// rather than to a neighbour's, because a call that is not there has no
// model.
func (p spanPricer) at(call int) cost.Weights {
	if call < 0 || call >= len(p.weights) {
		return cost.Default
	}
	return p.weights[call]
}

// resident is what one piece of content's residency cost.
type resident struct {
	// Billed is the cost as actually billed, each send at the model and the
	// cache class of the call it went out on.
	Billed float64
	// InputEIT is the same sends at full input price: the no-caching
	// counterfactual on the same trajectory.
	InputEIT float64
	// EntryInput is one more send at full input price, at the model of the
	// call the content entered on. Two of the callers here count the
	// entering call's own send in their counterfactual and one does not;
	// see the note on carried.
	EntryInput float64
	// Sends is how many sends were priced, Warm and Cold how they split
	// after the first, and Span how many calls the content was resident for.
	Sends, Warm, Cold, Span int
}

// arrived prices content that is new at the start of the span: its first
// send writes it to the cache and the rest read it, or write it again on a
// call that rebuilt the prefix.
//
// Pricing the first send as a read understated late-arriving content by more
// than an order of magnitude, which is why the write is charged even when
// the content arrived on the final call and was never re-read.
//
// The write is the first call of the span rather than a subtraction from the
// warm total. Those agree unless the first call was itself cold, where the
// old arithmetic charged a write for the cold call and another for the
// substitution; in call order there is one.
func (p spanPricer) arrived(tokens float64, from, end int, resets []int) resident {
	r := resident{EntryInput: tokens * p.at(from-1).Input}
	stop := stopAt(from, end, resets)
	if from >= stop {
		// Content that arrives on the final call is still sent once.
		w := p.at(from - 1)
		return resident{Billed: tokens * w.CacheWrite5m, InputEIT: tokens * w.Input,
			EntryInput: r.EntryInput, Sends: 1}
	}
	for k := from; k < stop; k++ {
		w := p.at(k)
		r.InputEIT += tokens * w.Input
		r.Sends++
		r.Span++
		switch {
		case k == from:
			r.Billed += tokens * w.CacheWrite5m
		case p.cold[k]:
			r.Billed += tokens * w.CacheWrite5m
			r.Cold++
		default:
			r.Billed += tokens * w.CacheRead
			r.Warm++
		}
	}
	return r
}

// carried prices content that was already resident when the span began, so
// no send in it is a write of new content.
//
// The preamble is the one of these: it is the first call's whole prompt, and
// what a compaction leaves behind is not in the transcript, so nothing here
// claims a rewrite of it.
func (p spanPricer) carried(tokens float64, from, end int, resets []int) resident {
	var r resident
	stop := stopAt(from, end, resets)
	for k := from; k < stop; k++ {
		w := p.at(k)
		r.InputEIT += tokens * w.Input
		r.Sends++
		r.Span++
		if p.cold[k] {
			r.Billed += tokens * w.CacheWrite5m
			r.Cold++
			continue
		}
		r.Billed += tokens * w.CacheRead
		r.Warm++
	}
	return r
}

// stopAt is where a residency span ends: the end of the session, or the
// first reset in it, because content does not survive a compaction.
func stopAt(from, end int, resets []int) int {
	stop := end
	for _, reset := range resets {
		if reset >= from && reset < stop {
			stop = reset
		}
	}
	return stop
}

func CarryWith(s *model.Session, cacheReport CacheReport, extraResets []int) CarryReport {
	cold := ColdCalls(cacheReport)
	price := newSpanPricer(s, cold)

	r := CarryReport{
		Calls:        len(s.Invocations),
		Unattributed: s.Estimator.Residual,
	}

	// Trajectory. A call reporting no prompt tokens is an API error entry and
	// must not be read as a context reset.
	prev := int64(-1)
	for _, inv := range s.Invocations {
		p := inv.Usage.PromptTokens()
		// Priced with this call's own model, not the session's first one.
		callPrompt, _ := cost.PerCall(inv)
		r.PromptCostEIT += callPrompt
		// The same tokens at full input price: what this session would have
		// cost with no cache at all, at this call's own input rate.
		r.PromptCostUncachedEIT += float64(p) * cost.For(inv.Model).Input
		if !inv.IsRealCall() {
			continue
		}
		if p > r.Peak {
			r.Peak = p
		}
		if prev > 0 && float64(p) < float64(prev)*ResetDrop {
			r.Resets = append(r.Resets, inv.Seq)
		}
		r.Final = p
		prev = p
	}
	if len(s.Invocations) > 0 {
		r.Preamble = s.Invocations[0].Usage.PromptTokens()
	}

	// The preamble is resident for every call, so it is carried by all of
	// them. Priced the same way as any other resident content -- including
	// stopping at a reset, because the measured preamble is the first call's
	// whole prompt and compaction can leave a prefix smaller than that. What
	// the harness put back afterwards is not in the transcript, so it is not
	// claimed here. This is also why the counterfactual resets below do not
	// apply to it: a clear certainly rebuilds the preamble rather than losing
	// it, and an intervention that clears must price that write itself.
	preamble := price.carried(float64(r.Preamble), 1, len(s.Invocations), r.Resets)
	r.PreambleCarryEIT = preamble.Billed
	r.PreambleCarryUncachedEIT = preamble.InputEIT
	r.PreambleRoundTrips = float64(preamble.Sends)

	// Everything else is priced against the resets that a counterfactual adds
	// as well as the ones that happened. r.Resets itself stays observed.
	resets := mergeResets(r.Resets, extraResets)
	if r.PromptCostEIT > 0 {
		r.PreambleShare = r.PreambleCarryEIT / r.PromptCostEIT
	}

	// What you typed is carried like anything else.
	carryTyped(&r, s, price, resets)

	// Residency per call, for anything attributed to a call rather than to a
	// retrieval: the arguments the model wrote into a tool call take their
	// residency from the call that wrote them.
	r.RoundTripsByCall = roundTripsByCall(s, resets)

	// The model's own output is carried too.
	carryOutput(&r, s, price, resets)

	// The arguments the model wrote into tool calls are carried too, but they
	// are part of its output rather than a separate quantity: they are
	// apportioned out of AssistantCarryEIT by byte share where the report
	// needs them. Computing them separately from byte counts as well gave two
	// competing answers for one thing, and the second was never read.

	for _, c := range s.Retrievals {
		entered := c.InvocationSeq
		if entered < 0 {
			continue
		}
		// Walked rather than counted: each send is priced at the model of
		// the call it went out on, so a retrieval that lived across a model
		// switch is billed at both.
		res := price.arrived(c.Tokens, entered+1, len(s.Invocations), resets)

		r.Items = append(r.Items, CarriedItem{
			RetrievalSeq: c.Seq,
			Tool:         c.Tool, Path: c.Path,
			Bytes: c.Bytes, Tokens: c.Tokens,
			EnteredAt:   entered,
			ResidentFor: res.Sends,
			WarmCalls:   res.Warm,
			ColdCalls:   res.Cold,
			CarryEIT:    res.Billed,
			// Every send at full input price: the counterfactual of no caching
			// at all, on the same trajectory.
			CarryUncachedEIT: res.InputEIT,
		})
	}
	sort.SliceStable(r.Items, func(i, j int) bool { return r.Items[i].CarryEIT > r.Items[j].CarryEIT })
	return r
}

// ratioOf is the session's calibrated bytes-per-token, or the fallback.
func ratioOf(s *model.Session) float64 {
	if s.Estimator.BytesPerToken > 0 {
		return s.Estimator.BytesPerToken
	}
	return 3.6
}

// mergeResets combines detected resets with counterfactual ones, sorted and
// deduplicated so residency sees each boundary once.
func mergeResets(detected, extra []int) []int {
	if len(extra) == 0 {
		return detected
	}
	seen := map[int]bool{}
	var out []int
	for _, xs := range [][]int{detected, extra} {
		for _, k := range xs {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Ints(out)
	return out
}

// carryTyped prices re-sending what you typed.
//
// The mean is weighted by tokens: an early prompt goes round far more often
// than a late one, so an unweighted mean over prompts would describe nobody's
// experience.
func carryTyped(r *CarryReport, s *model.Session, price spanPricer, resets []int) {
	var tokensTotal, tokenCalls float64
	for _, pe := range s.PromptEntries {
		if pe.Bytes == 0 || pe.InvocationSeq < 0 {
			continue
		}
		tokens := float64(pe.Bytes) / ratioOf(s)
		res := price.arrived(tokens, pe.InvocationSeq+1, len(s.Invocations), resets)
		// The counterfactual counts the entering call's own send as well,
		// which the item ranking does not. Kept as it was: which sends a
		// no-caching figure should count is a question about the
		// counterfactual, not about which weights apply to it.
		sends := res.Sends
		uncached := res.InputEIT
		if res.Span > 0 {
			sends++
			uncached += res.EntryInput
		}

		r.PromptCarryEIT += res.Billed
		r.PromptCarryUncachedEIT += uncached
		tokensTotal += tokens
		tokenCalls += tokens * float64(sends)
	}
	if tokensTotal > 0 {
		r.PromptRoundTrips = tokenCalls / tokensTotal
	}
}

// carryOutput prices re-sending the model's own words.
//
// Generated once at the output rate, then re-sent as input on every later
// call; this is the second part, which is invisible if you only look at
// output tokens. Thinking is excluded, because its text is not in the
// transcript and whether it is re-sent cannot be established from one.
func carryOutput(r *CarryReport, s *model.Session, price spanPricer, resets []int) {
	var tokensTotal, tokenCalls float64
	for _, inv := range s.Invocations {
		r.ThinkingTokens += inv.Usage.Thinking
		carried := inv.Usage.Output - inv.Usage.Thinking
		if !inv.IsRealCall() || carried <= 0 {
			continue
		}
		res := price.arrived(float64(carried), inv.Seq+1, len(s.Invocations), resets)
		sends := res.Sends
		uncached := res.InputEIT
		if res.Span > 0 {
			sends++
			uncached += res.EntryInput
		}

		r.AssistantCarryEIT += res.Billed
		r.AssistantCarryUncachedEIT += uncached
		tokensTotal += float64(carried)
		tokenCalls += float64(carried) * float64(sends)
	}
	if tokensTotal > 0 {
		r.AssistantRoundTrips = tokenCalls / tokensTotal
	}
}

// roundTripsByCall is how many calls re-sent whatever arrived on each call.
func roundTripsByCall(s *model.Session, resets []int) map[int]int {
	out := map[int]int{}
	for _, inv := range s.Invocations {
		if !inv.IsRealCall() {
			continue
		}
		// The call itself plus every later call that re-sent what it
		// produced, up to the first reset.
		out[inv.Seq] = 1 + stopAt(inv.Seq+1, len(s.Invocations), resets) - (inv.Seq + 1)
	}
	return out
}
