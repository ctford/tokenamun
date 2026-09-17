package whatif

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
)

// script writes an executable intervention and returns its path. Shell rather
// than Go, because the point of the interface is that an intervention does not
// have to be written in the tool's language.
func script(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// wellBehaved is a minimal intervention that satisfies the contract.
const wellBehaved = `
case "$1" in
describe) printf '{"name":"example","description":"an example"}' ;;
estimate)
  cat > /dev/null
  printf '{"applicable":true,"acts_on":"volume",'
  printf '"observed":[{"label":"calls","quantity":{"value":3,"unit":"calls","provenance":"observed"}}],'
  printf '"counterfactual":[{"label":"net","quantity":{"value":-100,"unit":"eit","provenance":"counterfactual"}}],'
  printf '"headline":{"label":"net","quantity":{"value":-100,"unit":"eit","provenance":"counterfactual"}},'
  printf '"caveat":"a ceiling, not an estimate",'
  printf '"unknown":["task_success: not observable."]}'
  ;;
esac
`

func testContext(t *testing.T) Context {
	t.Helper()
	s := &model.Session{
		Ref: model.SessionRef{ID: "plugin-fixture", Origin: model.FromLocal},
		Invocations: []model.ModelInvocation{{
			Seq: 0, Model: "claude-opus-5", Timestamp: time.Unix(0, 0),
			Usage: model.TokenUsage{Input: 10, CacheCreation: 1000, CacheCreation5m: 1000, Output: 50},
		}, {
			Seq: 1, Model: "claude-opus-5", Timestamp: time.Unix(60, 0),
			Usage: model.TokenUsage{CacheRead: 1000, Output: 40, Thinking: 10},
		}},
		Estimator: model.TokenEstimator{BytesPerToken: 3.6, Method: "fixed ratio"},
	}
	cache := analysis.Cache(s, analysis.TTL5m)
	return Context{
		Session: s, Cache: cache, Carry: analysis.Carry(s, cache),
		Weights: cost.Default, CompressionRatio: 0.5,
	}
}

func TestExternalInterventionIsIndistinguishableFromABuiltIn(t *testing.T) {
	path := script(t, "example", wellBehaved)
	e, err := LoadExternal(path)
	if err != nil {
		t.Fatal(err)
	}
	// It satisfies the same interface, which is what lets the report, the JSON
	// output and the treemap table treat it identically.
	var i Intervention = e
	if i.Name() != "example" || i.Describe() != "an example" {
		t.Fatalf("manifest not read: %q / %q", i.Name(), i.Describe())
	}

	r := i.Estimate(testContext(t))
	if !r.Applicable {
		t.Fatalf("expected an applicable result, got %+v", r)
	}
	if r.Intervention != "example" {
		t.Errorf("result should be named from the manifest, got %q", r.Intervention)
	}
	if r.Description != "an example" {
		t.Errorf("description should default to the manifest's, got %q", r.Description)
	}
	if r.Headline == nil || r.Headline.Quantity.Value != -100 {
		t.Errorf("headline did not survive the round trip: %+v", r.Headline)
	}
}

func TestExternalInterventionIsHeldToTheSameRulesAsABuiltIn(t *testing.T) {
	cases := []struct {
		name, body, wants string
	}{{
		name: "no-unknowns",
		body: `case "$1" in
describe) printf '{"name":"x","description":"d"}' ;;
estimate) cat > /dev/null; printf '{"applicable":true,"acts_on":"volume","unknown":[]}' ;;
esac`,
		wants: "no unknowns",
	}, {
		name: "headline-without-caveat",
		body: `case "$1" in
describe) printf '{"name":"x","description":"d"}' ;;
estimate) cat > /dev/null; printf '{"applicable":true,"acts_on":"volume","headline":{"label":"n","quantity":{"value":-1,"unit":"eit","provenance":"counterfactual"}},"unknown":["a"]}' ;;
esac`,
		wants: "no caveat",
	}, {
		name: "not-applicable-without-a-reason",
		body: `case "$1" in
describe) printf '{"name":"x","description":"d"}' ;;
estimate) cat > /dev/null; printf '{"applicable":false,"unknown":["a"]}' ;;
esac`,
		wants: "does not say why",
	}, {
		name: "counterfactual-labelled-observed",
		body: `case "$1" in
describe) printf '{"name":"x","description":"d"}' ;;
estimate) cat > /dev/null; printf '{"applicable":true,"acts_on":"volume","counterfactual":[{"label":"n","quantity":{"value":-1,"unit":"eit","provenance":"observed"}}],"unknown":["a"]}' ;;
esac`,
		wants: "labelled",
	}, {
		name: "renames-itself",
		body: `case "$1" in
describe) printf '{"name":"x","description":"d"}' ;;
estimate) cat > /dev/null; printf '{"intervention":"y","applicable":true,"acts_on":"volume","unknown":["a"]}' ;;
esac`,
		wants: "named itself",
	}, {
		name: "not-json",
		body: `case "$1" in
describe) printf '{"name":"x","description":"d"}' ;;
estimate) cat > /dev/null; printf 'sorry' ;;
esac`,
		wants: "not a result",
	}, {
		name: "exits-nonzero",
		body: `case "$1" in
describe) printf '{"name":"x","description":"d"}' ;;
estimate) exit 3 ;;
esac`,
		wants: "failed",
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, err := LoadExternal(script(t, "x", tc.body))
			if err != nil {
				t.Fatal(err)
			}
			r := e.Estimate(testContext(t))
			// A broken script becomes a row that says it is broken, not a
			// crash and not a silent zero.
			if r.Applicable {
				t.Fatalf("a rule-breaking result must not be reported as applicable: %+v", r)
			}
			if !strings.Contains(r.NotMeasurable, tc.wants) {
				t.Errorf("expected the failure to mention %q, got %q", tc.wants, r.NotMeasurable)
			}
			if len(r.Unknown) == 0 {
				t.Error("even a failure row must list what it cannot know")
			}
		})
	}
}

