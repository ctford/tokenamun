package ingest

import (
	"strings"
	"testing"

	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
)

func load(t *testing.T, path string) *model.Session {
	t.Helper()
	s, err := Load(model.SessionRef{ID: "s1", Transcript: path, Origin: model.FromLocal})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The central correctness rule: entries sharing a requestId are one API call.
func TestAssistantEntriesCollapseToOneInvocationPerRequest(t *testing.T) {
	s := load(t, "testdata/repeated-request.jsonl")

	if s.AssistantEntries != 5 {
		t.Fatalf("expected 5 assistant entries in the fixture, got %d", s.AssistantEntries)
	}
	if len(s.Invocations) != 3 {
		t.Fatalf("expected 3 API calls, got %d", len(s.Invocations))
	}
	if got := s.Invocations[0].Entries; got != 3 {
		t.Fatalf("expected the first call to collapse 3 entries, got %d", got)
	}
}

func TestUsageIsCountedOncePerCall(t *testing.T) {
	s := load(t, "testdata/repeated-request.jsonl")
	u := s.Usage()

	// req_1 contributes once despite appearing on three entries.
	wantRead := int64(8000 + 9000)
	if u.CacheRead != wantRead {
		t.Fatalf("cache read: got %d, want %d (naive summing would give %d)",
			u.CacheRead, wantRead, 8000*3+9000)
	}
	if u.CacheCreation != 1500 {
		t.Fatalf("cache creation: got %d, want 1500", u.CacheCreation)
	}
	if u.Output != 90 {
		t.Fatalf("output: got %d, want 90", u.Output)
	}
	if u.Thinking != 20 {
		t.Fatalf("thinking: got %d, want 20", u.Thinking)
	}
}

// Guards the invariant from AGENTS.md: deduplicated totals never exceed naive ones.
func TestDeduplicatedTotalNeverExceedsNaiveTotal(t *testing.T) {
	s := load(t, "testdata/repeated-request.jsonl")
	var naive int64
	for _, inv := range s.Invocations {
		naive += inv.Usage.PromptTokens() * int64(inv.Entries)
	}
	if s.Usage().PromptTokens() > naive {
		t.Fatal("deduplicated total exceeded the naive total, which is impossible")
	}
}

func TestToolCallsArePairedWithResults(t *testing.T) {
	s := load(t, "testdata/repeated-request.jsonl")
	if len(s.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(s.ToolCalls))
	}
	bash := s.ToolCalls[0]
	if bash.Name != "Bash" || !bash.Resolved || bash.ResultBytes != 10 {
		t.Fatalf("Bash call not paired with its observed result: %+v", bash)
	}
	if s.ToolCalls[1].Resolved {
		t.Fatal("the Agent call has no result in the fixture and must not be marked resolved")
	}
}

func TestDataProblemsAreWarnedAboutRatherThanHidden(t *testing.T) {
	s := load(t, "testdata/repeated-request.jsonl")
	want := []string{"entries_collapsed", "zero_prompt_calls", "subagent_usage_missing", "unresolved_tool_calls"}
	for _, code := range want {
		if !hasWarning(s, code) {
			t.Errorf("expected warning %q, got %v", code, s.Warnings)
		}
	}
}

func TestPartialFinalLineIsToleratedNotFatal(t *testing.T) {
	// A transcript being appended to while we read it ends mid-line. That is
	// the normal case for profiling a live session, not an error.
	complete := `{"type":"assistant","requestId":"req_1","message":{"id":"m1","model":"claude-opus-5","content":[],"usage":{"input_tokens":1,"cache_read_input_tokens":10,"output_tokens":2}}}`
	partial := `{"type":"assistant","requestId":"req_2","message":{"id":"m2","mod`
	s, err := Parse(strings.NewReader(complete+"\n"+partial), model.SessionRef{Current: true})
	if err != nil {
		t.Fatalf("a partial final line must not fail the parse: %v", err)
	}
	if len(s.Invocations) != 1 {
		t.Fatalf("expected the complete call to be kept, got %d", len(s.Invocations))
	}
	if !hasWarning(s, "partial_final_line") {
		t.Fatalf("expected a partial_final_line warning, got %v", s.Warnings)
	}
}

func TestUnkeyedAssistantEntryIsDroppedNotDoubleCounted(t *testing.T) {
	// Without requestId or message.id we cannot tell a repeat from a new call,
	// so counting it risks inflating usage. Dropping it is the safe error.
	line := `{"type":"assistant","message":{"role":"assistant","model":"claude-opus-5","content":[],"usage":{"input_tokens":5,"cache_read_input_tokens":100}}}`
	s, err := Parse(strings.NewReader(line), model.SessionRef{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Invocations) != 0 || s.Usage().PromptTokens() != 0 {
		t.Fatal("an unkeyed entry must not contribute usage")
	}
	if !hasWarning(s, "unkeyed_assistant_entry") {
		t.Fatalf("expected an unkeyed_assistant_entry warning, got %v", s.Warnings)
	}
}

func TestCostRanksCacheReadsBelowWrites(t *testing.T) {
	// End to end: the fixture's cheap cache reads must not dominate cost the
	// way they dominate volume.
	s := load(t, "testdata/repeated-request.jsonl")
	u := s.Usage()
	w := cost.For(s.Models()[0])
	if u.CacheRead <= u.CacheCreation {
		t.Skip("fixture no longer read-heavy")
	}
	if w.PromptCost(u) >= float64(u.PromptTokens()) {
		t.Fatal("effective cost should be well below raw prompt volume")
	}
}

func hasWarning(s *model.Session, code string) bool {
	for _, w := range s.Warnings {
		if w.Code == code {
			return true
		}
	}
	return false
}
