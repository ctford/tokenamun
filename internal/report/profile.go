// Package report renders analyses for humans and for agents. Every rendered
// quantity carries its provenance; see docs/METHODOLOGY.md section 1.
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
//
// 2: cache's per-cause `avoidable_by_longer_ttl` bool became
// `avoidable_by_longer_ttl_calls` and `_tokens`. The bool was ORed over the
// group, so a consumer reading it as "this row is avoidable" was reading an
// overclaim.
//
// 3: profile grew a `subagents` block, and `subagent_usage_missing` stopped
// firing when the subagent transcripts are on disk. A consumer that read
// that warning as "this session fanned out and the spend is unknowable" was
// reading something the data did not say.
//
// 4: `session` grew an optional `prices` block, in a new `usd` unit, when
// --prices is given. A consumer reading the old output had `mixed_pricing`
// and nothing else, and the only thing it could conclude was that the
// cost-weighted total could not be corrected. It can: the dollar figures are
// priced per call at each call's own model, so they add across models where
// EIT does not.
//
// 5: carry answers `all`. Over a set its items carry a `session`, and
// `final_prompt_tokens` and `reset_calls` are absent rather than zero. A
// consumer that read a missing final prompt as a zero one would be reading a
// week of work as a context that ended empty.
//
// 6: every tree node reports what one retrieval under it cost to carry, as
// p50/p95/max rather than only as a total and a count. A consumer dividing
// the two was reading a mean, and a mean reads the same whether a command is
// expensive on every run or ran once and dumped 200K tokens.
//
// 7: three additions.
//
// `sessions` grew `calls` and `cost_eit` per session, and top-level
// `sorted_by`, `unreadable` and `notes`. A consumer reading the old output
// had no cost in it at all and had to derive one from the transcripts, which
// is the deduplication rule in METHODOLOGY section 2 waiting to be got wrong:
// summing assistant entries instead of requests overstates by 66-97% on the
// fixtures here.
//
// Every share in `cache` gained a `_of_session_cost` twin, and `optimise`
// gained `addressable_share_of_prompt_cost`. A consumer reading one share
// from each command was reading two different denominators -- prompt cost
// against prompt-and-output -- with nothing in either payload saying so, and
// putting them in one column compared quantities that are not comparable.
//
// `optimise` grew `parts`, and its `at`/`optimise`/`why` may now repeat. A
// consumer that composed several single-node runs by hand was adding impacts,
// which do not add, or adding savings over nodes that may contain one another.
const SchemaVersion = 7

// Profile is a session overview.
type Profile struct {
	SchemaVersion int             `json:"schema_version"`
	Session       SessionInfo     `json:"session"`
	Usage         UsageReport     `json:"usage"`
	Caching       CachingReport   `json:"caching"`
	Retrieved     RetrievalTotals `json:"retrieved_content"`
	// Subagents is absent for the great majority of sessions, which never
	// call Agent at all.
	Subagents *SubagentReport `json:"subagents,omitempty"`
	Tools     []ToolSummary   `json:"tools"`
	Warnings  []model.Warning `json:"warnings,omitempty"`
	Notes     []string        `json:"notes"`
}

// SessionInfo identifies what was profiled.
type SessionInfo struct {
	ID string `json:"id"`
	// Selector is what to pass back to the tool to get this same scope
	// again. It is not always the ID: a merged set of sessions is labelled
	// "129 sessions, 2026-08-24 to 2026-09-01", which reads well and is not
	// a thing any command accepts, so every command printed with that in it
	// was a command that could not be run.
	Selector string       `json:"selector,omitempty"`
	Origin   model.Origin `json:"origin"`
	Current  bool         `json:"current"`
	Models   []string     `json:"models"`
	Branch   string       `json:"branch,omitempty"`
	Calls    int          `json:"api_calls"`
	// MixedPricing is true when more than one pricing applies across the
	// calls. A cost-weighted token is relative to a model's own input price,
	// so a total that spans two of them adds quantities of different sizes.
	// Reported rather than corrected: correcting it needs a price list, which
	// is configuration this tool does not have.
	MixedPricing bool `json:"mixed_pricing,omitempty"`
	// Prices is what this cost in money. Absent unless --prices asked, and
	// then it is the answer MixedPricing describes the lack of.
	Prices   *Prices `json:"prices,omitempty"`
	Prompts  int     `json:"user_prompts"`
	Duration string  `json:"duration"`
}

