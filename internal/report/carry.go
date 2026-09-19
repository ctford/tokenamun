package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/model"
)

// Carry reports what it cost to keep content in the context.
type Carry struct {
	SchemaVersion int         `json:"schema_version"`
	Session       SessionInfo `json:"session"`
	// Sessions is how many were summed, and is absent for one. A total over
	// an unknown number of sessions is not a total.
	Sessions     int             `json:"sessions,omitempty"`
	Context      ContextReport   `json:"context"`
	Preamble     PreambleReport  `json:"preamble"`
	Items        []CarryItem     `json:"items"`
	Unattributed model.Quantity  `json:"unattributed_growth_share"`
	Warnings     []model.Warning `json:"warnings,omitempty"`
	Notes        []string        `json:"notes"`
}

// ContextReport is the observed prompt-size trajectory.
type ContextReport struct {
	// Peak is the largest prompt observed. Over a set it is the largest any
	// one session reached, which is a maximum of maxima and not a sum.
	Peak model.Quantity `json:"peak_prompt_tokens"`
	// Final is the last call's prompt, and is absent over a set: a set of
	// sessions has no last context, and reporting one session's as the
	// group's would be picking a member at random.
	Final      *model.Quantity `json:"final_prompt_tokens,omitempty"`
	PromptCost model.Quantity  `json:"prompt_cost"`
	// Resets index into one session's calls, so they are absent over a set
	// for the same reason the item sequence numbers are.
	Resets []int `json:"reset_calls"`
}

// PreambleReport is the cost of everything that existed before any work.
type PreambleReport struct {
	Tokens model.Quantity `json:"tokens"`
	Carry  model.Quantity `json:"carry"`
	Share  model.Quantity `json:"share_of_prompt_cost"`
	Note   string         `json:"note"`
}

// CarryItem is one retrieval priced by residency.
type CarryItem struct {
	// Session names which session this retrieval is from, and is absent when
	// the report covers one. It is what makes the ranking composable: CarryEIT
	// is already priced per call, so rows from different sessions are
	// comparable, but a row you cannot trace back to a session is not
	// actionable.
	Session     string         `json:"session,omitempty"`
	Tool        string         `json:"tool"`
	Path        string         `json:"path,omitempty"`
	Tokens      model.Quantity `json:"tokens"`
	EnteredAt   int            `json:"entered_at_call"`
	ResidentFor model.Quantity `json:"resident_for_calls"`
	ColdCalls   model.Quantity `json:"cold_calls"`
	CarryCost   model.Quantity `json:"carry_cost"`
}

// CarryItemsShown is how many retrievals the ranking lists. Enough that the
// tail of a bad session is visible, short enough to read in one screen.
const CarryItemsShown = 15

// quantity takes the address of a quantity, for the fields that are absent
// rather than zero when a report covers a set.
func quantity(q model.Quantity) *model.Quantity { return &q }

// BuildCarry computes the carry report.
func BuildCarry(s *model.Session, c analysis.CarryReport) Carry {
	r := Carry{
		SchemaVersion: SchemaVersion,
		Session:       sessionInfo(s),
		Context: ContextReport{
			Peak:       model.Obs(float64(c.Peak), model.Tokens),
			Final:      quantity(model.Obs(float64(c.Final), model.Tokens)),
			PromptCost: model.Der(c.PromptCostEIT, model.EIT),
			Resets:     c.Resets,
		},
		Preamble: PreambleReport{
			Tokens: model.Obs(float64(c.Preamble), model.Tokens),
			Carry:  model.Der(c.PreambleCarryEIT, model.EIT),
			Share:  model.Der(c.PreambleShare, model.Ratio),
			Note: "The preamble is the first call's whole prompt: system prompt, " +
				"tool schemas, instruction files and skills together. It cannot be " +
				"decomposed, because none of those are in the transcript.",
		},
		Unattributed: model.Der(c.Unattributed, model.Ratio),
		Warnings:     s.Warnings,
		Notes: []string{
			"Carry is the cost of re-sending content on later calls, not the cost of fetching it.",
			"The call a retrieval entered at is what separates two retrievals of the same size: content that arrives early is re-sent for the rest of the session, content that arrives at the end is written once.",
			"Cold calls rebuilt the prefix and were billed at the cache write rate; warm calls were read at a tenth of input price.",
			"Per-item cache class is not directly observable: the API reports one split per call, so residency is priced per call -- at that call's own model as well as its own cache class.",
		},
	}

	for i, it := range c.Items {
		if i >= CarryItemsShown {
			break
		}
		r.Items = append(r.Items, CarryItem{
			Tool: it.Tool, Path: it.Path,
			Tokens:      model.Quantity{Value: it.Tokens, Unit: model.Tokens, Prov: model.DerivedApprox},
			EnteredAt:   it.EnteredAt,
			ResidentFor: model.Obs(float64(it.ResidentFor), model.Calls),
			ColdCalls:   model.Der(float64(it.ColdCalls), model.Calls),
			CarryCost:   model.Der(it.CarryEIT, model.EIT),
		})
	}
	return r
}

