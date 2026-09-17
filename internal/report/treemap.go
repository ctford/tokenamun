package report

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
	"github.com/ctford/tokenamun/internal/whatif"
)

//go:embed templates/treemap.html
var templates embed.FS

// dataPlaceholder is substituted with the session's payload. The report is a
// single self-contained file: no CDN, no build step, nothing fetched when it
// is opened.
const dataPlaceholder = "__TOKENAMUN_DATA__"

// TreemapPayload is the data the standalone report renders.
//
// Area encodes observed retrieved-content tokens, or derived carry cost when
// the reader switches mode. It is deliberately NOT a picture of the context
// window, and the report says so in its own footer rather than only here.
type TreemapPayload struct {
	// Title is set by the caller, so an agent generating this for a
	// particular repository can say whose session it is.
	Title            string          `json:"title"`
	Session          treemapSession  `json:"session"`
	Tiles            []treemapTile   `json:"tiles"`
	Interventions    []treemapWhatIf `json:"interventions"`
	Items            []treemapItem   `json:"items"`
	Tree             *Node           `json:"tree"`
	MaxCarryPerToken float64         `json:"maxCarryPerToken"`
	EstimatorNote    string          `json:"estimatorNote"`
}

type treemapSession struct {
	ID     string `json:"id"`
	Calls  int    `json:"calls"`
	Origin string `json:"origin"`
}

type treemapTile struct {
	Label      string `json:"label"`
	Value      string `json:"value"`
	Provenance string `json:"provenance"`
}

// treemapWhatIf is one intervention's bottom line, for the summary table.
// A viewer that shows where the tokens went should also say what would have
// changed it, and the interventions already compute that.
type treemapWhatIf struct {
	Name       string  `json:"name"`
	Targets    string  `json:"targets"`
	Effect     float64 `json:"effect"`
	Share      float64 `json:"share"`
	Applicable bool    `json:"applicable"`
	Note       string  `json:"note"`
	Caveat     string  `json:"caveat"`
}

type treemapItem struct {
	ID            int     `json:"id"`
	Label         string  `json:"label"`
	ShortLabel    string  `json:"shortLabel"`
	Category      string  `json:"category"`
	Tool          string  `json:"tool"`
	Bytes         int     `json:"bytes"`
	Tokens        float64 `json:"tokens"`
	Carry         float64 `json:"carry"`
	CarryPerToken float64 `json:"carryPerToken"`
	EnteredAt     int     `json:"enteredAt"`
	ResidentFor   int     `json:"residentFor"`
	Range         string  `json:"range,omitempty"`
}

// BuildTreemap assembles the payload from a session and its carry analysis.
func BuildTreemap(s *model.Session, carry analysis.CarryReport) TreemapPayload {
	return BuildTreemapTitled(s, carry, "")
}

// BuildTreemapTitled assembles the payload with a caller-supplied title.
func BuildTreemapTitled(s *model.Session, carry analysis.CarryReport, title string) TreemapPayload {
	retrieval := BuildRetrieval(s)

	if title == "" {
		title = "Tokenamun"
	}
	p := TreemapPayload{
		Title: title,
		Session: treemapSession{
			ID: s.Ref.ID, Calls: len(s.Invocations), Origin: string(s.Ref.Origin),
		},
		EstimatorNote: estimatorNote(s),
	}

	p.Tiles = []treemapTile{
		{"Retrieved content", bytesStr(retrieval.Total.Bytes.Value), "observed"},
		{"Estimated tokens", num(int(retrieval.Total.Tokens.Value)), "derived-approx"},
		{"Prompt cost", num(int(carry.PromptCostEIT)), "derived, cost-weighted tokens"},
		{"Retrieved again", bytesStr(retrieval.Total.Redundant.Value), "derived"},
	}
	if retrieval.Total.Withheld.Value > 0 {
		p.Tiles = append(p.Tiles, treemapTile{
			"Withheld by harness", bytesStr(retrieval.Total.Withheld.Value), "observed"})
	}

	p.Interventions = interventionTable(s, carry)

	// Carry is keyed by the retrieval's position, so index it to join.
	carryBySeq := map[string]analysis.CarriedItem{}
	for _, it := range carry.Items {
		carryBySeq[carryItemKey(it.Path, it.Tool, it.EnteredAt)] = it
	}

	for _, c := range s.Retrievals {
		label := c.Path
		if label == "" {
			label = "(" + c.Tool + " output)"
		}
		item := treemapItem{
			ID:         c.Seq,
			Label:      label,
			ShortLabel: shortLabel(label),
			Category:   string(c.Category),
			Tool:       c.Tool,
			Bytes:      c.Bytes,
			Tokens:     c.Tokens,
			EnteredAt:  c.InvocationSeq,
			Range:      rangeOf(c),
		}
		if it, ok := carryBySeq[carryItemKey(c.Path, c.Tool, c.InvocationSeq)]; ok {
			item.Carry = it.CarryEIT
			item.ResidentFor = it.ResidentFor
			if c.Tokens > 0 {
				item.CarryPerToken = it.CarryEIT / c.Tokens
			}
		}
		if item.CarryPerToken > p.MaxCarryPerToken {
			p.MaxCarryPerToken = item.CarryPerToken
		}
		p.Items = append(p.Items, item)
	}

	p.Tree = BuildTree(s, carry)

	// Largest first, so the table reads in the same order the eye scans the
	// treemap.
	sort.SliceStable(p.Items, func(i, j int) bool { return p.Items[i].Tokens > p.Items[j].Tokens })
	return p
}

