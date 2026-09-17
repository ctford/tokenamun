package codescan

import (
	"fmt"
	"sort"
	"strings"
)

// Budget is a set of limits a scan is allowed to breach.
//
// It turns the scan from a report you read into a check that fails, which is
// the only form of a code-quality measurement that survives contact with a
// deadline. Every limit is off by default: a budget nobody chose is a budget
// that gets raised rather than met.
//
// The numbers belong in the caller, next to the reason for them, not here.
// This package measures; what counts as too much is a judgement about a
// particular codebase.
type Budget struct {
	// MaxFileLines fails a file longer than this.
	MaxFileLines int
	// MaxFunctionComplexity fails a single function above this cyclomatic
	// complexity. The worst function is more actionable than a file total,
	// which is mostly a measure of how much is in the file.
	MaxFunctionComplexity int
	// MaxDuplicationPercent fails when duplicated lines exceed this share of
	// code lines. A share rather than a count, so the limit does not tighten
	// every time the codebase grows.
	MaxDuplicationPercent float64
	// SkipDuplicatesIn drops paths containing any of these substrings from
	// the duplication measurement. Table-driven tests repeat their own shape
	// by design, and counting that as duplication trains people to ignore the
	// number.
	SkipDuplicatesIn []string
}

// Breach is one limit that was exceeded, phrased so that the message says what
// to do about it.
type Breach struct {
	Limit   string
	Detail  string
	Measure float64
	Allowed float64
}

func (b Breach) String() string {
	return fmt.Sprintf("%s: %s", b.Limit, b.Detail)
}

// Check measures a report against a budget, worst breach first.
func Check(r Report, b Budget) []Breach {
	var out []Breach

	if b.MaxFileLines > 0 {
		var over []FileMetrics
		for _, f := range r.Files {
			if f.Lines > b.MaxFileLines {
				over = append(over, f)
			}
		}
		sort.SliceStable(over, func(i, j int) bool { return over[i].Lines > over[j].Lines })
		for _, f := range over {
			out = append(out, Breach{
				Limit: "file too long",
				Detail: fmt.Sprintf("%s is %d lines, over the %d-line limit",
					f.Path, f.Lines, b.MaxFileLines),
				Measure: float64(f.Lines), Allowed: float64(b.MaxFileLines),
			})
		}
	}

	if b.MaxFunctionComplexity > 0 {
		type worst struct {
			path string
			fn   FunctionMetrics
		}
		var over []worst
		for _, f := range r.Files {
			for _, fn := range f.Functions {
				if fn.Complexity > b.MaxFunctionComplexity {
					over = append(over, worst{f.Path, fn})
				}
			}
		}
		sort.SliceStable(over, func(i, j int) bool {
			return over[i].fn.Complexity > over[j].fn.Complexity
		})
		for _, w := range over {
			out = append(out, Breach{
				Limit: "function too complex",
				Detail: fmt.Sprintf("%s:%d %s has complexity %d, over the limit of %d",
					w.path, w.fn.Line, w.fn.Name, w.fn.Complexity, b.MaxFunctionComplexity),
				Measure: float64(w.fn.Complexity), Allowed: float64(b.MaxFunctionComplexity),
			})
		}
	}

	if b.MaxDuplicationPercent > 0 {
		dup, code := duplicationShare(r, b.SkipDuplicatesIn)
		if code > 0 {
			share := 100 * float64(dup) / float64(code)
			if share > b.MaxDuplicationPercent {
				out = append(out, Breach{
					Limit: "too much duplication",
					Detail: fmt.Sprintf("%d of %d code lines are duplicated (%.1f%%), "+
						"over the %.1f%% limit", dup, code, share, b.MaxDuplicationPercent),
					Measure: share, Allowed: b.MaxDuplicationPercent,
				})
			}
		}
	}
	return out
}

// duplicationShare totals duplicated lines and the code lines they are a share
// of, both with the skipped paths excluded so the ratio is over one population.
func duplicationShare(r Report, skip []string) (duplicated, codeLines int) {
	skipped := func(path string) bool {
		for _, frag := range skip {
			if strings.Contains(path, frag) {
				return true
			}
		}
		return false
	}
	for _, f := range r.Files {
		if !skipped(f.Path) {
			codeLines += f.CodeLines
		}
	}
	for _, d := range r.Duplicates {
		// A run counts once per extra copy: two occurrences means one
		// duplicated block, not two.
		var counted int
		for _, occ := range d.Occurrences {
			if !skipped(occ.Path) {
				counted++
			}
		}
		if counted > 1 {
			duplicated += d.Lines * (counted - 1)
		}
	}
	return duplicated, codeLines
}
