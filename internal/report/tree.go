package report

import (
	"fmt"

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
	// P50PerRetrieval, P95PerRetrieval and MaxPerRetrieval are what one
	// retrieval here cost to carry, as billed. Cost and count give a mean,
	// and a mean cannot tell "this command is verbose every time" from "one
	// run of it went berserk" -- which are different findings with different
	// answers. The percentiles are absent below PercentilesNeedAtLeast
	// samples, where they would be noise dressed as precision; the maximum
	// is one observed retrieval and is reported whatever the count.
	P50PerRetrieval float64 `json:"p50PerRetrieval,omitempty"`
	P95PerRetrieval float64 `json:"p95PerRetrieval,omitempty"`
	MaxPerRetrieval float64 `json:"maxPerRetrieval,omitempty"`
	// samples is one CarryEIT per retrieval underneath this node, which is
	// what the three figures above are computed from. Unexported, like
	// tokenCalls: it is the working, not a finding.
	samples []float64
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
	// Detail is what this node is, in a sentence. It goes in the viewer's
	// tooltip, where a paragraph is not read: a box you are hovering over
	// competes with the box itself for your attention.
	Detail string `json:"detail,omitempty"`
	// DetailMore is the argument behind it, for the CLI, which has room.
	// Same split as a caveat and for the same reason: the screen has no
	// space and the terminal does.
	DetailMore string  `json:"-"`
	Children   []*Node `json:"children,omitempty"`
	// Reconciliation is set on the root when the parts overshoot the measured
	// prompt cost, which happens through byte-per-token estimation error.
	Reconciliation float64 `json:"reconciliation,omitempty"`
	// PromptCost is the session's measured prompt cost, on the root only.
	//
	// The root's Carry is prompt cost plus output cost, and that is the right
	// denominator for "how much of the session is this". It is not the
	// denominator `cache` uses, which is prompt cost alone, and a share
	// against one read as a share against the other is a comparison that is
	// wrong without looking wrong. So both are available, and whoever prints
	// a percentage says which it is a percentage of.
	PromptCost float64 `json:"promptCost,omitempty"`
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
//	│  ├─ tool inputs               what it said to tools → by tool
//	│  └─ thinking                  what it said to itself; carry not knowable
//	├─ file content                 the environment answered → by file
//	├─ cli output                   → by command family → by subcommand
//	├─ mcp output                   → by tool
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
	explainInlinePrograms(root)
	rollUp(root)

	// Whatever the parts do not account for has to be present. Without it
	// every percentage in the view is a share of the part we can itemise
	// rather than of the session, which is the bug this block exists to
	// prevent -- and merging the output blocks reintroduced it once already.
	measured := carry.PromptCostEIT + outputCost(s)
	root.PromptCost = carry.PromptCostEIT
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
			Detail:     unattributedDetail(),
			DetailMore: unattributedMore(s, carry, rest),
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
		Detail:     "context read on session start, carried on every call.",
		DetailMore: "Not decomposable: its parts are not in the transcript.",
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
// remainder is apportioned between prose and tool inputs by byte share,
// which is the only split available since the API reports one output number
// per call.
func modelOutputNode(s *model.Session, carry analysis.CarryReport) *Node {
	thinking := carry.ThinkingTokens
	prose, args := apportion(s, s.Usage().Output-thinking)
	// Generation priced per call. This was never observably wrong, because
	// Output is 5.0x for every model published so far, so multiplying the
	// session's tokens by the first model's output rate happened to give the
	// right answer -- correct by coincidence, wrong by construction, and
	// wrong in fact the day a model ships with a different output multiple.
	// Thinking is per-invocation and observed, so it is priced directly; the
	// rest is split between prose and tool inputs by byte share, which is
	// the only split the API's single output figure allows.
	thinkingGen, restGen := generationCost(s)

	// The model writes three kinds of thing: what it says to you, what it
	// says to tools, and what it says to itself.
	n := &Node{Name: "model output", Kind: "bucket",
		Detail: "tokens written by the model into the conversation."}

	// The carried figure covers prose and tool inputs together, so it is
	// apportioned the same way the generation is.
	proseShare := 0.0
	if prose+args > 0 {
		proseShare = prose / (prose + args)
	}

	if prose > 0 {
		// Generation is at the output rate whatever the cache does, so only
		// the re-reading differs between the two modes.
		gen := restGen * proseShare
		held := carry.AssistantCarryEIT * proseShare
		heldUncached := carry.AssistantCarryUncachedEIT * proseShare
		n.Children = append(n.Children, &Node{
			Name: "replies", Kind: "source",
			Tokens: prose, Carry: gen + held, CarryUncached: gen + heldUncached, Items: 1,
			RoundTrips: carry.AssistantRoundTrips,
			tokenCalls: prose * carry.AssistantRoundTrips,
			Detail:     "what the model said to you.",
		})
	}
	if args > 0 {
		gen := restGen * (1 - proseShare)
		held := carry.AssistantCarryEIT * (1 - proseShare)
		heldUncached := carry.AssistantCarryUncachedEIT * (1 - proseShare)
		// "tool inputs" rather than "tool arguments" because it is the wire's
		// own word -- the transcript field is tool_use.input -- and because it
		// pairs with the output branches, making the two sides of one call
		// visible in the names. Not "tool invocations": that names the whole
		// call, and a reader would expect the result to be in it, when the
		// result is under cli output, mcp output or file content.
		argNode := &Node{Name: "tool inputs", Kind: "source",
			Detail: "the commands and patches the model wrote.",
			DetailMore: "What the tools printed back is under cli output, mcp " +
				"output or file content."}
		argNode.Children = byToolArguments(s, carry, gen+held, gen+heldUncached, args)
		n.Children = append(n.Children, argNode)
	}
	if thinking > 0 {
		gen := thinkingGen
		n.Children = append(n.Children, &Node{
			Name: "thinking", Kind: "source", Unscaled: true,
			// Grey, and this is what grey is for: Claude Code records thinking
			// blocks with empty text, so whether they go round again at all is
			// not in the transcript. No round trips, and for the same reason
			// the cost is identical in both modes -- this is generation only,
			// with no residency for caching to discount.
			Tokens: float64(thinking), Carry: gen, CarryUncached: gen, Items: 1,
			Detail: "what the model wrote for itself.",
			DetailMore: "Recorded with empty text, so any re-reading of it is in " +
				"unattributed.",
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
		node := &Node{
			Name: name, Kind: "tool",
			Tokens: tokens,
			Carry:  totalCost * share, CarryUncached: totalCostUncached * share,
			Items:      callsByTool[name],
			RoundTrips: trips,
			tokenCalls: tokens * trips,
			Detail: fmt.Sprintf("%s of arguments over %s.",
				byteStr(b), pluralCalls(callsByTool[name])),
		}
		// A shell call's arguments are a command, so they open up the same way
		// its output does. Without this, Bash arguments are one opaque box --
		// 15% of a team's week on one reference repository -- and the obvious
		// question about it, whether the content is a pattern or different
		// every time, has no answer in the tool. It is a pattern: on that
		// repository 62% of those bytes are inline python3 programs.
		node.Children = byCommandArguments(s, carry, name,
			node.Carry, node.CarryUncached, node.Tokens, b)
		out = append(out, node)
	}
	return out
}

// byCommandArguments splits one tool's arguments by the command they ran.
//
// Only meaningful for a tool that takes a command line, so the split is
// keyed on whether the calls recorded one at all. Apportioned by argument
// bytes within the tool, the same way the tool's own share was apportioned
// out of the model's output.
func byCommandArguments(s *model.Session, carry analysis.CarryReport, tool string,
	toolCost, toolCostUncached, toolTokens float64, toolBytes int) []*Node {
	bytesBy := map[string]int{}
	callsBy := map[string]int{}
	tripBytesBy := map[string]float64{}
	var total int
	for _, tc := range s.ToolCalls {
		if tc.Name != tool || tc.InputBytes == 0 || tc.Command == "" {
			continue
		}
		key := content.CommandBinary(tc.Command)
		if key == "" || !content.LooksLikeCommand(key) {
			key = "unattributed commands"
		}
		bytesBy[key] += tc.InputBytes
		callsBy[key]++
		tripBytesBy[key] += float64(tc.InputBytes) *
			float64(carry.RoundTripsByCall[tc.InvocationSeq])
		total += tc.InputBytes
	}
	// Below this there is nothing to split: the calls carried no command, so
	// the tool is a leaf as it was before.
	if total == 0 || len(bytesBy) < 2 {
		return nil
	}

	var out []*Node
	for name, b := range bytesBy {
		share := float64(b) / float64(total)
		tokens := toolTokens * share
		trips := 0.0
		if b > 0 {
			trips = tripBytesBy[name] / float64(b)
		}
		out = append(out, &Node{
			Name: name, Kind: "command",
			Tokens: tokens,
			Carry:  toolCost * share, CarryUncached: toolCostUncached * share,
			Items:      callsBy[name],
			RoundTrips: trips,
			tokenCalls: tokens * trips,
			Detail: fmt.Sprintf("%s of command text over %s.",
				byteStr(b), pluralCalls(callsBy[name])),
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
			// cli output opens up command by command: git, then git checkout,
			// then git checkout AGENTS.md.
			levels := []string{sub}
			if kind == "cli output" {
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
			samples:    []float64{it.CarryEIT},
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

// generationCost splits what the model's output cost to write into the part
// that is thinking and the part that is everything else, both per call.
//
// Thinking is recorded per invocation, so it needs no apportioning: it is
// summed at each call's own output rate. What remains is prose and tool
// inputs together, which the API reports as one number per call and which
// the caller splits by byte share.
func generationCost(s *model.Session) (thinking, rest float64) {
	for _, inv := range s.Invocations {
		out := cost.For(inv.Model).Output
		thinking += float64(inv.Usage.Thinking) * out
		rest += float64(inv.Usage.Output-inv.Usage.Thinking) * out
	}
	return thinking, rest
}

func outputCost(s *model.Session) float64 {
	// Per call, so a session that switched model is priced with the weights
	// that actually applied to each call rather than to its first.
	_, out := cost.SessionCost(s.Invocations)
	return out
}

// apportion splits non-thinking output between prose and tool inputs by
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
