// Package report renders analyses for humans and for agents. Every rendered
// quantity carries its provenance; see METHODOLOGY.md section 1.
package report

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ctford/tokenamun/internal/cost"
	"github.com/ctford/tokenamun/internal/model"
)

// SchemaVersion is the JSON output contract. Agents consume this output, so a
// key change is a breaking change even while the project is experimental.
const SchemaVersion = 1

// Profile is a session overview.
type Profile struct {
	SchemaVersion int             `json:"schema_version"`
	Session       SessionInfo     `json:"session"`
	Usage         UsageReport     `json:"usage"`
	Caching       CachingReport   `json:"caching"`
	Retrieved     RetrievalTotals `json:"retrieved_content"`
	Tools         []ToolSummary   `json:"tools"`
	Warnings      []model.Warning `json:"warnings,omitempty"`
	Notes         []string        `json:"notes"`
}

// SessionInfo identifies what was profiled.
type SessionInfo struct {
	ID       string       `json:"id"`
	Origin   model.Origin `json:"origin"`
	Current  bool         `json:"current"`
	Models   []string     `json:"models"`
	Branch   string       `json:"branch,omitempty"`
	Calls    int          `json:"api_calls"`
	Prompts  int          `json:"user_prompts"`
	Duration string       `json:"duration"`
}

// UsageReport separates volume from cost, which are different quantities.
type UsageReport struct {
	Input           model.Quantity `json:"input"`
	CacheRead       model.Quantity `json:"cache_read"`
	CacheCreation   model.Quantity `json:"cache_creation"`
	Output          model.Quantity `json:"output"`
	Thinking        model.Quantity `json:"thinking"`
	PromptVolume    model.Quantity `json:"prompt_volume"`
	PromptCost      model.Quantity `json:"prompt_cost"`
	OutputCost      model.Quantity `json:"output_cost"`
	TotalCost       model.Quantity `json:"total_cost"`
	VolumeCostRatio model.Quantity `json:"volume_to_cost_ratio"`
}

// CachingReport is the cheapest genuine finding available: it is entirely
// observed and needs no counterfactual.
type CachingReport struct {
	ReadShareOfVolume model.Quantity `json:"read_share_of_volume"`
	WriteShareOfCost  model.Quantity `json:"write_share_of_cost"`
	TTL5m             model.Quantity `json:"cache_writes_5m"`
	TTL1h             model.Quantity `json:"cache_writes_1h"`
	TTLBucket         string         `json:"observed_ttl"`
}

// ToolSummary aggregates one tool's calls.
type ToolSummary struct {
	Name        string         `json:"name"`
	Calls       model.Quantity `json:"calls"`
	ResultBytes model.Quantity `json:"result_bytes"`
	InputBytes  model.Quantity `json:"input_bytes"`
}