// Prices is a total in money, converted from observed tokens with a published
// rate per model.
//
// Opt-in, and deliberately so. EIT stays the default unit permanently: it
// needs no price list and is exact within a model. Dollars exist for the one
// case EIT cannot answer, which is the case MixedPricing names -- a total
// spanning two models adds quantities of different sizes, and dollars are the
// same size everywhere.
//
// Labelled derived rather than derived-approx. Nothing here is estimated: the
// tokens are observed, the arithmetic is exact, and each call is converted at
// its own model's rate. What can be wrong is the rate itself, which is a
// staleness rather than an approximation, and derived-approx promises the
// wrong caveat -- an estimator whose error you can reason about. The honest
// mitigation is the pin, so Catalog is not optional and no renderer prints
// these figures without it.
type Prices struct {
	Prompt model.Quantity `json:"prompt_cost"`
	Output model.Quantity `json:"output_cost"`
	Total  model.Quantity `json:"total_cost"`
	// Catalog names the published catalog and the commit it was read at.
	// "From LiteLLM" is not a source; published rates move.
	Catalog string `json:"catalog"`
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

// SubagentReport is what the subagents a session launched cost.
//
// Reported beside the session's totals rather than folded into them. A
// subagent runs in its own context, so adding its cache reads to the
// parent's would describe a prompt that never existed -- and the retrieval
// tree, the carry figures and the estimator are all about this context. What
// a reader wants is both numbers and the sum, which is what this gives.
type SubagentReport struct {
	Runs      model.Quantity `json:"runs"`
	Calls     model.Quantity `json:"api_calls"`
	ToolCalls model.Quantity `json:"tool_calls"`
	CacheRead model.Quantity `json:"cache_read"`
	Output    model.Quantity `json:"output"`
	TotalCost model.Quantity `json:"total_cost"`
	// CombinedCost is the session and its subagents together: what the work
	// actually cost, as against what the session's own context cost.
	CombinedCost model.Quantity `json:"combined_cost"`
	// ShareOfCombined is how much of that total happened out of sight of
	// every other figure in this report.
	ShareOfCombined model.Quantity `json:"share_of_combined"`
}

// buildSubagents summarises subagent spend, or returns nil when there was
// none. Priced per call with cost.SessionCost, because a subagent can run on
// a different model from its parent.
func buildSubagents(s *model.Session, sessionCost float64) *SubagentReport {
	if len(s.Subagents) == 0 {
		return nil
	}
	invs := s.SubagentInvocations()
	prompt, output := cost.SessionCost(invs)
	total := prompt + output
	combined := sessionCost + total
	share := 0.0
	if combined > 0 {
		share = total / combined
	}
	u := s.SubagentUsage()
	var toolCalls int
	for _, r := range s.Subagents {
		toolCalls += r.ToolCalls
	}
	return &SubagentReport{
		Runs:            model.Obs(float64(len(s.Subagents)), model.Calls),
		Calls:           model.Obs(float64(len(invs)), model.Calls),
		ToolCalls:       model.Obs(float64(toolCalls), model.Calls),
		CacheRead:       model.Obs(float64(u.CacheRead), model.Tokens),
		Output:          model.Obs(float64(u.Output), model.Tokens),
		TotalCost:       model.Der(total, model.EIT),
		CombinedCost:    model.Der(combined, model.EIT),
		ShareOfCombined: model.Der(share, model.Ratio),
	}
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
	// Each call at its own model's weights. This is total_cost, the headline
	// figure, and it used to be the session's summed usage priced at the
	// first model seen -- which on a session mixing the 5.1 generation with
	// anything else misprices cache reads fourfold, on the class that is
	// most of the bill.
	promptCost, outputCost := cost.SessionCost(s.Invocations)
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
		// The same treatment: the input and cache-read terms are accumulated
		// per call, not taken off the total at one model's rates.
		writeShare = cost.WriteCost(s.Invocations) / promptCost
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
		Subagents: buildSubagents(s, promptCost+outputCost),
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
		// For one session the id is a valid selector, so they agree here.
		ID: s.Ref.ID, Selector: s.Ref.ID,
		Origin: s.Ref.Origin, Current: s.Ref.Current,
		Models: s.Models(), Branch: s.Branch,
		Calls: s.RealCalls(), Prompts: s.Prompts,
		MixedPricing: cost.Mixed(s.Invocations),
		Duration:     s.Duration().Round(time.Second).String(),
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
	// Omitted rather than printed blank. A merged set spanning both sources
	// has no single origin, and a label with nothing after it reads as a
	// missing value rather than as an inapplicable one.
	if p.Session.Origin != "" {
		fmt.Fprintf(b, "  Source             %s\n", p.Session.Origin)
	}
	fmt.Fprintf(b, "  Model              %s\n", strings.Join(p.Session.Models, ", "))
	if p.Session.MixedPricing {
		b.WriteString("  ! these models are priced differently, so the cost-weighted\n" +
			"    total adds quantities of different sizes\n")
		if p.Session.Prices != nil {
			b.WriteString("    the money total below does not\n")
		}
	}
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

	prices(b, p.Session.Prices)

	b.WriteString("Caching\n")
	fmt.Fprintf(b, "%-22s %14s   [%s]\n", "  Observed TTL", p.Caching.TTLBucket, model.Observed)
	pct(b, "  Reads, % of volume", p.Caching.ReadShareOfVolume)
	pct(b, "  Writes, % of cost", p.Caching.WriteShareOfCost)
	b.WriteString("\n")

	if sa := p.Subagents; sa != nil {
		b.WriteString("Subagents (their own contexts, from their own transcripts)\n")
		line(b, "  Subagent runs", sa.Runs)
		line(b, "  Their API calls", sa.Calls)
		line(b, "  Their cache reads", sa.CacheRead)
		line(b, "  Their output", sa.Output)
		line(b, "  Their cost", sa.TotalCost)
		line(b, "  Session + subagents", sa.CombinedCost)
		pct(b, "  Share out of sight", sa.ShareOfCombined)
		b.WriteString("  Every other figure here is this session's own context.\n")
		b.WriteString("\n")
	}

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

// pct is line for a share, and refuses an unlabelled one for the same reason:
// an empty bracket beside a percentage reads as a rendering glitch rather
// than as a number nobody can vouch for.
func pct(b *strings.Builder, label string, q model.Quantity) {
	if err := q.Validate(); err != nil {
		fmt.Fprintf(b, "  %-20s %14s\n", label, "(unlabelled)")
		return
	}
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

// SessionOf is the session header a report prints, for callers outside this
// package that need it beside a tree.
func SessionOf(s *model.Session) SessionInfo { return sessionInfo(s) }

// WithPrices attaches dollar totals to a session header, priced per call at
// each call's own model.
//
// Sessions rather than a total, because the conversion is per call: a session
// that switches model has calls at two prices, and multiplying its EIT total
// by one of them would bill the whole session at whichever model went first.
//
// An error rather than a figure when the catalog does not know a model. The
// alternative is a total that quietly drops the calls it could not price,
// which is a bill missing a model and looks exactly like a bill.
func WithPrices(info SessionInfo, sessions ...*model.Session) (SessionInfo, error) {
	var prompt, output float64
	var unpriced []string
	seen := map[string]bool{}
	for _, s := range sessions {
		p, o, missing := cost.USD(s.Invocations)
		prompt += p
		output += o
		for _, m := range missing {
			if !seen[m] {
				seen[m] = true
				unpriced = append(unpriced, m)
			}
		}
	}
	if len(unpriced) > 0 {
		return info, fmt.Errorf(
			"no published price for %s; the catalog is pinned at %s, and scripts/refresh-prices.sh is how it moves",
			strings.Join(unpriced, ", "), cost.CatalogPin())
	}
	info.Prices = &Prices{
		Prompt:  model.Der(prompt, model.USD),
		Output:  model.Der(output, model.USD),
		Total:   model.Der(prompt+output, model.USD),
		Catalog: cost.CatalogPin(),
	}
	return info, nil
}

// prices renders the money block, pin included.
//
// One function for every surface that can print dollars, so that the pin
// cannot be forgotten on one of them. A dollar figure whose source is not
// stated is the kind of number this tool exists not to produce.
func prices(b *strings.Builder, p *Prices) {
	if p == nil {
		return
	}
	b.WriteString("Money (published rates, not a bill)\n")
	usd(b, "  Prompt", p.Prompt)
	usd(b, "  Output", p.Output)
	usd(b, "  Total", p.Total)
	fmt.Fprintf(b, "  Catalog            %s\n", p.Catalog)
	b.WriteString("  Priced per call, each at its own model's published input rate.\n\n")
}

// usd is line for money. Two decimal places rather than thousands separators:
// the figures are dollars, and rounding a session to the nearest dollar loses
// most of them.
func usd(b *strings.Builder, label string, q model.Quantity) {
	if err := q.Validate(); err != nil {
		fmt.Fprintf(b, "  %-20s %14s\n", label, "(unlabelled)")
		return
	}
	fmt.Fprintf(b, "%-22s %14s   [%s]\n", label, fmt.Sprintf("$%.2f", q.Value), q.Prov)
}
