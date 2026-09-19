package main

// The session list, end to end through the dispatch layer. Split from
// run_test.go when that file reached its length budget.

import (
	"encoding/json"
	"strings"
	"testing"
)

// Ranking a week by spend was the one thing `sessions` could not do, and the
// workaround for it -- reimplementing the accounting against raw transcripts
// -- gets the deduplication rule wrong first and overstates by most of a
// multiple. The column is the fix.
func TestSessionsReportsWhatEachSessionCost(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	out, err := capture(t, "sessions", "--dir", repo, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Sessions []struct {
			ID      string                   `json:"id"`
			Calls   *struct{ Value float64 } `json:"calls"`
			CostEIT *struct{ Value float64 } `json:"cost_eit"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Sessions) == 0 {
		t.Fatal("no sessions listed")
	}
	for _, s := range doc.Sessions {
		if s.Calls == nil || s.Calls.Value <= 0 {
			t.Errorf("%s: no call count", s.ID)
		}
		if s.CostEIT == nil || s.CostEIT.Value <= 0 {
			t.Errorf("%s: no cost", s.ID)
		}
	}

	// The same figure the session's own profile reports, because two commands
	// disagreeing about one session's cost is worse than neither having it.
	profileOut, err := capture(t, "profile", "--dir", repo, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Usage struct {
			TotalCost struct{ Value float64 } `json:"total_cost"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(profileOut), &p); err != nil {
		t.Fatal(err)
	}
	if doc.Sessions[0].CostEIT.Value != p.Usage.TotalCost.Value {
		t.Errorf("sessions says %.2f and profile says %.2f for the same session",
			doc.Sessions[0].CostEIT.Value, p.Usage.TotalCost.Value)
	}
}

func TestSessionsSortOrderIsCheckedRatherThanIgnored(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	if _, err := capture(t, "sessions", "--dir", repo, "--sort", "cost"); err != nil {
		t.Errorf("--sort cost should be accepted: %v", err)
	}
	// A misspelled sort that silently falls back to the default would rank a
	// week by recency while the caller believed it was ranked by spend.
	_, err := capture(t, "sessions", "--dir", repo, "--sort", "expensive")
	if err == nil {
		t.Fatal("an unknown --sort must be an error")
	}
	if !strings.Contains(err.Error(), "cost") {
		t.Errorf("the error should name the orders that exist: %v", err)
	}
}
