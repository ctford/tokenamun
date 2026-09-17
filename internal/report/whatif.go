package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
	"github.com/ctford/tokenamun/internal/whatif"
)

// WhatIf wraps a counterfactual result with the session it applies to.
type WhatIf struct {
	SchemaVersion int             `json:"schema_version"`
	Session       SessionInfo     `json:"session"`
	Result        whatif.Result   `json:"result"`
	Warnings      []model.Warning `json:"warnings,omitempty"`
	Notes         []string        `json:"notes"`
}

// BuildWhatIf assembles the report.
func BuildWhatIf(s *model.Session, r whatif.Result) WhatIf {
	return WhatIf{
		SchemaVersion: SchemaVersion,
		Session:       sessionInfo(s),
		Result:        r,
		Warnings:      s.Warnings,
		Notes: []string{
			"Observed values are in the telemetry. Derived values are arithmetic over them. Counterfactual values describe a session that never happened.",
			"A local reduction is not a whole-session saving: agent behaviour would have changed.",
			"The unknown section is not decoration. Read it before quoting any number above it.",
		},
	}
}

// RenderWhatIf writes the four-section result. All four sections always print,
// including unknown, which is the one that stops a counterfactual being read
// as a measurement.
func RenderWhatIf(w io.Writer, r WhatIf) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "TOKENAMUN  what-if: %s\n\n", r.Result.Intervention)
	fmt.Fprintf(b, "%s\n", wrap(r.Result.Description, 74, ""))
	fmt.Fprintf(b, "Session %s, %s API calls\n\n", r.Session.ID, num(r.Session.Calls))

	section(b, "Observed", r.Result.Observed)
	section(b, "Derived", r.Result.Derived)

	if r.Result.NotMeasurable != "" {
		b.WriteString("Not measurable from this data\n")
		fmt.Fprintf(b, "  %s\n\n", wrap(r.Result.NotMeasurable, 72, "  "))
	} else {
		section(b, "Counterfactual", r.Result.Counterfact)
	}

	// The caveat was missing from this report entirely, which is the one
	// place there is room for it. It is what to read before quoting the
	// headline, so it sits directly under the number it qualifies.
	if r.Result.Caveat != "" {
		b.WriteString("Caveat\n")
		fmt.Fprintf(b, "  %s\n", wrap(r.Result.Caveat, 72, "  "))
		if r.Result.CaveatDetail != "" {
			fmt.Fprintf(b, "  %s\n", wrap(r.Result.CaveatDetail, 72, "  "))
		}
		b.WriteString("\n")
	}

	b.WriteString("Unknown\n")
	for _, u := range r.Result.Unknown {
		fmt.Fprintf(b, "  - %s\n", wrap(u, 70, "    "))
	}
	b.WriteString("\n")

	_, err := io.WriteString(w, b.String())
	return err
}

func section(b *strings.Builder, title string, findings []whatif.Finding) {
	if len(findings) == 0 {
		return
	}
	fmt.Fprintf(b, "%s\n", title)
	for _, f := range findings {
		if f.Quantity == nil {
			// A finding with no number carries no provenance to print. It used
			// to be stamped [observed], which labelled an assumption stated in
			// the counterfactual section as a measurement.
			fmt.Fprintf(b, "%-46s %s\n", "  "+f.Label, f.Text)
			if f.Note != "" {
				fmt.Fprintf(b, "      %s\n", wrap(f.Note, 68, "      "))
			}
			continue
		}
		switch f.Quantity.Unit {
		case model.Ratio:
			fmt.Fprintf(b, "%-46s %11.1f%%   [%s]\n", "  "+f.Label,
				f.Quantity.Value*100, f.Quantity.Prov)
		case model.Bytes:
			fmt.Fprintf(b, "%-46s %12s   [%s]\n", "  "+f.Label,
				bytesStr(f.Quantity.Value), f.Quantity.Prov)
		default:
			fmt.Fprintf(b, "%-46s %12s   [%s]\n", "  "+f.Label,
				num(int(f.Quantity.Value)), f.Quantity.Prov)
		}
		if f.Note != "" {
			fmt.Fprintf(b, "      %s\n", wrap(f.Note, 68, "      "))
		}
	}
	b.WriteString("\n")
}

// Compare puts two sessions side by side.
type Compare struct {
	SchemaVersion int          `json:"schema_version"`
	A             SessionInfo  `json:"a"`
	B             SessionInfo  `json:"b"`
	Rows          []CompareRow `json:"rows"`
	Notes         []string     `json:"notes"`
}