// estimatorNote says in one line how content tokens were counted, since every
// area in the view depends on it.
func estimatorNote(s *model.Session) string {
	if !s.Estimator.Calibrated {
		return "Content token counts are estimated: " + s.Estimator.Method + "."
	}
	return fmt.Sprintf(
		"Content token counts are estimated at %.2f bytes per token, calibrated against this "+
			"session's own observed prompt growth. Token-class costs are observed.",
		s.Estimator.BytesPerToken)
}

// interventionTable runs every intervention and keeps the one number each
// nominates as its bottom line.
func interventionTable(s *model.Session, carry analysis.CarryReport) []treemapWhatIf {
	cache := analysis.Cache(s, analysis.TTL5m)
	ctx := whatif.Context{
		Session: s, Cache: cache, Carry: carry,
		Weights: cost.For(firstModel(s)), CompressionRatio: 0.5,
	}
	total := carry.PromptCostEIT + cost.For(firstModel(s)).OutputCost(s.Usage())

	var out []treemapWhatIf
	for _, i := range whatif.All() {
		r := i.Estimate(ctx)
		row := treemapWhatIf{
			Name:       r.Intervention,
			Targets:    r.Description,
			Applicable: r.Applicable,
			Caveat:     r.Caveat,
			Note:       r.NotMeasurable,
		}
		if r.Headline != nil && r.Headline.Quantity != nil {
			row.Effect = r.Headline.Quantity.Value
			if r.Headline.Quantity.Unit == model.Ratio {
				row.Share = r.Headline.Quantity.Value
				row.Effect = 0
			} else if total > 0 {
				row.Share = r.Headline.Quantity.Value / total
			}
		}
		out = append(out, row)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Share < out[j].Share })
	return out
}

// carryItemKey identifies a carried item well enough to join it back to the
// retrieval it came from.
func carryItemKey(path, tool string, enteredAt int) string {
	return fmt.Sprintf("%s|%s|%d", path, tool, enteredAt)
}

// shortLabel trims a path to something that fits in a rectangle.
func shortLabel(label string) string {
	if i := strings.LastIndex(label, "/"); i >= 0 && i+1 < len(label) {
		label = label[i+1:]
	}
	if len(label) > 28 {
		return label[:27] + "…"
	}
	return label
}

// RenderTreemap writes the standalone HTML report.
func RenderTreemap(w io.Writer, p TreemapPayload) error {
	tmpl, err := templates.ReadFile("templates/treemap.html")
	if err != nil {
		return err
	}
	payload, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if !strings.Contains(string(tmpl), dataPlaceholder) {
		return fmt.Errorf("treemap template is missing its %s placeholder", dataPlaceholder)
	}
	out := strings.Replace(string(tmpl), dataPlaceholder, string(payload), 1)
	_, err = io.WriteString(w, out)
	return err
}
