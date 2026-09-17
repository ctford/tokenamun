package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/model"
)

// Series reports an experiment's probe runs.
type Series struct {
	SchemaVersion int           `json:"schema_version"`
	Steps         []SeriesStep  `json:"steps"`
	Effect        *SeriesEffect `json:"effect,omitempty"`
	Notes         []string      `json:"notes"`
}

// SeriesStep is one point, reported as a median and a range.
type SeriesStep struct {
	Label  string         `json:"label"`
	Runs   model.Quantity `json:"runs"`
	Median model.Quantity `json:"median"`
	Min    model.Quantity `json:"min"`
	Max    model.Quantity `json:"max"`
	Spread model.Quantity `json:"spread"`
	Thin   bool           `json:"too_few_runs"`
}

// SeriesEffect compares the ends of the series.
type SeriesEffect struct {
	Change  model.Quantity  `json:"change"`
	Share   model.Quantity  `json:"share_of_first"`
	Cost    *model.Quantity `json:"intervention_cost,omitempty"`
	Payback *model.Quantity `json:"payback_runs,omitempty"`
}

// BuildSeries assembles the report.
func BuildSeries(s analysis.Series) Series {
	out := Series{
		SchemaVersion: SchemaVersion,
		Notes: []string{
			"A step is reported as a median and a range, never a point value: agents are stochastic, so one run of a probe is a sample rather than a measurement.",
			fmt.Sprintf("A mechanical effect is real at one run. A behavioural effect - an agent choosing to explore less - wants at least %d.", analysis.MinRunsForBehaviour),
			"Outcomes are not visible here. A probe that produced a broken change consumes fewer tokens than one that worked, so record pass or fail alongside these numbers.",
			"Payback assumes the saving recurs on every future change of this shape and that the intervention cost was measured, not estimated.",
		},
	}

	for _, step := range s.Steps {
		out.Steps = append(out.Steps, SeriesStep{
			Label:  step.Label,
			Runs:   model.Obs(float64(step.Runs), model.Calls),
			Median: model.Der(step.Median, model.EIT),
			Min:    model.Obs(step.Min, model.EIT),
			Max:    model.Obs(step.Max, model.EIT),
			Spread: model.Der(step.Spread, model.Ratio),
			Thin:   step.Thin,
		})
	}

	if s.Comparable {
		e := &SeriesEffect{
			Change: model.Der(s.Effect, model.EIT),
			Share:  model.Der(s.EffectPct, model.Ratio),
		}
		if s.PaybackRuns > 0 {
			cost := model.Obs(s.InterventionCost, model.EIT)
			payback := model.Der(s.PaybackRuns, model.Calls)
			e.Cost, e.Payback = &cost, &payback
		}
		out.Effect = e
	}
	return out
}

// RenderSeries writes the human-facing series report.
func RenderSeries(w io.Writer, s Series) error {
	b := &strings.Builder{}
	b.WriteString("TOKENAMUN  probe series\n\n")

	fmt.Fprintf(b, "  %-24s %5s %12s %12s %12s %8s\n",
		"STEP", "RUNS", "MEDIAN", "MIN", "MAX", "SPREAD")
	for _, step := range s.Steps {
		marker := ""
		if step.Thin {
			marker = "  (too few runs)"
		}
		fmt.Fprintf(b, "  %-24s %5s %12s %12s %12s %7.1f%%%s\n",
			trunc(step.Label, 24), num(int(step.Runs.Value)),
			num(int(step.Median.Value)), num(int(step.Min.Value)),
			num(int(step.Max.Value)), step.Spread.Value*100, marker)
	}
	b.WriteString("\n")

	if s.Effect != nil {
		b.WriteString("First step to last\n")
		line(b, "  Change", s.Effect.Change)
		fmt.Fprintf(b, "%-22s %13.1f%%   [%s]\n", "  Share of first",
			s.Effect.Share.Value*100, s.Effect.Share.Prov)
		if s.Effect.Payback != nil {
			line(b, "  Intervention cost", *s.Effect.Cost)
			fmt.Fprintf(b, "%-22s %14s   [%s]\n", "  Pays back after",
				fmt.Sprintf("%.1f runs", s.Effect.Payback.Value), s.Effect.Payback.Prov)
		}
		b.WriteString("\n")
	}

	for _, n := range s.Notes {
		fmt.Fprintf(b, "  %s\n", wrap(n, 72, "  "))
	}
	b.WriteString("\n")

	_, err := io.WriteString(w, b.String())
	return err
}
