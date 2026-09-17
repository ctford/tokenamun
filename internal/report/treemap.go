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
	Title         string          `json:"title"`
	Session       treemapSession  `json:"session"`
	Interventions []treemapWhatIf `json:"interventions"`
	Tree          *Node           `json:"tree"`
	// RampMax is the top of the colour ramp, in round trips. Taken from the
	// tree, so the scale covers exactly what the viewer can draw.
	RampMax float64 `json:"rampMax"`
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

type treemapSession struct {
	ID     string `json:"id"`
	Calls  int    `json:"calls"`
	Origin string `json:"origin"`
}

// BuildTreemap assembles the payload from a session and its carry analysis.
func BuildTreemap(s *model.Session, carry analysis.CarryReport) TreemapPayload {
	return BuildTreemapTitled(s, carry, "")
}

// BuildTreemapTitled assembles the payload with a caller-supplied title.
func BuildTreemapTitled(s *model.Session, carry analysis.CarryReport, title string) TreemapPayload {
	if title == "" {
		title = "Tokenamun"
	}
	p := TreemapPayload{
		Title: title,
		Session: treemapSession{
			ID: s.Ref.ID, Calls: len(s.Invocations), Origin: string(s.Ref.Origin),
		},
	}
	p.Interventions = interventionTable(s, carry)
	p.Tree = BuildTree(s, carry)
	p.RampMax = maxRoundTrips(p.Tree)
	return p
}

// maxRoundTrips is the top of the colour ramp. It comes from the tree
// rather than from the flat retrieval list, so the scale covers exactly the
// nodes the viewer can draw and no others: a ramp topped out by something
// off-screen would make every visible rectangle look pale.
func maxRoundTrips(n *Node) float64 {
	if n == nil {
		return 0
	}
	max := 0.0
	if !n.Unscaled {
		max = n.RoundTrips
	}
	for _, c := range n.Children {
		if m := maxRoundTrips(c); m > max {
			max = m
		}
	}
	return max
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
