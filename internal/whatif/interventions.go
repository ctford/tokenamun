package whatif

import (
	"fmt"
	"strings"

	"github.com/ctford/tokenamun/internal/analysis"
	"github.com/ctford/tokenamun/internal/model"
)

// CacheTTL estimates moving from the 5-minute prompt cache to the 1-hour one.
//
// The best-evidenced intervention available, because the expensive part is
// observed: which TTL each call used, and which misses a longer lifetime would
// have covered. It is also a real setting rather than a thought experiment --
// promptCacheTtl / CLAUDE_CODE_PROMPT_CACHE_TTL, Claude Code v2.1.242+.
type CacheTTL struct{}

func (CacheTTL) Name() string { return "cache-ttl" }

func (CacheTTL) Describe() string {
	return "switch the prompt cache from the 5-minute TTL to the 1-hour TTL"
}

func (CacheTTL) Estimate(c Context) Result {
	r := Result{
		Intervention: "cache-ttl",
		Description:  CacheTTL{}.Describe(),
		Unknown: []string{
			behaviourUnknown,
			"idle_gap_distribution: future sessions may pause differently.",
			"other_bucket: subagent, workflow and compaction requests need subagentPromptCacheTtl set separately.",
			"provider_support: 1-hour availability varies on Bedrock and through gateways.",
			outcomeUnknown,
		},
	}

	var avoidable, remaining int64
	var avoidableCalls, remainingCalls int
	for _, m := range c.Cache.Misses {
		if m.Cause != analysis.CauseTTLExpiry {
			continue
		}
		if m.AvoidableByTTL {
			avoidable += m.Rebuilt
			avoidableCalls++
		} else {
			remaining += m.Rebuilt
			remainingCalls++
		}
	}

	var writes5m int64
	for _, inv := range c.Session.Invocations {
		writes5m += inv.Usage.CacheCreation5m
	}

	r.Observed = []Finding{
		fact("observed TTL", c.Cache.ObservedTTL),
		obs("cache writes at 5m", float64(writes5m), model.Tokens),
		obs("cache writes at 1h", float64(c.Cache.Writes1h), model.Tokens),
		obs("prompt cost", c.Cache.TotalCostEIT, model.EIT),
		obs("expiry calls, gap under an hour", float64(avoidableCalls), model.Calls),
		obs("expiry calls, gap over an hour", float64(remainingCalls), model.Calls),
	}

	if c.Cache.Writes1h > 0 && writes5m == 0 {
		r.Applicable = false
		r.NotMeasurable = "this session already used the 1-hour TTL"
		r.Derived = []Finding{der("nothing to change", 0, model.EIT)}
		return r
	}
	r.Applicable = true

	r.Derived = []Finding{
		der("re-creation avoidable under a 1h TTL", float64(avoidable), model.Tokens),
		der("re-creation still required", float64(remaining), model.Tokens,
			"gaps longer than an hour expire under either TTL"),
	}

	// The doubled write price is charged honestly against the saving: every
	// write that did happen would have cost 2x instead of 1.25x.
	saved := float64(avoidable) * (c.Weights.CacheWrite5m - c.Weights.CacheRead)
	extraOnRemaining := float64(remaining) * (c.Weights.CacheWrite1h - c.Weights.CacheWrite5m)
	ordinaryWrites := float64(writes5m - avoidable - remaining)
	extraOnOrdinary := ordinaryWrites * (c.Weights.CacheWrite1h - c.Weights.CacheWrite5m)
	net := extraOnRemaining + extraOnOrdinary - saved

	pct := 0.0
	if c.Cache.TotalCostEIT > 0 {
		pct = net / c.Cache.TotalCostEIT
	}

	r.Counterfact = []Finding{
		cf("saved by avoided re-creation", -saved, model.EIT),
		cf("cost of remaining re-creation at 2.0x", extraOnRemaining, model.EIT),
		cf("ordinary writes repriced 1.25x to 2.0x", extraOnOrdinary, model.EIT),
		cf("net change", net, model.EIT,
			"negative is a saving; a session of short bursts that never idles past "+
				"five minutes comes out positive, which is the point of computing it"),
		cf("net change, share of prompt cost", pct, model.Ratio),
	}
	r.Headline = &r.Counterfact[3]
	r.Caveat = "A real setting: promptCacheTtl, Claude Code v2.1.242+. Comes out positive " +
		"on sessions of short bursts, where the doubled write price buys a lifetime you " +
		"never use."
	return r
}

