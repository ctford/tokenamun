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
//	├─ writing output               the model did, at the output rate
//	│  ├─ thinking                  observed
//	│  ├─ prose
//	│  └─ tool arguments            → by tool
//	├─ output carried               the same words, re-read as input
//	│  ├─ prose
//	│  └─ tool arguments            → by tool
//	└─ tool results                 the environment answered
//	   ├─ file content              → by file
//	   ├─ CLI output                → by command family → by command
//	   ├─ MCP output                → by tool
//	   ├─ web                       → by tool
//	   └─ subagent reports
//
// Splitting CLI from MCP is deliberate: it is the axis the whole
// MCP-versus-CLI argument turns on, and it is observable.
func BuildTree(s *model.Session, carry analysis.CarryReport) *Node {
	root := &Node{Name: "session", Kind: "root"}
	root.Children = append(root.Children,
		preambleNode(carry),
		promptNode(s, carry),
		writingNode(s, carry),
		carriedNode(s, carry),
		resultsNode(s, carry),
	)
	root.Children = compact(root.Children)
	rollUp(root)
	sortTree(root)

	// The parts are estimated where output is apportioned by byte share, so
	// they can overshoot the measured cost. Report the gap rather than
	// clamping: a decomposition that reconciles itself silently looks more
	// certain than it is.
	measured := carry.PromptCostEIT + outputCost(s)
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

// writingNode is the output-rate charge for generating tokens. Output totals
// are observed and thinking is observed within them; the remainder is
// apportioned between prose and tool arguments by byte share, which is the
// only split available since the API reports one output number per call.
func writingNode(s *model.Session, carry analysis.CarryReport) *Node {
	w := cost.For(firstModel(s))
	usage := s.Usage()
	thinking := carry.ThinkingTokens
	rest := usage.Output - thinking

	n := &Node{Name: "writing output", Kind: "bucket",
		Detail: "the output-rate charge for generating tokens, five times the input rate. " +
			"Carrying them afterwards is counted separately."}
	if thinking > 0 {
		n.Children = append(n.Children, &Node{
			Name: "thinking", Kind: "source", Unscaled: true,
			Tokens: float64(thinking),
			Carry:  float64(thinking) * w.Output, CarryUncached: float64(thinking) * w.Output,
			Items: 1,
			Detail: "observed. Whether it is re-read as input afterwards is not knowable " +
				"from a transcript: Claude Code records thinking blocks with empty text.",
		})
	}
	prose, args := apportion(s, rest)
	if prose > 0 {
		n.Children = append(n.Children, &Node{
			Name: "prose", Kind: "source", Unscaled: true,
			Tokens: prose, Carry: prose * w.Output, CarryUncached: prose * w.Output, Items: 1,
			Detail: "assistant text. Apportioned from the observed output total by byte share.",
		})
	}
	if args > 0 {
		argNode := &Node{Name: "tool arguments", Kind: "source", Unscaled: true,
			Detail: "what the model wrote to invoke tools, apportioned by byte share."}
		argNode.Children = byToolArguments(s, args*w.Output, args)
		n.Children = append(n.Children, argNode)
	}
	return n
}

// carriedNode is the input-side cost of the model's own words.
func carriedNode(s *model.Session, carry analysis.CarryReport) *Node {
	n := &Node{Name: "output carried", Kind: "bucket",
		Detail: "the model re-reading its own words on every later call. Thinking is " +
			"excluded, since whether it is re-sent cannot be established here."}
	if carry.AssistantCarryEIT > 0 {
		n.Children = append(n.Children, &Node{
			Name: "prose", Kind: "source", Unscaled: true,
			Tokens: 0,
			Carry:  carry.AssistantCarryEIT, CarryUncached: carry.AssistantCarryEIT, Items: 1,
			Detail: "assistant text, re-sent as input for the rest of the session.",
		})
	}
	if carry.ToolInputCarryEIT > 0 {
		argNode := &Node{Name: "tool arguments", Kind: "source", Unscaled: true,
			Detail: "the arguments of every tool call, re-sent exactly as the results are."}
		argNode.Children = byToolArguments(s, carry.ToolInputCarryEIT, 0)
		n.Children = append(n.Children, argNode)
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

// resultsNode is what the environment sent back, split by mechanism.
func resultsNode(s *model.Session, carry analysis.CarryReport) *Node {
	carryBySeq := map[int]analysis.CarriedItem{}
	for _, it := range carry.Items {
		carryBySeq[it.RetrievalSeq] = it
	}

	root := &Node{Name: "tool results", Kind: "bucket",
		Detail: "content the environment returned, which is what the retrieval " +
			"optimisations all target."}
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
			root.Children = append(root.Children, g)
		}
		parent := g
		if sub != "" {
			// CLI output opens up command by command: git, then git checkout,
			// then git checkout AGENTS.md.
			levels := []string{sub}
			if kind == "CLI output" {
				if p := content.CommandPath(commands[c.ToolID]); len(p) > 0 {
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

	collapseLeaves(root)
	return root
}

// collapseLeaves merges repeated names wherever leaves sit, at any depth.
func collapseLeaves(n *Node) {
	if len(n.Children) == 0 {
		return
	}
	if n.Children[0].Kind == "item" {
		n.Children = collapseByName(n).Children
		return
	}
	for _, child := range n.Children {
		collapseLeaves(child)
	}
}

// resultKind decides which mechanism returned a payload, and what to open it
// up by. File content is separated from command output because they are
// different questions -- which files, versus which commands -- and CLI is
// separated from MCP because that is the axis the MCP-versus-CLI argument
// turns on.
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
	if c.Path != "" && isReadingClass(c.CommandClass, c.Channel) {
		return "file content", ""
	}
	if c.Channel == model.ChanShell {
		return "CLI output", c.CommandClass
	}
	return "other tool output", ""
}

// isReadingClass reports whether the payload is a file's contents rather than
// a command's report about files.
func isReadingClass(class string, ch model.Channel) bool {
	if ch == model.ChanFileRead {
		return true
	}
	return class == "cat / sed / head"
}

func kindDetail(kind string) string {
	switch kind {
	case "file content":
		return "the contents of files, however they were read: the Read tool or cat, sed and head."
	case "CLI output":
		return "what command-line tools reported: git, test runners, builds, searches, listings."
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
