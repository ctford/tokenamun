package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ctford/tokenamun/internal/model"
)

func TestProfileReportsSubagentSpendBesideTheSessionsOwn(t *testing.T) {
	// The reader needs both figures and the sum: what this context cost,
	// what happened out of sight of every other figure in the report, and
	// what the work therefore cost altogether.
	s := &model.Session{
		Ref: model.SessionRef{ID: "s1", Origin: model.FromLocal},
		Invocations: []model.ModelInvocation{{
			Seq: 1, RequestID: "r1", Model: "claude-opus-5",
			Usage: model.TokenUsage{CacheRead: 10000, Output: 100},
		}},
		Subagents: []model.SubagentRun{{
			ID: "agent-a",
			Invocations: []model.ModelInvocation{{
				Seq: 1, RequestID: "sa", Model: "claude-opus-5",
				Usage: model.TokenUsage{CacheRead: 20000, Output: 200},
			}},
		}},
	}

	p := BuildProfile(s)
	if p.Subagents == nil {
		t.Fatal("a session with subagents must report them")
	}
	// Session: 10000*0.1 + 100*5 = 1500. Subagent: 20000*0.1 + 200*5 = 3000.
	if got := p.Usage.TotalCost.Value; got != 1500 {
		t.Errorf("the session's own cost excludes subagents, got %.0f", got)
	}
	if got := p.Subagents.TotalCost.Value; got != 3000 {
		t.Errorf("subagent cost: got %.0f, want 3000", got)
	}
	if got := p.Subagents.CombinedCost.Value; got != 4500 {
		t.Errorf("combined cost: got %.0f, want 4500", got)
	}
	if got := p.Subagents.ShareOfCombined.Value; got != 3000.0/4500.0 {
		t.Errorf("share out of sight: got %.4f, want %.4f", got, 3000.0/4500.0)
	}
}

func TestProfileOmitsTheSubagentBlockWhenThereAreNone(t *testing.T) {
	// Most sessions. An empty block would add a line to every report to
	// say nothing happened.
	s := &model.Session{
		Ref: model.SessionRef{ID: "s1", Origin: model.FromLocal},
		Invocations: []model.ModelInvocation{{
			Seq: 1, RequestID: "r1", Model: "claude-opus-5",
			Usage: model.TokenUsage{CacheRead: 10000, Output: 100},
		}},
	}
	if p := BuildProfile(s); p.Subagents != nil {
		t.Error("no subagents means no subagent block")
	}
}

// crossModelFanOut is the shape the combined-total signal exists for: a
// parent that never switched model, dispatching a subagent to a differently
// priced one. MixedPricing is false here and correctly so -- it is about
// this context -- which is why the combined figure needs its own signal
// rather than borrowing that one. Synthetic and hand-written, with round
// counts.
//
// claude-opus-5    reads at 0.1x
// claude-fable-5-1 reads at 0.025x
//
// parent   10,000x0.1   + 100x5 =   1,500 EIT
// subagent 20,000x0.025 + 200x5 =   1,500 EIT
func crossModelFanOut() *model.Session {
	return &model.Session{
		Ref: model.SessionRef{ID: "fan-out", Origin: model.FromLocal},
		Invocations: []model.ModelInvocation{{
			Seq: 1, RequestID: "r1", Model: "claude-opus-5", Entries: 1,
			Usage: model.TokenUsage{CacheRead: 10000, Output: 100},
		}},
		Subagents: []model.SubagentRun{{
			ID: "agent-a",
			Invocations: []model.ModelInvocation{{
				Seq: 1, RequestID: "sa", Model: "claude-fable-5-1", Entries: 1,
				Usage: model.TokenUsage{CacheRead: 20000, Output: 200},
			}},
		}},
	}
}

// TestCombinedCostSaysWhenItSpansTwoPricings is the correctness fix. The
// parent's own calls are all one model, so mixed_pricing is false and should
// stay false -- it is the claim "this context switched model", which this
// context did not. The combined total spans two pricings all the same, and
// before this it said so nowhere.
func TestCombinedCostSaysWhenItSpansTwoPricings(t *testing.T) {
	p := BuildProfile(crossModelFanOut())
	if p.Session.MixedPricing {
		t.Error("the parent never switched model, so its own figures are not mixed")
	}
	sa := p.Subagents
	if sa == nil {
		t.Fatal("a session with subagents must report them")
	}
	if !sa.CombinedMixedPricing {
		t.Error("combined_cost adds two pricings and does not say so")
	}
	if sa.CombinedCaveat == "" {
		t.Fatal("an unsound headline with no caveat on it")
	}
	if n := len([]rune(sa.CombinedCaveat)); n > CaveatLimit {
		t.Errorf("caveat is %d runes, over the %d cap it shares a line with a number at", n, CaveatLimit)
	}
	if !strings.Contains(sa.CombinedCaveat, "--prices") {
		t.Errorf("the caveat does not say where the sound number is: %q", sa.CombinedCaveat)
	}

	var b bytes.Buffer
	if err := RenderText(&b, p); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), sa.CombinedCaveat) {
		t.Error("the caveat is in the payload and not in what a reader sees")
	}
}

// TestOneModelThroughoutCarriesNoCaveat. The figure is exact whenever the
// parent and its subagents share a model, and a caveat on an exact number
// teaches a reader to skip caveats.
func TestOneModelThroughoutCarriesNoCaveat(t *testing.T) {
	s := crossModelFanOut()
	s.Subagents[0].Invocations[0].Model = "claude-opus-5"
	sa := BuildProfile(s).Subagents
	if sa.CombinedMixedPricing || sa.CombinedCaveat != "" {
		t.Errorf("one model throughout, yet qualified: %v %q",
			sa.CombinedMixedPricing, sa.CombinedCaveat)
	}
}
