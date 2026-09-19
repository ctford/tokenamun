package report

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ctford/tokenamun/internal/model"
)

// ofLength builds a session with a given number of real calls, each carrying
// the same usage, so a test controls both axes of the view directly.
func ofLength(id string, calls int, cacheReadPerCall int64) *model.Session {
	when := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	s := &model.Session{Ref: model.SessionRef{ID: id, Origin: model.FromLocal, Modified: when}}
	for i := range calls {
		s.Invocations = append(s.Invocations, model.ModelInvocation{
			Seq: i, RequestID: id + itoa(i), Model: "claude-opus-5", Timestamp: when,
			Usage: model.TokenUsage{Input: 10, CacheRead: cacheReadPerCall, Output: 5},
		})
	}
	return s
}

func lengthsOf(t *testing.T, sessions ...*model.Session) Lengths {
	t.Helper()
	byID := map[string]*model.Session{}
	var refs []model.SessionRef
	for _, s := range sessions {
		byID[s.Ref.ID] = s
		refs = append(refs, s.Ref)
	}
	return BuildLengths(refs, "a window", func(r model.SessionRef) (*model.Session, error) {
		return byID[r.ID], nil
	})
}

func binNamed(l Lengths, calls string) (LengthBin, bool) {
	for _, b := range l.Bins {
		if b.Calls == calls {
			return b, true
		}
	}
	return LengthBin{}, false
}

// The saturation is the finding, and it is only visible if the bands are
// ordered and empty ones do not pad the table out.
func TestLengthBandsAreOrderedAndOnlyThePopulatedOnesAppear(t *testing.T) {
	l := lengthsOf(t,
		ofLength("short", 5, 100),
		ofLength("medium", 50, 100),
		ofLength("long", 500, 100),
	)
	var got []string
	for _, b := range l.Bins {
		got = append(got, b.Calls)
	}
	want := []string{"1-9", "30-99", "300-999"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("bands are %v, want %v", got, want)
	}
	for i := 1; i < len(l.Bins); i++ {
		if l.Bins[i].From <= l.Bins[i-1].From {
			t.Errorf("bands are not ascending: %v then %v", l.Bins[i-1].Calls, l.Bins[i].Calls)
		}
	}
	if l.Sessions.Value != 3 {
		t.Errorf("binned %v sessions, want 3", l.Sessions.Value)
	}
}

func TestEveryCallCountLandsInOneBand(t *testing.T) {
	cases := map[int]string{
		1: "1-9", 9: "1-9", 10: "10-29", 29: "10-29", 30: "30-99", 99: "30-99",
		100: "100-299", 299: "100-299", 300: "300-999", 999: "300-999",
		1000: "1000+", 50000: "1000+",
	}
	for calls, want := range cases {
		if got := lengthBands[bandOf(calls)].label; got != want {
			t.Errorf("%d calls landed in %q, want %q", calls, got, want)
		}
	}
}

// A band's rate is its cost over its calls, and the two single-session
// columns bracket it. Without them a band of three sessions reads as a
// property of that length when it may be one session in it.
func TestABandsRateIsPooledAndBracketedByRealSessions(t *testing.T) {
	// Two sessions of the same length, one carrying ten times the context.
	l := lengthsOf(t, ofLength("thin", 50, 100), ofLength("fat", 50, 1_000))
	b, ok := binNamed(l, "30-99")
	if !ok {
		t.Fatal("no 30-99 band")
	}
	if b.Sessions.Value != 2 || b.APICalls.Value != 100 {
		t.Fatalf("band holds %v sessions and %v calls", b.Sessions.Value, b.APICalls.Value)
	}
	want := b.Cost.Value / b.APICalls.Value
	if b.PerCall.Value != want {
		t.Errorf("per call is %.4f, want cost over calls %.4f", b.PerCall.Value, want)
	}
	if !(b.Lowest.Value < b.PerCall.Value && b.PerCall.Value < b.Highest.Value) {
		t.Errorf("the pooled rate %.2f should sit between %.2f and %.2f",
			b.PerCall.Value, b.Lowest.Value, b.Highest.Value)
	}
}

