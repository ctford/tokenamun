# What people claim about token optimisation, and how good the evidence is

Survey of the techniques and tools currently claiming to cut coding-agent token
usage, taken from the September 2026 Tech Radar blip candidates and followed out
to primary sources. The point is not to pick winners. It is to work out which
claims Tokenamun would need to adjudicate, and what it has to measure to do it.

Read with [`COMMON-INTERVENTIONS.md`](COMMON-INTERVENTIONS.md), which says which of these this
data can actually settle.

## The one thing everybody agrees on

Input tokens dominate agentic coding spend — commonly cited at 93–99% of
trajectory volume, with one analysis attributing **62% of the bill to re-sent
context** alone. The mechanism is not disputed: the model has no memory between
turns, so every turn re-sends the accumulated conversation, and cost grows with
session length regardless of how much new work is happening. An arXiv study of
agent spending finds input dominance holds *even with prompt caching in use*.

Our own measurement agrees emphatically. Across 8 sessions: ~711K tokens of
unique tool-result content, ~856M tokens of billed input. The content is not the
cost. **Carrying** the content is the cost.

That is the good news for a profiler: the dominant term is a function of when
content enters and how long it stays, both of which are observable. It also
means every claim below should be read as a claim about *residency*, not volume,
and most of them are not stated that way.

## The claims

Evidence grades: **vendor** (self-reported, own benchmark), **independent**
(third party reproduced or measured), **contested** (independent result
materially disagrees), **structural** (follows from how the API works, hard to
dispute).

### Loading less up front

| Technique | Claim | Grade | What's actually being measured |
| --- | --- | --- | --- |
| Code execution with MCP (Anthropic) | 150,000 → 2,000 tokens, **98.7%** | vendor, with independent support | The 98.7% is one illustrative workflow. Independent reproductions land lower and scale with tool count: 58% at 96 tools, 84.5% at 251, 92.8% at 508; one measured 78.5% input-token reduction. A GitHub-tools implementation held ~98% at 112 tools. The saving is real but it is a **function of how many tools you had loaded**, which makes the headline number a property of the baseline, not the technique. |
| Tool search / deferred loading (Anthropic) | **85%** fewer tool-definition tokens | vendor | Tool schemas cost 100–400 tokens each, so the saving is arithmetic on a number you can count. More interesting is the reported *accuracy* gain — Opus 4.5 tool selection 79.5% → 88.1% — which suggests the win isn't only cost. Claude Code already applies this automatically when deferrable definitions exceed 10% of the context window, so many teams have the effect without having chosen it. |
| Skills instead of MCP servers | **3–32×** fewer tokens for equivalent capability | independent, wide spread | Measurements cluster around 2.9× "at rest" for 10 equivalent capabilities and stretch to 32× on specific tasks. The spread is the finding: it depends entirely on how many schemas you were loading and how chatty the task is. |
| Trimming agent instructions | no reliable figure | — | Everyone agrees over-configured skills and MCP servers saturate the window and force early compaction. Nobody publishes a token figure, because it's a property of your config, not of a technique. Measurable locally; not generalisable. |

The pattern: these are all the same intervention wearing different hats — don't
put things in the context until you need them. The claimed percentages differ
mostly because the baselines differ.

### Compressing what's already there

| Tool | Claim | Grade | What's actually being measured |
| --- | --- | --- | --- |
| Caveman | **65%** output reduction; 33.2% fewer input tokens over 54 runs with 18/18 correctness | vendor | **Contested.** Independent testing measured output-token saving on real agentic tasks at **8.5%**. A vendor benchmark in the same family reported ~33% input. That is a 7× gap between claim and independent result, and it is the cleanest example of why this tool should exist. |
| Headroom | **60–95%** fewer tokens | vendor, self-qualified | The project's own repository line is the honest one: *"20% fewer tokens for coding agents, 60–95% fewer tokens for JSON."* The headline range is the JSON case. The coding-agent case is 20%. Both are true; only one is relevant to a coding agent, and it is not the one that gets quoted. |
| RTK | **60–90%** on common dev commands | vendor | Rewrites shell commands through a proxy, filtering/grouping/truncating/deduplicating output. Plausible on the commands it targets; "common dev commands" is doing a lot of work, and the session-level effect depends on what share of your output those commands are. Headroom uses it underneath for shell output. |
| TOON | **~40%** fewer tokens than JSON, *with equal or better* accuracy (72.2% vs 71.4%) | contested | Official benchmarks report 39.6–46.3% token reduction with accuracy gains. An independent evaluation ranked TOON **9th of 12** formats at 47.5% accuracy, *below* JSON's 52.3%. Both can't be describing the same workload. Token reduction is easy to verify; the accuracy claim is where the disagreement lives. |
| Deliberate in-place compression | mechanism, not a number | structural | Distinct from compaction: compression targets individual blocks in place, compaction summarises and restarts. The distinction matters for cost in a way discussed below. |