// RepeatedRetrieval estimates fetching byte-identical content once.
//
// The strongest counterfactual available, because fetching something once
// instead of twice is arithmetic rather than modelling. The behavioural
// unknown is still real: the agent re-read it for a reason, even if the reason
// was forgetting.
type RepeatedRetrieval struct{}

func (RepeatedRetrieval) Name() string { return "repeated-retrieval" }

func (RepeatedRetrieval) Describe() string {
	return "retrieve byte-identical content once instead of repeatedly"
}

func (RepeatedRetrieval) Estimate(c Context) Result {
	r := Result{
		Intervention: "repeated-retrieval",
		Description:  RepeatedRetrieval{}.Describe(),
		Unknown: []string{
			"recovery_requests: the agent re-read this content for a reason. Removing " +
				"the second copy assumes it still had the first in mind.",
			behaviourUnknown,
			outcomeUnknown,
		},
	}

	var redundantBytes int
	var items int
	for _, rep := range c.Session.Repeats {
		redundantBytes += rep.WasteByte
		items++
	}
	// Everything observed, including image payload, or the share would be
	// inflated by excluding images from the denominator only.
	var totalBytes int
	for _, item := range c.Session.Retrievals {
		totalBytes += item.ObservedBytes()
	}

	share := 0.0
	if totalBytes > 0 {
		share = float64(redundantBytes) / float64(totalBytes)
	}

	r.Observed = []Finding{
		obs("retrieved content", float64(totalBytes), model.Bytes),
		obs("repeated payloads", float64(items), model.Calls),
		obs("redundant bytes", float64(redundantBytes), model.Bytes),
		obs("redundant share of retrieved bytes", share, model.Ratio),
	}
	r.Applicable = redundantBytes > 0
	if !r.Applicable {
		r.NotMeasurable = "no content was retrieved twice byte-for-byte in this session"
		return r
	}

	// The saving is the carry of the second and later copies: the first fetch
	// would still have happened. Joined on retrieval identity, since most
	// retrieved content is shell output with no path to match on.
	redundant := map[int]bool{}
	for _, rep := range c.Session.Repeats {
		// A repeat always has at least two retrievals in practice, but a
		// hand-built one may not, and slicing past the end would panic.
		if len(rep.RetrievalSeqs) < 2 {
			continue
		}
		for _, seq := range rep.RetrievalSeqs[1:] {
			redundant[seq] = true
		}
	}
	var carrySaved float64
	for _, item := range c.Carry.Items {
		if redundant[item.RetrievalSeq] {
			carrySaved += item.CarryEIT
		}
	}

	tokens := estimateTokens(c.Session, redundantBytes)
	r.Derived = []Finding{
		der("redundant content, estimated tokens", tokens, model.Tokens),
		der("carry cost of the redundant copies", carrySaved, model.EIT),
	}
	r.Counterfact = []Finding{
		cf("avoidable carry cost", -carrySaved, model.EIT,
			"the saving is the carry of the later copies; the first fetch still happens"),
	}
	r.Headline = &r.Counterfact[0]
	r.Caveat = "The agent re-read this content for a reason, even if the reason was " +
		"forgetting it had it."
	return r
}

// OutputCompression estimates shrinking tool output before it enters context.
type OutputCompression struct{}

func (OutputCompression) Name() string { return "output-compression" }

func (OutputCompression) Describe() string {
	return "compress tool output before it enters the context"
}

func (OutputCompression) Estimate(c Context) Result {
	return compressionEstimate(c, "output-compression", OutputCompression{}.Describe(), nil)
}

// Caveman estimates the published Caveman compression on this session.
type Caveman struct{}

func (Caveman) Name() string { return "caveman" }

func (Caveman) Describe() string {
	return "compress agent-facing content the way Caveman claims to"
}

