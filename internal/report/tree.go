package report

import (
	"fmt"
	"sort"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/content"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
)

// Node is one rectangle in the viewer, and may contain others.
//
// The hierarchy answers "where did the tokens go" by starting with how content
// was obtained -- file reading, shell output, web -- because that is the level
// a reader can act on. What the content turned out to be, and which file it
// came from, are the levels below.
type Node struct {
	Name string `json:"name"`
	// Kind labels the level, so the viewer can say what it is showing.
	Kind string `json:"kind"`
	// Tokens is the observed size of content here, in estimated tokens.
	Tokens float64 `json:"tokens"`
	// Carry is what keeping it cost, in EIT, as actually billed.
	Carry float64 `json:"carry"`
	// CarryUncached is the same residency priced with no caching. The ratio
	// against Carry is what prompt caching was worth here.
	CarryUncached float64 `json:"carryUncached"`
	Bytes         int     `json:"bytes"`
	Items         int     `json:"items"`
	// CarryPerToken drives the colour ramp: how expensive this content was to
	// keep relative to its size.
	CarryPerToken float64 `json:"carryPerToken"`
	// Unscaled marks a node the colour ramp does not apply to: it has cost but
	// no attributable token count, so a carry-per-token rate is undefined
	// rather than low. Rendering it at the palest step would read as "cheap to
	// keep", which is a claim we cannot make.
	Unscaled bool `json:"unscaled,omitempty"`
	// Detail is shown in the tooltip for leaves.
	Detail   string  `json:"detail,omitempty"`
	Children []*Node `json:"children,omitempty"`
	// Reconciliation is set on the root when the parts overshoot the measured
	// prompt cost, which happens through byte-per-token estimation error.
	Reconciliation float64 `json:"reconciliation,omitempty"`
}

// BuildTree assembles the drill-down hierarchy.
//
// One axis at the top: who or what put the tokens there. That is the level a
// reader can act on, because each branch is a different conversation --
// with the harness, with yourself, with the model, or with the environment.
//
//	session
//	├─ preamble                     the harness put it there
//	├─ your prompts                 you did
//	├─ model output                 the model wrote it, then re-read it
//	│  ├─ prose
//	│  ├─ tool arguments            → by tool
//	│  └─ thinking                  observed; its carry is not knowable
//	├─ file content                 the environment answered → by file
//	├─ CLI output                   → by command family → by subcommand
//	├─ MCP output                   → by tool
//	├─ web                          → by tool
//	└─ subagent reports
//
// Splitting CLI from MCP is deliberate: it is the axis the whole
// MCP-versus-CLI argument turns on, and it is observable.
func BuildTree(s *model.Session, carry analysis.CarryReport) *Node {
	root := &Node{Name: "session", Kind: "root"}
	root.Children = append(root.Children,
		preambleNode(carry),
		promptNode(s, carry),
		modelOutputNode(s, carry),
	)
	// The mechanisms the environment answered through sit at the top level
	// rather than under a "tool results" parent. Grouping them by authorship
	// was tidier, but it buried file reading two clicks down, and file
	// reading is the first thing anyone looks for.
	root.Children = append(root.Children, resultsNodes(s, carry)...)
	root.Children = compact(root.Children)
	rollUp(root)

	// Whatever the parts do not account for has to be present. Without it
	// every percentage in the view is a share of the part we can itemise
	// rather than of the session, which is the bug this block exists to
	// prevent -- and merging the output blocks reintroduced it once already.
	measured := carry.PromptCostEIT + outputCost(s)
	if rest := measured - root.Carry; rest > 0 {
		root.Children = append(root.Children, &Node{
			Name: "unattributed", Kind: "bucket", Unscaled: true,
			Carry: rest, CarryUncached: rest, Items: 1,
			Detail: "what the parts above do not account for: system reminders, per-call " +
				"message envelope, thinking re-read if it is re-read at all, and the error " +
				"in apportioning output by byte share. Reported rather than distributed.",
		})
		rollUp(root)
	}
	sortTree(root)

	// The parts are estimated where output is apportioned by byte share, so
	// they can overshoot the measured cost. Report the gap rather than
	// clamping: a decomposition that reconciles itself silently looks more
	// certain than it is.
	if measured > 0 && root.Carry > measured {
		root.Reconciliation = measured - root.Carry
	}
	return root
}

