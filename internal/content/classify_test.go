package content

import (
	"strings"
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

// Decision records are named differently in every codebase. Getting this wrong
// reported "no architectural decisions were ever read" for a repository that
// reads them constantly.
func TestDecisionRecordsUnderAnyOfTheUsualNames(t *testing.T) {
	cases := []string{
		"docs/adr/0024-name-the-environments.md",
		"docs/adrs/ADR-0042.md",
		"docs/decisions/bafog-lunon--deploy-as-separate-services.md",
		"docs/decisions/README.md",
		"docs/decisions/rationale",
		"docs/decision-records/0001-thing.md",
		"docs/rfcs/0007-retry.md",
		"decisions/0002-queue-choice.md",
		"architecture-decisions/db.md",
	}
	for _, path := range cases {
		if got, _ := Classify(path); got != model.CatADR {
			t.Errorf("Classify(%q) = %q, want adrs", path, got)
		}
	}
}

func TestACodePackageCalledDecisionsIsNotArchitecture(t *testing.T) {
	// This repository has both docs/decisions/ (records) and
	// packages/decisions/ (code). Sweeping the latter in would inflate the
	// ADR category with source.
	for _, path := range []string{
		"packages/decisions/index.ts",
		"apps/decision-surface/main.go",
		"infra/decision-surface/main.tf",
	} {
		if got, _ := Classify(path); got == model.CatADR {
			t.Errorf("Classify(%q) = adrs; it is code", path)
		}
	}
}

func TestPathsFromCommandHandlesDirectoriesAndGlobs(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"cat docs/adr/0009-github-actions-for-ci.md", []string{"docs/adr/0009-github-actions-for-ci.md"}},
		{"cat docs/decisions/rationale/*.md", []string{"docs/decisions/rationale"}},
		{"cat docs/adr/*.md", []string{"docs/adr"}},
		// A listing is not content. `ls docs/decisions` tells you what is
		// there, so counting its output as decision-record content would
		// inflate the category with directory metadata.
		{"ls docs/decisions/rationale", nil},
		{"find docs -name '*.md'", nil},
		{"cat docs/decisions/", []string{"docs/decisions"}},
		// The first argument to a grep-family command is the pattern, and a
		// pattern containing a slash must not become a phantom file.
		{"rg 'foo/bar' docs/decisions", []string{"docs/decisions"}},
		{"grep -n handler internal/api/server.go", []string{"internal/api/server.go"}},
		// Still not a file read.
		{"go test ./...", nil},
		{"git status", nil},
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

// Compound commands are the dominant shape in agent sessions. Reading only
// the first stage found `cd` and attributed nothing for over a hundred
// kilobytes of genuinely attributable content.
func TestPathsFromCompoundCommands(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want []string
	}{
		{"cd then read",
			`cd /repo && cat docs/decisions/x.md`,
			[]string{"docs/decisions/x.md"}},
		{"echo separators around several reads",
			`cd /repo
echo "=== a ===" && cat docs/decisions/a.md
echo "=== b ===" && cat internal/pay/charge.go`,
			[]string{"docs/decisions/a.md", "internal/pay/charge.go"}},
		{"semicolons",
			`sed -n '1,20p' mise.toml; cat docs/adr/0009.md`,
			[]string{"mise.toml", "docs/adr/0009.md"}},
		{"redirection is a destination, not a read",
			`cat docs/decisions/a.md > /tmp/out.txt`,
			[]string{"docs/decisions/a.md"}},
		{"leading environment assignment",
			`FOO=1 cat internal/x.go`,
			[]string{"internal/x.go"}},
		{"duplicates collapse",
			`cat a.go && cat a.go`,
			[]string{"a.go"}},
		{"nothing readable",
			`cd /repo && go build ./... && git status`,
			nil},
		{"a later pipeline stage consumes, not reads",
			`cat internal/x.go | grep -n foo`,
			[]string{"internal/x.go"}},
	}
	for _, c := range cases {
		got := PathsFromCommand(c.cmd)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}

func TestDeclaredSubtreesBeatNamingHeuristics(t *testing.T) {
	cl, err := NewClassifier(Config{Categories: map[string][]string{
		"adrs":         {"docs/decisions"},
		"instructions": {".claude"},
	}}, "test")
	if err != nil {
		t.Fatal(err)
	}

	// Declared: exact, and reported as declared.
	cat, ok, declared := cl.Match("docs/decisions/bafog-lunon--deploy.md")
	if !ok || cat != model.CatADR || !declared {
		t.Errorf("declared subtree: got (%q, %v, %v)", cat, ok, declared)
	}
	// Absolute paths are the normal case in a transcript.
	cat, ok, declared = cl.Match("/Users/x/repo/docs/decisions/a.md")
	if !ok || cat != model.CatADR || !declared {
		t.Errorf("absolute path: got (%q, %v, %v)", cat, ok, declared)
	}
	// A declaration wins over the heuristic that would have said `plans`.
	cat, _, declared = cl.Match("/Users/x/.claude/plans/thing.md")
	if cat != model.CatInstructions || !declared {
		t.Errorf("declaration should beat the heuristic, got (%q, declared=%v)", cat, declared)
	}
	// Undeclared paths still fall back, and say they were not declared.
	cat, ok, declared = cl.Match("internal/pay/charge.go")
	if !ok || cat != model.CatSourceCode || declared {
		t.Errorf("fallback: got (%q, %v, %v)", cat, ok, declared)
	}
}

func TestSubtreeMatchingRespectsSegmentBoundaries(t *testing.T) {
	cl, _ := NewClassifier(Config{Categories: map[string][]string{
		"adrs": {"docs/decisions"},
	}}, "test")
	if cat, _, _ := cl.Match("docs/decisions-archive/old.md"); cat == model.CatADR {
		t.Error("docs/decisions must not match docs/decisions-archive")
	}
}

func TestLongestDeclaredSubtreeWins(t *testing.T) {
	cl, _ := NewClassifier(Config{Categories: map[string][]string{
		"documentation": {"docs"},
		"adrs":          {"docs/decisions"},
	}}, "test")
	if cat, _, _ := cl.Match("docs/decisions/a.md"); cat != model.CatADR {
		t.Errorf("the more specific subtree should win, got %q", cat)
	}
	if cat, _, _ := cl.Match("docs/guide.md"); cat != model.CatDocumentation {
		t.Errorf("got %q, want documentation", cat)
	}
}

func TestUnknownCategoryInConfigIsRejected(t *testing.T) {
	_, err := NewClassifier(Config{Categories: map[string][]string{
		"archtecture": {"docs/decisions"},
	}}, "test")
	if err == nil {
		t.Fatal("a misspelled category should be an error, not silently ignored")
	}
	if !strings.Contains(err.Error(), "adrs") {
		t.Errorf("the error should list valid categories, got %q", err)
	}
}
