package content

import "testing"

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

// Families are matched against the leading command word, not the whole stage.
// Matching the whole stage picked family names out of quoted strings and filed
// results under the wrong command: an echo label mentioning "head" put its
// output in the file-reading tree, inside a git branch.
func TestFamilyNamesInsideQuotedStringsAreIgnored(t *testing.T) {
	cases := []struct {
		name  string
		cmd   string
		class string
		last  string
	}{
		{"echo label mentioning a command",
			`cd /repo && echo "=== head ===" && git log --oneline -5`,
			"git", "git log"},
		{"commit message mentioning a command",
			`git commit -m "run go test before pushing"`,
			"git", "git commit"},
		{"echo label mentioning git",
			`echo "=== git status ===" && go test ./...`,
			"tests", "go test"},
		{"cd is not the command",
			`cd /repo && cat internal/x.go`,
			"cat / sed / head", "cat"},
		{"time is a wrapper, not the command",
			`time mise run check`,
			"package management", "mise run"},
	}
	for _, c := range cases {
		if got := CommandClass(c.cmd); got != c.class {
			t.Errorf("%s: class = %q, want %q", c.name, got, c.class)
		}
		if got := CommandDetail(c.cmd); got != c.last {
			t.Errorf("%s: detail = %q, want %q", c.name, got, c.last)
		}
	}
}

// The class and the drill-down path must come from the same stage, or a result
// lands in one family's branch labelled with another family's command.
func TestClassAndPathAgreeOnTheSameStage(t *testing.T) {
	cmd := `cd /repo && echo "=== head ===" && git show HEAD:AGENTS.md`
	class := CommandClass(cmd)
	p := CommandPath(cmd)
	if len(p) == 0 {
		t.Fatal("expected a command path")
	}
	if class != "git" || p[0] != "git" {
		t.Fatalf("class %q and path %v disagree", class, p)
	}
}

func TestUnrecognisedCommandsAreNotForced(t *testing.T) {
	if got := CommandClass("./scripts/weird-thing --flag"); got != "other shell" {
		t.Errorf("got %q, want other shell", got)
	}
	if got := CommandClass(""); got != "" {
		t.Errorf("empty command should have no class, got %q", got)
	}
}

// Tool groups encode identity, which is stable across the industry: git is
// version control everywhere, sed is a POSIX text tool everywhere. They must
// not encode role, which varies per repository.
func TestCommandGroupsAreByToolIdentity(t *testing.T) {
	cases := map[string]string{
		"git":     "version control",
		"gh":      "version control",
		"go":      "language toolchains",
		"pnpm":    "language toolchains",
		"python3": "interpreters",
		"node":    "interpreters",
		"kubectl": "containers and orchestration",
		"gcloud":  "cloud and infrastructure",
		"mise":    "task runners",
		"curl":    "network",
		"ruff":    "linters and formatters",
	}
	// And the POSIX utilities are deliberately ungrouped: see groups.go.
	// grep is a tool you act on directly, and a heading hid it.
	for _, ungrouped := range []string{"grep", "sort", "cat", "sed", "find", "wc"} {
		if got := CommandGroup(ungrouped); got != "" {
			t.Errorf("CommandGroup(%q) = %q, want no group", ungrouped, got)
		}
	}
	for binary, want := range cases {
		if got := CommandGroup(binary); got != want {
			t.Errorf("CommandGroup(%q) = %q, want %q", binary, got, want)
		}
	}
}

func TestUnrecognisedToolsAreNotSweptIntoAGroup(t *testing.T) {
	// An unknown tool should stay visible as itself rather than being filed
	// under a guess.
	for _, binary := range []string{"the reference repository", "my-custom-thing", ""} {
		if got := CommandGroup(binary); got != "" {
			t.Errorf("CommandGroup(%q) = %q, want no group", binary, got)
		}
	}
}

// Output from an unparsed heredoc used to be filed under a "binary" called
// s1-tail-unserviceable.json, which is real output under a nonsense name.
func TestTokensThatAreNotCommandNamesAreRejected(t *testing.T) {
	for _, bad := range []string{
		"s1-tail-unserviceable.json", "services/spine/main.go", "2", "",
		"FOO=bar", "$var", "'quoted",
	} {
		if LooksLikeCommand(bad) {
			t.Errorf("LooksLikeCommand(%q) = true", bad)
		}
	}
	for _, good := range []string{"git", "grep", "python3", "golangci-lint"} {
		if !LooksLikeCommand(good) {
			t.Errorf("LooksLikeCommand(%q) = false", good)
		}
	}
}

