// Package content classifies retrieved content into categories.
//
// Rules are ordered and first-match-wins, so a more specific rule must come
// before a more general one: docs/adr/0001.md is an ADR, not documentation,
// and internal/x_test.go is a test, not source.
package content

import (
	"path"
	"regexp"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// Rule maps a path pattern to a category.
type Rule struct {
	Pattern  *regexp.Regexp
	Category model.Category
}

// rules is the embedded default. Order matters.
var rules = []Rule{
	{regexp.MustCompile(`(^|/)adrs?/|architecture-decision|(^|/)adr-\d+|(^|/)\d{4}-.*-adr`), model.CatADR},
	{regexp.MustCompile(`(^|/)specs?/|\.spec\.[a-z]+$|(^|/)spec\.md$|(^|/)contracts?/|openapi|\.proto$`), model.CatSpecification},
	{regexp.MustCompile(`(^|/)plans?/|-plan\.md$|(^|/)plan\.md$|(^|/)roadmap\.md$`), model.CatPlan},
	{regexp.MustCompile(`_test\.go$|\.test\.[a-z]+$|\.spec\.ts$|_spec\.rb$|(^|/)tests?/|(^|/)spec/|(^|/)testdata/|^test_|/test_`), model.CatTest},
	{regexp.MustCompile(`(^|/)claude\.md$|(^|/)agents\.md$|(^|/)\.claude/|(^|/)skills?/|(^|/)readme\.md$|(^|/)contributing\.md$|(^|/)methodology\.md$|(^|/)\.cursor/`), model.CatInstructions},
	{regexp.MustCompile(`(^|/)docs?/|\.md$|\.rst$|\.adoc$|(^|/)wiki/`), model.CatDocumentation},
	{regexp.MustCompile(`\.(go|ts|tsx|js|jsx|py|rb|rs|java|kt|scala|c|h|cc|cpp|hpp|cs|php|swift|m|mm|ex|exs|clj|sh|bash|zsh|sql|tf|yaml|yml|json|toml|proto)$`), model.CatSourceCode},
}

// Classify categorises a path. An empty or unrecognised path is not forced
// into a category: callers decide what an unattributable payload is.
func Classify(p string) (model.Category, bool) {
	if p == "" {
		return model.CatOther, false
	}
	lower := strings.ToLower(p)
	for _, r := range rules {
		if r.Pattern.MatchString(lower) {
			return r.Category, true
		}
	}
	return model.CatOther, false
}

// ClassifyTool categorises a result by the tool that produced it, used when no
// path could be attributed.
func ClassifyTool(tool string) model.Category {
	if strings.HasPrefix(tool, "mcp__") {
		return model.CatMCPOutput
	}
	return model.CatToolOutput
}

// readingCommands are shell commands whose output is substantially the content
// of the files they name. A command not on this list is treated as tool output
// rather than as file content, because guessing wrongly here would inflate the
// source-code category with build logs.
var readingCommands = map[string]bool{
	"cat": true, "head": true, "tail": true, "sed": true, "awk": true,
	"grep": true, "rg": true, "less": true, "more": true, "jq": true,
	"yq": true, "nl": true, "bat": true,
}

// pathish matches an argument that looks like a file path we can classify.
var pathish = regexp.MustCompile(`^[~./A-Za-z0-9_@-][A-Za-z0-9_./@+-]*\.[A-Za-z0-9]+$`)

// PathsFromCommand extracts the file paths a shell command reads.
//
// This is a heuristic and its results are labelled inferred, never observed.
// It matters more than it looks: on sessions run in auto mode, Bash is ~87% of
// tool calls because file reads go through cat and sed, so a classifier that
// keyed off tool names alone would see almost nothing.
//
// Only the first pipeline stage is considered. In `cat foo.go | grep bar`, the
// content originates from foo.go; in `make build | tail -5` nothing is a file
// read, and tail's argument must not be mistaken for one.
func PathsFromCommand(cmd string) []string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil
	}
	// Take the first stage of the first command; later stages consume the
	// output of earlier ones rather than reading files themselves.
	for _, sep := range []string{"|", "&&", ";", ">"} {
		if i := strings.Index(cmd, sep); i >= 0 {
			cmd = cmd[:i]
		}
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return nil
	}
	name := path.Base(fields[0])
	if !readingCommands[name] {
		return nil
	}

	var out []string
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") {
			continue
		}
		f = strings.Trim(f, `"'`)
		if pathish.MatchString(f) {
			out = append(out, f)
		}
	}
	return out
}