// BuildProfile computes a profile from a parsed session.
func BuildProfile(s *model.Session) Profile {
	u := s.Usage()
	w := cost.Default
	if ms := s.Models(); len(ms) > 0 {
		w = cost.For(ms[0])
	}
	promptCost := w.PromptCost(u)
	outputCost := w.OutputCost(u)
	volume := float64(u.PromptTokens())

	ratio := 0.0
	if promptCost > 0 {
		ratio = volume / promptCost
	}
	readShare, writeShare := 0.0, 0.0
	if volume > 0 {
		readShare = float64(u.CacheRead) / volume
	}
	if promptCost > 0 {
		writeShare = (promptCost - float64(u.Input)*w.Input - float64(u.CacheRead)*w.CacheRead) / promptCost
	}

	retrieval := BuildRetrieval(s)

	ttl := "none observed"
	switch {
	case u.CacheCreation1h > 0 && u.CacheCreation5m > 0:
		ttl = "mixed 5m and 1h"
	case u.CacheCreation1h > 0:
		ttl = "1h"
	case u.CacheCreation5m > 0:
		ttl = "5m"
	}

	return Profile{
		SchemaVersion: SchemaVersion,
		Session:       sessionInfo(s),
		Usage: UsageReport{
			Input:           model.Obs(float64(u.Input), model.Tokens),
			CacheRead:       model.Obs(float64(u.CacheRead), model.Tokens),
			CacheCreation:   model.Obs(float64(u.CacheCreation), model.Tokens),
			Output:          model.Obs(float64(u.Output), model.Tokens),
			Thinking:        model.Obs(float64(u.Thinking), model.Tokens),
			PromptVolume:    model.Der(volume, model.Tokens),
			PromptCost:      model.Der(promptCost, model.EIT),
			OutputCost:      model.Der(outputCost, model.EIT),
			TotalCost:       model.Der(promptCost+outputCost, model.EIT),
			VolumeCostRatio: model.Der(ratio, model.Ratio),
		},
		Caching: CachingReport{
			ReadShareOfVolume: model.Der(readShare, model.Ratio),
			WriteShareOfCost:  model.Der(writeShare, model.Ratio),
			TTL5m:             model.Obs(float64(u.CacheCreation5m), model.Tokens),
			TTL1h:             model.Obs(float64(u.CacheCreation1h), model.Tokens),
			TTLBucket:         ttl,
		},
		Retrieved: retrieval.Total,
		Tools:     toolSummaries(s),
		Warnings:  s.Warnings,
		Notes: []string{
			"prompt_volume is raw tokens moved; prompt_cost is what they cost. They are different quantities and must not be added.",
			"EIT is an effective input-equivalent token: one full-price input token of the same model.",
			"Tool result bytes are observed content size, not billed tokens.",
		},
	}
}

// sessionInfo identifies what was profiled.
func sessionInfo(s *model.Session) SessionInfo {
	return SessionInfo{
		ID: s.Ref.ID, Origin: s.Ref.Origin, Current: s.Ref.Current,
		Models: s.Models(), Branch: s.Branch,
		Calls: len(s.Invocations), Prompts: s.Prompts,
		Duration: s.Duration().Round(time.Second).String(),
	}
}

func toolSummaries(s *model.Session) []ToolSummary {
	type agg struct {
		calls, result, input int
	}
	byName := map[string]*agg{}
	var order []string
	for _, tc := range s.ToolCalls {
		a, ok := byName[tc.Name]
		if !ok {
			a = &agg{}
			byName[tc.Name] = a
			order = append(order, tc.Name)
		}
		a.calls++
		a.result += tc.ResultBytes
		a.input += tc.InputBytes
	}
	// Rank by observed result bytes: the biggest contributors first.
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			if byName[order[j]].result > byName[order[i]].result {
				order[i], order[j] = order[j], order[i]
			}
		}
	}
	out := make([]ToolSummary, 0, len(order))
	for _, name := range order {
		a := byName[name]
		out = append(out, ToolSummary{
			Name:        name,
			Calls:       model.Obs(float64(a.calls), model.Calls),
			ResultBytes: model.Obs(float64(a.result), model.Bytes),
			InputBytes:  model.Obs(float64(a.input), model.Bytes),
		})
	}
	return out
}

