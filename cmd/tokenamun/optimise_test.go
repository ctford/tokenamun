package main

// The optimise command, end to end through the dispatch layer. Split from
// run_test.go when that file reached its length budget: this half changes
// when the counterfactual does, the other when the CLI's shape does.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOptimiseNeedsAPartAndAReason(t *testing.T) {
	// The whole of what is left of the interventions: name a part of the
	// tree and what it becomes. The part is measured; the figure is yours,
	// and so is the reason it is plausible.
	repo := localFixture(t, "carry.jsonl")
	out, err := capture(t, "optimise", "--dir", repo, "--at", "cli output",
		"--optimise", "0.5", "--name", "some-proxy", "--why", "quieter test runner output")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "some-proxy") {
		t.Errorf("the caller names the hypothetical:\n%s", out)
	}
	if !strings.Contains(out, "quieter test runner output") {
		t.Error("the caller's reason must be printed with the number")
	}
	// Both halves of the answer: what the part is, and what the session
	// becomes.
	for _, want := range []string{"Applies to", "Addressable", "Optimisation", "Impact"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report is missing %q:\n%s", want, out)
		}
	}

	// It must not be possible to get a number without saying what it applies
	// to, what it becomes, or why that is plausible.
	for _, args := range [][]string{
		{"optimise", "--dir", repo, "--optimise", "0.5", "--why", "x."},
		{"optimise", "--dir", repo, "--at", "cli output", "--optimise", "0.5"},
		// Forgetting --optimise is not the same as asking for zero, which is
		// what the flag's own default would otherwise have meant.
		{"optimise", "--dir", repo, "--at", "cli output", "--why", "x."},
		{"optimise", "--dir", repo, "--at", "cli output", "--optimise", "1", "--why", "x."},
		{"optimise", "--dir", repo, "--at", "nowhere", "--optimise", "0.5", "--why", "x."},
	} {
		if _, err := capture(t, args...); err == nil {
			t.Errorf("%v should have been refused", args[2:])
		}
	}
}

func TestOptimiseComposesWithTheTree(t *testing.T) {
	// The addressable figure has to be the node's own cost, or the
	// hypothetical and the picture disagree about the same session.
	repo := localFixture(t, "carry.jsonl")
	var tree struct {
		Total    float64 `json:"session_total"`
		Children []struct {
			Name string  `json:"name"`
			Cost float64 `json:"cost"`
		} `json:"children"`
	}
	treeOut, err := capture(t, "tree", "--dir", repo, "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(treeOut), &tree); err != nil {
		t.Fatal(err)
	}
	var branch string
	var cost float64
	for _, c := range tree.Children {
		if c.Cost > 0 {
			branch, cost = c.Name, c.Cost
			break
		}
	}

	var h struct {
		Applies     string  `json:"applies_to"`
		Addressable float64 `json:"addressable_eit"`
		Share       float64 `json:"addressable_share"`
		Becomes     float64 `json:"becomes"`
		Impact      float64 `json:"impact"`
		Saving      float64 `json:"saving_eit"`
	}
	jsonOut, err := capture(t, "optimise", "--dir", repo, "--at", branch,
		"--optimise", "0.25", "--why", "A guess.", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(jsonOut), &h); err != nil {
		t.Fatal(err)
	}
	if h.Addressable != cost {
		t.Errorf("%s costs %.2f in the tree and %.2f here", branch, cost, h.Addressable)
	}
	// impact = 1 - addressable x (1 - becomes), and the saving agrees.
	if want := cost * (0.25 - 1); h.Saving != want {
		t.Errorf("saving = %.2f, want %.2f", h.Saving, want)
	}
	if want := 1 + h.Saving/tree.Total; h.Impact != want {
		t.Errorf("impact = %.6f, want %.6f", h.Impact, want)
	}
	if h.Share <= 0 || h.Share > 1 {
		t.Errorf("addressable share %.4f is not a share", h.Share)
	}
}

// A real proposal is several changes at once. Three separate runs of this
// command cannot be added up by hand: each reports its impact against the
// untouched session, and the savings only add where the parts are disjoint,
// which is a question about the tree rather than about the caller's intent.
func TestOptimiseTakesSeveralPartsAtOnce(t *testing.T) {
	repo := localFixture(t, "carry.jsonl")
	out, err := capture(t, "optimise", "--dir", repo,
		"--at", "cli output", "--optimise", "0.5", "--why", "quieter test output",
		"--at", "file content", "--optimise", "0", "--why", "read none of it",
		"--json")
	if err != nil {
		t.Fatal(err)
	}
	var h struct {
		Parts []struct {
			Applies string  `json:"applies_to"`
			Caveat  string  `json:"caveat"`
			Becomes float64 `json:"becomes"`
		} `json:"parts"`
		ShareOfPrompt float64 `json:"addressable_share_of_prompt_cost"`
		Share         float64 `json:"addressable_share"`
	}
	if err := json.Unmarshal([]byte(out), &h); err != nil {
		t.Fatal(err)
	}
	if len(h.Parts) != 2 {
		t.Fatalf("expected two parts, got %d", len(h.Parts))
	}
	// The three flags are columns of one table, so the pairing must survive
	// the flag parser: the wrong reason beside the wrong number is a report
	// that looks deliberate and says something nobody meant.
	if h.Parts[0].Caveat != "quieter test output" || h.Parts[0].Becomes != 0.5 {
		t.Errorf("first part mispaired: %+v", h.Parts[0])
	}
	if h.Parts[1].Caveat != "read none of it" || h.Parts[1].Becomes != 0 {
		t.Errorf("second part mispaired: %+v", h.Parts[1])
	}
	if h.ShareOfPrompt <= h.Share {
		t.Errorf("both denominators should be reported and differ: %.4f of prompt, %.4f of total",
			h.ShareOfPrompt, h.Share)
	}

	for _, args := range [][]string{
		// A figure without its own reason.
		{"optimise", "--dir", repo, "--at", "cli output", "--optimise", "0.5",
			"--why", "x.", "--at", "file content", "--optimise", "0.5"},
		// One reason for two changes.
		{"optimise", "--dir", repo, "--at", "cli output", "--at", "file content",
			"--optimise", "0.5", "--optimise", "0.5", "--why", "x."},
		// A part inside another part: the same tokens counted twice.
		{"optimise", "--dir", repo, "--at", "cli output", "--optimise", "0.5",
			"--why", "x.", "--at", "cli output", "--optimise", "0.5", "--why", "y."},
	} {
		if _, err := capture(t, args...); err == nil {
			t.Errorf("%v should have been refused", args[2:])
		}
	}
}
