package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// The "all" selector, and what it means for a report to cover a set rather
// than a session. Split out of run_test.go when that file hit the
// file-length budget: these are about composition -- what adds up across
// sessions and what stops meaning anything -- where the rest are about the
// command line.

func TestAllIsOfferedOnlyWhereItWorks(t *testing.T) {
	// The help text offered "all" as a general selector and four commands
	// answered "no session matches \"all\"", which reads as a typo rather
	// than as a command that does not take it.
	repo := localFixture(t, "carry.jsonl")

	// Where a set composes, it works. Cost is additive, so these do --
	// including carry, whose totals are sums over invocations and whose item
	// ranking is priced per call, so rows from different sessions are
	// comparable once each says which session it came from.
	for _, cmd := range []string{"tree", "profile", "cache", "carry"} {
		if _, err := capture(t, cmd, "all", "--dir", repo); err != nil {
			t.Errorf("%s all: %v", cmd, err)
		}
	}

	// Where it does not, the error says what to use instead. retrieval
	// reports redundant re-retrieval within one context and calibrates its
	// own bytes-per-token; hotspots joins per-file detail onto a checkout.
	// Neither survives two sessions being in one list.
	for _, cmd := range []string{"retrieval", "hotspots"} {
		_, err := capture(t, cmd, "all", "--dir", repo)
		if err == nil {
			t.Errorf("%s all should be refused", cmd)
			continue
		}
		if !strings.Contains(err.Error(), "tokenamun tree all") {
			t.Errorf("%s all must name a command that does take a set: %v", cmd, err)
		}
	}
}

// The refusal that sent a reader to the transcripts: carry answers the
// question people bring to this tool -- which retrieval cost the most to keep
// -- and answered it only about one session.
func TestCarryOverASetKeepsTheSessionsTotalsAndNamesTheSession(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	one, err := capture(t, "carry", "latest", "--dir", repo, "--json")
	if err != nil {
		t.Fatal(err)
	}
	all, err := capture(t, "carry", "all", "--dir", repo, "--json")
	if err != nil {
		t.Fatal(err)
	}

	type carryJSON struct {
		Sessions int `json:"sessions"`
		Context  struct {
			PromptCost struct{ Value float64 }  `json:"prompt_cost"`
			Final      *struct{ Value float64 } `json:"final_prompt_tokens"`
		} `json:"context"`
		Preamble struct {
			Carry struct{ Value float64 } `json:"carry"`
		} `json:"preamble"`
		Items []struct {
			Session   string                  `json:"session"`
			CarryCost struct{ Value float64 } `json:"carry_cost"`
		} `json:"items"`
	}
	var single, merged carryJSON
	if err := json.Unmarshal([]byte(one), &single); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(all), &merged); err != nil {
		t.Fatal(err)
	}

	// One session in the fixture, so the set equals the session. Anything
	// else means the merge is dropping or double-counting.
	if merged.Context.PromptCost.Value != single.Context.PromptCost.Value {
		t.Errorf("prompt cost over a set of one = %v, want %v",
			merged.Context.PromptCost.Value, single.Context.PromptCost.Value)
	}
	if merged.Preamble.Carry.Value != single.Preamble.Carry.Value {
		t.Errorf("preamble carry over a set of one = %v, want %v",
			merged.Preamble.Carry.Value, single.Preamble.Carry.Value)
	}
	if len(merged.Items) != len(single.Items) || len(merged.Items) == 0 {
		t.Fatalf("the ranking has %d rows over a set of one, want %d",
			len(merged.Items), len(single.Items))
	}
	for i := range merged.Items {
		if merged.Items[i].CarryCost.Value != single.Items[i].CarryCost.Value {
			t.Errorf("row %d costs %v over a set and %v alone", i,
				merged.Items[i].CarryCost.Value, single.Items[i].CarryCost.Value)
		}
		// A row nobody can trace back to a session is not actionable.
		if merged.Items[i].Session == "" {
			t.Errorf("row %d over a set does not say which session it is from", i)
		}
		if single.Items[i].Session != "" {
			t.Errorf("row %d repeats the session id the header already gives", i)
		}
	}
	if merged.Context.Final != nil {
		t.Error("a set of sessions has no final prompt")
	}
}

func TestProfileOverASetSumsRatherThanAverages(t *testing.T) {
	// A merged profile is built from finished per-session profiles, not from
	// one synthetic session: residency does not compose across sessions, and
	// a concatenation would look like a single context to everything
	// downstream. What must hold is that the observed totals add up.
	repo := localFixture(t, "carry.jsonl")
	one, err := capture(t, "profile", "latest", "--dir", repo, "--json")
	if err != nil {
		t.Fatal(err)
	}
	all, err := capture(t, "profile", "all", "--dir", repo, "--json")
	if err != nil {
		t.Fatal(err)
	}

	var single, merged struct {
		Usage struct {
			CacheRead struct{ Value float64 }
			TotalCost struct{ Value float64 }
		}
	}
	if err := json.Unmarshal([]byte(one), &single); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(all), &merged); err != nil {
		t.Fatal(err)
	}
	// One session in the fixture, so the set equals the session. Anything
	// else means the merge is dropping or double-counting.
	if merged.Usage.CacheRead.Value != single.Usage.CacheRead.Value {
		t.Errorf("cache read over a set of one = %v, want %v",
			merged.Usage.CacheRead.Value, single.Usage.CacheRead.Value)
	}
	if merged.Usage.TotalCost.Value != single.Usage.TotalCost.Value {
		t.Errorf("total cost over a set of one = %v, want %v",
			merged.Usage.TotalCost.Value, single.Usage.TotalCost.Value)
	}
}
