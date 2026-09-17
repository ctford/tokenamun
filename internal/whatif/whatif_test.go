package whatif

import (
	"math"
	"strings"
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
			{Tool: "Bash", Bytes: 5000, Tokens: 1400, InvocationSeq: 0},
			{Tool: "Bash", Bytes: 5000, Tokens: 1400, InvocationSeq: 1, Hash: "dup"},
			{Tool: "Bash", Bytes: 5000, Tokens: 1400, InvocationSeq: 2, Hash: "dup"},
		},
		Repeats: []model.Repeat{{
			Hash: "dup", Tool: "Bash", Count: 2, Bytes: 5000, WasteByte: 5000,
			RetrievalSeqs: []int{1, 2},
		}},
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
			{Tool: "Bash", Bytes: 9000, Tokens: 2500, InvocationSeq: 0},
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
			{Tool: "Bash", Bytes: 8000, Tokens: 2200, InvocationSeq: 0},
			{Tool: "Read", Bytes: 4000, Tokens: 1100, InvocationSeq: 1},
		},
		Estimator: model.TokenEstimator{BytesPerToken: 3.6, Calibrated: true},
	}
	c := ctxFor(s)

	for _, i := range []Intervention{OutputCompression{}, Caveman{}} {
		r := i.Estimate(c)
		var eligible, removed float64
		for _, f := range r.Observed {
			if f.Label == "eligible, delivered by a tool" {
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
			t.Errorf("%s: eligible = %.0f; a direct file Read is not delivered through a "+
				"channel a compression proxy sits in front of", i.Name(), eligible)
		}
	}
}

// Eligibility follows the delivery channel, not the content category: a proxy
// compresses whatever comes back from the shell, including a decision record
// that arrived via `cat`.
func TestShellDeliveredFileContentIsEligible(t *testing.T) {
	s := &model.Session{
		Invocations: []model.ModelInvocation{inv(0, 0, 1, 0, 20_000), inv(1, time.Minute, 1, 20_000, 500)},
		Retrievals: []model.RetrievedContent{
			// A decision record read through the shell: classified as an ADR,
			// still delivered by Bash.
			{Tool: "Bash", Path: "docs/decisions/a.md",
				Bytes: 5000, Tokens: 1400, InvocationSeq: 0},
			// The same content read directly: a different channel.
			{Tool: "Read", Path: "docs/decisions/b.md",
				Bytes: 5000, Tokens: 1400, InvocationSeq: 1},
		},
		Estimator: model.TokenEstimator{BytesPerToken: 3.6, Calibrated: true},
	}
	r := (OutputCompression{}).Estimate(ctxFor(s))

	var eligible, content float64
	for _, f := range r.Observed {
		switch f.Label {
		case "eligible, delivered by a tool":
			eligible = f.Quantity.Value
		case "  of which identifiable content":
			content = f.Quantity.Value
		}
	}
	if eligible != 5000 {
		t.Errorf("eligible = %.0f, want the 5000 bytes that came through the shell", eligible)
	}
	if content != 5000 {
		t.Errorf("identifiable content = %.0f; the split exists to show what is risky to compress", content)
	}
}

