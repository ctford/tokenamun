# Methodology

How Tokenamun computes what it reports, and where it stops.

Figures below are measured, and each says which dataset it came from. Sessions
differ enough that a single headline number would be misleading: across eight
sessions in three repositories, raw token volume overstated cost by between
2.6× and 8.3×.

## 1. Provenance

Every quantity carries one of six labels. It is a type in the data model, not
a documentation convention, and the renderer will not print an unlabelled
number.

| Label | Meaning |
| --- | --- |
| `observed` | Present in the telemetry. No interpretation. |
| `derived` | Deterministic arithmetic over observed values, and over pinned published rates. |
| `derived-approx` | Deterministic, but with a stated estimator: token counts from the byte-ratio estimator, and cyclomatic complexity from branch keywords. |
| `inferred` | Could be wrong. Used for a file path parsed out of a shell command line, as against one a tool reported. |
| `counterfactual` | Arithmetic about a session that never happened. |
| `given` | Supplied by the caller. Only `optimise --optimise`: the one reported figure this tool did not produce. |

Counterfactuals render in their own section, never inside an observed total,
and always with an `unknown` section. One that produces an empty `unknown`
fails a test.

`sessions`, `length`, `profile`, `retrieval`, `carry`, `cache`, `scan`,
`hotspots`, `series` and `optimise` label every figure they print. `tree`, `report` and
`compare` do not: each reports one quantity throughout, stated in its header,
and a label on every cell would be noise.

## 2. One API call, not one transcript line

The most important correctness rule here.

