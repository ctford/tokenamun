package whatif

import (
	"testing"
	"time"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
)

func inv(seq int, at time.Duration, in, read, create int64) model.ModelInvocation {
	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	return model.ModelInvocation{
		Seq: seq, Model: "claude-opus-5", Version: "2.1.246", Effort: "high",
		Timestamp: base.Add(at),
		Usage: model.TokenUsage{
			Input: in, CacheRead: read,
			CacheCreation: create, CacheCreation5m: create,
		},
	}
}

// ctxFor builds a Context the way the CLI does, so tests exercise the real
// wiring rather than hand-made analysis results.
func ctxFor(s *model.Session) Context {
	cache := analysis.Cache(s, analysis.TTL5m)
	return Context{
		Session:          s,
		Cache:            cache,
		Carry:            analysis.Carry(s, cache),
		Weights:          cost.Default,
		CompressionRatio: 0.5,
	}
}

// The rule that keeps a counterfactual from reading as a measurement.
func TestEveryInterventionDeclaresWhatItCannotKnow(t *testing.T) {
	s := &model.Session{
		Invocations: []model.ModelInvocation{
			inv(0, 0, 1, 0, 30_000),
			inv(1, time.Minute, 1, 30_000, 500),
			inv(2, 30*time.Minute, 1, 300, 32_000),
		},
		Retrievals: []model.RetrievedContent{
			{Tool: "Bash", Category: model.CatToolOutput, Bytes: 5000, Tokens: 1400, InvocationSeq: 0},
			{Tool: "Bash", Category: model.CatToolOutput, Bytes: 5000, Tokens: 1400, InvocationSeq: 1, Hash: "dup"},
			{Tool: "Bash", Category: model.CatToolOutput, Bytes: 5000, Tokens: 1400, InvocationSeq: 2, Hash: "dup"},
		},
		Repeats:   []model.Repeat{{Hash: "dup", Tool: "Bash", Count: 2, Bytes: 5000, WasteByte: 5000}},
		Estimator: model.TokenEstimator{BytesPerToken: 3.5, Calibrated: true},
	}
	c := ctxFor(s)

	for _, i := range All() {
		r := i.Estimate(c)
		if len(r.Unknown) == 0 {
			t.Errorf("%s: returned no unknowns; there is always something we cannot know", i.Name())
		}
		var mentionsOutcome bool
		for _, u := range r.Unknown {
			if len(u) > 12 && u[:12] == "task_success" {
				mentionsOutcome = true
			}
		}
		if !mentionsOutcome {
			t.Errorf("%s: must state that task success is not observable", i.Name())
		}
		if r.Intervention != i.Name() {
			t.Errorf("%s: result names %q", i.Name(), r.Intervention)
		}
	}
}

// Counterfactual values must never be labelled as anything stronger.
func TestCounterfactualFindingsAreLabelledCounterfactual(t *testing.T) {
	s := &model.Session{
		Invocations: []model.ModelInvocation{
			inv(0, 0, 1, 0, 20_000),
			inv(1, 40*time.Minute, 1, 200, 25_000),
		},
		Retrievals: []model.RetrievedContent{
			{Tool: "Bash", Category: model.CatToolOutput, Bytes: 9000, Tokens: 2500, InvocationSeq: 0},
		},
		Estimator: model.TokenEstimator{BytesPerToken: 3.6, Calibrated: true},
	}
	c := ctxFor(s)
	for _, i := range All() {
		for _, f := range i.Estimate(c).Counterfact {
			if f.Quantity == nil {
				continue
			}
			if f.Quantity.Prov != model.Counterfactual {
				t.Errorf("%s: %q is in the counterfactual section but labelled %q",
					i.Name(), f.Label, f.Quantity.Prov)
			}
		}
	}
}

