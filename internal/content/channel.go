package content

import (
	"path"
	"regexp"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// commandClasses group shell commands by the job they do. Ordered: the first
// match wins, so a test command that also mentions git is a test run.
//
// Named after the commands rather than the purpose, because the level above
// is already named by mechanism and two levels reading "file reading" told a
// reader nothing about which was which.
var commandClasses = []struct {
	name string
	re   *regexp.Regexp
}{
	{"tests", regexp.MustCompile(`\b(go test|npm test|pnpm test|yarn test|pytest|jest|vitest|cargo test|mvn test|gradle test|make test|ginkgo|rspec)\b`)},
	{"build and lint", regexp.MustCompile(`\b(go build|go vet|golangci-lint|npm run build|pnpm build|tsc|make build|cargo build|eslint|ruff|clippy|terraform|docker build)\b`)},
	{"git", regexp.MustCompile(`\bgit\b`)},
	{"cat / sed / head", regexp.MustCompile(`\b(cat|head|tail|sed|awk|nl|bat|jq|yq)\b`)},
	{"grep / rg / find", regexp.MustCompile(`\b(rg|grep|ag|ack|find|fd)\b`)},
	{"ls / tree / du", regexp.MustCompile(`\b(ls|tree|du|df|stat|wc)\b`)},
	{"package management", regexp.MustCompile(`\b(npm|pnpm|yarn|pip|go mod|cargo|bundle|brew|mise)\b`)},
	{"containers and cloud", regexp.MustCompile(`\b(docker|kubectl|gcloud|aws|helm)\b`)},
	{"scripting", regexp.MustCompile(`\b(python3?|node|ruby|perl|bash -c|sh -c)\b`)},
}

// notCommands are stages that never produce content: they label output,
// change directory or do nothing. Matching them is how `echo "=== head ==="`
// ended up filed as a file-reading command inside the git tree.
var notCommands = map[string]bool{
	"echo": true, "printf": true, "cd": true, "export": true,
	"true": true, ":": true, "set": true, "source": true,
	// Shell control flow: `for f in *; do git show $f; done` should be filed
	// under git, not under "do".
	"do": true, "done": true, "then": true, "else": true, "elif": true,
	"fi": true, "for": true, "while": true, "if": true, "case": true,
	"esac": true, "in": true,
}

// wrappers run another command. They are stripped rather than skipped, so
// `time mise run check` is package management rather than unrecognised.
var wrappers = map[string]bool{
	"time": true, "sudo": true, "env": true, "nohup": true,
	"command": true, "nice": true, "timeout": true, "xargs": true,
}

// matchStage finds the stage of a compound command that produced its output,
// and which family it belongs to.
//
// The family is matched against the stage's leading command word (and the
// second word, for two-word forms like `git log` or `go test`) rather than
// against the whole stage. Matching the whole stage picked up family names
// inside quoted strings -- an echo label mentioning "head", a commit message
// mentioning "git" -- and put the result under the wrong command entirely.
// It also meant the class and the drill-down path could be derived from
// different stages of the same command, so they disagreed.
// silentCommands produce no output on success.
//
// An identity property of the command, not a role it plays in a project:
// `git add` prints nothing whether you use it to stage a fix or a feature.
// That matters because a compound command's result is the concatenation of
// every stage's output, and attributing it to a stage that cannot have
// written any of it is the worst available guess. On one reference session
// `git add X && git commit -m ...` filed 8.7% of the whole bill under
// "git add", where the bytes were the commit's -- most of them the
// pre-commit hook's test output.
var silentCommands = map[string]bool{
	"git add": true, "git stage": true, "git rm": true, "git mv": true,
	"mkdir": true, "touch": true, "chmod": true, "chown": true,
	"ln": true, "mv": true, "cp": true, "rm": true, "export": true,
	"set": true, "unset": true, "cd": true,
}

func matchStage(cmd string) (text, family string, piped, ok bool) {
	// Two passes. A stage that cannot have produced output is skipped while
	// a later stage might have; if every stage is silent the first one is
	// used after all, because the output is then an error message and the
	// first command is as good a guess as any.
	if t, f, p, found := matchStageSkipping(cmd, true); found {
		return t, f, p, true
	}
	return matchStageSkipping(cmd, false)
}

func matchStageSkipping(cmd string, skipSilent bool) (text, family string, piped, ok bool) {
	for _, candidate := range splitStages(cmd) {
		fields := strings.Fields(candidate.text)
		for len(fields) > 1 && strings.Contains(fields[0], "=") && !strings.Contains(fields[0], "/") {
			fields = fields[1:]
		}
		if len(fields) == 0 {
			continue
		}
		for len(fields) > 1 && wrappers[path.Base(strings.ToLower(fields[0]))] {
			fields = fields[1:]
		}
		name := path.Base(strings.ToLower(fields[0]))
		if notCommands[name] {
			continue
		}
		probe := name
		if len(fields) > 1 {
			probe += " " + strings.ToLower(fields[1])
		}
		if skipSilent && (silentCommands[name] || silentCommands[probe]) {
			continue
		}
		for _, cl := range commandClasses {
			if cl.re.MatchString(probe) {
				return strings.ToLower(candidate.text), cl.name, candidate.piped, true
			}
		}
	}
	return "", "", false, false
}

// familyOfStage is the command family of a single stage, or false when the
// stage is not a command we recognise.
func familyOfStage(text string) (string, bool) {
	fields := strings.Fields(text)
	for len(fields) > 1 && strings.Contains(fields[0], "=") && !strings.Contains(fields[0], "/") {
		fields = fields[1:]
	}
	for len(fields) > 1 && wrappers[path.Base(strings.ToLower(fields[0]))] {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return "", false
	}
	name := path.Base(strings.ToLower(fields[0]))
	if notCommands[name] {
		return "", false
	}
	probe := name
	if len(fields) > 1 {
		probe += " " + strings.ToLower(fields[1])
	}
	for _, cl := range commandClasses {
		if cl.re.MatchString(probe) {
			return cl.name, true
		}
	}
	return "", false
}

// IsPipelineFilter reports whether the command that produced this output was
// downstream of a pipe, and so was filtering another command's output rather
// than reading a file.
//
// This matters more than it sounds: in real sessions head, tail and cat are
// overwhelmingly used as filters -- `something | head -20` -- and counting
// their output as file content attributed most of it to files that were never
// read. Only sed turned out to be mostly a genuine file reader.
func IsPipelineFilter(cmd string) bool {
	_, _, piped, ok := matchStage(cmd)
	return ok && piped
}

// fileReadingBinaries print a file's contents. Used to route their output to
// file content rather than to a command family, since `cat x.go` returns the
// file while `git show x.go` returns a report about it.
var fileReadingBinaries = map[string]bool{
	"cat": true, "head": true, "tail": true, "sed": true, "awk": true,
	"nl": true, "bat": true, "jq": true, "yq": true, "less": true, "more": true,
}

// IsFileReading reports whether a binary returns file contents.
func IsFileReading(binary string) bool { return fileReadingBinaries[binary] }

// CommandBinary is the command that produced the output: git, grep, python3,
// go. It is what "which CLI" means.
//
// The tree groups by this rather than by CommandClass, because the classes
// mixed two ideas -- purpose for tests and build, tool set for
// "cat / sed / head" and "scripting" -- and "scripting" in particular put
// python3, node and ruby in one box when they are entirely different tools.
// CommandClass survives because interventions need the purpose: RTK publishes
// a figure for test runs, not for python3.
func CommandBinary(cmd string) string {
	p := CommandPath(cmd)
	if len(p) == 0 {
		return ""
	}
	return p[0]
}

// CommandPath returns progressively more specific forms of the command, so a
// reader can open "git" into "git log" or "git status".
//
// It stops at the subcommand. Going further was tried and abandoned: real
// agent commands are compound shells with heredocs, quoted format strings,
// subshells and assignments, so splitting on whitespace produced levels like
//
//	git log --reverse --format='===   ->   git log --reverse --format='=== %h
//	was=$(git rev-parse               ->   was=$(git rev-parse @{upstream})
//
// which are noise, usually have exactly one child, and would need a real
// shell parser to fix. The arguments worth seeing are file paths, and those
// are already recovered separately and attributed as content.
func CommandPath(cmd string) []string {
	text, _, _, ok := matchStage(cmd)
	if !ok {
		return nil
	}
	var words []string
	for _, f := range strings.Fields(text) {
		if strings.HasPrefix(f, "-") || strings.ContainsAny(f, "'\"$=(){}<>`") {
			continue
		}
		if len(words) == 0 && wrappers[path.Base(strings.ToLower(f))] {
			continue
		}
		words = append(words, f)
		if len(words) == 2 {
			break
		}
	}
	if len(words) == 0 {
		return nil
	}
	out := []string{path.Base(words[0])}
	// A path as the second word is not a subcommand: `cat foo.go` opens up by
	// file, not by "cat foo.go".
	if len(words) > 1 && !strings.ContainsAny(words[1], "/.") {
		out = append(out, out[0]+" "+words[1])
	}
	return out
}

// stage is one part of a compound command, and whether it was fed by a pipe.
//
// The distinction matters: `cat x.go` reads a file, while `git log | cat`
// only reformats what git produced. A tool downstream of a pipe is filtering
// someone else's output, so the content belongs to whatever is upstream.
type stage struct {
	text  string
	piped bool
}

// splitStages breaks a compound command into independently-executed parts,
// keeping track of which were fed by a pipe.
func splitStages(cmd string) []stage {
	var out []stage
	piped := false
	rest := cmd
	for {
		loc := stageSplitter.FindStringIndex(rest)
		var part, sep string
		if loc == nil {
			part, sep, rest = rest, "", ""
		} else {
			part, sep, rest = rest[:loc[0]], rest[loc[0]:loc[1]], rest[loc[1]:]
		}
		if i := strings.IndexAny(part, ">"); i >= 0 {
			part = part[:i]
		}
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, stage{text: trimmed, piped: piped})
		}
		if loc == nil {
			return out
		}
		// Only a single pipe feeds the next stage its predecessor's output.
		piped = sep == "|"
	}
}

