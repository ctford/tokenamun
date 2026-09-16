package claudecode

import (
	"encoding/json"
	"testing"
)

func TestContentDecodesStringAndBlockForms(t *testing.T) {
	// Both shapes occur in real transcripts: user prompts arrive as bare
	// strings, tool results as block lists.
	var asString Content
	if err := json.Unmarshal([]byte(`"just text"`), &asString); err != nil {
		t.Fatal(err)
	}
	if asString.Text != "just text" || len(asString.Blocks) != 0 {
		t.Fatalf("string form decoded wrong: %+v", asString)
	}

	var asBlocks Content
	raw := `[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]`
	if err := json.Unmarshal([]byte(raw), &asBlocks); err != nil {
		t.Fatal(err)
	}
	if len(asBlocks.Blocks) != 1 || asBlocks.Blocks[0].Name != "Bash" {
		t.Fatalf("block form decoded wrong: %+v", asBlocks)
	}

	var null Content
	if err := json.Unmarshal([]byte(`null`), &null); err != nil {
		t.Fatal(err)
	}
	if null.Len() != 0 {
		t.Fatal("null content should be empty, not an error")
	}
}

func TestContentLenCountsNestedResultPayloads(t *testing.T) {
	var c Content
	raw := `[{"type":"tool_result","tool_use_id":"t","content":"0123456789"}]`
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	if got := c.Len(); got != 10 {
		t.Fatalf("expected 10 bytes of observed result content, got %d", got)
	}
}

func TestTimeIsZeroRatherThanEpochWhenUnknown(t *testing.T) {
	// An unknown timestamp must not become 1970, which would make every gap
	// calculation nonsense.
	if !(Entry{}).Time().IsZero() {
		t.Fatal("missing timestamp should be the zero time")
	}
	if !(Entry{Timestamp: "not a time"}).Time().IsZero() {
		t.Fatal("malformed timestamp should be the zero time")
	}
	e := Entry{Timestamp: "2026-08-27T10:48:15.903659Z"}
	if e.Time().IsZero() {
		t.Fatal("valid RFC3339 timestamp should parse")
	}
}
