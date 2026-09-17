package report

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/model"
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
	Session          treemapSession `json:"session"`
	Tiles            []treemapTile  `json:"tiles"`
	Categories       []treemapCat   `json:"categories"`
	Items            []treemapItem  `json:"items"`
	Tree             *Node          `json:"tree"`
	MaxCarryPerToken float64        `json:"maxCarryPerToken"`
	EstimatorNote    string         `json:"estimatorNote"`
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

type treemapCat struct {
	Name  string  `json:"name"`
	Bytes int     `json:"bytes"`
	Share float64 `json:"share"`
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
	retrieval := BuildRetrieval(s)

	p := TreemapPayload{
		Session: treemapSession{
			ID: s.Ref.ID, Calls: len(s.Invocations), Origin: string(s.Ref.Origin),
		},
		EstimatorNote: "Token counts: " + s.Estimator.Method + ".",
	}
	if s.Estimator.Calibrated {
		p.EstimatorNote += fmt.Sprintf(
			" %.2f bytes per token, %.0f tokens of fixed overhead per call, %.0f%% of observed growth unattributed.",
			s.Estimator.BytesPerToken, s.Estimator.PerCallOverhead, s.Estimator.Residual*100)
	}

	p.Tiles = []treemapTile{
		{"Retrieved content", bytesStr(retrieval.Total.Bytes.Value), "observed"},
		{"Estimated tokens", num(int(retrieval.Total.Tokens.Value)), "derived-approx"},
		{"Prompt cost", num(int(carry.PromptCostEIT)) + " EIT", "derived"},
		{"Retrieved again", bytesStr(retrieval.Total.Redundant.Value), "derived"},
	}
	if retrieval.Total.Withheld.Value > 0 {
		p.Tiles = append(p.Tiles, treemapTile{
			"Withheld by harness", bytesStr(retrieval.Total.Withheld.Value), "observed"})
	}

	for _, c := range retrieval.ByCategory {
		p.Categories = append(p.Categories, treemapCat{
			Name:  string(c.Category),
			Bytes: int(c.Bytes.Value),
			Share: c.Share.Value,
		})
	}

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
