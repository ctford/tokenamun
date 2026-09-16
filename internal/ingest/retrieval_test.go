package ingest

import (
	"testing"

	"github.com/ctford/tokenamun/internal/model"
)

func retrievalFixture(t *testing.T) *model.Session {
	t.Helper()
	s, err := Load(model.SessionRef{ID: "s2", Transcript: "testdata/retrieval.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func find(t *testing.T, s *model.Session, toolID string) model.RetrievedContent {
	t.Helper()
	for _, r := range s.Retrievals {
		if r.ToolID == toolID {
			return r
		}
	}
	t.Fatalf("no retrieval for tool %s", toolID)
	return model.RetrievedContent{}
}

// The rule that decides every size in the tool: measure the tool_result block
// the model received, not the transcript's richer toolUseResult.
func TestSizeComesFromWhatEnteredContextNotFromToolUseResult(t *testing.T) {
	s := retrievalFixture(t)
	spilled := find(t, s, "t2")

	if spilled.Bytes != 900 {
		t.Fatalf("expected the 900 bytes the model saw, got %d", spilled.Bytes)
	}
	if !spilled.Truncated {
		t.Error("output spilled to a file must be marked truncated")
	}
	// The 40KB the harness kept out of context is a saving, reported as such.
	if spilled.WithheldBytes != 39100 {
		t.Errorf("withheld = %d, want 39100", spilled.WithheldBytes)
	}
}

func TestPartialReadIsCountedAsPartial(t *testing.T) {
	// The spec's rule: do not count a whole file when only a range came back.
	s := retrievalFixture(t)
	r := find(t, s, "t1")

	if !r.Partial {
		t.Error("a 20-line read of a 500-line file is partial")
	}
	if r.StartLine != 10 || r.Lines != 20 || r.TotalLines != 500 {
		t.Errorf("line range wrong: %+v", r)
	}
	if r.Category != model.CatSourceCode {
		t.Errorf("category = %q, want source code", r.Category)
	}
	if r.CategoryProv != model.Derived {
		t.Errorf("a path reported by the tool is observed, so classification is derived; got %q", r.CategoryProv)
	}
	full := find(t, s, "t7")
	if full.Partial {
		t.Error("a read of all 140 of 140 lines is not partial")
	}
	if full.Category != model.CatTest {
		t.Errorf("category = %q, want tests", full.Category)
	}
}

func TestShellReadsAreAttributedButLabelledInferred(t *testing.T) {
	// Bash is the majority of tool calls in auto mode, so attributing its
	// output matters -- but the path is a guess and must say so.
	s := retrievalFixture(t)
	adr := find(t, s, "t3")

	if adr.Path != "docs/adr/0007-retry-policy.md" {
		t.Errorf("path = %q, want the file named in the command", adr.Path)
	}
	if adr.Category != model.CatADR {
		t.Errorf("category = %q, want adrs", adr.Category)
	}
	if adr.CategoryProv != model.Inferred {
		t.Errorf("a path parsed from a command line is inferred, got %q", adr.CategoryProv)
	}
}

func TestUnattributableOutputIsNotGivenASpeculativeCategory(t *testing.T) {
	s := retrievalFixture(t)
	spilled := find(t, s, "t2")
	if spilled.Category != model.CatToolOutput {
		t.Errorf("`go test` output is tool output, not a file category; got %q", spilled.Category)
	}
	if spilled.Path != "" {
		t.Errorf("no path should be claimed, got %q", spilled.Path)
	}
}

func TestMCPResultsAreCategorisedSeparately(t *testing.T) {
	s := retrievalFixture(t)
	if got := find(t, s, "t4").Category; got != model.CatMCPOutput {
		t.Errorf("category = %q, want mcp output", got)
	}
}

func TestRepeatedRetrievalIsDetectedByContentHash(t *testing.T) {
	s := retrievalFixture(t)
	if len(s.Repeats) != 1 {
		t.Fatalf("expected exactly one repeated payload, got %d: %+v", len(s.Repeats), s.Repeats)
	}
	rep := s.Repeats[0]
	if rep.Count != 2 {
		t.Errorf("count = %d, want 2", rep.Count)
	}
	// One of the two fetches was redundant, so the redundant bytes are one copy.
	if rep.WasteByte != rep.Bytes {
		t.Errorf("redundant = %d, want one copy (%d)", rep.WasteByte, rep.Bytes)
	}
	if len(rep.Seqs) != 2 {
		t.Errorf("expected the invocations of both fetches, got %v", rep.Seqs)
	}
}

func TestRedundantBytesNeverExceedRetrievedBytes(t *testing.T) {
	// Accounting invariant: you cannot waste more than you fetched.
	s := retrievalFixture(t)
	var total, redundant int
	for _, r := range s.Retrievals {
		total += r.Bytes
	}
	for _, rep := range s.Repeats {
		redundant += rep.WasteByte
	}
	if redundant > total {
		t.Fatalf("redundant %d exceeds retrieved %d", redundant, total)
	}
}

func TestTokenCountsAreEstimatesAndSaySo(t *testing.T) {
	s := retrievalFixture(t)
	for _, r := range s.Retrievals {
		if r.TokensProv != model.DerivedApprox {
			t.Fatalf("token counts must be labelled derived-approx, got %q", r.TokensProv)
		}
		if r.Tokens <= 0 {
			t.Fatalf("retrieval %d has no token estimate", r.Seq)
		}
	}
	if s.Estimator.Method == "" {
		t.Error("the estimator must describe its method")
	}
}

func TestEstimatedTokensAreNeverMoreThanBytes(t *testing.T) {
	s := retrievalFixture(t)
	for _, r := range s.Retrievals {
		if r.Tokens > float64(r.Bytes) {
			t.Fatalf("retrieval %d estimated %.0f tokens from %d bytes", r.Seq, r.Tokens, r.Bytes)
		}
	}
}
