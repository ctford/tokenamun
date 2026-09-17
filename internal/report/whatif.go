package report

import (
	"fmt"
	"io"
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
