# 𓂀 Tokenamun

A token profiler for coding agents. It answers where the tokens went, and what
they actually cost — for the session you are in, or for a whole team's history.

Costs are in cost-weighted tokens: every class on one scale where 1 is a
full-price input token, a cache read is 0.1 and output is 5.0. That ranking is
the useful one. Raw counts overstated the bill by between 2.6× and 8.3× on the
sessions measured so far, because almost everything in a context is a cache
read, and the expensive content is whatever arrived early and was then re-sent
on every later call.

> **Experimental, and vibed rather than rigorous.**

## For Claude and Entire

It reads two formats and prices them with Anthropic's published caching rates.
Nothing here transfers to another provider.

- **Claude Code's transcripts**, under `~/.claude/projects/`. Already on disk,
  so there is nothing to set up: work in a repository, then profile it.
- **[Entire](https://entire.io)'s recordings**, when a repository uses it —
  either `.entire/metadata/` on your machine, or the transcripts inside
  Entire's checkpoint commits. The second is how you profile a team, since a
  clone carries everybody's checkpoints.

## Installing

```sh
go install github.com/ctford/tokenamun/cmd/tokenamun@latest
```

That lands in `$(go env GOPATH)/bin`, which is often not on your `PATH`.

Or with Homebrew:

```sh
brew install --HEAD ctford/tap/tokenamun
```

`--HEAD` is required and there are no tagged versions: the
[formula](https://github.com/ctford/homebrew-tap) builds from `main`, because
the CLI and the JSON still change and a version number would say otherwise.
`brew upgrade --fetch-HEAD tokenamun` picks up new commits. Either route needs
Go, since the formula builds from source too.

Either way it is worth having on the `PATH` rather than built per-repository:
the thing you usually want to profile is whichever repository you are standing
in, and `--dir` points it at any other one.

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
answers with measurements. Every command takes `--json`, every figure carries
its provenance — `[observed]`, `[derived]`, `[counterfactual]` — and each level
of the drill-down prints the command that goes one deeper, so an agent can
navigate without guessing at names.

> *"Where did my tokens go this week?"*
> → `tokenamun tree all --since 7d`
>
> *"What is inside that cli output box?"*
> → `tokenamun tree all --at "cli output"`, then `--at "cli output/git"`
>
> *"What would halving the shell output be worth?"*
> → `tokenamun optimise --at "cli output" --optimise 0.5 --why "..."`

A session is named by id prefix, or by `current` or `latest`. `tree`, `report`,
`profile`, `cache` and `optimise` also take `all`, which sums every session
discovered — with Entire, that is the whole team. `--since` and `--until` take a date or an
age, so last week is `--since 7d`.

Interventions are not built in. Everything that shrinks content does the same
two things — pick a part of the session and make it smaller — so `optimise`
measures the part and you supply the change and a `--why`.
[`docs/interventions.md`](docs/interventions.md) is the catalogue in prose.
Caching is the exception, and stays modelled because every input to it is
observed: `tokenamun cache` prices a TTL change with no assumed parameter in it.

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

Token spend is an input, not an outcome, so no command has a developer
dimension. Filtering by whose sessions you look at is fine; ranking people is
not.

## Also here

- [`METHODOLOGY.md`](METHODOLOGY.md) — how every number is computed and labelled
- [`AGENTS.md`](AGENTS.md) — conventions and the quality gates
- [`docs/research-entire.md`](docs/research-entire.md) — what Entire's data contains, measured
- [`docs/experiments.md`](docs/experiments.md) — using `series` for before/after
