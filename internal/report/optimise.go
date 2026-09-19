package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// CaveatLimit is how long a caveat may be.
//
// It shares a line with a number. A paragraph there is not read, so the
// argument goes in the detail, where there is room.
const CaveatLimit = 64

// Optimisation is a hypothetical: parts of the tree, and what each becomes.
//
// This is all that is left of what was a set of named interventions --
// cache-ttl, caveman, rtk, mcp-to-cli and the rest -- and the reason is worth
// recording, because the deletion was the finding.
//
// Everything that shrinks content does the same two things: pick a part of
// the session, and make it smaller. The answer is always the product of that
// part's share and the change, which is Amdahl's law with a token bill
// instead of a runtime. So a named intervention adds nothing but a vendor's
// name and a default ratio, and it adds one thing it should not: the
// appearance that the tool knows something about that vendor. Caveman's
// published figures span 8.5% to 65%, an eight-fold spread; a row reporting
// 13% looked like evidence and was an assumption with a logo on it.
//
// What the tool can do honestly is measure the part and let you name the
// change. The part is measured exactly. The change is yours, and so is the
// reason it is plausible -- which is why --why is required.
//
// Parts, plural, because a real proposal is several changes at once and the
// composition does not survive being done by hand across separate runs: each
// run reports its impact against the untouched session, so the impacts cannot
// be added and the savings can only be added if the parts do not overlap --
// which is a question about the tree, and the tool is the party that can
// answer it.
type Optimisation struct {
	// Label names the row. The caller picks it, since the tool has no idea
	// what the parameters are meant to represent.
	Label string
	Parts []OptimisedPart
}

// OptimisedPart is one node and what it becomes.
type OptimisedPart struct {
	// At is the node path, as `tokenamun tree` names it: "cli output", or
	// "cli output/git".
	At []string
	// Becomes is what that node's cost becomes, as a fraction. 0.5 halves
	// it; 1.1 is a change for the worse, which is allowed, because not every
	// change is an improvement and a tool that cannot say so is no use for
	// deciding.
	Becomes float64
	// Why is the caveat. Required, and required per part: you are the only
	// one who knows why each figure is plausible, and one reason covering
	// three unrelated changes is not a reason for any of them.
	Why string
}

// Hypothetical is what an optimisation would have done.
//
// Four sections, and all four always print. The observed quantities come
// first because the addressable part is the measured half of the answer; the
// counterfactual is labelled as one; and the unknown section is not
// decoration -- it is the reason the number above it is not a promise.
type Hypothetical struct {
	SchemaVersion int         `json:"schema_version"`
	Session       SessionInfo `json:"session"`
	Name          string      `json:"name"`
	// Applies is the node, by name, so a reader can go and look at it; every
	// node, where there is more than one.
	Applies string `json:"applies_to"`
	// Addressable is the parts' cost, and the two shares are that against the
	// two totals a reader might have in mind. Observed.
	Addressable float64 `json:"addressable_eit"`
	// AddressableShare is against the session's total cost, prompt and output
	// together. AddressableShareOfPrompt is against prompt cost alone.
	//
	// Both, each naming its denominator, because they are different numbers
	// for the same part and nothing in a bare percentage says which one you
	// are reading. `cache` reports shares of prompt cost; this reported a
	// share of the total and called it "share of the session". Put into one
	// column by a reader, those compared two quantities with different
	// denominators pointing in opposite directions, and the column was wrong
	// without looking wrong.
	AddressableShare         float64 `json:"addressable_share"`
	AddressableShareOfPrompt float64 `json:"addressable_share_of_prompt_cost"`
	// Becomes is what the caller said the parts become, together, and Impact
	// what the session becomes. The two compose: impact = 1 - addressable x
	// (1 - becomes). Nothing here is signed, because a sign beside a
	// percentage reads as an annotation rather than as arithmetic.
	Becomes float64 `json:"becomes"`
	Impact  float64 `json:"impact"`
	// Saving is the change in cost-weighted tokens. Negative is a saving,
	// which is the one place a sign is the clearer form.
	Saving float64 `json:"saving_eit"`
	// Tokens, Retrievals and RoundTrips describe the part, since what you
	// can do about it depends on what it is made of.
	Tokens     float64 `json:"tokens"`
	Retrievals int     `json:"retrievals"`
	RoundTrips float64 `json:"round_trips,omitempty"`
	// Parts is each node on its own. Present even for a single node, so a
	// consumer has one shape to read rather than two.
	Parts []HypotheticalPart `json:"parts"`

	Caveat  string   `json:"caveat"`
	Unknown []string `json:"unknown"`
	Notes   []string `json:"notes"`
}

