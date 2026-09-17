package content

import (
	"testing"

	"github.com/ctford/tokenamun/internal/model"
)

func TestIsFileContentPartitionsTheShellCorrectly(t *testing.T) {
	// One rule serves both the viewer's "file content" branch and the
	// file-compression intervention, so these cases are the contract between
	// them.
	cases := []struct {
		name string
		c    model.RetrievedContent
		want bool
	}{{
		name: "the Read tool",
		c:    model.RetrievedContent{Channel: model.ChanFileRead, Path: "a.go"},
		want: true,
	}, {
		name: "a tool that returned a document",
		c:    model.RetrievedContent{Channel: model.ChanOtherTool, Path: "docs/plan.md"},
		want: true,
	}, {
		name: "a tool that returned no document",
		c:    model.RetrievedContent{Channel: model.ChanOtherTool},
		want: false,
	}, {
		name: "sed reading a file",
		c:    model.RetrievedContent{Channel: model.ChanShell, CommandBinary: "sed", Path: "a.go"},
		want: true,
	}, {
		name: "sed reading a file the path could not be recovered for",
		c:    model.RetrievedContent{Channel: model.ChanShell, CommandBinary: "sed"},
		want: true,
	}, {
		name: "head downstream of a pipe is shaping output, not reading",
		c: model.RetrievedContent{Channel: model.ChanShell, CommandBinary: "head",
			PipelineFilter: true},
		want: false,
	}, {
		name: "a test run",
		c:    model.RetrievedContent{Channel: model.ChanShell, CommandBinary: "go"},
		want: false,
	}, {
		name: "an MCP result, whatever it mentions",
		c:    model.RetrievedContent{Channel: model.ChanMCP, Path: "a.go"},
		want: false,
	}, {
		name: "an edit confirmation",
		c:    model.RetrievedContent{Channel: model.ChanEdit, Path: "a.go"},
		want: false,
	}, {
		name: "a fetched web page",
		c:    model.RetrievedContent{Channel: model.ChanWeb, Path: "index.html"},
		want: false,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsFileContent(tc.c); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOutputIsNotAttributedToASilentStage(t *testing.T) {
	// A compound command's result is the concatenation of every stage's
	// output. Attributing it to a stage that cannot have written any of it is
	// the worst available guess -- and it was the one being made: on a real
	// session `git add X && git commit -m ...` filed 8.7% of the whole bill
	// under "git add", where the bytes were the commit's.
	cases := []struct {
		cmd  string
		want string
	}{
		{"git add CLAUDE.md && git commit -m 'x'", "git commit"},
		{"mkdir -p build && go test ./...", "go test"},
		{"cd /repo && git status -sb", "git status"},
		{"touch a.txt && ls -la", "ls"},
		// Nothing later to prefer, so the silent command keeps it: the output
		// is then an error message, and this is as good a guess as any.
		{"git add -A", "git add"},
		// A stage that prints comes first and stays first.
		{"go test ./... && git push", "go test"},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			p := CommandPath(tc.cmd)
			if len(p) == 0 {
				t.Fatalf("no command path for %q", tc.cmd)
			}
			got := p[len(p)-1]
			if got != tc.want {
				t.Errorf("got %q, want %q (path %v)", got, tc.want, p)
			}
		})
	}
}

func TestOnlyToolsWithSubcommandsOpenUpByTheirSecondWord(t *testing.T) {
	// `grep group`, `grep air`, `grep and`, `grep 25` were levels in a real
	// report: 59 children under grep, each holding one retrieval, every one
	// of them a search pattern mistaken for a mode of the tool.
	//
	// Whether a tool has subcommands is part of its published interface and
	// the same in every codebase, which is what makes it safe to know here.
	cases := []struct {
		cmd  string
		want []string
	}{
		{"git status -sb", []string{"git", "git status"}},
		{"pnpm run build", []string{"pnpm", "pnpm run"}},
		{"go test ./...", []string{"go", "go test"}},
		{"brew install jq", []string{"brew", "brew install"}},
		// Patterns and arguments, not modes.
		{"grep -rn group .", []string{"grep"}},
		{"grep air", []string{"grep"}},
		{"wc -l", []string{"wc"}},
		{"ls -la", []string{"ls"}},
		// A target runner's target is a level too: it is read off the command
		// line rather than hardcoded, so it is the same kind of fact as
		// `git status`.
		{"make build", []string{"make", "make build"}},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			got := CommandPath(tc.cmd)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got %v, want %v", got, tc.want)
					return
				}
			}
		})
	}
}
