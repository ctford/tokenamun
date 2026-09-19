package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/model"
)

// Cache reports what cache behaviour cost, attributed to causes.
type Cache struct {
	SchemaVersion int             `json:"schema_version"`
	Session       SessionInfo     `json:"session"`
	ObservedTTL   string          `json:"observed_ttl"`
	Writes5m      model.Quantity  `json:"cache_writes_5m"`
	Writes1h      model.Quantity  `json:"cache_writes_1h"`
	PromptCost    model.Quantity  `json:"prompt_cost"`
	Causes        []CauseRow      `json:"by_cause"`
	Expiry        ExpiryReport    `json:"ttl_expiry"`
	Warnings      []model.Warning `json:"warnings,omitempty"`
	Notes         []string        `json:"notes"`
}

// CauseRow is one attributed cause of cache rebuilding.
//
// AvoidableCalls and AvoidableTokens replaced a single bool. A flag on a row
// of twenty calls says "a longer TTL would have avoided this" about all of
// them on the evidence of one, which is the kind of rounding-up this tool
// exists to refuse.
type CauseRow struct {
	Cause           string         `json:"cause"`
	Calls           model.Quantity `json:"calls"`
	Rebuilt         model.Quantity `json:"rebuilt_tokens"`
	Cost            model.Quantity `json:"cost"`
	Share           model.Quantity `json:"share_of_prompt_cost"`
	AvoidableCalls  model.Quantity `json:"avoidable_by_longer_ttl_calls"`
	AvoidableTokens model.Quantity `json:"avoidable_by_longer_ttl_tokens"`
}

// ExpiryReport isolates what a longer TTL could have addressed.
type ExpiryReport struct {
	Cost  model.Quantity `json:"cost"`
	Share model.Quantity `json:"share_of_prompt_cost"`
	// Avoidable is the part a 1-hour lifetime would have covered. Gaps over
	// an hour expire under either TTL.
	Avoidable model.Quantity `json:"avoidable_by_1h"`
	// Calls is how many expiries were attributed, and WarmAt1h how many of
	// them a longer lifetime would have covered. The rest ran over an hour
	// and expire either way.
	Calls    model.Quantity `json:"expiry_calls"`
	WarmAt1h model.Quantity `json:"would_stay_warm_at_1h_calls"`
	// Saved and Premium are the two halves of the net, as positive
	// magnitudes: what the avoided rewrites stop costing, and what the
	// surviving writes cost extra at 2.0x.
	Saved   model.Quantity `json:"longer_ttl_saved"`
	Premium model.Quantity `json:"longer_ttl_premium"`
	// LongerTTLNet is Premium minus Saved. Negative is a saving.
	LongerTTLNet   model.Quantity `json:"longer_ttl_net"`
	LongerTTLShare model.Quantity `json:"longer_ttl_net_share"`
	Note           string         `json:"note"`
	NetNote        string         `json:"net_note"`
}

// BuildCache computes the cache report.
func BuildCache(s *model.Session, c analysis.CacheReport) Cache {
	expiry := c.ByCause[analysis.CauseTTLExpiry]
	r := Cache{
		SchemaVersion: SchemaVersion,
		Session:       sessionInfo(s),
		ObservedTTL:   c.ObservedTTL,
		Writes5m:      model.Obs(float64(c.Writes5m), model.Tokens),
		Writes1h:      model.Obs(float64(c.Writes1h), model.Tokens),
		PromptCost:    model.Der(c.TotalCostEIT, model.EIT),
		Expiry: ExpiryReport{
			Cost:      model.Der(c.ExpiryCostEIT, model.EIT),
			Share:     model.Der(c.ExpiryShare, model.Ratio),
			Avoidable: model.Der(float64(c.AvoidableTokens), model.Tokens),
			Calls:     model.Der(float64(expiry.Calls), model.Calls),
			WarmAt1h:  model.Der(float64(expiry.AvoidableCalls), model.Calls),
			Saved: model.Quantity{
				Value: c.LongerTTLSavedEIT, Unit: model.EIT, Prov: model.Counterfactual,
			},
			Premium: model.Quantity{
				Value: c.LongerTTLPremiumEIT, Unit: model.EIT, Prov: model.Counterfactual,
			},
			LongerTTLNet: model.Quantity{
				Value: c.LongerTTLNetEIT, Unit: model.EIT, Prov: model.Counterfactual,
			},
			LongerTTLShare: model.Quantity{
				Value: c.LongerTTLShare, Unit: model.Ratio, Prov: model.Counterfactual,
			},
			NetNote: "A counterfactual, but with no assumed parameter: every input " +
				"is observed. Both halves are shown because a net figure asks to be " +
				"trusted and these can be checked. The avoided rewrite is not free -- " +
				"the prefix is still sent, as a cache read -- so the saving is the gap " +
				"between the two rates, not the whole write. The premium is every " +
				"write you still make, repriced from 1.25x to 2.0x. On a session of " +
				"short bursts the premium wins and switching costs you money.",
			Note: "Expiry is attributed by elimination, after the observable causes. " +
				"MCP server changes, plugin toggles and tool-deny rules also " +
				"invalidate the cache and are not visible in a transcript, so they " +
				"appear as unexplained rather than being blamed on the TTL.",
		},
		Warnings: mixedPricingWarning(s.Warnings, c.ReadRates),
		Notes: []string{
			"The TTL each call used is observed, from the API's own 5m/1h split.",
			"A cache entry's lifetime runs from request start and a read refreshes it, so calls starting closer together than the TTL keep the prefix warm.",
			"Claude Code exposes promptCacheTtl and subagentPromptCacheTtl (5m or 1h) since v2.1.242.",
		},
	}

	for cause, agg := range c.ByCause {
		r.Causes = append(r.Causes, CauseRow{
			Cause:           cause,
			Calls:           model.Obs(float64(agg.Calls), model.Calls),
			Rebuilt:         model.Obs(float64(agg.Tokens), model.Tokens),
			Cost:            model.Der(agg.CostEIT, model.EIT),
			Share:           model.Der(agg.Share, model.Ratio),
			AvoidableCalls:  model.Der(float64(agg.AvoidableCalls), model.Calls),
			AvoidableTokens: model.Der(float64(agg.AvoidableTokens), model.Tokens),
		})
	}
	sort.SliceStable(r.Causes, func(i, j int) bool { return r.Causes[i].Cost.Value > r.Causes[j].Cost.Value })
	return r
}