### Retrieving more cleverly

| Technique | Claim | Grade | What's actually being measured |
| --- | --- | --- | --- |
| Semantic retrieval / LSP (Serena and similar) | "saves tokens" by not reading whole files | **contested, and the best-measured of the lot** | A measurement study ran a five-arm ablation (grep-only, LSP-only, both, semantic-forced, repo-map+grep) and found LSP *costs* +6% tokens on symbol localisation with Opus and **+118% with Sonnet**; +19% on reference-finding but with perfect precision (1.00 vs 0.76); and grep beat location-only LSP on multi-file renames (100% vs 67% success). It only clearly saved tokens for the weakest model (−26% with Haiku). The widely repeated efficiency claim is, in their words, asserted without clear evidence. |
| Knowledge graphs / code indexing | precise subgraphs instead of whole files | vendor / anecdotal | Mechanism is sound, published numbers are personal-project scale. Same shape of claim as LSP, and the LSP study is the cautionary precedent. |
| Subagents for context isolation | keeps exploration out of the orchestrator | structural | Uncontroversial as a mechanism. The net token effect is rarely measured, because measuring it requires the subagent's own spend — which, in our dataset, wasn't recorded at all. |

### The anti-pattern

**Tokenmaxxing** — treating token spend as a productivity proxy — is
well-documented enough to have its own literature, and the supporting data is
uncomfortable: GitClear's analysis of 211M changed lines plus the 2026 Faros AI
Engineering Report report code churn **+861%** under high AI adoption, bugs per
developer up from +9% (2025) to **+54%**, median code-review time **+441%**, and
PRs merged with no review at all up 31%. Concrete failures include a gamed
internal activity leaderboard, pulled within weeks, and at least one
organisation exhausting its annual AI budget by April.

The relevance to Tokenamun is direct: a tool that makes token spend legible is
one product decision away from being the instrument that enables this. Hence no
developer dimension, anywhere.

## Three systematic problems with almost all of these numbers

**1. The denominator is the intervention.** "98.7% fewer tokens" describes a
baseline that loaded 150K tokens of tool schemas. "60–95%" describes JSON
payloads. "3–32×" describes however many MCP servers the author had connected.
None of these are properties of the technique; they are properties of the mess
the technique was pointed at. A team with two MCP servers and a lean `AGENTS.md`
should expect approximately none of the advertised saving, and nothing in the
marketing tells them that.

*What Tokenamun must do:* report the observed baseline first and the
counterfactual second, always, so the reader can see whether their denominator
resembles the one in the claim.

**2. Token reduction is not cost reduction, and compression can make it worse.**
The sharpest finding in this survey comes from a cost lab that instrumented the
proxy layer: **cached input costs ~10% of list price, and compression works by
rewriting history — which breaks the cached prefix and reprices everything after
it.** A compression pass that removes 30% of your tokens can increase your bill,
because it converts cheap cache reads into full-price input. The same work
measured `/compact` payback at **18–19 turns**, against a pre-registered
prediction of 2–4.

This is a correction to the naive carry model, and it changes Tokenamun's
what-if arithmetic: an intervention's effect has to be evaluated in
cache-weighted terms, with the invalidation point identified, or the sign of the
result can be wrong.

*What Tokenamun must do:* price the four token classes separately, and report
any intervention that mutates context as (tokens saved after the change point)
minus (cache reads repriced to full input at the change point). Where it can't
determine the invalidation point, say so rather than netting it out.

**3. Nobody reports the success rate.** The LSP study's methodological
commitment is the one to steal: their metric is **tokens-to-success** — total
context tokens divided by *successful* rollouts — and *"we never report a token
number without its success rate."* Every vendor percentage above is a token
number without a success rate. A 65% output reduction that causes the agent to
re-ask for what it lost is not a 65% saving, and an agent that fails the task
consumes the fewest tokens of all.

*What Tokenamun must do:* it cannot see task success, and must not imply it can.
So it reports token effects and names the outcome question as explicitly
unanswered, rather than letting a reduction read as an improvement. This is what
the `unknown` section of every what-if result is for, and it's why an empty one
is a test failure.

## Secondary observations worth keeping

