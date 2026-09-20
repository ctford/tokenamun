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
tokenamun doctor     # can it read anything here?
tokenamun report     # an HTML report of usage
tokenamun profile    # what the session cost, and how it was billed
tokenamun tree       # where the tokens went, one level at a time
```

Every command takes `[--dir directory]` for where to look, defaulting to the
current directory: Claude Code's transcripts recorded for it, and Entire's
recordings in the repository containing it. `--source local` or
`--source entire` picks one when both are there.

With no session named they take the one you are in, or the latest for that
directory. Name one with `current`, `latest`, or an id prefix. Where the
question composes across sessions, `all` sums every session found — with
Entire, that is the whole team — and `tokenamun help` says which commands
take it. `--since 7d` narrows it to the last week.

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

## Methodology

Costs are in cost-weighted tokens: every class on one scale where 1 is a
full-price input token, a cache read is 0.1 and output is 5.0. That holds
within one model, so for a total spanning two, `profile`, `cache` and `tree`
take `--prices` and add it up in dollars instead.

How each figure is computed and labelled is in
[`docs/METHODOLOGY.md`](docs/METHODOLOGY.md).

## Commands

`tokenamun help` lists them, each by the question it answers, and `tokenamun
help <command>` gives one command's flags and examples. That is the only copy:
an agent driving this tool has the help text and not this file, and a second
list here would be the one that goes stale.

## Also here

- [`docs/METHODOLOGY.md`](docs/METHODOLOGY.md) — how every number is computed and labelled
- [`docs/ENTIRE.md`](docs/ENTIRE.md) — what Entire's data contains, measured
- [`docs/RUNNING-EXPERIMENTS.md`](docs/RUNNING-EXPERIMENTS.md) — using `series` for before/after
- [`docs/COMMON-INTERVENTIONS.md`](docs/COMMON-INTERVENTIONS.md) — what people try to cut tokens, and what can be checked
