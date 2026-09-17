package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/codescan"
	"github.com/ctford/tokenamun/internal/model"
)

// Scan reports code properties on their own.
type Scan struct {
	SchemaVersion int             `json:"schema_version"`
	Root          string          `json:"root"`
	Totals        ScanTotals      `json:"totals"`
	Largest       []ScanFile      `json:"largest_files"`
	MostComplex   []ScanFile      `json:"most_complex_files"`
	Duplicates    []ScanDuplicate `json:"duplicates"`
	Notes         []string        `json:"notes"`
}

// ScanTotals summarises the tree.
type ScanTotals struct {
	Files          model.Quantity `json:"files"`
	Lines          model.Quantity `json:"lines"`
	CodeLines      model.Quantity `json:"code_lines"`
	LargeFiles     model.Quantity `json:"large_files"`
	DuplicateRuns  model.Quantity `json:"duplicate_runs"`
	DuplicateLines model.Quantity `json:"duplicated_lines"`
	SkippedFiles   model.Quantity `json:"skipped_files"`
}

// ScanFile is one file's metrics.
type ScanFile struct {
	Path            string           `json:"path"`
	Language        string           `json:"language"`
	Lines           model.Quantity   `json:"lines"`
	Complexity      model.Quantity   `json:"complexity"`
	MaxFunction     model.Quantity   `json:"max_function_complexity"`
	MaxFunctionName string           `json:"max_function_name,omitempty"`
	Provenance      model.Provenance `json:"complexity_provenance"`
}

// ScanDuplicate is one duplicated run.
type ScanDuplicate struct {
	Lines       model.Quantity `json:"lines"`
	Occurrences []string       `json:"occurrences"`
}

// BuildScan computes the scan report.
func BuildScan(r codescan.Report) Scan {
	out := Scan{SchemaVersion: SchemaVersion, Root: r.Root}

	var lines, code, large, dupLines int
	for _, f := range r.Files {
		lines += f.Lines
		code += f.CodeLines
		if f.Large {
			large++
		}
	}
	for _, d := range r.Duplicates {
		dupLines += d.Lines * (len(d.Occurrences) - 1)
	}

	out.Totals = ScanTotals{
		Files:          model.Obs(float64(len(r.Files)), model.Calls),
		Lines:          model.Obs(float64(lines), model.Calls),
		CodeLines:      model.Obs(float64(code), model.Calls),
		LargeFiles:     model.Der(float64(large), model.Calls),
		DuplicateRuns:  model.Der(float64(len(r.Duplicates)), model.Calls),
		DuplicateLines: model.Der(float64(dupLines), model.Calls),
		SkippedFiles:   model.Obs(float64(r.Skipped), model.Calls),
	}

	byLines := append([]codescan.FileMetrics(nil), r.Files...)
	sort.SliceStable(byLines, func(i, j int) bool { return byLines[i].Lines > byLines[j].Lines })
	byComplexity := append([]codescan.FileMetrics(nil), r.Files...)
	sort.SliceStable(byComplexity, func(i, j int) bool {
		return byComplexity[i].MaxFunction > byComplexity[j].MaxFunction
	})

	out.Largest = topFiles(byLines, 10)
	out.MostComplex = topFiles(byComplexity, 10)

	for i, d := range r.Duplicates {
		if i >= 10 {
			break
		}
		var where []string
		for _, o := range d.Occurrences {
			where = append(where, fmt.Sprintf("%s:%d", o.Path, o.StartLine))
		}
		out.Duplicates = append(out.Duplicates, ScanDuplicate{
			Lines:       model.Der(float64(d.Lines), model.Calls),
			Occurrences: where,
		})
	}

	out.Notes = []string{
		"Go complexity is counted from the parsed syntax tree, so it is a real control-flow measurement.",
		"Other languages are approximated by counting branch keywords, which correlates with cyclomatic complexity but is not a computation of it.",
		"Duplicate runs compare normalised lines: whitespace, case and line comments are ignored, identifiers are not.",
		"Code lines exclude blanks and line comments. Block comments are not tracked and count as code.",
	}
	return out
}

func topFiles(files []codescan.FileMetrics, n int) []ScanFile {
	var out []ScanFile
	for i, f := range files {
		if i >= n {
			break
		}
		out = append(out, ScanFile{
			Path:            f.Path,
			Language:        f.Language,
			Lines:           model.Obs(float64(f.Lines), model.Calls),
			Complexity:      model.Quantity{Value: float64(f.Complexity), Unit: model.Calls, Prov: f.ComplexityProv},
			MaxFunction:     model.Quantity{Value: float64(f.MaxFunction), Unit: model.Calls, Prov: f.ComplexityProv},
			MaxFunctionName: f.MaxFunctionName,
			Provenance:      f.ComplexityProv,
		})
	}
	return out
}

