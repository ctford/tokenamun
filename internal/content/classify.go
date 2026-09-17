// Package content works out what a tool result was: which file it came from,
// which command produced it, and how it arrived.
//
// It deliberately does not classify content into semantic categories such as
// ADRs or specifications. That axis was removed: in practice a repository's
// directory layout already carries it -- docs/decisions *is* the decision
// records -- so nesting retrieved content by directory answers the same
// question with no configuration and no guessing about someone else's
// project. The one thing categories could do that directories cannot is
// aggregate content scattered by convention, and on real sessions that was a
// rounding error next to the cost of being wrong about a layout.
package content

import (
	"path"
	"regexp"
	"strings"
)

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
	// Paths come only from stages belonging to the family that produced the
	// output. Taking them from any reading stage let a `grep` later in a
	// pipeline name a file for output that `git log` had produced, so file
	// names appeared as leaves inside the git tree. Restricting it to the one
	// matched stage went too far the other way: `cat a.md && cat b.md`
	// returns both files, and both are the same family.
	_, family, _, ok := matchStage(cmd)
	if !ok {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, st := range splitStages(cmd) {
		if f, ok := familyOfStage(st.text); !ok || f != family {
			continue
		}
		for _, p := range pathsFromStage(st.text) {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
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