func TestCompressionStatesItsAssumedRatio(t *testing.T) {
	// The reader must be able to see the assumption they are trusting.
	s := &model.Session{
		Invocations: []model.ModelInvocation{inv(0, 0, 1, 0, 10_000)},
		Retrievals: []model.RetrievedContent{
			{Tool: "Bash", Bytes: 5000, Tokens: 1400},
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
			{Tool: "Bash", Bytes: 10_000, Tokens: 2800},
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
			{Tool: "Bash", Bytes: 5000, Tokens: 1400},
		},
		Estimator: model.TokenEstimator{BytesPerToken: 3.6},
	}
	r := (Caveman{}).Estimate(ctxFor(s))

	// The spread is the finding, so it is stated where a reader will see it
	// rather than buried in the unknowns.
	if !contains(r.NotMeasurable, "8.5%") || !contains(r.NotMeasurable, "65%") {
		t.Errorf("caveman should say the published figures span 8.5%% to 65%%: %q",
			r.NotMeasurable)
	}

	// And it must not report a single number from inside that spread. The
	// first version priced --ratio's default of 50%, which came from neither
	// the vendor nor the independent test while carrying the vendor's name.
	if r.Applicable {
		t.Error("an eight-fold spread is not a measurement")
	}
	if r.Headline != nil {
		t.Errorf("caveman must not nominate a headline: %+v", r.Headline)
	}
	if r.Addressable != nil {
		t.Error("no addressable-times-reduction either; that implies a chosen ratio")
	}

	// Both ends are priced, because the range is what there is to say.
	var ends int
	for _, f := range r.Counterfact {
		if contains(f.Label, "vendor") || contains(f.Label, "independent") {
			ends++
		}
	}
	if ends < 4 {
		t.Errorf("both ends should be priced in EIT and as a share, got %d rows", ends)
	}
	if !contains(r.CaveatDetail, "replay-with") {
		t.Errorf("it should point at the way to settle it: %q", r.CaveatDetail)
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
	// The one-line reason says what is missing; the detail says what to do
	// about it, which is where there is room for a command.
	if !contains(r.CaveatDetail, "compare") {
		t.Error("it should point at the A/B that would measure it")
	}
}

// The join must work for content with no path, which is most of it: shell
// output has no file to match on, so an earlier path-based join reported zero
// avoidable cost for every session.
func TestRepeatedRetrievalJoinsCarryForPathlessContent(t *testing.T) {
	s := &model.Session{
		Invocations: []model.ModelInvocation{
			inv(0, 0, 1, 0, 20_000),
			inv(1, time.Minute, 1, 20_000, 800),
			inv(2, 2*time.Minute, 1, 20_800, 800),
			inv(3, 3*time.Minute, 1, 21_600, 800),
		},
		Retrievals: []model.RetrievedContent{
			{Seq: 0, Tool: "Bash", Bytes: 4000, Tokens: 1100, InvocationSeq: 0, Hash: "dup"},
			{Seq: 1, Tool: "Bash", Bytes: 4000, Tokens: 1100, InvocationSeq: 2, Hash: "dup"},
		},
		Repeats: []model.Repeat{{
			Hash: "dup", Tool: "Bash", Count: 2, Bytes: 4000, WasteByte: 4000,
			RetrievalSeqs: []int{0, 1},
		}},
		Estimator: model.TokenEstimator{BytesPerToken: 3.6, Calibrated: true},
	}
	r := (RepeatedRetrieval{}).Estimate(ctxFor(s))

	var carry float64
	for _, f := range r.Derived {
		if f.Label == "carry cost of the redundant copies" {
			carry = f.Quantity.Value
		}
	}
	if carry <= 0 {
		t.Fatal("the carry of the second copy must be found even with no path to join on")
	}

	// And only the later copy counts: the first fetch would still happen.
	first := c0(s, 0)
	if carry >= first {
		t.Errorf("carry %.0f should be the later copy alone, not both", carry)
	}
}

// c0 is the carry cost of a session's first retrieval, for comparison.
func c0(s *model.Session, seq int) float64 {
	cache := analysis.Cache(s, analysis.TTL5m)
	for _, it := range analysis.Carry(s, cache).Items {
		if it.RetrievalSeq == seq {
			return it.CarryEIT
		}
	}
	return 0
}

func TestRepeatedRetrievalIsNotApplicableWithoutRepeats(t *testing.T) {
	s := &model.Session{
		Invocations: []model.ModelInvocation{inv(0, 0, 1, 0, 10_000)},
		Retrievals: []model.RetrievedContent{
			{Tool: "Read", Path: "a.go", Bytes: 500, Tokens: 140},
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

func TestEveryBuiltInCaveatFitsInTheTable(t *testing.T) {
	// The caveat is a column beside a number, in a table of eight rows. The
	// built-ins are held to the limit that external interventions are held
	// to, or the rule is advice rather than a contract.
	c := testContext(t)
	for _, i := range Builtin() {
		r := i.Estimate(c)
		if n := len([]rune(r.Caveat)); n > CaveatLimit {
			t.Errorf("%s: caveat is %d characters, over the %d limit:\n  %q",
				i.Name(), n, CaveatLimit, r.Caveat)
		}
		// A one-line caveat has to be a claim, not a fragment.
		if r.Caveat != "" && !strings.HasSuffix(r.Caveat, ".") {
			t.Errorf("%s: caveat should read as a sentence: %q", i.Name(), r.Caveat)
		}
		// And the argument behind it must not be lost, only moved.
		if r.Applicable && r.Headline != nil && r.CaveatDetail == "" && r.Caveat == "" {
			t.Errorf("%s: a headline with neither caveat nor detail", i.Name())
		}
	}
}

func TestAddressableTimesReductionIsTheEffect(t *testing.T) {
	// The identity the summary table is built on: what an intervention can
	// touch, times what it does to that, is what it does to the session. If
	// it does not hold, the three columns are three unrelated numbers and
	// the table invites arithmetic that does not work.
	c := testContext(t)
	c.Total = c.Carry.PromptCostEIT + c.Weights.OutputCost(c.Session.Usage())
	if c.Total <= 0 {
		t.Fatal("the fixture needs a cost to take shares of")
	}

	for _, i := range Builtin() {
		r := i.Estimate(c)
		if !r.Applicable || r.Headline == nil || r.Headline.Quantity == nil {
			continue
		}
		if r.Headline.Quantity.Unit == model.Ratio {
			continue // already a share; nothing to decompose
		}
		if r.Addressable == nil {
			t.Errorf("%s has an effect but does not say what it can act on", i.Name())
			continue
		}
		want := r.Headline.Quantity.Value / c.Total
		got := r.Addressable.Share * r.Reduction
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("%s: addressable %.6f x reduction %.6f = %.6f, but the effect is %.6f "+
				"of the session", i.Name(), r.Addressable.Share, r.Reduction, got, want)
		}
		// And the addressable part has to be a real slice of the session.
		if r.Addressable.Share <= 0 || r.Addressable.Share > 1.0001 {
			t.Errorf("%s: addressable share %.4f is not a share of anything",
				i.Name(), r.Addressable.Share)
		}
		if r.Addressable.Name == "" {
			t.Errorf("%s: the addressable part needs a name a reader can go and find",
				i.Name())
		}
	}
}

func TestReasonsFitInTheTableToo(t *testing.T) {
	// not_measurable shares the caveat's column, so it shares the caveat's
	// limit. "No task boundary here: this was one sitting." beats four lines
	// explaining what a task boundary is; the explanation goes in
	// caveat_detail, which the full report prints and the table does not.
	c := testContext(t)
	for _, i := range Builtin() {
		r := i.Estimate(c)
		if n := len([]rune(r.NotMeasurable)); n > CaveatLimit {
			t.Errorf("%s: not_measurable is %d characters, over the %d limit:\n  %q",
				i.Name(), n, CaveatLimit, r.NotMeasurable)
		}
		if r.NotMeasurable != "" && !strings.HasSuffix(r.NotMeasurable, ".") {
			t.Errorf("%s: it should read as a sentence: %q", i.Name(), r.NotMeasurable)
		}
	}
}
