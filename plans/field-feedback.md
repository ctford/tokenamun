# Feedback from a week of real use

Status: **items 1–5 built**, 2026-09-19. Item 6 belongs to
[`litellm-pricing.md`](litellm-pricing.md); the `all`-selector work at the
end of this file went to [`outlier-detection.md`](outlier-detection.md).

Ten suggestions from an agent that profiled a week with this tool and hit
its edges. Two were already done, one asked for the wrong fix to a real
problem, and the ordering wanted changing — but the substance mostly held.

The author's own summary of why: the tool "sent me to the raw transcripts,
which is precisely what it exists to prevent." That is the right test to
rank by.

## Already done, and it did not reach them

**The `all` selector exception is documented** — `main.go`'s usage names
which commands take a set, `loadSelected` refuses the others by name, and
`TestAllIsOfferedOnlyWhereItWorks` pins both halves. **The `--why` limit is
documented too**, in the usage text.

Both being already-done is itself the finding: the author read the tool's
behaviour, not its help. Whatever `--help` says, it was not reaching
somebody working hard inside the tool for a week. That is worth more than
either individual item.

## 1. Cost in `sessions`, with an index under it

The worst failure in the list, and the author underrated it by splitting it
in two. `cmdSessions` emitted `SessionRef` — id, transcript, origin,
modified — and nothing else, so to rank a week by cost they reimplemented
the EIT formula against raw transcripts. That reimplementation is
near-certain to be wrong: deduplicating assistant entries by `requestId` is
this tool's central correctness rule, and missing it overstates by 66–97%
on the fixtures here. An unmeasured workaround is not a slow path to the
right answer; it is a fast path to a wrong one.

The cache had to come first. Cost means parsing every transcript on every
invocation, over data that has not changed, and an exploratory tool that
costs 25s per question does not get explored. The columns without the cache
would have been a worse command than the one we had.

**Built in that order.** `internal/parsecache` keys a parsed session on what
the parse actually reads: the transcript's size and mtime, plus every
subagent transcript beside it, since a subagent can finish writing after its
parent's last line — `ingest.Sources` owns that list so the loader and the
cache cannot disagree. An Entire recording is keyed on its checkpoint ref
instead, a git object spec, which names bytes that cannot change. The
session the tool is running inside is never cached. Entries live under the
identity of the executable that wrote them, so rebuilding empties the cache
rather than trusting that whoever changed `ingest` remembered to bump a
format number; `--no-cache` forces the parse. Every uncertainty is a miss,
and the invalidation has more tests than the hit path.

`sessions` then grew `calls`, `cost_eit`, `--sort cost|calls|recent`, and an
error rather than a silent fallback on an unknown sort. The figure comes
from `BuildProfile`, so `sessions` and `profile` cannot disagree about one
session. Subagent spend is excluded, as it is from `profile`'s headline, and
the notes say so. A transcript that will not parse keeps its row, loses its
figures, and sorts below every session that has a cost.

## 2. Relate cost to session length

Promoted from third, because it is the only item where the tool's absence
produced a *published wrong number*: a saving fitted from a day-aggregate
regression, retracted after discovering that cost-per-call saturates. That
is an aggregation artefact — a relationship at the day level that reverses
at the session level — and it is the exact failure this tool's provenance
vocabulary exists to prevent.

**Built as `tokenamun length`**, a view of its own rather than a flag on an
existing one. Bands of session length by call count, each with its cost per
call beside its session count, and the cheapest and dearest single session's
own per-call rate so a band of three cannot be read as a property of that
length. Bands are fixed and geometric: fixed because a boundary chosen to
suit the numbers is the first step of fitting a shape to them and would move
between two runs of the same command; geometric because session length spans
three orders of magnitude. Nothing is fitted and no line is drawn — where
the rise stops rising is read off the table. It takes no selector, because
one session has no distribution in it. `docs/METHODOLOGY.md` §7a carries the
aggregation argument.

Not built: a day-level block. The framing is that the day-level relationship
reverses at session level, so the session-level view is the answer; naming
the trap in the notes beat building a second table whose only job is to be
wrong.

## 3. Open the wrapper leaves

`CommandGroup` keyed on the first token of a command line, so every
`mise run x`, `pnpm exec y` and `npx z` collapsed into one leaf — the author
measured a quarter of their week sitting behind leaves the tool could not
open.

