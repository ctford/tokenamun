# Interventions: what people try, and what can be checked

Tokenamun does not model named techniques. It measures a session and lets you
name a hypothetical:

```
tokenamun optimise --at "cli output" --optimise 0.5 --why "Vendor figure, not measured here."
```

This document is the other half of that: a catalogue of the interventions
people actually try, what part of a session each one acts on, and which of them
can be checked against evidence at all. It used to be a set of built-in
estimates. Those were deleted, because a vendor's figure applied to your
session is that vendor's claim wearing this tool's authority — see
[the deletion](#why-there-are-no-built-in-estimates) at the end.

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

## Volume: less content

### Compressing tool output

A proxy between the tool and the context that rewrites what passes through —
Caveman, Headroom, RTK-style adapters.

* **Acts on** `cli output`, `mcp output`, `web content`.
* **Addressable, measured:** `tokenamun tree --at "cli output"`. On the two
  reference repositories this was 22% and 11% of session cost.
* **What nobody measures:** the ratio on *your* content. Published figures span
  8.5% to 65% for the same tool — an eight-fold spread, and both ends were
  measured on somebody else's output. Your own split matters: build logs and
  status noise compress well, and a file the agent went looking for compresses
  into a second tool call.
* **Netting:** rewriting context invalidates the cached prefix from that point,
  turning cheap reads into full-price writes. A compression saving quoted
  without that is gross, not net.
* **Check it:** pick the ratio yourself and say why. `--at "cli output"
  --optimise 0.5 --why "Measured on our own logs."`

### Compressing the files themselves

Shorter documents, deleted dead code, less duplication. Distinct from a proxy:
a smaller file is smaller every time anything reads it, smaller in every
re-send, and smaller in every prefix rebuild.

* **Acts on** `file content`, and you can point at a directory:
  `--at "file content/docs"`.
* **Addressable, measured:** per file, with round trips. The order is not the
  order a directory listing gives you — a 91 KB file read 22 times outranks a
  28 KB file read once by three to one, and ranking by bytes puts them the
  other way round. `tokenamun hotspots` joins file size and complexity onto
  session cost from the other end.
* **What nobody measures:** whether the shorter file still answers the question.
  Trimmed past that point it is read *and* something else is read as well.
* **Also:** unlike a proxy this changes the repository, and the files are read
  by people too. Their time is not in this budget.

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
  few long ones, it is noise. `tokenamun period` tells you which you have.

## Round trips: the same content, re-sent fewer times

The model has no memory between calls, so everything still in the context is
sent again on every call and billed each time. Arriving early and staying is
what makes content expensive — not being large.

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

### Compaction

* **Acts on** round trips, and it happens to you rather than being chosen.
* **Observed:** `tokenamun carry` reports the calls where the context was reset.
  They truncate every residency span, which is why nothing in a compacted
  session is resident for the whole of it.
* **Worth knowing:** compaction is also a cache miss, so it appears in
  `tokenamun cache` under its own cause rather than being blamed on the TTL.

## Price: the same content, cheaper per send

### Cache TTL

Moving from the 5-minute prompt cache to the 1-hour one —
`promptCacheTtl`, Claude Code v2.1.242+.

* **Acts on** price. It changes nothing about what is in the context.
* **This is the best-evidenced intervention in this document, and the only one
  with no assumed parameter.** Which TTL each call used is observed from the
  API's own 5m/1h split. Misses are attributed per cause — model switch, Claude
  Code upgrade, compaction, effort change are all observable — with TTL expiry
  reached by elimination. The multipliers are published.
* **It can cost more than it saves,** and that is the point of computing it. The
  1-hour TTL prices *every* cache write at 2.0× instead of 1.25×. A session of
  short bursts that never idles past five minutes buys a lifetime it never
  uses. On one 19-call session over 15 minutes this came out 23% worse; on a
  1,926-call session over 43 hours it was 7.4% better.
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
* **Not a token question:** switching model changes price *and* changes the
  work. `tokenamun compare` on two sessions is the honest form, and even that
  cannot see whether the output was as good.

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
  traffic that ceiling was **1.3% of session cost** — worth knowing next to the
  7.1% of MCP *results* in the same session, which is the number someone
  reaches for and which this intervention does not touch. Results arrive either
  way; only the schemas leave.
* **How to actually measure it:** run the same opening prompt with the server
  connected and disconnected, and compare the first call's prompt size. That
  difference is observed. `tokenamun compare` does it.

### Deferred tool loading and tool search

Same reason, same shape: the saving lives entirely inside the preamble, which
cannot be decomposed. You can count tools *used* against tools *available* when
the harness records a listing, which tells you whether the mechanism has
anything to bite on — but not its token value.

### Fewer or smaller skills

Definition sizes are on disk, not in the transcript. Entire records which
skills fired, so you can see what was invoked; the cost is a local file
measurement rather than an observation of the session.

### Code-mode MCP and code execution over MCP

The pattern it targets *is* detectable: large intermediate payloads that arrive
in context and are then echoed back out in a subsequent tool input. Tool inputs
are observed — `model output / tool inputs` is 16% to 48% of the sessions
here — so the round trip is visible. Sizing the fix needs a counterfactual
about code the agent never wrote.

### Knowledge graphs and code indexing

What the intervention *replaces* is measurable, and precisely: how much content
was retrieved to find things, from where, how much was re-retrieved, and what
carrying it cost. The post-intervention side needs a second session.
`tokenamun period --since` is the before-and-after form.

## Where to start

Ranked by evidence quality rather than by claimed upside, which is the
inversion this document exists to make possible.

1. **Cache hygiene.** Observed end to end, no assumed parameter, and the
   largest number measured here by a wide margin: 40% of one session's
   effective input bill went on prefixes that expired while someone was
   thinking. One setting. Nobody is talking about it.
2. **Repeated retrieval.** Observed, per session, and the counterfactual is
   arithmetic. Also the most likely to be free: nobody wants the same file
   three times.
3. **Cost of carry on large, early-arriving content.** Observed cost, observed
   call count, arithmetic in between. Tells you which retrievals were expensive
   *decisions* rather than which were large.
4. **Preamble size.** Observed exactly, bounded in decomposition, and the
   multiplier by call count is usually the surprise. Check whether your work is
   many short sessions before spending time here.
5. **Output compression, including a compact format.** Real addressable volume,
   an assumed ratio, and a behavioural risk nobody measures. Measure the ratio
   on your own content before quoting anyone's.
6. **MCP and tool-schema interventions.** Plausible, possibly large,
   unmeasurable here. If you want evidence, measure at the request layer. That
   is a different tool, and saying so beats inventing a percentage.

## Why there are no built-in estimates

There used to be eight: `cache-ttl`, `repeated-retrieval`,
`clear-on-new-task`, `output-compression`, `file-compression`, `caveman`, `rtk`
and `mcp-to-cli`, with a plugin protocol for adding more. They are gone.

Everything that shrinks content does the same two things — pick a part of the
session, make it smaller — and the answer is always the product of that part's
share and the change. So a named intervention added nothing but a vendor's name
and a default ratio, and it added one thing it should not: the appearance that
the tool knew something about that vendor. The `caveman` row reported 13% of a
session. That figure came from `--ratio`'s default of 50%, applied to measured
volume, on evidence that spans 2% to 17%. It looked like a finding and was an
assumption with a logo on it.

Two of the eight were real models — `cache-ttl` and `repeated-retrieval`, both
with every input observed — and their content survives as the measurements
above, in `tokenamun cache` and `tokenamun retrieval`, where they are reported
as what they are rather than as counterfactuals.

## The anti-pattern to avoid building

**Token spend as a productivity metric.** Tokens are an input, not an outcome,
and treating them as the latter is the lines-of-code vanity metric with a new
unit. It has produced real damage: at least one gamed internal leaderboard, and
at least one AI budget exhausted a third of the way through the year.

It is also the failure mode this tool is closest to. A profiler that reported
"tokens per developer" would be worse than no profiler. Hence:

* **Filtering by developer is fine; reporting by developer is not.** Analysing
  your own sessions, or a colleague's at their request, is how you help. A
  column comparing people is how a leaderboard starts. The line is between
  choosing whose work to look at and publishing a ranking of it.
* No developer dimension in any command's output, including `hotspots`.
* Findings are framed against the engineering system — subsystems, file
  properties, retrieval patterns — never against people.
* A reduction is never reported as an improvement without the outcome question
  attached. Spending fewer tokens to do worse work is not a win, and Tokenamun
  cannot see work quality, so it must not imply that it can.
