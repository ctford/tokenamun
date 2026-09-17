package codescan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ctford/tokenamun/internal/model"
)

// write lays out a temporary tree for a scan.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func fileNamed(t *testing.T, r Report, name string) FileMetrics {
	t.Helper()
	for _, f := range r.Files {
		if f.Path == name {
			return f
		}
	}
	t.Fatalf("no metrics for %s (got %d files)", name, len(r.Files))
	return FileMetrics{}
}

// Go is parsed, so its complexity is a real control-flow count.
func TestGoComplexityIsMeasuredFromTheAST(t *testing.T) {
	src := `package x

// straight through: complexity 1
func simple() int { return 1 }

// if + && + for + two cases = 1 + 5
func branchy(n int, ok bool) int {
	if n > 0 && ok {
		for i := 0; i < n; i++ {
			n--
		}
	}
	switch n {
	case 1:
		return 1
	case 2:
		return 2
	default:
		return 0
	}
}
`
	root := write(t, map[string]string{"x.go": src})
	r, err := Scan(root, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	f := fileNamed(t, r, "x.go")

	if f.ComplexityProv != model.Derived {
		t.Errorf("parsed Go complexity should be derived, got %q", f.ComplexityProv)
	}
	byName := map[string]int{}
	for _, fn := range f.Functions {
		byName[fn.Name] = fn.Complexity
	}
	if byName["simple"] != 1 {
		t.Errorf("simple() = %d, want 1", byName["simple"])
	}
	if got := byName["branchy"]; got != 6 {
		t.Errorf("branchy() = %d, want 6 (if, &&, for, two cases, plus one)", got)
	}
	if f.MaxFunctionName != "branchy" {
		t.Errorf("worst function = %q, want branchy", f.MaxFunctionName)
	}
}

func TestMethodsAreNamedWithTheirReceiver(t *testing.T) {
	root := write(t, map[string]string{"x.go": `package x

type T struct{}

func (t *T) Method() {}
`})
	r, _ := Scan(root, DefaultOptions())
	f := fileNamed(t, r, "x.go")
	if len(f.Functions) != 1 || f.Functions[0].Name != "T.Method" {
		t.Fatalf("want T.Method, got %+v", f.Functions)
	}
}

func TestDefaultClauseIsNotABranch(t *testing.T) {
	// A default is the fall-through, so it adds no decision point.
	root := write(t, map[string]string{"x.go": `package x

func f(n int) int {
	switch n {
	default:
		return 0
	}
}
`})
	r, _ := Scan(root, DefaultOptions())
	if got := fileNamed(t, r, "x.go").Complexity; got != 1 {
		t.Fatalf("complexity = %d, want 1", got)
	}
}

// Other languages are approximated, and must say so.
func TestNonGoComplexityIsLabelledApproximate(t *testing.T) {
	root := write(t, map[string]string{"app.py": `def handle(x):
    if x > 0:
        for i in range(x):
            if i % 2:
                pass
    return x
`})
	r, _ := Scan(root, DefaultOptions())
	f := fileNamed(t, r, "app.py")

	if f.ComplexityProv != model.DerivedApprox {
		t.Errorf("keyword counting must be derived-approx, got %q", f.ComplexityProv)
	}
	if f.Complexity <= 1 {
		t.Errorf("expected the branches to be counted, got %d", f.Complexity)
	}
	if len(f.Functions) == 0 || f.Functions[0].Name != "handle" {
		t.Errorf("expected the function to be attributed, got %+v", f.Functions)
	}
}

func TestUnparseableGoFallsBackRatherThanReportingNothing(t *testing.T) {
	// A file caught mid-edit should still be measured, approximately.
	root := write(t, map[string]string{"broken.go": `package x
func f( {
	if true {
	}
`})
	r, _ := Scan(root, DefaultOptions())
	f := fileNamed(t, r, "broken.go")
	if f.ComplexityProv != model.DerivedApprox {
		t.Errorf("a file that does not parse should be approximated, got %q", f.ComplexityProv)
	}
}

func TestSizeMetricsSeparateCodeFromBlanksAndComments(t *testing.T) {
	root := write(t, map[string]string{"x.go": `package x

// a comment

func f() {}
`})
	f := fileNamed(t, mustScan(t, root), "x.go")
	if f.Lines != 5 {
		t.Errorf("lines = %d, want 5", f.Lines)
	}
	if f.CodeLines != 2 {
		t.Errorf("code lines = %d, want 2 (package and func)", f.CodeLines)
	}
}

func TestLargeFilesAreFlaggedAgainstTheThreshold(t *testing.T) {
	long := ""
	for i := 0; i < 50; i++ {
		long += "var x = 1\n"
	}
	root := write(t, map[string]string{"big.go": "package x\n" + long})
	opts := DefaultOptions()
	opts.LargeFileLines = 10
	r, _ := Scan(root, opts)
	if !fileNamed(t, r, "big.go").Large {
		t.Error("a file over the threshold should be flagged")
	}
}

func TestDuplicateBlocksAreFoundAcrossFilesAndExtendedToTheirFullLength(t *testing.T) {
	block := `func handle(a int) int {
	total := 0
	for i := 0; i < a; i++ {
		total += i * 2
	}
	if total > 100 {
		total = 100
	}
	return total
}`
	root := write(t, map[string]string{
		"a.go": "package x\n\n" + block + "\n",
		"b.go": "package y\n\nvar pad = 1\n\n" + block + "\n",
	})
	r, err := Scan(root, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Duplicates) != 1 {
		t.Fatalf("expected one duplicate run, got %d: %+v", len(r.Duplicates), r.Duplicates)
	}
	d := r.Duplicates[0]
	if len(d.Occurrences) != 2 {
		t.Fatalf("expected two occurrences, got %+v", d.Occurrences)
	}
	// The run must be reported at its full length, not at the window size.
	if d.Lines != 10 {
		t.Errorf("run length = %d, want the whole 10-line function", d.Lines)
	}
	if d.Occurrences[0].Path != "a.go" || d.Occurrences[1].Path != "b.go" {
		t.Errorf("occurrences not attributed to both files: %+v", d.Occurrences)
	}
}

func TestReformattingDoesNotHideADuplicate(t *testing.T) {
	// Normalisation covers whitespace, case and comments, which is what
	// copy-paste-then-tidy actually produces.
	original := "func f() {\n\tif a && b {\n\t\tdoWork()\n\t}\n\treturn\n}\n"
	reformatted := "func   f() {\n    // tidied up\n    if a  &&  b {\n        doWork()\n    }\n    return\n}\n"
	root := write(t, map[string]string{
		"a.go": "package x\n" + original,
		"b.go": "package y\n" + reformatted,
	})
	r, _ := Scan(root, DefaultOptions())
	if len(r.Duplicates) == 0 {
		t.Fatal("reformatted copy-paste should still be detected")
	}
}

func TestShortRepetitionIsNotReportedAsDuplication(t *testing.T) {
	// Two matching lines are not a finding; they are how code looks.
	root := write(t, map[string]string{
		"a.go": "package x\nvar a = 1\nvar b = 2\n",
		"b.go": "package y\nvar a = 1\nvar b = 2\n",
	})
	r, _ := Scan(root, DefaultOptions())
	if len(r.Duplicates) != 0 {
		t.Fatalf("expected no duplicates below the window, got %+v", r.Duplicates)
	}
}

func TestVendoredAndGeneratedTreesAreSkipped(t *testing.T) {
	root := write(t, map[string]string{
		"main.go":                 "package main\nfunc main() {}\n",
		"vendor/dep/dep.go":       "package dep\nfunc D() {}\n",
		"node_modules/x/index.js": "function x() {}\n",
	})
	r, _ := Scan(root, DefaultOptions())
	for _, f := range r.Files {
		if f.Path != "main.go" {
			t.Errorf("unexpected scanned file %q", f.Path)
		}
	}
}

func TestUnknownFileTypesAreCountedNotGuessedAt(t *testing.T) {
	root := write(t, map[string]string{
		"main.go":   "package main\nfunc main() {}\n",
		"notes.txt": "hello\n",
		"data.bin":  "\x00\x01\x02\n",
	})
	r, _ := Scan(root, DefaultOptions())
	if len(r.Files) != 1 {
		t.Errorf("only the Go file should be measured, got %d", len(r.Files))
	}
	if r.Skipped != 2 {
		t.Errorf("skipped = %d, want 2 reported rather than silently dropped", r.Skipped)
	}
}

func TestScanIsDeterministic(t *testing.T) {
	block := "func g() {\n\tif x {\n\t\ty()\n\t}\n\tz()\n\treturn\n}\n"
	root := write(t, map[string]string{
		"a.go": "package x\n" + block,
		"b.go": "package y\n" + block,
		"c.go": "package z\n" + block,
	})
	first := mustScan(t, root)
	for i := 0; i < 3; i++ {
		again := mustScan(t, root)
		if len(again.Duplicates) != len(first.Duplicates) {
			t.Fatal("duplicate detection must not vary between runs")
		}
		for j := range again.Duplicates {
			if again.Duplicates[j].Lines != first.Duplicates[j].Lines {
				t.Fatal("duplicate ordering must be stable")
			}
		}
	}
}

func mustScan(t *testing.T, root string) Report {
	t.Helper()
	r, err := Scan(root, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	return r
}
