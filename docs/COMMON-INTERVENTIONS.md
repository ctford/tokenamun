# Common interventions: what people try, what the evidence says, and what you can check

Tokenamun does not model named techniques. It measures a session and lets you
name a hypothetical:

```
tokenamun optimise --at "cli output" --optimise 0.5 --why "quieter test runner output"
```

This document is the other half of that: what people try, which part of a
session each technique acts on, how good its published evidence is, and which
can be checked against your own data. It used to be a set of built-in
estimates; see [why they were deleted](#why-there-are-no-built-in-estimates).

Evidence grades used below: **vendor** (self-reported, own benchmark),
**independent** (third party reproduced or measured), **contested**
(independent result materially disagrees), **structural** (follows from how the
API works, hard to dispute). [Sources](#sources) are at the end.

A source can be more than one at once, so each claim is graded on its own.
Anthropic's cost guide is both: its benchmarks are **vendor** — own harness,
own tasks, list prices on the day — while its account of cache invalidation and
the multipliers is **structural**, and `internal/cost` prices with them.

## The one thing everybody agrees on

Input tokens dominate agentic coding spend — commonly cited at 93–99% of
trajectory volume, with one analysis attributing 62% of the bill to re-sent
context alone. The model has no memory between turns, so every turn re-sends
the accumulated conversation and cost grows with session length regardless of
how much new work is happening. An arXiv study of agent spending finds input
dominance holds *even with prompt caching in use*.

Measurement here agrees emphatically: across eight sessions, ~711K tokens of
unique tool-result content sat behind ~856M tokens of billed input
([`ENTIRE.md`](ENTIRE.md#prompt-size-is-observed-on-every-call)). The content
is not the cost. **Carrying** the content is the cost.

That is the good news for a profiler — the dominant term is a function of when
content enters and how long it stays, both observable. It also means every
claim below should be read as a claim about *residency*, and most of them are
not stated that way.

## The three things an intervention can change

A part of a session costs **volume × round trips × price**. Every technique
moves exactly one of those factors, and which one tells you what evidence is
available.

| axis | what it means | how to check it here |
| --- | --- | --- |
| **volume** | less content enters the context | `tokenamun tree` measures the part; `optimise --at` prices a change to it |
| **round trips** | the same content is re-sent fewer times | `tokenamun carry` and the round-trip column; a change needs a different session |
| **price** | the same content costs less per send | `tokenamun cache` — observed, and the one axis with no assumption in it |

Only a change in **volume** reads as a discount on the treemap: the same
rectangles, smaller. The other two leave the picture the same shape and change
what it cost, which is why they cannot be expressed as `--optimise` on a node.

## Why the published percentages do not transfer

Three problems run through almost every number in this document. They are also
the reasons behind the output contract in
[`METHODOLOGY.md`](METHODOLOGY.md#6-counterfactuals) §6.

**1. The denominator is the intervention.** "98.7% fewer tokens" describes a
baseline that loaded 150K tokens of tool schemas. "60–95%" describes JSON
payloads. "3–32×" describes however many MCP servers the author had connected.
None of these are properties of the technique; they are properties of the mess
it was pointed at. A team with two MCP servers and a lean `AGENTS.md` should
expect approximately none of the advertised saving, and nothing in the
marketing says so. Hence: the observed baseline prints before the
counterfactual, always.

**2. Token reduction is not cost reduction, and compression can invert it.**
Cached input costs around a tenth of list price, and compression works by
rewriting history — which breaks the cached prefix and reprices everything
after it. A pass that removes 30% of your tokens can increase your bill. The
same cost lab measured `/compact` payback at 18–19 turns against a
pre-registered prediction of 2–4. Hence: four token classes priced separately,
and an intervention that mutates context is reported net of the invalidation or
not netted at all.

**3. Almost nobody reports the success rate.** The LSP study's commitment is
the one worth stealing — its metric is *tokens-to-success*, total context
tokens over *successful* rollouts, and "we never report a token number without
its success rate". Most tool-vendor percentages here are token numbers without
one. A 65% output reduction that makes the agent re-ask for what it lost is not
a 65% saving.

The exception is instructive. Anthropic's cost guide reports accuracy beside
cost almost throughout — tool search at 45% less cost with accuracy unmoved, a
CSV uploaded instead of pasted at 6/25 → 25/25 correct for 92% less, a prompt
audit at 14% cheaper for five points more. The asymmetry is structural: a model
vendor is paid whichever way accuracy lands, so publishing it costs nothing,
while a tool vendor whose product *is* the reduction has one number that sells
and one that can only hurt. Read the grades carefully; a vendor benchmarking
its own model is still a vendor. Either way Tokenamun cannot see task success,
and says so every time.

## Volume: less content

### Compressing tool output

A proxy between the tool and the context that rewrites what passes through —
Caveman, Headroom, RTK-style adapters.

* **Acts on** `cli output`, `mcp output`, `web content`.
* **Addressable, measured:** `tokenamun tree --at "cli output"`. On the two
  reference repositories this was 22% and 11% of session cost.
* **The evidence, graded:** Caveman claims 65% output reduction (vendor);
  independent testing on real agentic tasks measured 8.5% — an eight-fold gap,
  and the cleanest example of why this tool exists. A second vendor figure in
  the same family reports 33.2% fewer input tokens over 54 runs *with 18/18
  correctness*, the only tool-vendor claim here carrying a success rate.
  Headroom's own repository line is the honest one: "20% fewer tokens for
  coding agents, 60–95% fewer tokens for JSON" — the range is the JSON case,
  the coding-agent case is 20%. RTK claims 60–90% on "common dev commands"
  (vendor), where the session-level effect depends on what share of your
  output those commands are.
* **What nobody measures:** the ratio on *your* content. Both ends of that
  spread came from someone else's output, and the split matters: build logs
  compress well, while a file the agent went looking for compresses into a
  second tool call. Settling it means piping your own content through the real
  compressor and counting — a thing to build, not a figure to quote.
* **Netting:** rewriting context invalidates the cached prefix from that point,
  turning cheap reads into full-price writes. A saving quoted without that is
  gross, not net.
* **Check it:** pick the ratio yourself and say why. `--at "cli output"
  --optimise 0.5 --why "Measured on our own logs."`

### Compressing the files themselves

Shorter documents, deleted dead code, less duplication. Unlike a proxy, a
smaller file is smaller on every read, every re-send and every prefix
rebuild.

* **Acts on** `file content`, and you can point at a directory:
  `--at "file content/docs"`.
* **Addressable, measured:** per file, with round trips. The order is not the
  order a directory listing gives you — a 91 KB file read 22 times outranks a
  28 KB file read once by three to one, and ranking by bytes puts them the
  other way round. `tokenamun hotspots` joins file size and complexity onto
  session cost from the other end.
* **What nobody measures:** whether the shorter file still answers the
  question. Trimmed past that point it is read *and* something else is too.
* **Also:** this changes the repository, and people read the files as well.
  Their time is not in this budget.

### A more compact output format

TOON, compact JSON, tables instead of objects — re-encoding uniform structured
data so the same information costs fewer tokens.

* **Acts on** whichever branch the structured data arrives in, usually
  `mcp output` or `cli output`.
* **Addressable, measured:** how much of your tool output is uniform structured
  data, per tool. Drill in: `--at "mcp output"` then by tool.
* **Why this is the best-founded of the compression family:** the saving is a
  property of the encoding rather than of the content's meaning. Nothing is
  discarded, so the sufficiency question that haunts every other compression
  technique does not arise.
* **The evidence, graded: contested.** TOON's official benchmarks report
  39.6–46.3% fewer tokens than JSON *with equal or better* accuracy (72.2% vs
  71.4%). An independent evaluation ranked it 9th of 12 formats at 47.5%
  accuracy, *below* JSON's 52.3%. Both cannot be describing the same workload.
  The token reduction is easy to verify; the accuracy claim is where the
  disagreement lives — which is the retry risk below, quantified by nobody.
* **What nobody measures:** whether the model reads the compact form as
  reliably. A format the model mis-parses costs a retry, and a retry is a whole
  round trip.

### Not fetching the same thing twice

* **Acts on** any branch. Byte-identical content, identified by hash.
* **Measured, not assumed:** `tokenamun retrieval` reports redundant bytes and
  their share. This is the strongest counterfactual available anywhere in this
  document — fetching something once instead of twice is arithmetic, not
  modelling. On one reference session it was 24% of retrieved bytes; on five
  others, zero.
* **The one unknown:** the agent re-read it for a reason, even if the reason was
  forgetting. Removing the second copy assumes it still had the first in mind.

### Shorter test and build output

* **Acts on** `cli output`, under the test runner or build tool.
* **Well-formed as a target:** large, repetitive, and identifiable from the
  command line. `--at "cli output/go"` or wherever your suite lives.
* **Worth checking first:** test output arrives late in a session, when carry is
  cheapest. The round-trip column tells you whether that is true for you rather
  than assuming it.

### Shorter answers from the model itself

The agent's own output: final summaries, the narration between tool calls, and
whatever shape the system prompt asks answers to take.

* **Acts on** `model output`, which is the largest box in some sessions here —
  45% of one — and contains `model output / tool inputs` at 16% to 48%.
* **Addressable, measured:** output tokens are observed per call and weighted
  at 5.0. This is the one branch where volume and cost diverge by a multiple
  upwards rather than a tenth downwards.
* **Why it compounds:** in an agent loop every token the model writes comes
  back as input on every later turn. It is billed once at 5.0 and then carried
  — the residency argument the rest of this document makes, with a five-fold
  head start.
* **The evidence, graded: vendor.** One triage job under three final-answer
  instructions: one line, the original two, and a five-section memo at nearly
  3× the one-line cost. The one-line form used 39% fewer output tokens than
  the two-line and cost 14% less. All three scored 78–85% correct, and the
  figures do not say which format landed where in that band — so the
  supportable reading is that the extra spend bought nothing visible, not that
  brevity was free.
* **What nobody measures:** where your own curve turns over. An answer format
  too cramped to carry the finding costs a follow-up question, and a follow-up
  question is a whole round trip.
* **Check it:** `--at "model output" --optimise 0.7 --why "..."`. What comes
  back is the direct saving; the carry it avoids on later turns is the larger
  half and is not in it.

### Trimming instructions and the preamble

`CLAUDE.md`, `AGENTS.md`, skills, tool schemas — everything sent before any
work happens.

* **Acts on** `preamble`.
* **Bounded, not decomposed:** the first call's prompt is observed exactly, and
  it is paid again on every call. What share is your instructions versus the
  system prompt versus tool schemas is **not in the transcript**, and this tool
  will not guess. The ceiling is still useful: it tells you whether the whole
  category is worth an hour.
* **A caution:** the preamble's cost looks tiny in a long session (1.3% of one
  43-hour session) and dominant in a short one (38% of a 19-call session). If
  your work is many short sessions, this is your biggest line item; if it is
  few long ones, it is noise. `tokenamun length` tells you which you have.
* **Stale instructions are not free, and the sign surprises people.** A prompt
  written for an older model can cost *more* on a newer one: a support-desk
  prompt carried from Opus 4.8 to Opus 5 ran 36% more expensive for no accuracy
  gain, and auditing it turned that into 14% cheaper and five points better
  (vendor). The patterns named are instructions that compensate for a weaker
  model — "verify twice", hand-rolled reasoning scratchpads, mandatory
  step-by-step procedures that force extra tool rounds. **Not measurable here:**
  the preamble's size is observed and its contents are not, so this is a thing
  to do rather than a thing to check. The before-and-after is observable, by
  `tokenamun compare` on two sessions.

## Round trips: the same content, re-sent fewer times

Arriving early and staying is what makes content expensive — not being large.

### Clearing before a new task

Starting a fresh context at a task boundary instead of carrying the last one
forward.

* **Acts on** everything resident, by truncating its residency.
* **What is measurable:** the cost of what was being carried across a boundary.
  Task boundaries are not recorded anywhere, so they have to be inferred — an
  idle gap before something you typed is the only signal a transcript has.
* **What is not:** the cost of the intervention. After a clear the agent
  re-reads whatever it still needs, and none of that is in the transcript. Any
  saving is a **ceiling**, and whatever share it would have re-fetched comes
  straight back off.
* **Also:** a clear rebuilds the preamble at the write rate, and you have to say
  again what you were doing.
* **In practice:** on the sessions here this was worth about 1.2% at its
  ceiling, and usually not applicable at all, because long single sittings have
  no boundary to clear at. It may matter much more for a working style of many
  short tasks in one session.

### Pruning stale results at a task boundary

Replacing large, finished tool results with a one-line extract while leaving
the conversation otherwise intact. Distinct from clearing: the thread survives,
so there is nothing to say again.

* **Acts on** round trips, by ending the residency of content still being paid
  for after it stopped being useful.
* **Addressable, measured:** this is the intervention this tool is best placed
  to price. `tokenamun carry` gives the cost of keeping a thing and `tree`
  gives the round trips, so the ceiling is the carry on large tool results
  after the point they were last needed.
* **What is not observable:** that point. A transcript records when content
  arrived and when the context reset, not when the agent stopped caring. Name
  the boundary yourself and the tool will price it.
* **The evidence, graded: vendor, and it cuts both ways.** On a long run a
  prune saved 39% and compaction 32%. On a short one it saved *nothing*, and a
  context-editing variant cost 74% more. That is the denominator problem in §1
  conceded by the vendor: the technique is worth nothing until there is stale
  content to prune, and a short session does not have any.
* **Netting:** editing history breaks the prefix from that point. Measured
  there it cached well anyway — 89% cache reads on the first request after a
  boundary, 81% between them — because the edits sit at the tail, where the
  next task was going to add content regardless. The rule that follows is to
  prune in few large batches rather than many small ones, since each one pays
  for a rebuild.

### Subagents

Delegating exploration so the orchestrator's context never sees it.

* **Acts on** round trips, by keeping content out of the long-lived context
  entirely. The parent sees a report instead of the exploration.
* **Measurable, on the parent side:** `subagent reports` is the returned summary,
  and the carry it avoided is the difference between that and doing the
  exploration inline.
* **Not measurable, and this is the catch:** the subagent's own spend was
  **absent from every session** in these datasets despite `Agent` being called.
  Without it, a claimed net saving is unverifiable — you are comparing a
  measured parent-side saving against an unmeasured child-side cost. Tokenamun
  reports which half it has.
* **The evidence, graded: vendor, both directions.** Delegation is reported to
  insure the tail rather than the median: on one easy slice the frontier model
  alone cost nearly 3× the delegated configuration at the 90th percentile, and
  its single most expensive run was also wrong. On work that fits one context
  window, or that is one dependent chain, the single model was cheaper every
  time. Both halves are consistent with the
  measurement gap above — the saving lives in the child's spend, and that is
  the half absent here.

### Compaction

* **Acts on** round trips, and it happens to you rather than being chosen.
* **Observed:** `tokenamun carry` reports the calls where the context was reset.
  They truncate every residency span, which is why nothing in a compacted
  session is resident for the whole of it.
* **Worth knowing:** compaction is also a cache miss, so it appears in
  `tokenamun cache` under its own cause rather than being blamed on the TTL.

### Lowering effort

Asking for less thinking, less verification and fewer tool calls per turn.

* **Acts on** round trips first and output volume second, which is why it is
  here rather than under price: the rate per token does not change, the number
  of calls does.
* **Observed, partly:** the effort of every call is in the transcript and
  Tokenamun already reads it — a change of effort is one of the cache-miss
  causes it attributes, and it is priced. What is missing is the
  counterfactual. Nothing in one session says what the same work would have
  cost at a lower setting, so this needs a second session rather than an
  assumed ratio.
* **The evidence, graded: vendor, and strongly workload-dependent.** On
  long-horizon coding the curve is steep: about 2 points off for half the cost
  at `medium`, about 8 points off for a quarter at `low`. On knowledge and
  research work it is nearly flat, `medium` matching `high` at 70–87% of cost.
  A single figure for "lower effort" would be meaningless. The shape is the
  finding.
* **The strongest version of this is closed to us.** Running everything at
  `low` and re-running only the failures reached about 93% pass at roughly half
  the cost per attempt, against 91.7%. It is the best cost result in that
  document and it is unavailable to a profiler, because it needs a pass/fail
  signal and Tokenamun cannot see one: the largest lever on this list is gated
  on the thing this tool explicitly cannot measure.
* **Also:** changing effort mid-session invalidates the prefix from that point.
  `tokenamun cache` prices that under `effort_change` — so the cost of
  *switching* is measured even though the benefit of *having switched* is not.

## Price: the same content, cheaper per send

### Cache TTL

Moving from the 5-minute prompt cache to the 1-hour one —
`promptCacheTtl`, Claude Code v2.1.242+.

* **Acts on** price. It changes nothing about what is in the context.
* **This is the best-evidenced intervention in this document, and the only one
  with no assumed parameter.** Which TTL each call used is observed from the
  API's own 5m/1h split, the multipliers are published, and misses are
  attributed per cause rather than all blamed on expiry
  ([`METHODOLOGY.md`](METHODOLOGY.md#5-cache-misses-are-attributed-to-a-cause)).
* **It can cost more than it saves,** and that is the point of computing it. The
  1-hour TTL prices *every* cache write at 2.0× instead of 1.25×. A session of
  short bursts that never idles past five minutes buys a lifetime it never
  uses. On one 19-call session over 15 minutes this came out 23% worse; on a
  1,926-call session over 43 hours it was 7.4% better.
* **There is a third option, and Tokenamun does not price it yet.** Rather
  than buying the longer lifetime you can keep the short one warm: resend the
  previous request with `max_tokens` set to 0 within four minutes of that
  request's *start*, and every four minutes after. On the 5.1 generation, whose
  cache reads are 0.025× rather than 0.1×, that measured 13–20% cheaper per
  session than the 1-hour TTL whenever pauses ran for minutes; the 1-hour
  setting only won once pauses approached 45 minutes, and then by about twelve
  cents a session (vendor, over structural multipliers). Every input to that
  comparison is observed here — the gaps, the prefix sizes, the per-model read
  rate — so `tokenamun cache` offers a two-way counterfactual where the data
  supports a three-way one.
* **Check it:** `tokenamun cache`. The by-cause table is the whole answer.

### Avoiding mid-session cache invalidation

Fewer `/model` switches, fewer effort changes, fewer plugin toggles.

* **Observed, per cause, separately priced.** "Your four model switches cost X"
  is a derived number, not an estimate. Note that `opusplan` makes every
  plan-mode toggle a model switch.
* **Not visible:** MCP server changes and tool-deny rules also invalidate the
  cache and leave no trace in a transcript. They land in `unexplained` rather
  than being blamed on the TTL.

### Model choice

* **Observed:** the model per API call, so per-model token and call
  distributions are available. Cost weights are model-relative, so a
  mixed-model session still adds up.
* **The evidence, graded: vendor, and better than the routing claim it
  replaces.** An earlier version of this entry cited "route the easy 80% of
  steps to a small model, escalate the hard 20%, pay ~12% of all-frontier
  cost". The controlled versions are less flattering and more useful. Two
  shapes are distinguished: an **advisor**, where a cheaper executor escalates
  hard decisions, and an **orchestrator**, where a frontier model plans and
  delegates bulk work to cheaper workers.
* **Where an orchestrator pays:** on a corpus larger than any context window,
  a lead over 25 cheaper workers cost 47–55% less than the same model solo and
  finished in about 2.3 hours against 15 to 20, for 10 to 12 points of
  accuracy. The condition is work that fans out into independent pieces, not
  the price of the models.
* **Where they do not, which is the half worth carrying:** on the full
  BrowseComp set the single model alone reached the coordinator's accuracy at
  22–30% *lower* cost. An advisor pairing on chart reading came in at 65.0
  against the advisor model working alone at 67.5 — within noise — for about
  2.6× the cost per task, because the executor consulted on nearly every task.
  The stated rule is to baseline one model's whole effort curve before adding
  a second.
* **Price the tail, not the median.** On one 20-problem run, two problems
  carried 43% of the spend — the strongest case anywhere here for reading a
  distribution rather than a total. It sits awkwardly beside the rule below
  against reporting by developer, since a tail of sessions can be one person's
  week. Left unresolved deliberately: it is a decision, not a feature.
* **Not a token question:** switching model changes price *and* changes the
  count. The same text is reported to cost about 30% more tokens on Opus 4.7
  and later, which is a tokenizer change rather than a behavioural one. A token
  delta measured across a model switch is therefore not a saving and may not
  have the right sign. It also means the byte-ratio estimator in
  [`METHODOLOGY.md`](METHODOLOGY.md#7-what-it-cannot-measure) is calibrated per
  session for a reason. `tokenamun compare` on two sessions is the honest form,
  and even that cannot see whether the output was as good.

## Not measurable from a transcript

These are plausible, possibly large, and the evidence is not in this data.
Saying so is more useful than a fabricated percentage.

### Putting a CLI in front of an MCP server

* **Where the cost is:** tool schemas, which sit in the preamble on every call
  whether or not anything calls the server.
* **Why a transcript cannot settle it:** schemas are not in it. Worse, a session
  with zero MCP calls is indistinguishable from a session with no server
  attached — and the first of those is the expensive case the intervention
  exists for. Reporting 0% would be wrong in the expensive direction.
* **What you can bound:** the whole preamble. On one session with real MCP
  traffic that ceiling was **1.3% of session cost**, next to the 7.1% of MCP
  *results* in the same session — which is the number someone reaches for, and
  which this intervention does not touch. Results arrive either
  way; only the schemas leave.
* **The evidence, graded: vendor, with independent support.** Anthropic's
  code-execution-with-MCP figure is 150,000 → 2,000 tokens, 98.7%. That is one
  illustrative workflow. Independent reproductions land lower and scale with
  tool count — 58% at 96 tools, 84.5% at 251, 92.8% at 508, and one measuring
  78.5% input-token reduction; a GitHub-tools implementation held ~98% at 112.
  The saving is real and it is a function of how many tools you had loaded,
  which makes the headline a property of the baseline.
* **How to actually measure it:** run the same opening prompt with the server
  connected and disconnected, and compare the first call's prompt size. That
  difference is observed. `tokenamun compare` does it.

### Deferred tool loading and tool search

Same reason, same shape: the saving lives entirely inside the preamble, which
cannot be decomposed. You can count tools *used* against tools *available* when
the harness records a listing, which tells you whether the mechanism has
anything to bite on — but not its token value.

The vendor figure is 85% fewer tool-definition tokens, which is arithmetic on a
number you can count: schemas cost 100–400 tokens each. More interesting is the
reported *accuracy* gain, tool selection 79.5% → 88.1%, which suggests the win
is not only cost. Claude Code already applies this automatically once
deferrable definitions exceed 10% of the context window, so many teams have the
effect without having chosen it.

A later and better-controlled run is worth the update, because it holds
accuracy fixed while varying the thing that matters. Growing a catalogue to
502 tools nearly doubled the cost of a run with every definition loaded, and
left it flat at every catalogue size behind tool search — 45% less at 502 —
with accuracy 15 to 18 of 20 in every cell either way. Deferring a single
public GitHub MCP server's toolset cut a run 20% at the same accuracy. The
saving is still a function of how many schemas you had loaded: a property of
your baseline, and yours is not decomposable here.

### Fewer or smaller skills

Definition sizes are on disk, not in the transcript. Entire records which
skills fired, so you can see what was invoked; the cost is a local file
measurement rather than an observation of the session.

Independent measurements of skills against equivalent MCP servers cluster
around 2.9× "at rest" for ten equivalent capabilities and stretch to 32× on
specific tasks. The spread is the finding: it depends entirely on how many
schemas you were loading and how chatty the task is.

### Code-mode MCP and code execution over MCP

The pattern it targets *is* detectable: large intermediate payloads that arrive
in context and are then echoed back out in a subsequent tool input. Tool inputs
are observed — `model output / tool inputs` is 16% to 48% of the sessions
here — so the round trip is visible. Sizing the fix needs a counterfactual
about code the agent never wrote.

### Semantic retrieval, language servers, knowledge graphs

Reading symbols instead of whole files — Serena and similar, LSP-backed tools,
code indexes.

**The evidence, graded: contested, and the best-measured thing in this
document.** A five-arm ablation (grep-only, LSP-only, both, semantic-forced,
repo-map plus grep) found the language server *costs* tokens rather than
saving them: +6% on symbol localisation with Opus and **+118% with Sonnet**;
+19% on reference-finding, though with perfect precision against grep's 0.76;
and grep beat location-only LSP on multi-file renames, 100% success against
67%. It clearly saved tokens only for the weakest model tested, −26% with
Haiku. The widely repeated efficiency claim is, in the authors' words,
asserted without clear evidence.

Knowledge graphs and code indexing make the same shape of claim with
published numbers at personal-project scale. The LSP study is the cautionary
precedent: the mechanism being sound does not make the direction obvious.

What the intervention *replaces* is measurable here, and precisely: how much
content was retrieved to find things, from where, how much was re-retrieved,
and what carrying it cost. The post-intervention side needs a second session.
`tokenamun tree all --since` and `--until` are the before-and-after form.

**Accuracy moves with efficiency in both directions.** Tool search improved
tool-selection accuracy; TOON's accuracy claim is contested; the language
server bought precision at a token premium. Efficiency and quality are not
opposite ends of one axis.

## Where to start

Ranked by evidence quality rather than by claimed upside.

1. **Cache hygiene.** Observed end to end, no assumed parameter, and the
   largest number measured here by a wide margin: 40% of one session's
   effective input bill went on prefixes that expired while someone was
   thinking. Cached input costs 2.5–12% of list
   ([`METHODOLOGY.md`](METHODOLOGY.md#3-volume-is-not-cost) has the per-model
   multipliers) against the 93–99% of spend that is input. One setting.
   Nobody is talking about it.
2. **Repeated retrieval.** Observed, per session, and the counterfactual is
   arithmetic. Also the most likely to be free: nobody wants the same file
   three times.
3. **Cost of carry on large, early-arriving content.** Observed cost, observed
   call count, arithmetic in between. Tells you which retrievals were expensive
   *decisions* rather than which were large.
4. **Preamble size.** Observed exactly, bounded in decomposition, and the
   multiplier by call count is usually the surprise. Check whether your work is
   many short sessions before spending time here.
5. **Effort and model choice.** Observed per call, but the counterfactual
   needs a second session rather than an assumed ratio — which is a better
   class of evidence than a ratio you picked, not a worse one. Sweep the effort
   curve on one model before reaching for two.
6. **Output volume, the model's and the tools'.** Real addressable volume, an
   assumed ratio, and a behavioural risk nobody measures. The model's own
   output is weighted at 5.0 and then carried, so start there. Measure the
   ratio on your own content before quoting anyone's.
7. **MCP and tool-schema interventions.** Plausible, possibly large,
   unmeasurable here. If you want evidence, measure at the request layer. That
   is a different tool, and saying so beats inventing a percentage.

## Why there are no built-in estimates

There used to be eight, with a plugin protocol for adding more. They are gone.

Everything that shrinks content does the same two things — pick a part of the
session, make it smaller — and the answer is always that part's share times the
change. A named intervention added nothing but a vendor's name and a default
ratio, plus one thing it should not: the appearance that the tool knew
something about that vendor. The `caveman` row reported 13% of a session. That
came from `--ratio`'s default of 50% applied to measured volume, on evidence
spanning 2% to 17% — a finding in appearance, an assumption with a logo on it.

Two of the eight were real models, `cache-ttl` and `repeated-retrieval`, with
every input observed. They survive as measurements, in `tokenamun cache` and
`tokenamun retrieval`, reported as what they are rather than as
counterfactuals.

## The anti-pattern to avoid building

**Token spend as a productivity metric.** Tokens are an input, not an outcome,
and treating them as the latter is the lines-of-code vanity metric with a new
unit. It has produced real damage: at least one gamed internal leaderboard,
pulled within weeks, and at least one AI budget exhausted a third of the way
through the year. The surrounding data is worse than the anecdotes — GitClear's
analysis of 211M changed lines and the 2026 Faros AI Engineering Report
together report code churn +861% under high AI adoption, bugs per developer up
from +9% to +54%, median code-review time +441%, and PRs merged with no review
at all up 31%.

It is also the failure mode this tool is closest to. A profiler that reported
"tokens per developer" would be worse than no profiler. What follows from that
is in [`METHODOLOGY.md`](METHODOLOGY.md#9-not-a-productivity-metric). One
distinction belongs here in full: **filtering by developer is fine; reporting
by developer is not.** Analysing your own sessions, or a colleague's
at their request, is how you help. A column comparing people is how a
leaderboard starts. The line is between choosing whose work to look at and
publishing a ranking of it.

## Sources

Anthropic — [Code execution with MCP](https://www.anthropic.com/engineering/code-execution-with-mcp) ·
[Advanced tool use / tool search](https://www.anthropic.com/engineering/advanced-tool-use) ·
[Tool search tool docs](https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool) ·
[Optimizing for cost and intelligence](https://platform.claude.com/docs/en/about-claude/models/optimizing-for-cost-and-intelligence)

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