// HypotheticalPart is one node's half of the answer.
type HypotheticalPart struct {
	Applies                  string  `json:"applies_to"`
	Addressable              float64 `json:"addressable_eit"`
	AddressableShare         float64 `json:"addressable_share"`
	AddressableShareOfPrompt float64 `json:"addressable_share_of_prompt_cost"`
	Becomes                  float64 `json:"becomes"`
	Saving                   float64 `json:"saving_eit"`
	Tokens                   float64 `json:"tokens"`
	Retrievals               int     `json:"retrievals"`
	RoundTrips               float64 `json:"round_trips,omitempty"`
	Caveat                   string  `json:"caveat"`
}

// ParseOptimisation builds one from the command line.
//
// The three flags are read as columns of one table: the first --at goes with
// the first --optimise and the first --why. A mismatched count is an error
// rather than a best guess, because both plausible guesses -- reuse the last
// --why, apply one figure to every node -- produce a report that looks
// deliberate and says something the caller did not.
//
// becomes is a slice rather than a value because zero is a figure you might
// mean -- it removes the part entirely -- so a flag left off cannot be told
// from a flag set to its default. Forgetting --optimise used to report the
// most extreme counterfactual the tool can state, which is the opposite of
// the point.
func ParseOptimisation(at []string, becomes []float64, why []string, label string) (
	Optimisation, error) {
	if len(at) == 0 {
		return Optimisation{}, fmt.Errorf("--at is required: name the part of the tree, " +
			"as `tokenamun tree` names it")
	}
	if len(becomes) == 0 {
		return Optimisation{}, fmt.Errorf("--optimise is required: say what %q becomes, "+
			"since the figure is yours and not something this tool can measure. "+
			"0.5 halves it, 0 removes it, 1.1 is a change for the worse", at[0])
	}
	if len(becomes) != len(at) || len(why) != len(at) {
		return Optimisation{}, fmt.Errorf("%d --at, %d --optimise and %d --why: they are "+
			"read as columns of one table, so each part needs its own figure and its "+
			"own reason", len(at), len(becomes), len(why))
	}
	if label == "" {
		label = "optimisation"
	}
	o := Optimisation{Label: label}
	for i, path := range at {
		if err := checkPart(path, becomes[i], why[i]); err != nil {
			return Optimisation{}, err
		}
		o.Parts = append(o.Parts, OptimisedPart{
			At: splitPath(path), Becomes: becomes[i], Why: why[i],
		})
	}
	return o, nil
}

// checkPart validates one --at/--optimise/--why column.
func checkPart(at string, becomes float64, why string) error {
	switch {
	case at == "":
		return fmt.Errorf("--at is required: name the part of the tree, " +
			"as `tokenamun tree` names it")
	case becomes < 0:
		return fmt.Errorf("--optimise %.2f is negative; it is what the "+
			"part becomes, so 0.5 halves it and 0 removes it", becomes)
	case becomes == 1:
		return fmt.Errorf("--optimise 1 changes nothing: it is what the " +
			"part becomes, not how much comes off")
	case strings.TrimSpace(why) == "":
		return fmt.Errorf("--why is required for %q: you are the only one who "+
			"knows why this figure is plausible, and a number without that is what "+
			"this tool exists to avoid", at)
	}
	if n := len([]rune(why)); n > CaveatLimit {
		return fmt.Errorf("--why is %d characters and the limit is %d: "+
			"it shares a line with a number", n, CaveatLimit)
	}
	return nil
}

