# Methodology

How Tokenamun computes what it reports, what each number means, and where it
stops. If you are going to make an engineering decision from this tool's output,
this is the document that tells you whether you should.

Every figure quoted below was measured from a reference dataset of 8 Entire-recorded
Claude Code sessions (2,029 API calls) in a private Go monorepo.

## 1. Provenance: four kinds of number

Every number Tokenamun emits carries one of these labels. It is a type in the
data model, not a documentation convention, and the renderer will not print an
unlabelled quantity.

| Label | Meaning | Example |
| --- | --- | --- |
| **observed** | Present in the telemetry. No interpretation. | `cache_read_input_tokens` for an API call |
| **derived** | Deterministic arithmetic over observed values. Reproducible, no judgement. | Effective cost; deduplicated session totals; content hashes |
| **derived-approx** | Deterministic, but with a stated estimator and a reported error term. | Token counts from the calibrated byte-ratio estimator |
| **inferred** | A classifier's opinion. Could be wrong. | "This window was planning"; "this Bash output came from `src/foo.go`" |
| **counterfactual** | Arithmetic about a session that never happened. | "Compression would have removed 400K tokens" |

Two rules follow, and they are load-bearing:

* **Inferred and counterfactual values are never presented as measurements.**
  They render in separate sections, never mixed into an observed total.
* **What we cannot know is printed, not omitted.** Every counterfactual carries
  an `unknown` section. An intervention that produces an empty one fails a test.

## 2. One API call, not one transcript line

The single most important correctness rule.

In a Claude Code transcript an `assistant` entry is a **content block**, not an
API call, and every entry sharing a `requestId` repeats the same `usage` object
verbatim. In one reference session: 1,132 assistant entries, 691 unique
`requestId`s.

Summing usage per entry overstates by ~70%:

| | per entry (wrong) | per `requestId` (correct) | error |
| --- | --- | --- | --- |
| cache read | 494,206,381 | 289,418,574 | +71% |
| cache creation | 40,091,613 | 24,196,449 | +66% |
| output | 829,726 | 439,952 | +89% |

Tokenamun deduplicates by `requestId`, falling back to `message.id`, and reports
the collapse ratio as a diagnostic.

**Entire's per-checkpoint `token_usage` is not used for accounting at all.** In
the reference dataset it is a per-checkpoint delta in 28 of 41 checkpoints and
cumulative-from-session-start in the other 13, with the same `cli_version` and
no field distinguishing them. Summing one session's checkpoints yields ~1.47
billion cache-read tokens against a transcript total of 443 million. Checkpoints
are used only for slicing, `files_touched` and git attribution.

## 3. Volume is not cost

Raw token counts describe how much text moved. They do not describe what it cost,
and in agentic sessions the two differ by almost an order of magnitude.

**"Cache" here always means Anthropic prompt caching**, and every cache figure in
this document is read straight out of the `usage` object the Messages API
returns: `cache_read_input_tokens`, `cache_creation_input_tokens`, and the
`cache_creation.ephemeral_5m_input_tokens` / `ephemeral_1h_input_tokens` TTL
split. Nothing is modelled or simulated — the API tells us, per call, how much
of the prompt it served from cache, how much it wrote, and at which TTL.
(Tokenamun also keeps its own local content-hash cache for `count_tokens`
results. That is an implementation detail of this tool and never appears in any
reported figure.)

Prices are per-class multiples of a model's own input price:

| Class | Multiplier | Note |
| --- | --- | --- |
| Fresh input | 1.0× | |
| Cache read | **0.1×** | 0.025× on some models — per-model config |
| Cache write, 5-minute TTL | **1.25×** | |
| Cache write, 1-hour TTL | **2.0×** | |
| Output | 5.0× | holds across current Claude models, still config |

So Tokenamun's primary unit is the **effective input-equivalent token (EIT)**:
one full-price input token of the same model. Because the multipliers are
relative to each model's own input price, EIT is comparable across models in a
mixed session in a way that dollars are not. Dollars are available but opt-in,
because a price table is configuration, not measurement.

Measured on the reference dataset:

