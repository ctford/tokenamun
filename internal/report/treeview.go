package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/model"
)

// TreeView is one level of the drill-down, as the CLI serves it.
//
// It exists so that an agent can learn everything the HTML viewer shows a
// person. The viewer is a browser and a mouse; this is the same tree, the same
// cost modes, the same two percentages and the same per-node explanations,
// reachable by name. Anything the viewer can display and this cannot is a
// place where the agent has to ask the human what it says, which defeats the
// point of the tool.
type TreeView struct {
	SchemaVersion int         `json:"schema_version"`
	Session       SessionInfo `json:"session"`
	// Mode is which pricing the costs are in: as billed, or as though nothing
	// cached. The same toggle the viewer has.
	Mode string `json:"mode"`
	// Path is where in the tree this level is, by name, and it is also how to
	// get back: pass it to --at.
	Path []string `json:"path"`
	Here TreeNode `json:"here"`
	// Children are the nodes at this level. Empty at a leaf.
	Children []TreeNode `json:"children"`
	// Total is the whole session's cost in this mode, the denominator for
	// ShareOfSession.
	Total float64 `json:"session_total"`
	// Reconciliation is how far the parts overshoot the measured prompt cost,
	// through byte-per-token estimation error. On the root only.
	Reconciliation float64 `json:"reconciliation,omitempty"`
	// Drill lists the exact commands to go one level further, because an
	// affordance an agent has to guess at is not an affordance.
	Drill []string        `json:"drill_in,omitempty"`
	Notes []string        `json:"notes"`
	Warns []model.Warning `json:"warnings,omitempty"`
}

// TreeNode is one node, flattened: no children, so a level is a list rather
// than a document to walk.
type TreeNode struct {
	Name string  `json:"name"`
	Kind string  `json:"kind"`
	Cost float64 `json:"cost"`
	// ShareOfLevel and ShareOfSession are the viewer's two percentages. Both,
	// because either alone misleads: 88% of a branch that is 3% of the session
	// is not something to go and fix.
	ShareOfLevel   float64 `json:"share_of_level"`
	ShareOfSession float64 `json:"share_of_session"`
	Tokens         float64 `json:"tokens"`
	Bytes          int     `json:"bytes"`
	Items          int     `json:"retrievals"`
	// RoundTrips is how many calls the content sat through, averaged over its
	// tokens: how much of the back and forth it was part of. It is the
	// viewer's colour ramp, as a number.
	//
	// There used to be a cost-per-token ratio here as well. It was a price
	// divided by a size, it needed a paragraph to explain, and a reader who
	// wants it has cost and tokens right beside it. This is the quantity that
	// causes it and it explains itself.
	RoundTrips float64 `json:"round_trips,omitempty"`
	// Unscaled marks a node with cost but no attributable token count, so
	// neither rate is defined rather than being low. Grey in the viewer.
	Unscaled bool `json:"unscaled,omitempty"`
	// Children is how many nodes are inside, so a caller knows whether there
	// is anything to drill into.
	Children int `json:"children"`
	// Detail is what the viewer puts in the tooltip: what this node means.
	Detail string `json:"detail,omitempty"`
	// DetailMore is the argument behind it. Only the node you are on carries
	// it, because it is a paragraph and a level has up to forty rows.
	DetailMore string `json:"detail_more,omitempty"`
	// At is the value to pass to --at to go here.
	At string `json:"at,omitempty"`
}

// Tree cost modes, matching the viewer's two buttons.
const (
	ModeCarry    = "carry"
	ModeUncached = "uncached"
)

// BuildTreeView resolves a path into the tree and flattens that one level.
//
// One level at a time, like the viewer: a whole hierarchy dumped at once is
// what the treemap replaced, and it is no more readable as text than it was
// as nested rectangles.
func BuildTreeView(s *model.Session, carry analysis.CarryReport, at []string, mode string) (TreeView, error) {
	if mode == "" {
		mode = ModeCarry
	}
	if mode != ModeCarry && mode != ModeUncached {
		return TreeView{}, fmt.Errorf("unknown mode %q; use %q or %q",
			mode, ModeCarry, ModeUncached)
	}
	return BuildTreeViewFrom(BuildTree(s, carry), sessionInfo(s), at, mode)
}

