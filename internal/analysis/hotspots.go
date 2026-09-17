package analysis

import (
	"sort"

	"github.com/ctford/tokenamun/internal/codescan"
	"github.com/ctford/tokenamun/internal/model"
)

// Hotspot joins what a file cost this session against what the file is like.
//
// The join is the point of the whole analysis: token spend only becomes
// actionable when you can see what it was spent on. It is a correlation and
// nothing more -- a file being complex and a file being expensive to explore
// are two observations, not a cause and an effect -- and the report says so
// rather than leaving the reader to supply the inference.
//
// There is deliberately no developer dimension here, or anywhere.
type Hotspot struct {
	Path string `json:"path"`

	// Code properties, from the working tree as it is now.
	Language        string           `json:"language,omitempty"`
	Lines           int              `json:"lines"`
	CodeLines       int              `json:"code_lines"`
	Complexity      int              `json:"complexity"`
	MaxFunction     int              `json:"max_function_complexity"`
	MaxFunctionName string           `json:"max_function_name,omitempty"`
	ComplexityProv  model.Provenance `json:"complexity_provenance,omitempty"`
	DuplicateLines  int              `json:"duplicated_lines"`
	Large           bool             `json:"large"`
	Scanned         bool             `json:"scanned"`

	// Session properties, from the transcript.
	Retrievals      int     `json:"retrievals"`
	RetrievedBytes  int     `json:"retrieved_bytes"`
	RetrievedTokens float64 `json:"retrieved_tokens"`
	RedundantBytes  int     `json:"redundant_bytes"`
	CarryEIT        float64 `json:"carry_eit"`
}

// HotspotReport ranks files by what they cost to carry.
type HotspotReport struct {
	Hotspots []Hotspot `json:"hotspots"`
	// Matched and Missing count files the scan could and could not speak to.
	// A file retrieved by the session but absent from the tree is the normal
	// consequence of profiling a session that ran on another branch, and it
	// has to be said rather than shown as a blank column.
	Matched int `json:"files_in_scanned_tree"`
	Missing int `json:"files_not_in_scanned_tree"`
	// Unmatched counts retrievals with no path, which on auto-mode sessions is
	// most of them: shell output cannot be attributed to a file.
	UnmatchedRetrievals int     `json:"unmatched_retrievals"`
	UnmatchedBytes      int     `json:"unmatched_bytes"`
	UnmatchedCarryEIT   float64 `json:"unmatched_carry_eit"`
}

// Hotspots joins a code scan against a session's retrieval and carry costs.
func Hotspots(s *model.Session, scan codescan.Report, carry CarryReport) HotspotReport {
	dupLines := map[string]int{}
	for _, d := range scan.Duplicates {
		for _, o := range d.Occurrences {
			dupLines[o.Path] += d.Lines
		}
	}
	metrics := map[string]codescan.FileMetrics{}
	for _, f := range scan.Files {
		metrics[f.Path] = f
	}

	byPath := map[string]*Hotspot{}
	var r HotspotReport

	get := func(path string) *Hotspot {
		h, ok := byPath[path]
		if ok {
			return h
		}
		h = &Hotspot{Path: path}
		if m, found := matchMetrics(metrics, path); found {
			h.Language, h.Lines, h.CodeLines = m.Language, m.Lines, m.CodeLines
			h.Complexity, h.MaxFunction, h.MaxFunctionName = m.Complexity, m.MaxFunction, m.MaxFunctionName
			h.ComplexityProv, h.Large, h.Scanned = m.ComplexityProv, m.Large, true
			h.DuplicateLines = dupLines[m.Path]
		}
		byPath[path] = h
		return h
	}

	for _, c := range s.Retrievals {
		if c.Path == "" {
			r.UnmatchedRetrievals++
			r.UnmatchedBytes += c.Bytes
			continue
		}
		h := get(c.Path)
		h.Retrievals++
		h.RetrievedBytes += c.Bytes
		h.RetrievedTokens += c.Tokens
	}
	for _, rep := range s.Repeats {
		if rep.Path == "" {
			continue
		}
		get(rep.Path).RedundantBytes += rep.WasteByte
	}
	for _, it := range carry.Items {
		if it.Path == "" {
			r.UnmatchedCarryEIT += it.CarryEIT
			continue
		}
		get(it.Path).CarryEIT += it.CarryEIT
	}

	for _, h := range byPath {
		if h.Scanned {
			r.Matched++
		} else {
			r.Missing++
		}
		r.Hotspots = append(r.Hotspots, *h)
	}
	sort.SliceStable(r.Hotspots, func(i, j int) bool {
		if r.Hotspots[i].CarryEIT != r.Hotspots[j].CarryEIT {
			return r.Hotspots[i].CarryEIT > r.Hotspots[j].CarryEIT
		}
		return r.Hotspots[i].Path < r.Hotspots[j].Path
	})
	return r
}

// matchMetrics resolves a retrieval path against the scan.
//
// Retrieval paths come from tool arguments and shell command lines, so they
// may be absolute while the scan is relative to its root. Matching on a path
// suffix handles that without pretending to resolve symlinks or build systems.
func matchMetrics(metrics map[string]codescan.FileMetrics, path string) (codescan.FileMetrics, bool) {
	if m, ok := metrics[path]; ok {
		return m, true
	}
	for rel, m := range metrics {
		if len(path) > len(rel) && hasSuffixPath(path, rel) {
			return m, true
		}
	}
	return codescan.FileMetrics{}, false
}

// hasSuffixPath reports whether path ends with rel at a separator boundary, so
// that "internal/a/b.go" does not match "a/ab.go".
func hasSuffixPath(path, rel string) bool {
	if len(path) <= len(rel) {
		return path == rel
	}
	return path[len(path)-len(rel):] == rel && path[len(path)-len(rel)-1] == '/'
}
