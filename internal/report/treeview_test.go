package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
	"github.com/ctford/tokenamun/internal/whatif"
)

func treeViewFixture(t *testing.T) (view TreeView, carry analysis.CarryReport) {
	t.Helper()
	s := carrySession(t)
	carry = analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))
	v, err := BuildTreeView(s, carry, nil, ModeCarry)
	if err != nil {
		t.Fatal(err)
	}
	return v, carry
}

func TestTreeViewShowsOneLevelWithBothPercentages(t *testing.T) {
	v, _ := treeViewFixture(t)
	if len(v.Children) == 0 {
		t.Fatal("the root has children")
	}
	// One level at a time, like the viewer: children are flattened, so a
	// caller is not handed a whole hierarchy to walk.
	for _, c := range v.Children {
		if c.ShareOfLevel <= 0 {
			t.Errorf("%s has no share of its level", c.Name)
		}
		if c.ShareOfSession <= 0 {
			t.Errorf("%s has no share of the session", c.Name)
		}
		// At the root the two are the same; below it they must not be
		// conflated, which is why both are reported.
		if c.ShareOfLevel != c.ShareOfSession {
			t.Errorf("at the root a level share and a session share are the same thing: %s", c.Name)
		}
	}
	var sum float64
	for _, c := range v.Children {
		sum += c.ShareOfLevel
	}
	if sum < 0.99 || sum > 1.01 {
		t.Errorf("the level should account for itself, shares sum to %.3f", sum)
	}
}

func TestTreeViewDrillsInByNameAndSaysHow(t *testing.T) {
	v, carry := treeViewFixture(t)
	s := carrySession(t)

	var branch TreeNode
	for _, c := range v.Children {
		if c.Children > 0 {
			branch = c
			break
		}
	}
	if branch.Name == "" {
		t.Skip("the fixture has no branch to drill into")
	}
	// Every branch comes with the command that goes into it. An affordance an
	// agent has to guess at is not an affordance.
	var advertised bool
	for _, d := range v.Drill {
		if strings.Contains(d, branch.At) {
			advertised = true
		}
	}
	if !advertised {
		t.Errorf("no drill-in command was offered for %q: %v", branch.Name, v.Drill)
	}

	inner, err := BuildTreeView(s, carry, strings.Split(branch.At, "/"), ModeCarry)
	if err != nil {
		t.Fatal(err)
	}
	if inner.Here.Name != branch.Name {
		t.Errorf("--at %q landed on %q", branch.At, inner.Here.Name)
	}
	if inner.Here.Cost != branch.Cost {
		t.Errorf("the node's cost changed on the way in: %.0f then %.0f",
			branch.Cost, inner.Here.Cost)
	}
	// Below the root the two percentages diverge, which is the whole reason
	// the viewer shows both.
	if inner.Here.ShareOfSession >= 1 {
		t.Error("a branch cannot be the whole session")
	}

	// Case-insensitive, so a name read out of a previous level works as typed.
	if _, err := BuildTreeView(s, carry, []string{strings.ToUpper(branch.Name)}, ModeCarry); err != nil {
		t.Errorf("--at should not be case-sensitive: %v", err)
	}
}

func TestTreeViewErrorsNameWhatIsActuallyThere(t *testing.T) {
	s := carrySession(t)
	carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))

	_, err := BuildTreeView(s, carry, []string{"no such branch"}, ModeCarry)
	if err == nil {
		t.Fatal("an unknown node must be an error")
	}
	// The error is the discovery mechanism when a caller guesses wrong, so it
	// has to list the options rather than just refusing.
	if !strings.Contains(err.Error(), "it contains:") {
		t.Errorf("the error should say what is available: %v", err)
	}

	if _, err := BuildTreeView(s, carry, nil, "sideways"); err == nil {
		t.Error("an unknown cost mode must be an error, not silently carry")
	}
}

func TestTreeViewUncachedModeCostsMoreThanAsBilled(t *testing.T) {
	s := carrySession(t)
	carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))

	billed, err := BuildTreeView(s, carry, nil, ModeCarry)
	if err != nil {
		t.Fatal(err)
	}
	uncached, err := BuildTreeView(s, carry, nil, ModeUncached)
	if err != nil {
		t.Fatal(err)
	}
	// The gap between the two is what prompt caching was worth, which is not
	// visible from either number alone. Both modes must therefore exist.
	if !(uncached.Total > billed.Total) {
		t.Errorf("pricing without caching should cost more: %.0f then %.0f",
			billed.Total, uncached.Total)
	}
	if uncached.Mode != ModeUncached {
		t.Error("the report must say which pricing it used")
	}
	var saidSo bool
	for _, n := range uncached.Notes {
		saidSo = saidSo || strings.Contains(n, "as though nothing cached")
	}
	if !saidSo {
		t.Error("the uncached mode must say what it is showing")
	}
}

