# Feedback from a week of real use

Status: items 1-5 built, 2026-09-19. Item 6 belongs to
[`litellm-pricing.md`](litellm-pricing.md); the `all`-selector work at the end
of this file belongs to [`outlier-detection.md`](outlier-detection.md).

> **Decided and done:** wrapper leaves are split by measurement, not by an
> allow-list — see item 3, which records what "by measurement" turned out to
> need.

Ten suggestions from an agent that profiled a 271-session week with this tool
and hit its edges. Checked against the code before ranking. Two are already
done, one asks for the wrong fix to a real problem, and the ordering wants
changing — but the substance mostly holds, and the two items the author says
matter most are the two that matter most.

The author's own summary of why: the tool "sent me to the raw transcripts,
which is precisely what it exists to prevent." That is the right test to rank
by, and it is used below.

## Already done

**"Fix the `all` selector, or document the exception" (their 5).** The claim
is that `tokenamun help` "documents `all` as a general session selector with
no note of the exception". It does not. `cmd/tokenamun/main.go:51`:

```
  "all"      every session discovered, summed. [...] Taken by
             tree, report, profile, cache and optimise; the others report
             on one session.
```

`loadSelected` also refuses it with a message naming four commands that do
take a set, and `TestAllIsOfferedOnlyWhereItWorks` pins both halves. This was
fixed for exactly the reason the author gives, and the fix did not reach them
— see "What this actually asks for" below, because the underlying need is
real.

**"Validate `--why` before scanning" (their 9).** The 64-character limit is
in the usage text at `main.go:87` ("Required, max 64 characters"). The
validation order is worth checking — dying after a scan rather than before it
is a genuine waste on a 271-session set — but the claim that "the limit isn't
documented anywhere the user looks" is wrong.

Both being already-done is itself a finding: the author read the tool's
behaviour, not its help. Whatever `--help` says, it was not reaching someone
working hard inside the tool for a week. That is worth more thought than
either individual item.

## What to build, reordered

### 1. Cost in `sessions`, and an index under it (their 1 and 8, together) — **built**

The worst failure in the list, and the author underrates it by splitting it
in two. `cmdSessions` emits `SessionRef` — id, transcript, origin, modified —
and nothing else. To rank a week by cost they reimplemented the EIT formula
against raw transcripts.

That reimplementation is near-certain to be wrong. Deduplicating assistant
entries by `requestId` is this tool's "central correctness rule", and the
penalty for missing it is 66–97% overstatement on the fixtures here. Writing
this repo's own subagent support today, I made exactly that mistake against
exactly these files and overstated one measurement by 3.3x before the tool's
own output caught me. An unmeasured workaround is not a slow path to the
right answer; it is a fast path to a wrong one.

So: add `calls` and `cost_eit` to `sessions`, plus `--sort cost`.

The part the author separates out is the reason this is not trivial. Cost
means parsing every transcript — the same ~25s that `tree all` already
spends, on every invocation, over data that has not changed. An exploratory
tool that costs 25s per question does not get explored. A parse cache keyed
on transcript path plus mtime (and, for Entire, the checkpoint ref, which is
immutable and better than an mtime) makes the second question instant.

Do the cache first, then the columns. The columns without the cache are a
25-second `sessions`, which is a worse command than the one we have.

**Built, in that order.** `internal/parsecache` keys a parsed session on what
the parse actually reads: the transcript's size and modification time, plus
every subagent transcript beside it, since a subagent can finish writing after
its parent's last line — `ingest.Sources` owns that list so the loader and the
cache cannot come to disagree. An Entire recording is keyed on its checkpoint
ref instead, which is a git object spec and so names bytes that cannot change.
The session the tool is running inside is never cached at all. Entries live
under the identity of the executable that wrote them, so rebuilding the tool
empties the cache rather than trusting that whoever changed `ingest`
remembered to bump a format number; `--no-cache` forces the parse. Every
uncertainty is a miss, and the invalidation has more tests than the hit path.

`sessions` then grew `calls` and `cost_eit`, `--sort cost|calls|recent`, and
an error rather than a silent fallback on an unknown sort. The figure comes
from `BuildProfile`, so `sessions` and `profile` cannot disagree about one
session; subagent spend is excluded, as it is from `profile`'s headline, and
the notes say so. A transcript that will not parse keeps its row, loses its
figures, and sorts below every session that has a cost.