// BuildHypotheticalFrom prices a change to a tree that is already built, so
// "all" can be optimised the same way one session can.
func BuildHypotheticalFrom(tree *Node, info SessionInfo, o Optimisation) (
	Hypothetical, error) {
	// The root's own cost is the total, which is true of a merged tree as
	// much as of one session's: the remainder node exists so the parts
	// reconcile with the measured bill.
	total, prompt := tree.Carry, tree.PromptCost
	h := Hypothetical{
		SchemaVersion: SchemaVersion,
		Session:       info,
		Name:          o.Label,
		Unknown:       optimisationUnknowns(),
		Notes:         optimisationNotes(),
	}

	var names, caveats []string
	var resolved [][]string
	for _, p := range o.Parts {
		part, path, err := pricePart(tree, p, total, prompt)
		if err != nil {
			return Hypothetical{}, err
		}
		if err := checkDisjoint(resolved, path); err != nil {
			return Hypothetical{}, err
		}
		resolved = append(resolved, path)
		h.Parts = append(h.Parts, part)
		h.Addressable += part.Addressable
		h.Saving += part.Saving
		h.Tokens += part.Tokens
		h.Retrievals += part.Retrievals
		// Weighted by tokens, the way a branch's round trips are: a plain
		// average over parts of different sizes is a number about nothing.
		h.RoundTrips += part.RoundTrips * part.Tokens
		names = append(names, part.Applies)
		caveats = append(caveats, part.Caveat)
	}

	h.RoundTrips = share(h.RoundTrips, h.Tokens)
	h.Applies = strings.Join(names, ", ")
	h.Caveat = strings.Join(caveats, "; ")
	h.AddressableShare = share(h.Addressable, total)
	h.AddressableShareOfPrompt = share(h.Addressable, prompt)
	// What the addressable parts become together: the figure that composes
	// with the share to give the impact. For a single part it is simply what
	// the caller said.
	h.Becomes = 1 + share(h.Saving, h.Addressable)
	h.Impact = 1 + share(h.Saving, total)
	return h, nil
}

// pricePart resolves one node and prices the change to it.
func pricePart(tree *Node, p OptimisedPart, total, prompt float64) (
	HypotheticalPart, []string, error) {
	node, path, err := resolve(tree, p.At)
	if err != nil {
		return HypotheticalPart{}, nil, err
	}
	if node.Carry <= 0 {
		return HypotheticalPart{}, nil, fmt.Errorf(
			"%s cost nothing in this session, so there is nothing there to optimise",
			pathOrRoot(path))
	}
	return HypotheticalPart{
		Applies:                  pathOrRoot(path),
		Addressable:              node.Carry,
		AddressableShare:         share(node.Carry, total),
		AddressableShareOfPrompt: share(node.Carry, prompt),
		Becomes:                  p.Becomes,
		Saving:                   node.Carry * (p.Becomes - 1),
		Tokens:                   node.Tokens,
		Retrievals:               node.Items,
		RoundTrips:               node.RoundTrips,
		Caveat:                   p.Why,
	}, path, nil
}

// share divides, and is zero rather than an infinity when there is nothing to
// divide by.
func share(part, whole float64) float64 {
	if whole == 0 {
		return 0
	}
	return part / whole
}

// checkDisjoint refuses two parts where one contains the other.
//
// Optimising "cli output" and "cli output/git" in one command reads as two
// changes and is one change counted twice: git's cost is already inside cli
// output's, so the savings add to more than either change could produce. The
// composition is only true over parts that do not overlap, and the tool is
// the only party in a position to know whether they do.
func checkDisjoint(already [][]string, path []string) error {
	for _, prev := range already {
		if within(prev, path) || within(path, prev) {
			return fmt.Errorf("%q and %q overlap: one is inside the other, so their "+
				"costs are the same tokens counted twice. Optimise the outer node, or "+
				"the inner ones separately", pathOrRoot(prev), pathOrRoot(path))
		}
	}
	return nil
}

// within reports whether outer is a prefix of inner, which is what "inside"
// means for a tree path.
func within(outer, inner []string) bool {
	if len(outer) > len(inner) {
		return false
	}
	for i, name := range outer {
		if !strings.EqualFold(name, inner[i]) {
			return false
		}
	}
	return true
}

func optimisationUnknowns() []string {
	return []string{
		"the figure: it is one you supplied, not a measurement. Whatever would " +
			"achieve it has costs of its own, and they are not here.",
		"sufficiency: content removed is content the agent cannot read. If it " +
			"needed any of it, it fetches something else instead and the saving " +
			"becomes extra calls.",
		"cache invalidation: rewriting context breaks the cached prefix from that " +
			"point, turning cheap reads into full-price writes. Not netted out.",
		"the trajectory: the agent is assumed to have behaved identically. It " +
			"would not have.",
		"whether the work still came out right: not observable from this data. An " +
			"agent that fails the task consumes the fewest tokens of all, so a " +
			"reduction is not an improvement.",
	}
}

