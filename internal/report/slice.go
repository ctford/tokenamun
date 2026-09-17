package report

import (
	"fmt"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
	"github.com/ctford/tokenamun/internal/whatif"
)

// Slice is an intervention described rather than implemented: a part of the
// tree, and how much smaller it would be.
//
// This is Amdahl's law applied to a token bill. Everything that shrinks
// content does the same two things -- pick a slice, cut it by a fraction --
// and the answer is always the product of the slice's share and the cut. The
// vendor, the mechanism and the marketing vary; the arithmetic does not.
//
// So the tool does not need to model Caveman, or the next thing. An agent
// that wants to ask "what if the shell output were halved" says which node
// and how much, and gets a row that sits in the same table as the built-ins,
// under whatever name it chooses. What the tool keeps in Go is the handful of
// interventions that are *not* a slice and a fraction: cache-ttl changes the
// price rather than the volume, clear-on-new-task changes the round trips,
// repeated-retrieval identifies its own slice by content hash, and RTK
// carries published per-family figures that are evidence rather than an
// assumption.
//
// It lives in this package because the tree does, and the whole point is that
// the slice is named the way the viewer names it: what you can point at in
// the picture is what you can ask about.
type Slice struct {
	// Label names the row. An agent generating a report picks it, since the
	// tool has no idea what the parameters are meant to represent.
	Label string
	// At is the node path, in the same form the tree command takes:
	// "CLI output", or "CLI output/git".
	At []string
	// Cut is the fraction of that node's cost the intervention removes.
	// 0.5 halves it. Negative would be an increase, which is allowed: not
	// every change is an improvement, and a tool that cannot express that
	// is not much use for deciding.
	Cut float64
	// Why is the caveat. Required, and held to the same length limit as a
	// built-in's, because a number with no caveat is the failure mode this
	// whole package exists to avoid. The agent supplying the parameters is
	// the only thing that knows why the cut is plausible.
	Why string
}

func (s Slice) Name() string {
	if s.Label == "" {
		return "slice"
	}
	return s.Label
}

func (s Slice) Describe() string {
	return fmt.Sprintf("cut %s by %.0f%%", pathOrRoot(s.At), 100*s.Cut)
}

// Estimate resolves the node and applies the cut.
func (s Slice) Estimate(c whatif.Context) whatif.Result {
	r := whatif.Result{
		Intervention: s.Name(),
		Description:  s.Describe(),
		Unknown: []string{
			"mechanism: this is a fraction you supplied, not a measurement. " +
				"Whatever would achieve it has costs of its own, and they are not here.",
			"sufficiency: content removed is content the agent cannot read. If it " +
				"needed any of it, it fetches something else instead and the saving " +
				"turns into extra calls.",
			"behavioural_change: the agent's trajectory is assumed identical. It " +
				"would not have been.",
			"task_success: not observable from this data. An agent that fails the " +
				"task consumes the fewest tokens of all, so a reduction is not an " +
				"improvement.",
		},
	}

	carry := c.Carry
	node, path, err := resolve(BuildTree(c.Session, carry), s.At)
	if err != nil {
		r.Applicable = false
		r.NotMeasurable = err.Error()
		return r
	}
	total := c.Total
	if total == 0 {
		total = carry.PromptCostEIT + outputCost(c.Session)
	}

	r.Applicable = node.Carry > 0
	r.Acts = whatif.AxisVolume
	if !r.Applicable {
		r.NotMeasurable = fmt.Sprintf("%s cost nothing in this session, so there is "+
			"nothing there to cut", pathOrRoot(path))
		return r
	}

	r.Observed = []whatif.Finding{
		{Label: "node", Text: pathOrRoot(path)},
		obsQ("cost of that node", node.Carry, model.EIT),
		obsQ("content there", node.Tokens, model.Tokens),
		obsQ("retrievals there", float64(node.Items), model.Calls),
		obsQ("prompt cost", carry.PromptCostEIT, model.EIT),
	}

	r.Addressable = &whatif.Addressable{Name: pathOrRoot(path), CostEIT: node.Carry}
	if total > 0 {
		r.Addressable.Share = node.Carry / total
	}
	r.Reduction = -s.Cut

	saved := node.Carry * s.Cut
	r.Derived = []whatif.Finding{
		derQ("cut applied to that node", s.Cut, model.Ratio),
	}
	r.Counterfact = []whatif.Finding{
		cfQ("net change", -saved, model.EIT,
			"the node's cost scaled by the cut. Carrying less content also shrinks "+
				"every prefix rebuild, which is already in the node's cost"),
	}
	if total > 0 {
		r.Counterfact = append(r.Counterfact,
			cfQ("net change, share of session", -saved/total, model.Ratio))
	}
	r.Headline = &r.Counterfact[0]
	r.Caveat = s.Why
	r.CaveatDetail = fmt.Sprintf(
		"The cut is a parameter, not a finding: %s is %s of this session, and this "+
			"assumes %.0f%% of it goes away. Whether anything can actually achieve "+
			"that here is the question this row cannot answer.",
		pathOrRoot(path), pctStr(r.Addressable.Share), 100*s.Cut)
	return r
}

// ParseSlice builds a Slice from the command line.
//
// The caveat is required, and that is the point of asking for it: the agent
// supplying a fraction is the only thing that knows why the fraction is
// plausible, and a row without that is a number somebody will quote.
func ParseSlice(at string, cut float64, label, why string) (Slice, error) {
	if at == "" {
		return Slice{}, fmt.Errorf("--at is required: name the part of the tree to cut, " +
			"as `tokenamun tree` names it")
	}
	if cut <= 0 || cut > 1 {
		return Slice{}, fmt.Errorf("--cut must be between 0 and 1; %.2f is not a fraction "+
			"of the node to remove", cut)
	}
	if strings.TrimSpace(why) == "" {
		return Slice{}, fmt.Errorf("--why is required: a number with no caveat is the " +
			"thing this tool exists to avoid, and you are the only one who knows why " +
			"this cut is plausible")
	}
	if n := len([]rune(why)); n > whatif.CaveatLimit {
		return Slice{}, fmt.Errorf("--why is %d characters and the limit is %d: it is a "+
			"column in a table", n, whatif.CaveatLimit)
	}
	if label == "" {
		label = "slice"
	}
	return Slice{Label: label, At: splitPath(at), Cut: cut, Why: why}, nil
}

// splitPath turns "CLI output/git" into the levels the tree resolves.
func splitPath(at string) []string {
	if at == "" {
		return nil
	}
	return strings.Split(at, "/")
}

// obsQ, derQ and cfQ build findings at the right provenance. The whatif
// package keeps its own unexported versions; these are the same three, for
// the one intervention that lives outside it.
func obsQ(label string, v float64, u model.Unit) whatif.Finding {
	q := model.Obs(v, u)
	return whatif.Finding{Label: label, Quantity: &q}
}

func derQ(label string, v float64, u model.Unit) whatif.Finding {
	q := model.Der(v, u)
	return whatif.Finding{Label: label, Quantity: &q}
}

func cfQ(label string, v float64, u model.Unit, note ...string) whatif.Finding {
	q := model.Quantity{Value: v, Unit: u, Prov: model.Counterfactual}
	f := whatif.Finding{Label: label, Quantity: &q}
	if len(note) > 0 {
		f.Note = note[0]
	}
	return f
}
