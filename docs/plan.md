# Tokenamun: architecture and v0.1 plan

Read [`research-entire.md`](research-entire.md) first — every design decision
below is a consequence of something measured there.

## Language: Go

Single static binary, `go install`-able, nothing to bootstrap before an agent
can invoke it. Matches the Go + `mise` + `golangci-lint` conventions already in use on the
adjacent projects this will be pointed at. `anthropic-sdk-go` covers the opt-in
`count_tokens` path. The workload is JSONL streaming and arithmetic, so nothing
about it argues for a faster or more dynamic language.

## The three rules

Everything else is detail.

**1. Every number carries a provenance label.** `observed`, `derived`,
`inferred`, `counterfactual`, or absent. This is a type in the domain model, not
a comment. JSON output carries it per field. The renderer refuses to print an
unlabelled number, so a number can't sneak into the report without one.

**2. Token accounting comes from the transcript, deduplicated by `requestId`.**
Never from checkpoint `token_usage` — it is cumulative in 13 of our 41
checkpoints and delta in the other 28, with nothing to distinguish them.

**3. Retrieved-content tokens and billed tokens are different quantities and are
never added together.** 711K of content sat behind 856M of billed input in the
reference dataset. Any view that mixes them is wrong.

## Architecture

```
cmd/tokenamun/            CLI entry point (cobra)
internal/
  entire/                 ADAPTER — the only package that knows Entire's layout
    discover.go             find .entire/metadata, enumerate sessions
    checkpoints.go          read refs/entire/checkpoints/** via go-git
    transcript.go           stream full.jsonl
    assumptions.go          every format assumption, named and documented
  claudecode/             ADAPTER — the only package that knows Claude Code's JSONL
    decode.go               entry types, content blocks, usage
    toolresult.go           per-tool toolUseResult shapes
  model/                  normalized domain — Session, ModelInvocation, Activity,
                          ToolCall, RetrievedContent, Artifact, GitChange,
                          TokenUsage, Provenance
  ingest/                 adapters -> model. Dedup, ordering, integrity warnings.
  content/                classification (source/test/ADR/spec/docs/plan/tool
                          output/MCP/instructions/other), rules-driven
  tokens/                 counting: calibrated estimator | count_tokens API
  cost/                   per-model class weights, effective input-equivalents
  analysis/
    profile.go              session roll-up
    retrieval.go            per-item, per-category, duplicates
    carry.go                context deltas, attribution, cost-of-carry
    cache.go                miss detection, cause attribution, expiry cost
    activity.go             inferred phase classification
    compare.go              A vs B
  codescan/               code metrics: size, complexity, duplication
  whatif/                 counterfactual strategies behind one interface
  report/                 text renderer, JSON renderer, HTML treemap
```

`internal/entire` and `internal/claudecode` are the only packages allowed to
know a field name from someone else's format. Everything downstream sees
`model` types. `assumptions.go` is a literal list of what we assume and how we
detect that it stopped being true — Entire's format is not an API and we should
find out loudly, not silently.

### Ingest pipeline

1. **Discover** — walk up for `.entire/metadata/`; enumerate session dirs; open
   the repo's git refs for checkpoints.
2. **Stream** the transcript line by line. 9 MB files exist; nothing loads whole.
3. **Dedup** `assistant` entries by `requestId` (falling back to `message.id`)
   into one `ModelInvocation` per API call. Emit a warning with counts —
   "1,132 entries → 691 invocations" is diagnostic, not noise.
4. **Pair** `tool_use` with its `tool_result` / `toolUseResult`. Unpaired calls
   (interrupted, denied) are kept and flagged.
5. **Extract** `RetrievedContent` with source, category, byte size, token count,
   hash, tool, path, line range, truncation flag, sequence position.
6. **Integrity pass** — zero-size prompt entries (API errors), `<synthetic>`
   models, missing checkpoint offsets, absent sidechains for `Agent` calls,
   truncated Bash output. All surfaced in a `warnings` block, none silently
   dropped.

### Retrieved content extraction

Per tool, using the measured `toolUseResult` shapes:

* **Read** — `file.content`, with `startLine`/`numLines`/`totalLines`, so a
  partial read is counted as a partial read.
* **Bash** — `stdout` + `stderr`. Truncation detected at the 30,000-char cap and
  via `persistedOutputPath`/`persistedOutputSize` (size known, content absent →
  observed size, unknown content).
