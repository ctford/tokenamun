# Price every call at its own model

Status: **built**, 2026-09-19. All four sites are on main.

## The bug

Four places priced a whole session at the weights of the first model they
saw — `cost.For(ms[0])`. `cost.PerCall` and `cost.SessionCost` exist to
prevent exactly that, and `analysis/cache.go` had already been fixed to use
them; these four had not.

An earlier description said it "bites any `opusplan` session, on every
plan-mode toggle". That was wrong, and the correction is the most useful
thing in this file. `cost.For` returns identical weights for every model
except the 5.1 generation:

```
claude-opus-5     {Input:1 CacheRead:0.1   CacheWrite5m:1.25 CacheWrite1h:2 Output:5}
claude-sonnet-5   {Input:1 CacheRead:0.1   CacheWrite5m:1.25 CacheWrite1h:2 Output:5}
claude-haiku-4-5  {Input:1 CacheRead:0.1   CacheWrite5m:1.25 CacheWrite1h:2 Output:5}
claude-fable-5-1  {Input:1 CacheRead:0.025 CacheWrite5m:1.25 CacheWrite1h:2 Output:5}
```

`Input` is 1.0 and `Output` is 5.0 everywhere, so an Opus/Sonnet toggle
changes no figure at all. **The only field that could be wrong was
`CacheRead`**, and only in a session mixing the 5.1 generation with anything
else. Narrow — and the worst available field to be wrong about, since cache
reads are ~97% of prompt volume and the error is fourfold.

What each site actually risked, judged by what its weights were used for
rather than by the fact that it had them:

| site | used | exposed? |
| --- | --- | --- |
| `report/profile.go` | `PromptCost`, `OutputCost`, `CacheRead` | **yes** — `total_cost`, the headline |
| `analysis/carry.go` | `CacheRead`, `CacheWrite5m` in `CarryEIT` | **yes** — the per-item carry ranking |
| `report/treenames.go` | `CacheRead`, twice | **yes** — the ceilings in the unattributed note |
| `report/tree.go` | `Output` only | **no** — `Output` is 5.0 for every model |

`tree.go` was latent rather than live: correct by coincidence, wrong by
construction, and live the day a model ships with a different output
multiple.

## What was built

The three cheap sites first, then the span walk, all against one shared
fixture in `internal/cost/costtest` — a session whose first call is on
`claude-opus-5` and whose remaining calls are on `claude-fable-5-1`, with
cache reads dominating. Shared because the recurrence pattern was four
independent copies of one mistake, and a test written beside each fix would
have covered that copy and let the fifth in. Synthetic and hand-written,
with counts chosen to make the error visible.

- **`report/profile.go`** sums `cost.SessionCost`, and its write share uses
  the new `cost.WriteCost`, which accumulates the input and cache-read terms
  per call instead of subtracting one model's rates from a session total.
- **`report/treenames.go`** prices its two ceilings at `cost.MaxCacheRead`,
  the dearest read among the models present. A ceiling computed at the
  cheapest rate in a mixed session is not a ceiling.
- **`report/tree.go`** splits generation into thinking, observed per call,
  and the rest, apportioned by byte share — both at each call's own output
  rate. It was never observably wrong and is not now.
- **`analysis/carry.go`** walks the span. `spanPricer.arrived` prices the
  first call of a residency span at its own model's write rate and each
  later call at its own model's read or write rate; `spanPricer.carried`
  does the same without a leading write, for the preamble. A bucket would
  not have done: a bucket loses the order, and the order is what says which
  send was the write.

`weightsFor`, `residency`, `residencyCost` and `chargeFirstSendAsWrite` are
gone, along with the comment above `weightsFor` claiming this was "the only
place left" that approximated. It was not, and a comment asserting a
property the code lacks is worse than none — it stops the next person
looking.

## Two figures moved, both corrections

A retrieval arriving into a cold call used to be charged a write for the
cold call **and** a second write for the first-send substitution: 149 EIT
where the content was written once. In call order it is 80, and the same
item carries through hotspots' unattributed total. Two more rows moved in
the last decimal place, from accumulating per call rather than multiplying
counts.

No schema bump: the keys are unchanged and the values are less wrong. The
version is the output contract, and a consumer needs no code change.

## What was deliberately left

The counterfactual send counts, including the place where the item ranking
and what-you-typed disagree by one send. Which sends a no-caching figure
should count is a question about the counterfactual, not about which weights
apply to it, and folding the two together would have made the golden diff
unreadable.

## Why the catalog check does not cover this

[`litellm-pricing.md`](litellm-pricing.md) verifies that the *weights* are
right. This plan was about *selecting* the right weights. The two now sit
next to each other in the code and read as one thing, so it is worth being
explicit: a catalog check that every `cache_read` multiple matches a
published rate passes unchanged on a session priced entirely at the wrong
model's multiple, and the fixture here passes unchanged if every multiple in
the table is wrong by the same factor. Keep both — `internal/cost`'s catalog
comparison and `internal/cost/costtest`'s mixed session. Neither substitutes
for the other, and "we have a pricing test now" is exactly the sentence that
would have let this bug survive.