### 2. Relate cost to session length (their 3) — **built**

Promoted from third, because it is the only item where the tool's absence
produced a *published wrong number*: a 26% saving fitted from a day-aggregate
regression, retracted after discovering that cost-per-call saturates around
200 calls.

That is an aggregation artefact — a relationship at the day level that
reverses at the session level — and it is the exact failure this tool's whole
provenance vocabulary exists to prevent. `optimise` demands `--why` on a
one-line hypothetical, and meanwhile the tool offers no view of the variable
the hypothetical most often turns on. A built-in cost-per-call binned by
session length would have shown the plateau immediately.

`series` and `compare` are the neighbours, and neither answers it. Worth
designing as a real view rather than a flag on an existing one.

**Built as `tokenamun length`**, a view of its own rather than a flag. Bands
of session length by call count, each with its cost per call beside its
session count, and the cheapest and dearest single session's own per-call rate
so a band of three cannot be read as a property of that length. Bands are
fixed and geometric: fixed because a boundary chosen to suit the numbers is
the first step of fitting a shape to them and would move between two runs of
the same command, geometric because session length spans three orders of
magnitude. Nothing is fitted and no line is drawn — where the rise stops
rising is read off the table. It takes no selector, because one session has no
distribution in it, and is scoped by `--since`/`--until` like everything else.
`docs/METHODOLOGY.md` §7a carries the aggregation argument.

### 3. Open the wrapper leaves (their 2, with 7 folded in) — **built**

`CommandGroup` keys on the first token of a command line, so `mise run x`,
`pnpm exec y` and `npx z` each collapse into one leaf. The author measures
24.1% of the week sitting behind leaves the tool cannot open, most of it
this.

The mechanism already exists: `byCommandArguments` splits a tool's cost by
command. It needs to split one token deeper for wrappers.

Their fix is an allow-list — `mise run`, `pnpm exec`, `npm run`, `npx`,
`yarn`, `make`, `just`, `cargo`, `go`. It would work, and it is small. It is
also a hardcoded name list of the kind this repo generally refuses, and it
will be wrong for whatever wrapper someone adopts next.

**Decided: split on measurement rather than on names.**
Descend a token when a leaf's second tokens are high-cardinality and do not
look like paths — `LooksLikeCommand` is already the predicate for "this token
is a command, not a filename". A wrapper is then something the data
identifies rather than something we list. If it proves fiddly in practice, fall back to the allow-list and say in a
comment why the list is a list -- but the measurement is what to try first.

**Built by measurement; the allow-list was not needed.** High cardinality on
its own is not the signal, and `content.LooksLikeCommand` on its own does not
save it: grep's second tokens are high-cardinality patterns, and "group",
"air" and "and" all look like commands — which is the 59-one-retrieval-children
failure `hasSubcommands` already exists to avoid. The property that separates
a runner from an argument is **repetition**: a runner's targets are a small
fixed set run over and over, where an argument is close to unique per call.
So `content.ObserveWrappers` opens a leaf when, across the calls behind it,
there are at least four calls, at least two distinct next tokens, every next
token looks like a command, and each distinct token is used at least twice on
average. It is a property of the whole set of command lines, so it is observed
once per tree and consulted per retrieval.

Two things that fell out of it. The target's leaf is named after the deepest
level, or `mise run check` holds one leaf called `mise run` — the same row
twice with the less specific label on the inner one. And the per-leaf
distribution follows the split, which it has to: a p95 still reported against
the old grouping is a number that looks right and is not.

Their item 7 belongs here, not separately. A leaf saying "nothing inside:
this is a leaf" reads identically for a genuine atom and for 3,795 lumped
retrievals. The count is known. Say "aggregate of 3,795 retrievals — not
separable from the transcript". Cheap, and it turns a silent limit into a
stated one, which is what this tool is supposed to do everywhere else.

### 4. One denominator (their 6) — **built**

