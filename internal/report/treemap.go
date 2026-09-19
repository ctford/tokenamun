package report

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
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
	// Title is set by the caller, so an agent generating this for a
	// particular repository can say whose session it is.
	Title   string         `json:"title"`
	Session treemapSession `json:"session"`
	Tree    *Node          `json:"tree"`
	// RampMax is the top of the colour ramp, in round trips. Taken from the
	// tree, so the scale covers exactly what the viewer can draw.
	RampMax float64 `json:"rampMax"`
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
	return BuildTreemapFrom(BuildTree(s, carry), treemapSession{
		ID: s.Ref.ID, Calls: s.RealCalls(), Origin: string(s.Ref.Origin),
	}, title)
}

// BuildTreemapFrom renders a tree that is already built.
//
// So a team's whole history draws the same picture one session does: `all`
// merges every session's tree and hands it here.
func BuildTreemapFrom(tree *Node, session treemapSession, title string) TreemapPayload {
	noteAggregates(tree)
	if title == "" {
		title = "Tokenamun"
	}
	return TreemapPayload{
		Title:   title,
		Session: session,
		Tree:    tree,
		RampMax: maxRoundTrips(tree),
	}
}

// TreemapSession describes what a payload covers, for callers outside this
// package that have merged several sessions.
func TreemapSession(id string, calls int, origin string) treemapSession {
	return treemapSession{ID: id, Calls: calls, Origin: origin}
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
