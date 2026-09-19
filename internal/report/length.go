package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// Lengths is what a call cost, against how many calls the session made.
//
// It exists because the variable a hypothetical most often turns on had no
// view in this tool. `optimise` demands a reason for every figure, and the
// figure people reach for is a per-call saving projected across a period --
// which is only sound if a call costs the same in a long session as in a
// short one. It does not: the model has no memory, so every call re-sends
// everything still resident, and a call late in a long session is carrying
// what the first two hundred put there.
//
// The neighbours do not answer it. `series` compares probe runs of the same
// step, and `compare` puts two sessions side by side; neither says what
// happens across a population of sessions of different lengths.
//
// Aggregated by day the relationship looks like a straight line, because a
// day is a mixture of session lengths and the long ones dominate the totals.
// Fitted at that level it has produced a published saving that the
// session-level figures then contradicted. So this bins sessions, not days,
// and prints the bins rather than a curve through them: the saturation is
// there to be read, and nothing here asserts where it starts.
type Lengths struct {
	SchemaVersion int    `json:"schema_version"`
	Window        string `json:"window"`
	// Sessions is how many were binned, and Unreadable the ones that were
	// not: a distribution over an unknown population is not a distribution.
	Sessions   model.Quantity `json:"sessions"`
	Bins       []LengthBin    `json:"bins"`
	Unreadable []string       `json:"unreadable,omitempty"`
	// MixedPricing is true when the set spans more than one model's pricing,
	// which makes every EIT figure here a sum of differently-sized units.
	MixedPricing bool            `json:"mixed_pricing,omitempty"`
	Warnings     []model.Warning `json:"warnings,omitempty"`
	Notes        []string        `json:"notes"`
}

// LengthBin is one band of session length.
type LengthBin struct {
	// Calls is the band as a reader reads it: "30-99", "1000+".
	Calls string `json:"calls"`
	From  int    `json:"from_calls"`
	// To is absent on the open-ended band.
	To       int            `json:"to_calls,omitempty"`
	Sessions model.Quantity `json:"sessions"`
	APICalls model.Quantity `json:"api_calls"`
	Cost     model.Quantity `json:"cost_eit"`
	// PerCall is the band's cost divided by the band's calls: what a call in
	// a session of this length cost, pooled over the band.
	PerCall model.Quantity `json:"cost_per_call_eit"`
	// Lowest and Highest are the cheapest and dearest single session's own
	// per-call rate in the band. Two observations rather than a spread
	// statistic, because a band routinely holds three sessions and a
	// percentile over three is the middle one with a decimal point on it.
	Lowest  model.Quantity `json:"lowest_session_cost_per_call_eit"`
	Highest model.Quantity `json:"highest_session_cost_per_call_eit"`
}

// lengthBands are the bands, fixed and stated.
//
// Geometric, because session length spans three orders of magnitude and equal
// bands would put almost everything in the first one. Fixed rather than
// chosen from the data, because a band boundary picked to suit the numbers is
// the first step of fitting a shape to them, and the boundary would then move
// between two runs of the same command.
var lengthBands = []struct {
	label    string
	from, to int
}{
	{"1-9", 1, 9},
	{"10-29", 10, 29},
	{"30-99", 30, 99},
	{"100-299", 100, 299},
	{"300-999", 300, 999},
	{"1000+", 1000, 0},
}