`cache` reports `share_of_prompt_cost` — a saving as a fraction of prompt
cost. `optimise` reports `AddressableShare` (`node.Carry / total`) and then
what the session *becomes*, a remainder. Two bases, opposite directions, and
the author put them in one column and misled their reader until queried.

Printing both bases on both commands is the suggested fix and is right.
Small, and squarely the kind of labelling problem this codebase already
spends its comments on.

**Built.** `cache` gained a `% TOTAL` column beside `% PROMPT`, a session-cost
line beside its prompt-cost line, and a `_of_session_cost` twin for every
share in its JSON. `optimise` gained a share of prompt cost beside its share
of the session total, and both lines now name their denominator where the
figure is rather than only in a key. Prompt cost reaches the tree on the root
node, because that is the one figure there that is measured per session and
sums rather than rolling up from leaves.

### 5. Several nodes in one `optimise` (their 4) — **built**

Agreed and correctly diagnosed: `optimiseArgs` holds a single `at` and a
single `becomes`, while the composition rule — `1 − addressable × (1 −
becomes)` — is already in the binary. Accepting repeated `--at`/`--optimise`
pairs is mostly wiring.

Lower than the author's rank only because the current tool gives a correct
answer awkwardly, where items 1–3 give no answer or a wrong one.

**Built.** `--at`, `--optimise` and `--why` repeat and are read as columns of
one table; a mismatched count is an error, because both plausible guesses —
reuse the last reason, apply one figure everywhere — produce a report that
looks deliberate and says something the caller did not. The composition needed
one thing the plan did not mention: overlapping parts are refused. Optimising
`cli output` together with `cli output/git` reads as two changes and is one
counted twice, and the tool is the only party that can see the containment.
`parts` is in the JSON even for a single node, so a consumer has one shape to
read rather than two.

### 6. Money (their 10) — **built elsewhere**

Already planned in detail; see [`litellm-pricing.md`](litellm-pricing.md).
The author's framing — "let these numbers go to people who don't know what an
input-equivalent token is" — is a better argument for it than the one in that
plan, which is about cross-model summation being unsound. Both are true and
the second is the one that makes EIT *wrong* rather than merely unfamiliar.

## What this actually asks for

The `all` complaint is misdiagnosed but not wrong. Under it:

> That cost me the whole carry analysis, which is the one view aimed squarely
> at residency, the thing that turned out to matter most here.

They did not want the help text changed. They wanted `carry all`.

The refusal's stated reason (`TestAllIsOfferedOnlyWhereItWorks`) is that
these commands "report per-retrieval or per-file detail whose sequence
numbers and joins mean nothing once two sessions are in one list". True of
`CarriedItem` rows. **Not true of carry's totals** — `PreambleCarryEIT`,
`PromptCarryEIT` and the uncached counterfactuals are sums over invocations,
and sums compose.

So `carry all` is refusable in its detail and answerable in its totals. That
is the same shape as `profile all`, which is built from finished per-session
profiles precisely because residency does not compose across sessions.

Worth doing, and worth generalising: a blanket refusal at the selector is too
blunt when half the report is additive. The same question applies to
`retrieval all`.

## Also found while building it

Two things that were not in the list.

**`git -C` does not decide which repository git works on.** GIT_DIR and its
relatives take precedence over it, and git sets them in the environment of
everything it invokes — a hook, a `git rebase --exec`, a filter. Run from any
of those, every reader in `internal/entire` answered about *that* repository
while labelling the answer with the directory it was pointed at. No error, no
empty result, just somebody else's checkpoints reported as this directory's.
Fixed by scrubbing those variables; the package's own tests were failing under
the pre-commit hook for exactly this reason and had been read as flakiness.

**Two test files and `cmd/tokenamun/main.go` hit the length budget** and were
split along the seams the budget exposed: the counterfactual's tests, the
session list's tests, and the flag-handling half of `main.go`.

## Ranking, if only some of it lands

The author says: if only two, make it 1 and 2 (cost in `sessions`, wrapper
splitting). I would make it **cost in `sessions` with the parse cache under
it, and the session-length view** — because those are the two where the tool
did not merely inconvenience someone, but let a wrong number out of the
building. Wrapper splitting is the best of the rest and is cheap.

In the event all five landed, in that order.
