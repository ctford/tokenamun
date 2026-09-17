// Package content classifies retrieved content into categories.
//
// There are two mechanisms, and the order matters. A Classifier built from
// .tokenamun.json maps declared subtrees to categories, which is exact: the
// team says where its decision records live. The ordered rules below are the
// zero-configuration fallback -- a guess from directory naming, covering the
// conventions that are common across codebases rather than any one
// repository's layout. A declared answer is knowledge; a rule match is an
// assumption, and reports distinguish them.
//
// Rules are first-match-wins, so a more specific rule must come before a more
// general one: docs/adr/0001.md is an ADR, not documentation, and
// internal/x_test.go is a test, not source.
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
	{regexp.MustCompile(`(^|/)docs?/(decisions?|decision-records?|rfcs?)(/|$)`), model.CatADR},
	{regexp.MustCompile(`(^|/)(decisions?|decision-records?|rfcs?)/[^/]*\.(md|rst|adoc|txt)$`), model.CatADR},
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

// pathish matches an argument that looks like a file we can classify.
var pathish = regexp.MustCompile(`^[~./A-Za-z0-9_@*-][A-Za-z0-9_./@+*-]*\.[A-Za-z0-9*]+$`)

// dirish matches an argument that looks like a directory: it has a separator
// and its last segment carries no extension. Classification works as well on
// a directory as on a file, and real sessions read whole directories.
var dirish = regexp.MustCompile(`^[~./A-Za-z0-9_@-][A-Za-z0-9_./@+-]*/?$`)

// patternFirst are commands whose first non-flag argument is a search pattern
// rather than a path. Without this, `rg 'foo/bar' .` attributes content to a
// file called foo/bar that never existed.
var patternFirst = map[string]bool{"rg": true, "grep": true, "ag": true, "ack": true}

// PathsFromCommand extracts the file paths a shell command reads.
//
// This is a heuristic and its results are labelled inferred, never observed.
// It matters more than it looks: on sessions run in auto mode, Bash is the
// large majority of tool calls because file reads go through cat and sed, so a
// classifier that keyed off tool names alone would see almost nothing.
//
// Real commands are compound. The dominant shape in agent sessions is
// something like
//
//	cd /repo && echo "=== a ===" && cat docs/decisions/x.md && git log -2
//
// so every stage is examined, not just the first: an earlier version read only
// up to the first separator, found `cd`, and attributed nothing at all for
// over a hundred kilobytes of genuinely attributable content. Within a
// pipeline stage only the reading command's own arguments count, because a
// later stage consumes the previous one's output rather than reading files.
func PathsFromCommand(cmd string) []string {
	var out []string
	seen := map[string]bool{}
	for _, stage := range stages(cmd) {
		for _, p := range pathsFromStage(stage) {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// stageSeparators split a compound command into independently-executed parts.
var stageSeparators = regexp.MustCompile(`\|\||&&|[;\n|]`)

// stages splits a compound command. Redirections are cut rather than split on,
// since what follows is a destination, not a command.
func stages(cmd string) []string {
	var out []string
	for _, part := range stageSeparators.Split(cmd, -1) {
		if i := strings.IndexAny(part, ">"); i >= 0 {
			part = part[:i]
		}
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// pathsFromStage extracts the paths one command stage reads.
func pathsFromStage(stage string) []string {
	fields := strings.Fields(stage)
	if len(fields) == 0 {
		return nil
	}
	// Skip a leading environment assignment, e.g. FOO=1 cat x.
	for len(fields) > 1 && strings.Contains(fields[0], "=") && !strings.Contains(fields[0], "/") {
		fields = fields[1:]
	}
	name := path.Base(fields[0])
	if !readingCommands[name] {
		return nil
	}

	var out []string
	skipPattern := patternFirst[name]
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") {
			continue
		}
		f = strings.Trim(f, `"'`)
		if f == "" {
			continue
		}
		if skipPattern {
			// The search pattern, not a path.
			skipPattern = false
			continue
		}
		switch {
		case pathish.MatchString(f):
			out = append(out, globParent(f))
		case strings.Contains(f, "/") && dirish.MatchString(f):
			out = append(out, strings.TrimSuffix(f, "/"))
		}
	}
	return out
}

// globParent reduces a glob to the directory it sits in, so that
// docs/adr/*.md classifies as decision records rather than as nothing.
func globParent(f string) string {
	if !strings.ContainsAny(f, "*?[") {
		return f
	}
	if i := strings.LastIndex(f, "/"); i > 0 {
		return f[:i]
	}
	return f
}