func optimisationNotes() []string {
	return []string{
		"The addressable part is measured. What it becomes is yours, and so is the reason it is plausible.",
		"Impact is what the session becomes, and it composes: 1 - addressable x (1 - becomes).",
		"Carrying less content also shrinks every prefix rebuild, which is already inside the node's cost.",
		"Two denominators, both printed: the session's total cost, prompt and output together, and " +
			"prompt cost alone. `cache` reports shares of prompt cost, so that is the one its figures " +
			"are comparable with.",
		"Repeat --at, --optimise and --why to price several changes at once. The parts are checked not " +
			"to contain one another, because overlapping parts are the same tokens counted twice.",
	}
}

// splitPath turns "cli output/git" into the levels the tree resolves.
func splitPath(at string) []string {
	if at == "" {
		return nil
	}
	return strings.Split(at, "/")
}

// RenderHypothetical writes the result.
func RenderHypothetical(w io.Writer, h Hypothetical) error {
	b := &strings.Builder{}
	fmt.Fprintf(b, "TOKENAMUN  hypothetical: %s\n\n", h.Name)
	fmt.Fprintf(b, "Session %s, %s API calls\n\n", h.Session.ID, num(h.Session.Calls))

	b.WriteString("Observed\n")
	fmt.Fprintf(b, "  Applies to         %14s\n", h.Applies)
	fmt.Fprintf(b, "  Addressable        %14s   [observed]  (what that part cost)\n",
		num(int(h.Addressable)))
	// Both denominators, each named. A percentage whose base is not on the
	// line is how a reader came to put this in one column beside `cache`'s
	// share of prompt cost and compare the two.
	fmt.Fprintf(b, "  Share of session   %14s   [derived]   (of total cost: prompt and output)\n",
		pctStr(h.AddressableShare))
	fmt.Fprintf(b, "  Share of prompt    %14s   [derived]   (the base `cache` reports against)\n",
		pctStr(h.AddressableShareOfPrompt))
	fmt.Fprintf(b, "  Content there      %14s   [derived]\n", num(int(h.Tokens)))
	fmt.Fprintf(b, "  Retrievals         %14s   [observed]\n", num(h.Retrievals))
	if h.RoundTrips > 0 {
		fmt.Fprintf(b, "  Round trips        %14s   [derived]\n", num(int(h.RoundTrips)))
	}
	b.WriteString("\n")

	renderParts(b, h)

	b.WriteString("Counterfactual\n")
	fmt.Fprintf(b, "  Optimisation       %14s   [%s]     (what that part becomes)\n",
		remainingStr(h.Becomes-1), model.Given)
	fmt.Fprintf(b, "  Saving             %14s   [counterfactual]\n", num(int(h.Saving)))
	fmt.Fprintf(b, "  Impact             %14s   [counterfactual]  (what the session becomes)\n\n",
		remainingStr(h.Impact-1))

	renderCaveats(b, h)

	b.WriteString("Unknown\n")
	for _, u := range h.Unknown {
		fmt.Fprintf(b, "  - %s\n", wrap(u, 70, "    "))
	}
	b.WriteString("\n")
	for _, n := range h.Notes {
		fmt.Fprintf(b, "%s\n", wrap(n, 74, ""))
	}
	b.WriteString("\n")

	_, err := io.WriteString(w, b.String())
	return err
}

// renderCaveats prints every part's reason, named where there is more than
// one. A caveat that cannot be told apart from the next part's is not a
// caveat about anything.
func renderCaveats(b *strings.Builder, h Hypothetical) {
	b.WriteString("Caveat\n")
	for _, p := range h.Parts {
		if len(h.Parts) == 1 {
			fmt.Fprintf(b, "  %s\n", wrap(p.Caveat, 72, "  "))
			continue
		}
		fmt.Fprintf(b, "  %s: %s\n", p.Applies, wrap(p.Caveat, 70, "    "))
	}
	b.WriteString("\n")
}

// renderParts breaks the answer down by node, and only when there is more
// than one: for a single part every column repeats what is above it.
func renderParts(b *strings.Builder, h Hypothetical) {
	if len(h.Parts) < 2 {
		return
	}
	b.WriteString("Parts\n")
	fmt.Fprintf(b, "  %-32s %12s %9s %9s %12s\n",
		"NODE", "COST", "SESSION", "BECOMES", "SAVING")
	for _, p := range h.Parts {
		fmt.Fprintf(b, "  %-32s %12s %9s %9s %12s\n",
			trunc(p.Applies, 32), num(int(p.Addressable)), pctStr(p.AddressableShare),
			remainingStr(p.Becomes-1), num(int(p.Saving)))
	}
	b.WriteString("\n")
}
