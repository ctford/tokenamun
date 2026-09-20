# 𓂀 Tokenamun

A token profiler for Claude. Gives you the evidence to decide which
optimisations matter.

> **Experimental.**

## For Claude and Entire

It reads data from two sources:

- **Claude Code's transcripts**, under `~/.claude/projects/`.
- **[Entire](https://entire.io) recordings** of teams using Claude Code.

## Installing

```sh
go install github.com/ctford/tokenamun/cmd/tokenamun@latest
```

That lands in `$(go env GOPATH)/bin`, which you may have to add to your
`PATH`.

Or with Homebrew:

```sh
brew install --HEAD ctford/tap/tokenamun
```

To work on it, `go build ./cmd/tokenamun` and run `./scripts/checks.sh` — the
same script the pre-commit hook and CI run.

## Using it directly

```sh
tokenamun doctor            # can it read anything here?
tokenamun report current    # a standalone HTML viewer of the same tree
tokenamun profile current   # the session you are in
tokenamun tree current      # where the tokens went, one level at a time
```

Every command takes `[--dir directory]` for where to look, defaulting to the
current directory: Claude Code's transcripts recorded for it, and Entire's
recordings in the repository containing it. `--source local` or
`--source entire` picks one when both are there.

Name a session by `current` — the one you are in — or by `latest`, or by id
prefix. Most commands also take `all`, which sums every session found; with
Entire, that is the whole team. `--since 7d` narrows it to the last week.

## The better way — driving it with Claude Code

The intent is that you ask about your own usage in conversation and your agent
answers with measurements.

> *"Where did my tokens go this week?"*
> → `tokenamun tree all --since 7d`
>
> *"What is inside that cli output box?"*
> → `tokenamun tree all --at "cli output"`, then `--at "cli output/git"`
>
> *"What would halving the shell output be worth?"*
> → `tokenamun optimise --at "cli output" --optimise 0.5 --why "..."`

## What it cannot measure

Some things are not visible in the transcript:

- **Tool schemas**, so data around MCP and tool loading is bundled up in the
  preamble.
- **Thinking that gets re-read.** Claude Code records thinking boxes with
  empty text. This is a large part of the unattributed usage.
- **Exact token counts for content**, which are estimated from bytes at a
  ratio calibrated against the session's own prompt growth.
- **Whether the token spend was worth it.** Tokenamun doesn't judge the value
  of your tokens, just helps you to know where they went.

Details in [`docs/METHODOLOGY.md`](docs/METHODOLOGY.md).

## Commands

| command | what it answers |
| --- | --- |
| `doctor` | whether either source is set up to record here |
| `sessions` | what transcripts it can see, and what each cost |
| `length` | what a call cost, binned by how long the session ran |
| `profile` | where the tokens went, and what they cost |
| `tree` | the same, one level at a time; `--at` drills in |
| `report` | a standalone HTML viewer of the same tree |
| `carry` | the individual retrievals that cost the most to *keep*, worst first |
| `cache` | why the prompt cache was rebuilt, and what that cost |
| `retrieval` | what content entered the context, and from where |
| `optimise` | what a hypothetical change to part of the tree is worth |
| `scan` | code properties: size, complexity, duplication |
| `hotspots` | those properties joined against session cost |
| `compare` | two sessions side by side |
| `series` | experiment probe runs: median, range, payback |

## Also here

- [`docs/METHODOLOGY.md`](docs/METHODOLOGY.md) — how every number is computed and labelled
- [`docs/ENTIRE.md`](docs/ENTIRE.md) — what Entire's data contains, measured
- [`docs/RUNNING-EXPERIMENTS.md`](docs/RUNNING-EXPERIMENTS.md) — using `series` for before/after
- [`docs/COMMON-INTERVENTIONS.md`](docs/COMMON-INTERVENTIONS.md) — what people try to cut tokens, and what can be checked
