# The money stops at the subagent boundary

Status: proposed, 2026-09-19. Nothing here is built.

Found by running the tool on one of this repository's own sessions after
[`litellm-pricing.md`](litellm-pricing.md) landed — a session that fanned
out to three subagents, with all four transcripts on disk.

## Two problems, one boundary

`profile --prices` prints:

```
Money (published rates, not a bill)
  Prompt                        $5.74   [derived]
  Output                        $1.58   [derived]
  Total                         $7.16   [derived]
  Catalog            BerriAI/litellm@8c4c394ecc82, retrieved 2026-09-19

Subagents (their own contexts, from their own transcripts)
  ...
  Their cost                9,357,749   [derived]
  Session + subagents      10,807,191   [derived]
  Share out of sight            86.6%   [derived]
```

Both blocks are individually correct. Together they say that the money
figure covers the eighth of the work that happened in this context, and the
other seven eighths are available only in EIT.

### 1. The dollar total is a seventh of the answer

`--prices` reaches `Profile.Cost` and not `Profile.Subagents`. Nothing is
stated wrongly — the block says "every other figure here is this session's
own context" — but a reader who asked for dollars and got `$7.16` has the
cost of a fraction of what they asked about. On the session measured, the
true figure was roughly eight times that.

The whole argument for `--prices` was that a number people can act on beats
a unit they have to learn. That argument applies hardest exactly where the
spend is, and that is increasingly not the parent context: 463 subagent
calls against the parent's 58.

### 2. `Session + subagents` is an unsound cross-model sum

This is the more serious half, and it is a soundness bug rather than a
missing feature.

`buildSubagents` prices subagent calls with `cost.SessionCost`, and its doc
comment says why: **"a subagent can run on a different model from its
parent."** It then computes `combined := sessionCost + total` and labels it
`model.EIT`.

That is precisely what `internal/cost/cost.go` warns against:

> One cost-weighted token means "one full-price input token of this model",
> so a total that spans two models adds quantities of different sizes.

And the existing guard does not see it. `MixedPricing` is
`cost.Mixed(s.Invocations)` — the session's *own* invocations.
`s.SubagentInvocations()` is not consulted. So a parent on one model that
dispatches a subagent to another produces `mixed_pricing: false`, a
`combined_cost` adding two models' EIT, and no warning anywhere.

This is not hypothetical. Dispatching cheap subagents from an expensive
parent is a deliberate and common pattern, and it is the case where the
combined figure is both most wanted and most wrong.

## What to build

### Stage 1 — make the existing figure honest

Independent of dollars, and worth shipping alone.

`MixedPricing` should be computed over the session's calls *and* its
subagents' calls, or a second signal added beside it if the existing one has
consumers that mean "this context switched model". The latter is probably
right: they are different claims, and `sessions.go`'s comment ties the
current one to a session that switched model.

Then `combined_cost` carries a caveat when the models differ — inside the
64-character cap, so something of the shape "Adds two models' EIT. Use
--prices." The number stays, because suppressing a headline is worse than
qualifying it, and because it is exact whenever parent and subagents share
a model, which is the common case.

### Stage 2 — dollars across the boundary

`cost.USD(invocations)` already exists and already prices each call at its
own model. It takes `s.SubagentInvocations()` unchanged.

So the work is plumbing plus a decision about shape: `SubagentReport` gains
dollar figures for its own total and for the combined total, and the
renderers print them under `--prices` with the same mandatory catalog pin.
`TestEveryRendererThatPrintsDollarsPrintsThePin` extends to cover them.

Where the combined dollar figure belongs is a real question, not a detail —
see below.

### Stage 3 — the same hole in `sessions`

`sessions` excludes subagent spend from `cost_eit`, deliberately, matching
`profile`'s headline, and says so in its notes. That is defensible per
session and wrong for the command's purpose: `--sort cost` is for ranking a
week, and on a fan-out-heavy week it ranks sessions by the part of them
that happened in the parent context. The session measured here would sort
seven places lower than its true cost warrants.

Probably a column rather than a changed one, so the existing figure keeps
its meaning — and so a reader can see the fan-out ratio, which is itself
the interesting number.

## Decisions not made here

1. **Where the combined dollar figure lives.** Under `Money`, which is
   where a reader looks for money but would then contain a figure about
   more than this context; or under `Subagents` beside `Session +
   subagents`, which keeps each block internally consistent but hides the
   real total from anyone reading the money block. Leaning the second with
   a pointer line in the first, but it is a judgement about where people
   look.

2. **Whether `combined_cost` should exist in EIT at all when the models
   differ.** The alternative to caveating is to omit it and print dollars
   or nothing, on the grounds that an unsound number with a caveat is still
   an unsound number and this tool's whole posture is that `not measurable
   from this data` is a feature. The counter is that it *is* measurable,
   just not in EIT, and the caveat says where to get it.

3. **Whether subagent spend should reach `tree` and `carry`.** It should
   not, on the present argument — the retrieval tree and the carry figures
   are about one context, and a subagent's context is its own. But
   "87% of the cost is invisible to `tree`" is the same complaint in a
   different place, and the honest answer may be a separate view rather
   than a refusal.

## Not in scope

Per-node and per-cause dollars inside `tree` and `cache`, which
[`litellm-pricing.md`](litellm-pricing.md) leaves open for a different
reason: they need each node's cost attributed to the call that incurred it.
Subagent totals need no such attribution — the invocations are right there.
The two unlock separately.
