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
type CauseRow struct {
	Cause       string         `json:"cause"`
	Calls       model.Quantity `json:"calls"`
	Rebuilt     model.Quantity `json:"rebuilt_tokens"`
	Cost        model.Quantity `json:"cost"`
	Share       model.Quantity `json:"share_of_prompt_cost"`
	TTLWouldFix bool           `json:"avoidable_by_longer_ttl"`
}

// ExpiryReport isolates what a longer TTL could have addressed.
type ExpiryReport struct {
	Cost  model.Quantity `json:"cost"`
	Share model.Quantity `json:"share_of_prompt_cost"`
	Note  string         `json:"note"`
}

// BuildCache computes the cache report.
func BuildCache(s *model.Session, c analysis.CacheReport) Cache {
	r := Cache{
		SchemaVersion: SchemaVersion,
		Session:       sessionInfo(s),
		ObservedTTL:   c.ObservedTTL,
		Writes5m:      model.Obs(float64(c.Writes5m), model.Tokens),
		Writes1h:      model.Obs(float64(c.Writes1h), model.Tokens),
		PromptCost:    model.Der(c.TotalCostEIT, model.EIT),
		Expiry: ExpiryReport{
			Cost:  model.Der(c.ExpiryCostEIT, model.EIT),
			Share: model.Der(c.ExpiryShare, model.Ratio),
			Note: "Expiry is attributed by elimination, after the observable causes. " +
				"MCP server changes, plugin toggles and tool-deny rules also " +
				"invalidate the cache and are not visible in a transcript, so they " +
				"appear as unexplained rather than being blamed on the TTL.",
		},
		Warnings: s.Warnings,
		Notes: []string{
			"The TTL each call used is observed, from the API's own 5m/1h split.",
			"A cache entry's lifetime runs from request start and a read refreshes it, so calls starting closer together than the TTL keep the prefix warm.",
			"Claude Code exposes promptCacheTtl and subagentPromptCacheTtl (5m or 1h) since v2.1.242.",
		},
	}

	for cause, agg := range c.ByCause {
		r.Causes = append(r.Causes, CauseRow{
			Cause:       cause,
			Calls:       model.Obs(float64(agg.Calls), model.Calls),
			Rebuilt:     model.Obs(float64(agg.Tokens), model.Tokens),
			Cost:        model.Der(agg.CostEIT, model.EIT),
			Share:       model.Der(agg.Share, model.Ratio),
			TTLWouldFix: agg.TTLFixes,
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
		if c.TTLWouldFix {
			marker = "  <- a longer TTL would have avoided this"
		}
		fmt.Fprintf(b, "  %-22s %7s %14s %12s %7.1f%%%s\n",
			trunc(c.Cause, 22), num(int(c.Calls.Value)), num(int(c.Rebuilt.Value)),
			num(int(c.Cost.Value)), c.Share.Value*100, marker)
	}
	b.WriteString("\n")

	b.WriteString("TTL expiry\n")
	line(b, "  Cost", r.Expiry.Cost)
	fmt.Fprintf(b, "%-22s %13.1f%%   [%s]\n", "  Share of prompt cost", r.Expiry.Share.Value*100, r.Expiry.Share.Prov)
	fmt.Fprintf(b, "  %s\n\n", wrap(r.Expiry.Note, 72, "  "))

	_, err := io.WriteString(w, b.String())
	return err
}