// RenderCarry writes the human-facing carry report.
func RenderCarry(w io.Writer, r Carry) error {
	b := &strings.Builder{}
	b.WriteString("TOKENAMUN  cost of carry\n\n")
	fmt.Fprintf(b, "Session %s\n", r.Session.ID)
	if r.Sessions > 1 {
		fmt.Fprintf(b, "  Sessions           %s\n", num(r.Sessions))
	}
	fmt.Fprintf(b, "  API calls          %s\n\n", num(r.Session.Calls))

	b.WriteString("Context trajectory\n")
	line(b, "  Peak prompt", r.Context.Peak)
	// Absent over a set rather than zero: a set of sessions has no last
	// context.
	if r.Context.Final != nil {
		line(b, "  Final prompt", *r.Context.Final)
	}
	line(b, "  Prompt cost", r.Context.PromptCost)
	if len(r.Context.Resets) > 0 {
		fmt.Fprintf(b, "%-22s %14s   [%s]  (content does not survive these)\n",
			"  Context resets", callList(r.Context.Resets), model.Observed)
	}
	b.WriteString("\n")

	b.WriteString("Session preamble, paid on every call\n")
	line(b, "  Preamble", r.Preamble.Tokens)
	line(b, "  Cost of carrying it", r.Preamble.Carry)
	fmt.Fprintf(b, "%-22s %13.1f%%   [%s]\n", "  Share of prompt cost", r.Preamble.Share.Value*100, r.Preamble.Share.Prov)
	fmt.Fprintf(b, "  %s\n\n", wrap(r.Preamble.Note, 72, "  "))

	if len(r.Items) > 0 {
		b.WriteString("Most expensive to carry (not the largest)\n")
		// ENTERED is the call the content arrived on, and it is the column
		// the size-versus-arrival argument turns on: two retrievals of the
		// same size differ by orders of magnitude depending on it. It was in
		// the JSON and not here, so a reader of the text had the ranking
		// without the reason for it.
		fmt.Fprintf(b, "  %-34s%s %10s %7s %7s %6s %12s\n",
			"CONTENT", carrySessionCol(r, "SESSION"), "TOKENS", "ENTERED",
			"CALLS", "COLD", "CARRY (EIT)")
		for _, it := range r.Items {
			label := it.Path
			if label == "" {
				label = "(" + it.Tool + " output)"
			}
			fmt.Fprintf(b, "  %-34s%s %10s %7s %7s %6s %12s\n",
				trunc(label, 34), carrySessionCol(r, it.Session),
				num(int(it.Tokens.Value)), "call "+itoa(it.EnteredAt),
				num(int(it.ResidentFor.Value)), num(int(it.ColdCalls.Value)),
				num(int(it.CarryCost.Value)))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(b, "%-22s %13.1f%%   [%s]\n", "Unattributed growth", r.Unattributed.Value*100, r.Unattributed.Prov)
	b.WriteString("  System reminders, attachments and message envelopes. Reported\n")
	b.WriteString("  rather than distributed across items.\n\n")

	_, err := io.WriteString(w, b.String())
	return err
}

// carrySessionCol is the session column, present only over a set. One
// session's report already names it in the header, and repeating it on
// fifteen rows would be a column of the same string.
func carrySessionCol(r Carry, id string) string {
	if r.Sessions == 0 {
		return ""
	}
	return fmt.Sprintf(" %-12s", trunc(id, 12))
}

// callList formats call sequence numbers for display.
func callList(seqs []int) string {
	parts := make([]string, 0, len(seqs))
	for _, s := range seqs {
		parts = append(parts, fmt.Sprintf("%d", s))
	}
	return "call " + strings.Join(parts, ", ")
}

// wrap breaks text to width, indenting continuation lines.
func wrap(s string, width int, indent string) string {
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for _, word := range words {
		if cur == "" {
			cur = word
			continue
		}
		if len(cur)+1+len(word) > width {
			lines = append(lines, cur)
			cur = word
			continue
		}
		cur += " " + word
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n"+indent)
}