func TestTreeViewCarriesTheSameExplanationsAsTheTooltips(t *testing.T) {
	// A node's meaning is in the viewer's tooltip. If the CLI does not carry
	// it, an agent has to ask a person what the box says.
	s := carrySession(t)
	carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))
	tree := BuildTree(s, carry)

	var withDetail int
	var walk func(*Node, []string)
	walk = func(n *Node, path []string) {
		if n.Detail != "" && len(path) > 0 {
			v, err := BuildTreeView(s, carry, path, ModeCarry)
			if err != nil {
				t.Errorf("%v: %v", path, err)
				return
			}
			if v.Here.Detail != n.Detail {
				t.Errorf("%v: the explanation did not survive: %q", path, v.Here.Detail)
			}
			withDetail++
		}
		for _, c := range n.Children {
			walk(c, append(path, c.Name))
		}
	}
	walk(tree, nil)
	if withDetail == 0 {
		t.Skip("the fixture has no explained nodes")
	}
}

// TestCLIAndViewerCannotDiverge is the point of all of this.
//
// The HTML report is handed a payload. The tree command is built from the same
// tree with the same carry report. If a field reaches one and not the other,
// an agent knows less about the session than a person looking at the picture,
// and the tool has a class of question it can only answer to humans.
func TestCLIAndViewerCannotDiverge(t *testing.T) {
	s := carrySession(t)
	carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))
	payload := BuildTreemap(s, carry)

	v, err := BuildTreeView(s, carry, nil, ModeCarry)
	if err != nil {
		t.Fatal(err)
	}

	// Same root total, in both modes.
	if v.Total != payload.Tree.Carry {
		t.Errorf("root cost differs: CLI %.2f, viewer %.2f", v.Total, payload.Tree.Carry)
	}
	uncached, err := BuildTreeView(s, carry, nil, ModeUncached)
	if err != nil {
		t.Fatal(err)
	}
	if uncached.Total != payload.Tree.CarryUncached {
		t.Errorf("uncached root cost differs: CLI %.2f, viewer %.2f",
			uncached.Total, payload.Tree.CarryUncached)
	}

	// Same children, same order, same numbers -- including the quantity the
	// colour ramp encodes, which is otherwise only legible as a shade.
	if len(v.Children) != len(payload.Tree.Children) {
		t.Fatalf("child count differs: CLI %d, viewer %d",
			len(v.Children), len(payload.Tree.Children))
	}
	for i, c := range v.Children {
		n := payload.Tree.Children[i]
		if c.Name != n.Name {
			t.Errorf("child %d: CLI %q, viewer %q", i, c.Name, n.Name)
		}
		if c.Cost != n.Carry {
			t.Errorf("%s: cost differs: CLI %.2f, viewer %.2f", c.Name, c.Cost, n.Carry)
		}
		if c.Items != n.Items || c.Bytes != n.Bytes || c.Tokens != n.Tokens {
			t.Errorf("%s: size differs between the two views", c.Name)
		}
		if c.ResidentCalls != n.ResidentCalls && !n.Unscaled {
			t.Errorf("%s: the ramp quantity differs: CLI %.2f, viewer %.2f",
				c.Name, c.ResidentCalls, n.ResidentCalls)
		}
		if c.Detail != n.Detail {
			t.Errorf("%s: the tooltip text is not in the CLI output", c.Name)
		}
		if c.Unscaled != n.Unscaled {
			t.Errorf("%s: one view greys this out and the other does not", c.Name)
		}
	}

	// And the interventions table the viewer draws has a command behind it.
	all := BuildWhatIfAll(s, whatIfContextFor(s, carry))
	if len(all.Rows) != len(payload.Interventions) {
		t.Errorf("the viewer shows %d interventions and the CLI %d",
			len(payload.Interventions), len(all.Rows))
	}
	viewerNames := map[string]bool{}
	for _, i := range payload.Interventions {
		viewerNames[i.Name] = true
	}
	for _, row := range all.Rows {
		if !viewerNames[row.Name] {
			t.Errorf("%s is in the CLI summary but not the viewer's table", row.Name)
		}
	}
}

