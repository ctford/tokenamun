// Package codescan measures properties of the code a session worked on.
//
// Token spend only becomes actionable when you can see what it was spent on,
// so these metrics exist to be joined against retrieval and carry. They are
// deliberately modest: no external tools, no network, no language servers.
package codescan

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// Options controls a scan.
type Options struct {
	// MinDuplicateLines is the shortest run of normalised lines reported as a
	// duplicate. Below about 5 the results are noise.
	MinDuplicateLines int
	// LargeFileLines is the threshold above which a file is flagged.
	LargeFileLines int
	// Skip are path fragments excluded from the scan.
	Skip []string
}

// DefaultOptions are deliberately conservative.
func DefaultOptions() Options {
	return Options{
		MinDuplicateLines: 6,
		LargeFileLines:    400,
		Skip: []string{
			"/.git/", "/node_modules/", "/vendor/", "/dist/", "/build/",
			"/.venv/", "/target/", "/.cache/",
		},
	}
}

// FileMetrics describes one file.
type FileMetrics struct {
	Path      string `json:"path"`
	Language  string `json:"language"`
	Bytes     int    `json:"bytes"`
	Lines     int    `json:"lines"`
	CodeLines int    `json:"code_lines"`
	// Complexity sums the per-function complexity of the file.
	Complexity int `json:"complexity"`
	// MaxFunction is the worst single function, which is usually the more
	// actionable number.
	MaxFunction     int    `json:"max_function_complexity"`
	MaxFunctionName string `json:"max_function_name,omitempty"`
	// ComplexityProv distinguishes a real control-flow measurement from a
	// keyword approximation. Go is parsed; everything else is estimated.
	ComplexityProv model.Provenance  `json:"complexity_provenance"`
	Functions      []FunctionMetrics `json:"functions,omitempty"`
	Large          bool              `json:"large"`
}

// FunctionMetrics describes one function.
type FunctionMetrics struct {
	Name       string `json:"name"`
	Line       int    `json:"line"`
	Complexity int    `json:"complexity"`
}

// Location is where a duplicated block appears.
type Location struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
}

// Duplicate is a run of lines appearing in more than one place.
type Duplicate struct {
	Lines       int        `json:"lines"`
	Occurrences []Location `json:"occurrences"`
}

// Report is the result of a scan.
type Report struct {
	Root       string        `json:"root"`
	Files      []FileMetrics `json:"files"`
	Duplicates []Duplicate   `json:"duplicates"`
	Skipped    int           `json:"skipped_files"`
}

// Scan walks root and measures every file it recognises.
func Scan(root string, opts Options) (Report, error) {
	r := Report{Root: root}
	if opts.MinDuplicateLines <= 0 {
		opts.MinDuplicateLines = DefaultOptions().MinDuplicateLines
	}
	if opts.LargeFileLines <= 0 {
		opts.LargeFileLines = DefaultOptions().LargeFileLines
	}

	corpus := newCorpus(opts.MinDuplicateLines)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // an unreadable directory should not fail the scan
		}
		if d.IsDir() {
			if skipped(path+"/", opts.Skip) {
				return filepath.SkipDir
			}
			return nil
		}
		if skipped(path, opts.Skip) {
			return nil
		}
		lang := languageOf(path)
		if lang == "" {
			r.Skipped++
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			r.Skipped++
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		m := measure(rel, lang, string(src), opts)
		r.Files = append(r.Files, m)
		corpus.add(rel, lang, string(src))
		return nil
	})
	if err != nil {
		return r, err
	}
	r.Duplicates = corpus.duplicates()
	return r, nil
}

// measure computes one file's metrics.
func measure(rel, lang, src string, opts Options) FileMetrics {
	lines := strings.Split(src, "\n")
	// A trailing newline produces a final empty element that is not a line.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	code := 0
	for _, l := range lines {
		if isCode(l, lang) {
			code++
		}
	}

	m := FileMetrics{
		Path:      rel,
		Language:  lang,
		Bytes:     len(src),
		Lines:     len(lines),
		CodeLines: code,
		Large:     len(lines) > opts.LargeFileLines,
	}

	funcs, prov := complexity(rel, lang, src)
	m.Functions = funcs
	m.ComplexityProv = prov
	for _, f := range funcs {
		m.Complexity += f.Complexity
		if f.Complexity > m.MaxFunction {
			m.MaxFunction = f.Complexity
			m.MaxFunctionName = f.Name
		}
	}
	return m
}

func skipped(path string, skip []string) bool {
	norm := filepath.ToSlash(path)
	for _, s := range skip {
		if strings.Contains(norm, s) {
			return true
		}
	}
	return false
}