// RenderScan writes the human-facing scan report.
func RenderScan(w io.Writer, s Scan) error {
	b := &strings.Builder{}
	b.WriteString("TOKENAMUN  code scan\n\n")
	fmt.Fprintf(b, "Tree %s\n", s.Root)
	line(b, "  Files measured", s.Totals.Files)
	line(b, "  Files skipped", s.Totals.SkippedFiles)
	line(b, "  Lines", s.Totals.Lines)
	line(b, "  Code lines", s.Totals.CodeLines)
	line(b, "  Large files", s.Totals.LargeFiles)
	line(b, "  Duplicate runs", s.Totals.DuplicateRuns)
	line(b, "  Duplicated lines", s.Totals.DuplicateLines)
	b.WriteString("\n")

	if len(s.MostComplex) > 0 {
		b.WriteString("Most complex functions\n")
		for _, f := range s.MostComplex {
			if f.MaxFunction.Value == 0 {
				continue
			}
			fmt.Fprintf(b, "  %-44s %4d  %s   [%s]\n", trunc(f.Path, 44),
				int(f.MaxFunction.Value), trunc(f.MaxFunctionName, 22), f.Provenance)
		}
		b.WriteString("\n")
	}

	if len(s.Largest) > 0 {
		b.WriteString("Largest files\n")
		for _, f := range s.Largest {
			fmt.Fprintf(b, "  %-50s %6d lines\n", trunc(f.Path, 50), int(f.Lines.Value))
		}
		b.WriteString("\n")
	}

	b.WriteString("Duplicated code\n")
	if len(s.Duplicates) == 0 {
		b.WriteString("  none found above the minimum run length\n\n")
	} else {
		for _, d := range s.Duplicates {
			fmt.Fprintf(b, "  %3d lines  %s\n", int(d.Lines.Value), strings.Join(d.Occurrences, "  "))
		}
		b.WriteString("\n")
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// Hotspots reports code properties joined against session cost.
type Hotspots struct {
	SchemaVersion int             `json:"schema_version"`
	Session       SessionInfo     `json:"session"`
	Files         []HotspotRow    `json:"files"`
	Coverage      CoverageReport  `json:"coverage"`
	Unmatched     UnmatchedReport `json:"unmatched"`
	Warnings      []model.Warning `json:"warnings,omitempty"`
	Notes         []string        `json:"notes"`
}

// HotspotRow is one file.
type HotspotRow struct {
	Path            string           `json:"path"`
	Lines           model.Quantity   `json:"lines"`
	Complexity      model.Quantity   `json:"max_function_complexity"`
	ComplexityProv  model.Provenance `json:"complexity_provenance,omitempty"`
	DuplicatedLines model.Quantity   `json:"duplicated_lines"`
	Retrievals      model.Quantity   `json:"retrievals"`
	RetrievedBytes  model.Quantity   `json:"retrieved_bytes"`
	CarryCost       model.Quantity   `json:"carry_cost"`
	Scanned         bool             `json:"scanned"`
}

// CoverageReport says how much of the session the code scan could speak to.
type CoverageReport struct {
	InTree   model.Quantity `json:"files_in_scanned_tree"`
	NotFound model.Quantity `json:"files_not_in_scanned_tree"`
	Note     string         `json:"note,omitempty"`
}

// UnmatchedReport accounts for the retrievals no file could claim.
type UnmatchedReport struct {
	Retrievals model.Quantity `json:"retrievals"`
	Bytes      model.Quantity `json:"bytes"`
	CarryCost  model.Quantity `json:"carry_cost"`
	Note       string         `json:"note"`
}

// BuildHotspots computes the hotspot report.
func BuildHotspots(s *model.Session, h analysis.HotspotReport) Hotspots {
	out := Hotspots{
		SchemaVersion: SchemaVersion,
		Session:       sessionInfo(s),
		Unmatched: UnmatchedReport{
			Retrievals: model.Obs(float64(h.UnmatchedRetrievals), model.Calls),
			Bytes:      model.Obs(float64(h.UnmatchedBytes), model.Bytes),
			CarryCost:  model.Der(h.UnmatchedCarryEIT, model.EIT),
			Note: "Output that could not be attributed to a file. On sessions run " +
				"in auto mode this is most of it, because file reads go through the " +
				"shell and a build log has no path.",
		},
		Warnings: s.Warnings,
		Notes: []string{
			"This is a join of two observations, not a causal claim: a file being complex and a file being expensive to explore are separate facts.",
			"Code properties come from the working tree as it is now, which may have changed since the session ran.",
			"There is no developer dimension here, deliberately. Tokenamun is a sensor, not a judge.",
		},
	}

	out.Coverage = CoverageReport{
		InTree:   model.Obs(float64(h.Matched), model.Calls),
		NotFound: model.Obs(float64(h.Missing), model.Calls),
	}
	if h.Missing > 0 {
		out.Coverage.Note = fmt.Sprintf(
			"%d of the %d files this session retrieved are not in the scanned tree, so they "+
				"have no code metrics. The session recorded branch %q; a tree on a different "+
				"branch will not contain the code the session worked on.",
			h.Missing, h.Matched+h.Missing, s.Branch)
	}

	for i, f := range h.Hotspots {
		if i >= 20 {
			break
		}
		out.Files = append(out.Files, HotspotRow{
			Path:            f.Path,
			Lines:           model.Obs(float64(f.Lines), model.Calls),
			Complexity:      model.Quantity{Value: float64(f.MaxFunction), Unit: model.Calls, Prov: provOr(f.ComplexityProv)},
			ComplexityProv:  f.ComplexityProv,
			DuplicatedLines: model.Der(float64(f.DuplicateLines), model.Calls),
			Retrievals:      model.Obs(float64(f.Retrievals), model.Calls),
			RetrievedBytes:  model.Obs(float64(f.RetrievedBytes), model.Bytes),
			CarryCost:       model.Der(f.CarryEIT, model.EIT),
			Scanned:         f.Scanned,
		})
	}
	return out
}

// provOr supplies a provenance for files the scan did not reach, so that a
// zero is not rendered as if it had been measured.
func provOr(p model.Provenance) model.Provenance {
	if p == "" {
		return model.Derived
	}
	return p
}

// RenderHotspots writes the human-facing hotspot report.
func RenderHotspots(w io.Writer, h Hotspots) error {
	b := &strings.Builder{}
	b.WriteString("TOKENAMUN  hotspots\n\n")
	fmt.Fprintf(b, "Session %s\n", h.Session.ID)
	fmt.Fprintf(b, "  API calls          %s\n\n", num(h.Session.Calls))

	if len(h.Files) == 0 {
		b.WriteString("No retrievals could be attributed to a file in this session.\n\n")
	} else {
		b.WriteString("Files by what they cost to carry, against what they are like\n")
		fmt.Fprintf(b, "  %-38s %11s %6s %7s %6s %5s\n",
			"FILE", "CARRY(EIT)", "FETCH", "BYTES", "LINES", "CPLX")
		for _, f := range h.Files {
			lines, cplx := "-", "-"
			if f.Scanned {
				lines = num(int(f.Lines.Value))
				cplx = num(int(f.Complexity.Value))
			}
			fmt.Fprintf(b, "  %-38s %11s %6s %7s %6s %5s\n",
				trunc(f.Path, 38), num(int(f.CarryCost.Value)),
				num(int(f.Retrievals.Value)), bytesStr(f.RetrievedBytes.Value), lines, cplx)
		}
		b.WriteString("  LINES and CPLX are '-' where the file is not in the scanned tree.\n\n")
		b.WriteString("Code-scan coverage\n")
		line(b, "  In the scanned tree", h.Coverage.InTree)
		line(b, "  Not found", h.Coverage.NotFound)
		if h.Coverage.Note != "" {
			fmt.Fprintf(b, "  %s\n", wrap(h.Coverage.Note, 72, "  "))
		}
		b.WriteString("\n")
	}

	b.WriteString("Unattributed\n")
	line(b, "  Retrievals", h.Unmatched.Retrievals)
	fmt.Fprintf(b, "%-22s %14s   [%s]\n", "  Bytes", bytesStr(h.Unmatched.Bytes.Value), h.Unmatched.Bytes.Prov)
	line(b, "  Carry cost", h.Unmatched.CarryCost)
	fmt.Fprintf(b, "  %s\n\n", wrap(h.Unmatched.Note, 72, "  "))

	b.WriteString("This is a join of two observations, not a causal claim.\n\n")
	_, err := io.WriteString(w, b.String())
	return err
}
