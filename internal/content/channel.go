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
func matchStage(cmd string) (stage, family string, ok bool) {
	for _, candidate := range splitStages(cmd) {
		fields := strings.Fields(candidate)
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
		for _, cl := range commandClasses {
			if cl.re.MatchString(probe) {
				return strings.ToLower(candidate), cl.name, true
			}
		}
	}
	return "", "", false
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
	stage, _, ok := matchStage(cmd)
	if !ok {
		return nil
	}
	var words []string
	for _, f := range strings.Fields(stage) {
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
	if _, family, ok := matchStage(cmd); ok {
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
