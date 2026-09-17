package report

import (
	"sort"

	"github.com/ctford/tokenamun/internal/analysis"
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
	// Carry is what keeping it cost, in EIT.
	Carry float64 `json:"carry"`
	Bytes int     `json:"bytes"`
	Items int     `json:"items"`
	// CarryPerToken drives the colour ramp: how expensive this content was to
	// keep relative to its size.
	CarryPerToken float64 `json:"carryPerToken"`
	// Detail is shown in the tooltip for leaves.
	Detail   string  `json:"detail,omitempty"`
	Children []*Node `json:"children,omitempty"`
}

// BuildTree assembles the drill-down hierarchy:
//
//	channel -> (command class | content category) -> file or result
//
// Shell output splits by what the command was doing, because "shell output"
// with no further structure is the least useful answer a profiler can give.
// Every other channel splits by content category.
func BuildTree(s *model.Session, carry analysis.CarryReport) *Node {
	carryBySeq := map[int]analysis.CarriedItem{}
	for _, it := range carry.Items {
		carryBySeq[it.RetrievalSeq] = it
	}

	root := &Node{Name: "all retrieved content", Kind: "root"}
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
			root.Children = append(root.Children, chNode)
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
			Name:   leafName,
			Kind:   "item",
			Tokens: c.Tokens,
			Carry:  it.CarryEIT,
			Bytes:  c.ObservedBytes(),
			Items:  1,
			Detail: leafDetail(c, it),
		}
		if c.Tokens > 0 {
			leaf.CarryPerToken = it.CarryEIT / c.Tokens
		}
		grp.Children = append(grp.Children, leaf)
	}

	// Files read more than once collapse into one rectangle, since a treemap
	// of forty identical slivers hides the thing worth seeing.
	for _, ch := range root.Children {
		for i, grp := range ch.Children {
			ch.Children[i] = collapseByName(grp)
		}
	}
	rollUp(root)
	sortTree(root)
	return root
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
	n.Tokens, n.Carry, n.Bytes, n.Items = 0, 0, 0, 0
	for _, child := range n.Children {
		rollUp(child)
		n.Tokens += child.Tokens
		n.Carry += child.Carry
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
