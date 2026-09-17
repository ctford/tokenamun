package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/codescan"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/ingest"
	"github.com/ctford/tokenamun/internal/model"
	"github.com/ctford/tokenamun/internal/whatif"
)

var update = flag.Bool("update", false, "regenerate golden files")

// Integration: the whole pipeline from transcript bytes to rendered output,
// with no Entire installation, no git and no network.
func TestProfileGoldenOutput(t *testing.T) {
	s, err := ingest.Load(model.SessionRef{
		ID:         "fixture-session",
		Transcript: filepath.Join("..", "ingest", "testdata", "repeated-request.jsonl"),
		Origin:     model.FromLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	p := BuildProfile(s)

	var text bytes.Buffer
	if err := RenderText(&text, p); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "profile.txt", text.Bytes())

	pretty, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "profile.json", append(pretty, '\n'))
}

func TestRetrievalGoldenOutput(t *testing.T) {
	s, err := ingest.Load(model.SessionRef{
		ID:         "retrieval-fixture",
		Transcript: filepath.Join("..", "ingest", "testdata", "retrieval.jsonl"),
		Origin:     model.FromLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := BuildRetrieval(s)

	var text bytes.Buffer
	if err := RenderRetrieval(&text, r); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "retrieval.txt", text.Bytes())

	pretty, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "retrieval.json", append(pretty, '\n'))
	walkQuantities(t, "retrieval", mustTree(t, r))
}

func mustTree(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	return tree
}

// Every quantity the JSON contract exposes must carry a provenance label and a
// unit. This is the check that stops an unlabelled number reaching an agent.
func TestEveryReportedQuantityIsLabelled(t *testing.T) {
	s, err := ingest.Load(model.SessionRef{
		Transcript: filepath.Join("..", "ingest", "testdata", "repeated-request.jsonl"),
	})
	if err != nil {
		t.Fatal(err)
	}
	p := BuildProfile(s)

	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatal(err)
	}
	walkQuantities(t, "", tree)
}

// walkQuantities finds every object with a "value" key and asserts it is a
// well-formed Quantity.
func walkQuantities(t *testing.T, path string, v any) {
	t.Helper()
	switch n := v.(type) {
	case map[string]any:
		if _, isQuantity := n["value"]; isQuantity {
			q := model.Quantity{}
			if p, ok := n["provenance"].(string); ok {
				q.Prov = model.Provenance(p)
			}
			if u, ok := n["unit"].(string); ok {
				q.Unit = model.Unit(u)
			}
			if val, ok := n["value"].(float64); ok {
				q.Value = val
			}
			if err := q.Validate(); err != nil {
				t.Errorf("%s: %v", path, err)
			}
			return
		}
		for k, child := range n {
			walkQuantities(t, path+"."+k, child)
		}
	case []any:
		for i, child := range n {
			walkQuantities(t, path+"[]", child)
			_ = i
		}
	}
}

func TestVolumeAndCostAreDistinctQuantities(t *testing.T) {
	// The invariant from AGENTS.md rule 3 and 4: these are different units and
	// cost must never be reported as tokens.
	s, _ := ingest.Load(model.SessionRef{
		Transcript: filepath.Join("..", "ingest", "testdata", "repeated-request.jsonl"),
	})
	p := BuildProfile(s)
	if p.Usage.PromptVolume.Unit != model.Tokens {
		t.Error("prompt volume must be in tokens")
	}
	if p.Usage.PromptCost.Unit != model.EIT {
		t.Error("prompt cost must be in EIT, not tokens")
	}
	if p.Usage.PromptCost.Value >= p.Usage.PromptVolume.Value {
		t.Error("a cache-heavy session's cost must be below its raw volume")
	}
}

func TestTextRendererRefusesUnlabelledQuantities(t *testing.T) {
	var b strings.Builder
	line(&b, "  Bare", model.Quantity{Value: 42})
	if !strings.Contains(b.String(), "unlabelled") {
		t.Fatalf("an unlabelled quantity must not render as a number, got %q", b.String())
	}
}

func compareGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s (run: go test ./internal/report -update)", path)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from golden file.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

func carrySession(t *testing.T) *model.Session {
	t.Helper()
	s, err := ingest.Load(model.SessionRef{
		ID:         "carry-fixture",
		Transcript: filepath.Join("..", "ingest", "testdata", "carry.jsonl"),
		Origin:     model.FromEntire,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCarryGoldenOutput(t *testing.T) {
	s := carrySession(t)
	r := BuildCarry(s, analysis.Carry(s, analysis.Cache(s, analysis.TTL5m)))

	var text bytes.Buffer
	if err := RenderCarry(&text, r); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "carry.txt", text.Bytes())

	pretty, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "carry.json", append(pretty, '\n'))
	walkQuantities(t, "carry", mustTree(t, r))
}

func TestCacheGoldenOutput(t *testing.T) {
	s := carrySession(t)
	r := BuildCache(s, analysis.Cache(s, analysis.TTL5m))

	var text bytes.Buffer
	if err := RenderCache(&text, r); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "cache.txt", text.Bytes())

	pretty, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "cache.json", append(pretty, '\n'))
	walkQuantities(t, "cache", mustTree(t, r))
}

