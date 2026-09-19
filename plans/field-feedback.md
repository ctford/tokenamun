# Feedback from a week of real use

Status: proposed, 2026-09-19. Nothing here is built.

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

### 1. Cost in `sessions`, and an index under it (their 1 and 8, together)

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

### 2. Relate cost to session length (their 3)

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

### 3. Open the wrapper leaves (their 2, with 7 folded in)

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

**Worth considering instead: split on measurement rather than on names.**
Descend a token when a leaf's second tokens are high-cardinality and do not
look like paths — `LooksLikeCommand` is already the predicate for "this token
is a command, not a filename". A wrapper is then something the data
identifies rather than something we list. If that proves fiddly, take the
allow-list; it is better than the status quo either way.

Their item 7 belongs here, not separately. A leaf saying "nothing inside:
this is a leaf" reads identically for a genuine atom and for 3,795 lumped
retrievals. The count is known. Say "aggregate of 3,795 retrievals — not
separable from the transcript". Cheap, and it turns a silent limit into a
stated one, which is what this tool is supposed to do everywhere else.

### 4. One denominator (their 6)

`cache` reports `share_of_prompt_cost` — a saving as a fraction of prompt
cost. `optimise` reports `AddressableShare` (`node.Carry / total`) and then
what the session *becomes*, a remainder. Two bases, opposite directions, and
the author put them in one column and misled their reader until queried.

Printing both bases on both commands is the suggested fix and is right.
Small, and squarely the kind of labelling problem this codebase already
spends its comments on.

### 5. Several nodes in one `optimise` (their 4)

Agreed and correctly diagnosed: `optimiseArgs` holds a single `at` and a
single `becomes`, while the composition rule — `1 − addressable × (1 −
becomes)` — is already in the binary. Accepting repeated `--at`/`--optimise`
pairs is mostly wiring.

Lower than the author's rank only because the current tool gives a correct
answer awkwardly, where items 1–3 give no answer or a wrong one.

### 6. Money (their 10)

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

## Ranking, if only some of it lands

The author says: if only two, make it 1 and 2 (cost in `sessions`, wrapper
splitting). I would make it **cost in `sessions` with the parse cache under
it, and the session-length view** — because those are the two where the tool
did not merely inconvenience someone, but let a wrong number out of the
building. Wrapper splitting is the best of the rest and is cheap.