// BuildTreeViewFrom serves a level of a tree that is already built.
//
// Split out so a team's whole history can be viewed the same way one session
// is: `all` merges every session's tree and hands it here, and every level,
// percentage and drill-in works unchanged. Profiling one session is the
// special case, not the shape of the thing.
func BuildTreeViewFrom(root *Node, info SessionInfo, at []string, mode string) (
	TreeView, error) {
	if mode == "" {
		mode = ModeCarry
	}
	if mode != ModeCarry && mode != ModeUncached {
		return TreeView{}, fmt.Errorf("unknown mode %q; use %q or %q",
			mode, ModeCarry, ModeUncached)
	}
	here, path, err := resolve(root, at)
	if err != nil {
		return TreeView{}, err
	}

	v := TreeView{
		SchemaVersion: SchemaVersion,
		Session:       info,
		Mode:          mode,
		Path:          path,
		Total:         costOf(root, mode),
	}
	if len(path) == 0 {
		v.Reconciliation = root.Reconciliation
	}
	v.Here = flatten(here, costOf(here, mode), v.Total, mode, path)
	v.Here.DetailMore = here.DetailMore

	for _, c := range here.Children {
		child := flatten(c, costOf(here, mode), v.Total, mode, append(path, c.Name))
		v.Children = append(v.Children, child)
		if len(c.Children) > 0 {
			v.Drill = append(v.Drill,
				fmt.Sprintf("tokenamun tree %s --at %q", selectorOf(info), child.At))
		}
	}

	v.Notes = []string{
		"Costs are cost-weighted tokens: every token class on one scale where 1 is a " +
			"full-price input token of this model. Model-relative, so a mixed-model " +
			"session still adds up.",
		"share_of_level is of this level; share_of_session is of the whole session.",
		"round_trips is the number of calls the content was sent on. The model has no " +
			"memory between calls, so anything still in the context is sent again on " +
			"every call and billed each time. Absent where there is no token count to " +
			"weight by.",
		"On a leaf, round_trips is that retrieval's own residency, observed. On a " +
			"branch it is a token-weighted average of the leaves inside, so a branch " +
			"never reads higher than the worst leaf in it.",
		"Caching does not change round_trips, only what each trip cost: a tenth of " +
			"input price when the prefix was warm, the write rate when it had to be " +
			"rebuilt. So --mode moves the cost and never the round trips. A run ends at " +
			"a context reset, which is why the most here is fewer than the session's calls.",
		"This is not a picture of the context window at any moment. It is what each " +
			"token class was billed at, attributed to the content resident when it was sent.",
	}
	if mode == ModeUncached {
		v.Notes = append(v.Notes,
			"This level is priced as though nothing cached: the same content, re-sent the "+
				"same number of times, at full input price. Compare with --mode carry to see "+
				"what prompt caching was worth here.")
	}
	return v, nil
}

// resolve walks a path of names, matching case-insensitively so that an agent
// reading a name out of a previous level does not have to reproduce it
// exactly.
func resolve(root *Node, at []string) (*Node, []string, error) {
	n := root
	var path []string
	for _, want := range at {
		if want == "" {
			continue
		}
		var next *Node
		for _, c := range n.Children {
			if strings.EqualFold(c.Name, want) {
				next = c
				break
			}
		}
		if next == nil {
			var available []string
			for _, c := range n.Children {
				available = append(available, c.Name)
			}
			if len(available) == 0 {
				return nil, nil, fmt.Errorf("%q is a leaf: there is nothing inside it",
					strings.Join(path, "/"))
			}
			return nil, nil, fmt.Errorf("no %q inside %q; it contains: %s",
				want, pathOrRoot(path), strings.Join(available, ", "))
		}
		n = next
		path = append(path, n.Name)
	}
	return n, path, nil
}

// selectorOf is what to pass back to the tool to get this scope again.
//
// Falls back to the ID, which is a valid selector for a single session and is
// all a caller set before merged trees existed.
func selectorOf(info SessionInfo) string {
	if info.Selector != "" {
		return info.Selector
	}
	return info.ID
}

func pathOrRoot(path []string) string {
	if len(path) == 0 {
		return "everything"
	}
	return strings.Join(path, "/")
}

func costOf(n *Node, mode string) float64 {
	if mode == ModeUncached {
		return n.CarryUncached
	}
	return n.Carry
}

func flatten(n *Node, levelTotal, sessionTotal float64, mode string, path []string) TreeNode {
	cost := costOf(n, mode)
	out := TreeNode{
		Name: n.Name, Kind: n.Kind, Cost: cost,
		Tokens: n.Tokens, Bytes: n.Bytes, Items: n.Items,
		Unscaled: n.Unscaled, Children: len(n.Children), Detail: n.Detail,
		At: strings.Join(path, "/"),
	}
	if !n.Unscaled && n.Tokens > 0 {
		out.RoundTrips = n.RoundTrips
	}
	if levelTotal > 0 {
		out.ShareOfLevel = cost / levelTotal
	}
	if sessionTotal > 0 {
		out.ShareOfSession = cost / sessionTotal
	}
	return out
}

