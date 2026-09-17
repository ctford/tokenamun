package whatif

import (
	"fmt"
	"time"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/model"
)

// TaskGap is how long the session must have sat idle before a prompt for that
// prompt to be treated as the start of a new task.
//
// A guess, but a disclosed one, and it is a guess about a quantity that is
// observed rather than about the effect being estimated. Half an hour is long
// enough that the person left the desk and came back, which is when they
// usually came back to something else. Shorter thresholds pick up thinking
// pauses inside one task, and clearing there would be actively harmful.
const TaskGap = 30 * time.Minute

// ClearOnNewTask estimates starting a fresh context at each new task instead
// of carrying the previous one forward.
//
// The mechanism is the strongest part of this estimate and the behavioural
// assumption is the weakest, and those pull in opposite directions: a clear
// certainly stops the old context being re-sent, and it certainly makes the
// agent re-read some of what it just dropped. Only the first is countable, so
// the number here is a ceiling on the saving, not the saving.
type ClearOnNewTask struct{}

func (ClearOnNewTask) Name() string { return "clear-on-new-task" }

func (ClearOnNewTask) Describe() string {
	return "clear the context at each new task instead of carrying the last one forward"
}

func (ClearOnNewTask) Estimate(c Context) Result {
	r := Result{
		Intervention: "clear-on-new-task",
		Description:  ClearOnNewTask{}.Describe(),
		Unknown: []string{
			"re_reading: the agent would have had to fetch again whatever it still " +
				"needed. That is the whole cost of the intervention and none of it is " +
				"in this transcript, so the saving below is a ceiling.",
			"task_boundaries: inferred from idle gaps, because no transcript records " +
				"where one task ended. Two tasks back to back look like one.",
			"restatement: after a clear you have to say again what you were doing, " +
				"and the agent has to re-establish where it was.",
			behaviourUnknown,
			outcomeUnknown,
		},
	}

	boundaries, gaps := taskBoundaries(c.Session, c.Carry.Resets)

	r.Observed = []Finding{
		obs("api calls", float64(c.Carry.Calls), model.Calls),
		obs("idle gaps over the task threshold", float64(len(gaps)), model.Calls,
			fmt.Sprintf("a gap of at least %s before something you typed", gapStr(TaskGap))),
		obs("context resets that already happened", float64(len(c.Carry.Resets)), model.Calls,
			"compaction already cleared the context at these calls, so clearing there again saves nothing"),
		obs("prompt cost", c.Carry.PromptCostEIT, model.EIT),
		obs("session preamble, re-written by every clear", float64(c.Carry.Preamble), model.Tokens),
	}

	if len(boundaries) == 0 {
		r.Applicable = false
		r.NotMeasurable = fmt.Sprintf(
			"no task boundary is detectable in this session: nothing you typed follows "+
				"an idle gap of %s or more that compaction had not already cleared. That "+
				"is a statement about this session's shape, not about the technique -- a "+
				"single sitting has no new task to clear for.", gapStr(TaskGap))
		return r
	}
	r.Applicable = true
	r.Acts = AxisRoundTrips

	base := attributedCarry(c.Carry)
	cleared := attributedCarry(analysis.CarryWith(c.Session, c.Cache, boundaries))

	// A clear invalidates the cached prefix, so the harness writes the
	// preamble again at the write rate instead of reading it. CarryWith does
	// not model that -- it truncates residency, and the preamble is the one
	// thing a clear rebuilds rather than drops -- so it is charged here, where
	// it is also visible as its own line.
	extraPreambleWrites := float64(len(boundaries)) * float64(c.Carry.Preamble) *
		(c.Weights.CacheWrite5m - c.Weights.CacheRead)
	net := cleared + extraPreambleWrites - base

	// What the clears would have truncated, as a size rather than a price, so
	// the reader can see how much context was being carried across a boundary
	// in the first place.
	var droppedTokens float64
	for _, it := range c.Carry.Items {
		for _, b := range boundaries {
			if it.EnteredAt < b && it.EnteredAt+it.ResidentFor > b {
				droppedTokens += it.Tokens
				break
			}
		}
	}

	r.Addressable = addressable("everything carried across a boundary", base, c.Total)
	if base > 0 {
		r.Reduction = net / base
	}

	r.Derived = []Finding{
		der("task boundaries", float64(len(boundaries)), model.Calls),
		der("content carried across a boundary", droppedTokens, model.Tokens,
			"retrieved before a boundary and still resident after it"),
		der("cost of carrying everything, as billed", base, model.EIT,
			"preamble, prompts, model output and retrieved content"),
	}

	r.Counterfact = []Finding{
		cf("cost of carrying everything, with the clears", cleared+extraPreambleWrites, model.EIT),
		cf("preamble re-written after each clear", extraPreambleWrites, model.EIT,
			"a clear invalidates the cached prefix, so the system prompt and tool "+
				"schemas are written again at full write price"),
		cf("net change, before any re-reading", net, model.EIT,
			"negative is a saving. It is a ceiling: it credits the clear with dropping "+
				"content the agent would have gone and fetched again"),
	}
	if c.Carry.PromptCostEIT > 0 {
		r.Counterfact = append(r.Counterfact,
			cf("net change, share of prompt cost", net/c.Carry.PromptCostEIT, model.Ratio))
	}
	r.Headline = &r.Counterfact[2]
	r.Caveat = "A ceiling, not an estimate."
	r.CaveatDetail = fmt.Sprintf(
		"Clearing is free only for content the agent never needed again, and %s of "+
			"content was being carried across a boundary here -- whatever share of that "+
			"it would have re-fetched comes straight back off the saving. The boundaries "+
			"are inferred from idle gaps of %s or more.",
		tokensStr(droppedTokens), gapStr(TaskGap))
	return r
}