Their fix was an allow-list of runner names. It would have worked, and it is
a hardcoded name list of the kind this repo refuses, wrong for whatever
wrapper someone adopts next.

> **Decided:** split on measurement rather than on names.

**Built by measurement; the allow-list was not needed.** High cardinality
alone is not the signal, and `content.LooksLikeCommand` alone does not save
it: grep's second tokens are high-cardinality patterns, and "group", "air"
and "and" all look like commands — which is the many-one-retrieval-children
failure `hasSubcommands` already exists to avoid. The property separating a
runner from an argument is **repetition**: a runner's targets are a small
fixed set run over and over, where an argument is close to unique per call.
So `content.ObserveWrappers` opens a leaf when, across the calls behind it,
there are at least four calls, at least two distinct next tokens, every next
token looks like a command, and each distinct token is used at least twice
on average. It is a property of the whole set of command lines, so it is
observed once per tree and consulted per retrieval.

Two things fell out. The target's leaf is named after the deepest level, or
a runner holds one leaf named after itself — the same row twice with the
less specific label inside. And the per-leaf distribution has to follow the
split: a p95 still reported against the old grouping is a number that looks
right and is not. `TestTheDistributionFollowsTheSplit` pins it.

Folded in here: a leaf saying "nothing inside: this is a leaf" read
identically for a genuine atom and for thousands of lumped retrievals. The
count is known, so it now says so. That turns a silent limit into a stated
one, which is what this tool does everywhere else.

## 4. One denominator

`cache` reported a saving as a fraction of prompt cost; `optimise` reported
`node.Carry / total` and then what the session *becomes*. Two bases,
opposite directions, and the author put them in one column and misled their
reader until queried.

**Built.** `cache` gained a `% TOTAL` column beside `% PROMPT`, a
session-cost line beside its prompt-cost line, and a `_of_session_cost` twin
for every share in its JSON. `optimise` gained a share of prompt cost beside
its share of the session total, and both lines name their denominator where
the figure is rather than only in a key. Prompt cost reaches the tree on the
root node, because it is the one figure there measured per session that sums
rather than rolling up from leaves.

## 5. Several nodes in one `optimise`

Correctly diagnosed: `optimiseArgs` held a single `at` and a single
`becomes`, while the composition rule is already in the binary.

**Built.** `--at`, `--optimise` and `--why` repeat and are read as columns of
one table; a mismatched count is an error, because both plausible guesses —
reuse the last reason, apply one figure everywhere — produce a report that
looks deliberate and says something the caller did not. One thing the plan
did not mention: **overlapping parts are refused.** Optimising a node
together with a node inside it reads as two changes and is one counted
twice, and the tool is the only party that can see the containment. `parts`
is in the JSON even for a single node, so a consumer has one shape to read.

## 6. Money — built elsewhere

See [`litellm-pricing.md`](litellm-pricing.md). The author's framing — "let
these numbers go to people who don't know what an input-equivalent token
is" — is a better argument for it than that plan's, which is about
cross-model summation being unsound. Both are true, and the second is the
one that makes EIT *wrong* rather than merely unfamiliar.

## What the `all` complaint actually asked for

Misdiagnosed but not wrong. They did not want the help text changed; they
wanted `carry all`. The refusal's stated reason is that these commands
report detail whose sequence numbers and joins mean nothing once two
sessions are in one list — true of `CarriedItem` rows, **not true of
carry's totals**, which are sums over invocations and compose.

Built in [`outlier-detection.md`](outlier-detection.md), which also records
why `retrieval all` did *not* follow: a blanket refusal at the selector is
too blunt when half the report is additive, but the other half of
`retrieval` is about one context and does not survive merging.

## Also found while building it

**`git -C` does not decide which repository git works on.** `GIT_DIR` and
its relatives take precedence, and git sets them in the environment of
everything it invokes — a hook, a `git rebase --exec`, a filter. Run from
any of those, every reader in `internal/entire` answered about *that*
repository while labelling the answer with the directory it was pointed at.
No error, no empty result, just somebody else's checkpoints reported as this
directory's. Fixed by scrubbing those variables. The package's own tests had
been failing under the pre-commit hook for exactly this reason and had been
read as flakiness.

**Three files hit the length budget** and were split along the seams the
budget exposed: the counterfactual's tests, the session list's tests, and
the flag-handling half of `main.go`.