* **Edit / Write** — `structuredPatch`, not the whole file.
* **Agent** — the returned report only; internal tokens unknown when the
  sidechain is absent.
* **Everything else** — the `tool_result` block text.

Bash is 87% of tool calls in the reference dataset, so classification cannot
lean on tool names. The Bash handler parses command lines for file paths
(`cat`, `sed -n`, `head`, `tail`, `rg`, `grep`, `jq`, `git show`) and attributes
the output to those paths, tagging the attribution `inferred`. Commands whose
output can't be attributed become category `tool output` rather than a guess.

### Content classification: removed

There was a category axis -- ADRs, specifications, plans, tests, source --
declared per repository in `.tokenamun.json` and otherwise guessed from
directory naming. It is gone, and the reasoning is worth keeping because it
applies to the next taxonomy someone proposes.

In practice a repository's directory layout already carries the category:
`docs/decisions` *is* the decision records. Once retrieved content was nested
by directory, the tree answered the same question with no configuration and
no guessing about someone else's project. Measured on the reference dataset,
8 of 9 categories were being filled by naming heuristics rather than by
declarations, and only one category's content spanned more than one directory
-- so the thing a category could do that a directory cannot was a rounding
error next to the cost of being wrong about a layout.

What was lost, stated plainly: aggregating content scattered by convention
(Go tests live beside the code they test, so a directory view shows them in
nine places), and the ability to declare that a path is not what it looks
like (`.claude/projects/**/tool-results/*.txt` reads as instructions from its
path but is spilled tool output). If either becomes painful, the answer is a
narrow override file for misleading paths -- not a second taxonomy.

Paths are still attributed, and their provenance still distinguishes a path
the tool reported (derived) from one parsed out of a shell command line
(inferred).

### Token counting

Two backends behind one interface:

* **`calibrated` (default, offline).** Fit bytes-per-token per session by
  least-squares against observed prompt-size deltas (§"context growth
  attribution" in the research notes — 87% of growth is explained by observed
  output plus observed content, so the residual is a real objective function).
  Reports the fitted ratio and its residual. Labelled `derived-approx`.
* **`api` (opt-in, `--tokenizer=api`).** `count_tokens` on `anthropic-sdk-go`,
  model-specific and exact. Cached by content hash on disk, so a re-profile is
  free. Labelled `derived`.

`tiktoken` is not an option: it's OpenAI's tokenizer and undercounts Claude by
15–20% on prose and worse on code.

### Cost of carry

The analysis that makes the tool worth having, and it is derived from observed
numbers rather than modelled.

* `promptTokens(k)` = `input + cache_read + cache_creation` for call *k* —
  **observed**.
* `preamble` = `promptTokens(0)` minus the first user prompt — the floor paid on
  every call. `preamble × calls` is the carried cost of instructions, tool
  schemas and skills *in aggregate*. We cannot decompose it and will not pretend
  to.
* `delta(k)` = `promptTokens(k) − promptTokens(k−1)`, attributed to the tool
  results and assistant output between them. Residual reported as
  `unattributed`.
* `carry(item)` = `tokens(item) × (calls remaining after it entered)`, adjusted
  for observed context resets (the drop to 0 at call 507 of `S1` is an
  API error; a genuine compaction shows as a large partial drop and truncates
  every open residency span).

This changes what the profile says. "You read a 5K-token file" is not the
finding. "You read a 5K-token file at call 20 of 691, so it was re-sent 671
times for ~3.4M tokens of billed input" is the finding.

**Carry is reported per token class, never as one number.** A cache read costs
roughly a tenth of a fresh input token, so 3.4M carried tokens that stayed
cached and 3.4M that kept being re-created are very different findings. Carry
therefore splits into `cache_read`, `cache_creation` and `input` components,
taken from the observed per-call classes rather than assumed. A price table is
configuration, so dollars are opt-in — but the *classes* are observed and always
shown.

This also gives `profile` a caching-health line that is entirely observed:
`cache_read` versus `cache_creation` over the session, and every call where
creation spiked (a cache miss re-paying for a prefix). Caching applies to the
93–99% of spend that is input, so "is your caching actually working" is probably
the cheapest genuine finding this tool can produce, and it needs no
counterfactual at all.

### Activity classification, deferred