// CompareRow is one metric in both sessions.
type CompareRow struct {
	Metric string         `json:"metric"`
	A      model.Quantity `json:"a"`
	B      model.Quantity `json:"b"`
	Delta  model.Quantity `json:"delta"`
	// Ratio is B over A, where that is meaningful.
	Ratio model.Quantity `json:"ratio"`
}

// BuildCompare assembles a comparison.
func BuildCompare(a, b Profile, aRetr, bRetr Retrieval) Compare {
	c := Compare{
		SchemaVersion: SchemaVersion,
		A:             a.Session,
		B:             b.Session,
		Notes: []string{
			"Two sessions are not a controlled experiment. They differ in task, code and operator as well as in whatever you changed.",
			"A difference here is a question worth asking, not an effect size.",
			"The preamble differs whenever the harness version, model or instruction files differ, which is almost always.",
		},
	}

	add := func(metric string, x, y model.Quantity) {
		delta := model.Der(y.Value-x.Value, x.Unit)
		ratio := model.Der(0, model.Ratio)
		if x.Value != 0 {
			ratio = model.Der(y.Value/x.Value, model.Ratio)
		}
		c.Rows = append(c.Rows, CompareRow{Metric: metric, A: x, B: y, Delta: delta, Ratio: ratio})
	}

	add("api calls", model.Obs(float64(a.Session.Calls), model.Calls),
		model.Obs(float64(b.Session.Calls), model.Calls))
	add("prompt volume", a.Usage.PromptVolume, b.Usage.PromptVolume)
	add("prompt cost", a.Usage.PromptCost, b.Usage.PromptCost)
	add("output tokens", a.Usage.Output, b.Usage.Output)
	add("total cost", a.Usage.TotalCost, b.Usage.TotalCost)
	add("retrieved bytes", aRetr.Total.Bytes, bRetr.Total.Bytes)
	add("redundant bytes", aRetr.Total.Redundant, bRetr.Total.Redundant)
	return c
}

// RenderCompare writes the comparison.
func RenderCompare(w io.Writer, c Compare) error {
	b := &strings.Builder{}
	b.WriteString("TOKENAMUN  compare\n\n")
	fmt.Fprintf(b, "A  %s  (%s calls)\n", c.A.ID, num(c.A.Calls))
	fmt.Fprintf(b, "B  %s  (%s calls)\n\n", c.B.ID, num(c.B.Calls))

	fmt.Fprintf(b, "  %-18s %14s %14s %14s %7s\n", "METRIC", "A", "B", "DELTA", "B/A")
	for _, row := range c.Rows {
		ratio := "-"
		if row.Ratio.Value != 0 {
			ratio = fmt.Sprintf("%.2fx", row.Ratio.Value)
		}
		fmt.Fprintf(b, "  %-18s %14s %14s %14s %7s\n", trunc(row.Metric, 18),
			num(int(row.A.Value)), num(int(row.B.Value)),
			num(int(row.Delta.Value)), ratio)
	}
	b.WriteString("\n")
	for _, n := range c.Notes {
		fmt.Fprintf(b, "  %s\n", wrap(n, 72, "  "))
	}
	b.WriteString("\n")

	_, err := io.WriteString(w, b.String())
	return err
}

// WhatIfAll is every intervention's bottom line, side by side.
//
// The HTML report has always shown this table; no command produced it, so an
// agent could ask about one intervention at a time but could not see which of
// them was worth asking about. That made the ranking a thing only a human
// with a browser could see.
type WhatIfAll struct {
	SchemaVersion int                `json:"schema_version"`
	Session       SessionInfo        `json:"session"`
	Rows          []WhatIfSummaryRow `json:"interventions"`
	Notes         []string           `json:"notes"`
}

// WhatIfSummaryRow is one intervention, summarised.
type WhatIfSummaryRow struct {
	Name    string `json:"name"`
	Targets string `json:"targets"`
	// Applicable is false when the evidence for this one is not in the data.
	// Such a row is not a zero: see NotMeasurable for why.
	Applicable bool `json:"applicable"`
	// Effect is the intervention's own nominated bottom line, in EIT.
	// Negative is a saving.
	Effect *model.Quantity `json:"effect,omitempty"`
	// Share is Effect against the session's whole token cost.
	Share *model.Quantity `json:"share_of_session,omitempty"`
	// Caveat is the thing to know before quoting Effect. Never empty on an
	// applicable row with an effect; the interventions are validated on that.
	Caveat string `json:"caveat,omitempty"`
	// CaveatDetail is the argument behind it, for the places with room.
	CaveatDetail  string `json:"caveat_detail,omitempty"`
	NotMeasurable string `json:"not_measurable,omitempty"`
	// Detail is the command that shows the full four-section result.
	Detail string `json:"detail_command"`
}

