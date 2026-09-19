package report

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ctford/tokenamun/internal/model"
)

// priced builds a session whose cost is controlled by the number of cache
// reads in it, so a test can order sessions by spend without a transcript.
func priced(id string, cacheRead int64, when time.Time) *model.Session {
	return &model.Session{
		Ref: model.SessionRef{ID: id, Origin: model.FromLocal, Modified: when},
		Invocations: []model.ModelInvocation{{
			Seq: 1, RequestID: "r1", Model: "claude-opus-5", Timestamp: when,
			Usage: model.TokenUsage{Input: 10, CacheRead: cacheRead, Output: 5},
		}},
	}
}

func listOf(t *testing.T, sortBy string, sessions ...*model.Session) SessionList {
	t.Helper()
	byID := map[string]*model.Session{}
	var refs []model.SessionRef
	for _, s := range sessions {
		byID[s.Ref.ID] = s
		refs = append(refs, s.Ref)
	}
	l, err := BuildSessionList(refs, sortBy, func(r model.SessionRef) (*model.Session, error) {
		return byID[r.ID], nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func ids(l SessionList) []string {
	var out []string
	for _, r := range l.Sessions {
		out = append(out, r.ID)
	}
	return out
}

func TestSessionsCanBeRankedByCost(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	cheap := priced("cheap", 1_000, now)
	dear := priced("dear", 500_000, now.Add(-time.Hour))

	// Discovery order is by recency, and the expensive session is the older
	// one: the whole point of the sort is that those two orders differ.
	if got := ids(listOf(t, SortRecent, cheap, dear)); got[0] != "cheap" {
		t.Errorf("recent should keep discovery order, got %v", got)
	}
	if got := ids(listOf(t, SortCost, cheap, dear)); got[0] != "dear" {
		t.Errorf("cost should rank the expensive session first, got %v", got)
	}
	if got := ids(listOf(t, "", cheap, dear)); got[0] != "cheap" {
		t.Errorf("no sort should mean discovery order, got %v", got)
	}
}

func TestAnUnknownSortIsRefusedRatherThanIgnored(t *testing.T) {
	_, err := BuildSessionList(nil, "cheapest", func(model.SessionRef) (*model.Session, error) {
		return nil, nil
	})
	if err == nil {
		t.Fatal("an unknown sort must be refused")
	}
	for _, order := range SortOrders {
		if !strings.Contains(err.Error(), order) {
			t.Errorf("the error should name %q as an option: %v", order, err)
		}
	}
}

// A transcript that will not parse is still a session that happened. Dropping
// it would make the week look smaller than it was, and reporting it at zero
// would make it look free.
func TestAnUnreadableSessionIsListedWithoutFigures(t *testing.T) {
	refs := []model.SessionRef{{ID: "broken", Origin: model.FromLocal}}
	l, err := BuildSessionList(refs, SortCost, func(model.SessionRef) (*model.Session, error) {
		return nil, errors.New("truncated at line 4")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Sessions) != 1 {
		t.Fatalf("the session should still be listed, got %d rows", len(l.Sessions))
	}
	if l.Sessions[0].Calls != nil || l.Sessions[0].CostEIT != nil {
		t.Error("an unparsed session must carry no figures at all")
	}
	if len(l.Unreadable) != 1 || !strings.Contains(l.Unreadable[0], "truncated") {
		t.Errorf("the reason should be reported, got %v", l.Unreadable)
	}

	var b bytes.Buffer
	if err := RenderSessionList(&b, l); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), " 0 ") {
		t.Error("a missing cost must not render as zero")
	}
	if !strings.Contains(b.String(), "--") {
		t.Error("a missing figure should read as absent")
	}
}

// A session ranked below the cheapest measured one, rather than above it: an
// unknown at the top of a cost ranking is the reading that misleads.
func TestASessionWithNoCostSortsBelowTheOnesThatHaveOne(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	refs := []model.SessionRef{
		{ID: "broken", Origin: model.FromLocal, Modified: now},
		{ID: "cheap", Origin: model.FromLocal, Modified: now},
	}
	cheap := priced("cheap", 1, now)
	l, err := BuildSessionList(refs, SortCost, func(r model.SessionRef) (*model.Session, error) {
		if r.ID == "cheap" {
			return cheap, nil
		}
		return nil, errors.New("unreadable")
	})
	if err != nil {
		t.Fatal(err)
	}
	if ids(l)[0] != "cheap" {
		t.Errorf("a session with no figure should sort last, got %v", ids(l))
	}
}

func TestTheSessionListLabelsEveryFigureItPrints(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	l := listOf(t, SortCost, priced("a", 1_000, now))
	walkQuantities(t, "sessions", mustTree(t, l))

	var b bytes.Buffer
	if err := RenderSessionList(&b, l); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{string(model.Observed), string(model.Derived), "COST (EIT)"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the rendered list should say %q:\n%s", want, b.String())
		}
	}
}

// dispatcher is a cheap parent that sent expensive work to a subagent: the
// shape --sort cost used to rank by the wrong half of.
func dispatcher(id string, own, sub int64, when time.Time) *model.Session {
	s := priced(id, own, when)
	s.Subagents = []model.SubagentRun{{
		ID: "agent-a",
		Invocations: []model.ModelInvocation{{
			Seq: 1, RequestID: "sa", Model: "claude-opus-5", Timestamp: when,
			Usage: model.TokenUsage{CacheRead: sub},
		}},
	}}
	return s
}

// TestCostRanksAWeekByWhatEachSessionCaused. The parent context is the part
// of a fan-out session that a list can see, and it is not the part that cost
// the money.
func TestCostRanksAWeekByWhatEachSessionCaused(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	solo := priced("solo", 100_000, now)
	fanned := dispatcher("fanned", 1_000, 900_000, now.Add(-time.Hour))

	l := listOf(t, SortCost, solo, fanned)
	if ids(l)[0] != "fanned" {
		t.Errorf("the dispatching session cost the most and sorted %v", ids(l))
	}

	var row SessionRow
	for _, r := range l.Sessions {
		if r.ID == "fanned" {
			row = r
		}
	}
	if row.CostEIT.Value >= row.CombinedCostEIT.Value {
		t.Error("the session's own figure must keep its meaning beside the combined one")
	}
	if got, want := row.CombinedCostEIT.Value,
		row.CostEIT.Value+row.SubagentCostEIT.Value; got != want {
		t.Errorf("combined %.0f is not own plus subagents %.0f", got, want)
	}
}

// TestASessionThatLaunchedNothingReportsZeroRatherThanNothing. Zero is a
// measurement here: the session made no subagent calls. Absent means the
// transcript would not parse, which is a different claim.
func TestASessionThatLaunchedNothingReportsZeroRatherThanNothing(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	l := listOf(t, SortCost, priced("solo", 1_000, now))
	row := l.Sessions[0]
	if row.SubagentCostEIT == nil || row.SubagentCostEIT.Value != 0 {
		t.Errorf("a session with no subagents should report zero, got %v", row.SubagentCostEIT)
	}
	if row.CombinedCostEIT == nil || row.CombinedCostEIT.Value != row.CostEIT.Value {
		t.Error("with nothing launched, the combined total is the session's own")
	}

	var b bytes.Buffer
	if err := RenderSessionList(&b, l); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "WITH SUBAGENTS") {
		t.Error("a week that fanned out nowhere should not grow a column of repeats")
	}
}

// TestTheListSaysWhenACombinedTotalSpansTwoPricings. The row's own figure is
// exact -- the parent never switched model -- and the column the list is
// sorted by is not.
func TestTheListSaysWhenACombinedTotalSpansTwoPricings(t *testing.T) {
	now := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	s := dispatcher("fanned", 1_000, 900_000, now)
	s.Subagents[0].Invocations[0].Model = "claude-fable-5-1"
	l := listOf(t, SortCost, s)
	row := l.Sessions[0]
	if row.MixedPricing {
		t.Error("the parent never switched model")
	}
	if !row.CombinedMixedPricing {
		t.Fatal("the combined total spans two pricings and does not say so")
	}

	var b bytes.Buffer
	if err := RenderSessionList(&b, l); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "WITH SUBAGENTS") {
		t.Error("a week with fan-out in it should show the combined column")
	}
	if !strings.Contains(out, "priced differently") {
		t.Errorf("the row does not say its sort key is unsound:\n%s", out)
	}
}
