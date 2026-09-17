package report

import (
	"sort"

	"github.com/ctford/tokenamun/internal/model"
)

// MergeProfiles adds profiles together, for "all".
//
// Built by summing finished profiles rather than by concatenating sessions
// into one synthetic session, which was the shorter route and the wrong one.
// A concatenated session would have a single context in the eyes of every
// downstream analysis, so residency would run across session boundaries and
// content would be billed for calls it was never sent on. Cost is additive
// across sessions; residency is not. Trees are merged for the same reason --
// see MergeTrees.
//
// Three things here are not sums.
//
// Ratios are recomputed against the new totals, never averaged: the mean of
// eight sessions' volume-to-cost ratios weights a ten-call session the same
// as a ten-thousand-call one.
//
// The observed TTL becomes "mixed across sessions" when the set disagrees,
// because a set where some ran 5m and some 1h is a third situation and
// reporting either one hides it. Same treatment as analysis.Merge.
//
// Per-session identity is dropped rather than picked from an arbitrary
// member: the branch, the origin and whether it is the current session
// describe one session and say nothing about a set.
func MergeProfiles(profiles []Profile, info SessionInfo) Profile {
	out := Profile{SchemaVersion: SchemaVersion, Session: info}
	if len(profiles) == 0 {
		return out
	}

	var w model.TokenUsage // only as an accumulator of the observed classes
	var promptCost, outputCost, writeCost float64
	tools := map[string]*ToolSummary{}
	ttls := map[string]bool{}
	for _, p := range profiles {
		w.Input += int64(p.Usage.Input.Value)
		w.CacheRead += int64(p.Usage.CacheRead.Value)
		w.CacheCreation += int64(p.Usage.CacheCreation.Value)
		w.Output += int64(p.Usage.Output.Value)
		w.Thinking += int64(p.Usage.Thinking.Value)
		w.CacheCreation5m += int64(p.Caching.TTL5m.Value)
		w.CacheCreation1h += int64(p.Caching.TTL1h.Value)
		promptCost += p.Usage.PromptCost.Value
		outputCost += p.Usage.OutputCost.Value
		// Each session's own write cost, taken from its own share of its own
		// prompt cost. Recomputing it from the summed tokens would need the
		// cache weights, and those are per-model -- a set spanning two
		// pricings would then be priced at whichever one got hardcoded here.
		writeCost += p.Usage.PromptCost.Value * p.Caching.WriteShareOfCost.Value

		out.Retrieved = addTotals(out.Retrieved, p.Retrieved)
		out.Warnings = append(out.Warnings, p.Warnings...)
		if p.Caching.TTLBucket != "" && p.Caching.TTLBucket != "none observed" {
			ttls[p.Caching.TTLBucket] = true
		}
		for _, t := range p.Tools {
			agg, ok := tools[t.Name]
			if !ok {
				agg = &ToolSummary{Name: t.Name,
					Calls:       model.Obs(0, t.Calls.Unit),
					ResultBytes: model.Obs(0, t.ResultBytes.Unit),
					InputBytes:  model.Obs(0, t.InputBytes.Unit)}
				tools[t.Name] = agg
			}
			agg.Calls.Value += t.Calls.Value
			agg.ResultBytes.Value += t.ResultBytes.Value
			agg.InputBytes.Value += t.InputBytes.Value
		}
	}

	volume := float64(w.PromptTokens())
	out.Usage = UsageReport{
		Input:           model.Obs(float64(w.Input), model.Tokens),
		CacheRead:       model.Obs(float64(w.CacheRead), model.Tokens),
		CacheCreation:   model.Obs(float64(w.CacheCreation), model.Tokens),
		Output:          model.Obs(float64(w.Output), model.Tokens),
		Thinking:        model.Obs(float64(w.Thinking), model.Tokens),
		PromptVolume:    model.Der(volume, model.Tokens),
		PromptCost:      model.Der(promptCost, model.EIT),
		OutputCost:      model.Der(outputCost, model.EIT),
		TotalCost:       model.Der(promptCost+outputCost, model.EIT),
		VolumeCostRatio: model.Der(ratioOrZero(volume, promptCost), model.Ratio),
	}
	out.Caching = CachingReport{
		ReadShareOfVolume: model.Der(ratioOrZero(float64(w.CacheRead), volume), model.Ratio),
		WriteShareOfCost:  model.Der(ratioOrZero(writeCost, promptCost), model.Ratio),
		TTL5m:             model.Obs(float64(w.CacheCreation5m), model.Tokens),
		TTL1h:             model.Obs(float64(w.CacheCreation1h), model.Tokens),
		TTLBucket:         mergedTTL(ttls),
	}

	for _, t := range tools {
		out.Tools = append(out.Tools, *t)
	}
	sort.SliceStable(out.Tools, func(i, j int) bool {
		return out.Tools[i].ResultBytes.Value > out.Tools[j].ResultBytes.Value
	})

	out.Notes = []string{
		"Sessions are profiled separately and their costs added. Cost is additive; residency is not, because each session has its own context.",
		"Shares and ratios are recomputed against the totals, not averaged over sessions, so a long session weighs more than a short one.",
		"prompt_volume is raw tokens moved; prompt_cost is what they cost. They are different quantities and must not be added.",
		"EIT is an effective input-equivalent token: one full-price input token of the same model.",
	}
	return out
}

func ratioOrZero(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

// mergedTTL reports one TTL only when the whole set agrees on it.
func mergedTTL(seen map[string]bool) string {
	switch len(seen) {
	case 0:
		return "none observed"
	case 1:
		for k := range seen {
			return k
		}
	}
	return "mixed across sessions"
}

func addTotals(a, b RetrievalTotals) RetrievalTotals {
	a.Items.Value += b.Items.Value
	a.Bytes.Value += b.Bytes.Value
	a.Tokens.Value += b.Tokens.Value
	if a.Items.Unit == "" {
		a.Items, a.Bytes, a.Tokens = b.Items, b.Bytes, b.Tokens
		a.Items.Value, a.Bytes.Value, a.Tokens.Value =
			b.Items.Value, b.Bytes.Value, b.Tokens.Value
	}
	return a
}
