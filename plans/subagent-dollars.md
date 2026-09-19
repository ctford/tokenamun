# The money stopped at the subagent boundary

Status: **built**, 2026-09-19. All three stages are in.

> **Decided:** the combined dollar figure lives under `Subagents`, not under
> `Money`. Money is this context, like every other figure in the report bar
> the combined one, and moving a cross-context total into the block a reader
> trusts for "what this session cost" would make that block mean something
> else depending on whether the session happened to fan out. The money block
> carries a pointer line instead — "Subagents cost more again; the combined
> total is in their block" — so the reader who looks where money lives is
> told there is more of it, in one line, rather than being left to find the
> larger figure further down or not. Each block stays internally consistent
> and the payload says both things.

> **Decided:** `combined_cost` keeps existing in EIT when the models differ,
> with a caveat, rather than being suppressed. It is *exact* whenever parent
> and subagents share a model, which is the common case, so withholding it
> would refuse the common case to guard the uncommon one. And the tool's
> `not measurable from this data` posture does not apply: the quantity is
> measurable, just not in a model-relative unit, so the honest move is to
> say where the sound number is. The caveat does exactly that — "Adds two
> models' EIT. Only --prices adds across models." — in 55 characters, and
> `--prices` then prints the sound number three lines below it.

> **Decided:** subagent spend does not reach `tree` or `carry`, and this is
> settled rather than left open. Both are about residency inside one
> context: a carry figure is "this content was re-sent on these calls", and
> a retrieval tree is what entered *this* prompt. A subagent's retrievals
> entered a different prompt, so a merged tree would describe a context that
> never existed — the same error as folding subagent cache reads into the
> parent's usage, which `SubagentReport`'s doc comment rejects. "87% of the
> cost is invisible to `tree`" is a real complaint, but the answer to it is
> a view that profiles the subagent transcripts as what they are — their own
> sessions, which they are on disk — not a number smuggled into a figure
> that means one context. Nothing was built for that here.

## What was wrong

Found by running the tool on one of this repository's own sessions after
[`litellm-pricing.md`](litellm-pricing.md) landed — a session that fanned
out to three subagents, with all four transcripts on disk. `profile
--prices` printed a money block about the parent context and a subagent
block about everything else, each individually correct, and together saying
that the figure a reader asked for covered an eighth of the work.

Two problems, one boundary.

**The dollar total was a fraction of the answer.** `--prices` reached
`Profile.Cost` and not `Profile.Subagents`. Nothing was stated wrongly, but
the whole argument for `--prices` was that a number people can act on beats
a unit they have to learn, and that argument applies hardest where the spend
is — increasingly not the parent context.

**`Session + subagents` was an unsound cross-model sum**, which was the
more serious half and a soundness bug rather than a missing feature.
`buildSubagents` prices subagent calls with `cost.SessionCost` precisely
because "a subagent can run on a different model from its parent", then
added the result to the session's EIT. `MixedPricing` was
`cost.Mixed(s.Invocations)` — the parent's calls only — so a parent on one
model dispatching a subagent to another reported `mixed_pricing: false`
beside a total adding quantities of different sizes, with no warning
anywhere. Dispatching cheap subagents from an expensive parent is a
deliberate and common pattern, so the bug sat exactly where the combined
figure is most wanted.

## Stage 1 — the existing figure made honest

Shipped on its own, before any dollars, because it is a correctness fix.

`SubagentReport` gained `CombinedMixedPricing`, computed with `cost.Mixed`
over the parent's calls *and* its subagents', and `CombinedCaveat`, which
carries the 55-character claim when they differ. The text renderer prints it
as a `!` line directly under `Session + subagents`.

**A second signal rather than a wider one**, which the plan guessed at and
the code confirmed. `sessions.go` marks a row "switched model" from
`MixedPricing`, and a parent that dispatched a subagent elsewhere switched
nothing; widening the existing flag would have made that marker wrong on
every fan-out session in order to keep one figure honest. They are two
claims — "this context switched model" and "this total spans contexts and
models" — and the report now makes both.

The fixture is the shape the bug needs and nothing else: one parent call on
`claude-opus-5`, one subagent call on `claude-fable-5-1`, round token counts
chosen so both halves come to 1,500 EIT. Because the halves are equal in
EIT and unequal in dollars, the same fixture later proved the stage 2
conversion was not a rescaled EIT total.

## Stage 2 — dollars across the boundary

`cost.USD` needed no change: it prices each call at its own model and takes
`s.SubagentInvocations()` unchanged. So this was the shape decision above
plus plumbing.

`SubagentReport.Prices` holds `total_cost`, `combined_cost` and a
`catalog`. `WithProfilePrices` prices a whole profile — header and subagent
block from one call — because the combined figure needs both halves, and a
profile carrying money in one block and not the other would answer "what
did this cost" with the part that happened here. `WithPrices` and it now
share `usdOf`, so the refusal to price an unknown model reads the same for a
subagent's model as for the session's: an error, never a total that quietly
drops the calls it could not price.

**The pin is repeated in the block rather than referred to.** A consumer
reading the subagent block alone would otherwise have a dollar figure with
no source, which is the number this tool exists not to produce.
`TestEveryRendererThatPrintsDollarsPrintsThePin` gained the fan-out profile,
and a second test checks the pin appears *within* the subagent block — the
whole-output check passes on a report whose money block has the pin and
whose subagent block does not, and the reader quoting the combined figure is
reading the second one.

## Stage 3 — the same hole in `sessions`

Rows gained `subagent_cost_eit` and `combined_cost_eit`, with a
`combined_mixed_pricing` twin, and `--sort cost` now ranks by the combined
figure. `cost_eit` is unchanged and still one context, which is what every
other command reports; the fan-out ratio between the two is itself the
interesting number on a week of dispatching work out.

The sort had to move rather than gain an option. Ranking a week is the whole
purpose of that order, the list is where somebody decides which session to
open, and a session that dispatched its work to subagents caused that spend
whichever context it landed in.

Two smaller calls. **The combined column prints only on a set where
something fanned out**, because a column that repeats its neighbour on every
row of most weeks is one people learn not to read. And a row says "subagents
priced differently" separately from "switched model", since the parent can
do the second without the first and the sort key is the figure that is
unsound.

## What this deliberately did not do

**Nothing was folded into the session's headline figures.** `total_cost`,
the usage block, the caching shares and the tree are all this context, and
adding a subagent's cache reads to the parent's would describe a prompt that
was never sent. The design is *beside*, not *inside*, and the one figure
that crosses the boundary is labelled as crossing it.

`profile all` still drops the subagent block, as it did before:
`MergeProfiles` never carried one. A merged set's combined total is a
reasonable thing to want and a separate piece of work.

`sessions` does not take `--prices`. The flag is on the surfaces where EIT
is unsound within one report, and adding it to the list is a wider question
than this plan settled.

Per-node and per-cause dollars inside `tree` and `cache` remain out of
scope, for the reason [`litellm-pricing.md`](litellm-pricing.md) gives: they
need each node's cost attributed to the call that incurred it. Subagent
totals needed no such attribution, which is why the two unlocked separately.

## What it cost in schema

Three bumps, 8 → 11, one per stage, rather than one covering all three.
Each stage landed separately and changed the output separately, so a
consumer that read between two landings needs a version that tells it which
of them it got.
