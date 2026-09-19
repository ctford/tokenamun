# Finding the one bad retrieval

Status: **built**, 2026-09-19. All three additions are on main.

## The question behind it

Can the tool detect a *pathological* event — one test run among 3,795 that
dumped 200k tokens on call 40 of a 600-call session — as opposed to
attributing cost to a category? The distinction is right and it is the
interesting one. Attribution says a runner costs X. A pathology is one
invocation inside that, and averaging it into any category makes it vanish;
a third level of naming just gives you a smaller category to average it
into.

Three additions were proposed. Two already existed, one was new, and the
reason the author could not see the two that existed was the most useful
finding in the report.

## Already built, and unreachable

The proposed `outliers` command — the N individual retrievals with the
highest cost, each with its command, file and arrival index — was
`tokenamun carry`. `analysis/carry.go` sorts items by `CarryEIT` descending,
`report/carry.go` takes the top 15, and the table's heading is literally
"Most expensive to carry (not the largest)". `CarriedItem` is per event, not
per leaf, carrying `RetrievalSeq`, `Tool`, `Path`, `Bytes`, `Tokens`,
`EnteredAt`, `ResidentFor`, `WarmCalls`, `ColdCalls`, `CarryEIT` and
`CarryUncachedEIT`. So the claim that two identically-sized retrievals could
not be separated by arrival position was wrong — separating them is what the
view is for.

The author knew about `carry`. They called it "the closest existing view" in
the same breath as saying it "rejects the `all` selector, so it can't be
used across a window at all". That is the whole causal chain: their question
was about a week, `carry` refused a week, so they never ran it, never saw
its item table, rebuilt a worse version and got images wrong by 100x.

### The image false positive validates the tool

Worth recording as evidence rather than opinion. Their hand-built detector
ranked PNG screenshots at 21.8M EIT each by sizing base64 at 3.8
bytes/token, when images are priced by dimensions at roughly a hundredth of
that. The accounting they needed was already here:
`RetrievedContent.Bytes` excludes image payload, `ImageBytes` holds it
separately, `TotalBytes()` sums the two only where that is wanted, and
`image_tokens_not_estimated` says image token cost is "excluded from the
byte-ratio estimate rather than guessed". The tool declines to guess exactly
where the reimplementation guessed and was wrong by two orders of magnitude.

## What was built

1. **`carry all`** (`report.MergeCarries`, `carryOf` in `cmd/tokenamun`).
   Totals are summed, shares recomputed against the new totals rather than
   averaged, and the item ranking merged across every session and cut
   *afterwards* — cut per session first, a week's worst retrieval could be
   missing because its own session held fifteen worse ones. Each row carries
   its session id, which is sound because `CarryEIT` is already priced per
   call at each call's own rate. Dropped rather than merged: the final
   prompt and the reset call numbers, which index into one session.
   `TestAllIsOfferedOnlyWhereItWorks` now pins the narrower refusal.

2. **Per-leaf distribution.** Every tree node reports p50/p95/max of what
   one retrieval under it cost to carry, rolled up from the leaves and kept
   across a merge. Below ten priced retrievals the percentiles are declined
   and the maximum printed alone, as `-/-/max`, because a p95 from four
   samples is noise. Nearest-rank, so every figure printed is a retrieval
   that really happened. This is what separates "grep is habitually
   verbose" from "one test run went berserk": max close to p95 is a policy
   problem, max far above it is one bad actor.

3. **`EnteredAt` in carry's text table.** It was in the JSON and not the
   text, and nothing here is viewer-only. The golden now shows three
   identical 59-token results costing 161, 155 and 80 EIT, which is the
   arrival-position argument in one glance.

## Not built: a separate `outliers` command

It would duplicate `carry`. But the naming problem behind the request was
real: `carry` named its mechanism (residency), not its use, and someone
hunting a pathological event does not scan a command list and think
"residency". Cheaper than a new command — the help now reads "the
individual retrievals that cost the most to keep, worst first".

## Not built: `retrieval all`

[`field-feedback.md`](field-feedback.md) raised the same question for
`retrieval`, and the answer is different because the report is different.

`carry` composes because every figure in it is a cost, cost is additive
across sessions, and `CarryEIT` is priced per call so rows from two sessions
are comparable. `retrieval` reports volume, and its two headline structures
are about one context: `repeated_retrieval` means content fetched more than
once *into the same context*, which is what makes it redundant, and the
token estimator is calibrated per session from that session's own prompt
growth. Merged, the first would count a file read in two sessions as a
repeat — it is not one, the second session never had it — and the second
would average calibrations each fitted to different data.

Both are fixable, neither by summing, and the fix would be a second report
with its own meaning. `tokenamun tree all` already answers "what content
cost the most across the week" in cost rather than volume, which is the
question `retrieval all` would be reached for.

## The pattern across both rounds of feedback

In each round the author reimplemented something tokenamun already does
correctly, and got it wrong in the direction the tool is careful about:

- Round one: EIT against raw transcripts without `requestId` deduplication
  — worth 66–97% on this repo's own fixtures.
- Round two: event ranking without per-modality accounting — worth ~100x on
  image results.

Both times the correct answer was already in the binary and was not
reachable from where they stood. That is not a features problem. `sessions`
had no cost column so they left for the transcripts; `carry` refused `all`
so they left for the transcripts. Removing the reason to leave is worth
weighing above any new view — including the ones proposed here.
