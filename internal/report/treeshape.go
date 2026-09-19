package report

import (
	"sort"
	"strings"
)

// The tree is built in tree.go and reconciled here: collapsing levels that
// say nothing, nesting paths into directories, merging repeated names and
// totalling each branch from its children. Split out when tree.go hit the
// file-length budget, along the seam the budget exposed -- this half changes
// when the shape of the hierarchy changes, the other when what goes into it
// does.

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

// nestByDirectory turns a flat list of file paths into a directory tree, the
// way a disk-usage viewer does.
//
// Without it, a file read by name and a directory read by glob sat at the same
// level with nothing to distinguish them: docs/plans/journey-04-plan.md next
// to docs/decisions, where the second is a directory only because the command
// used a glob. Nesting makes that difference structural rather than
// invisible, and lets a reader see which *area* of the tree cost the most
// before drilling to individual files.
//
// Chains of single-child directories are collapsed, so a lone file deep in a
// tree reads as docs/plans rather than as docs, then plans, then the file.
func nestByDirectory(n *Node) {
	var leaves, branches []*Node
	for _, c := range n.Children {
		if c.Kind == "item" && strings.Contains(c.Name, "/") {
			leaves = append(leaves, c)
			continue
		}
		branches = append(branches, c)
	}
	if len(leaves) == 0 {
		return
	}

	root := &Node{Name: n.Name, Kind: n.Kind}
	for _, leaf := range leaves {
		// Keep an absolute path absolute: trimming the leading separator
		// turned /Users/... into Users/..., which names a different thing.
		abs := strings.HasPrefix(leaf.Name, "/")
		segments := strings.Split(strings.Trim(leaf.Name, "/"), "/")
		if abs && len(segments) > 0 {
			segments[0] = "/" + segments[0]
		}
		// A glob or bare-directory read has no filename, so every segment is
		// a directory and the cost hangs inside it. Putting it beside the
		// directory instead would make a directory look like one of its own
		// files.
		isDir := looksLikeDirectory(leaf.Name)
		parent := root
		for i, seg := range segments {
			if !isDir && i == len(segments)-1 {
				copied := *leaf
				copied.Name = seg
				parent.Children = append(parent.Children, &copied)
				break
			}
			parent = ensureDir(parent, seg)
		}
		if isDir {
			copied := *leaf
			copied.Name = "(read as a directory)"
			parent.Children = append(parent.Children, &copied)
		}
	}
	collapseSingleChildDirs(root)
	n.Children = append(branches, root.Children...)
}

// GroupsEarnTheirPlaceAt is how many tools a tool-identity group needs before
// it is worth a level of its own.
//
// A group's whole job is to stop a handful of small rows crowding out a big
// one: "standard unix tools" holds grep, find, head, tail, ls, wc and which, and
// hoisting all seven would bury git. Three rows is not a crowd. Below this
// many members the group costs a click and saves nothing -- "language
// toolchains" holding pnpm, go and npm is a word you have to click through to
// learn that you ran pnpm.
//
// A threshold rather than a judgement per group, because the taxonomy is
// fixed and industry-wide while how much of it a session exercised is not.
const GroupsEarnTheirPlaceAt = 4

// collapseEmptyLevels removes a level you click through to learn nothing.
//
// Two kinds of them. A tool-identity group with too few tools in it to be
// worth the click: one is always pointless, and the threshold above says
// where the rest stop paying for themselves. The members are hoisted into the
// parent, so nothing is lost but the heading.
//
// And a node whose only child repeats its name, which is how "git add"
// containing one leaf called "git add" happened. That is not a hierarchy, it
// is the same row twice.
func collapseEmptyLevels(n *Node) {
	var out []*Node
	for _, c := range n.Children {
		collapseEmptyLevels(c)

		if c.Kind == "group" && len(c.Children) < GroupsEarnTheirPlaceAt {
			out = append(out, c.Children...)
			continue
		}
		// A node whose only child is a repeat of itself.
		for len(c.Children) == 1 && c.Children[0].Name == c.Name {
			only := c.Children[0]
			if only.Detail == "" {
				only.Detail = c.Detail
			}
			c = only
		}
		out = append(out, c)
	}
	n.Children = out
}