| | raw tokens | share of volume | EIT | share of cost |
| --- | --- | --- | --- | --- |
| Cache read | 807,112,468 | 94.2% | 80,711,247 | 56.7% |
| Cache creation | 49,284,083 | 5.8% | 61,605,104 | 43.3% |
| Fresh input | 4,058 | 0.0% | 4,058 | 0.0% |
| **Total prompt** | **856,400,609** | | **142,320,409** | |

**Raw prompt volume overstates cost by 6.0×.** Cache reads are 94% of the
volume and 57% of the cost; cache *writes* are 6% of the volume and 43% of the
cost.

This is why Tokenamun ranks by EIT by default and shows raw volume alongside,
never alone. A tool that reports a 40K-token file read as "expensive" without
knowing whether those tokens were billed at 0.1× or 1.25× is not measuring cost.
You cannot blame a tool for context volume that is being served from cache at a
tenth of list price.

## 4. Carry: content is cheap, keeping it is not

The model has no memory between calls, so everything in the context is re-sent
on every subsequent call. Content is priced once but *carried* many times.

* `promptTokens(k)` = `input + cache_read + cache_creation` for call *k*.
  **Observed** — it is exactly what that request was billed for.
* `carry(item)` = the item's tokens × the calls it remained resident for,
  **split by the class each of those re-sends was billed at**. A single number
  here would be the mistake this whole section exists to avoid.

On the reference dataset, ~711K tokens of unique tool-result content sat behind
856M raw prompt tokens. The size of a retrieval is not the interesting quantity;
when it arrived is. A 5K-token read at call 20 of a 691-call session is re-sent
671 times — but at 0.1× if it stays cached, which makes it a ~336K EIT decision
rather than a 3.4M one.

### Attributing growth to the tool calls that caused it

`delta(k) = promptTokens(k) − promptTokens(k−1)` is the tokens added between two
calls, and it can be attributed to the tool results and assistant output in
between. Validated against a 22-call session:

```
total observed growth   80,270
explained by output + tool-result content   69,500   (87%)
```

The 13% residual is system reminders, attachments and message-envelope overhead.
It is reported as `unattributed`, not distributed across items. Because these
per-item costs come from the API's own accounting rather than from a tokenizer,
they are `derived`, not estimated.

## 5. Cache lifecycle is a first-class cost, and usually the biggest one

A cache entry's TTL is measured from the **start** of the request that writes or
reads it, and a read refreshes the timer for free. So while calls start less than
the TTL apart, the prefix stays warm indefinitely. Once a gap exceeds the TTL,
the entire prefix is rewritten at the write multiplier — and in a long session
the prefix is enormous.

Tokenamun detects an **expired prefix** at call *k* when
`cache_creation / promptTokens > 0.5` and `cache_creation > 2,000`. That is a
`derived` classification with stated thresholds, not an observation, so it is
labelled and the thresholds are configurable. It validates well: in one session
47 of 48 detected cold calls were preceded by a gap of more than 5 minutes, and
cache reads on those calls collapsed to a small constant while creation rose to
95–100% of the prompt.

What this cost on the reference dataset, where **all** caching used the
5-minute TTL (zero 1-hour writes observed):

| Lever | raw tokens | EIT | share of effective input bill |
| --- | --- | --- | --- |
| Expired-prefix re-creation after a >5-minute gap | 45,572,544 | 56,965,680 | **40.0%** |
| Session preamble, carried on every call (if always cached) | 69,609,464 | 6,960,946 | 4.9% |
| All output tokens, at 5× | 1,537,275 | 7,686,375 | 5.1% of total bill |

**Forty percent of the effective input bill was re-warming a cache that expired
while someone was thinking.** It is not a content problem, not a tool problem,
and no amount of output compression touches it. Late expiries are the expensive
ones: an expiry at call 186 of one session rewrote 249,424 tokens.

The levers overlap and must not be added — the preamble is part of what gets
re-created on an expiry.

## 6. Counterfactuals

A what-if result has four sections and all four always print: `observed`,
`derived`, `counterfactual`, `unknown`. Three rules constrain them.

