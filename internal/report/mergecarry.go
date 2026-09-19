package report

import (
	"sort"

	"github.com/ctford/tokenamun/internal/model"
)

// MergeCarries adds carry reports together, for "all".
//
// Carry used to refuse a set outright, and the refusal cost more than it
// saved: the question people bring to this tool is about a week, so a command
// that answers only about one session is a command they leave for the
// transcripts. What is true is narrower than the refusal was.
//
// Composes. Every total here is a sum over invocations, and invocations
// belong to exactly one session: preamble carry, prompt carry, the uncached
// counterfactuals, prompt cost. The item ranking composes too, because
// CarryEIT is priced per call at each call's own rate, so a row from one
// session is comparable with a row from another -- provided the row says
// which session it is from, which is why CarryItem grew a Session.
//
// Does not compose, and is dropped rather than merged. Call indices: a reset
// at call 40 means nothing once two sessions are in one list, and neither
// does a final prompt, because a set of sessions has no last context.
// Percentages are recomputed against the new totals rather than averaged, on
// the same argument as MergeProfiles: the mean of eight sessions' shares
// weights a ten-call session like a ten-thousand-call one.
func MergeCarries(carries []Carry, info SessionInfo) Carry {
	out := Carry{
		SchemaVersion: SchemaVersion,
		Session:       info,
		Sessions:      len(carries),
		Preamble: PreambleReport{
			Tokens: model.Obs(0, model.Tokens),
			Carry:  model.Der(0, model.EIT),
			Share:  model.Der(0, model.Ratio),
		},
		Context: ContextReport{
			Peak:       model.Obs(0, model.Tokens),
			PromptCost: model.Der(0, model.EIT),
		},
		Unattributed: model.Der(0, model.Ratio),
	}
	if len(carries) == 0 {
		return out
	}

	var residualWeighted float64
	for _, c := range carries {
		out.Context.PromptCost.Value += c.Context.PromptCost.Value
		if c.Context.Peak.Value > out.Context.Peak.Value {
			out.Context.Peak.Value = c.Context.Peak.Value
		}
		// Each session pays for its own preamble, so the tokens add up the
		// same way the cost of carrying them does.
		out.Preamble.Tokens.Value += c.Preamble.Tokens.Value
		out.Preamble.Carry.Value += c.Preamble.Carry.Value
		// Weighted by what each session spent on its prompt, so a share over
		// the set is the share a reader would get by measuring the set.
		residualWeighted += c.Unattributed.Value * c.Context.PromptCost.Value
		out.Warnings = append(out.Warnings, c.Warnings...)

		for _, it := range c.Items {
			it.Session = c.Session.ID
			out.Items = append(out.Items, it)
		}
	}
	if out.Context.PromptCost.Value > 0 {
		out.Preamble.Share.Value = out.Preamble.Carry.Value / out.Context.PromptCost.Value
		out.Unattributed.Value = residualWeighted / out.Context.PromptCost.Value
	}
	out.Preamble.Note = carries[0].Preamble.Note

	// Ranked across the whole set and then cut, rather than cut per session
	// and then ranked: the worst fifteen retrievals of a week are the
	// question, and a session's own worst may not be in them.
	sort.SliceStable(out.Items, func(i, j int) bool {
		return out.Items[i].CarryCost.Value > out.Items[j].CarryCost.Value
	})
	if len(out.Items) > CarryItemsShown {
		out.Items = out.Items[:CarryItemsShown]
	}

	out.Notes = append(append([]string{}, carries[0].Notes...),
		"Sessions are analysed separately and their carry costs added. Cost is additive; residency is not, because each session has its own context.",
		"Retrievals are ranked across every session, and each row says which session it is from. Cost-weighted tokens are comparable between them: every call is priced at its own model's rate.",
		"Call numbers -- the call a retrieval entered at, and the resets -- index into one session. The final prompt is absent for the same reason: a set has no last context.",
	)
	return out
}