func TestScanGoldenOutput(t *testing.T) {
	// A small tree with a known shape: one complex Go function, one oversized
	// file, and a duplicated block across two files.
	root := t.TempDir()
	files := map[string]string{
		"internal/pay/charge.go": `package pay

func Charge(n int, ok bool) int {
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
	}
	return 0
}
`,
		"internal/pay/refund.go": `package pay

func Refund(a int) int {
	total := 0
	for i := 0; i < a; i++ {
		total += i * 2
	}
	if total > 100 {
		total = 100
	}
	return total
}
`,
		"internal/billing/credit.go": `package billing

func Credit(a int) int {
	total := 0
	for i := 0; i < a; i++ {
		total += i * 2
	}
	if total > 100 {
		total = 100
	}
	return total
}
`,
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r, err := codescan.Scan(root, codescan.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	// The root is a temporary path, so normalise it before comparing.
	out := BuildScan(r)
	out.Root = "(test tree)"

	var text bytes.Buffer
	if err := RenderScan(&text, out); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "scan.txt", text.Bytes())
	walkQuantities(t, "scan", mustTree(t, out))
}

func TestHotspotsGoldenOutput(t *testing.T) {
	s := carrySession(t)
	scan := codescan.Report{
		Root: "(test tree)",
		Files: []codescan.FileMetrics{{
			Path: "internal/payment/charge.go", Language: "go",
			Lines: 480, CodeLines: 400, Complexity: 62, MaxFunction: 21,
			MaxFunctionName: "Charge", ComplexityProv: model.Derived, Large: true,
		}},
		Duplicates: []codescan.Duplicate{{
			Lines: 14,
			Occurrences: []codescan.Location{
				{Path: "internal/payment/charge.go", StartLine: 120},
				{Path: "internal/payment/refund.go", StartLine: 40},
			},
		}},
	}
	carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))
	out := BuildHotspots(s, analysis.Hotspots(s, scan, carry))

	var text bytes.Buffer
	if err := RenderHotspots(&text, out); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "hotspots.txt", text.Bytes())
	walkQuantities(t, "hotspots", mustTree(t, out))
}

// The join must not acquire a developer dimension. Checked against the schema
// keys rather than the document text, since the notes legitimately mention
// that the dimension is absent.
func TestHotspotsHaveNoDeveloperDimension(t *testing.T) {
	s := carrySession(t)
	out := BuildHotspots(s, analysis.Hotspots(s, codescan.Report{},
		analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))))

	banned := []string{"developer", "author", "committer", "email", "username", "user_id"}
	for _, key := range jsonKeys(t, out) {
		lower := strings.ToLower(key)
		for _, b := range banned {
			if strings.Contains(lower, b) {
				t.Errorf("hotspot schema must not carry a %q field, found %q", b, key)
			}
		}
	}
}