// BuildLengths bins every session it can read by how many calls it made.
func BuildLengths(refs []model.SessionRef, window string,
	load func(model.SessionRef) (*model.Session, error)) Lengths {
	l := Lengths{SchemaVersion: SchemaVersion, Window: window, Notes: lengthNotes()}

	type sample struct {
		calls   int
		cost    float64
		perCall float64
	}
	byBand := make([][]sample, len(lengthBands))
	models := map[string]bool{}
	var binned int
	for _, ref := range refs {
		s, err := load(ref)
		if err != nil {
			l.Unreadable = append(l.Unreadable, ref.ID+": "+err.Error())
			continue
		}
		calls := s.RealCalls()
		if calls == 0 {
			// No calls is not a session of length zero; it is a transcript
			// with no accounting in it, and a cost-per-call over it is a
			// division by nothing.
			l.Unreadable = append(l.Unreadable, ref.ID+": no API calls in this transcript")
			continue
		}
		p := BuildProfile(s)
		cost := p.Usage.TotalCost.Value
		for _, m := range s.Models() {
			if m != model.SyntheticModel {
				models[m] = true
			}
		}
		i := bandOf(calls)
		byBand[i] = append(byBand[i], sample{calls: calls, cost: cost, perCall: cost / float64(calls)})
		binned++
	}
	l.Sessions = model.Obs(float64(binned), model.Calls)
	l.MixedPricing = len(models) > 1
	if l.MixedPricing {
		l.Warnings = append(l.Warnings, model.Warning{
			Code: "mixed_pricing",
			Detail: "This set spans more than one model, and a cost-weighted token is " +
				"relative to a model's own input price. Bands holding different models " +
				"are not strictly comparable with each other.",
		})
	}

	for i, band := range lengthBands {
		samples := byBand[i]
		if len(samples) == 0 {
			continue
		}
		var calls int
		var cost, lowest, highest float64
		for j, s := range samples {
			calls += s.calls
			cost += s.cost
			if j == 0 || s.perCall < lowest {
				lowest = s.perCall
			}
			if s.perCall > highest {
				highest = s.perCall
			}
		}
		l.Bins = append(l.Bins, LengthBin{
			Calls: band.label, From: band.from, To: band.to,
			Sessions: model.Obs(float64(len(samples)), model.Calls),
			APICalls: model.Obs(float64(calls), model.Calls),
			Cost:     model.Der(cost, model.EIT),
			PerCall:  model.Der(cost/float64(calls), model.EIT),
			Lowest:   model.Der(lowest, model.EIT),
			Highest:  model.Der(highest, model.EIT),
		})
	}
	return l
}

// bandOf finds the band a session's call count falls in. The last band is
// open-ended, so anything past the others lands there.
func bandOf(calls int) int {
	for i, b := range lengthBands {
		if b.to == 0 || calls <= b.to {
			return i
		}
	}
	return len(lengthBands) - 1
}

func lengthNotes() []string {
	return []string{
		"Cost is cost-weighted tokens, and cost per call is a band's cost divided by its " +
			"calls. Per call, not per session, because sessions in a band still differ in " +
			"length by up to three times.",
		"The lowest and highest columns are two real sessions' own per-call rates, not a " +
			"spread. They say how much of a band's figure is the band and how much is one " +
			"session in it.",
		"Nothing is fitted. The bands are fixed and geometric, and no line is drawn " +
			"through them: where a rise stops rising is for you to read off the table.",
		"Why a call costs more in a longer session is residency, not inefficiency: the " +
			"model has no memory, so everything still in the context is re-sent on every " +
			"call. `tokenamun carry` is where that cost is broken down.",
		"Bin by session, never by day. A day is a mixture of session lengths, the long " +
			"sessions dominate its totals, and a per-call figure fitted at that level has " +
			"already produced a saving that the session-level figures contradicted.",
		"A band holding one session is that session, labelled as a band. The session " +
			"count is the column to read first.",
	}
}

// RenderLengths writes the bands as a table.
func RenderLengths(w io.Writer, l Lengths) error {
	b := &strings.Builder{}
	b.WriteString("TOKENAMUN  what a call cost, by how long the session ran\n\n")
	fmt.Fprintf(b, "%s sessions, %s\n\n", num(int(l.Sessions.Value)), l.Window)

	if len(l.Bins) == 0 {
		b.WriteString("No sessions with API calls in them, so there is nothing to bin.\n\n")
		_, err := io.WriteString(w, b.String())
		return err
	}

	fmt.Fprintf(b, "%-10s %9s %9s %14s %12s %12s %12s\n",
		"CALLS", "SESSIONS", "CALLS", "COST (EIT)", "PER CALL", "LOWEST", "HIGHEST")
	for _, bin := range l.Bins {
		fmt.Fprintf(b, "%-10s %9s %9s %14s %12s %12s %12s\n",
			bin.Calls, num(int(bin.Sessions.Value)), num(int(bin.APICalls.Value)),
			num(int(bin.Cost.Value)), num(int(bin.PerCall.Value)),
			num(int(bin.Lowest.Value)), num(int(bin.Highest.Value)))
	}
	fmt.Fprintf(b, "\nSessions and calls [%s]; costs and rates [%s].\n\n",
		model.Observed, model.Derived)

	for _, warn := range l.Warnings {
		fmt.Fprintf(b, "! %s\n\n", wrap(warn.Detail, 72, "  "))
	}
	for _, u := range l.Unreadable {
		fmt.Fprintf(b, "! not binned: %s\n", wrap(u, 70, "  "))
	}
	if len(l.Unreadable) > 0 {
		b.WriteString("\n")
	}
	for _, n := range l.Notes {
		fmt.Fprintf(b, "%s\n", wrap(n, 74, ""))
	}
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}