**Not in v0.1.** The core question is what consumed tokens and context, and
that is answerable from observed data. Activity type -- was this planning or
debugging -- is inferred, is the weakest thing the tool would report, and
answers a question nobody has asked yet. Building it early would also invite
exactly the misreading the epistemics exist to prevent: a confident-looking
"planning: 34%" sitting next to genuinely observed token figures.

So it waits until the observed and derived analyses are done and have proved
useful. The design below stands for when it is wanted; `Classifier` is an
interface so the first implementation can be crude without being permanent.

Evidence available: tool name and arguments; `permissionMode` and `mode` entries
(plan mode is *observed*, which is a gift); `EnterPlanMode`/`ExitPlanMode`
calls; Entire's `skill_events` with `confidence: "explicit"`; test and build
commands in Bash; edit/write density; `Agent` calls; `turn_duration`.

Taxonomy: orientation, planning, research, implementation, debugging,
verification, review, other. A window that doesn't clear a confidence threshold
is `other` — the spec's instruction to prefer `other` over false precision is
the acceptance criterion, and there is a test asserting we don't over-assign.

### Code scans

A second axis of evidence: token spend is only actionable when you can see what
it was spent *on*. `internal/codescan` walks the working tree and produces
per-file metrics, which `tokenamun hotspots` joins against token spend via
checkpoint `files_touched` and retrieved-content paths.

v0.1 scanners, all built in (no external tools required, no network):

* **size** — bytes, total lines, code lines per file. Flags files over a
  configurable threshold. `observed`.
* **duplication** — normalised-line rolling hash (lowercase, strip comments and
  whitespace) over a sliding window, reporting duplicate blocks ≥ N lines within
  and across files. A small CPD, not a clone-detection research project.
  `derived`.
* **complexity** — per-function branch counting (`if`/`for`/`while`/`case`/
  `catch`/`&&`/`||`/`?`) via language keyword tables. This is an
  **approximation of cyclomatic complexity**, not a control-flow-graph
  computation, and it is labelled `derived-approx`. Go gets the real thing from
  `go/ast` since the parser is in the standard library; other languages get the
  approximation. Where an external tool is on `PATH` and the user opts in
  (`--complexity=external`), shell out and label the result `observed`.

The join is the point. Findings we want to be able to produce:

* "The 12 files touched by these checkpoints average 4× the duplication of the
  repo baseline, and changes to them cost 2.3× the exploration tokens."
* "Every session that touched `internal/example/` spent >40% of retrieval on
  re-reading the same three oversized files."

Those are system findings, which is the whole design intent. What we must not
produce is a per-developer ranking, and the absence of a developer dimension in
`hotspots` output is deliberate.

Correlation is not causation and the CLI says so: a `hotspots` result reports
both metrics and the join, and never asserts that complexity *caused* the spend.

### What-if strategies

```go
type Intervention interface {
    Name() string
    Applicable(*model.Session) Eligibility  // observed: what it could touch
    Estimate(*model.Session) Result          // counterfactual + unknowns
}
```

`Result` has four sections and the renderer prints all four, always:
`observed`, `derived`, `counterfactual`, `unknown`. An intervention that
returns an empty `unknown` fails a test — there is always something we can't
know.

Three rules on top, each of which exists because published optimisation claims
get it wrong (see [`optimisation-claims.md`](optimisation-claims.md)):

* **Baseline first.** The observed quantity the intervention targets is printed
  before any counterfactual. Most headline percentages in circulation are
  properties of the author's baseline rather than of the technique, and a reader
  can only spot that if they can see ours.
* **Cache-aware, or the sign can be wrong.** Any intervention that rewrites
  context invalidates the cached prefix from that point, converting cheap cache
  reads into full-price input. `Result` therefore carries a
  `cache_invalidation` term: tokens saved after the change point, minus reads
  repriced at the change point. Where the invalidation point can't be
  determined, it goes in `unknown` rather than being netted out silently. A 30%
  token reduction that increases the bill is a real outcome and we should be
  able to report it.
* **No reduction without the outcome caveat.** We cannot see whether the task
  still succeeded, and an agent that fails consumes the fewest tokens of all.
  Every counterfactual states that success rate is not in this data. The metric
  worth borrowing from the literature is tokens-to-success; we can supply the
  numerator and must be explicit that we cannot supply the denominator.

v0.1 ships:

* **`output-compression`** — a labelled prototype estimator over eligible tool
  output. Ratios are configurable and printed with the result, so the reader can
  see the assumption they're trusting.
