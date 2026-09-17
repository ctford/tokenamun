package report

import (
	"sort"

	"github.com/ctford/tokenamun/internal/analysis"
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
	// Detail is shown in the tooltip for leaves.
	Detail   string  `json:"detail,omitempty"`
	Children []*Node `json:"children,omitempty"`
}

// BuildTree assembles the drill-down hierarchy.
//
// A viewer of retrieved content alone is answering a narrower question than
// "where did the tokens go", and by a wide margin: on a real session the
// retrieval carry is about a third of the bill, and retrieved content measured
// as *volume* is under one percent of it. Content is priced once and carried
// on every later call, and most of what is carried is not retrieved content at
// all -- it is the preamble, the conversation, and per-call overhead.
//
// So the tree covers the whole prompt cost. Retrieved content is the part that
// decomposes; the rest appears as sibling blocks that cannot be broken down
// from a transcript, and say so. That way a percentage in the viewer is a
// share of the session's cost rather than a share of the part we happen to be
// able to itemise.
//
// The hierarchy:
//
//	channel -> (command class | content category) -> file or result
//
// Shell output splits by what the command was doing, because "shell output"
// with no further structure is the least useful answer a profiler can give.
// Every other channel splits by content category.
func BuildTree(s *model.Session, carry analysis.CarryReport) *Node {
	root := buildRetrievalTree(s, carry)
	addNonRetrievalCost(root, s, carry)
	rollUp(root)
	sortTree(root)
	return root
}

// buildRetrievalTree builds the part that decomposes.
func buildRetrievalTree(s *model.Session, carry analysis.CarryReport) *Node {
	carryBySeq := map[int]analysis.CarriedItem{}
	for _, it := range carry.Items {
		carryBySeq[it.RetrievalSeq] = it
	}

	root := &Node{Name: "session", Kind: "root"}
	retrieved := &Node{Name: "retrieved content", Kind: "bucket"}
	root.Children = append(root.Children, retrieved)
	channels := map[model.Channel]*Node{}
	groups := map[string]*Node{}

	for _, c := range s.Retrievals {
		ch := c.Channel
		if ch == "" {
			ch = model.ChanOtherTool
		}
		chNode := channels[ch]
		if chNode == nil {
			chNode = &Node{Name: string(ch), Kind: "channel"}
			channels[ch] = chNode
			retrieved.Children = append(retrieved.Children, chNode)
		}

		// Second level: what the command was doing, or what the content is.
		groupName := string(c.Category)
		groupKind := "category"
		if ch == model.ChanShell && c.CommandClass != "" {
			groupName = c.CommandClass
			groupKind = "command"
		}
		key := string(ch) + "/" + groupName
		grp := groups[key]
		if grp == nil {
			grp = &Node{Name: groupName, Kind: groupKind}
			groups[key] = grp
			chNode.Children = append(chNode.Children, grp)
		}

		leafName := c.Path
		if leafName == "" {
			// Results with no path collapse into one rectangle per tool, so
			// the name has to say it is a bucket rather than a single result.
			leafName = "(unattributed " + c.Tool + " output)"
		}
		it := carryBySeq[c.Seq]
		leaf := &Node{
			Name:          leafName,
			Kind:          "item",
			Tokens:        c.Tokens,
			Carry:         it.CarryEIT,
			CarryUncached: it.CarryUncachedEIT,
			Bytes:         c.ObservedBytes(),
			Items:         1,
			Detail:        leafDetail(c, it),
		}
		if c.Tokens > 0 {
			leaf.CarryPerToken = it.CarryEIT / c.Tokens
		}
		grp.Children = append(grp.Children, leaf)
	}

	// Files read more than once collapse into one rectangle, since a treemap
	// of forty identical slivers hides the thing worth seeing.
	for _, ch := range retrieved.Children {
		for i, grp := range ch.Children {
			ch.Children[i] = collapseByName(grp)
		}
	}
	return root
}

// addNonRetrievalCost adds what the retrieval breakdown does not cover, so the
// tree accounts for the whole prompt cost.
//
// None of these can be decomposed from a transcript: the preamble is the
// system prompt, tool schemas and instruction files together with no
// separation available, and the remainder is user prompts, assistant text,
// thinking tokens, system reminders and per-call message envelope carried on
// every later call. They are blocks with a note rather than an omission,
// because leaving them out silently inflates every percentage in the view.
func addNonRetrievalCost(root *Node, s *model.Session, carry analysis.CarryReport) {
	var retrievalCarry float64
	for _, it := range carry.Items {
		retrievalCarry += it.CarryEIT
	}

	if carry.PreambleCarryEIT > 0 {
		root.Children = append(root.Children, &Node{
			Name: "session preamble", Kind: "bucket",
			Carry: carry.PreambleCarryEIT, CarryUncached: carry.PreambleCarryEIT, Items: 1,
			Detail: "system prompt, tool schemas, instruction files and skills, carried on " +
				"every call. Not decomposable: none of it is in the transcript.",
		})
	}

	rest := carry.PromptCostEIT - retrievalCarry - carry.PreambleCarryEIT
	if rest > 0 {
		root.Children = append(root.Children, &Node{
			Name: "conversation and overhead", Kind: "bucket",
			Carry: rest, CarryUncached: rest, Items: 1,
			Detail: "user prompts, assistant text, thinking tokens, system reminders and " +
				"per-call envelope, carried on every later call. Not decomposable.",
		})
	}

	w := cost.For(firstModel(s))
	if out := w.OutputCost(s.Usage()); out > 0 {
		root.Children = append(root.Children, &Node{
			Name: "output generated", Kind: "bucket",
			Carry: out, CarryUncached: out, Items: 1,
			Detail: "tokens the model wrote, priced at the output rate. Observed.",
		})
	}
}

func firstModel(s *model.Session) string {
	if ms := s.Models(); len(ms) > 0 {
		return ms[0]
	}
	return ""
}

// collapseByName merges leaves that name the same file.
func collapseByName(grp *Node) *Node {
	merged := map[string]*Node{}
	var order []string
	for _, leaf := range grp.Children {
		m := merged[leaf.Name]
		if m == nil {
			copy := *leaf
			merged[leaf.Name] = &copy
			order = append(order, leaf.Name)
			continue
		}
		m.Tokens += leaf.Tokens
		m.Carry += leaf.Carry
		m.CarryUncached += leaf.CarryUncached
		m.Bytes += leaf.Bytes
		m.Items += leaf.Items
	}
	out := &Node{Name: grp.Name, Kind: grp.Kind}
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

// sortTree orders every level largest first, so the layout is stable and the
// eye starts at the thing that matters.
func sortTree(n *Node) {
	sort.SliceStable(n.Children, func(i, j int) bool {
		if n.Children[i].Tokens != n.Children[j].Tokens {
			return n.Children[i].Tokens > n.Children[j].Tokens
		}
		return n.Children[i].Name < n.Children[j].Name
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
