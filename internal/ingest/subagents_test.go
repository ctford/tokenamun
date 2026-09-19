package ingest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
)

// sessionWithSubagents lays out the on-disk shape Claude Code writes for a
// session that called Agent, and returns the parent transcript's path.
//
//	<project>/<id>.jsonl
//	<project>/<id>/subagents/agent-*.jsonl
func sessionWithSubagents(t *testing.T, parent string, subagents ...string) string {
	t.Helper()
	project := t.TempDir()
	path := filepath.Join(project, "s1.jsonl")
	if err := os.WriteFile(path, []byte(parent), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(project, "s1", "subagents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, body := range subagents {
		name := fmt.Sprintf("agent-%d.jsonl", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// call renders one assistant entry. Sharing a requestId across two of them is
// how a transcript records one API call, which is the thing that must not be
// counted twice.
func call(requestID, model string, cacheRead, output int) string {
	return fmt.Sprintf(
		`{"type":"assistant","requestId":%q,"cwd":"/work","message":{"id":"m-%s","model":%q,`+
			`"usage":{"input_tokens":0,"cache_read_input_tokens":%d,"output_tokens":%d}}}`+"\n",
		requestID, requestID, model, cacheRead, output)
}

func loadPath(t *testing.T, path string) *model.Session {
	t.Helper()
	s, err := Load(model.SessionRef{ID: "s1", Transcript: path, Origin: model.FromLocal})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSubagentTranscriptsAreLoadedFromDisk(t *testing.T) {
	// The bug this fixes: the spend was on disk in a sibling file and the
	// tool reported the session without it, as an observed figure.
	path := sessionWithSubagents(t,
		call("req_1", "claude-opus-5", 1000, 10),
		call("sub_a", "claude-opus-5", 5000, 20),
		call("sub_b", "claude-opus-5", 7000, 30),
	)

	s := loadPath(t, path)
	if len(s.Subagents) != 2 {
		t.Fatalf("both subagent transcripts should be found, got %d", len(s.Subagents))
	}
	if got := s.SubagentUsage().CacheRead; got != 12000 {
		t.Errorf("subagent cache reads should total 12000, got %d", got)
	}
}

func TestSubagentSpendStaysOutOfTheSessionsOwnTotals(t *testing.T) {
	// A subagent runs in its own context. Adding its cache reads to the
	// parent's would describe a prompt that never existed, and would feed
	// another context's cold start to the estimator as growth in this one.
	path := sessionWithSubagents(t,
		call("req_1", "claude-opus-5", 1000, 10),
		call("sub_a", "claude-opus-5", 5000, 20),
	)

	s := loadPath(t, path)
	if got := s.Usage().CacheRead; got != 1000 {
		t.Errorf("the session's own usage must exclude subagents, got %d", got)
	}
	if len(s.Invocations) != 1 {
		t.Errorf("subagent calls must not be appended to the session's, got %d", len(s.Invocations))
	}
}

func TestSubagentEntriesAreDeduplicatedByRequestID(t *testing.T) {
	// A subagent transcript repeats its usage object across the entries of
	// one call, exactly as a session transcript does. Summing per entry
	// overstated one real subagent by 3.3x.
	path := sessionWithSubagents(t,
		call("req_1", "claude-opus-5", 1000, 10),
		call("sub_a", "claude-opus-5", 5000, 20)+
			call("sub_a", "claude-opus-5", 5000, 20)+
			call("sub_a", "claude-opus-5", 5000, 20),
	)

	s := loadPath(t, path)
	if got := len(s.Subagents[0].Invocations); got != 1 {
		t.Fatalf("three entries of one request are one call, got %d", got)
	}
	if got := s.SubagentUsage().CacheRead; got != 5000 {
		t.Errorf("naive summing would give 15000, got %d", got)
	}
}

func TestSubagentsArePricedAtTheirOwnModel(t *testing.T) {
	// A subagent can run on a different model from its parent, and the
	// cache-read rate differs fourfold between them. Pricing the set at the
	// parent's weights is the mistake cost.PerCall exists to avoid.
	path := sessionWithSubagents(t,
		call("req_1", "claude-opus-5", 1000, 0),
		call("sub_a", "claude-fable-5-1", 100000, 0),
	)

	s := loadPath(t, path)
	prompt, _ := cost.SessionCost(s.SubagentInvocations())
	// fable-5-1 reads at 0.025x, not the 0.1x its parent reads at.
	if want := 2500.0; prompt != want {
		t.Errorf("subagent priced at its own rate should cost %.0f EIT, got %.0f", want, prompt)
	}
}

func TestSubagentWarningFiresOnlyWhenTheSpendIsReallyGone(t *testing.T) {
	// The old warning said subagent usage "is not in this transcript",
	// which was true and read as unmeasurable. It should fire when the
	// transcripts are absent -- cleanup deletes them too -- and stay quiet
	// when they are there to be read.
	agentCall := `{"type":"assistant","requestId":"req_1","cwd":"/work","message":{"id":"m1",` +
		`"model":"claude-opus-5","usage":{"input_tokens":10,"output_tokens":1},` +
		`"content":[{"type":"tool_use","id":"t1","name":"Agent","input":{}}]}}` + "\n"

	withSub := sessionWithSubagents(t, agentCall, call("sub_a", "claude-opus-5", 5000, 20))
	if warned(loadPath(t, withSub), "subagent_usage_missing") {
		t.Error("the spend was found on disk, so nothing is missing")
	}

	withoutSub := sessionWithSubagents(t, agentCall)
	if !warned(loadPath(t, withoutSub), "subagent_usage_missing") {
		t.Error("an Agent call with no transcript anywhere is spend this tool cannot see")
	}
}

func TestSessionWithNoSubagentsReportsNone(t *testing.T) {
	// The common case by far, and the one that must stay cheap and silent.
	path := sessionWithSubagents(t, call("req_1", "claude-opus-5", 1000, 10))

	if s := loadPath(t, path); len(s.Subagents) != 0 {
		t.Errorf("no subagent directory means no subagents, got %d", len(s.Subagents))
	}
}

func warned(s *model.Session, code string) bool {
	for _, w := range s.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}
