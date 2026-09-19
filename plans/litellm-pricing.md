# A price catalog, from LiteLLM

Status: built, 2026-09-19. All three stages are in.

> **Decided:** EIT stays the default output unit permanently. Dollars are
> opt-in via `--prices`, and only on the cross-model surfaces where EIT is
> unsound. Stage 1 is still worth shipping on its own.

> **Decided:** dollars are labelled `[derived]`, with the catalog pin
> mandatory beside them. `[derived-approx]` is for a stated *estimator* --
> the byte-ratio token count, branch-keyword complexity -- where the error is
> a property of the method and you can reason about its size. Nothing here is
> estimated: the tokens are observed, each call is converted at its own
> model's published rate, and the arithmetic is exact. What can go wrong is
> that the rate is stale or that the catalog is wrong about a model, which is
> not an approximation and is not made honest by a label. It is made honest
> by saying which catalog, at which commit, which is why `Prices.Catalog` is
> not optional and a test asserts no renderer can print a dollar without it.

## The problem this solves

`internal/cost/cost.go` says it plainly:

> One cost-weighted token means "one full-price input token of this model", so
> a total that spans two models adds quantities of different sizes. [...] a
> week of twelve Opus sessions and six Sonnet ones came to be summed into one
> figure without anyone noticing.

The current answer is to warn. `SessionInfo.MixedPricing` is set, `cache`
reports when a set spans more than one pricing, and the reader is left to do
nothing about it. That is the right answer for a tool with no price list, and
`tokenamun tree all` — the command the whole Entire integration exists to
serve — is the command it fails.

There is a second, smaller problem. The weights in `cost.go` are hand-written
constants checked against nothing. The comment above `For()` records what that
cost once already: matching on `"fable"` priced Fable 5's cache reads at a
quarter of their real rate, "very nearly a fourfold under-statement of the
session", on the class that is ~97% of prompt volume.

## Why LiteLLM and not models.dev

Both are MIT, both are maintained, both cover the models this tool profiles.
Checked 2026-09-19:

|                              | LiteLLM                  | models.dev             |
| ---------------------------- | ------------------------ | ---------------------- |
| 1h cache-write rate          | yes, `..._above_1hr`     | **no**, one `cache_write` |
| `claude-mythos-5-1`          | present                  | **missing**            |
| entries                      | 4,318                    | 222 providers, 14 Anthropic models |
| licence                      | MIT (outside `enterprise/`) | MIT                 |

The 1h rate decides it on its own. The entire `cache` counterfactual is the
1.25x-against-2.0x spread; a catalog with one cache-write number would
collapse the two silently, which is the worst available failure for that
command.

LiteLLM also agrees with every weight in `cost.go`, including the distinction
that `For()` carries a paragraph of scar tissue about:

```
model                   in$/M   read x    w5m x    w1h x    out x
claude-opus-5            5.00      0.1     1.25        2        5
claude-sonnet-5          2.00      0.1     1.25        2        5
claude-fable-5-1        10.00    0.025     1.25        2        5
claude-fable-5          10.00      0.1     1.25        2        5   <- the bug
claude-mythos-5-1       10.00    0.025     1.25        2        5
claude-mythos-5         10.00      0.1     1.25        2        5
claude-haiku-4-5         1.00      0.1     1.25        2        5
```

Source: `BerriAI/litellm`, `model_prices_and_context_window.json`.

## The one design constraint

**No network access at run time.** The README's first claim about what this
tool is:

> It reads transcripts that already exist [...] It records nothing itself.

A price fetched when a report is rendered would make the report of an
unchanged session depend on the day you ran it, which is the opposite of what
a profiler is for. The repository already settles this question for tool
dependencies, in `scripts/checks.sh`:

> Pinned as a tool dependency rather than fetched at @latest, so a new release
> of the analyser cannot change what this check says about an unchanged
> commit.

Same reasoning, same answer: vendor a pinned extract, refresh it deliberately.
The upstream file is 2.8 MB and 4,318 entries; extract the ~30 Claude rows and
the commit SHA they came from, not the file.

Both other tools in this space fetch at run time and cache — tokscale from
LiteLLM with a one-hour disk cache, token-monitor from models.dev with a daily
refresh. They are dashboards showing you today's spend, so a figure that moves
under them is not a problem. It is a problem here.

## Stage 1 — a check, not a feature *(built)*

The catalog's first job is to test the constants, not to produce a number.
This is what answers the objection in `cost.go` that a price table is
"configuration rather than measurement": at this stage nothing is read to
produce a figure. A published rate is used to verify a constant, which is a
test.

- `scripts/refresh-prices.sh` writes `internal/cost/testdata/litellm-prices.json`:
  the Claude subset, plus the upstream commit SHA it was taken from.
- A table test asserts, for every model in the extract, that
  `cache_read / input` equals the `Weights.CacheRead` that `For()` returns for
  it, and likewise for the two write rates and output.
- CI runs the refresh and fails on drift, so a published rate change arrives
  as a red build rather than as a quietly wrong report.

This would have caught the Fable bug: reintroducing the `"fable"` match fails
the table test on `claude-fable-5`, `claude-mythos-5` and
`claude-mythos-preview`. It shipped with **no change to any output**.

