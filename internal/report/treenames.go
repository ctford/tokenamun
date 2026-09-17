package report

import (
	"fmt"
	"strings"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/content"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
)

// What a node is called, and what it says when you hover it.
//
// Split from tree.go, which builds and reshapes the hierarchy. The two change
// for different reasons: the shape changes when the taxonomy does, and these
// change every time a name turns out not to explain itself -- which, on the
// evidence of this file's history, is often.

// Under file content the name is the file, because that is what the payload
// is. Under cli output it must be the command: naming a command's report
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
		return "mcp output", c.Tool
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
		// reading with the file unknown. Saying so beats inflating cli output
		// with it: the point of separating cli output is that git and test
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
			return "cli output", "unattributed commands"
		}
		if g := content.CommandGroup(c.CommandBinary); g != "" {
			return "cli output", g
		}
		// An unrecognised tool stays visible as itself rather than being
		// swept into a catch-all.
		return "cli output", c.CommandBinary
	}

	// What is left is what the harness's own tools returned: plan mode,
	// skills, questions, tool search.
	return "harness output", c.Tool
}

// levelDetail explains a level whose name cannot carry its own meaning.
func levelDetail(level string) string {
	if level == "unidentified files" {
		return "real file reading, with the file unknown."
	}
	return ""
}

func kindDetail(kind string) string {
	switch kind {
	case "file content":
		return "the contents of files, however they arrived."
	case "web content":
		return "fetched pages and search results."
	case "cli output":
		return "what command-line tools printed back."
	case "harness output":
		return "what Claude Code's own tools returned, not commands you ran."
	case "mcp output":
		return "what MCP servers returned."
	default:
		return ""
	}
}

func leafDetail(c model.RetrievedContent, it analysis.CarriedItem) string {
	d := c.Tool
	d += " · call " + itoa(c.InvocationSeq)
	if it.ResidentFor > 0 {
		d += " · " + itoa(it.ResidentFor) + " round trips"
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

// inlineProgramNote explains an interpreter that did not open up.
//
// 2.7% of one session sat in a single box called python3, and the reason is
// worth stating where it is seen: the calls were heredocs, so there is no
// program name to group them by. Saying nothing invites the question of
// whether the tool simply failed to split them.
func inlineProgramNote(n *Node) string {
	if !content.RunsInlineProgram(n.Name) || len(n.Children) > 0 || n.Items < 2 {
		return ""
	}
	return "inline code, so there is no program name to group by."
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

// unattributedDetail says what the remainder is, and unattributedMore makes
// the argument.
//
// Two lengths, because the tooltip and the terminal are different places. A
// 547-character paragraph on a box you are hovering over is not read; the
// same text in `tokenamun tree --at unattributed` is exactly what somebody
// asking has asked for.
func unattributedDetail() string {
	return "charged, but not attributable to any one piece of content."
}

// unattributedMore computes what the two largest candidates would come to.
//
// Both are ceilings, and both rest on residency the transcript does not
// confirm -- which is exactly why the cost is in the remainder and not in a
// branch. Computing them turns "34% we cannot explain" into "34%, of which
// this much would be one thing we decline to claim".
func unattributedMore(s *model.Session, carry analysis.CarryReport, rest float64) string {
	w := cost.For(firstModel(s))
	var b strings.Builder
	b.WriteString("Four things end up here. Thinking that was re-read: it is generated " +
		"and billed, but Claude Code records thinking blocks with empty text, so " +
		"whether it went round again is not in the transcript. The preamble after a " +
		"compaction, since what survives one is not observable. The system reminders " +
		"and per-call message envelope the harness adds, which are in the prompt but " +
		"in no retrieval. And the error in estimating content tokens from bytes.")

	if carry.ThinkingTokens > 0 && carry.AssistantRoundTrips > 0 {
		ceiling := float64(carry.ThinkingTokens) * carry.AssistantRoundTrips * w.CacheRead
		fmt.Fprintf(&b, " Carrying thinking the way the rest of the model's output is "+
			"carried would be up to %s, or %s of this remainder.",
			num(int(ceiling)), pctStr(ceiling/nonZero(rest)))
	}
	if beyond := float64(carry.Calls-1) - carry.PreambleRoundTrips; beyond > 0 {
		ceiling := float64(carry.Preamble) * beyond * w.CacheRead
		fmt.Fprintf(&b, " Carrying the preamble past the first context reset would be "+
			"up to a further %s, or %s.",
			num(int(ceiling)), pctStr(ceiling/nonZero(rest)))
	}
	return b.String()
}

func nonZero(v float64) float64 {
	if v == 0 {
		return 1
	}
	return v
}

func pluralCalls(n int) string {
	if n == 1 {
		return "1 call"
	}
	return itoa(n) + " calls"
}