var stageSplitter = regexp.MustCompile(`\|\||&&|[;\n|]`)

// CommandClass names what a shell command was doing. An unrecognised command
// is "other shell" rather than being forced into a class it does not fit.
func CommandClass(cmd string) string {
	if _, family, _, ok := matchStage(cmd); ok {
		return family
	}
	if strings.TrimSpace(cmd) == "" {
		return ""
	}
	return "other shell"
}

// CommandDetail names the specific command inside its family: the family says
// "git", this says "git log".
func CommandDetail(cmd string) string {
	p := CommandPath(cmd)
	if len(p) == 0 {
		return ""
	}
	return p[len(p)-1]
}

// ChannelFor reports how content arrived.
//
// Shell output that could be attributed to a file is still shell output by
// channel: the distinction the viewer needs at its top level is how the
// content was obtained, since that is what a reader can change. What the
// content turned out to be is Category, one level down.
func ChannelFor(tool string, direct bool) model.Channel {
	if strings.HasPrefix(tool, "mcp__") {
		return model.ChanMCP
	}
	switch tool {
	case "Read", "NotebookRead":
		return model.ChanFileRead
	case "Bash", "BashOutput":
		return model.ChanShell
	case "WebFetch", "WebSearch":
		return model.ChanWeb
	case "Agent", "Task":
		return model.ChanSubagent
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		return model.ChanEdit
	default:
		return model.ChanOtherTool
	}
}

// IsFileContent reports whether a retrieval is the contents of a file,
// however it arrived.
//
// One rule, in one place, because two things need it and they must agree: the
// viewer's "file content" branch and the file-compression intervention. A
// saving priced over a different population than the one the viewer shows is
// a number nobody can check against the picture.
//
// Three cases, in order. A path attached to a non-shell result means the file
// itself came back, whichever tool delivered it -- restricting that to Read
// and cat filed a plan document from ExitPlanMode under "other tool output",
// where nobody would look for it. A shell result from a file-printing binary
// is file content even when the path could not be recovered from a compound
// command. And a file-printing binary downstream of a pipe is not reading a
// file at all: `git log | head -20` is a git report, and counting it as file
// content attributed a third of that bucket to files that were never read.
func IsFileContent(c model.RetrievedContent) bool {
	switch c.Channel {
	case model.ChanMCP, model.ChanWeb, model.ChanSubagent, model.ChanEdit:
		return false
	case model.ChanShell:
		return IsFileReading(c.CommandBinary) && !c.PipelineFilter
	default:
		return c.Path != ""
	}
}
