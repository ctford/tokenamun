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
		carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))
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
	}
	rollUp(out)
	sortTree(out)
	return out
}

func mergeInto(dst, src *Node) {
	// Leaves carry the numbers; branches are recomputed by rollUp, so only
	// the leaf totals need adding.
	if len(src.Children) == 0 {
		dst.Tokens += src.Tokens
		dst.Carry += src.Carry
		dst.CarryUncached += src.CarryUncached
		dst.Bytes += src.Bytes
		dst.Items += src.Items
		dst.tokenCalls += src.tokenCalls
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
func childByName(parent, like *Node) *Node {
	for _, c := range parent.Children {
		if c.Name == like.Name && c.Kind == like.Kind {
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
	for _, ref := range refs {
		s, err := load(ref)
		if err != nil {
			failed = append(failed, ref.ID+": "+err.Error())
			continue
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Ref.Modified.Before(out[j].Ref.Modified)
	})
	return out, failed
}