**Baseline first.** The observed quantity an intervention targets prints before
any counterfactual. Most published optimisation percentages are properties of
the author's baseline rather than of the technique, and a reader can only notice
that if they can see ours.

**Net the cache invalidation, or the sign can be wrong.** Any intervention that
rewrites context breaks the cached prefix from that point, converting 0.1× reads
into 1.25× writes. So the result is (tokens saved after the change point) minus
(reads repriced at the change point), and an intervention can cost more than it
saves. Where the invalidation point can't be determined it goes in `unknown`
rather than being netted out silently.

**No reduction without the outcome caveat.** We cannot see whether the task still
succeeded, and an agent that fails consumes the fewest tokens of all. The
literature's best-measured study in this area uses *tokens-to-success* and never
reports a token number without its success rate. Tokenamun can supply the
numerator and cannot supply the denominator, and says so every time.

### Worked example: 1-hour TTL

The highest-value intervention on the reference dataset, computed the way every
what-if is:

```
Intervention: cache-ttl (5-minute -> 1-hour)

Observed
  effective input-equivalents                 142,320,409 EIT
  cache writes, 5-minute TTL                   49,284,083 tokens
  cache writes, 1-hour TTL                              0 tokens
  cold-prefix calls after a >5-minute gap              99
      of which gap was 5-60 minutes                    89
      of which gap exceeded 60 minutes                 10

Derived
  re-creation avoidable under a 1-hour TTL      41,455,406 tokens
  re-creation still required (>60min gaps)       4,117,138 tokens

Counterfactual
  saved by avoided re-creation                 -47,673,717 EIT
  cost of remaining re-creation at 2.0x          +3,087,854 EIT
  cost of ordinary writes repriced 1.25x->2.0x   +2,783,654 EIT
  net effective input-equivalents              100,518,199 EIT
  net change                                   -41,802,209 EIT  (-29.4%)

Unknown
  behavioural_change        the agent's trajectory is assumed identical
  ttl_control               TTL is set by the harness, not by the user
  idle_gap_distribution     future sessions may pause differently
  task_success              not observable from this data
```

Note the shape: the doubled write price is charged honestly against the saving,
and the largest `unknown` is that this is not even the user's setting to change.
The finding is still worth having — it tells you what to ask your harness for,
and it tells you that compressing tool output is the wrong place to start here.

## 7. What Tokenamun cannot measure

**The transcript is not the request.** `full.jsonl` records what the agent did,
not what was sent. No system prompt, no tool schemas, no skill definitions, no
instruction-file expansion. Total prompt size per call is observed and is our
anchor, but the first ~30K tokens of it cannot be decomposed. Any tool that
breaks that down from a transcript is guessing.

**So tool-schema interventions are bounded, not measured.** Deferred tool
loading, tool search, and MCP-to-CLI all live inside that undecomposable
preamble. Tokenamun bounds them and counts tools used versus available; it will
not emit a schema-token figure. The way to *measure* one is a controlled A/B —
same repo, same opening prompt, MCP server connected and not — where the
difference in observed first-call prompt size **is** the schema cost. That is
observed, and `tokenamun compare` is the command for it.

**Activity is inferred.** Planning and debugging are not recorded. The
classifier prefers `other` to a confident guess.

**Subagent spend may be absent.** Sidechain transcripts were missing from every
session in the reference dataset despite the `Agent` tool being called. Missing
is reported as missing, never folded into the parent.

**Retrieved tokens and billed tokens are different quantities** and are never
added together.

**Prices are configuration.** The multipliers in §3 are current published
Claude rates. They change, they vary by model and platform, and Tokenamun's
defaults are a starting point you should check against your own bill.

## 8. Not a productivity metric

Tokenamun has no developer dimension, in any command, deliberately. Token spend
is an input, not an outcome; treating it as a productivity measure is a
documented anti-pattern with real casualties. Findings are framed against the
engineering system — subsystems, file properties, retrieval patterns, cache
behaviour — never against people. Fewer tokens for worse work is not an
improvement, and this tool cannot see work quality, so it must not imply that it
can.