// RenderText writes the human-facing profile.
func RenderText(w io.Writer, p Profile) error {
	b := &strings.Builder{}
	b.WriteString("TOKENAMUN\n\n")

	fmt.Fprintf(b, "Session %s", p.Session.ID)
	if p.Session.Current {
		b.WriteString("  (this session, still running)")
	}
	b.WriteString("\n")
	fmt.Fprintf(b, "  Source             %s\n", p.Session.Origin)
	fmt.Fprintf(b, "  Model              %s\n", strings.Join(p.Session.Models, ", "))
	fmt.Fprintf(b, "  API calls          %s\n", num(p.Session.Calls))
	fmt.Fprintf(b, "  User prompts       %s\n", num(p.Session.Prompts))
	fmt.Fprintf(b, "  Duration           %s\n\n", p.Session.Duration)

	b.WriteString("Observed tokens\n")
	line(b, "  Input", p.Usage.Input)
	line(b, "  Cache read", p.Usage.CacheRead)
	line(b, "  Cache creation", p.Usage.CacheCreation)
	line(b, "  Output", p.Usage.Output)
	line(b, "  of which thinking", p.Usage.Thinking)
	b.WriteString("\n")

	b.WriteString("Cost (cache-weighted)\n")
	line(b, "  Prompt volume", p.Usage.PromptVolume)
	line(b, "  Prompt cost", p.Usage.PromptCost)
	line(b, "  Output cost", p.Usage.OutputCost)
	line(b, "  Total cost", p.Usage.TotalCost)
	fmt.Fprintf(b, "%-22s %14s   [%s]\n", "  Volume overstates by",
		fmt.Sprintf("%.1fx", p.Usage.VolumeCostRatio.Value), p.Usage.VolumeCostRatio.Prov)
	b.WriteString("\n")

	b.WriteString("Caching\n")
	fmt.Fprintf(b, "%-22s %14s   [%s]\n", "  Observed TTL", p.Caching.TTLBucket, model.Observed)
	pct(b, "  Reads, % of volume", p.Caching.ReadShareOfVolume)
	pct(b, "  Writes, % of cost", p.Caching.WriteShareOfCost)
	b.WriteString("\n")

	if p.Retrieved.Items.Value > 0 {
		b.WriteString("Retrieved content (observed size of what entered context)\n")
		fmt.Fprintf(b, "%-22s %14s   [%s]\n", "  Total", bytesStr(p.Retrieved.Bytes.Value), p.Retrieved.Bytes.Prov)
		if p.Retrieved.Redundant.Value > 0 {
			fmt.Fprintf(b, "%-22s %14s   [%s]  (%.1f%% of retrieved bytes)\n", "  Retrieved again",
				bytesStr(p.Retrieved.Redundant.Value), p.Retrieved.Redundant.Prov,
				p.Retrieved.RedundantShare.Value*100)
		}
		if p.Retrieved.Withheld.Value > 0 {
			fmt.Fprintf(b, "%-22s %14s   [%s]  (kept out of context, never paid for)\n", "  Withheld by harness",
				bytesStr(p.Retrieved.Withheld.Value), p.Retrieved.Withheld.Prov)
		}
		b.WriteString("\n")
	}

	if len(p.Tools) > 0 {
		b.WriteString("Tool results by tool\n")
		for _, t := range p.Tools {
			fmt.Fprintf(b, "  %-20s %12s  %8s calls\n", trunc(t.Name, 20),
				bytesStr(t.ResultBytes.Value), num(int(t.Calls.Value)))
		}
		b.WriteString("\n")
	}

	if len(p.Warnings) > 0 {
		b.WriteString("Data notes\n")
		for _, warn := range p.Warnings {
			fmt.Fprintf(b, "  ! %s: %s\n", warn.Code, warn.Detail)
		}
		b.WriteString("\n")
	}

	_, err := io.WriteString(w, b.String())
	return err
}

// line renders a labelled quantity, refusing to print one without provenance.
func line(b *strings.Builder, label string, q model.Quantity) {
	if err := q.Validate(); err != nil {
		fmt.Fprintf(b, "  %-20s %14s\n", label, "(unlabelled)")
		return
	}
	fmt.Fprintf(b, "%-22s %14s   [%s]\n", label, num(int(q.Value)), q.Prov)
}

func pct(b *strings.Builder, label string, q model.Quantity) {
	fmt.Fprintf(b, "%-22s %13.1f%%   [%s]\n", label, q.Value*100, q.Prov)
}

func num(n int) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	digits := fmt.Sprintf("%d", n)
	var out []byte
	for i, c := range []byte(digits) {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return sign + string(out)
}

func bytesStr(v float64) string {
	switch {
	case v >= 1<<20:
		return fmt.Sprintf("%.1f MB", v/(1<<20))
	case v >= 1<<10:
		return fmt.Sprintf("%.1f KB", v/(1<<10))
	default:
		return fmt.Sprintf("%d B", int(v))
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
