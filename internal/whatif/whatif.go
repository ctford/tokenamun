// Package whatif estimates what an intervention would have done to a session
// that already happened.
//
// Everything here is a counterfactual and is labelled as one. Three rules
// constrain every intervention, each because published optimisation claims get
// it wrong (see docs/optimisation-claims.md):
//
//   - Baseline first. The observed quantity an intervention targets is
//     reported before any estimate, because most headline percentages in
//     circulation are properties of the author's baseline rather than of the
//     technique.
//   - Net the cache invalidation. Rewriting context breaks the cached prefix
//     from that point, converting cheap reads into full-price writes, so an
//     intervention can cost more than it saves and must be allowed to.
//   - No reduction without the outcome caveat. We cannot see whether the task
//     still succeeded, and an agent that fails consumes the fewest tokens of
//     all.
package whatif

import (
	"fmt"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
)

// Finding is one line of a result. Most findings are quantities; a few are
// facts that are not numbers, and forcing those into a number would produce
// exactly the meaningless "0.0%" this tool exists to avoid.
type Finding struct {
	Label    string          `json:"label"`
	Quantity *model.Quantity `json:"quantity,omitempty"`
	Text     string          `json:"text,omitempty"`
	Note     string          `json:"note,omitempty"`
}

// Axis is the part of the picture an intervention changes.
//
// Three of them, and the distinction matters because only one reads as a
// discount on the treemap. A box's area is volume times round trips times
// price; an intervention moves exactly one of those factors, and which one
// tells you whether you can point at the boxes it would shrink.
type Axis string

const (
	// AxisVolume is less content. This is the one that is a discount on a box
	// or a set of boxes: the same rectangles, smaller.
	AxisVolume Axis = "volume"
	// AxisRoundTrips is the same content going round fewer times. It changes
	// the shade rather than the area, and which content it touches depends on
	// when that content arrived, which cuts across the hierarchy.
	AxisRoundTrips Axis = "round trips"
	// AxisPrice is the same content going round the same number of times at a
	// different rate. It is not a discount on anything you can point at, and
	// it is the one that can come out negative: a longer cache TTL reprices
	// every write upward as well as saving the rebuilds.
	AxisPrice Axis = "price"
)

// Result separates what we saw from what we computed from what we guessed.
type Result struct {
	Intervention string `json:"intervention"`
	Description  string `json:"description"`
	Applicable   bool   `json:"applicable"`
	// Acts is which factor of the cost this intervention moves. Required on
	// an applicable result: without it a reader cannot tell a discount on
	// visible boxes from a repricing of the whole session.
	Acts Axis `json:"acts_on,omitempty"`
	// Addressable is the part of the session this intervention can touch at
	// all, and Reduction is what it does to that part. The two multiply to
	// the headline's share of the session, which is the identity that makes
	// a table of interventions readable:
	//
	//	addressable share x reduction there = overall effect
	//
	// Separating them is the difference between "this saves 9%" and "this
	// halves a thing that is 17% of your bill". The second tells you whether
	// the ceiling is worth chasing at all, and unlike the first it does not
	// move when you change an assumed ratio.
	Addressable *Addressable `json:"addressable,omitempty"`
	// Reduction is the signed change to the addressable part, as a fraction.
	// Negative is a saving, matching the sign convention on every effect.
	Reduction   float64   `json:"reduction,omitempty"`
	Observed    []Finding `json:"observed"`
	Derived     []Finding `json:"derived"`
	Counterfact []Finding `json:"counterfactual"`
	// Headline is the one number the intervention nominates as its bottom
	// line, so a summary does not have to guess which finding matters. Nil
	// when there is nothing defensible to report.
	Headline *Finding `json:"headline,omitempty"`
	// Caveat is the single most important thing to know before quoting the
	// headline, in a few words.
	//
	// Short on purpose, and the length is enforced. It is a column beside a
	// number in a table of eight rows; a paragraph there is not read, and
	// eight paragraphs are read even less. Say the one thing, and put the
	// argument in CaveatDetail.
	Caveat string `json:"caveat,omitempty"`
	// CaveatDetail is the argument behind the caveat: what makes it true, and
	// what it costs you to ignore. Shown on the intervention's own report,
	// where there is room for it.
	CaveatDetail string `json:"caveat_detail,omitempty"`
	// Unknown lists what cannot be known retrospectively. It is never empty;
	// an intervention that returns none fails a test.
	Unknown []string `json:"unknown"`
	// NotMeasurable explains why, when the evidence is not in this data.
	NotMeasurable string `json:"not_measurable,omitempty"`
}

