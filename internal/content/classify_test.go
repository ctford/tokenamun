package content

import (
	"testing"

	"github.com/ctford/tokenamun/internal/model"
)

func TestClassifyPrecedence(t *testing.T) {
	// Every case here exists because a more general rule would otherwise win.
	cases := []struct {
		path string
		want model.Category
	}{
		{"docs/adr/0001-use-go.md", model.CatADR},
		{"docs/adrs/ADR-0042.md", model.CatADR},
		{"architecture-decisions/db.md", model.CatADR},
		{"specs/booking.md", model.CatSpecification},
		{"SPEC.md", model.CatSpecification},
		{"contracts/openapi.yaml", model.CatSpecification},
		{"docs/plans/journey-plan.md", model.CatPlan},
		{"internal/ingest/ingest_test.go", model.CatTest},
		{"test/fixtures/thing.go", model.CatTest},
		{"src/app.test.ts", model.CatTest},
		{"CLAUDE.md", model.CatInstructions},
		{"AGENTS.md", model.CatInstructions},
		{".claude/settings.json", model.CatInstructions},
		{"README.md", model.CatInstructions},
		{"docs/architecture.md", model.CatDocumentation},
		{"notes.md", model.CatDocumentation},
		{"internal/cost/cost.go", model.CatSourceCode},
		{"src/payment.ts", model.CatSourceCode},
		{"Makefile", model.CatOther},
	}
	for _, c := range cases {
		got, _ := Classify(c.path)
		if got != c.want {
			t.Errorf("Classify(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestClassifyReportsWhenItCouldNotAttribute(t *testing.T) {
	if _, ok := Classify(""); ok {
		t.Error("an empty path must not claim a confident classification")
	}
	if _, ok := Classify("Makefile"); ok {
		t.Error("an unmatched path must report that it was not classified")
	}
	if _, ok := Classify("main.go"); !ok {
		t.Error("a matched path must report success")
	}
}

func TestMCPOutputIsCategorisedByTool(t *testing.T) {
	if got := ClassifyTool("mcp__github__list_issues"); got != model.CatMCPOutput {
		t.Errorf("got %q, want mcp output", got)
	}
	if got := ClassifyTool("Bash"); got != model.CatToolOutput {
		t.Errorf("got %q, want tool output", got)
	}
}

func TestPathsFromReadingCommands(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"cat internal/cost/cost.go", []string{"internal/cost/cost.go"}},
		{"sed -n '1,40p' docs/plan.md", []string{"docs/plan.md"}},
		{"head -20 README.md", []string{"README.md"}},
		{"rg --json pattern src/app.ts", []string{"src/app.ts"}},
		{"cat a.go b.go", []string{"a.go", "b.go"}},
		// Not a read: the output is a build log, and tail's argument here is
		// a line count rather than a file.
		{"make build | tail -5", nil},
		{"go test ./...", nil},
		{"git commit -m 'thing.go'", nil},
		{"", nil},
	}
	for _, c := range cases {
		got := PathsFromCommand(c.cmd)
		if len(got) != len(c.want) {
			t.Errorf("PathsFromCommand(%q) = %v, want %v", c.cmd, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("PathsFromCommand(%q) = %v, want %v", c.cmd, got, c.want)
				break
			}
		}
	}
}

func TestOnlyTheFirstPipelineStageIsTreatedAsTheSource(t *testing.T) {
	// In `cat x.go | grep y` the content comes from x.go. Counting grep's
	// pattern or a downstream file would double-attribute.
	got := PathsFromCommand("cat internal/model/session.go | grep -n Usage")
	if len(got) != 1 || got[0] != "internal/model/session.go" {
		t.Fatalf("got %v, want just the source file", got)
	}
}
