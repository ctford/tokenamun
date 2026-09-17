package whatif

import (
	"strings"
	"testing"
	"time"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
)

// twoSittings is a session that did some work, sat idle for an hour, then got
// a new prompt and carried everything from the first sitting forward.
func twoSittings(t *testing.T) Context {
	t.Helper()
	const preamble = 20000
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

	var s model.Session
	s.Ref = model.SessionRef{ID: "two-sittings", Origin: model.FromLocal}
	s.Estimator = model.TokenEstimator{BytesPerToken: 3.6, Method: "fixed ratio", Calibrated: true}

	// Six calls: 0-2 are the first task, 3-5 the second, an hour later.
	at := []time.Duration{0, time.Minute, 2 * time.Minute,
		62 * time.Minute, 63 * time.Minute, 64 * time.Minute}
	prompt := int64(preamble)
	for i, d := range at {
		u := model.TokenUsage{Output: 500}
		switch i {
		case 0:
			u.CacheCreation, u.CacheCreation5m = prompt, prompt
		case 3:
			// The gap expired the prefix, so this call rebuilt it.
			u.CacheCreation, u.CacheCreation5m = prompt, prompt
		default:
			u.CacheRead = prompt
		}
		s.Invocations = append(s.Invocations, model.ModelInvocation{
			Seq: i, RequestID: "req", Model: "claude-opus-5",
			Timestamp: start.Add(d), Usage: u,
		})
		prompt += 20000
	}
	// One prompt at the start of each sitting.
	s.PromptEntries = []model.PromptEntry{
		{Bytes: 200, InvocationSeq: 0},
		{Bytes: 200, InvocationSeq: 3},
	}
	// A file read in the first sitting and carried through the second.
	s.Retrievals = []model.RetrievedContent{{
		Seq: 0, Tool: "Read", Path: "internal/big.go", Channel: model.ChanFileRead,
		Bytes: 360000, Tokens: 100000, TokensProv: model.DerivedApprox, InvocationSeq: 1,
	}}

	cache := analysis.Cache(&s, analysis.TTL5m)
	return Context{
		Session: &s, Cache: cache, Carry: analysis.Carry(&s, cache),
		Weights: cost.For("claude-opus-5"), CompressionRatio: 0.5,
	}
}

func TestClearOnNewTaskFindsTheBoundaryAndPricesIt(t *testing.T) {
	c := twoSittings(t)
	boundaries, gaps := taskBoundaries(c.Session, c.Carry.Resets)
	if len(gaps) != 1 || len(boundaries) != 1 || boundaries[0] != 3 {
		t.Fatalf("expected one boundary at call 3, got %v (gaps %v)", boundaries, gaps)
	}

	r := ClearOnNewTask{}.Estimate(c)
	if !r.Applicable {
		t.Fatalf("a session with an hour's idle gap has a task boundary: %+v", r)
	}
	if r.Headline == nil || r.Headline.Quantity == nil {
		t.Fatal("the intervention must nominate a headline")
	}
	// The file read in the first sitting stops being re-sent in the second, so
	// the clear saves more than the preamble rewrite costs.
	if got := r.Headline.Quantity.Value; got >= 0 {
		t.Errorf("clearing should have saved something here, got %.0f", got)
	}
	if r.Headline.Quantity.Prov != model.Counterfactual {
		t.Error("the headline is a counterfactual and must be labelled as one")
	}
	// The saving must be a ceiling, and must say so where it will be quoted.
	if !strings.Contains(r.Caveat, "ceiling") {
		t.Errorf("the caveat must say the number is a ceiling: %q", r.Caveat)
	}
	var reReading bool
	for _, u := range r.Unknown {
		reReading = reReading || strings.HasPrefix(u, "re_reading")
	}
	if !reReading {
		t.Error("the cost of the intervention -- re-reading what was dropped -- must be listed as unknown")
	}
}

func TestClearOnNewTaskDoesNotClaimAnythingForASingleSitting(t *testing.T) {
	c := twoSittings(t)
	// Collapse the gap: one continuous sitting, so there is no new task.
	for i := range c.Session.Invocations {
		c.Session.Invocations[i].Timestamp =
			c.Session.Invocations[0].Timestamp.Add(time.Duration(i) * time.Minute)
	}
	r := ClearOnNewTask{}.Estimate(c)
	if r.Applicable {
		t.Error("a single sitting has no task boundary to clear at")
	}
	if !strings.Contains(r.NotMeasurable, "one sitting") {
		t.Errorf("it should say why: %q", r.NotMeasurable)
	}
	// The argument moves to the detail rather than being lost.
	if !strings.Contains(r.CaveatDetail, "session's shape") {
		t.Errorf("it should still say this is about the session, not the technique: %q",
			r.CaveatDetail)
	}
}

func TestClearOnNewTaskChargesThePreambleRewrite(t *testing.T) {
	// A clear invalidates the cached prefix. An estimate that dropped the old
	// context without paying to rebuild the preamble would be free money.
	c := twoSittings(t)
	boundaries, _ := taskBoundaries(c.Session, c.Carry.Resets)
	base := attributedCarry(c.Carry)
	cleared := attributedCarry(analysis.CarryWith(c.Session, c.Cache, boundaries))

	r := ClearOnNewTask{}.Estimate(c)
	var rewrite float64
	for _, f := range r.Counterfact {
		if strings.Contains(f.Label, "preamble re-written") {
			rewrite = f.Quantity.Value
		}
	}
	if rewrite <= 0 {
		t.Fatal("the preamble rewrite must be priced and visible")
	}
	if got, want := r.Headline.Quantity.Value, cleared+rewrite-base; got != want {
		t.Errorf("the headline is %.2f but the parts come to %.2f: the rewrite is not "+
			"being charged against the saving", got, want)
	}
}

func TestCarryWithExtraResetsTruncatesResidencyButNotThePreamble(t *testing.T) {
	c := twoSittings(t)
	base := analysis.Carry(c.Session, c.Cache)
	cleared := analysis.CarryWith(c.Session, c.Cache, []int{3})

	if len(base.Items) != 1 || len(cleared.Items) != 1 {
		t.Fatalf("expected one retrieval in each, got %d and %d", len(base.Items), len(cleared.Items))
	}
	if cleared.Items[0].ResidentFor >= base.Items[0].ResidentFor {
		t.Errorf("a clear at call 3 must shorten residency: %d then %d",
			base.Items[0].ResidentFor, cleared.Items[0].ResidentFor)
	}
	// The preamble is the one thing a clear rebuilds rather than drops, so
	// CarryWith must leave it alone and let the intervention price the rewrite.
	if cleared.PreambleCarryEIT != base.PreambleCarryEIT {
		t.Errorf("preamble carry moved with a counterfactual reset: %.2f then %.2f",
			base.PreambleCarryEIT, cleared.PreambleCarryEIT)
	}
	// And the observed reset list must not have the counterfactual in it.
	for _, k := range cleared.Resets {
		if k == 3 {
			t.Error("a counterfactual reset must not appear in the observed reset list")
		}
	}
}
