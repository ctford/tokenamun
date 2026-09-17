package report

import (
	"fmt"
	"sort"
	"strings"

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
	// CarryPerToken is the node's cost divided by its size. Kept for the
	// payload; nothing displays it, because a price over a size needs a
	// paragraph and ResidentCalls says the same thing in calls.
	CarryPerToken float64 `json:"carryPerToken"`
	// RoundTrips is how many calls this content sat through, averaged over its
	// tokens. It drives the colour ramp.
	//
	// The ramp used to be CarryPerToken, which is a price ratio and needed a
	// paragraph nobody could make short enough. This is the thing that causes
	// it: the model has no memory, so content still in the context goes back
	// and forth on every call and is billed each time. "Round trips" is the
	// reader's own word for it and needs no gloss.
	RoundTrips float64 `json:"roundTrips,omitempty"`
	// tokenCalls is the accumulator behind ResidentCalls: the sum over
	// retrievals of tokens x calls resident. Weighted by tokens, so a big file
	// carried briefly does not read the same as a small one carried
	// throughout. Unexported: it is scaffolding, not a finding.
	tokenCalls float64
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
//	│  ├─ replies to you            what it said to you
//	│  ├─ tool arguments            what it said to tools → by tool
//	│  └─ thinking                  what it said to itself; carry not knowable
//	├─ file content                 the environment answered → by file
//	├─ CLI output                   → by command family → by subcommand
//	├─ MCP output                   → by tool
//	├─ web content                  → by tool
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
	collapseEmptyLevels(root)
	rollUp(root)

	// Whatever the parts do not account for has to be present. Without it
	// every percentage in the view is a share of the part we can itemise
	// rather than of the session, which is the bug this block exists to
	// prevent -- and merging the output blocks reintroduced it once already.
	measured := carry.PromptCostEIT + outputCost(s)
	// The no-caching total is the same arithmetic against a counterfactual
	// bill: every prompt token at full input price, output unchanged. Without
	// its own remainder the uncached mode reconciled against the cached total
	// and reported the difference as a saving on content it had not repriced.
	measuredUncached := carry.PromptCostUncachedEIT + outputCost(s)
	restUncached := measuredUncached - root.CarryUncached
	if rest := measured - root.Carry; rest > 0 {
		if restUncached < rest {
			restUncached = rest
		}
		root.Children = append(root.Children, &Node{
			Name: "unattributed", Kind: "bucket", Unscaled: true,
			Carry: rest, CarryUncached: restUncached, Items: 1,
			Detail: unattributedDetail(s, carry, rest),
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
		Name: "preamble", Kind: "bucket",
		Tokens: float64(carry.Preamble),
		Carry:  carry.PreambleCarryEIT, CarryUncached: carry.PreambleCarryUncachedEIT, Items: 1,
		RoundTrips: carry.PreambleRoundTrips,
		tokenCalls: float64(carry.Preamble) * carry.PreambleRoundTrips,
		Detail: "system prompt, tool schemas, instruction files and skills, carried on every " +
			"call. Not decomposable: none of its parts are in the transcript. Its round " +
			"trips and its cost both stop at the first context reset -- compaction can " +
			"leave a prefix smaller than the first call's prompt, and what the harness " +
			"put back is not observable -- so both are lower bounds.",
	}
}

func promptNode(s *model.Session, carry analysis.CarryReport) *Node {
	var bytes int
	for _, pe := range s.PromptEntries {
		bytes += pe.Bytes
	}
	tokens := float64(bytes) / ratioOf(s)
	return &Node{
		Name: "your prompts", Kind: "bucket",
		Tokens: tokens,
		Carry:  carry.PromptCarryEIT, CarryUncached: carry.PromptCarryUncachedEIT,
		Items:      len(s.PromptEntries),
		RoundTrips: carry.PromptRoundTrips,
		tokenCalls: tokens * carry.PromptRoundTrips,
		Detail:     "what you typed, carried for the rest of the session.",
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

	// The model writes three kinds of thing: what it says to you, what it
	// says to tools, and what it says to itself.
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
		// Generation is at the output rate whatever the cache does, so only
		// the re-reading differs between the two modes.
		gen := prose * w.Output
		held := carry.AssistantCarryEIT * proseShare
		heldUncached := carry.AssistantCarryUncachedEIT * proseShare
		n.Children = append(n.Children, &Node{
			Name: "replies to you", Kind: "source",
			Tokens: prose, Carry: gen + held, CarryUncached: gen + heldUncached, Items: 1,
			RoundTrips: carry.AssistantRoundTrips,
			tokenCalls: prose * carry.AssistantRoundTrips,
			Detail: fmt.Sprintf("the text it wrote for you to read, as opposed to its "+
				"thinking or its tool calls: %s to write, %s to keep re-reading",
				num(int(gen)), num(int(held))),
		})
	}
	if args > 0 {
		gen := args * w.Output
		held := carry.AssistantCarryEIT * (1 - proseShare)
		heldUncached := carry.AssistantCarryUncachedEIT * (1 - proseShare)
		argNode := &Node{Name: "tool arguments", Kind: "source",
			Detail: fmt.Sprintf("what it wrote to invoke tools - the command strings, "+
				"file paths and patch text. The other side of the same calls is CLI "+
				"output, which is what the tools printed back: %s to write, %s to keep "+
				"re-reading", num(int(gen)), num(int(held)))}
		argNode.Children = byToolArguments(s, carry, gen+held, gen+heldUncached, args)
		n.Children = append(n.Children, argNode)
	}
	if thinking > 0 {
		gen := float64(thinking) * w.Output
		n.Children = append(n.Children, &Node{
			Name: "thinking", Kind: "source", Unscaled: true,
			// Grey, and this is what grey is for: Claude Code records thinking
			// blocks with empty text, so whether they go round again at all is
			// not in the transcript. No round trips, and for the same reason
			// the cost is identical in both modes -- this is generation only,
			// with no residency for caching to discount.
			Tokens: float64(thinking), Carry: gen, CarryUncached: gen, Items: 1,
			Detail: "what it wrote for itself, not shown to you. Observed, and priced at " +
				"the output rate for writing it. Whether it is re-read as input afterwards " +
				"is not knowable from a transcript: Claude Code records thinking blocks " +
				"with empty text.",
		})
	}
	return n
}

// byToolArguments splits a cost across the tools whose arguments produced it,
// which is what "which tools" means on this side of the ledger.
func byToolArguments(s *model.Session, carry analysis.CarryReport,
	totalCost, totalCostUncached, totalTokens float64) []*Node {
	bytesByTool := map[string]int{}
	callsByTool := map[string]int{}
	// Residency is weighted by argument bytes and taken from the call that
	// wrote them, so a tool used early reads differently from one used at the
	// end. Inheriting one session-wide average instead put the same number on
	// every row, which is a shade that tells you nothing.
	tripBytesByTool := map[string]float64{}
	var total int
	for _, tc := range s.ToolCalls {
		if tc.InputBytes == 0 {
			continue
		}
		bytesByTool[tc.Name] += tc.InputBytes
		callsByTool[tc.Name]++
		tripBytesByTool[tc.Name] += float64(tc.InputBytes) *
			float64(carry.RoundTripsByCall[tc.InvocationSeq])
		total += tc.InputBytes
	}
	if total == 0 {
		return nil
	}
	var out []*Node
	for name, b := range bytesByTool {
		share := float64(b) / float64(total)
		tokens := totalTokens * share
		trips := 0.0
		if b > 0 {
			trips = tripBytesByTool[name] / float64(b)
		}
		out = append(out, &Node{
			Name: name, Kind: "tool",
			Tokens: tokens,
			Carry:  totalCost * share, CarryUncached: totalCostUncached * share,
			Items:      callsByTool[name],
			RoundTrips: trips,
			tokenCalls: tokens * trips,
			Detail: fmt.Sprintf("%d calls, %s of arguments. This is what the model "+
				"wrote to invoke the tool, not what the tool printed back -- that is "+
				"under CLI output, MCP output or file content, depending on the tool.",
				callsByTool[name], byteStr(b)),
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
			for depth, level := range levels {
				key += "/" + level
				child := nested[key]
				if child == nil {
					// Mark the tool-identity group -- version control,
					// language toolchains -- so a group that turns out to
					// hold one tool can be dropped: see
					// collapseSingleChildGroups. Only a name that came from
					// the taxonomy counts. An unrecognised binary also sits
					// at this level, and it is a tool, not a group: removing
					// `npx` because it only ran vitest would lose the fact
					// that vitest was run through npx.
					nodeKind := "command"
					if depth == 0 && level == content.CommandGroup(c.CommandBinary) {
						nodeKind = "group"
					}
					child = &Node{Name: level, Kind: nodeKind, Detail: levelDetail(level)}
					nested[key] = child
					parent.Children = append(parent.Children, child)
				}
				parent = child
			}
		}

		leafName := leafNameFor(kind, c)
		leaf := &Node{
			Name: leafName, Kind: "item",
			Tokens: c.Tokens, Carry: it.CarryEIT, CarryUncached: it.CarryUncachedEIT,
			Bytes: c.ObservedBytes(), Items: 1,
			Detail:     leafDetail(c, it),
			tokenCalls: c.Tokens * float64(it.ResidentFor),
		}
		if c.Tokens > 0 {
			leaf.CarryPerToken = it.CarryEIT / c.Tokens
			leaf.RoundTrips = float64(it.ResidentFor)
		}
		parent.Children = append(parent.Children, leaf)
	}

	for _, g := range order {
		collapseLeaves(g)
		if g.Name == "file content" {
			nestByDirectory(g)
		}
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

// collapseEmptyLevels removes a level you click through to learn nothing.
//
// Two kinds of them. A tool-identity group that turned out to contain one
// tool: the groups are worth having when they group -- "standard tools"
// holding seven binaries saves a reader from scanning seven rows -- but a
// "version control" holding nothing but git teaches a word you already knew,
// and it pushes the drill-down that matters, git then git status, one click
// further away. The taxonomy stays fixed and industry-wide; whether a given
// session exercised enough of a group for it to be worth a level is a
// property of that session, and this is where that is decided.
//
// And a node whose only child repeats its name, which is how "git add"
// containing one leaf called "git add" happened. That is not a hierarchy, it
// is the same row twice.
func collapseEmptyLevels(n *Node) {
	for i, c := range n.Children {
		collapseEmptyLevels(c)
		for len(c.Children) == 1 {
			only := c.Children[0]
			group := c.Kind == "group"
			// The child keeps its own name: it is the tool, and the level
			// above contributed nothing but a heading.
			if !group && only.Name != c.Name {
				break
			}
			if only.Detail == "" {
				only.Detail = c.Detail
			}
			c = only
			n.Children[i] = c
		}
	}
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
	n.tokenCalls = 0
	for _, child := range n.Children {
		rollUp(child)
		n.Tokens += child.Tokens
		n.Carry += child.Carry
		n.CarryUncached += child.CarryUncached
		n.Bytes += child.Bytes
		n.Items += child.Items
		n.tokenCalls += child.tokenCalls
	}
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
