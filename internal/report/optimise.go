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

// Optimisation is a hypothetical: a part of the tree, and what it becomes.
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
type Optimisation struct {
	// Label names the row. The caller picks it, since the tool has no idea
	// what the parameters are meant to represent.
	Label string
	// At is the node path, as `tokenamun tree` names it: "cli output", or
	// "cli output/git".
	At []string
	// Becomes is what that node's cost becomes, as a fraction. 0.5 halves
	// it; 1.1 is a change for the worse, which is allowed, because not every
	// change is an improvement and a tool that cannot say so is no use for
	// deciding.
	Becomes float64
	// Why is the caveat. Required: you are the only one who knows why the
	// figure is plausible, and a number without that is what this tool
	// exists to avoid.
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
	// Applies is the node, by name, so a reader can go and look at it.
	Applies string `json:"applies_to"`
	// Addressable is the node's cost, and AddressableShare that against the
	// session. Observed.
	Addressable      float64 `json:"addressable_eit"`
	AddressableShare float64 `json:"addressable_share"`
	// Becomes is what the caller said the part becomes, and Impact what the
	// session becomes. The two compose: impact = 1 - addressable x (1 -
	// becomes). Nothing here is signed, because a sign beside a percentage
	// reads as an annotation rather than as arithmetic.
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

	Caveat  string   `json:"caveat"`
	Unknown []string `json:"unknown"`
	Notes   []string `json:"notes"`
}

// ParseOptimisation builds one from the command line.
//
// becomes is a pointer because zero is a figure you might mean -- it removes
// the part entirely -- so a flag left off cannot be told from a flag set to
// its default. Forgetting --optimise used to report the most extreme
// counterfactual the tool can state, which is the opposite of the point.
func ParseOptimisation(at string, becomes *float64, label, why string) (Optimisation, error) {
	if at == "" {
		return Optimisation{}, fmt.Errorf("--at is required: name the part of the tree, " +
			"as `tokenamun tree` names it")
	}
	if becomes == nil {
		return Optimisation{}, fmt.Errorf("--optimise is required: say what %q becomes, "+
			"since the figure is yours and not something this tool can measure. "+
			"0.5 halves it, 0 removes it, 1.1 is a change for the worse", at)
	}
	if *becomes < 0 {
		return Optimisation{}, fmt.Errorf("--optimise %.2f is negative; it is what the "+
			"part becomes, so 0.5 halves it and 0 removes it", *becomes)
	}
	if *becomes == 1 {
		return Optimisation{}, fmt.Errorf("--optimise 1 changes nothing: it is what the " +
			"part becomes, not how much comes off")
	}
	if strings.TrimSpace(why) == "" {
		return Optimisation{}, fmt.Errorf("--why is required: you are the only one who " +
			"knows why this figure is plausible, and a number without that is what " +
			"this tool exists to avoid")
	}
	if n := len([]rune(why)); n > CaveatLimit {
		return Optimisation{}, fmt.Errorf("--why is %d characters and the limit is %d: "+
			"it shares a line with a number", n, CaveatLimit)
	}
	if label == "" {
		label = "optimisation"
	}
	return Optimisation{Label: label, At: splitPath(at), Becomes: *becomes, Why: why}, nil
}

// BuildHypotheticalFrom prices a change to a tree that is already built, so
// "all" can be optimised the same way one session can.
func BuildHypotheticalFrom(tree *Node, info SessionInfo, o Optimisation) (
	Hypothetical, error) {
	node, path, err := resolve(tree, o.At)
	if err != nil {
		return Hypothetical{}, err
	}
	// The root's own cost is the total, which is true of a merged tree as
	// much as of one session's: the remainder node exists so the parts
	// reconcile with the measured bill.
	total := tree.Carry
	if node.Carry <= 0 {
		return Hypothetical{}, fmt.Errorf("%s cost nothing in this session, so there is "+
			"nothing there to optimise", pathOrRoot(path))
	}

	h := Hypothetical{
		SchemaVersion: SchemaVersion,
		Session:       info,
		Name:          o.Label,
		Applies:       pathOrRoot(path),
		Addressable:   node.Carry,
		Becomes:       o.Becomes,
		Saving:        node.Carry * (o.Becomes - 1),
		Tokens:        node.Tokens,
		Retrievals:    node.Items,
		RoundTrips:    node.RoundTrips,
		Caveat:        o.Why,
		Unknown: []string{
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
		},
		Notes: []string{
			"The addressable part is measured. What it becomes is yours, and so is the reason it is plausible.",
			"Impact is what the session becomes, and it composes: 1 - addressable x (1 - becomes).",
			"Carrying less content also shrinks every prefix rebuild, which is already inside the node's cost.",
		},
	}
	if total > 0 {
		h.AddressableShare = node.Carry / total
		h.Impact = 1 + h.Saving/total
	}
	return h, nil
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
	fmt.Fprintf(b, "  Its cost           %14s   [observed]\n", num(int(h.Addressable)))
	fmt.Fprintf(b, "  Addressable        %14s   [derived]   (share of the session)\n",
		pctStr(h.AddressableShare))
	fmt.Fprintf(b, "  Content there      %14s   [derived]\n", num(int(h.Tokens)))
	fmt.Fprintf(b, "  Retrievals         %14s   [observed]\n", num(h.Retrievals))
	if h.RoundTrips > 0 {
		fmt.Fprintf(b, "  Round trips        %14s   [derived]\n", num(int(h.RoundTrips)))
	}
	b.WriteString("\n")

	b.WriteString("Counterfactual\n")
	fmt.Fprintf(b, "  Optimisation       %14s   [%s]     (what that part becomes)\n",
		remainingStr(h.Becomes-1), model.Given)
	fmt.Fprintf(b, "  Saving             %14s   [counterfactual]\n", num(int(h.Saving)))
	fmt.Fprintf(b, "  Impact             %14s   [counterfactual]  (what the session becomes)\n\n",
		remainingStr(h.Impact-1))

	fmt.Fprintf(b, "Caveat\n  %s\n\n", wrap(h.Caveat, 72, "  "))

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