* **Model routing beats compression on the numbers.** Running the easy 80% of
  steps on a small model and escalating the hard 20% is reported at ~12% of
  all-frontier cost. If true even approximately, it dominates every compression
  result here — and it is measurable from our data, because model per API call
  is observed.
* **Prompt caching is the highest-leverage thing most teams already have.**
  Cached input at 10–25% of list price, applied to the 93–99% of spend that is
  input. Tokenamun observes `cache_read` versus `cache_creation` directly, so
  "is your caching actually working" is an *observed* question, not a
  counterfactual one. That is probably the cheapest real finding this tool can
  produce.
* **Accuracy sometimes moves with efficiency, in both directions.** Tool search
  improved tool-selection accuracy; TOON's accuracy claim is contested; LSP
  bought precision at a token premium. Efficiency and quality are not on
  opposite ends of one axis, which is another reason not to report reduction as
  improvement.

## What this survey changes about the plan

1. Add **cache-class weighting** to carry and to every what-if: token counts per
   class, and an explicit cache-invalidation term for interventions that rewrite
   context. Without it, a what-if can report a saving that is a loss. ✔ built;
   see [`METHODOLOGY.md`](METHODOLOGY.md) §3 and §6.
2. Add an **observed caching-health check** to `profile` —
   `cache_read` / `cache_creation` ratio and re-creation events. Observed, cheap,
   and the highest-value early finding.
3. Keep the **baseline-first output contract**: the observed quantity an
   intervention targets is printed before any counterfactual, because the
   denominator is where the claims go wrong.
4. **Never print a reduction without the outcome disclaimer.** Borrow
   tokens-to-success as the framing even though we can't compute the numerator's
   denominator: say what we measured, and say that success rate is not in this
   data.
5. Treat **`--replay-with=<cmd>`** as a priority rather than a nicety. Caveman
   claims 65% and independent testing says 8.5%; Headroom's own headline differs
   from its own coding-agent figure by 3–4×. The only way to settle that for a
   given repo is to pipe that repo's observed content through the real
   compressor and count. That is a small feature and it is the most useful thing
   here.

## Sources

Anthropic — [Code execution with MCP](https://www.anthropic.com/engineering/code-execution-with-mcp) ·
[Advanced tool use / tool search](https://www.anthropic.com/engineering/advanced-tool-use) ·
[Tool search tool docs](https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool)

Independent measurement — [Does a Language Server Save Tokens for Coding Agents? (arXiv 2608.13568)](https://arxiv.org/html/2608.13568) ·
[How Do AI Agents Spend Your Money? (arXiv 2604.22750)](https://arxiv.org/pdf/2604.22750) ·
[agent-cost-lab](https://github.com/yuki-uix/agent-cost-lab) ·
[Notation Matters: token-optimized formats benchmark (arXiv 2605.29676)](https://arxiv.org/pdf/2605.29676) ·
[TOON benchmarks, critical analysis](https://www.towardsdeeplearning.com/toon-benchmarks-a-critical-analysis-of-different-results-d2a74563adca) ·
[JetBrains: Speaking to AI agents like cavemen](https://blog.jetbrains.com/ai/2026/07/speak-to-ai-agents-like-cavemen-tosave-tokens/) ·
[Agent Skills vs MCP: measuring the actual context cost](https://dev.to/topuzas/agent-skills-vs-mcp-i-stopped-reading-hot-takes-and-measured-the-actual-context-cost-1cp1) ·
[Production results: MCP code-first pattern at 112 tools](https://github.com/orgs/modelcontextprotocol/discussions/629)

Tools — [Headroom](https://github.com/headroomlabs-ai/headroom) ·
[RTK](https://github.com/rtk-ai/rtk) ·
[Serena](https://github.com/oraios/serena) ·
[TOON](https://toonformat.dev/guide/benchmarks) ·
[Graphify](https://github.com/Graphify-Labs/graphify) ·
[Caveman](https://www.producthunt.com/products/caveman)

Spend analysis — [Augment Code: where token spend really goes in an agent loop](https://www.augmentcode.com/guides/ai-coding-cost-analysis-agent-token-spend) ·
[Vantage: the hidden cost driver in agentic coding](https://www.vantage.sh/blog/agentic-coding-costs)

Anti-pattern — [Faros AI Engineering Report 2026](https://www.faros.ai/blog/ai-acceleration-whiplash-takeaways) ·
[IBM: what is tokenmaxxing](https://www.ibm.com/think/topics/tokenmaxxing) ·
[InfoWorld: tokenmaxxing](https://www.infoworld.com/article/4208123/tokenmaxxing-the-strangest-developer-productivity-metric-of-all-time.html)