* **`cache-ttl`** — the one with the best evidence on real data, and a real
  setting behind it (`promptCacheTtl` / `CLAUDE_CODE_PROMPT_CACHE_TTL`, Claude
  Code v2.1.242+). Takes the observed per-cause miss attribution, isolates the
  misses a longer TTL would have prevented, charges the doubled write premium
  against the saving, and charges gaps longer than an hour at the higher rate
  too. Can legitimately come out negative on short-burst sessions, and must be
  allowed to.
* **`repeated-retrieval`** — the strongest one, because the counterfactual is
  nearly derived: identical content retrieved N times could have been retrieved
  once. Reduction = observed duplicate bytes × carry.
* **`caveman`** — interface plus prototype estimator. Caveman is not installed
  here and its compression is not reproducible locally, so v0.1 estimates and
  says so. The design point is a `--replay-with=<cmd>` hook: pipe observed
  eligible content through any real compressor and compare counts. That makes
  the analysis honest *and* extensible to Headroom, RTK or anything else on the
  radar This is a priority, not a nicety: Caveman's published output
  saving is 65% and an independent test measured 8.5%, and Headroom's headline
  60–95% is its JSON case against a stated 20% for coding agents. Piping a given
  repo's own observed content through the real compressor is the only way to
  settle which number applies to that repo.

`mcp-to-cli` gets an interface and a stub that reports `not measurable from this
data`. There were zero `mcp__*` calls in the reference dataset and the schema
cost we'd need is not in the transcript. Shipping a number here would be
manufacturing precision.

### Treemap

Standalone self-contained HTML, one file, no build step, no CDN. Squarified
treemap over `RetrievedContent`, hierarchy `category → path → retrieval`, area =
**observed retrieved-content tokens**. A toggle switches area to
**derived cost-of-carry**, which is the more decision-relevant view. The title
says which mode is active and the footer states plainly that this is not a
context-window visualisation.

## CLI surface

```
tokenamun profile   [session]              # the overview
tokenamun retrieval [session]              # per-item, per-category, duplicates
tokenamun activities [session]             # (deferred) inferred phases
tokenamun carry     [session]              # preamble + cost-of-carry ranking
tokenamun cache     [session]              # cache misses, causes, what they cost
tokenamun hotspots  [session]              # code metrics x token spend
tokenamun scan      [path]                 # code metrics alone
tokenamun compare   <a> <b>                # sessions or checkpoints
tokenamun series    <file>...              # probe runs: median, range, payback
tokenamun what-if   <intervention> [session]
tokenamun sessions                         # list what's available
tokenamun report   [session] -o out.html
```

Global: `--json`, `--repo`, `--tokenizer=calibrated|api`, `--config`,
`--warnings=show|hide`. `[session]` accepts a session UUID prefix, a checkpoint
ULID prefix, or `latest`.

JSON is a first-class output, not a dump: stable top-level keys, provenance per
field, `warnings` array, and a `schema_version`. It is the interface a coding
agent will actually use, so it gets a golden-file test per command.

## Testing

Two layers, both required to be green before anything is called done.

**Unit tests** — table-driven, per package, no I/O:

* `ingest` dedup: a fixture with repeated `requestId`s asserting 1,132→691
  semantics and that naive summing would have been 71% high. This is the
  regression test that protects rule 2.
* `content` classification: every rule in the table, plus precedence
  (`docs/adr/0001.md` is an ADR, not documentation; `internal/x_test.go` is a
  test, not source).
* `tokens` calibration: synthetic deltas with a known ratio, asserting recovery
  and residual reporting.
* `analysis/carry`: hand-computed carry over a 5-call session; context-reset
  handling; the zero-prompt API-error entry excluded.
* `codescan`: known-complexity Go functions against `go/ast`; keyword
  approximation against hand-counted fixtures in two other languages; a planted
  duplicate block found at the right offsets.
* `whatif`: every intervention returns a non-empty `unknown`; eligibility never
  exceeds observed volume.
* provenance: a labelled-number type that the renderer cannot print without a
  label, asserted by test.

**Integration tests** — over checked-in test data, no Entire, no network, no
git-daemon:

* `testdata/sessions/` holds transcript fixtures as plain files. Tests run the
  full pipeline from bytes to rendered output and compare against golden files
  (`-update` regenerates). Every command gets a golden text render and a golden
  JSON render.