// jsonKeys collects every object key in a marshalled value.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	var keys []string
	var walk func(any)
	walk = func(n any) {
		switch node := n.(type) {
		case map[string]any:
			for k, child := range node {
				keys = append(keys, k)
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(mustTree(t, v))
	return keys
}

func TestWhatIfGoldenOutput(t *testing.T) {
	s := carrySession(t)
	cache := analysis.Cache(s, analysis.TTL5m)
	ctx := whatif.Context{
		Session:          s,
		Cache:            cache,
		Carry:            analysis.Carry(s, cache),
		Weights:          cost.Default,
		CompressionRatio: 0.5,
	}
	for _, i := range whatif.All() {
		out := BuildWhatIf(s, i.Estimate(ctx))
		var text bytes.Buffer
		if err := RenderWhatIf(&text, out); err != nil {
			t.Fatal(err)
		}
		compareGolden(t, "whatif-"+i.Name()+".txt", text.Bytes())
		walkQuantities(t, "whatif."+i.Name(), mustTree(t, out))
	}
}

// The unknown section must always render, because it is the thing that stops a
// counterfactual being read as a measurement.
func TestWhatIfAlwaysRendersItsUnknowns(t *testing.T) {
	s := carrySession(t)
	cache := analysis.Cache(s, analysis.TTL5m)
	ctx := whatif.Context{
		Session: s, Cache: cache, Carry: analysis.Carry(s, cache),
		Weights: cost.Default, CompressionRatio: 0.5,
	}
	for _, i := range whatif.All() {
		var text bytes.Buffer
		if err := RenderWhatIf(&text, BuildWhatIf(s, i.Estimate(ctx))); err != nil {
			t.Fatal(err)
		}
		out := text.String()
		if !strings.Contains(out, "Unknown") {
			t.Errorf("%s: no Unknown section rendered", i.Name())
		}
		if !strings.Contains(out, "task_success") {
			t.Errorf("%s: the outcome caveat must be visible in the text output", i.Name())
		}
	}
}

func TestCompareGoldenOutput(t *testing.T) {
	a := carrySession(t)
	b, err := ingest.Load(model.SessionRef{
		ID:         "retrieval-fixture",
		Transcript: filepath.Join("..", "ingest", "testdata", "retrieval.jsonl"),
		Origin:     model.FromLocal,
	})
	if err != nil {
		t.Fatal(err)
	}
	out := BuildCompare(BuildProfile(a), BuildProfile(b), BuildRetrieval(a), BuildRetrieval(b))

	var text bytes.Buffer
	if err := RenderCompare(&text, out); err != nil {
		t.Fatal(err)
	}
	compareGolden(t, "compare.txt", text.Bytes())
	walkQuantities(t, "compare", mustTree(t, out))
}

// A comparison must not be presented as an experiment.
func TestCompareSaysItIsNotAControlledExperiment(t *testing.T) {
	a := carrySession(t)
	out := BuildCompare(BuildProfile(a), BuildProfile(a), BuildRetrieval(a), BuildRetrieval(a))
	var text bytes.Buffer
	if err := RenderCompare(&text, out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "not a controlled experiment") {
		t.Error("the caveat must be in the output, not only in the docs")
	}
}

func TestTreemapIsSelfContainedAndHonestAboutWhatItShows(t *testing.T) {
	s := carrySession(t)
	p := BuildTreemap(s, analysis.Carry(s, analysis.Cache(s, analysis.TTL5m)))

	var out bytes.Buffer
	if err := RenderTreemap(&out, p); err != nil {
		t.Fatal(err)
	}
	html := out.String()

	// Self-contained: a report someone opens months later must not depend on
	// a CDN still being up, and must not phone home from their machine.
	for _, external := range []string{"src=\"http", "href=\"http", "cdn.", "unpkg", "googleapis"} {
		if strings.Contains(html, external) {
			t.Errorf("the report must be self-contained, found %q", external)
		}
	}
	if strings.Contains(html, "__TOKENAMUN_DATA__") {
		t.Error("the data placeholder was not substituted")
	}

	// The disclaimer is the point: area is observed retrieved size, not a
	// reconstruction of the context window.
	if !strings.Contains(html, "not a picture of the context window") {
		t.Error("the report must say what it is not")
	}
	// Ten categories all carry meaning, so the numbers must also exist as text.
	if !strings.Contains(html, "<table>") {
		t.Error("a table view must exist for accessibility and for >7 categories")
	}
	// Dark mode is selected, under both the OS setting and the explicit toggle.
	if !strings.Contains(html, "prefers-color-scheme: dark") ||
		!strings.Contains(html, `:root[data-theme="dark"]`) {
		t.Error("dark mode must be declared for both the OS setting and the toggle")
	}
}

func TestTreemapPayloadJoinsCarryOntoRetrievals(t *testing.T) {
	s := carrySession(t)
	p := BuildTreemap(s, analysis.Carry(s, analysis.Cache(s, analysis.TTL5m)))

	if len(p.Items) == 0 {
		t.Fatal("the fixture has retrievals")
	}
	var withCarry int
	for _, i := range p.Items {
		if i.Carry > 0 {
			withCarry++
			if i.ResidentFor == 0 {
				t.Errorf("%s has carry cost but no residency", i.Label)
			}
			if i.CarryPerToken <= 0 {
				t.Errorf("%s has carry cost but no per-token rate for the colour ramp", i.Label)
			}
		}
	}
	if withCarry == 0 {
		t.Error("carry should have joined onto at least one retrieval")
	}
	if p.MaxCarryPerToken <= 0 {
		t.Error("the colour ramp needs a maximum to scale against")
	}
	// Items are ordered largest-first so the table reads in scan order.
	for i := 1; i < len(p.Items); i++ {
		if p.Items[i-1].Tokens < p.Items[i].Tokens {
			t.Fatal("items should be ordered largest first")
		}
	}
}

func TestTreemapFailsLoudlyIfTheTemplateLosesItsPlaceholder(t *testing.T) {
	// Guards against a template edit silently producing a report with no data.
	tmpl, err := templates.ReadFile("templates/treemap.html")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(tmpl, []byte("__TOKENAMUN_DATA__")) {
		t.Fatal("the embedded template must carry the data placeholder")
	}
}