// attributedCarry totals the four things a carry report can attribute, which
// is what a clear changes. The unattributed remainder is estimation error and
// does not move when the context is cleared, so including it would dilute the
// difference between the two scenarios with a constant.
func attributedCarry(r analysis.CarryReport) float64 {
	total := r.PreambleCarryEIT + r.PromptCarryEIT + r.AssistantCarryEIT
	for _, it := range r.Items {
		total += it.CarryEIT
	}
	return total
}

// taskBoundaries finds the calls where a clear would plausibly have happened:
// a prompt you typed after the session had been sitting idle.
//
// The gap is measured between consecutive API calls, which is the only clock a
// transcript has. It therefore measures how long the session was idle, not how
// long you were away, and those differ if something else was using the
// session. Calls that already reset the context are excluded: compaction
// cleared it there, so a clear would have saved nothing.
func taskBoundaries(s *model.Session, resets []int) (boundaries []int, gaps []time.Duration) {
	alreadyReset := map[int]bool{}
	for _, k := range resets {
		alreadyReset[k] = true
	}
	// Which calls carried something you typed.
	typed := map[int]bool{}
	for _, pe := range s.PromptEntries {
		if pe.Bytes > 0 && pe.InvocationSeq >= 0 {
			typed[pe.InvocationSeq] = true
		}
	}

	var prev *model.ModelInvocation
	for i := range s.Invocations {
		inv := s.Invocations[i]
		if !inv.IsRealCall() {
			continue
		}
		if prev != nil && typed[inv.Seq] {
			gap := inv.Timestamp.Sub(prev.Timestamp)
			if gap >= TaskGap {
				gaps = append(gaps, gap)
				if !alreadyReset[inv.Seq] {
					boundaries = append(boundaries, inv.Seq)
				}
			}
		}
		prev = &s.Invocations[i]
	}
	return boundaries, gaps
}

// tokensStr is a compact token count for prose.
func tokensStr(v float64) string {
	switch {
	case v >= 1e6:
		return fmt.Sprintf("%.1fM tokens", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.0fk tokens", v/1e3)
	default:
		return fmt.Sprintf("%.0f tokens", v)
	}
}

// gapStr says a duration the way a person would.
func gapStr(d time.Duration) string {
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%d hours", int(d/time.Hour))
	case d >= time.Minute:
		return fmt.Sprintf("%d minutes", int(d/time.Minute))
	default:
		return d.String()
	}
}
