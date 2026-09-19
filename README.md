# 𓂀 Tokenamun

A token profiler for coding agents. It tells you and your coding agent where
the tokens went, and what they actually cost — for the session you are in, or
for a whole team's history.

Costs are in cost-weighted tokens: every class on one scale where 1 is a
full-price input token, a cache read is 0.1 and output is 5.0.

> **Experimental, and vibed rather than rigorous.**

## For Claude and Entire

It reads two formats and prices them with Anthropic's published caching rates.
Not portable to other providers.

- **Claude Code's transcripts**, under `~/.claude/projects/`.
- **[Entire](https://entire.io)'s recordings**, when a repository uses it.

## Installing

```sh
go install github.com/ctford/tokenamun/cmd/tokenamun@latest
```

That lands in `$(go env GOPATH)/bin`, which is often not on your `PATH`.

Or with Homebrew:

```sh
brew install --HEAD ctford/tap/tokenamun
```

To work on it, `go build ./cmd/tokenamun` and run `./scripts/checks.sh` — the
same script the pre-commit hook and CI run.

## Using it

```sh
tokenamun doctor            # can it read anything here?
tokenamun profile current   # the session you are in
tokenamun tree current      # where the tokens went, one level at a time
```

## Driven by an agent

The intent is that you ask about your own usage in conversation and your agent
answers with measurements. Every command takes `--json`, and each level of the
drill-down prints the command that goes one deeper, so an agent can navigate
without guessing at names.

Figures that are not plain measurements say so. Every command that prints a
table of figures labels each one — `[observed]`, `[derived]`, or
`[derived-approx]` where a stated estimator is involved — and the two that
answer a what-if label that `[counterfactual]`: `cache` for a TTL change,
`optimise` for a part of the tree. In `optimise` the figure you supply is
`[given]`, because it is the one number here this tool did not produce.

> *"Where did my tokens go this week?"*
> → `tokenamun tree all --since 7d`
>
> *"What is inside that cli output box?"*
> → `tokenamun tree all --at "cli output"`, then `--at "cli output/git"`
>
> *"What would halving the shell output be worth?"*
> → `tokenamun optimise --at "cli output" --optimise 0.5 --why "..."`

A session is named by id prefix, or by `current` or `latest`. `tree`,
`report`, `profile`, `cache` and `optimise` also take `all`, which sums every
session discovered — with Entire, that is the whole team. `--since` and
`--until` take a date or an age, so last week is `--since 7d`.

## What it cannot measure

Some things are not visible in the transcript:

- **Tool schemas**, so every MCP and tool-loading question is bounded by the
  preamble rather than measured.
- **Thinking that gets re-read.** Claude Code records thinking blocks with
  empty text, so it is billed but its residency is unknowable. A large part of
  the `unattributed` box.
- **Exact token counts for content**, which are estimated from bytes at a
  ratio calibrated against the session's own prompt growth. `tiktoken` is not
  Claude's tokenizer and is not used.
- **Subagent-internal spend**, absent from every session examined despite
  `Agent` being called.
- **Whether the work came out right.** An agent that fails a task consumes the
  fewest tokens of all, so a reduction is not an improvement on its own.

Details in [`METHODOLOGY.md`](METHODOLOGY.md).

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

## Also here

- [`METHODOLOGY.md`](METHODOLOGY.md) — how every number is computed and labelled
- [`docs/research-entire.md`](docs/research-entire.md) — what Entire's data contains, measured
- [`docs/experiments.md`](docs/experiments.md) — using `series` for before/after
- [`docs/interventions.md`](docs/interventions.md) — what people try to cut tokens, and what can be checked