func (Caveman) Estimate(c Context) Result {
	return compressionEstimate(c, "caveman", Caveman{}.Describe(), []string{
		"published_ratio: Caveman's own figure is a 65% output reduction, while an " +
			"independent test measured 8.5% on real agentic tasks. Neither was measured " +
			"here. Use --replay-with to pipe this session's own content through the real " +
			"compressor and settle it for this repository.",
	})
}

// compressionEstimate is shared by every compression-shaped intervention: they
// differ in their assumed ratio and their caveats, not in their arithmetic.
func compressionEstimate(c Context, name, desc string, extraUnknown []string) Result {
	r := Result{
		Intervention: name,
		Description:  desc,
		Unknown: append([]string{
			"additional_tool_calls: content the agent could no longer read may have " +
				"been fetched again.",
			"cache_invalidation_point: rewriting context breaks the cached prefix from " +
				"that point, converting cheap reads into full-price writes. Where the " +
				"change point cannot be determined this is not netted out.",
			behaviourUnknown,
			outcomeUnknown,
		}, extraUnknown...),
	}

	// Eligibility is by delivery channel, not by content category. A proxy
	// sits in front of tool output and compresses whatever comes back, so
	// a decision record delivered by `cat` is eligible even though it
	// classifies as an ADR. Direct file reads and patches are excluded: those
	// arrive through a different path and compressing source the agent is
	// about to edit is a different, riskier intervention.
	//
	// The split matters for judging the risk rather than the size: opaque
	// output is build logs and status noise, where lossy compression is
	// cheap; identifiable file content is something the agent went looking
	// for, where losing detail is how a compression saving turns into extra
	// tool calls.
	var eligibleBytes, totalBytes, opaqueBytes, contentBytes int
	var eligibleItems int
	for _, item := range c.Session.Retrievals {
		totalBytes += item.ObservedBytes()
		if !viaTool(item.Tool) {
			continue
		}
		eligibleBytes += item.Bytes
		eligibleItems++
		// Opaque output is anything with no file behind it: build logs,
		// status, search results. Content is a file the agent went looking
		// for. Whether a path could be attributed is the honest test, and it
		// replaces a semantic category that was itself a guess.
		if item.Path == "" {
			opaqueBytes += item.Bytes
		} else {
			contentBytes += item.Bytes
		}
	}

	r.Observed = []Finding{
		obs("retrieved content", float64(totalBytes), model.Bytes),
		obs("eligible, delivered by a tool", float64(eligibleBytes), model.Bytes),
		obs("  of which opaque output", float64(opaqueBytes), model.Bytes,
			"build logs and status noise: the cheap part to compress"),
		obs("  of which identifiable content", float64(contentBytes), model.Bytes,
			"files the agent went looking for: compressing these is where a saving turns into extra tool calls"),
		obs("eligible items", float64(eligibleItems), model.Calls),
	}
	r.Applicable = eligibleBytes > 0
	if !r.Applicable {
		r.NotMeasurable = "no eligible tool output in this session"
		return r
	}

	ratio := c.CompressionRatio
	source := fmt.Sprintf("assumed surviving fraction %.2f, stated rather than measured", ratio)
	if c.Replay != nil && c.Replay.InputBytes > 0 {
		ratio = float64(c.Replay.OutputBytes) / float64(c.Replay.InputBytes)
		source = fmt.Sprintf("measured by replaying %s through %q",
			byteSize(c.Replay.InputBytes), c.Replay.Command)
		r.Observed = append(r.Observed,
			obs("replayed through a real compressor", float64(c.Replay.InputBytes), model.Bytes, source))
	}

	compressed := float64(eligibleBytes) * ratio
	removed := float64(eligibleBytes) - compressed

	// Eligible carry, so the saving is expressed in what it actually costs.
	var eligibleCarry float64
	for _, item := range c.Carry.Items {
		if viaTool(item.Tool) {
			eligibleCarry += item.CarryEIT
		}
	}

	r.Derived = []Finding{
		der("compression ratio applied", ratio, model.Ratio, source),
		der("eligible content, estimated tokens", estimateTokens(c.Session, eligibleBytes), model.Tokens),
		der("carry cost of eligible content", eligibleCarry, model.EIT),
	}
	r.Counterfact = []Finding{
		cf("compressed size", compressed, model.Bytes),
		cf("bytes removed", removed, model.Bytes),
		cf("carry cost avoided, before invalidation", -eligibleCarry*(1-ratio), model.EIT,
			"proportional to the content removed; the cache-invalidation cost of "+
				"rewriting context is in unknown, not netted out here"),
	}
	if c.Replay == nil {
		r.Counterfact = append(r.Counterfact, cf("local reduction of eligible content",
			1-ratio, model.Ratio,
			"a local reduction is not a session saving and is not presented as one"))
	}
	r.Headline = &r.Counterfact[2]
	if c.Replay != nil {
		r.Caveat = "Ratio measured by replaying this session's own content through " +
			c.Replay.Command + "."
	} else {
		r.Caveat = fmt.Sprintf("Assumes %.0f%% of eligible content survives; that ratio is "+
			"stated, not measured here. Use --replay-with to measure it.", ratio*100)
	}
	return r
}

