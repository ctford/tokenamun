package report

import (
	"sort"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/model"
)

// Period is many sessions summed, for the before-and-after question.
type Period struct {
	SchemaVersion int `json:"schema_version"`
	// Window is the period this covers, as the reader asked for it.
	Window string `json:"window"`
	// Sessions is how many were summed, and Calls their total API calls.
	Sessions int `json:"sessions"`
	Calls    int `json:"api_calls"`
	// Failed records sessions that could not be read, because a total over
	// an unknown number of sessions is not a total.
	Failed []string `json:"unreadable,omitempty"`
	Tree   *Node    `json:"tree"`
	Notes  []string `json:"notes"`
}

// BuildPeriod sums the trees of several sessions.
//
// Summed rather than concatenated, and that distinction is the whole design.
// Cost is additive across sessions, so the totals add up. Residency is not:
// content in one session is not resident during the next, because each
// session has its own context. So each session is analysed on its own and the
// results are added, which keeps every round-trip figure true to the session
// it came from.
//
// The same reason a window filters whole sessions rather than calls. Halves
// of a session do not compose: content that entered before the window is
// carried through it, and there is no honest way to say how much of that cost
// belongs inside.
func BuildPeriod(sessions []*model.Session, window string, failed []string) Period {
	p := Period{
		SchemaVersion: SchemaVersion,
		Window:        window,
		Sessions:      len(sessions),
		Failed:        failed,
		Notes: []string{
			"Sessions are analysed separately and their costs added. Cost is additive; residency is not, because each session has its own context.",
			"A window selects whole sessions by last activity. A session that straddles the boundary is in or out, never split.",
			"Round trips are averaged over tokens across every session, so a long session weighs more than a short one -- which is what you want when the question is where the money went.",
		},
	}

	var trees []*Node
	for _, s := range sessions {
		carry := analysis.Carry(s, analysis.Cache(s))
		trees = append(trees, BuildTree(s, carry))
		p.Calls += s.RealCalls()
	}
	p.Tree = MergeTrees(trees)
	return p
}

// MergeTrees adds trees together, matching nodes by name at each level.
//
// Names are the join key because they are the vocabulary the whole tool is
// built on: "cli output" means the same thing in every session, which is the
// point of a taxonomy that does not vary by repository.
func MergeTrees(trees []*Node) *Node {
	out := &Node{Name: "session", Kind: "root"}
	for _, t := range trees {
		if t == nil {
			continue
		}
		mergeInto(out, t)
		// Summed on the root rather than recomputed, because it is the one
		// figure here that is not a roll-up of leaves: prompt cost is
		// measured per session, and a set's is the sum of theirs.
		out.PromptCost += t.PromptCost
	}
	// Collapsed after merging as well as before, because a shape reconciled
	// during the merge may turn out to have a single same-name child, and
	// because whether a group earns its place is a question about the set
	// being reported rather than about each session in it.
	collapseEmptyLevels(out)
	rollUp(out)
	sortTree(out)
	return out
}

func mergeInto(dst, src *Node) {
	// The same name can arrive in two shapes, so reconcile before adding.
	//
	// collapseEmptyLevels dissolves a node whose only child repeats its
	// name, which is what a command run exactly once becomes: `command:
	// python3` with a single `item: python3` under it collapses to the item.
	// Run several times in another session, it stays a branch with a child
	// per subcommand. Merging those by name *and* kind produced two
	// siblings called python3 at the same level -- and, worse, two nodes
	// with the same --at path, so drilling in resolved to whichever came
	// first. Reconcile to the branch shape; the trailing collapse in
	// MergeTrees puts it back if it turns out to be the only child.
	switch {
	case len(src.Children) == 0 && len(dst.Children) > 0:
		// A collapsed leaf meeting subdivisions: it is the portion of this
		// command that was never subdivided, which is a child of it.
		mergeInto(childByName(dst, src), src)
		return
	case len(src.Children) > 0 && dst.Items > 0:
		// Subdivisions meeting a collapsed leaf: move what dst has
		// accumulated down into a child, so rollUp still reconciles.
		demote(dst)
	}

	// Leaves carry the numbers; branches are recomputed by rollUp, so only
	// the leaf totals need adding.
	if len(src.Children) == 0 {
		dst.Tokens += src.Tokens
		dst.Carry += src.Carry
		dst.CarryUncached += src.CarryUncached
		dst.Bytes += src.Bytes
		dst.Items += src.Items
		dst.tokenCalls += src.tokenCalls
		// One sample per retrieval, kept across the merge: the worst single
		// retrieval of a week is a fact about the week, not about whichever
		// session happened to contain it.
		dst.samples = append(dst.samples, src.samples...)
		// A node is off the ramp only if every session's version of it was.
		dst.Unscaled = dst.Unscaled && src.Unscaled
		if dst.Detail == "" {
			dst.Detail = src.Detail
		}
		if dst.DetailMore == "" {
			dst.DetailMore = src.DetailMore
		}
		return
	}
	for _, sc := range src.Children {
		mergeInto(childByName(dst, sc), sc)
	}
}

// childByName finds or creates the matching child, copying the parts of the
// source node that describe rather than measure.
//
// Matched on name alone. Matching on kind as well split every command that
// one session ran once and another ran repeatedly, because the collapse rule
// gives those two different kinds.
func childByName(parent, like *Node) *Node {
	for _, c := range parent.Children {
		if c.Name == like.Name {
			// The branch shape wins: a node with subdivisions is a branch
			// whatever the session that contributed a bare leaf called it.
			if len(like.Children) > 0 {
				c.Kind = like.Kind
			}
			return c
		}
	}
	child := &Node{
		Name: like.Name, Kind: like.Kind,
		Detail: like.Detail, DetailMore: like.DetailMore,
		// Starts off the ramp so that the first merge decides: a node is
		// scalable if any session could scale it.
		Unscaled: like.Unscaled,
	}
	parent.Children = append(parent.Children, child)
	return child
}

// demote moves a node's own measurements into a child of the same name.
//
// The inverse of the collapse rule, used when a node that arrived as a leaf
// turns out to have subdivisions in another session. Its own fields are
// cleared because rollUp recomputes a branch from its children, so leaving
// them would either be double-counted or silently dropped.
func demote(n *Node) {
	child := &Node{
		Name: n.Name, Kind: "item",
		Detail: n.Detail, DetailMore: n.DetailMore,
		Tokens: n.Tokens, Carry: n.Carry, CarryUncached: n.CarryUncached,
		Bytes: n.Bytes, Items: n.Items, tokenCalls: n.tokenCalls,
		samples:  n.samples,
		Unscaled: n.Unscaled,
	}
	n.Children = append(n.Children, child)
	n.Tokens, n.Carry, n.CarryUncached = 0, 0, 0
	n.Bytes, n.Items, n.tokenCalls = 0, 0, 0
	n.samples = nil
}

// Readable loads every session it can and says which it could not.
//
// A period report that silently skipped a session would be a total over an
// unknown number of things. Errors are collected rather than returned,
// because one unreadable transcript among two hundred is a fact about that
// transcript, not a reason to answer nothing.
func Readable(refs []model.SessionRef, load func(model.SessionRef) (*model.Session, error)) (
	[]*model.Session, []string) {
	var out []*model.Session
	var failed []string
	for i, r := range loadEach(refs, load) {
		if r.Err != nil {
			failed = append(failed, refs[i].ID+": "+r.Err.Error())
			continue
		}
		out = append(out, r.Session)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Ref.Modified.Before(out[j].Ref.Modified)
	})
	return out, failed
}
