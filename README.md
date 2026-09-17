# Tokenamun

**A profiler for coding-agent token usage.**

> **Experimental.** Tokenamun is an experiment, not a product. The data model, the
> CLI surface, the JSON schema and the analyses will change without notice or
> migration. Nothing here is stable, supported, or suitable for anything you
> depend on. It exists to find out whether this kind of measurement is useful at
> all.

Tokenamun uses agent session data to show where tokens go, what work they
support, and whether proposed optimisations might actually help.

Ask questions like:

* Where did my tokens go?
* How much did I spend exploring code?
* How much content came from ADRs?
* What did verification cost?
* What did I retrieve repeatedly?
* Would compressing tool output have helped?
* Would moving an MCP server behind a CLI have helped?

Tokenamun provides evidence. You decide what to optimise.

## It reads Entire's data. That is a hard dependency.

Tokenamun is **not** a session recorder. It has no hooks, no proxy, no wrapper,
no instrumentation of its own. It reads data that
Entire has already written to disk, and
if Entire wasn't recording, Tokenamun has nothing to say.

Concretely, v0.1 requires a repository where Entire is installed and active, and
reads two things from it:

| Source | What it gives us |
| --- | --- |
| `.entire/metadata/<session-id>/full.jsonl` | The native Claude Code transcript — per-API-call token usage, tool calls, tool results |
| `refs/entire/checkpoints/**` (git refs) | Checkpoint metadata: commit attribution, files touched, transcript offsets, skill events |

This has consequences worth being explicit about:

* **No Entire, no profile.** There is no fallback path that reads Claude Code's
  own `~/.claude/projects/` transcripts. Adding one is plausible later; it isn't
  here.
* **Retrospective only.** Tokenamun profiles sessions that already happened. It
  cannot profile a session in flight and it cannot change one.
* **Entire's format is not a stable API.** The layout above was read off Entire
  CLI `0.10.2` by inspecting real data. It is versioned behind an adapter
  (`internal/entire`) and documented in
  [`docs/research-entire.md`](docs/research-entire.md), including the fields we
  found to be unreliable. Expect breakage when Entire moves.
* **Claude Code only, for now.** Entire supports other agents; the internal model
  is designed for them, but no other adapter is implemented or tested.

## What Tokenamun cannot measure

This matters more than the feature list. Tokenamun labels every number as
**observed**, **derived**, **inferred** or **counterfactual**, and refuses to
present the last three as measurements. The limits that produce those labels:

**The transcript is not the request.** `full.jsonl` records what the agent did,
not what was sent to the API. It contains no system prompt, no tool schemas, no
skill definitions, no `CLAUDE.md`/`AGENTS.md` expansion. We can see the *total*
prompt size of every API call — that is observed, and it is the ground truth we
anchor to — but we cannot decompose the first ~30K tokens of it into
"system prompt vs tool schemas vs your instruction files". Any tool that claims
to break that down from a transcript is guessing.

**This means MCP and tool-schema analyses are bounded, not measured.** The
opportunity from deferred tool loading, tool search, or putting an MCP server
behind a CLI all live in the schema overhead we cannot see. Tokenamun can bound
it (the observed session preamble is a ceiling) and count which tools were
actually used versus available, but it will not hand you a schema-token figure
it cannot observe.