// MCPToCLI reports that the evidence for this one is not in a transcript.
type MCPToCLI struct{}

func (MCPToCLI) Name() string { return "mcp-to-cli" }

func (MCPToCLI) Describe() string {
	return "put an MCP server behind a CLI instead of exposing its tool schemas"
}

func (MCPToCLI) Estimate(c Context) Result {
	var mcpCalls int
	var mcpBytes int
	for _, item := range c.Session.Retrievals {
		if item.Channel == model.ChanMCP {
			mcpCalls++
			mcpBytes += item.Bytes
		}
	}

	// Zero MCP calls is not zero MCP cost, and reporting 0% here would be the
	// wrong answer in the expensive direction. A connected server puts its
	// tool schemas in the preamble whether or not anything calls it, so the
	// session that never touches MCP and the session with no server attached
	// look identical from a transcript -- and the first of those is the case
	// the intervention exists for.
	whyNot := "The saving lives in tool-schema size, and tool schemas are not in the " +
		"transcript. The observed session preamble is a ceiling on the whole category, " +
		"not a measurement of the schemas inside it."
	if mcpCalls == 0 {
		whyNot += " This session made no MCP calls, which is not the same as having no " +
			"MCP cost: a connected server's schemas sit in the preamble on every call " +
			"whether anything calls it or not, and a transcript cannot tell an unused " +
			"server from an absent one. Reporting 0% here would be a claim, and it " +
			"would be wrong in the expensive direction."
	}
	whyNot += " To measure it, run the same opening prompt with the server connected " +
		"and disconnected and compare the first call's prompt size: that difference is " +
		"observed. `tokenamun compare` is the command for it."

	r := Result{
		Intervention:  "mcp-to-cli",
		Description:   MCPToCLI{}.Describe(),
		Applicable:    false,
		NotMeasurable: whyNot,
		Observed: []Finding{
			obs("mcp tool results", float64(mcpCalls), model.Calls),
			obs("mcp result content", float64(mcpBytes), model.Bytes),
			obs("session preamble, a ceiling on the category", float64(c.Carry.Preamble), model.Tokens),
			obs("cost of carrying the preamble", c.Carry.PreambleCarryEIT, model.EIT),
		},
		Caveat: "Measure it with an A/B instead: same opening prompt, server connected and " +
			"disconnected, and compare the first call's prompt size.",
		Unknown: []string{
			"schema_tokens: not observable from a transcript at all.",
			"tools_available: only tools that were used appear; the ones that merely " +
				"occupied context do not.",
			outcomeUnknown,
		},
	}
	return r
}

// viaTool reports whether content arrived through a channel a compression
// proxy sits in front of. Kept in step with the replay eligibility in
// replay.go: a ratio measured over a different set than the saving is applied
// to is quietly wrong.
func viaTool(tool string) bool {
	if strings.HasPrefix(tool, "mcp__") {
		return true
	}
	switch tool {
	case "Bash", "BashOutput", "WebFetch", "WebSearch":
		return true
	default:
		return false
	}
}

// estimateTokens applies the session's own calibrated estimator.
func estimateTokens(s *model.Session, byteCount int) float64 {
	ratio := s.Estimator.BytesPerToken
	if ratio <= 0 {
		return 0
	}
	return float64(byteCount) / ratio
}

func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
