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
