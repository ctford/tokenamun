# Price every call at its own model

Status: proposed, 2026-09-19. Nothing here is built.

Four places price a whole session at the weights of the first model they see.
`cost.PerCall` and `cost.SessionCost` exist to prevent exactly this, and
`internal/analysis/cache.go` was fixed to use them; these four were not.

## Correcting the severity first

An earlier description of this said it "bites any `opusplan` session", on
every plan-mode toggle. That is wrong, and the correction is most of what
makes this plan worth reading.

`cost.For` returns identical weights for every model except the 5.1
generation:

```
claude-opus-5     {Input:1 CacheRead:0.1   CacheWrite5m:1.25 CacheWrite1h:2 Output:5}
claude-sonnet-5   {Input:1 CacheRead:0.1   CacheWrite5m:1.25 CacheWrite1h:2 Output:5}
claude-haiku-4-5  {Input:1 CacheRead:0.1   CacheWrite5m:1.25 CacheWrite1h:2 Output:5}
claude-fable-5-1  {Input:1 CacheRead:0.025 CacheWrite5m:1.25 CacheWrite1h:2 Output:5}
```

So an Opus/Sonnet toggle changes no figure at all: the ratios are the same
and EIT is relative to each model's own input price. `Input` is 1.0 and
`Output` is 5.0 everywhere, which means **the only field that can be wrong is
`CacheRead`**, and only when a session mixes the 5.1 generation with anything
else.

That is narrow. It is also the worst possible field to be wrong about, since
cache reads are ~97% of prompt volume, and the error is fourfold.

`analysis/cache.go:114` carries a version of the same overstatement —
"A session switches model whenever `opusplan` toggles plan mode, and the 5.1
generation reads cache at 0.025x" — which reads as though the toggle were the
trigger. It is not. Worth correcting there too.

## What each site actually risks

Checked by what each `w` is used for, not by the fact that it exists.

| site | uses | exposed? |
| --- | --- | --- |
| `report/profile.go:159` | `w.PromptCost(u)`, `w.OutputCost(u)`, `w.CacheRead` | **yes** — this is `total_cost`, the headline |
| `analysis/carry.go:160` | `w.CacheRead`, `w.CacheWrite5m` in `CarryEIT` | **yes** — the per-item carry ranking |
| `report/treenames.go:212` | `w.CacheRead` twice | **yes** — the thinking and preamble ceilings in the unattributed note |
| `report/tree.go:184` | `w.Output` only | **no, today** — `Output` is 5.0 for every model |

`tree.go` is latent rather than live: correct by coincidence, wrong by
construction, and it becomes live the day a model ships with a different
output multiple. Fix it with the others and say in the comment why it was
never observably wrong.

## The comment that is false

`analysis/carry.go:145`, above `weightsFor`:

> This is an approximation and the only place left that makes it.

It is not the only place left. `profile.go`, `tree.go` and `treenames.go` all
do the same thing, and none of them say so. Whatever else this plan does, that
sentence should stop being in the repository — a comment asserting a property
the code does not have is worse than no comment, because it stops the next
person looking.

## The fix, per site

Three of the four are straightforward, because the correct machinery already
exists and is already used elsewhere in the same files.

**`profile.go`** — replace the hand-rolled `w.PromptCost(u)` / `w.OutputCost(u)`
with `cost.SessionCost(s.Invocations)`, which prices each call at its own
model. The `writeShare` line needs the same treatment: it subtracts
`u.Input*w.Input + u.CacheRead*w.CacheRead` from the prompt cost, so it needs
to accumulate those two terms per call rather than against session totals.

**`treenames.go`** — the two ceilings multiply a token count by `w.CacheRead`.
They are ceilings, deliberately loose, so the honest fix is a cache-read rate
that is itself a ceiling: the *highest* `CacheRead` across the models actually
present. A ceiling computed with the cheapest rate is not a ceiling.

**`tree.go`** — no behaviour change; take the output rate per call for the
same reason, and comment that it was never observably wrong.

**`carry.go` is genuinely harder, and its own comment says why:**

> Carry prices the residency of one content item over a span of calls, and
> those calls can span models, so doing it properly means pricing each send at
> the model of the call it went out on rather than the span at one rate. [...]
> Fixing carry the same way is a larger change, because a bucket is not enough
> — the span has to be walked.

That is correct and the work is real. `CarriedItem.CarryEIT` is
`tokens × (write + warm×read + cold×write)`, where `warm` and `cold` are
counts of calls in the residency span. Priced properly, each of those calls
contributes at its own model's rate, so the scalar counts become a walk over
the span accumulating rates.

Do it last, and only after the cheap three, because it is the one that needs
a design rather than a substitution.

## The test that should have caught it

A session whose first call is `claude-opus-5` and whose remaining calls are
`claude-fable-5-1`, with cache reads dominating. Assert the reported cost
against the per-call sum. Every site above fails it; `cache.go` passes it.

Worth writing once, in a shared place, and pointing every pricing surface at
it — the recurrence pattern here is four independent copies of the same
mistake, and a test that only covers the one site being fixed today will let
the fifth copy in.

## Interaction with the other plans

[`outlier-detection.md`](outlier-detection.md) is the agreed next piece of
work and touches `analysis/carry.go` and `report/carry.go` for `carry all`.
That is the same file as the hardest fix here. Sequence them rather than
running them together: `carry all` first, this second, or the merge will be
unpleasant.

[`litellm-pricing.md`](litellm-pricing.md) Stage 1 is orthogonal — it
verifies that the *weights* are right, where this plan is about *selecting*
the right weights. Neither catches the other's bug. Worth stating in both
places, because "we have a pricing test now" is exactly the sentence that
would let this one survive.
