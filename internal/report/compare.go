package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// Two sessions side by side. Split out when the named interventions were
// deleted: comparing two measurements is measurement, and it had been sharing
// a file with the counterfactuals for no better reason than that both
// answered "and so?".

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
