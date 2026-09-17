package whatif

import (
	"strings"
	"testing"
	"time"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/content"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
)

// readingSession reads one file early and re-reads it, runs a test suite, and
// pipes a git log through head.
func readingSession(t *testing.T) Context {
	t.Helper()
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	var s model.Session
	s.Ref = model.SessionRef{ID: "reading-fixture", Origin: model.FromLocal}
	s.Estimator = model.TokenEstimator{BytesPerToken: 4, Method: "fixed ratio", Calibrated: true}

	prompt := int64(10000)
	for i := 0; i < 8; i++ {
		u := model.TokenUsage{Output: 200}
		if i == 0 {
			u.CacheCreation, u.CacheCreation5m = prompt, prompt
		} else {
			u.CacheRead = prompt
		}
		s.Invocations = append(s.Invocations, model.ModelInvocation{
			Seq: i, RequestID: "r", Model: "claude-opus-5",
			Timestamp: start.Add(time.Duration(i) * time.Minute), Usage: u,
		})
		prompt += 4000
	}

	s.Retrievals = []model.RetrievedContent{
		// Read early, so it is carried by everything after it.
		{Seq: 0, Tool: "Read", Path: "big.md", Channel: model.ChanFileRead,
			Bytes: 40000, Tokens: 10000, InvocationSeq: 1},
		// The same file again, later.
		{Seq: 1, Tool: "Read", Path: "big.md", Channel: model.ChanFileRead,
			Bytes: 40000, Tokens: 10000, InvocationSeq: 4},
		// A shell read of a file: file content too.
		{Seq: 2, Tool: "Bash", Path: "small.go", Channel: model.ChanShell,
			CommandBinary: "sed", Bytes: 4000, Tokens: 1000, InvocationSeq: 2},
		// A test run: not file content, whatever it printed.
		{Seq: 3, Tool: "Bash", Channel: model.ChanShell,
			CommandBinary: "go", Bytes: 20000, Tokens: 5000, InvocationSeq: 3},
		// `git log | head` -- head is shaping git's output, not reading a file.
		{Seq: 4, Tool: "Bash", Channel: model.ChanShell,
			CommandBinary: "head", PipelineFilter: true,
			Bytes: 8000, Tokens: 2000, InvocationSeq: 5},
	}
	cache := analysis.Cache(&s, analysis.TTL5m)
	return Context{
		Session: &s, Cache: cache, Carry: analysis.Carry(&s, cache),
		Weights: cost.For("claude-opus-5"), CompressionRatio: 0.5,
	}
}

func TestFileCompressionCountsOnlyFileContent(t *testing.T) {
	c := readingSession(t)
	r := FileCompression{}.Estimate(c)
	if !r.Applicable {
		t.Fatalf("this session read files: %+v", r)
	}

	var reads, files, bytes float64
	for _, f := range r.Observed {
		switch f.Label {
		case "file reads":
			reads = f.Quantity.Value
		case "distinct files read":
			files = f.Quantity.Value
		case "file content":
			bytes = f.Quantity.Value
		}
	}
	// Three file reads: big.md twice and small.go once. The test run and the
	// piped head are not file content and must not be in the total.
	if reads != 3 {
		t.Errorf("expected 3 file reads, got %.0f", reads)
	}
	if files != 2 {
		t.Errorf("expected 2 distinct files, got %.0f", files)
	}
	if want := 40000.0 + 40000 + 4000; bytes != want {
		t.Errorf("expected %.0f bytes of file content, got %.0f", want, bytes)
	}
}

func TestFileCompressionRanksByWhatShrinkingIsWorthNotBySize(t *testing.T) {
	// A file read early is re-sent by every call after it, so its cost is not
	// its size. That is the whole reason this is worth computing rather than
	// sorting a directory listing by bytes.
	c := readingSession(t)
	r := FileCompression{}.Estimate(c)

	var named []string
	for _, f := range r.Derived {
		if strings.Contains(f.Label, ".md") || strings.Contains(f.Label, ".go") {
			named = append(named, f.Label)
		}
	}
	if len(named) < 2 {
		t.Fatalf("the report must name the files worth shrinking, got %v", named)
	}
	if named[0] != "big.md" {
		t.Errorf("expected big.md first, got %v", named)
	}
}

func TestFileCompressionScalesWithTheRatioAndSaysWhereItCameFrom(t *testing.T) {
	c := readingSession(t)
	half := FileCompression{}.Estimate(c).Headline.Quantity.Value

	c.CompressionRatio = 0.25
	quarter := FileCompression{}.Estimate(c).Headline.Quantity.Value
	if !(quarter < half) {
		t.Errorf("a harsher ratio must save more: %.0f then %.0f", half, quarter)
	}

	// A measured ratio beats an assumed one, and the report must say which it
	// used, because the number is linear in it.
	c.FileReplay = &ReplayResult{
		Command: "gzip -c", Scope: "file content", Items: 3,
		InputBytes: 84000, OutputBytes: 8400,
	}
	r := FileCompression{}.Estimate(c)
	// The one-line caveat says the number is linear in a ratio; which ratio,
	// and where it came from, is the argument behind it.
	if !strings.Contains(r.CaveatDetail, "measured") ||
		!strings.Contains(r.CaveatDetail, "gzip -c") {
		t.Errorf("the detail must say the ratio was measured and by what: %q", r.CaveatDetail)
	}
	if measured := r.Headline.Quantity.Value; !(measured < quarter) {
		t.Errorf("a 10%% measured ratio must beat a 25%% assumption: %.0f then %.0f",
			quarter, measured)
	}
}

func TestFileCompressionAndOutputCompressionDoNotOverlap(t *testing.T) {
	// The two interventions partition the content between them. If they
	// overlapped, someone applying both would double-count the saving.
	c := readingSession(t)
	for _, item := range c.Session.Retrievals {
		file := content.IsFileContent(item)
		output := viaTool(item.Tool) && !file
		if file && output {
			t.Errorf("retrieval %d is counted by both interventions", item.Seq)
		}
	}
}

func TestFileCompressionSaysNothingWhenNoFilesWereRead(t *testing.T) {
	c := readingSession(t)
	var shellOnly []model.RetrievedContent
	for _, item := range c.Session.Retrievals {
		if !content.IsFileContent(item) {
			shellOnly = append(shellOnly, item)
		}
	}
	c.Session.Retrievals = shellOnly
	c.Carry = analysis.Carry(c.Session, c.Cache)

	r := FileCompression{}.Estimate(c)
	if r.Applicable {
		t.Error("a session that read no files has nothing to shrink")
	}
	if !strings.Contains(r.NotMeasurable, "no file content") {
		t.Errorf("it should say why: %q", r.NotMeasurable)
	}
}