* Fixtures come from two sources. **Synthetic**, hand-built to exercise specific
  shapes: repeated `requestId`s, a partial Read, a truncated Bash result, a
  `persistedOutputPath` result, an API-error zero-prompt call, an `Agent` call
  with no sidechain, a checkpoint with no `checkpoint_transcript_start`.
  **Anonymised**, generated from the reference dataset by a `tools/anonymise`
  helper that replaces content with deterministic filler of identical byte
  length and rewrites paths, preserving every size, count, hash-collision
  structure and usage number. That keeps real-shaped data in the tests without
  committing anyone's source code or prompts.
* Checkpoint reading is tested against a fixture git repo constructed in
  `TestMain` with `go-git` — real refs, real trees, no network, no `.entire`
  install.
* An opt-in smoke test runs the pipeline against a real Entire repo given by
  `TOKENAMUN_SMOKE_REPO`, and skips when that variable is unset. No private path
  is hard-coded and CI never depends on it.

The assertions that matter are the accounting invariants, and they are property
tests rather than fixed numbers: deduplicated totals never exceed naive totals;
retrieved tokens are never summed into billed tokens; carry never exceeds
`promptTokens` summed over the session; every counterfactual reduction is ≤ its
observed eligible volume.

## Milestones

Each one ends with tests green and something runnable.

**M1 — read it correctly.** `entire` + `claudecode` adapters, `model`, `ingest`
with dedup, `tokenamun sessions`, `tokenamun profile` with token classes only.
The deliverable is trustworthy arithmetic; everything later is built on it.

**M2 — retrieved content.** Extraction per tool, classification, hashing,
duplicate detection, calibrated counting. `tokenamun retrieval`, plus retrieval
sections in `profile`.

**M3 — carry and cache.** Cost weighting (`internal/cost`), prompt-size
trajectory, preamble, delta attribution, cost-of-carry, cache-miss cause
attribution and expiry cost. `tokenamun carry` and `tokenamun cache`, plus
`what-if cache-ttl`. This is the milestone that changes what the tool is for —
on the reference dataset it is where the 40% finding lives — so it comes before
the softer analyses.

**M4 — JSON, and the agent-facing contract.** `--json` everywhere with
provenance, `schema_version`, golden tests. At this point Claude Code can use it.

**M5 — code scans.** `internal/codescan`, `tokenamun scan`, `tokenamun hotspots`.

**M6 — counterfactuals.** `compare`, `what-if` with `cache-ttl`,
`repeated-retrieval` and `output-compression`, `caveman` prototype plus
`--replay-with`, and `series`.

**M7 — treemap.** HTML report.

**Later — activity classification.** `activities`, once the observed and
derived analyses have proved useful and there is a reason to want inference.

M1–M4 is the spec's "first useful milestone" — point it at a real session and
understand where the tokens and content went. M5 and M6 are where the evidence
becomes actionable. M7 is the demo. Everything inferred comes after all of it,
because the observed answers are the ones worth trusting.

## Experiments

[`experiments.md`](experiments.md) works through what a real published
experiment needs and what this tool can and cannot supply. The short version:
Tokenamun measures, a driver script orchestrates, and the experimenter records
the outcome of each run because no profiler can see whether the work was any
good.

One command earns its place in v0.1 from that analysis: `tokenamun series`
takes labelled probe runs and reports median and range per step rather than a
point value, plus payback against a measured intervention cost. It is a table
and a division over measurements `profile` and `scan` already produce. Medians
rather than points because a behavioural effect at n=1 is not a measurement,
and "the agent explored less" is a behavioural effect.

## Experiments, later

Not built in v0.1, but nothing here should block it: `compare` already takes two
sessions; `Intervention` already separates targeted reduction from behavioural
change; checkpoints already carry `files_touched` and commit attribution. The
four questions an experiment has to answer —
did the intervention reduce what it targeted, did behaviour change, did total
consumption change, did outcomes hold — map onto `what-if` (1), `activities` and
call counts (2), `profile` and `carry` (3), and `hotspots` plus the repo's own
test suite (4). What's missing is a runner and a store, and neither needs the
architecture to change.

## Distribution

Three install paths, in the order people will reach for them.

**Homebrew (the default for Mac).**

```
brew install <owner>/tap/tokenamun
```