func TestWhatIfAllRanksBySavingAndPutsUnmeasurableLast(t *testing.T) {
	s := carrySession(t)
	carry := analysis.Carry(s, analysis.Cache(s, analysis.TTL5m))
	all := BuildWhatIfAll(s, whatIfContextFor(s, carry))

	if len(all.Rows) == 0 {
		t.Fatal("there are interventions to run")
	}
	var seenUnmeasurable bool
	var prev float64
	for i, row := range all.Rows {
		if !row.Applicable {
			seenUnmeasurable = true
			// An unmeasurable row is not a zero. Sorting it to zero would put
			// it above every intervention that costs more than it saves.
			if row.Effect != nil {
				t.Errorf("%s is not applicable but reports an effect", row.Name)
			}
			if row.NotMeasurable == "" {
				t.Errorf("%s says nothing can be said but not why", row.Name)
			}
			continue
		}
		if seenUnmeasurable {
			t.Errorf("row %d (%s) is measurable but sorted after an unmeasurable one",
				i, row.Name)
		}
		if row.Share != nil {
			if i > 0 && row.Share.Value < prev {
				t.Errorf("rows are not in order: %s at %.4f after %.4f",
					row.Name, row.Share.Value, prev)
			}
			prev = row.Share.Value
		}
		// Every quoted number arrives with the thing to know before quoting
		// it. The interventions are validated on this; so is the summary.
		if row.Effect != nil && row.Caveat == "" {
			t.Errorf("%s reports an effect with no caveat", row.Name)
		}
		if row.Detail == "" {
			t.Errorf("%s does not say how to see the full result", row.Name)
		}
	}

	// It must not read as a shopping list to add up.
	var warned bool
	for _, n := range all.Notes {
		warned = warned || strings.Contains(n, "not additive")
	}
	if !warned {
		t.Error("the summary must say the rows do not add up")
	}
}

func TestTreeViewJSONNamesItsFieldsForAnAgent(t *testing.T) {
	v, _ := treeViewFixture(t)
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"schema_version", "session", "mode", "path", "here", "children",
		"session_total", "notes",
	} {
		if _, ok := doc[key]; !ok {
			t.Errorf("the tree JSON is missing %q", key)
		}
	}
	child := doc["children"].([]any)[0].(map[string]any)
	for _, key := range []string{
		"name", "cost", "share_of_level", "share_of_session", "retrievals", "at",
	} {
		if _, ok := child[key]; !ok {
			t.Errorf("a child node is missing %q", key)
		}
	}
	// `at` is what makes the JSON navigable rather than just readable.
	if child["at"] == "" {
		t.Error("a child must carry the value to pass to --at")
	}
}

func TestRenderTreeViewSaysWhereItIsAndHowToGoDeeper(t *testing.T) {
	v, _ := treeViewFixture(t)
	var b strings.Builder
	if err := RenderTreeView(&b, v); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"At session", "OF LEVEL", "SESSION", "CALLS", "Drill in with:", "--at",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendered level is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "not a picture of the context window") {
		t.Error("the text view must carry the disclaimer too")
	}
}

func TestPercentagesHandleSavingsWhichAreNegative(t *testing.T) {
	// The first version of this compared a signed value against its
	// thresholds, so every saving on the interventions summary printed as
	// "<0.1%" -- the numbers were right and unreadable.
	cases := map[float64]string{
		0: "0%", 0.174: "17%", -0.174: "-17%",
		0.086: "8.6%", -0.086: "-8.6%",
		0.0004: "<0.1%", -0.0004: "-<0.1%",
	}
	for in, want := range cases {
		if got := pctStr(in); got != want {
			t.Errorf("pctStr(%v) = %q, want %q", in, got, want)
		}
	}
}

// whatIfContextFor is the evidence the interventions reason over, assembled
// the way the CLI assembles it so the summary under test is the one shipped.
func whatIfContextFor(s *model.Session, carry analysis.CarryReport) whatif.Context {
	return whatif.Context{
		Session: s,
		Cache:   analysis.Cache(s, analysis.TTL5m),
		Carry:   carry,
		Weights: cost.For(firstModel(s)),
		// The same default as --ratio, so the summary in a test is the summary
		// a reader gets.
		CompressionRatio: 0.5,
	}
}
