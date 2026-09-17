package analysis

import (
	"sort"

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
	PreambleShare    float64 `json:"preamble_share_of_prompt_cost"`
	PromptCostEIT    float64 `json:"prompt_cost_eit"`

	// Peak and Final describe the context trajectory.
	Peak  int64 `json:"peak_prompt_tokens"`
	Final int64 `json:"final_prompt_tokens"`
	// Resets are calls where the prompt shrank sharply: compaction, or a
	// context that was rebuilt. They truncate every open residency span.
	Resets []int `json:"reset_calls"`

	// AssistantCarryEIT is what re-sending the model's own words cost. Output
	// is billed once at the output rate when generated, then carried as input
	// on every later call; this is the second part, which is invisible if you
	// only look at output tokens.
	AssistantCarryEIT float64 `json:"assistant_carry_eit"`
	// ToolInputCarryEIT is what re-sending the tool calls the model wrote
	// cost. On real sessions the model writes nearly as many bytes into tool
	// calls as it reads back out of them.
	ToolInputCarryEIT float64 `json:"tool_input_carry_eit"`

	// Items ranks retrievals by what carrying them cost.
	Items []CarriedItem `json:"items"`
	// Unattributed is the share of observed growth the content could not
	// explain: system reminders, attachments, envelopes. Reported rather than
	// distributed across items.
	Unattributed float64 `json:"unattributed_growth_share"`
}

// CarriedItem is one retrieval priced by residency rather than by size.
type CarriedItem struct {
	// RetrievalSeq identifies which retrieval this is, so other analyses can
	// join onto it exactly instead of matching on a path that may be absent.
	RetrievalSeq int            `json:"retrieval_seq"`
	Tool         string         `json:"tool"`
	Path         string         `json:"path,omitempty"`
	Category     model.Category `json:"category"`
	Bytes        int            `json:"bytes"`
	Tokens       float64        `json:"tokens"`
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
// Per-item cache class is not directly observable -- the API reports one split
// per call, not per block -- so residency is priced by call: content resident
// across a call that rebuilt its prefix was re-created at the write rate, and
// content resident across a warm call was read at the cheap one. Caching is
// prefix-based and retrievals sit in the prefix, so this follows the mechanism
// rather than guessing.
func Carry(s *model.Session, cacheReport CacheReport) CarryReport {
	w := weightsFor(s)
	cold := ColdCalls(cacheReport)

	r := CarryReport{
		Calls:        len(s.Invocations),
		Unattributed: s.Estimator.Residual,
	}

	// Trajectory. A call reporting no prompt tokens is an API error entry and
	// must not be read as a context reset.
	prev := int64(-1)
	for _, inv := range s.Invocations {
		p := inv.Usage.PromptTokens()
		r.PromptCostEIT += w.PromptCost(inv.Usage)
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
	// them. Priced the same way as any other resident content.
	r.PreambleCarryEIT = residencyCost(float64(r.Preamble), 1, len(s.Invocations), cold, r.Resets, w)
	if r.PromptCostEIT > 0 {
		r.PreambleShare = r.PreambleCarryEIT / r.PromptCostEIT
	}

	// The model's own output is carried too: generated once at the output
	// rate, then re-sent as input on every later call.
	for _, inv := range s.Invocations {
		if !inv.IsRealCall() || inv.Usage.Output == 0 {
			continue
		}
		warm, coldN := residency(inv.Seq+1, len(s.Invocations), cold, r.Resets)
		r.AssistantCarryEIT += float64(inv.Usage.Output) *
			(float64(warm)*w.CacheRead + float64(coldN)*w.CacheWrite5m)
	}

	// So are the tool calls it wrote, which are not free: the arguments sit in
	// the conversation exactly like the results do.
	ratio := s.Estimator.BytesPerToken
	if ratio <= 0 {
		ratio = 3.6
	}
	for _, tc := range s.ToolCalls {
		if tc.InvocationSeq < 0 || tc.InputBytes == 0 {
			continue
		}
		warm, coldN := residency(tc.InvocationSeq+1, len(s.Invocations), cold, r.Resets)
		r.ToolInputCarryEIT += (float64(tc.InputBytes) / ratio) *
			(float64(warm)*w.CacheRead + float64(coldN)*w.CacheWrite5m)
	}

	for _, c := range s.Retrievals {
		entered := c.InvocationSeq
		if entered < 0 {
			continue
		}
		warm, coldN := residency(entered+1, len(s.Invocations), cold, r.Resets)
		if warm+coldN == 0 {
			continue
		}
		r.Items = append(r.Items, CarriedItem{
			RetrievalSeq: c.Seq,
			Tool:         c.Tool, Path: c.Path, Category: c.Category,
			Bytes: c.Bytes, Tokens: c.Tokens,
			EnteredAt:   entered,
			ResidentFor: warm + coldN,
			WarmCalls:   warm,
			ColdCalls:   coldN,
			CarryEIT:    c.Tokens * (float64(warm)*w.CacheRead + float64(coldN)*w.CacheWrite5m),
			// Every re-send at full input price: the counterfactual of no
			// caching at all, on the same trajectory.
			CarryUncachedEIT: c.Tokens * float64(warm+coldN) * w.Input,
		})
	}
	sort.SliceStable(r.Items, func(i, j int) bool { return r.Items[i].CarryEIT > r.Items[j].CarryEIT })
	return r
}

// residency counts the warm and cold calls between from and end, stopping at
// the first reset: content does not survive a compaction.
func residency(from, end int, cold map[int]bool, resets []int) (warm, coldN int) {
	stop := end
	for _, reset := range resets {
		if reset >= from && reset < stop {
			stop = reset
		}
	}
	for k := from; k < stop; k++ {
		if cold[k] {
			coldN++
		} else {
			warm++
		}
	}
	return warm, coldN
}

func residencyCost(tokens float64, from, end int, cold map[int]bool, resets []int, w interface {
	PromptCost(model.TokenUsage) float64
}) float64 {
	warm, coldN := residency(from, end, cold, resets)
	// Priced through the same weights as everything else by constructing the
	// equivalent usage, so there is one place that knows the rates.
	return w.PromptCost(model.TokenUsage{CacheRead: int64(tokens) * int64(warm)}) +
		w.PromptCost(model.TokenUsage{
			CacheCreation:   int64(tokens) * int64(coldN),
			CacheCreation5m: int64(tokens) * int64(coldN),
		})
}