func TestCacheTTLChargesTheDoubledWritePrice(t *testing.T) {
	// A single long-idle session: the saving should exceed the repricing.
	s := &model.Session{Invocations: []model.ModelInvocation{
		inv(0, 0, 1, 0, 10_000),
		inv(1, 20*time.Minute, 1, 200, 200_000),
		inv(2, 21*time.Minute, 1, 200_000, 400),
	}}
	r := (CacheTTL{}).Estimate(ctxFor(s))

	if !r.Applicable {
		t.Fatal("a 5m session with an in-hour expiry is a candidate")
	}
	var net, repriced float64
	for _, f := range r.Counterfact {
		switch f.Label {
		case "net change":
			net = f.Quantity.Value
		case "ordinary writes repriced 1.25x to 2.0x":
			repriced = f.Quantity.Value
		}
	}
	if repriced <= 0 {
		t.Error("the doubled write price must be charged, not ignored")
	}
	if net >= 0 {
		t.Errorf("net = %.0f; this session should show a saving", net)
	}
}

func TestCacheTTLCanComeOutPositive(t *testing.T) {
	// Short bursts that never idle past five minutes pay the 2x write premium
	// for a lifetime they never use. The tool must be able to say so.
	invs := []model.ModelInvocation{inv(0, 0, 1, 0, 30_000)}
	for k := 1; k < 8; k++ {
		invs = append(invs, inv(k, time.Duration(k)*time.Minute, 1, int64(30_000+k*1000), 900))
	}
	s := &model.Session{Invocations: invs}
	r := (CacheTTL{}).Estimate(ctxFor(s))

	for _, f := range r.Counterfact {
		if f.Label == "net change" && f.Quantity.Value <= 0 {
			t.Fatalf("net = %.0f; with no expiries a longer TTL is a pure cost",
				f.Quantity.Value)
		}
	}
}

func TestCacheTTLIsNotApplicableWhenAlreadyOnOneHour(t *testing.T) {
	one := inv(0, 0, 1, 0, 10_000)
	one.Usage.CacheCreation5m = 0
	one.Usage.CacheCreation1h = 10_000
	r := (CacheTTL{}).Estimate(ctxFor(&model.Session{Invocations: []model.ModelInvocation{one}}))
	if r.Applicable {
		t.Error("a session already on the 1h TTL has nothing to change")
	}
	if r.NotMeasurable == "" {
		t.Error("it should say why")
	}
}

func TestReductionNeverExceedsTheObservedEligibleVolume(t *testing.T) {
	// The invariant from AGENTS.md: a counterfactual cannot remove more than
	// was there.
	s := &model.Session{
		Invocations: []model.ModelInvocation{inv(0, 0, 1, 0, 20_000), inv(1, time.Minute, 1, 20_000, 500)},
		Retrievals: []model.RetrievedContent{
			{Tool: "Bash", Category: model.CatToolOutput, Bytes: 8000, Tokens: 2200, InvocationSeq: 0},
			{Tool: "Read", Category: model.CatSourceCode, Bytes: 4000, Tokens: 1100, InvocationSeq: 1},
		},
		Estimator: model.TokenEstimator{BytesPerToken: 3.6, Calibrated: true},
	}
	c := ctxFor(s)

	for _, i := range []Intervention{OutputCompression{}, Caveman{}} {
		r := i.Estimate(c)
		var eligible, removed float64
		for _, f := range r.Observed {
			if f.Label == "eligible tool and MCP output" {
				eligible = f.Quantity.Value
			}
		}
		for _, f := range r.Counterfact {
			if f.Label == "bytes removed" {
				removed = f.Quantity.Value
			}
		}
		if removed > eligible {
			t.Errorf("%s: removed %.0f bytes of %.0f eligible", i.Name(), removed, eligible)
		}
		if eligible != 8000 {
			t.Errorf("%s: eligible = %.0f; source code is not eligible for output compression",
				i.Name(), eligible)
		}
	}
}

func TestCompressionStatesItsAssumedRatio(t *testing.T) {
	// The reader must be able to see the assumption they are trusting.
	s := &model.Session{
		Invocations: []model.ModelInvocation{inv(0, 0, 1, 0, 10_000)},
		Retrievals: []model.RetrievedContent{
			{Tool: "Bash", Category: model.CatToolOutput, Bytes: 5000, Tokens: 1400},
		},
		Estimator: model.TokenEstimator{BytesPerToken: 3.6},
	}
	c := ctxFor(s)
	c.CompressionRatio = 0.4

	var found bool
	for _, f := range (OutputCompression{}).Estimate(c).Derived {
		if f.Label == "compression ratio applied" {
			found = true
			if f.Quantity.Value != 0.4 {
				t.Errorf("ratio = %v, want 0.4", f.Quantity.Value)
			}
			if f.Note == "" {
				t.Error("the ratio must say where it came from")
			}
		}
	}
	if !found {
		t.Error("the applied ratio must be reported")
	}
}

