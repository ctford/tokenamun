package report

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ctford/tokenamun/internal/model"
)

// Retrieval reports what content entered the context and where it came from.
type Retrieval struct {
	SchemaVersion int                  `json:"schema_version"`
	Session       SessionInfo          `json:"session"`
	Estimator     model.TokenEstimator `json:"token_estimator"`
	Total         RetrievalTotals      `json:"total"`
	ByCategory    []CategoryTotal      `json:"by_category"`
	Largest       []Item               `json:"largest"`
	Repeated      []RepeatItem         `json:"repeated_retrieval"`
	Warnings      []model.Warning      `json:"warnings,omitempty"`
	Notes         []string             `json:"notes"`
}

// RetrievalTotals summarises the session's retrieved content.
type RetrievalTotals struct {
	Items     model.Quantity `json:"items"`
	Bytes     model.Quantity `json:"bytes"`
	Tokens    model.Quantity `json:"tokens"`
	Withheld  model.Quantity `json:"withheld_bytes"`
	Redundant model.Quantity `json:"redundant_bytes"`
	// RedundantShare is the fraction of retrieved bytes that had already been
	// retrieved earlier in the session.
	RedundantShare model.Quantity `json:"redundant_share"`
}

// CategoryTotal is one row of the category breakdown.
type CategoryTotal struct {
	Category model.Category `json:"category"`
	Items    model.Quantity `json:"items"`
	Bytes    model.Quantity `json:"bytes"`
	Tokens   model.Quantity `json:"tokens"`
	Share    model.Quantity `json:"share_of_bytes"`
	// Confidence is the weakest provenance contributing to this row: a
	// category built from shell-command guesses is inferred, not derived.
	Confidence model.Provenance `json:"confidence"`
}

// Item is a single retrieval.
type Item struct {
	Tool     string         `json:"tool"`
	Path     string         `json:"path,omitempty"`
	Category model.Category `json:"category"`
	Bytes    model.Quantity `json:"bytes"`
	Tokens   model.Quantity `json:"tokens"`
	Range    string         `json:"range,omitempty"`
	Call     int            `json:"invocation_seq"`
}

// RepeatItem is content retrieved more than once.
type RepeatItem struct {
	Path      string         `json:"path,omitempty"`
	Tool      string         `json:"tool"`
	Category  model.Category `json:"category"`
	Count     model.Quantity `json:"retrievals"`
	Bytes     model.Quantity `json:"bytes_each"`
	Redundant model.Quantity `json:"redundant_bytes"`
}

// BuildRetrieval computes the retrieval report.
func BuildRetrieval(s *model.Session) Retrieval {
	r := Retrieval{
		SchemaVersion: SchemaVersion,
		Session:       sessionInfo(s),
		Estimator:     s.Estimator,
		Warnings:      s.Warnings,
		Notes: []string{
			"Bytes are the observed size of what entered the context, measured from the tool_result block the model received.",
			"Token counts are estimates unless a real tokenizer was used; check token_estimator.",
			"Retrieved-content tokens are not billed tokens. Content is priced once and carried many times; see METHODOLOGY.md.",
			"withheld_bytes is content the harness kept out of context, so it was never paid for.",
		},
	}

	var totalBytes, totalTokens, withheld, redundant int
	type agg struct {
		items  int
		bytes  int
		tokens float64
		prov   model.Provenance
	}
	byCat := map[model.Category]*agg{}
	for _, c := range s.Retrievals {
		totalBytes += c.Bytes
		totalTokens += int(c.Tokens)
		withheld += c.WithheldBytes
		a, ok := byCat[c.Category]
		if !ok {
			a = &agg{prov: c.CategoryProv}
			byCat[c.Category] = a
		}
		a.items++
		a.bytes += c.Bytes
		a.tokens += c.Tokens
		a.prov = weakest(a.prov, c.CategoryProv)
	}
	for _, rep := range s.Repeats {
		redundant += rep.WasteByte
	}

	share := 0.0
	if totalBytes > 0 {
		share = float64(redundant) / float64(totalBytes)
	}
	r.Total = RetrievalTotals{
		Items:          model.Obs(float64(len(s.Retrievals)), model.Calls),
		Bytes:          model.Obs(float64(totalBytes), model.Bytes),
		Tokens:         model.Quantity{Value: float64(totalTokens), Unit: model.Tokens, Prov: estimatorProv(s)},
		Withheld:       model.Obs(float64(withheld), model.Bytes),
		Redundant:      model.Der(float64(redundant), model.Bytes),
		RedundantShare: model.Der(share, model.Ratio),
	}

	for _, cat := range model.Categories() {
		a, ok := byCat[cat]
		if !ok {
			continue
		}
		catShare := 0.0
		if totalBytes > 0 {
			catShare = float64(a.bytes) / float64(totalBytes)
		}
		r.ByCategory = append(r.ByCategory, CategoryTotal{
			Category:   cat,
			Items:      model.Obs(float64(a.items), model.Calls),
			Bytes:      model.Obs(float64(a.bytes), model.Bytes),
			Tokens:     model.Quantity{Value: a.tokens, Unit: model.Tokens, Prov: estimatorProv(s)},
			Share:      model.Der(catShare, model.Ratio),
			Confidence: a.prov,
		})
	}
	sort.SliceStable(r.ByCategory, func(i, j int) bool {
		return r.ByCategory[i].Bytes.Value > r.ByCategory[j].Bytes.Value
	})

	ranked := make([]model.RetrievedContent, len(s.Retrievals))
	copy(ranked, s.Retrievals)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Bytes > ranked[j].Bytes })
	for i, c := range ranked {
		if i >= 10 {
			break
		}
		r.Largest = append(r.Largest, Item{
			Tool: c.Tool, Path: c.Path, Category: c.Category,
			Bytes:  model.Obs(float64(c.Bytes), model.Bytes),
			Tokens: model.Quantity{Value: c.Tokens, Unit: model.Tokens, Prov: c.TokensProv},
			Range:  rangeOf(c),
			Call:   c.InvocationSeq,
		})
	}

	for i, rep := range s.Repeats {
		if i >= 10 {
			break
		}
		r.Repeated = append(r.Repeated, RepeatItem{
			Path: rep.Path, Tool: rep.Tool, Category: rep.Category,
			Count:     model.Obs(float64(rep.Count), model.Calls),
			Bytes:     model.Obs(float64(rep.Bytes), model.Bytes),
			Redundant: model.Der(float64(rep.WasteByte), model.Bytes),
		})
	}
	return r
}