func preambleNode(carry analysis.CarryReport) *Node {
	return &Node{
		Name: "preamble", Kind: "bucket", Unscaled: true,
		Tokens: float64(carry.Preamble),
		Carry:  carry.PreambleCarryEIT, CarryUncached: carry.PreambleCarryEIT, Items: 1,
		Detail: "system prompt, tool schemas, instruction files and skills, carried on every " +
			"call. Not decomposable: none of its parts are in the transcript.",
	}
}

func promptNode(s *model.Session, carry analysis.CarryReport) *Node {
	var bytes int
	for _, pe := range s.PromptEntries {
		bytes += pe.Bytes
	}
	return &Node{
		Name: "your prompts", Kind: "bucket", Unscaled: true,
		Tokens: float64(bytes) / ratioOf(s),
		Carry:  carry.PromptCarryEIT, CarryUncached: carry.PromptCarryEIT,
		Items:  len(s.PromptEntries),
		Detail: "what you typed, carried for the rest of the session.",
	}
}

// modelOutputNode is everything the model's own words cost.
//
// Each word is paid for twice: once at the output rate when written, and again
// at the input rate on every later call that re-reads it. Shown as two
// sibling blocks those read as duplication, because it is the same text. So
// there is one block per kind of output, with the split in its detail.
//
// Output totals are observed and thinking is observed within them; the
// remainder is apportioned between prose and tool arguments by byte share,
// which is the only split available since the API reports one output number
// per call.
func modelOutputNode(s *model.Session, carry analysis.CarryReport) *Node {
	w := cost.For(firstModel(s))
	thinking := carry.ThinkingTokens
	prose, args := apportion(s, s.Usage().Output-thinking)

	n := &Node{Name: "model output", Kind: "bucket",
		Detail: "what the model wrote, priced twice: at the output rate when written, " +
			"then at the input rate on every later call that re-reads it."}

	// The carried figure covers prose and tool arguments together, so it is
	// apportioned the same way the generation is.
	proseShare := 0.0
	if prose+args > 0 {
		proseShare = prose / (prose + args)
	}

	if prose > 0 {
		gen := prose * w.Output
		held := carry.AssistantCarryEIT * proseShare
		n.Children = append(n.Children, &Node{
			Name: "prose", Kind: "source", Unscaled: true,
			Tokens: prose, Carry: gen + held, CarryUncached: gen + held, Items: 1,
			Detail: fmt.Sprintf("assistant text: %s to write, %s to keep re-reading",
				num(int(gen)), num(int(held))),
		})
	}
	if args > 0 {
		gen := args * w.Output
		held := carry.AssistantCarryEIT * (1 - proseShare)
		argNode := &Node{Name: "tool arguments", Kind: "source", Unscaled: true,
			Detail: fmt.Sprintf("what the model wrote to invoke tools: %s to write, "+
				"%s to keep re-reading", num(int(gen)), num(int(held)))}
		argNode.Children = byToolArguments(s, gen+held, args)
		n.Children = append(n.Children, argNode)
	}
	if thinking > 0 {
		gen := float64(thinking) * w.Output
		n.Children = append(n.Children, &Node{
			Name: "thinking", Kind: "source", Unscaled: true,
			Tokens: float64(thinking), Carry: gen, CarryUncached: gen, Items: 1,
			Detail: "observed, and priced at the output rate for writing it. Whether it is " +
				"re-read as input afterwards is not knowable from a transcript: Claude Code " +
				"records thinking blocks with empty text.",
		})
	}
	return n
}

// byToolArguments splits a cost across the tools whose arguments produced it,
// which is what "which tools" means on this side of the ledger.
func byToolArguments(s *model.Session, totalCost, totalTokens float64) []*Node {
	bytesByTool := map[string]int{}
	callsByTool := map[string]int{}
	var total int
	for _, tc := range s.ToolCalls {
		if tc.InputBytes == 0 {
			continue
		}
		bytesByTool[tc.Name] += tc.InputBytes
		callsByTool[tc.Name]++
		total += tc.InputBytes
	}
	if total == 0 {
		return nil
	}
	var out []*Node
	for name, b := range bytesByTool {
		share := float64(b) / float64(total)
		out = append(out, &Node{
			Name: name, Kind: "tool", Unscaled: true,
			Tokens: totalTokens * share,
			Carry:  totalCost * share, CarryUncached: totalCost * share,
			Items:  callsByTool[name],
			Detail: fmt.Sprintf("%d calls, %s of arguments", callsByTool[name], byteStr(b)),
		})
	}
	return out
}