// RenderTreeView writes the level as a table, in the same order and with the
// same columns as the viewer's table view.
func RenderTreeView(w io.Writer, v TreeView) error {
	b := &strings.Builder{}
	b.WriteString("TOKENAMUN  where the tokens went\n\n")

	fmt.Fprintf(b, "Session %s, %s API calls\n", v.Session.ID, num(v.Session.Calls))
	pricing := "as billed, with caching"
	if v.Mode == ModeUncached {
		pricing = "as though nothing cached"
	}
	fmt.Fprintf(b, "Priced %s\n\n", pricing)
	// The money total, when it was asked for. The figures below it stay in
	// EIT: a node is a share of content, and attributing content cost to the
	// call -- and so to the model -- that carried it is not something this
	// tree does yet. A per-node dollar figure would have to pick one model
	// for the whole tree, which is the error the money total exists to avoid.
	prices(b, v.Session.Prices)

	fmt.Fprintf(b, "At %s\n", pathOrRoot(v.Path))
	fmt.Fprintf(b, "  Cost               %14s   (%s of session)\n",
		num(int(v.Here.Cost)), pctStr(v.Here.ShareOfSession))
	if v.Here.Items > 0 {
		fmt.Fprintf(b, "  Retrievals         %14s\n", num(v.Here.Items))
	}
	if v.Here.Detail != "" {
		fmt.Fprintf(b, "\n%s\n", wrap(v.Here.Detail, 74, ""))
	}
	if v.Here.DetailMore != "" {
		fmt.Fprintf(b, "\n%s\n", wrap(v.Here.DetailMore, 74, ""))
	}
	b.WriteString("\n")

	if len(v.Children) == 0 {
		b.WriteString("Nothing inside: this is a leaf.\n\n")
	} else {
		fmt.Fprintf(b, "%-38s %8s %9s %13s %7s  %s\n",
			"INSIDE", "OF LEVEL", "SESSION", "COST", "TRIPS", "CONTAINS")
		for _, c := range v.Children {
			// Round trips, not cost per token: it is the cause rather than
			// the ratio, and it needs no explaining.
			trips := "   --"
			if c.RoundTrips > 0 {
				trips = num(int(c.RoundTrips))
			}
			contains := fmt.Sprintf("%s retrievals", num(c.Items))
			if c.Children > 0 {
				contains = fmt.Sprintf("%s inside", num(c.Children))
			} else if c.Items == 1 {
				contains = "1 retrieval"
			}
			fmt.Fprintf(b, "%-38s %8s %9s %13s %7s  %s\n",
				trunc(c.Name, 38), pctStr(c.ShareOfLevel), pctStr(c.ShareOfSession),
				num(int(c.Cost)), trips, contains)
		}
		b.WriteString("\n")
	}

	if v.Reconciliation != 0 && v.Total > 0 {
		fmt.Fprintf(b, "The parts come to %s more than the measured prompt cost. That gap is\n"+
			"byte-per-token estimation error, not a finding.\n\n",
			pctStr(-v.Reconciliation/v.Total))
	}

	if len(v.Drill) > 0 {
		b.WriteString("Drill in with:\n")
		for i, d := range v.Drill {
			if i >= 5 {
				fmt.Fprintf(b, "  ... and %d more; --at takes any name from the table above\n",
					len(v.Drill)-i)
				break
			}
			fmt.Fprintf(b, "  %s\n", d)
		}
		b.WriteString("\n")
	}

	for _, n := range v.Notes {
		fmt.Fprintf(b, "%s\n", wrap(n, 74, ""))
	}
	b.WriteString("\n")

	_, err := io.WriteString(w, b.String())
	return err
}

// pctStr formats a share the way the viewer does: a whole number when it is
// big enough that a decimal is noise, one decimal when it is not.
//
// Switched on magnitude, because a saving is negative and the first version of
// this compared a signed value against its thresholds -- which reported every
// intervention on the summary table as "<0.1%".
func pctStr(r float64) string {
	v := 100 * r
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	switch {
	case v == 0:
		return "0%"
	case v >= 10:
		return fmt.Sprintf("%s%.0f%%", sign, v)
	case v >= 0.1:
		return fmt.Sprintf("%s%.1f%%", sign, v)
	default:
		return sign + "<0.1%"
	}
}

// remainingStr states an intervention's effect as what the thing becomes,
// rather than as a signed change to it.
//
// 50% is halved, 100% is untouched, 110% is worse. A sign in front of a
// percentage in a table reads as an annotation rather than as arithmetic, and
// nobody has to work out which direction "-50%" points. Used for both
// columns, so the table is sign-free and the two read the same way: what the
// addressable part becomes, and what the session becomes.
//
// The JSON keeps the signed fractions. They are the arithmetic, a caller
// composing them wants them signed, and nobody is reading JSON in a hurry.
func remainingStr(reduction float64) string {
	// Precision from the size of the change, not the size of the result. A
	// scale factor clusters near 100%, so pctStr's rule -- whole numbers
	// above 10% -- printed a real 0.2% saving as "100%".
	change := reduction
	if change < 0 {
		change = -change
	}
	remaining := 100 * (1 + reduction)
	switch {
	case change == 0:
		return "100%"
	case change >= 0.1:
		return fmt.Sprintf("%.0f%%", remaining)
	case change >= 0.001:
		return fmt.Sprintf("%.1f%%", remaining)
	default:
		return fmt.Sprintf("%.2f%%", remaining)
	}
}