func TestExternalInterventionCannotShadowABuiltIn(t *testing.T) {
	body := `case "$1" in
describe) printf '{"name":"cache-ttl","description":"mine"}' ;;
esac`
	if _, err := LoadExternal(script(t, "cache-ttl", body)); err == nil {
		t.Fatal("a script must not be able to take a built-in's name")
	} else if !strings.Contains(err.Error(), "built-in") {
		t.Errorf("the error should say why: %v", err)
	}
}

func TestExternalInterventionThatHangsIsKilled(t *testing.T) {
	body := `case "$1" in
describe) printf '{"name":"slow","description":"d"}' ;;
estimate) sleep 30 ;;
esac`
	e, err := LoadExternal(script(t, "slow", body))
	if err != nil {
		t.Fatal(err)
	}
	e.Timeout = 200 * time.Millisecond
	start := time.Now()
	r := e.Estimate(testContext(t))
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("a hung intervention wedged the report for %s", elapsed)
	}
	if !strings.Contains(r.NotMeasurable, "did not finish") {
		t.Errorf("expected a timeout to be reported, got %q", r.NotMeasurable)
	}
}

func TestEvidenceGivesAScriptWhatABuiltInSees(t *testing.T) {
	// The interface's central promise: an extension is not handed a summary.
	// Asserted on the wire format, because that is what an author writes
	// against, and a field quietly dropped here breaks scripts silently.
	raw, err := json.Marshal(evidenceFor(testContext(t)))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"schema_version", "session", "cache", "carry", "weights", "compression_ratio",
	} {
		if _, ok := doc[key]; !ok {
			t.Errorf("the evidence document is missing %q", key)
		}
	}
	if doc["schema_version"].(float64) != EvidenceVersion {
		t.Error("the evidence must declare its version so a script can refuse a newer one")
	}

	// The parts a script actually reaches for, spelled out: these paths are
	// in the example intervention and in the docs.
	session := doc["session"].(map[string]any)
	for _, key := range []string{"invocations", "retrievals", "prompt_entries", "token_estimator"} {
		if _, ok := session[key]; !ok {
			t.Errorf("session is missing %q", key)
		}
	}
	inv := session["invocations"].([]any)[0].(map[string]any)
	if _, ok := inv["usage"]; !ok {
		t.Error("an invocation must carry its usage, or nothing can be priced")
	}
	carry := doc["carry"].(map[string]any)
	for _, key := range []string{"prompt_cost_eit", "preamble_tokens", "items"} {
		if _, ok := carry[key]; !ok {
			t.Errorf("carry is missing %q", key)
		}
	}
	weights := doc["weights"].(map[string]any)
	for _, key := range []string{"input", "cache_read", "output"} {
		if _, ok := weights[key]; !ok {
			t.Errorf("weights is missing %q, so a script would have to hardcode prices", key)
		}
	}
}