// resultsNodes is what the environment sent back, one node per mechanism.
func resultsNodes(s *model.Session, carry analysis.CarryReport) []*Node {
	carryBySeq := map[int]analysis.CarriedItem{}
	for _, it := range carry.Items {
		carryBySeq[it.RetrievalSeq] = it
	}

	var order []*Node
	commands := map[string]string{}
	for _, tc := range s.ToolCalls {
		if tc.Command != "" {
			commands[tc.ID] = tc.Command
		}
	}

	groups := map[string]*Node{}
	nested := map[string]*Node{}

	for _, c := range s.Retrievals {
		it := carryBySeq[c.Seq]
		kind, sub := resultKind(c)

		g := groups[kind]
		if g == nil {
			g = &Node{Name: kind, Kind: "mechanism", Detail: kindDetail(kind)}
			groups[kind] = g
			order = append(order, g)
		}
		parent := g
		if sub != "" {
			// CLI output opens up command by command: git, then git checkout,
			// then git checkout AGENTS.md.
			levels := []string{sub}
			if kind == "CLI output" {
				if p := content.CommandPath(commands[c.ToolID]); len(p) > 0 {
					// Inside a group the binary is its own level; where the
					// group name already is the binary, do not repeat it.
					if sub != p[0] {
						levels = append(levels, p[0])
					}
					levels = append(levels, p[1:]...)
				}
			}
			key := kind
			for _, level := range levels {
				key += "/" + level
				child := nested[key]
				if child == nil {
					child = &Node{Name: level, Kind: "command"}
					nested[key] = child
					parent.Children = append(parent.Children, child)
				}
				parent = child
			}
		}

		leafName := c.Path
		if leafName == "" {
			leafName = c.CommandDetail
			if leafName == "" {
				leafName = "(unattributed " + c.Tool + " output)"
			}
		}
		leaf := &Node{
			Name: leafName, Kind: "item",
			Tokens: c.Tokens, Carry: it.CarryEIT, CarryUncached: it.CarryUncachedEIT,
			Bytes: c.ObservedBytes(), Items: 1,
			Detail: leafDetail(c, it),
		}
		if c.Tokens > 0 {
			leaf.CarryPerToken = it.CarryEIT / c.Tokens
		}
		parent.Children = append(parent.Children, leaf)
	}

	for _, g := range order {
		collapseLeaves(g)
	}
	return order
}

// collapseLeaves merges repeated names wherever leaves sit, at any depth.
//
// A level can hold both leaves and branches -- file content holds individual
// files alongside a "path not attributed" branch -- so it must do both. An
// earlier version checked only the first child and returned, which left
// eighteen separate rows all called "sed" inside a branch it never reached.
func collapseLeaves(n *Node) {
	if len(n.Children) == 0 {
		return
	}

	var leaves, branches []*Node
	for _, child := range n.Children {
		if child.Kind == "item" {
			leaves = append(leaves, child)
			continue
		}
		branches = append(branches, child)
	}
	for _, b := range branches {
		collapseLeaves(b)
	}
	if len(leaves) == 0 {
		return
	}
	merged := collapseByName(&Node{Name: n.Name, Kind: n.Kind, Children: leaves})
	n.Children = append(branches, merged.Children...)
}

