# 𓂀 Tokenamun

A token profiler for coding agents. It answers **where the tokens went, and
what they actually cost** — for the Claude Code session you are in, or for a
whole team's recorded history.

> **Experimental, and vibed rather than rigorous.** Built in a few sittings to
> find out whether this kind of measurement is useful at all. Several numbers
> were wrong the first time and were only caught by an independent recount of
> the raw transcripts; assume more are. Every figure is labelled with where it
> came from — `[observed]`, `[derived]`, `[counterfactual]` — so you can tell
> which ones to lean on. The CLI, the JSON and the analyses will change without
> notice.

## It only works with Claude Code, and optionally Entire

This is not a general LLM cost tool. It reads two specific formats and prices
them with Anthropic's published prompt-caching economics. Nothing here
transfers to another provider.

- **Claude Code's own transcripts**, under `~/.claude/projects/`. No setup:
  they are already on disk. This is the cold-start path — work in a repo, then
  profile it.
- **[Entire](https://entire.io)'s recordings**, when a repository uses it:
  either `.entire/metadata/` on your machine, or the transcripts inside
  Entire's checkpoint commits. The second is how you profile a *team* — a
  clone carries everybody's checkpoints.

```sh
go build ./cmd/tokenamun            # or: brew install ctford/tap/tokenamun, once released
```

## Start here

```sh
tokenamun doctor            # can it read anything in this repo?
tokenamun profile current   # the session you are in right now
tokenamun tree current      # where the tokens went, one level at a time
```

Run `doctor` first: "no sessions found" has four different causes and it tells
you which one you have.

## It is meant to be driven by an agent

The intent is that you interrogate your own usage in conversation and your
agent answers with real numbers. Every command takes `--json`, every figure
carries its provenance, and every level of the drill-down prints the command
that goes one deeper — so an agent can navigate without guessing at names.

> *"Where did my tokens go this week?"*
> → `tokenamun tree all --since 7d`
>
> *"What's inside that cli output box?"*
> → `tokenamun tree all --at "cli output"`, then `--at "cli output/git"`
>
> *"What would halving the shell output be worth?"*
> → `tokenamun optimise --at "cli output" --optimise 0.5 --why "..."`

Selectors are `all`, `current`, `latest`, or an id prefix. `--since` and
`--until` take a date or an age, so "profile last week" is `--since 7d`.

## The one idea worth knowing

**Volume is not cost.** Raw token counts overstate the bill by 6–8× on real
sessions. The model has no memory between calls, so everything still in the
context is sent again every time — and almost all of that is cache reads,
priced at a tenth of fresh input.

So Tokenamun reports **cost-weighted tokens**: every class on one scale where 1
is a full-price input token (cache read 0.1, 5-minute write 1.25, 1-hour write
2.0, output 5.0). The reordering is the point. On one session the largest
retrieval by bytes was a 53 KB document, but the most expensive content was a
10,557-token file that sat through 620 later calls.

The unit is relative to one model's input price, so a total spanning two
differently-priced models adds different-sized things. Reports say when that
applies.

## What it cannot measure

Up front, because a profiler that hides its blind spots is worse than none.

- **Tool schemas** are not in the transcript, so every MCP and tool-loading
  question is *bounded* by the preamble rather than measured.
- **Thinking that gets re-read.** Claude Code records thinking blocks with
  empty text. It is generated and billed, but whether it goes round again is
  unknowable — a large part of the `unattributed` box.
- **Content token counts** are estimated from bytes, at a ratio calibrated
  against the session's own prompt growth. `tiktoken` is not Claude's
  tokenizer and is not used.
- **Subagent-internal spend** was absent from every session examined, despite
  `Agent` being called.
- **Whether the work came out right.** An agent that fails a task consumes the
  fewest tokens of all, so a reduction is not automatically an improvement.

Details in [`METHODOLOGY.md`](METHODOLOGY.md).

## Not a leaderboard

Token spend is an input, not an outcome. Reporting it per person is the
lines-of-code vanity metric with a new unit, and it has already produced gamed
internal leaderboards and budgets burnt a third of the way through the year.

Filtering by whose sessions you look at is fine — that is how you help
somebody. Ranking people is not, so no command has a developer dimension.

## Commands

| command | what it answers |
| --- | --- |
| `doctor` | whether either source is set up to record here |
| `sessions` | what transcripts it can see |
| `profile` | where the tokens went, and what they cost |
| `tree` | the same, one level at a time; `--at` drills in |
| `report` | a standalone HTML viewer of the same tree |
| `carry` | what it cost to *keep* content, not to fetch it |
| `cache` | why the prompt cache was rebuilt, and what that cost |
| `retrieval` | what content entered the context, and from where |
| `optimise` | what a hypothetical change to part of the tree is worth |
| `scan` | code properties: size, complexity, duplication |
| `hotspots` | those properties joined against session cost |
| `compare` | two sessions side by side |
| `series` | experiment probe runs: median, range, payback |

## No catalogue of techniques

There were once built-in estimates for named optimisations — Caveman, RTK,
MCP-to-CLI and the rest. They are gone. Everything that shrinks content does
the same two things: pick a part of the session and make it smaller, and the
answer is the product of that part's share and the change. A named
intervention added nothing but a vendor's name and a default ratio, plus the
false impression that this tool knew something about that vendor.

So `optimise` measures the part and you supply the change, with `--why`
required. [`docs/interventions.md`](docs/interventions.md) is the catalogue in
prose: what people try, which part of a session each acts on, and which can be
checked against evidence at all.

Cache behaviour is the exception, and stays modelled because every input to it
is observed: which TTL each call used, which misses were expiry, the gap
lengths, the published multipliers. `tokenamun cache` prices a TTL change with
no assumed parameter in it anywhere.

## Also here

- [`METHODOLOGY.md`](METHODOLOGY.md) — how every number is computed and labelled
- [`AGENTS.md`](AGENTS.md) — conventions and the quality gates
- [`docs/research-entire.md`](docs/research-entire.md) — what Entire's data contains, measured
- [`docs/experiments.md`](docs/experiments.md) — using `series` for before/after