func TestReplayedMeasurementReplacesTheAssumedRatio(t *testing.T) {
	s := &model.Session{
		Invocations: []model.ModelInvocation{inv(0, 0, 1, 0, 10_000)},
		Retrievals: []model.RetrievedContent{
			{Tool: "Bash", Category: model.CatToolOutput, Bytes: 10_000, Tokens: 2800},
		},
		Estimator: model.TokenEstimator{BytesPerToken: 3.6},
	}
	c := ctxFor(s)
	c.CompressionRatio = 0.5
	c.Replay = &ReplayResult{Command: "fakezip", Items: 3, InputBytes: 1000, OutputBytes: 250}

	r := (Caveman{}).Estimate(c)
	for _, f := range r.Derived {
		if f.Label == "compression ratio applied" {
			if f.Quantity.Value != 0.25 {
				t.Errorf("ratio = %v, want the measured 0.25 rather than the assumed 0.5",
					f.Quantity.Value)
			}
			if f.Note == "" || !contains(f.Note, "fakezip") {
				t.Errorf("the note should name the command that measured it, got %q", f.Note)
			}
		}
	}
}

func TestCavemanCitesTheGapBetweenClaimAndIndependentMeasurement(t *testing.T) {
	s := &model.Session{
		Invocations: []model.ModelInvocation{inv(0, 0, 1, 0, 10_000)},
		Retrievals: []model.RetrievedContent{
			{Tool: "Bash", Category: model.CatToolOutput, Bytes: 5000, Tokens: 1400},
		},
		Estimator: model.TokenEstimator{BytesPerToken: 3.6},
	}
	var mentions bool
	for _, u := range (Caveman{}).Estimate(ctxFor(s)).Unknown {
		if contains(u, "8.5%") && contains(u, "65%") {
			mentions = true
		}
	}
	if !mentions {
		t.Error("caveman should surface that the published and independent figures differ by 7x")
	}
}

func TestMCPToCLIRefusesToInventANumber(t *testing.T) {
	s := &model.Session{Invocations: []model.ModelInvocation{inv(0, 0, 1, 0, 28_000)}}
	r := (MCPToCLI{}).Estimate(ctxFor(s))

	if r.Applicable {
		t.Error("schema size is not in a transcript, so this is not measurable")
	}
	if len(r.Counterfact) != 0 {
		t.Error("it must not produce a counterfactual it cannot support")
	}
	if r.NotMeasurable == "" {
		t.Fatal("it must explain why")
	}
	if !contains(r.NotMeasurable, "compare") {
		t.Error("it should point at the A/B that would measure it")
	}
}

func TestRepeatedRetrievalIsNotApplicableWithoutRepeats(t *testing.T) {
	s := &model.Session{
		Invocations: []model.ModelInvocation{inv(0, 0, 1, 0, 10_000)},
		Retrievals: []model.RetrievedContent{
			{Tool: "Read", Path: "a.go", Category: model.CatSourceCode, Bytes: 500, Tokens: 140},
		},
		Estimator: model.TokenEstimator{BytesPerToken: 3.6},
	}
	r := (RepeatedRetrieval{}).Estimate(ctxFor(s))
	if r.Applicable {
		t.Error("nothing was retrieved twice")
	}
	if len(r.Unknown) == 0 {
		t.Error("even an inapplicable result declares its unknowns")
	}
}

func TestFindRejectsUnknownInterventions(t *testing.T) {
	if _, err := Find("make-it-fast"); err == nil {
		t.Fatal("an unknown intervention should be an error")
	} else if !contains(err.Error(), "cache-ttl") {
		t.Errorf("the error should list what is available, got %q", err)
	}
	for _, i := range All() {
		if got, err := Find(i.Name()); err != nil || got.Name() != i.Name() {
			t.Errorf("Find(%q) failed", i.Name())
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
