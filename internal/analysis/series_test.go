package analysis

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// probes writes profile-shaped JSON files, n runs per step.
func probes(t *testing.T, steps map[string][]float64) []string {
	t.Helper()
	dir := t.TempDir()
	var paths []string
	for label, values := range steps {
		for i, v := range values {
			path := filepath.Join(dir, fmt.Sprintf("%s-%d.json", label, i+1))
			body := fmt.Sprintf(
				`{"schema_version":1,"usage":{"total_cost":{"value":%f,"unit":"eit","provenance":"derived"}}}`, v)
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			paths = append(paths, path)
		}
	}
	return paths
}

func stepNamed(t *testing.T, s Series, label string) Step {
	t.Helper()
	for _, step := range s.Steps {
		if step.Label == label {
			return step
		}
	}
	t.Fatalf("no step %q", label)
	return Step{}
}

func TestRunsOfAStepAreGroupedAndReportedAsMedianAndRange(t *testing.T) {
	paths := probes(t, map[string][]float64{
		"step-01": {100, 110, 120, 130, 140},
	})
	s, err := LoadSeries(paths, 0)
	if err != nil {
		t.Fatal(err)
	}
	step := stepNamed(t, s, "step-01")

	if step.Runs != 5 {
		t.Errorf("runs = %d, want 5", step.Runs)
	}
	if step.Median != 120 {
		t.Errorf("median = %v, want 120", step.Median)
	}
	if step.Min != 100 || step.Max != 140 {
		t.Errorf("range = %v..%v, want 100..140", step.Min, step.Max)
	}
	if math.Abs(step.Spread-(40.0/120.0)) > 1e-9 {
		t.Errorf("spread = %v, want the range over the median", step.Spread)
	}
	if step.Thin {
		t.Error("five runs is not too few")
	}
}

func TestASingleRunIsFlaggedAsTooFewForABehaviouralEffect(t *testing.T) {
	// The methodological point: "the agent explored less" is behavioural, and
	// one sample of a stochastic process is not a measurement of it.
	s, err := LoadSeries(probes(t, map[string][]float64{"step-01": {100}}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !stepNamed(t, s, "step-01").Thin {
		t.Errorf("a single run must be flagged; the threshold is %d", MinRunsForBehaviour)
	}
}

func TestMedianOfAnEvenNumberOfRunsAveragesTheMiddlePair(t *testing.T) {
	s, err := LoadSeries(probes(t, map[string][]float64{"step-01": {10, 20, 30, 40}}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := stepNamed(t, s, "step-01").Median; got != 25 {
		t.Errorf("median = %v, want 25", got)
	}
}

func TestEffectComparesTheEndsOfTheSeries(t *testing.T) {
	s, err := LoadSeries(probes(t, map[string][]float64{
		"step-01": {200, 200, 200},
		"step-05": {150, 150, 150},
		"step-09": {50, 50, 50},
	}), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Comparable {
		t.Fatal("three steps are comparable")
	}
	if s.Effect != -150 {
		t.Errorf("effect = %v, want -150", s.Effect)
	}
	if math.Abs(s.EffectPct-(-0.75)) > 1e-9 {
		t.Errorf("effect share = %v, want -0.75", s.EffectPct)
	}
}

func TestPaybackIsTheInterventionCostOverTheSavingPerRun(t *testing.T) {
	// The arithmetic the published experiment could not do, because it could
	// only bound the intervention cost rather than measure it.
	s, err := LoadSeries(probes(t, map[string][]float64{
		"step-01": {160000, 160000, 160000},
		"step-09": {60000, 60000, 60000},
	}), 900000)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(s.PaybackRuns-9) > 1e-9 {
		t.Errorf("payback = %v runs, want 9 (900,000 over a 100,000 saving)", s.PaybackRuns)
	}
}

func TestNoPaybackWhenTheInterventionMadeThingsWorse(t *testing.T) {
	// A payback figure for an intervention that increased cost would be
	// nonsense, so none is reported.
	s, err := LoadSeries(probes(t, map[string][]float64{
		"step-01": {100, 100, 100},
		"step-09": {180, 180, 180},
	}), 900000)
	if err != nil {
		t.Fatal(err)
	}
	if s.Effect <= 0 {
		t.Fatal("this fixture got worse, so the effect should be positive")
	}
	if s.PaybackRuns != 0 {
		t.Errorf("payback = %v; there is nothing to pay back", s.PaybackRuns)
	}
}

func TestOneStepHasNoEffectToReport(t *testing.T) {
	s, err := LoadSeries(probes(t, map[string][]float64{"step-01": {100, 110}}), 500)
	if err != nil {
		t.Fatal(err)
	}
	if s.Comparable || s.Effect != 0 || s.PaybackRuns != 0 {
		t.Error("a single step cannot be compared to anything")
	}
}

func TestStepsAreOrderedByLabel(t *testing.T) {
	s, err := LoadSeries(probes(t, map[string][]float64{
		"step-09": {50}, "step-01": {200}, "step-05": {120},
	}), 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"step-01", "step-05", "step-09"}
	for i, label := range want {
		if s.Steps[i].Label != label {
			t.Fatalf("step %d = %q, want %q; ordering decides which ends are compared",
				i, s.Steps[i].Label, label)
		}
	}
}

func TestWrongInputIsRejectedWithAUsefulMessage(t *testing.T) {
	if _, err := LoadSeries(nil, 0); err == nil {
		t.Error("no files should be an error")
	}

	dir := t.TempDir()
	notProfile := filepath.Join(dir, "thing-1.json")
	if err := os.WriteFile(notProfile, []byte(`{"hello":"world"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadSeries([]string{notProfile}, 0)
	if err == nil {
		t.Fatal("a file that is not profile output should be rejected")
	}
	if !contains(err.Error(), "profile --json") {
		t.Errorf("the error should say what input is expected, got %q", err)
	}

	if _, err := LoadSeries([]string{filepath.Join(dir, "missing-1.json")}, 0); err == nil {
		t.Error("a missing file should be an error")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
