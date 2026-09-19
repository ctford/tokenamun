package report

import (
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// SessionList is what `sessions` answers: the transcripts the tool can see,
// and what each one cost.
//
// The cost is here because the alternative is worse than a slow command.
// Without it, ranking a week by spend means reimplementing the accounting
// against raw transcripts, and the first thing a reimplementation gets wrong
// is deduplicating assistant entries by requestId -- the rule in
// docs/METHODOLOGY.md section 2, and the one whose absence overstates by
// 66-97% on the fixtures in this repository. A list you have to leave in
// order to rank is a list that sends people to the transcripts, which is the
// thing this tool exists to stop.
//
// It costs a parse of every transcript, which is why the parse cache came
// first: see internal/parsecache.
type SessionList struct {
	SchemaVersion int          `json:"schema_version"`
	SortedBy      string       `json:"sorted_by"`
	Sessions      []SessionRow `json:"sessions"`
	// Unreadable names the transcripts that would not parse, with why. They
	// stay in Sessions, without figures: a session you cannot account for is
	// still a session that happened, and dropping it from the list would make
	// the set look smaller than it is.
	Unreadable []string `json:"unreadable,omitempty"`
	Notes      []string `json:"notes"`
}

// SessionRow is one discovered session. The ref's own fields are kept flat,
// because they were the whole of this output before there were figures beside
// them and a consumer indexes into them by name.
type SessionRow struct {
	model.SessionRef
	// Calls and CostEIT are absent, not zero, when the transcript would not
	// parse. Zero is a measurement; this is the absence of one.
	Calls   *model.Quantity `json:"calls,omitempty"`
	CostEIT *model.Quantity `json:"cost_eit,omitempty"`
	// MixedPricing marks a session that switched model, where the EIT total
	// adds quantities of different sizes. Comparing two of these rows against
	// each other is the thing that goes wrong quietly.
	MixedPricing bool `json:"mixed_pricing,omitempty"`
}

// Session sort orders.
const (
	// SortRecent is discovery order: the session running this tool, then the
	// most recently active.
	SortRecent = "recent"
	// SortCost ranks by what each session cost, which is the question the
	// list could not answer before.
	SortCost = "cost"
	// SortCalls ranks by API calls, which is volume of turns rather than
	// spend. Kept apart from cost deliberately: they disagree, and which one
	// disagrees with the other is a finding.
	SortCalls = "calls"
)

// SortOrders is every accepted --sort value, for the error message.
var SortOrders = []string{SortRecent, SortCost, SortCalls}

// BuildSessionList prices every discovered session.
//
// load is passed in rather than called directly so that the caller decides
// whether a cached parse is acceptable, and so this stays testable without a
// filesystem.
func BuildSessionList(refs []model.SessionRef, sortBy string,
	load func(model.SessionRef) (*model.Session, error)) (SessionList, error) {
	if sortBy == "" {
		sortBy = SortRecent
	}
	if !slices.Contains(SortOrders, sortBy) {
		return SessionList{}, fmt.Errorf("unknown --sort %q; use one of %s",
			sortBy, strings.Join(SortOrders, ", "))
	}
	l := SessionList{
		SchemaVersion: SchemaVersion,
		SortedBy:      sortBy,
		Notes: []string{
			"Cost is cost-weighted tokens: every token class on one scale where 1 is a " +
				"full-price input token of this session's model. Model-relative, so two " +
				"rows on different models are not strictly comparable and the ones that " +
				"switched say so.",
			"Calls are API requests, deduplicated by requestId. A transcript line is a " +
				"content block, not a call, and counting lines overstates everything.",
			"Subagent spend is not in these totals. `tokenamun profile <session>` reports " +
				"it beside the session's own, because a subagent runs in its own context.",
		},
	}
	for _, ref := range refs {
		row := SessionRow{SessionRef: ref}
		s, err := load(ref)
		if err != nil {
			l.Unreadable = append(l.Unreadable, ref.ID+": "+err.Error())
			l.Sessions = append(l.Sessions, row)
			continue
		}
		p := BuildProfile(s)
		calls := model.Obs(float64(s.RealCalls()), model.Calls)
		cost := p.Usage.TotalCost
		row.Calls, row.CostEIT = &calls, &cost
		row.MixedPricing = p.Session.MixedPricing
		l.Sessions = append(l.Sessions, row)
	}
	sortSessions(l.Sessions, sortBy)
	return l, nil
}

// sortSessions orders the rows. Stable, and recent order is whatever
// discovery already produced, so the default output is unchanged.
func sortSessions(rows []SessionRow, by string) {
	value := func(r SessionRow) float64 {
		switch {
		case by == SortCost && r.CostEIT != nil:
			return r.CostEIT.Value
		case by == SortCalls && r.Calls != nil:
			return r.Calls.Value
		}
		// A session with no figure sorts last rather than as zero: it is
		// unknown, and the bottom of a ranking is where an unknown misleads
		// least.
		return -1
	}
	if by == SortRecent {
		return
	}
	sort.SliceStable(rows, func(i, j int) bool { return value(rows[i]) > value(rows[j]) })
}

// RenderSessionList writes the list as a table.
func RenderSessionList(w io.Writer, l SessionList) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "%-38s %-8s %-17s %8s %13s  %s\n",
		"SESSION", "SOURCE", "LAST ACTIVE", "CALLS", "COST (EIT)", "")
	for _, r := range l.Sessions {
		marker := ""
		if r.Current {
			marker = "<- this session"
		}
		if r.MixedPricing {
			marker = strings.TrimSpace(marker + " (switched model)")
		}
		fmt.Fprintf(b, "%-38s %-8s %-17s %8s %13s  %s\n",
			trunc(r.ID, 38), r.Origin, r.Modified.Format("2006-01-02 15:04"),
			figure(r.Calls), figure(r.CostEIT), marker)
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "Calls [%s], cost [%s]. Sorted by %s.\n\n",
		model.Observed, model.Derived, l.SortedBy)

	for _, u := range l.Unreadable {
		fmt.Fprintf(b, "! could not parse %s\n", wrap(u, 70, "  "))
	}
	if len(l.Unreadable) > 0 {
		b.WriteString("\n")
	}
	for _, n := range l.Notes {
		fmt.Fprintf(b, "%s\n", wrap(n, 74, ""))
	}
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// figure prints a quantity, or a dash where there is none. A dash rather than
// a zero: the transcript would not parse, and zero is a claim about spend.
func figure(q *model.Quantity) string {
	if q == nil {
		return "--"
	}
	return num(int(q.Value))
}
