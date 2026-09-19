package report

import (
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