// resultKind decides which mechanism returned a payload, and what to open it
// up by.
//
// File content is separated from command output because they are different
// questions -- which files, versus which commands -- and CLI is separated
// from MCP because that is the axis the MCP-versus-CLI argument turns on.
func resultKind(c model.RetrievedContent) (kind, sub string) {
	switch c.Channel {
	case model.ChanMCP:
		return "MCP output", c.Tool
	case model.ChanWeb:
		return "web", c.Tool
	case model.ChanSubagent:
		return "subagent reports", ""
	case model.ChanEdit:
		return "edit confirmations", ""
	}

	// Anything that came back with a file path attached is that file's
	// contents, whichever tool delivered it. Restricting this to the Read
	// tool and to cat filed a plan document returned by ExitPlanMode under
	// "other tool output", where nobody would look for it.
	if c.Channel != model.ChanShell && c.Path != "" {
		return "file content", ""
	}

	if c.Channel == model.ChanShell {
		// Shell reads are file content too. When the path could not be
		// recovered from a compound command they are still file content, and
		// saying so beats inflating CLI output with them: the whole point of
		// separating CLI output is that git and test runs are not file
		// reading.
		if content.IsFileReading(c.CommandBinary) {
			if c.Path != "" {
				return "file content", ""
			}
			return "file content", "path not attributed"
		}
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

	// What is left is the harness's own tools: plan mode, skills, questions,
	// tool search. Named for what they are, since "other" told a reader
	// nothing and invited the question of how it differed from CLI output.
	return "harness tools", c.Tool
}

func kindDetail(kind string) string {
	switch kind {
	case "file content":
		return "the contents of files, however they arrived: the Read tool, cat and sed, or a " +
			"tool that returned a document. Reads whose path could not be recovered from a " +
			"compound command are grouped separately rather than counted as commands."
	case "CLI output":
		return "what command-line tools reported: git, test runners, builds, searches, listings."
	case "harness tools":
		return "Claude Code's own tools: plan mode, skills, questions, tool search. Not " +
			"commands you ran."
	case "MCP output":
		return "what MCP servers returned. Compare its size with CLI output when weighing " +
			"whether to put a server behind a CLI."
	default:
		return ""
	}
}

func outputCost(s *model.Session) float64 {
	return cost.For(firstModel(s)).OutputCost(s.Usage())
}

// apportion splits non-thinking output between prose and tool arguments by
// byte share, since the API reports one output number per call.
func apportion(s *model.Session, rest int64) (prose, args float64) {
	var argBytes int
	for _, tc := range s.ToolCalls {
		argBytes += tc.InputBytes
	}
	total := s.ProseBytes + argBytes
	if rest <= 0 || total == 0 {
		return 0, 0
	}
	proseShare := float64(s.ProseBytes) / float64(total)
	return float64(rest) * proseShare, float64(rest) * (1 - proseShare)
}

// compact drops branches with no cost, so an empty category is absent rather
// than a zero-area rectangle.
func compact(nodes []*Node) []*Node {
	var out []*Node
	for _, n := range nodes {
		n.Children = compact(n.Children)
		if n.Carry > 0 || len(n.Children) > 0 {
			out = append(out, n)
		}
	}
	return out
}

func ratioOf(s *model.Session) float64 {
	if s.Estimator.BytesPerToken > 0 {
		return s.Estimator.BytesPerToken
	}
	return 3.6
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

// collapseByName merges leaves that name the same thing, since a treemap of
// forty identical slivers hides what is worth seeing.
func collapseByName(grp *Node) *Node {
	merged := map[string]*Node{}
	var order []string
	for _, leaf := range grp.Children {
		m := merged[leaf.Name]
		if m == nil {
			copied := *leaf
			merged[leaf.Name] = &copied
			order = append(order, leaf.Name)
			continue
		}
		m.Tokens += leaf.Tokens
		m.Carry += leaf.Carry
		m.CarryUncached += leaf.CarryUncached
		m.Bytes += leaf.Bytes
		m.Items += leaf.Items
	}
	out := &Node{Name: grp.Name, Kind: grp.Kind, Detail: grp.Detail}
	for _, name := range order {
		m := merged[name]
		if m.Items > 1 {
			m.Detail = pluralRetrievals(m.Items) + ", " + m.Detail
		}
		if m.Tokens > 0 {
			m.CarryPerToken = m.Carry / m.Tokens
		}
		out.Children = append(out.Children, m)
	}
	return out
}

func firstModel(s *model.Session) string {
	if ms := s.Models(); len(ms) > 0 {
		return ms[0]
	}
	return ""
}

// rollUp totals each branch from its children.
func rollUp(n *Node) {
	if len(n.Children) == 0 {
		return
	}
	n.Tokens, n.Carry, n.CarryUncached, n.Bytes, n.Items = 0, 0, 0, 0, 0
	for _, child := range n.Children {
		rollUp(child)
		n.Tokens += child.Tokens
		n.Carry += child.Carry
		n.CarryUncached += child.CarryUncached
		n.Bytes += child.Bytes
		n.Items += child.Items
	}
	if n.Tokens > 0 {
		n.CarryPerToken = n.Carry / n.Tokens
	}
}

// sortTree orders every level by cost, largest first, so the layout is stable
// and the eye starts where the money is. Cost rather than volume, because
// several branches legitimately have cost and no attributable token count.
func sortTree(n *Node) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.Carry != b.Carry {
			return a.Carry > b.Carry
		}
		if a.Tokens != b.Tokens {
			return a.Tokens > b.Tokens
		}
		return a.Name < b.Name
	})
	for _, child := range n.Children {
		sortTree(child)
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
