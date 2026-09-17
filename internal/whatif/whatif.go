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

// Result separates what we saw from what we computed from what we guessed.
type Result struct {
	Intervention string    `json:"intervention"`
	Description  string    `json:"description"`
	Applicable   bool      `json:"applicable"`
	Observed     []Finding `json:"observed"`
	Derived      []Finding `json:"derived"`
	Counterfact  []Finding `json:"counterfactual"`
	// Headline is the one number the intervention nominates as its bottom
	// line, so a summary does not have to guess which finding matters. Nil
	// when there is nothing defensible to report.
	Headline *Finding `json:"headline,omitempty"`
	// Caveat is the single most important thing to know before quoting the
	// headline.
	Caveat string `json:"caveat,omitempty"`
	// Unknown lists what cannot be known retrospectively. It is never empty;
	// an intervention that returns none fails a test.
	Unknown []string `json:"unknown"`
	// NotMeasurable explains why, when the evidence is not in this data.
	NotMeasurable string `json:"not_measurable,omitempty"`
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
	// Replay, when set, measured real compression instead of assuming a ratio.
	Replay *ReplayResult
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
