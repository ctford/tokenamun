package analysis

import (
	"testing"

	"github.com/ctford/tokenamun/internal/codescan"
	"github.com/ctford/tokenamun/internal/model"
)

func scanOf(files ...codescan.FileMetrics) codescan.Report {
	return codescan.Report{Root: "/repo", Files: files}
}

func TestHotspotsJoinCodePropertiesToSessionCost(t *testing.T) {
	s := &model.Session{
		Retrievals: []model.RetrievedContent{
			{Path: "internal/payment/charge.go", Tool: "Read", Bytes: 7000, Tokens: 2000, InvocationSeq: 0},
			{Path: "internal/payment/charge.go", Tool: "Read", Bytes: 7000, Tokens: 2000, InvocationSeq: 3},
			{Path: "cmd/main.go", Tool: "Read", Bytes: 500, Tokens: 140, InvocationSeq: 5},
		},
	}
	scan := scanOf(
		codescan.FileMetrics{Path: "internal/payment/charge.go", Language: "go",
			Lines: 480, CodeLines: 400, Complexity: 62, MaxFunction: 21,
			MaxFunctionName: "Charge", ComplexityProv: model.Derived, Large: true},
		codescan.FileMetrics{Path: "cmd/main.go", Language: "go",
			Lines: 40, Complexity: 3, MaxFunction: 3, ComplexityProv: model.Derived},
	)
	carry := CarryReport{Items: []CarriedItem{
		{Path: "internal/payment/charge.go", CarryEIT: 5000},
		{Path: "cmd/main.go", CarryEIT: 200},
		{Path: "", Tool: "Bash", CarryEIT: 9000},
	}}

	r := Hotspots(s, scan, carry)

	if len(r.Hotspots) != 2 {
		t.Fatalf("expected two files, got %d", len(r.Hotspots))
	}
	top := r.Hotspots[0]
	if top.Path != "internal/payment/charge.go" {
		t.Fatalf("ranking should be by carry cost, got %q first", top.Path)
	}
	if top.Retrievals != 2 || top.RetrievedBytes != 14000 {
		t.Errorf("retrieval totals wrong: %+v", top)
	}
	if top.CarryEIT != 5000 {
		t.Errorf("carry = %v, want 5000", top.CarryEIT)
	}
	if !top.Scanned || top.MaxFunction != 21 || top.MaxFunctionName != "Charge" {
		t.Errorf("code properties not joined: %+v", top)
	}
}

func TestUnattributableOutputIsAccountedForRatherThanDropped(t *testing.T) {
	// On auto-mode sessions most output has no path. Leaving it out of the
	// report would make the file rows look like the whole story.
	s := &model.Session{
		Retrievals: []model.RetrievedContent{
			{Path: "", Tool: "Bash", Bytes: 30000, Tokens: 8000, InvocationSeq: 0},
			{Path: "a.go", Tool: "Read", Bytes: 1000, Tokens: 280, InvocationSeq: 1},
		},
	}
	carry := CarryReport{Items: []CarriedItem{
		{Path: "", Tool: "Bash", CarryEIT: 40000},
		{Path: "a.go", CarryEIT: 300},
	}}
	r := Hotspots(s, scanOf(), carry)

	if r.UnmatchedRetrievals != 1 || r.UnmatchedBytes != 30000 {
		t.Errorf("unmatched retrievals not accounted: %+v", r)
	}
	if r.UnmatchedCarryEIT != 40000 {
		t.Errorf("unmatched carry = %v, want 40000", r.UnmatchedCarryEIT)
	}
}

func TestAbsolutePathsMatchTheRelativeScan(t *testing.T) {
	// Retrieval paths come from tool arguments and command lines, so they are
	// often absolute while the scan is relative to its root.
	s := &model.Session{Retrievals: []model.RetrievedContent{
		{Path: "/Users/x/repo/internal/payment/charge.go", Tool: "Read", Bytes: 100, Tokens: 30},
	}}
	scan := scanOf(codescan.FileMetrics{
		Path: "internal/payment/charge.go", Lines: 480, MaxFunction: 21, ComplexityProv: model.Derived})
	r := Hotspots(s, scan, CarryReport{})

	if len(r.Hotspots) != 1 || !r.Hotspots[0].Scanned {
		t.Fatalf("absolute path should match the relative scan entry: %+v", r.Hotspots)
	}
	if r.Hotspots[0].Lines != 480 {
		t.Errorf("lines = %d, want 480", r.Hotspots[0].Lines)
	}
}

func TestSuffixMatchingRespectsPathBoundaries(t *testing.T) {
	// "a/ab.go" must not satisfy a lookup for "b.go".
	s := &model.Session{Retrievals: []model.RetrievedContent{
		{Path: "/repo/a/ab.go", Tool: "Read", Bytes: 10, Tokens: 3},
	}}
	scan := scanOf(codescan.FileMetrics{Path: "b.go", Lines: 99, ComplexityProv: model.Derived})
	r := Hotspots(s, scan, CarryReport{})
	if r.Hotspots[0].Scanned {
		t.Error("ab.go must not match b.go")
	}
}

func TestDuplicatedLinesAreAttributedToEveryOccurrence(t *testing.T) {
	s := &model.Session{Retrievals: []model.RetrievedContent{
		{Path: "a.go", Tool: "Read", Bytes: 10, Tokens: 3},
		{Path: "b.go", Tool: "Read", Bytes: 10, Tokens: 3},
	}}
	scan := codescan.Report{
		Files: []codescan.FileMetrics{
			{Path: "a.go", Lines: 100, ComplexityProv: model.Derived},
			{Path: "b.go", Lines: 100, ComplexityProv: model.Derived},
		},
		Duplicates: []codescan.Duplicate{{
			Lines: 12,
			Occurrences: []codescan.Location{
				{Path: "a.go", StartLine: 10}, {Path: "b.go", StartLine: 40},
			},
		}},
	}
	r := Hotspots(s, scan, CarryReport{})
	for _, h := range r.Hotspots {
		if h.DuplicateLines != 12 {
			t.Errorf("%s: duplicated lines = %d, want 12", h.Path, h.DuplicateLines)
		}
	}
}

func TestRepeatedRetrievalIsCarriedThrough(t *testing.T) {
	s := &model.Session{
		Retrievals: []model.RetrievedContent{
			{Path: "a.go", Tool: "Read", Bytes: 500, Tokens: 140},
			{Path: "a.go", Tool: "Read", Bytes: 500, Tokens: 140},
		},
		Repeats: []model.Repeat{{Path: "a.go", Tool: "Read", Count: 2, Bytes: 500, WasteByte: 500}},
	}
	r := Hotspots(s, scanOf(), CarryReport{})
	if r.Hotspots[0].RedundantBytes != 500 {
		t.Errorf("redundant bytes = %d, want 500", r.Hotspots[0].RedundantBytes)
	}
}