// A file-printing tool downstream of a pipe is filtering someone else's
// output, not reading a file. In real sessions head, tail and cat are used
// that way far more often than as readers, so counting their output as file
// content attributed it to files that were never read.
//
// Where the upstream command is itself recognised -- `git log | head -20` --
// the earliest matching stage wins and the output is attributed to git, which
// is the better answer still.
func TestPipelineFiltersAreDistinguishedFromFileReads(t *testing.T) {
	// The filter case is when nothing upstream is recognised, so the
	// file-printing tool is the only thing the transcript can name.
	filters := []string{
		`./scripts/report.sh | head -20`,
		`./bin/report | sed -n '1,20p'`,
	}
	for _, cmd := range filters {
		if !IsPipelineFilter(cmd) {
			t.Errorf("%q: expected a pipeline filter", cmd)
		}
	}

	// When the source is recognised, the content is attributed to it rather
	// than to the filter downstream.
	for _, cmd := range []string{
		"git log --oneline | head -20",
		"go test ./... 2>&1 | tail -50",
		"find . -name '*.go' | head",
	} {
		if IsPipelineFilter(cmd) {
			t.Errorf("%q: the upstream command is recognised, so it is the source", cmd)
		}
	}
	if got := CommandBinary("git log --oneline | head -20"); got != "git" {
		t.Errorf("attribution went to %q, want git", got)
	}

	readers := []string{
		`cat internal/pay/charge.go`,
		`sed -n '1,80p' docs/plan.md`,
		`cd /repo && head -40 README.md`,
		`tail -100 /var/log/app.log`,
	}
	for _, cmd := range readers {
		if IsPipelineFilter(cmd) {
			t.Errorf("%q: expected a file read, not a filter", cmd)
		}
	}
}

// && and ; sequence commands; they do not feed one command's output into the
// next, so a read after them is still a read.
func TestOnlyPipesMakeADownstreamFilter(t *testing.T) {
	for _, cmd := range []string{
		`cd /repo && cat x.go`,
		`echo "=== x ===" ; cat x.go`,
		`make build || cat build.log`,
	} {
		if IsPipelineFilter(cmd) {
			t.Errorf("%q: sequencing is not piping", cmd)
		}
	}
}

// A tool in two groups would make its grouping depend on map iteration order,
// so the tables must be disjoint. Checked here as well as panicking at init,
// because a panic at init is a bad way to find out.
func TestToolGroupsAreDisjoint(t *testing.T) {
	seen := map[string]string{}
	for group, members := range groupMembers {
		for _, m := range members {
			if other, dup := seen[m]; dup {
				t.Errorf("%q is in both %q and %q", m, other, group)
			}
			seen[m] = group
		}
	}
}

// Paths must come from stages of the family that produced the output. Taking
// them from any reading stage let a later grep name a file for output that
// git had produced, so file names appeared as leaves inside the git tree.
func TestPathsComeFromTheProducingFamilyOnly(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want []string
	}{
		{"a later grep does not name git's output",
			`git log --oneline -- docs/plan.md | grep -n internal/x.go`,
			nil},
		{"a later filter does not name the reader's file",
			`cat internal/x.go | grep -n docs/other.md`,
			[]string{"internal/x.go"}},
		{"two stages of the same family both count",
			`cat docs/a.md && cat docs/b.md`,
			[]string{"docs/a.md", "docs/b.md"}},
		// A path is only extracted when the output *is* that file's content.
		// wc prints a count, git log prints history: those are reports about
		// a file, and attributing the file to them would say the session read
		// content it never saw.
		{"a count is not the file",
			`wc -l docs/plan.md`,
			nil},
		{"history is not the file",
			`git log --oneline -- docs/plan.md`,
			nil},
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

// Wrappers are stripped before a command is identified, so listing one in a
// tool group would create a membership that can never be reached.
func TestWrappersAreNotAlsoGroupMembers(t *testing.T) {
	for wrapper := range wrappers {
		if g := CommandGroup(wrapper); g != "" {
			t.Errorf("%q is stripped as a wrapper but also grouped under %q", wrapper, g)
		}
	}
	// And the stripping works: the binary is the real command.
	if got := CommandBinary("xargs grep -n foo"); got != "grep" {
		t.Errorf("xargs grep resolved to %q, want grep", got)
	}
}