// ensureDir finds or creates a directory level.
func ensureDir(parent *Node, name string) *Node {
	for _, c := range parent.Children {
		if c.Kind == "dir" && c.Name == name {
			return c
		}
	}
	dir := &Node{Name: name, Kind: "dir"}
	parent.Children = append(parent.Children, dir)
	return dir
}

// looksLikeDirectory reports whether an attributed path names a directory. A
// final segment with no extension, from a glob or a bare directory argument,
// is the signature.
func looksLikeDirectory(p string) bool {
	last := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		last = p[i+1:]
	}
	return !strings.Contains(last, ".")
}

// collapseSingleChildDirs joins a directory that contains exactly one
// directory into its child, so the path reads docs/plans rather than nesting
// twice for no information.
func collapseSingleChildDirs(n *Node) {
	for i, c := range n.Children {
		collapseSingleChildDirs(c)
		for c.Kind == "dir" && len(c.Children) == 1 && c.Children[0].Kind == "dir" {
			only := c.Children[0]
			only.Name = c.Name + "/" + only.Name
			c = only
		}
		n.Children[i] = c
	}
}

// leafNameFor names a payload for the branch it sits in.
//

// resultKind decides which mechanism returned a payload, and what to open it
// up by.
//

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
		m.tokenCalls += leaf.tokenCalls
		m.samples = append(m.samples, leaf.samples...)
	}
	out := &Node{Name: grp.Name, Kind: grp.Kind, Detail: grp.Detail}
	for _, name := range order {
		m := merged[name]
		if m.Items > 1 {
			m.Detail = pluralRetrievals(m.Items) + ", " + m.Detail
		}
		if m.Tokens > 0 {
			m.CarryPerToken = m.Carry / m.Tokens
			m.RoundTrips = m.tokenCalls / m.Tokens
		}
		out.Children = append(out.Children, m)
	}
	return out
}

// rollUp totals each branch from its children.
func rollUp(n *Node) {
	if len(n.Children) == 0 {
		// A leaf normally arrives with its round trips already set. A merged
		// one does not: it has accumulated tokenCalls from several sessions
		// and nothing has divided through yet.
		if n.Tokens > 0 && n.tokenCalls > 0 {
			n.RoundTrips = n.tokenCalls / n.Tokens
		}
		setDistribution(n)
		return
	}
	n.Tokens, n.Carry, n.CarryUncached, n.Bytes, n.Items = 0, 0, 0, 0, 0
	n.tokenCalls = 0
	// Gathered from the children rather than kept on the branch, so a branch
	// answers the same question about the retrievals under it that a leaf
	// answers about its own: was this level uniformly expensive, or does it
	// contain one event.
	n.samples = nil
	for _, child := range n.Children {
		rollUp(child)
		n.Tokens += child.Tokens
		n.Carry += child.Carry
		n.CarryUncached += child.CarryUncached
		n.Bytes += child.Bytes
		n.Items += child.Items
		n.tokenCalls += child.tokenCalls
		n.samples = append(n.samples, child.samples...)
	}
	setDistribution(n)
	if n.Tokens > 0 {
		n.CarryPerToken = n.Carry / n.Tokens
		n.RoundTrips = n.tokenCalls / n.Tokens
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

// explainInlinePrograms notes why an interpreter did not open up.
func explainInlinePrograms(n *Node) {
	for _, c := range n.Children {
		explainInlinePrograms(c)
		if note := inlineProgramNote(c); note != "" {
			// Appended, not assigned: a merged leaf already carries its tool
			// and residency, and both are worth keeping.
			if c.Detail == "" {
				c.Detail = note
			} else {
				c.Detail += " · " + note
			}
		}
	}
}