// RenderCache writes the human-facing cache report.
func RenderCache(w io.Writer, r Cache) error {
	b := &strings.Builder{}
	b.WriteString("TOKENAMUN  prompt cache\n\n")
	fmt.Fprintf(b, "Session %s\n", r.Session.ID)
	fmt.Fprintf(b, "  API calls          %s\n", num(r.Session.Calls))
	fmt.Fprintf(b, "%-22s %14s   [%s]\n", "  Observed TTL", r.ObservedTTL, model.Observed)
	line(b, "  Writes at 5m", r.Writes5m)
	line(b, "  Writes at 1h", r.Writes1h)
	line(b, "  Prompt cost", r.PromptCost)
	for _, w := range r.Warnings {
		fmt.Fprintf(b, "  ! %s\n", wrap(w.Detail, 70, "    "))
	}
	b.WriteString("\n")

	if len(r.Causes) == 0 {
		b.WriteString("No large cache rebuilds. The prefix stayed warm for this session.\n\n")
		_, err := io.WriteString(w, b.String())
		return err
	}

	b.WriteString("Why the cache was rebuilt\n")
	fmt.Fprintf(b, "  %-22s %7s %14s %12s %8s\n", "CAUSE", "CALLS", "TOKENS", "COST (EIT)", "% COST")
	for _, c := range r.Causes {
		marker := ""
		if n := int(c.AvoidableCalls.Value); n > 0 {
			marker = fmt.Sprintf("  <- %s of %s avoidable at 1h",
				num(n), num(int(c.Calls.Value)))
		}
		fmt.Fprintf(b, "  %-22s %7s %14s %12s %7.1f%%%s\n",
			trunc(c.Cause, 22), num(int(c.Calls.Value)), num(int(c.Rebuilt.Value)),
			num(int(c.Cost.Value)), c.Share.Value*100, marker)
	}
	b.WriteString("\n")

	b.WriteString("TTL expiry\n")
	line(b, "  Cost", r.Expiry.Cost)
	fmt.Fprintf(b, "%-22s %13.1f%%   [%s]\n", "  Share of prompt cost", r.Expiry.Share.Value*100, r.Expiry.Share.Prov)
	line(b, "  Avoidable at 1h", r.Expiry.Avoidable)
	fmt.Fprintf(b, "  %s\n\n", wrap(r.Expiry.Note, 72, "  "))

	b.WriteString("Switching to the 1-hour TTL\n")
	line(b, "  Expirations", r.Expiry.Calls)
	line(b, "  Would stay warm", r.Expiry.WarmAt1h)
	line(b, "  Avoided rewrites save", r.Expiry.Saved)
	line(b, "  1h write premium costs", r.Expiry.Premium)
	line(b, "  Net change", r.Expiry.LongerTTLNet)
	fmt.Fprintf(b, "%-22s %13.1f%%   [%s]\n", "  Share of prompt cost",
		r.Expiry.LongerTTLShare.Value*100, r.Expiry.LongerTTLShare.Prov)
	fmt.Fprintf(b, "  %s\n\n", wrap(r.Expiry.NetNote, 72, "  "))

	_, err := io.WriteString(w, b.String())
	return err
}

// mixedPricingWarning adds a warning when the set spans more than one
// pricing.
//
// It belongs here and not only in `profile` because this report's headline is
// a share -- the net as a percentage of prompt cost -- and a mixed
// denominator is harder to notice than a mixed total. The numerator is each
// pricing's own arithmetic and is right either way.
func mixedPricingWarning(ws []model.Warning, rates []float64) []model.Warning {
	if len(rates) < 2 {
		return ws
	}
	return append(ws, model.Warning{
		Code: "mixed_pricing",
		Detail: "This set spans more than one cache-read rate, so the EIT " +
			"totals add quantities of different sizes. Each pricing's " +
			"counterfactual is computed at its own rates; the share it is " +
			"expressed against is the mixed one.",
	})
}

// BuildCacheOf assembles the report from an already-merged analysis, for the
// "all" selector where there is no single session to describe.
func BuildCacheOf(info SessionInfo, c analysis.CacheReport) Cache {
	r := BuildCache(&model.Session{}, c)
	r.Session = info
	return r
}