**Volume is not cost, and reporting volume as cost would be the easiest way to
make this tool misleading.** Anthropic prompt caching means most of what moves
through the context is billed at a tenth of list price, while the cache *writes*
that are 6% of the volume are 43% of the cost. On the reference dataset, raw
prompt volume overstates cost by 6.0×. Tokenamun therefore ranks everything in
cache-weighted effective input-equivalents and never calls a retrieval expensive
on volume alone — see [`METHODOLOGY.md`](METHODOLOGY.md#3-volume-is-not-cost).

**Retrieved tokens are not billed tokens, and the gap is enormous.** On our
reference dataset, ~711K tokens of unique tool-result content sat behind ~856M
tokens of billed input. Content is cheap to retrieve and expensive to *carry*:
every token that enters the context is re-sent on every subsequent API call. A
treemap of retrieved content is a map of what was fetched, not of what was paid
for. Tokenamun reports both, separately, and never adds them together.

**There is no semantic classification of content.** An earlier version
declared categories -- ADRs, specifications, plans -- per repository. It was
removed: a directory layout already carries that, so retrieved content nests
by directory instead, which needs no configuration and cannot be wrong about
your project. See [`docs/plan.md`](docs/plan.md) for what that trades away.

**Activity attribution is inferred.** "Planning" and "debugging" are not
recorded anywhere. They are a classifier's opinion over tool-call patterns, and
the classifier prefers `other` to a confident guess.

**Counterfactuals are counterfactuals.** If Tokenamun says compressing tool
output would have saved 400K tokens, that is arithmetic on observed content
under a stated compression model. It is not a claim about what the session would
have cost, because the agent would have behaved differently — possibly better,
possibly by re-reading everything it just lost. Every what-if result carries an
explicit `unknown` section, and it is not there for decoration.

**Subagent work may be invisible.** Sidechain transcripts were absent from every
session in our reference dataset even though the `Agent` tool was called. Where
subagent token usage is not in the transcript, Tokenamun reports it as missing
rather than folding it into the parent.

**Checkpoint token totals cannot be summed.** Entire's per-checkpoint
`token_usage` is cumulative-from-session-start in some checkpoints and a delta in
others, with no field distinguishing them. Tokenamun derives all token
accounting from the transcript and uses checkpoints only for slicing and git
attribution. See [`docs/research-entire.md`](docs/research-entire.md#the-double-counting-trap).

## Not a leaderboard

Tokenamun is a sensor, not a judge. It is built to support findings like
"changes in this subsystem require 2.3× more exploration than comparable ones",
not "this developer uses 2.3× more tokens than that one". Treating token spend
as a productivity metric is a known anti-pattern and this tool is not an
instrument for it.

## How the numbers are computed

[**`METHODOLOGY.md`**](METHODOLOGY.md) is the document to read before making a
decision from this tool's output. It defines the provenance labels, the
cache-weighted cost model, how context carry is attributed, how cache expiry is
detected and what it cost on real sessions, and the limits of every
counterfactual.

## Status

Experimental and partly built. Working today, against both sources:

| command | what it answers |
| --- | --- |
| `tokenamun sessions` | what transcripts it can see |
| `tokenamun profile` | where the tokens went, and what they cost |
| `tokenamun retrieval` | what content entered the context, and from where |
| `tokenamun carry` | what it cost to *keep* content, not to fetch it |
| `tokenamun cache` | why the prompt cache was rebuilt, and what that cost |
| `tokenamun scan` | code properties: size, complexity, duplication |
| `tokenamun hotspots` | those properties joined against what the session cost |
| `tokenamun compare` | two sessions side by side |
| `tokenamun tree` | where the tokens went, one level at a time; `--at` drills in |
| `tokenamun interventions` | what `what-if` can be asked, built-in and installed |
| `tokenamun what-if` | would an optimisation have helped, and by how much |
| `tokenamun what-if --all` | every intervention's bottom line, ranked |
| `tokenamun treemap` | standalone HTML viewer, drilling down from channel to file |
| `tokenamun series` | experiment probe runs: median, range, payback |

### The CLI shows what the picture shows

The HTML viewer needs a browser and a mouse. `tokenamun tree` is the same
hierarchy reachable by name — the same two percentages, the same two cost
modes, the same per-node explanations the viewer puts in its tooltips — and
every level prints the command that goes one deeper. `tokenamun what-if --all`
is the interventions table, and `tokenamun treemap --json` prints the viewer's
own payload, byte for byte what the HTML is handed.

That is deliberate, and a test enforces it. Anything the picture can show and
the CLI cannot is a question this tool can only answer to a human, and an agent
driving it would have to ask someone to read the screen.

```
tokenamun tree --json                          # where did it go?
tokenamun tree --at "cli output/version control"   # and inside that?
tokenamun what-if --all                        # what would have helped?
```

Every milestone in [`docs/plan.md`](docs/plan.md) is implemented. Activity
classification is deliberately excluded: it is inferred, and the observed
answers are the ones worth trusting. See
[`docs/experiments.md`](docs/experiments.md) for using `series` in a
before/after experiment.

```
go build ./cmd/tokenamun && ./tokenamun profile current
```

The research and plan are:

* [`METHODOLOGY.md`](METHODOLOGY.md) — how every number is computed and labelled
* [`SPEC.md`](SPEC.md) — what we're building and why
* [`docs/research-entire.md`](docs/research-entire.md) — what Entire's data actually contains, measured
* [`docs/plan.md`](docs/plan.md) — architecture and v0.1 implementation plan
* [`docs/interventions.md`](docs/interventions.md) — which proposed optimisations Tokenamun can produce evidence for
* [`docs/optimisation-claims.md`](docs/optimisation-claims.md) — survey of what the tools and techniques claim, and how good the evidence is
* [`docs/experiments.md`](docs/experiments.md) — using it for before/after experiments, and what it can't do for you