// Addressable is the slice of the session an intervention can act on.
//
// Name is what to look for in the viewer, so a reader can go and see it. The
// branch names are the vocabulary, which is another reason they have to be
// honest.
type Addressable struct {
	Name    string  `json:"name"`
	CostEIT float64 `json:"cost_eit"`
	// Share is CostEIT against the session's whole cost.
	Share float64 `json:"share_of_session"`
}

// addressable builds the slice, given its cost and the session total.
func addressable(name string, costEIT, sessionTotal float64) *Addressable {
	a := &Addressable{Name: name, CostEIT: costEIT}
	if sessionTotal > 0 {
		a.Share = costEIT / sessionTotal
	}
	return a
}

// Context is the evidence an intervention reasons over.
type Context struct {
	Session *model.Session
	Cache   analysis.CacheReport
	Carry   analysis.CarryReport
	Weights cost.Weights
	// CompressionRatio is the assumed surviving fraction of compressed
	// content. Printed with the result so the reader sees the assumption.
	CompressionRatio float64
	// Total is the session's whole cost, prompt plus output, in EIT. The
	// denominator for an addressable share, computed once so that every
	// intervention divides by the same thing.
	Total float64
	// Replay, when set, measured real compression of this session's tool
	// output instead of assuming a ratio.
	Replay *ReplayResult
	// FileReplay is the same measurement over file content, which is a
	// different population and compresses differently.
	FileReplay *ReplayResult
}

// Intervention estimates one optimisation.
type Intervention interface {
	Name() string
	Describe() string
	Estimate(Context) Result
}

// Builtin returns the interventions that ship with the tool, in the order they
// are worth considering.
func Builtin() []Intervention {
	return []Intervention{
		CacheTTL{},
		RepeatedRetrieval{},
		ClearOnNewTask{},
		OutputCompression{},
		FileCompression{},
		Caveman{},
		RTK{},
		MCPToCLI{},
	}
}

// registered holds interventions supplied from outside the binary. The CLI
// fills it once at startup, before anything reads All().
var registered []Intervention

// Register adds an externally supplied intervention.
//
// A built-in and an extension are the same thing to everything downstream:
// the report table, the JSON output and the treemap all iterate All() and
// cannot tell which is which. That is deliberate. An extension that could
// only produce a second-class row would be a demo rather than an interface.
func Register(i Intervention) { registered = append(registered, i) }

// All returns every intervention, built-in ones first so a report's leading
// rows do not move when someone installs a script.
func All() []Intervention {
	return append(Builtin(), registered...)
}

// Find returns the named intervention.
func Find(name string) (Intervention, error) {
	for _, i := range All() {
		if i.Name() == name {
			return i, nil
		}
	}
	var names []string
	for _, i := range All() {
		names = append(names, i.Name())
	}
	return nil, fmt.Errorf("unknown intervention %q; available: %v", name, names)
}

// obs, der and cf build findings at the right provenance.
func obs(label string, v float64, u model.Unit, note ...string) Finding {
	q := model.Obs(v, u)
	return Finding{Label: label, Quantity: &q, Note: first(note)}
}

func der(label string, v float64, u model.Unit, note ...string) Finding {
	q := model.Der(v, u)
	return Finding{Label: label, Quantity: &q, Note: first(note)}
}

func cf(label string, v float64, u model.Unit, note ...string) Finding {
	q := model.Quantity{Value: v, Unit: u, Prov: model.Counterfactual}
	return Finding{Label: label, Quantity: &q, Note: first(note)}
}

// fact records something observed that is not a number.
func fact(label, text string) Finding {
	return Finding{Label: label, Text: text}
}

func first(notes []string) string {
	if len(notes) > 0 {
		return notes[0]
	}
	return ""
}

// outcomeUnknown is on every result, because it is always true.
const outcomeUnknown = "task_success: not observable from this data. An agent that " +
	"fails the task consumes the fewest tokens of all, so a reduction is not an improvement."

const behaviourUnknown = "behavioural_change: the agent's trajectory is assumed " +
	"identical. It would not have been."

// CaveatLimit is how long a caveat may be. A number rather than a taste, so
// that it is enforced the same way on a built-in and on someone's script.
const CaveatLimit = 64