// BuildWhatIfAll runs every intervention over one session.
func BuildWhatIfAll(s *model.Session, ctx whatif.Context) WhatIfAll {
	out := WhatIfAll{
		SchemaVersion: SchemaVersion,
		Session:       sessionInfo(s),
		Notes: []string{
			"Each row is computed against this session, not quoted from a vendor. Negative is a saving.",
			"These are counterfactuals: they assume the agent would have behaved identically, and none of them can see whether the work still came out right.",
			"A row that is not applicable is not a zero. The evidence for it is not in this data, and why is in not_measurable.",
			"Rows are not additive. Two interventions that shrink the same content do not each save what they claim.",
			"Read the caveat before quoting the effect, and the full result for what it cannot know.",
		},
	}

	total := ctx.Carry.PromptCostEIT + ctx.Weights.OutputCost(s.Usage())
	for _, i := range whatif.All() {
		r := i.Estimate(ctx)
		row := WhatIfSummaryRow{
			Name: r.Intervention, Targets: r.Description,
			Applicable: r.Applicable, Caveat: r.Caveat, CaveatDetail: r.CaveatDetail,
			NotMeasurable: r.NotMeasurable,
			Detail:        fmt.Sprintf("tokenamun what-if %s %s", r.Intervention, s.Ref.ID),
		}
		if r.Headline != nil && r.Headline.Quantity != nil {
			q := *r.Headline.Quantity
			row.Effect = &q
			if q.Unit != model.Ratio && total > 0 {
				share := model.Quantity{
					Value: q.Value / total, Unit: model.Ratio, Prov: q.Prov,
				}
				row.Share = &share
			} else if q.Unit == model.Ratio {
				row.Share = &q
				row.Effect = nil
			}
		}
		out.Rows = append(out.Rows, row)
	}

	// Biggest saving first: the ranking is the point of showing them together.
	sort.SliceStable(out.Rows, func(i, j int) bool {
		return summaryRank(out.Rows[i]) < summaryRank(out.Rows[j])
	})
	return out
}

// summaryRank sorts by share, with unmeasurable rows last. They are last
// rather than at zero because zero is a claim, and "we cannot tell" is not.
func summaryRank(r WhatIfSummaryRow) float64 {
	if !r.Applicable {
		return 1e9
	}
	if r.Share != nil {
		return r.Share.Value
	}
	return 0
}

// RenderWhatIfAll writes the summary table.
func RenderWhatIfAll(w io.Writer, r WhatIfAll) error {
	b := &strings.Builder{}
	b.WriteString("TOKENAMUN  would an optimisation have helped?\n\n")
	fmt.Fprintf(b, "Session %s, %s API calls\n\n", r.Session.ID, num(r.Session.Calls))

	fmt.Fprintf(b, "%-22s %14s %9s  %s\n", "INTERVENTION", "EFFECT (EIT)", "SESSION", "TARGETS")
	for _, row := range r.Rows {
		effect, share := "not measurable", "--"
		if row.Applicable {
			effect = "--"
			if row.Effect != nil {
				effect = num(int(row.Effect.Value))
			}
			if row.Share != nil {
				share = pctStr(row.Share.Value)
			}
		}
		fmt.Fprintf(b, "%-22s %14s %9s  %s\n",
			trunc(row.Name, 22), effect, share, trunc(row.Targets, 60))
	}
	b.WriteString("\n")

	for _, row := range r.Rows {
		reason := strings.TrimSpace(row.Caveat + " " + row.CaveatDetail)
		if !row.Applicable {
			reason = row.NotMeasurable
		}
		if reason == "" {
			continue
		}
		fmt.Fprintf(b, "%s\n  %s\n  %s\n\n", row.Name,
			wrap(reason, 72, "  "), row.Detail)
	}

	for _, n := range r.Notes {
		fmt.Fprintf(b, "%s\n", wrap(n, 74, ""))
	}
	b.WriteString("\n")

	_, err := io.WriteString(w, b.String())
	return err
}
