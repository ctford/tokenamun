package report

import (
	"fmt"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/content"
	"github.com/ctford/tokenamun/internal/model"
)

// What a node is called, and what it says when you hover it.
//
// Split from tree.go, which builds and reshapes the hierarchy. The two change
// for different reasons: the shape changes when the taxonomy does, and these
// change every time a name turns out not to explain itself -- which, on the
// evidence of this file's history, is often.

// Under file content the name is the file, because that is what the payload
// is. Under CLI output it must be the command: naming a command's report
// after the file it was about -- `git log` of a plan, `wc` of a document --
// made a report look like the document's contents.
func leafNameFor(kind string, c model.RetrievedContent) string {
	if kind == "file content" {
		if c.Path != "" {
			return c.Path
		}
		if c.CommandBinary != "" {
			// Here the only thing known about the payload is the command, so
			// the label says that is what it is naming.
			return "read via " + c.CommandBinary
		}
	}
	if c.CommandDetail != "" {
		if c.Path != "" {
			return c.CommandDetail + " — " + c.Path
		}
		return c.CommandDetail
	}
	if c.Path != "" {
		return c.Path
	}
	return "(unattributed " + c.Tool + " output)"
}

// File content is separated from command output because they are different
// questions -- which files, versus which commands -- and CLI is separated
// from MCP because that is the axis the MCP-versus-CLI argument turns on.
func resultKind(c model.RetrievedContent) (kind, sub string) {
	switch c.Channel {
	case model.ChanMCP:
		return "MCP output", c.Tool
	case model.ChanWeb:
		return "web content", c.Tool
	case model.ChanSubagent:
		return "subagent reports", ""
	case model.ChanEdit:
		return "edit confirmations", ""
	}

	// What counts as file content is one rule, in content.IsFileContent, so
	// that this branch and the file-compression intervention cannot drift
	// apart: a saving priced over a different population than the one the
	// viewer draws is a number nobody can check against the picture.
	if content.IsFileContent(c) {
		if c.Path != "" {
			return "file content", ""
		}
		// Read through a compound shell command, so the content is real file
		// reading with the file unknown. Saying so beats inflating CLI output
		// with it: the point of separating CLI output is that git and test
		// runs are not file reading.
		return "file content", "unidentified files"
	}

	if c.Channel == model.ChanShell {
		// Grouped by the tool that ran, not by a purpose category: "which
		// CLI" is a question about tools.
		if !content.LooksLikeCommand(c.CommandBinary) {
			// A token that is not plausibly a command name came from an
			// unparsed heredoc. Saying so beats inventing a tool called
			// s1-tail-unserviceable.json.
			return "CLI output", "unattributed commands"
		}
		if g := content.CommandGroup(c.CommandBinary); g != "" {
			return "CLI output", g
		}
		// An unrecognised tool stays visible as itself rather than being
		// swept into a catch-all.
		return "CLI output", c.CommandBinary
	}

	// What is left is what the harness's own tools returned: plan mode,
	// skills, questions, tool search.
	return "harness output", c.Tool
}

// levelDetail explains a level whose name cannot carry its own meaning.
func levelDetail(level string) string {
	if level == "unidentified files" {
		return "file contents read through the shell where the filename could not be " +
			"recovered, because the command was compound or the path was in a variable: " +
			"`cd /repo && echo \"=== spec ===\" && sed -n '1,80p' \"$SPEC\"`. It is real file " +
			"reading with the file unknown. Reads through the Read tool, or through simpler " +
			"commands, are attributed to their files."
	}
	return ""
}

func kindDetail(kind string) string {
	switch kind {
	case "file content":
		return "the contents of files, however they arrived: the Read tool, cat and sed, or a " +
			"tool that returned a document."
	case "web content":
		return "what came back from fetching a page or running a search. Not the URL you " +
			"asked for, which is under model output."
	case "CLI output":
		return "what command-line tools reported: git, test runners, builds, searches, listings."
	case "harness output":
		return "what Claude Code's own tools returned: plan mode, skills, questions, " +
			"tool search. Not commands you ran."
	case "MCP output":
		return "what MCP servers returned. Compare its size with CLI output when weighing " +
			"whether to put a server behind a CLI."
	default:
		return ""
	}
}

func leafDetail(c model.RetrievedContent, it analysis.CarriedItem) string {
	d := c.Tool
	if c.CommandClass != "" {
		d += " · " + c.CommandClass
	}
	d += " · entered at call " + itoa(c.InvocationSeq)
	if it.ResidentFor > 0 {
		d += ", resident for " + itoa(it.ResidentFor) + " calls"
	}
	if c.Partial {
		d += " · partial read"
	}
	if c.Truncated {
		d += " · truncated by the harness"
	}
	if c.Images > 0 {
		d += " · " + itoa(c.Images) + " image(s), tokens not estimated"
	}
	return d
}

func pluralRetrievals(n int) string {
	if n == 1 {
		return "1 retrieval"
	}
	return itoa(n) + " retrievals"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

func byteStr(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
