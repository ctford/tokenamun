package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ctford/tokenamun/internal/ingest"
	"github.com/ctford/tokenamun/internal/model"
)

var update = flag.Bool("update", false, "regenerate golden files")

// Integration: the whole pipeline from transcript bytes to rendered output,
// with no Entire installation, no git and no network.
func TestProfileGoldenOutput(t *testing.T) {
	s, err := ingest.Load(model.SessionRef{
		ID:         "fixture-session",
		Transcript: filepath.Join("..", "ingest", "testdata", "repeated-request.jsonl"),
		Origin:     model.FromLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	p := BuildProfile(s)

	var text bytes.Buffer
	if err := RenderText(&text, p); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "profile.txt", text.Bytes())

	pretty, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "profile.json", append(pretty, '\n'))
}

// Every quantity the JSON contract exposes must carry a provenance label and a
// unit. This is the check that stops an unlabelled number reaching an agent.
func TestEveryReportedQuantityIsLabelled(t *testing.T) {
	s, err := ingest.Load(model.SessionRef{
		Transcript: filepath.Join("..", "ingest", "testdata", "repeated-request.jsonl"),
	})
	if err != nil {
		t.Fatal(err)
	}
	p := BuildProfile(s)

	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	walkQuantities(t, "", tree)
}

// walkQuantities finds every object with a "value" key and asserts it is a
// well-formed Quantity.
func walkQuantities(t *testing.T, path string, v any) {
	t.Helper()
	switch n := v.(type) {
	case map[string]any:
		if _, isQuantity := n["value"]; isQuantity {
			q := model.Quantity{}
			if p, ok := n["provenance"].(string); ok {
				q.Prov = model.Provenance(p)
			}
			if u, ok := n["unit"].(string); ok {
				q.Unit = model.Unit(u)
			}
			if val, ok := n["value"].(float64); ok {
				q.Value = val
			}
			if err := q.Validate(); err != nil {
				t.Errorf("%s: %v", path, err)
			}
			return
		}
		for k, child := range n {
			walkQuantities(t, path+"."+k, child)
		}
	case []any:
		for i, child := range n {
			walkQuantities(t, path+"[]", child)
			_ = i
		}
	}
}

func TestVolumeAndCostAreDistinctQuantities(t *testing.T) {
	// The invariant from AGENTS.md rule 3 and 4: these are different units and
	// cost must never be reported as tokens.
	s, _ := ingest.Load(model.SessionRef{
		Transcript: filepath.Join("..", "ingest", "testdata", "repeated-request.jsonl"),
	})
	p := BuildProfile(s)
	if p.Usage.PromptVolume.Unit != model.Tokens {
		t.Error("prompt volume must be in tokens")
	}
	if p.Usage.PromptCost.Unit != model.EIT {
		t.Error("prompt cost must be in EIT, not tokens")
	}
	if p.Usage.PromptCost.Value >= p.Usage.PromptVolume.Value {
		t.Error("a cache-heavy session's cost must be below its raw volume")
	}
}

func TestTextRendererRefusesUnlabelledQuantities(t *testing.T) {
	var b strings.Builder
	line(&b, "  Bare", model.Quantity{Value: 42})
	if !strings.Contains(b.String(), "unlabelled") {
		t.Fatalf("an unlabelled quantity must not render as a number, got %q", b.String())
	}
}

func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s (run: go test ./internal/report -update)", path)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from golden file.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
