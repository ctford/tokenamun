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