// The relationship the view exists to show: a call in a longer session costs
// more, because everything still resident is re-sent on it.
func TestALongerSessionsCallsCostMore(t *testing.T) {
	l := lengthsOf(t,
		ofLength("short", 5, 100),
		ofLength("long", 500, 10_000),
	)
	short, _ := binNamed(l, "1-9")
	long, _ := binNamed(l, "300-999")
	if short.PerCall.Value >= long.PerCall.Value {
		t.Errorf("per-call cost should rise with length: %.2f then %.2f",
			short.PerCall.Value, long.PerCall.Value)
	}
}

// A transcript with no calls in it is not a session of length zero, and a
// cost per call over it is a division by nothing.
func TestASessionWithNoCallsIsNotBinned(t *testing.T) {
	l := lengthsOf(t, ofLength("empty", 0, 0), ofLength("real", 5, 100))
	if l.Sessions.Value != 1 {
		t.Errorf("binned %v sessions, want 1", l.Sessions.Value)
	}
	if len(l.Unreadable) != 1 || !strings.Contains(l.Unreadable[0], "no API calls") {
		t.Errorf("the skipped session should be reported: %v", l.Unreadable)
	}
	for _, b := range l.Bins {
		if b.From == 1 && b.Sessions.Value != 1 {
			t.Errorf("the empty session reached a band: %+v", b)
		}
	}
}

func TestAnUnreadableSessionIsNotSilentlyMissingFromTheBands(t *testing.T) {
	refs := []model.SessionRef{{ID: "broken"}, {ID: "fine"}}
	fine := ofLength("fine", 5, 100)
	l := BuildLengths(refs, "a window", func(r model.SessionRef) (*model.Session, error) {
		if r.ID == "fine" {
			return fine, nil
		}
		return nil, errors.New("truncated")
	})
	if l.Sessions.Value != 1 {
		t.Errorf("binned %v sessions, want 1", l.Sessions.Value)
	}
	if len(l.Unreadable) != 1 {
		t.Fatalf("the unreadable session should be named: %v", l.Unreadable)
	}
	var b bytes.Buffer
	if err := RenderLengths(&b, l); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "not binned") {
		t.Error("a session left out of the population must be said out loud")
	}
}

func TestBandsSpanningTwoModelsSayTheirUnitsDiffer(t *testing.T) {
	a := ofLength("opus", 5, 100)
	b := ofLength("other", 5, 100)
	for i := range b.Invocations {
		b.Invocations[i].Model = "claude-haiku-4-5"
	}
	l := lengthsOf(t, a, b)
	if !l.MixedPricing || len(l.Warnings) == 0 {
		t.Error("a set spanning two models must say the EIT totals are not one unit")
	}
}

func TestTheLengthViewLabelsEveryFigureAndRefusesToFitOne(t *testing.T) {
	l := lengthsOf(t, ofLength("a", 5, 100), ofLength("b", 500, 100))
	walkQuantities(t, "length", mustTree(t, l))

	var b bytes.Buffer
	if err := RenderLengths(&b, l); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"PER CALL", "LOWEST", "HIGHEST",
		string(model.Observed), string(model.Derived),
		// The two claims that keep this a measurement: nothing is fitted, and
		// the day-level aggregation it replaces is named as the trap it is.
		"Nothing is fitted", "never by day"} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendered view is missing %q:\n%s", want, out)
		}
	}
}

func TestNoSessionsWithCallsSaysSoRatherThanPrintingAnEmptyTable(t *testing.T) {
	l := lengthsOf(t, ofLength("empty", 0, 0))
	var b bytes.Buffer
	if err := RenderLengths(&b, l); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "nothing to bin") {
		t.Errorf("an empty population should say why:\n%s", b.String())
	}
}