That needs two things: a `homebrew-<owner>/homebrew-tap` repo to hold the
formula, and something to keep the formula in step with releases. GoReleaser
does both — `.goreleaser.yml` builds darwin/linux × amd64/arm64, publishes the
archives and checksums to a GitHub release, and writes the updated formula into
the tap repo on every tag. The formula is generated, never hand-edited.

A custom tap is the right call rather than homebrew-core: core requires notable
usage and a stable release history, which an experimental tool does not have and
should not pretend to.

What this needs in the repo:

* `.goreleaser.yml` — builds, archives, checksums, `brews:` block pointing at
  the tap, `-s -w -X main.version={{.Version}}` ldflags.
* `.github/workflows/release.yml` — on `v*` tags, `goreleaser release`, with a
  token scoped to the tap repo only.
* `tokenamun version` reporting the injected version, plus the Entire CLI
  versions the adapter has been validated against. Given Entire's format is not
  an API, "which Entire did this build understand" is a question users will
  have.
* `make release-dry-run` wrapping `goreleaser release --snapshot --clean` so a
  release can be rehearsed without tagging.

**`go install`** — `go install github.com/<owner>/tokenamun/cmd/tokenamun@latest`.
Free, works today, the fallback when the tap is unavailable.

**Direct binary** — the release archives, for CI images that have neither Go nor
Homebrew.

No `curl | sh` installer. It is a profiler for other people's repositories; it
should be boring to install and trivially auditable.

## CLI first, thin skill later, never an MCP server

The CLI is the product. A coding agent invoking `tokenamun profile --json` costs
nothing until it runs, needs no configuration, and works the same from a
terminal, a script, or CI. That has to be good on its own.

A wrapping **skill** is worth adding afterwards, and for one reason that a good
CLI cannot cover: an agent reading this output will get the epistemics wrong. It
will see `counterfactual.potential_reduction: 125218` and report "this saves
125K tokens". The CLI can label the field, but it cannot stop the agent
misreading it. That instruction — *observed and derived are measurements,
inferred is an opinion, counterfactual is arithmetic under an assumption, and a
local reduction is not a session saving* — belongs in a skill, alongside a map
from question to command ("where did the tokens go" → `profile`, "what was
expensive to keep" → `carry`).

It should be **thin**: a short description, a question→command table, the
epistemic rules, and nothing else. No wrapper logic, no parsing, no
reformatting. Progressive disclosure means the body costs nothing until the
skill is actually invoked, so the standing cost is the description line.

Not an MCP server. An MCP server would load tool schemas into the context of
every session it was connected to, whether or not anyone profiled anything —
which is precisely the overhead
[`interventions.md`](interventions.md#trimming-instructions-and-the-preamble) says we
cannot even measure. Shipping a profiler whose own footprint is invisible to it
would be a poor joke.

One consequence worth stating: Tokenamun's own contribution to the sessions it
profiles should stay small enough to be uninteresting, and we should check
rather than assume. `tokenamun profile` run against a session that itself used
Tokenamun is the test.

## Retiring the spec

`SPEC.md` is a brief, and a brief is spent once it has been delivered. The
intention is to delete it when v0.1 works. Two things have to be true first,
and they are worth stating now because the second is easy to get wrong.

**Everything load-bearing has to have a durable home.** Most of it already
does: the epistemic principle is [`METHODOLOGY.md`](../METHODOLOGY.md) §1, the
counterfactual output contract is §6, the not-a-leaderboard constraint is §8
and [`interventions.md`](interventions.md), the scope limits are AGENTS.md
§"Things not to build", the positioning and the questions are the README, and
the Caveman and MCP-to-CLI specifics are `interventions.md` and
`optimisation-claims.md`.

What is **not** yet durably housed is the content-classification table and the
activity taxonomy. Both currently live here, in the plan — and this document is
itself temporary, because a plan describing work that has been done is just a
stale description of the code. So those two tables need to move to a reference
doc or to documented code before either file is deleted, or the spec and the
plan will take them down together.

**The model has to actually exist.** `RetrievedContent`, `Activity`, `Artifact`
and `GitChange` are specified but unimplemented. Until they are in
`internal/model`, the spec is the only description of them and deleting it
loses real information.

Order of operations, when the time comes: migrate the two tables, confirm the
domain model is complete, then delete `SPEC.md` and trim this plan to whatever
is still unbuilt. The README and AGENTS.md links to `SPEC.md` need removing in
the same commit.
