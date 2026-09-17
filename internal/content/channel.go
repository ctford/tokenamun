package content

import (
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

// CommandDetail names the specific command inside its family: the family says
// "git", this says "git status". It returns the text the family's own pattern
// matched, which is already the specific form.
func CommandDetail(cmd string) string {
	lower := strings.ToLower(cmd)
	for _, c := range commandClasses {
		if m := c.re.FindString(lower); m != "" {
			return strings.Join(strings.Fields(m), " ")
		}
	}
	return ""
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
//
// Only the stage that matched a known family is used, because a compound
// command like `cd /repo && git status` belongs under git rather than cd.
func CommandPath(cmd string) []string {
	stage, ok := matchingStage(cmd)
	if !ok {
		return nil
	}
	var words []string
	for _, f := range strings.Fields(stage) {
		// Flags, assignments, substitutions and quoted fragments are not
		// levels; they are the reason this used to produce nonsense.
		if strings.HasPrefix(f, "-") || strings.ContainsAny(f, "'\"$=(){}<>`") {
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
	out := []string{words[0]}
	if len(words) > 1 {
		// A path as the second word is not a subcommand: `cat foo.go` opens
		// up by file, not by "cat foo.go".
		if !strings.ContainsAny(words[1], "/.") {
			out = append(out, words[0]+" "+words[1])
		}
	}
	return out
}

// matchingStage finds the part of a compound command that a known family
// matched.
func matchingStage(cmd string) (string, bool) {
	for _, stage := range splitStages(cmd) {
		lower := strings.ToLower(stage)
		for _, c := range commandClasses {
			if c.re.MatchString(lower) {
				return lower, true
			}
		}
	}
	return "", false
}

// splitStages breaks a compound command into independently-executed parts.
func splitStages(cmd string) []string {
	var out []string
	for _, part := range stageSplitter.Split(cmd, -1) {
		if i := strings.IndexAny(part, ">"); i >= 0 {
			part = part[:i]
		}
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

var stageSplitter = regexp.MustCompile(`\|\||&&|[;\n|]`)

// CommandClass names what a shell command was doing. An unrecognised command
// is "other shell" rather than being forced into a class it does not fit.
func CommandClass(cmd string) string {
	lower := strings.ToLower(cmd)
	for _, c := range commandClasses {
		if c.re.MatchString(lower) {
			return c.name
		}
	}
	if strings.TrimSpace(cmd) == "" {
		return ""
	}
	return "other shell"
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
