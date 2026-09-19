# Finding the one bad retrieval

Status: proposed, 2026-09-19. Nothing here is built.

Second round of feedback from the same week of use. The question behind it:
can the tool detect a *pathological* event — one test run among 3,795 that
dumped 200k tokens on call 40 of a 600-call session — as opposed to
attributing cost to a category?

The distinction is right and it is the interesting one. Attribution says
`mise run check` costs X. A pathology is one invocation inside that, and
averaging it into any category makes it vanish; a third level of naming just
gives you a smaller category to average it into. That reasoning holds.

Three additions were proposed. Checked against the code: **two already
exist**, one is new and worth building, and the reason the author could not
see the two that exist is the most useful finding in the whole report.

## Already built: the event-level ranking (their 1 and 3)

> `tokenamun outliers [session]` — the N individual retrievals with the
> highest [cost], each with the command, file and the call index it arrived
> at.

This is `tokenamun carry`.

- `analysis/carry.go:265` sorts items by `CarryEIT`, descending.
- `report/carry.go:79` takes the top 15.
- The table's heading is literally **"Most expensive to carry (not the
  largest)"** — the size-versus-cost distinction the report says is missing.
- Columns: content, tokens, calls resident, cold calls, carry EIT.

Their third proposal — "rank by arrival position, not just size" — is the
same thing again:

> The pathology isn't big output, it's big output early. [...] Tokenamun
> holds both numbers and multiplies them at leaf level [...] not per event,
> so you can't see that two identically-sized retrievals differ by two orders
> of magnitude.

`CarriedItem` is per event, not per leaf. It carries `RetrievalSeq`, `Tool`,
`Path`, `Bytes`, `Tokens`, `EnteredAt`, `ResidentFor`, `WarmCalls`,
`ColdCalls`, `CarryEIT` and `CarryUncachedEIT`. `CarryEIT` *is* size times
residency, priced per call against the observed cache split. `Carry`'s doc
comment makes the arrival-position point directly:

> content that arrives near the end of a session is written once and barely
> re-read, so the write is nearly all of its cost.

So the claim that the tool cannot separate two identically-sized retrievals
by arrival position is wrong; separating them is what the view is for.

**One real gap inside this.** `EnteredAt` is in the JSON and not in the text
table. "The call index it arrived at" is exactly the column the author asked
for, and a text reader cannot see it. One column, trivial.

## The image false positive validates the tool

Worth recording, because it is evidence rather than opinion. Their hand-built
detector ranked PNG screenshots at 21.8M EIT each by sizing base64 at 3.8
bytes/token, when images are priced by dimensions at roughly a hundredth of
that. They caught it themselves and concluded it argued for building the
feature in.

It does, and the accounting they needed is already here:
`RetrievedContent.Bytes` excludes image payload, `ImageBytes` holds it
separately, `TotalBytes()` sums the two only where that is wanted, and
`image_tokens_not_estimated` warns that image token cost is "excluded from
the byte-ratio estimate rather than guessed". The tool declines to guess
precisely where the reimplementation guessed and was wrong by two orders of
magnitude.

## What to build

### 1. `carry all` — first, because it caused the rest

The author knew about `carry`. They called it "the closest existing view" in
the same breath as saying it "rejects the `all` selector, so it can't be used
across a window at all."

That is the whole causal chain. Their question was about a week, `carry`
refuses a week, so they never ran it, so they never saw its item table, so
they rebuilt a worse version of it and got images wrong by 100x. Fixing the
selector is not a convenience item — it is what would have prevented the
false positive.

Already identified independently in [`field-feedback.md`](field-feedback.md),
and the argument there stands: the refusal is justified for per-item
sequence numbers and joins, but carry's totals are sums over invocations and
compose fine. The item ranking also composes, as long as each row carries its
session id — `CarryEIT` is comparable across sessions, since it is already
priced per call at each call's own rate.

### 2. Distribution per leaf, not just the mean (their 2)

The genuinely new one, and the author is right that it is the cheapest and
highest-value of their three.

Every tree leaf reports total cost and retrieval count, so a reader can
compute a mean and nothing else. Print p50 / p95 / max EIT-per-retrieval
beside them. Then:

- max close to p95 — uniform cost, the leaf is habitually expensive, and the
  answer is a policy change.
- max far above p95 — one bad actor, and the answer is a fix.

That single column separates "grep is habitually verbose" from "one test run
went berserk" without any new command or navigation, and it makes every leaf
self-diagnosing. The data is already in hand: per-retrieval `Tokens` and
`CarryEIT` both exist, and the leaf already groups the retrievals it covers.

Note for implementation: quantiles over a handful of retrievals are noise.
Below some count — ten, say — print max alone and omit the percentiles rather
than computing a p95 from four samples.

### 3. `EnteredAt` in carry's text table

One column, as above.

## Not building: a separate `outliers` command

It would duplicate `carry`. But the naming problem behind the request is
real: `carry` names its mechanism (residency), not its use (finding the
expensive thing). Someone hunting a pathological event does not scan a
command list and think "residency".

Cheaper than a new command: say so in the help. `carry`'s one-line
description is "what it cost to *keep* content, not to fetch it", which is
accurate and gives no hint that it ranks individual retrievals worst-first.
Something closer to "the individual retrievals that cost the most to keep,
worst first" would have been found.

## The pattern across both rounds of feedback

Two rounds, and in each the author reimplemented something tokenamun already
does correctly, and got it wrong in the direction the tool is careful about:

- Round one: reimplemented EIT against raw transcripts, without
  `requestId` deduplication — an error worth 66–97% on this repo's own
  fixtures.
- Round two: reimplemented event ranking without per-modality accounting —
  an error worth ~100x on image results.

Both times the correct answer was already in the binary and was not reachable
from where they were standing. That is not a features problem. `sessions` had
no cost column so they left for the transcripts; `carry` refused `all` so
they left for the transcripts. The fix in both cases is to remove the reason
to leave, and it is worth weighing that above any new view — including the
new view proposed here.
