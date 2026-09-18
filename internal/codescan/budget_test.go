package codescan

import (
	"strings"
	"testing"
)

func TestNoBudgetIsNoBreach(t *testing.T) {
	// Every limit is off by default. A budget nobody chose is a budget that
	// gets raised rather than met, so the zero value must not invent one.
	r := Report{Files: []FileMetrics{{
		Path: "huge.go", Lines: 100000, CodeLines: 90000,
		Functions: []FunctionMetrics{{Name: "F", Complexity: 400}},
	}}}
	if got := Check(r, Budget{}); len(got) != 0 {
		t.Errorf("an empty budget must pass anything, got %v", got)
	}
}

func TestFileLengthBudgetReportsTheWorstFirst(t *testing.T) {
	r := Report{Files: []FileMetrics{
		{Path: "medium.go", Lines: 120},
		{Path: "ok.go", Lines: 40},
		{Path: "worst.go", Lines: 900},
	}}
	got := Check(r, Budget{MaxFileLines: 100})
	if len(got) != 2 {
		t.Fatalf("expected two breaches, got %v", got)
	}
	if !strings.Contains(got[0].Detail, "worst.go") {
		t.Errorf("the worst offender should come first, got %q", got[0].Detail)
	}
	// The message has to name the file and both numbers, or it is not
	// actionable from a CI log.
	for _, want := range []string{"worst.go", "900", "100"} {
		if !strings.Contains(got[0].Detail, want) {
			t.Errorf("the message is missing %q: %q", want, got[0].Detail)
		}
	}
}

func TestComplexityBudgetIsPerFunctionNotPerFile(t *testing.T) {
	// A file total is mostly a measure of how much is in the file. The worst
	// single function is the one somebody can go and fix.
	r := Report{Files: []FileMetrics{{
		Path: "a.go", Complexity: 500,
		Functions: []FunctionMetrics{
			{Name: "fine", Line: 10, Complexity: 4},
			{Name: "alsoFine", Line: 40, Complexity: 9},
			{Name: "gnarly", Line: 80, Complexity: 30},
		},
	}}}
	got := Check(r, Budget{MaxFunctionComplexity: 20})
	if len(got) != 1 {
		t.Fatalf("only the one function is over, got %v", got)
	}
	for _, want := range []string{"a.go:80", "gnarly", "30", "20"} {
		if !strings.Contains(got[0].Detail, want) {
			t.Errorf("the message is missing %q: %q", want, got[0].Detail)
		}
	}
}

func TestDuplicationBudgetIsAShareAndCountsExtraCopies(t *testing.T) {
	r := Report{
		Files: []FileMetrics{{Path: "a.go", CodeLines: 500}, {Path: "b.go", CodeLines: 500}},
		Duplicates: []Duplicate{{
			// Appears three times: two copies are redundant, not three.
			Lines: 10,
			Occurrences: []Location{
				{Path: "a.go", StartLine: 1},
				{Path: "a.go", StartLine: 100},
				{Path: "b.go", StartLine: 1},
			},
		}},
	}
	dup, code := duplicationShare(r)
	if dup != 20 {
		t.Errorf("three occurrences of a 10-line block is 20 duplicated lines, got %d", dup)
	}
	if code != 1000 {
		t.Errorf("expected 1000 code lines, got %d", code)
	}
	// 2% of the codebase.
	if got := Check(r, Budget{MaxDuplicationPercent: 3}); len(got) != 0 {
		t.Errorf("2%% is under a 3%% limit, got %v", got)
	}
	got := Check(r, Budget{MaxDuplicationPercent: 1})
	if len(got) != 1 {
		t.Fatalf("2%% is over a 1%% limit, got %v", got)
	}
	if !strings.Contains(got[0].Detail, "2.0%") {
		t.Errorf("the message should give the measured share: %q", got[0].Detail)
	}
}

func TestDuplicationSkipsExcludedPathsOnBothSidesOfTheRatio(t *testing.T) {
	// Table-driven tests repeat their own shape by design. Excluding them
	// from the numerator but not the denominator would understate the share,
	// which is the subtler way to get this wrong.
	r := Report{
		DuplicatesSkippedIn: []string{"_test.go"},
		Files: []FileMetrics{
			{Path: "a.go", CodeLines: 100},
			{Path: "a_test.go", CodeLines: 900},
		},
		Duplicates: []Duplicate{{
			Lines: 50,
			Occurrences: []Location{
				{Path: "a_test.go", StartLine: 1},
				{Path: "a_test.go", StartLine: 200},
			},
		}, {
			Lines: 4,
			Occurrences: []Location{
				{Path: "a.go", StartLine: 1},
				{Path: "a.go", StartLine: 50},
			},
		}},
	}
	dup, code := duplicationShare(r)
	if dup != 4 {
		t.Errorf("only the non-test duplication counts, got %d", dup)
	}
	if code != 100 {
		t.Errorf("the denominator must exclude the same files, got %d", code)
	}
	// 4 of 100 is 4%: over a 3% limit, even though it is well under 3% of
	// everything including the tests.
	if got := Check(r, Budget{MaxDuplicationPercent: 3}); len(got) != 1 {
		t.Errorf("expected the non-test duplication to breach, got %v", got)
	}
}

func TestDuplicationIgnoresARunThatOnlySurvivesInSkippedFiles(t *testing.T) {
	r := Report{
		DuplicatesSkippedIn: []string{"_test.go"},
		Files:               []FileMetrics{{Path: "a.go", CodeLines: 100}},
		Duplicates: []Duplicate{{
			Lines: 40,
			Occurrences: []Location{
				{Path: "a.go", StartLine: 1},
				{Path: "a_test.go", StartLine: 1},
			},
		}},
	}
	// One occurrence left after the exclusion, so nothing is duplicated: a
	// block appearing once is not a copy of anything.
	if dup, _ := duplicationShare(r); dup != 0 {
		t.Errorf("a single surviving occurrence is not duplication, got %d", dup)
	}
}

func TestBreachReadsAsOneLine(t *testing.T) {
	b := Breach{Limit: "file too long", Detail: "x.go is 900 lines, over the 100-line limit"}
	if got := b.String(); !strings.HasPrefix(got, "file too long: ") {
		t.Errorf("a breach must print as limit then detail, got %q", got)
	}
}
