package whatif

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ctford/tokenamun/internal/model"
)

// replayFixture writes a transcript with one eligible shell result, one shell
// result that read a file (not eligible), and one file read (not eligible).
func replayFixture(t *testing.T) model.SessionRef {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "full.jsonl")
	lines := `{"type":"assistant","requestId":"r1","message":{"id":"m1","model":"claude-opus-5","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./..."}}],"usage":{"input_tokens":1,"cache_creation_input_tokens":100,"cache_read_input_tokens":0,"output_tokens":5}}}
{"type":"user","toolUseID":"t1","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"AAAAAAAAAA"}]},"toolUseResult":{"stdout":"AAAAAAAAAA","stderr":""}}
{"type":"assistant","requestId":"r2","message":{"id":"m2","model":"claude-opus-5","content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"cat internal/pay/charge.go"}}],"usage":{"input_tokens":1,"cache_creation_input_tokens":100,"cache_read_input_tokens":0,"output_tokens":5}}}
{"type":"user","toolUseID":"t2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"BBBBBBBBBB"}]},"toolUseResult":{"stdout":"BBBBBBBBBB","stderr":""}}
{"type":"assistant","requestId":"r3","message":{"id":"m3","model":"claude-opus-5","content":[{"type":"tool_use","id":"t3","name":"Read","input":{"file_path":"a.go"}}],"usage":{"input_tokens":1,"cache_creation_input_tokens":100,"cache_read_input_tokens":0,"output_tokens":5}}}
{"type":"user","toolUseID":"t3","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t3","content":"CCCCCCCCCC"}]},"toolUseResult":{"type":"text","file":{"filePath":"a.go","content":"CCCCCCCCCC","startLine":1,"numLines":1,"totalLines":1}}}
`
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return model.SessionRef{ID: "replay", Transcript: path, Origin: model.FromEntire}
}

func TestReplayMeasuresRealCompressionOfEligibleContentOnly(t *testing.T) {
	ref := replayFixture(t)

	// head -c 4 is a stand-in for a compressor: deterministic and lossy.
	r, err := Replay(ref, "head -c 4")
	if err != nil {
		t.Fatal(err)
	}
	// Only the `go test` output is eligible: the `cat` result classifies as
	// source, and the Read is a file.
	if r.Items != 1 {
		t.Fatalf("replayed %d items, want 1 (shell output that is not a file read)", r.Items)
	}
	if r.InputBytes != 10 || r.OutputBytes != 4 {
		t.Fatalf("input=%d output=%d, want 10 and 4", r.InputBytes, r.OutputBytes)
	}
	if r.Ratio() != 0.4 {
		t.Errorf("ratio = %v, want 0.4", r.Ratio())
	}
}

func TestReplayIsIdentityWhenTheCommandIsIdentity(t *testing.T) {
	r, err := Replay(replayFixture(t), "cat")
	if err != nil {
		t.Fatal(err)
	}
	if r.Ratio() != 1 {
		t.Errorf("ratio = %v, want 1 for a pass-through", r.Ratio())
	}
}

func TestReplayCountsFailuresRatherThanHidingThem(t *testing.T) {
	// A compressor that cannot handle some input is a finding about the
	// compressor. Swallowing it would flatter the result.
	_, err := Replay(replayFixture(t), "exit 1")
	if err == nil {
		t.Fatal("a command that fails on everything should be an error, not a silent ratio of 1")
	}
}

func TestReplayRequiresACommand(t *testing.T) {
	if _, err := Replay(replayFixture(t), "   "); err == nil {
		t.Fatal("an empty command should be rejected")
	}
}

func TestReplayOnAMissingTranscriptFails(t *testing.T) {
	if _, err := Replay(model.SessionRef{Transcript: "/nonexistent/full.jsonl"}, "cat"); err == nil {
		t.Fatal("a missing transcript should be an error")
	}
}