// weakest returns the less confident of two provenances, so a category built
// partly from guesses is not presented as if it were observed.
func weakest(a, b model.Provenance) model.Provenance {
	rank := map[model.Provenance]int{
		model.Observed: 0, model.Derived: 1, model.DerivedApprox: 2,
		model.Inferred: 3, model.Counterfactual: 4,
	}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

func estimatorProv(s *model.Session) model.Provenance {
	if s.Estimator.Calibrated {
		return model.DerivedApprox
	}
	return model.DerivedApprox
}

func rangeOf(c model.RetrievedContent) string {
	if c.Lines == 0 || c.TotalLines == 0 {
		return ""
	}
	end := c.StartLine + c.Lines - 1
	if c.Partial {
		return fmt.Sprintf("lines %d-%d of %d", c.StartLine, end, c.TotalLines)
	}
	return fmt.Sprintf("all %d lines", c.TotalLines)
}

// RenderRetrieval writes the human-facing retrieval report.
func RenderRetrieval(w io.Writer, r Retrieval) error {
	b := &strings.Builder{}
	b.WriteString("TOKENAMUN  retrieved content\n\n")

	fmt.Fprintf(b, "Session %s\n", r.Session.ID)
	fmt.Fprintf(b, "  API calls          %s\n\n", num(r.Session.Calls))

	b.WriteString("Retrieved content (what entered the context)\n")
	line(b, "  Items", r.Total.Items)
	fmt.Fprintf(b, "%-22s %14s   [%s]\n", "  Bytes", bytesStr(r.Total.Bytes.Value), r.Total.Bytes.Prov)
	line(b, "  Estimated tokens", r.Total.Tokens)
	if r.Total.Withheld.Value > 0 {
		fmt.Fprintf(b, "%-22s %14s   [%s]  (kept out of context by the harness)\n",
			"  Withheld", bytesStr(r.Total.Withheld.Value), r.Total.Withheld.Prov)
	}
	b.WriteString("\n")

	if len(r.ByCategory) > 0 {
		b.WriteString("By category\n")
		for _, c := range r.ByCategory {
			fmt.Fprintf(b, "  %-18s %12s %6.1f%%  %4s items   [%s]\n",
				trunc(string(c.Category), 18), bytesStr(c.Bytes.Value),
				c.Share.Value*100, num(int(c.Items.Value)), c.Confidence)
		}
		b.WriteString("\n")
	}

	if len(r.Largest) > 0 {
		b.WriteString("Largest retrievals\n")
		for _, it := range r.Largest {
			label := it.Path
			if label == "" {
				label = "(" + it.Tool + " output)"
			}
			extra := it.Range
			if extra != "" {
				extra = "  " + extra
			}
			fmt.Fprintf(b, "  %-40s %10s  call %d%s\n",
				trunc(label, 40), bytesStr(it.Bytes.Value), it.Call, extra)
		}
		b.WriteString("\n")
	}

	b.WriteString("Repeated retrieval\n")
	if len(r.Repeated) == 0 {
		b.WriteString("  none: no content was retrieved twice byte-for-byte\n\n")
	} else {
		fmt.Fprintf(b, "%-22s %13.1f%%   [%s]\n", "  Redundant share",
			r.Total.RedundantShare.Value*100, r.Total.RedundantShare.Prov)
		for _, rep := range r.Repeated {
			label := rep.Path
			if label == "" {
				label = "(" + rep.Tool + " output)"
			}
			fmt.Fprintf(b, "  %-40s %4dx %10s redundant\n",
				trunc(label, 40), int(rep.Count.Value), bytesStr(rep.Redundant.Value))
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(b, "Token estimate: %s\n", r.Estimator.Method)
	if r.Estimator.Calibrated {
		fmt.Fprintf(b, "  %.2f bytes/token, %.0f tokens of fixed overhead per call, "+
			"%.0f%% of observed growth unattributed, %d samples\n",
			r.Estimator.BytesPerToken, r.Estimator.PerCallOverhead,
			r.Estimator.Residual*100, r.Estimator.Samples)
	}
	b.WriteString("\n")

	_, err := io.WriteString(w, b.String())
	return err
}