Three things came out differently from the sketch above.

The extract lives at `internal/cost/litellm-prices.json`, not under
`testdata/`. From stage 2 it is embedded and read by the binary, and a
`testdata/` path for shipped data would be a lie about what it is.

It found a second, much smaller instance of the bug it was written for.
Claude 3 Haiku's published rates are not round multiples of its input price --
$0.03 read and $0.30 write against $0.25 input, so 0.12x and 1.2x -- and the
constants had assumed they were. `cost.For` gained a `haiku3` case.

The CI check compares **prices only**, not the pinned commit. Upstream commits
several times an hour, so comparing the SHA would redden the build whenever
somebody there touched an unrelated provider, and a gate that fails for
reasons you learn to ignore is not a gate. `refresh-prices.sh` resolves the
upstream tip to a SHA and fetches *that*, so the extract is reproducible from
its pin even though the pin is not what the check compares.

It does not catch the adjacent bug below, and the test says so in a comment
where it would otherwise be assumed: verifying that a model's weights are
right is a different property from verifying that a call is priced with its
own model's weights, and neither test catches the other's bug.

## Stage 2 — one scalar per model *(built)*

EIT is *defined* as a multiple of that model's input price, so the conversion
needs exactly one number per model:

```
dollars = EIT x input_price_per_token
```

The other four columns stay verification-only. Work:

- add `USD` to `model.Unit` (currently `Tokens, EIT, Bytes, Calls, Ratio, Seconds`)
- add `cost.InputPrice(modelID) (float64, bool)` over the pinned extract

The `bool` is the point. **An unknown model must fail loudly rather than fall
through to a default.** Silent fall-through is how the Fable bug survived, and
`For()` still does it — its doc comment argues the default is "the right
failure mode", which is true for a ratio and false for a price.

Built as described, plus `cost.CatalogPin()` for the provenance line and
`cost.USD(invocations)`, which totals a set of calls in dollars by pricing
each at its own model. Adding `USD` to `model.Unit` changed no output on its
own, so stage 2 did not bump the schema; stage 3 did, once, for the unit and
the fields together.

Note the key shapes before writing the lookup: 19 keys in the upstream file
mention `opus-5` (`anthropic.claude-opus-5`, `eu.anthropic.…`,
`bedrock/us-gov-east-1/…`, `databricks/…`, `aihubmix/…`). Match the bare key
only.

## Stage 3 — `--prices` where EIT is actually unsound *(built)*

Only on the surfaces that span models: `profile all`, `tree all`, `cache`, the
merged reports — wherever `MixedPricing` is set today. There, `--prices`
replaces a warning with a correct total. Single-model reports keep EIT as the
default and lose nothing by it.

Any report that prints dollars must print the catalog pin beside them, the way
`Estimator` already prints its method. A dollar figure whose source is not
stated is the kind of number this tool exists not to produce.

Built as `SessionInfo.Prices`, because that is where `MixedPricing` already
lives: the warning and the answer to it belong in the same place, and every
report that identifies what was profiled then carries both. `profile`,
`profile all`, `cache`, `cache all`, `tree` and `tree all` take `--prices`.
Every other command **rejects** it rather than ignoring it — a flag that
silently does nothing is the worst failure available to a CLI an agent drives.

Two boundaries worth naming.

`report` is not on the list. Its payload feeds the HTML viewer, so pricing it
without touching the template would put a figure in the JSON that the browser
does not show, and "nothing is viewer-only" cuts both ways.

Cost *inside* a report stays in EIT — node costs in `tree`, per-cause costs in
`cache`. Converting those needs each node's or cause's cost attributed to the
call, and so the model, that incurred it. Neither the carry tree nor
`analysis.CacheReport` carries that yet, and a per-node dollar figure would
have to pick one model for the whole tree, which is precisely the error the
money total exists to avoid. It is the same missing attribution as the
adjacent bug below, and it becomes available once that is fixed.

## Both decisions, now made

1. ~~**How dollars are labelled.**~~ Decided: `[derived]`, with the catalog
   pin mandatory beside them. See the blockquote at the top.
2. ~~**Whether EIT stays the default.**~~ Decided: it does, permanently. It
   needs no price list and is exact within a model. Dollars are opt-in, for
   the cross-model case only.

## Not in scope, but adjacent

`profile.go:97`, `tree.go:184`, `treenames.go:212` and `carry.go:160` price a
whole session with `cost.For(ms[0])` — the first model's weights applied to
every call in it. That is the mistake `cost.PerCall` was written to prevent
and that `analysis/cache.go:118` records as having been fixed; it survives in
four other places, and bites any session that switches model, which `opusplan`
does on every plan-mode toggle.

Stage 1 will not catch it: the weights are right and the *selection* of them
is wrong. Worth fixing first, and separately — it is a bug in what this tool
already claims to do, where the catalog is an addition to it.

Still true after all three stages, and now visible side by side. On a
two-model fixture the money total is right and the EIT total beside it is not,
because `cost.USD` walks invocations and `BuildProfile` still takes
`cost.For(ms[0])`. Fixing that also unlocks per-node dollars in `tree`.