In a Claude Code transcript an `assistant` entry is a **content block**, not an
API call, and every entry sharing a `requestId` repeats the same `usage` object
verbatim, so summing per entry overstates everything — by up to 89% on the
reference dataset, which
[`ENTIRE.md`](ENTIRE.md#the-double-counting-trap) breaks down per class.

Tokenamun deduplicates by `requestId`, falling back to `message.id`. It also
ignores the placeholder entries Claude Code writes for failed requests, whose
usage is all zeros.

**Entire's per-checkpoint `token_usage` is never used for accounting.** It is
a delta in some checkpoints and cumulative from session start in others, with
the same `cli_version` and no field distinguishing them
([`ENTIRE.md`](ENTIRE.md#token_usage-on-a-checkpoint-is-not-safe-to-sum) has the
counts and what summing them cost). Checkpoints are used only to find
transcripts.

## 3. Volume is not cost

Raw token counts say how much text moved, not what it cost.

"Cache" here always means Anthropic prompt caching, and every cache figure is
read from the `usage` object the API returns — `cache_read_input_tokens`,
`cache_creation_input_tokens`, and the `ephemeral_5m_input_tokens` /
`ephemeral_1h_input_tokens` split.

Prices are per-class multiples of a model's own input price:

| Class | Multiplier |
| --- | --- |
| Fresh input | 1.0× |
| Cache read | 0.1×, and 0.025× on Claude Fable 5.1 and Mythos 5.1 — the 5.1 generation, not the whole Fable family; 0.12× on Claude 3 Haiku |
| Cache write, 5-minute TTL | 1.25×, and 1.2× on Claude 3 Haiku |
| Cache write, 1-hour TTL | 2.0× |
| Output | 5.0× |

So the unit is the **cost-weighted token**: one full-price input token of the
same model.

**It is model-relative, and that is a limit rather than a feature.** A total
spanning two differently priced models adds quantities of different sizes.
Reports say when that applies, and `--prices` is how you correct it. Within
one model the unit is exact and needs no price list, which is why it is the
default and stays the default.

### Money, on request

`profile`, `cache` and `tree` take `--prices`, which adds a total in dollars.
Only those three: money is for the total that spans models, and the commands
that report one quantity in EIT throughout refuse the flag rather than ignore
it.

Dollars are computed **per call**, each at its own model's published input
price, and then added. That is the only conversion that is sound across
models, and it is why the flag exists: EIT cannot add two models, dollars can.
A model the catalog does not know is an error, not a zero — a total that drops
the calls it could not price is a bill missing a model, and reads as a bill.

The figures are labelled `derived`, not `derived-approx`: the tokens are
observed and the arithmetic is exact, so what can be wrong is the published
rate, which is staleness rather than estimation. The mitigation is the pin —
every surface that prints dollars prints the catalog and commit beside them,
and a test asserts that none of them can print one without the other.

Cost *within* a report stays in EIT, because a per-node dollar figure would
have to price a whole tree at one model, which is the error the money total
exists to avoid.

These multipliers are checked, not asserted. `scripts/refresh-prices.sh`
vendors the Claude rows of LiteLLM's published catalog into
`internal/cost/litellm-prices.json`, pinned to the upstream commit they came
from, and a table test divides each published rate by that model's input price
and compares the result with the constant. CI re-fetches and fails when a
published rate moves. Nothing fetches a price while a report is rendered: a
figure that depends on the day you ran it is not a measurement of the session.
Claude 3 Haiku is in the table above because that check found it — its
published rates are not round multiples of its input price, and the constants
had assumed they were.

On one session: cache reads were 94% of volume and 57% of cost; cache *writes*
were 6% of volume and 43% of cost. A tool that calls a 40K-token read
expensive without knowing whether it was billed at 0.1× or 1.25× is not
measuring cost.

### Across the subagent boundary

A subagent runs in its own context, so its spend is reported *beside* the
session's totals and never folded into them: adding its cache reads to the
parent's would describe a prompt that was never sent. `profile` prints both,
and their sum. Where those transcripts have been cleaned up, the child's spend
is reported missing rather than estimated.

That sum is the one figure that spans contexts, and so the one that can span
models without the session having switched — dispatching cheap subagents from
an expensive parent is a deliberate pattern. It therefore carries its own
mixed-pricing signal, computed over the parent's calls *and* its subagents',
and it is exact whenever they share a model, which is the common case.

`tokenamun sessions` **ranks on the combined figure**: a session that
dispatched its work to subagents caused that spend whichever context it landed
in. Every other figure, there and elsewhere, is this context alone.

`--prices` reaches both, each priced per call at its own model and so sound
across the boundary that EIT is not.

## 4. Carry: content is cheap, keeping it is not

The model has no memory between calls, so everything still in the context is
re-sent on every later call, and priced at whatever class that re-send was
billed at. A single number for an item's cost would hide exactly that.

Each send is also priced at the model of the call it went out on, because a
residency span can cross a model switch and the cache read is the one
multiplier that differs between models. The span is walked rather than
counted: the first send in it writes the content to the cache, and the rest
read it, or write it again on a call that rebuilt the prefix.

Size is not the interesting quantity; arrival time is. A 5K-token read at call
20 of a 691-call session is re-sent 671 times — at 0.1× while the prefix stays
warm, which makes it a ~336K decision rather than a 3.4M one.

Growth between calls, `promptTokens(k) − promptTokens(k−1)`, is attributed to
the tool results and assistant output in between. On a 22-call session that
explained 87% of observed growth; the rest is system reminders, attachments and
message envelope, reported as `unattributed` rather than distributed across
items.

## 5. Cache misses are attributed to a cause

A TTL is measured from the start of the request that writes or reads the entry,
and a read refreshes it for free. So while calls start less than the TTL apart
the prefix stays warm indefinitely; once a gap exceeds it, the whole prefix is
rewritten at the write multiplier — and in a long session the prefix is large.

TTL expiry is one of about nine things that invalidate a Claude Code cache, so
counting every large write as an expiry would inflate it and point at the wrong
fix. A **large miss** is `cache_creation / promptTokens > 0.5` with
`cache_creation > 2,000`. The observable causes are tested first, because those
misses would have happened under any TTL:

| Cause | Evidence | Provenance |
| --- | --- | --- |
| `model_switch` | `message.model` differs from the previous call | observed |
| `claude_code_upgrade` | the transcript's `version` differs | observed |
| `compaction_or_reset` | prompt size dropped >40% | observed |
| `effort_change` | the `effort` field differs | observed |
| `session_start` | the first call | observed |
| `ttl_expiry` | none of the above, and the start-to-start gap exceeds the lifetime that prefix was written under | derived, by elimination |
| `unexplained` | none of the above | — |

**The lifetime is observed, not assumed.** A cache entry's TTL is fixed when
it is written, and the API reports which one as `ephemeral_5m_input_tokens` or
`ephemeral_1h_input_tokens`, so the threshold a gap is measured against is read
off the write that established the prefix — the most recent write, not the most
recent call, since a read refreshes an entry for its own lifetime rather than a
new one. A prefix written at both takes the shorter: it is matched from the
front, so an early segment expiring takes everything after it. Where no write
precedes a call, which is a session resumed onto a prefix another one wrote,
the session's own writes are the fallback, and an ambiguous session takes five
minutes — the shorter lifetime is the one that still lets an expiry be detected.

MCP, plugin and tool-set changes are not observable from a transcript, so they
land in `unexplained` rather than being guessed at.

The bucket can at least be named. A cached prefix is a byte-exact match over
tools, then system, then messages, so anything that edits an earlier byte
invalidates everything after it. Published causes that leave no trace in a
transcript: adding, removing or reordering a tool, which invalidates the whole
prefix rather than part of it; setting or changing a structured output format,
which invalidates the conversation; editing the system prompt; and per-request
data placed ahead of the stable prefix — a timestamp or a queue position —
which turns every single request into a full cache write. That last one is the
expensive one and the easiest to do by accident: a 25-token status line in the
wrong position has been measured taking one run from $0.59 to $4.24.

Naming them is not detecting them. They stay in `unexplained`, and the reason
to list them is that a large `unexplained` line is a prompt to go and look at
the harness rather than a shrug.

Expiry usually dominates, but as a result rather than an assumption: on one
team's week it was 24.4% of prompt cost over 640 calls. On another dataset it
could be model switching instead.

### The TTL counterfactual

The one modelled change in the tool, because every input to it is observed:
which TTL each call used, which misses were expiry, the gap lengths, and the
published multipliers. Claude Code exposes `promptCacheTtl` and
`subagentPromptCacheTtl` (also as `CLAUDE_CODE_PROMPT_CACHE_TTL` and
`CLAUDE_CODE_SUBAGENT_PROMPT_CACHE_TTL`), each taking `5m` or `1h`, since
v2.1.242. The main conversation defaults to 1 hour only on a subscription
within plan usage; on an API key or a cloud provider it is 5 minutes.

Four things the arithmetic must do, and the first three are each a way to get
the answer wrong by about 1.75×:

1. An avoided rewrite is not free. The prefix is still sent, as a cache read —
   0.1× on most models, 0.025× on the 5.1 generation.
2. Every write you still make reprices from 1.25× to 2.0×.
3. Gaps longer than an hour expire under either lifetime, so they are excluded
   from the avoidable set and charged at the higher rate.
4. Each pricing is priced on its own. A session switches model whenever
   `opusplan` toggles plan mode, and summing everybody's tokens to price the
   total once gets an answer that depends on which model came first.

`tokenamun cache` does all four, and prints the two halves rather than only
the net: what the avoided rewrites stop costing, and what the surviving writes
cost extra. Done by hand it is easy to miss the first two, and missing the
first is what puts break-even at 37.5% instead of its real 39.5% — 0.75/2.0
against 0.75/1.9. The result can be positive: on a session of short bursts
that never idles past five minutes you would pay the 2× write premium for a
lifetime you never use.

## 6. Counterfactuals

**Baseline first.** The observed quantity a change targets prints before any
counterfactual. Most published optimisation percentages are properties of the
author's baseline rather than of the technique, and a reader can only notice
that if they can see yours.

**Net the cache invalidation, or the sign can be wrong.** Rewriting context
breaks the cached prefix from that point, converting 0.1× reads into 1.25×
writes. Where the invalidation point cannot be determined it goes in `unknown`
rather than being netted out silently.

**No reduction without the outcome caveat.** An agent that fails a task
consumes the fewest tokens of all. Tokenamun can supply a token count and
cannot supply a success rate, and says so every time.

## 7. What it cannot measure

**The transcript is not the request.** `full.jsonl` records what the agent did,
not what was sent: no system prompt, no tool schemas, no skill definitions, no
instruction-file expansion. Total prompt size per call is observed and is the
anchor, but the first tens of thousands of tokens cannot be decomposed.
Anything that breaks that down from a transcript is guessing.

**So tool-schema questions are bounded, not measured.** Deferred tool loading,
tool search and MCP-to-CLI all act inside that undecomposable preamble. The way
to measure one is a controlled A/B — same repo, same opening prompt, server
connected and not — where the difference in first-call prompt size *is* the
schema cost. `tokenamun compare` is the command for that.

**Thinking that gets re-read.** Claude Code records thinking blocks with empty
text. They are billed, but whether they go round again is unknowable, which is
a large part of `unattributed`.

**Activity.** Whether a stretch of work was planning or debugging is not
recorded, and no classifier is built. The better question is cost per change
rather than cost per session, and Entire records `files_touched` for it;
nothing here reads that yet.

**Prices are configuration.** The multipliers in §3 are published Claude rates.
They change and vary by model and platform. Check them against your own bill.

## 8. Aggregate at the level the question is about

A relationship measured over day totals is not the same relationship measured
over sessions, and it can point the other way. A day is a mixture of session
lengths; the long sessions dominate its totals; and cost per call rises with
session length and then flattens, because a call re-sends whatever is still
resident and eventually there is nothing more to add. Fitted across day
aggregates that curve looks like a line, and a per-call saving read off the
line has already been published and then retracted against the session-level
figures.

`tokenamun length` is the view that shows it: sessions binned by how many
calls they made, with each band's cost per call beside its session count.
Fixed geometric bands, no curve through them, and the two extreme sessions in
each band printed so a band of three cannot be read as a property of that
length. Where the rise stops is read off the table, not asserted.

## 9. Not a productivity metric

No command has a developer dimension, deliberately. Token spend is an input,
not an outcome, and treating it as a productivity measure is a documented
anti-pattern with real casualties — the evidence is in
[`COMMON-INTERVENTIONS.md`](COMMON-INTERVENTIONS.md#the-anti-pattern-to-avoid-building).
Findings are framed against the engineering
system — subsystems, file properties, retrieval patterns, cache behaviour —
never against people. Fewer tokens for worse work is not an improvement, and
this tool cannot see work quality, so it must not imply that it can.