func TestDiscoverReadsTheSearchPathAndReportsBrokenScripts(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("aaa", strings.Replace(wellBehaved, `"name":"example"`, `"name":"aaa"`, 1), 0o755)
	write("bbb", strings.Replace(wellBehaved, `"name":"example"`, `"name":"bbb"`, 1), 0o755)
	write("broken", `case "$1" in describe) printf 'nope' ;; esac`, 0o755)
	// Not executable, so not an intervention.
	write("notes.md", "", 0o644)
	// Hidden, so not an intervention either: editors leave files like this.
	write(".aaa.swp", "", 0o755)

	t.Setenv(InterventionDirEnv, dir)
	found, errs := Discover(nil)

	var names []string
	for _, i := range found {
		names = append(names, i.Name())
	}
	// Sorted, so two runs of a report agree.
	if got := strings.Join(names, ","); got != "aaa,bbb" {
		t.Errorf("discovered %q, want aaa,bbb", got)
	}
	if len(errs) != 1 {
		t.Fatalf("the broken script should be reported once, got %v", errs)
	}
	if !strings.Contains(errs[0].Error(), "describe itself") {
		t.Errorf("the error should name the problem: %v", errs[0])
	}
}

func TestSearchPathExcludesTheRepositoryBeingAnalysed(t *testing.T) {
	// Tokenamun is routinely pointed at a checkout someone else wrote. If it
	// executed scripts it found there, asking a question about a repository
	// would run that repository's code.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range SearchPath() {
		if strings.HasPrefix(dir, wd) || dir == "." || strings.HasPrefix(dir, ".tokenamun") {
			t.Errorf("the search path must not include the working tree, found %q", dir)
		}
	}
}

func TestAnInterventionMustSayWhichFactorItMoves(t *testing.T) {
	// A box's cost is volume x round trips x price. Only a change in volume
	// reads as a discount on the rectangles the viewer draws; the other two
	// leave the picture the same shape and change what it cost. A result that
	// does not say which it is leaves the reader to guess.
	body := `case "$1" in
describe) printf '{"name":"x","description":"d"}' ;;
estimate) cat > /dev/null; printf '{"applicable":true,"unknown":["a"],"caveat":"c."}' ;;
esac`
	e, err := LoadExternal(script(t, "x", body))
	if err != nil {
		t.Fatal(err)
	}
	r := e.Estimate(testContext(t))
	if r.Applicable {
		t.Fatal("a result with no axis must not be reported as a finding")
	}
	if !strings.Contains(r.NotMeasurable, "acts_on") {
		t.Errorf("the failure should name the missing field: %q", r.NotMeasurable)
	}
}

func TestEveryBuiltInDeclaresWhatItMoves(t *testing.T) {
	c := testContext(t)
	for _, i := range Builtin() {
		r := i.Estimate(c)
		if !r.Applicable {
			continue
		}
		switch r.Acts {
		case AxisVolume, AxisRoundTrips, AxisPrice:
		default:
			t.Errorf("%s is applicable but does not say which factor it moves", i.Name())
		}
	}
}
