package analysis

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Series aggregates repeated probe runs from an experiment.
//
// The shape it supports is the one published experiments actually use: hold a
// representative change constant, vary the codebase, and measure what the
// probe consumes. Two disciplines come with that, and this type exists to
// enforce the second:
//
//   - A mechanical effect - fewer bytes in a file - is real at n=1. A
//     behavioural effect, such as an agent choosing to explore less, is not,
//     and "the agent explored less" is what these experiments measure. So a
//     step reports a median and a range, never a point value.
//   - Token numbers without an outcome are uninterpretable. This type cannot
//     see outcomes and says so rather than implying the runs succeeded.
type Series struct {
	Steps []Step `json:"steps"`
	// Effect compares the first and last step, which is the before/after the
	// experiment was run to find.
	Effect     float64 `json:"effect_eit"`
	EffectPct  float64 `json:"effect_share_of_first"`
	Comparable bool    `json:"comparable"`
	// Payback is how many probe runs the intervention has to save for before
	// it pays for itself, when an intervention cost is supplied.
	InterventionCost float64 `json:"intervention_cost_eit,omitempty"`
	PaybackRuns      float64 `json:"payback_runs,omitempty"`
}

// Step is one point in the series: all the runs sharing a label.
type Step struct {
	Label  string    `json:"label"`
	Runs   int       `json:"runs"`
	Median float64   `json:"median_eit"`
	Min    float64   `json:"min_eit"`
	Max    float64   `json:"max_eit"`
	Spread float64   `json:"spread_share_of_median"`
	Values []float64 `json:"values_eit"`
	// Thin is true when there are too few runs to distinguish a behavioural
	// effect from noise.
	Thin bool `json:"too_few_runs"`
}

// MinRunsForBehaviour is the point below which a difference between steps
// should not be read as an effect. Agents are stochastic; a single run of a
// probe is one sample of a distribution, not a measurement of it.
const MinRunsForBehaviour = 5

// probeFile is the subset of a profile JSON that a series needs.
type probeFile struct {
	Usage struct {
		TotalCost struct {
			Value float64 `json:"value"`
		} `json:"total_cost"`
	} `json:"usage"`
}

// LoadSeries reads profile JSON files and groups them into steps.
//
// Runs of the same step are recognised by filename: everything up to the last
// hyphen is the label, so step-07-probe-1.json and step-07-probe-2.json are
// two runs of step-07-probe. That keeps the driver script free of a manifest
// format while still allowing repeats.
func LoadSeries(paths []string, interventionCost float64) (Series, error) {
	if len(paths) == 0 {
		return Series{}, fmt.Errorf("series needs at least one profile JSON file")
	}

	byLabel := map[string][]float64{}
	var order []string
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Series{}, err
		}
		var p probeFile
		if err := json.Unmarshal(raw, &p); err != nil {
			return Series{}, fmt.Errorf("%s: %w", path, err)
		}
		if p.Usage.TotalCost.Value == 0 {
			return Series{}, fmt.Errorf("%s: no usage.total_cost; is it `tokenamun profile --json` output?", path)
		}
		label := labelOf(path)
		if _, seen := byLabel[label]; !seen {
			order = append(order, label)
		}
		byLabel[label] = append(byLabel[label], p.Usage.TotalCost.Value)
	}
	sort.Strings(order)

	var s Series
	for _, label := range order {
		values := byLabel[label]
		sort.Float64s(values)
		step := Step{
			Label:  label,
			Runs:   len(values),
			Median: median(values),
			Min:    values[0],
			Max:    values[len(values)-1],
			Values: values,
			Thin:   len(values) < MinRunsForBehaviour,
		}
		if step.Median > 0 {
			step.Spread = (step.Max - step.Min) / step.Median
		}
		s.Steps = append(s.Steps, step)
	}

	if len(s.Steps) >= 2 {
		first, last := s.Steps[0], s.Steps[len(s.Steps)-1]
		s.Comparable = true
		s.Effect = last.Median - first.Median
		if first.Median > 0 {
			s.EffectPct = s.Effect / first.Median
		}
		if interventionCost > 0 && s.Effect < 0 {
			s.InterventionCost = interventionCost
			s.PaybackRuns = interventionCost / -s.Effect
		}
	}
	return s, nil
}

// labelOf derives a step label from a filename.
func labelOf(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	if i := strings.LastIndex(name, "-"); i > 0 {
		return name[:i]
	}
	return name
}

func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
