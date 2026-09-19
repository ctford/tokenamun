# A price catalog, from LiteLLM

Status: **built**, 2026-09-19. All three stages are in.

> **Decided:** EIT stays the default output unit permanently. Dollars are
> opt-in via `--prices`, and only on the cross-model surfaces where EIT is
> unsound.

> **Decided:** dollars are labelled `[derived]`, with the catalog pin
> mandatory beside them. `[derived-approx]` is for a stated *estimator* —
> the byte-ratio token count, branch-keyword complexity — where the error is
> a property of the method and you can reason about its size. Nothing here
> is estimated: the tokens are observed, each call is converted at its own
> model's published rate, and the arithmetic is exact. What can go wrong is
> that the rate is stale or the catalog is wrong about a model, which is not
> an approximation and is not made honest by a label. It is made honest by
> saying which catalog at which commit — which is why `Prices.Catalog` is
> not optional and a test asserts no renderer can print a dollar without it.

## The problem this solved

`internal/cost/cost.go` says it plainly:

> One cost-weighted token means "one full-price input token of this model",
> so a total that spans two models adds quantities of different sizes.
> [...] a week of twelve Opus sessions and six Sonnet ones came to be summed
> into one figure without anyone noticing.

The answer had been to warn: `SessionInfo.MixedPricing` is set, `cache`
reports when a set spans more than one pricing, and the reader could do
nothing about it. That is the right answer for a tool with no price list,
and `tokenamun tree all` — the command the whole Entire integration exists
to serve — is the command it failed.

Second and smaller: the weights in `cost.go` were hand-written constants
checked against nothing. The comment above `For()` records what that cost
once already — matching on `"fable"` priced Fable 5's cache reads at a
quarter of their real rate, a fourfold under-statement on the class that is
~97% of prompt volume.

## Why LiteLLM and not models.dev

Both MIT, both maintained, both covering the models this tool profiles.
Checked 2026-09-19:

|                              | LiteLLM                  | models.dev             |
| ---------------------------- | ------------------------ | ---------------------- |
| 1h cache-write rate          | yes, `..._above_1hr`     | **no**, one `cache_write` |
| `claude-mythos-5-1`          | present                  | **missing**            |
| entries                      | 4,318                    | 222 providers, 14 Anthropic models |
| licence                      | MIT (outside `enterprise/`) | MIT                 |

The 1h rate decides it alone. The entire `cache` counterfactual is the
1.25x-against-2.0x spread, and a catalog with one cache-write number would
collapse the two silently — the worst available failure for that command.

## The one design constraint

**No network access at run time.** A price fetched when a report is rendered
would make the report of an unchanged session depend on the day you ran it,
which is the opposite of what a profiler is for. `scripts/checks.sh` already
settles this question for tool dependencies:

> Pinned as a tool dependency rather than fetched at @latest, so a new
> release of the analyser cannot change what this check says about an
> unchanged commit.

Same reasoning, same answer: vendor a pinned extract, refresh it
deliberately. Both other tools in this space fetch at run time and cache —
they are dashboards showing today's spend, so a figure that moves under them
is not a problem. It is a problem here.

## Stage 1 — a check, not a feature

The catalog's first job was to test the constants, not to produce a number.
That answers the objection in `cost.go` that a price table is "configuration
rather than measurement": nothing was read to produce a figure. A published
rate verifying a constant is a test. It shipped with **no change to any
output**.

- `scripts/refresh-prices.sh` writes the Claude subset plus the upstream
  commit SHA it came from. It is also the quarantine boundary for LiteLLM's
  field names, per the adapter rule in AGENTS.md: nothing in Go sees
  `cache_creation_input_token_cost_above_1hr`.
- A table test asserts, for every model in the extract, that
  `cache_read / input` equals the `Weights.CacheRead` that `For()` returns,
  and likewise both write rates and output.
- CI runs the refresh and fails on drift.

Three things came out differently from the sketch.

**The extract lives at `internal/cost/litellm-prices.json`, not under
`testdata/`.** From stage 2 it is embedded and read by the binary, and a
`testdata/` path for shipped data would be a lie about what it is.

**It found a second instance of the bug it was written for.** Claude 3
Haiku's published rates are not round multiples of its input price — $0.03
read and $0.30 write against $0.25 input, so 0.12x and 1.2x — and the
constants had assumed they were. `cost.For` gained a `haiku3` case.
Reintroducing the old `"fable"` match fails the table test on
`claude-fable-5`, `claude-mythos-5` and `claude-mythos-preview`, so the
check does catch what it was built for.

**The CI check compares prices only, not the pinned commit.** Upstream
commits several times an hour, so comparing the SHA would redden the build
whenever somebody touched an unrelated provider, and a gate that fails for
reasons you learn to ignore is not a gate. `refresh-prices.sh` still
resolves the upstream tip to a SHA and fetches *that*, so the extract stays
reproducible from its pin.

## Stage 2 — one scalar per model

EIT is *defined* as a multiple of that model's input price, so the
conversion needs exactly one number per model:

```
dollars = EIT x input_price_per_token
```

The other four columns stay verification-only. Built as `cost.InputPrice`,
`cost.CatalogPin()` for the provenance line, `cost.USD(invocations)` which
totals a set of calls by pricing each at its own model, and `USD` on
`model.Unit`.

`InputPrice` returns a `bool`, and that is the point: **an unknown model
fails loudly rather than falling through to a default.** Silent
fall-through is how the Fable bug survived. `For()` still defaults, and its
doc comment argues that is "the right failure mode" — true for a ratio,
false for a price.

The lookup matches the bare key only. 19 keys upstream mention `opus-5`
(`anthropic.claude-opus-5`, `eu.anthropic.…`, `bedrock/us-gov-east-1/…`,
`databricks/…`, `aihubmix/…`).

Adding `USD` changed no output on its own, so stage 2 did not bump the
schema. Stage 3 bumped once, for the unit and the fields together.

## Stage 3 — `--prices` where EIT is actually unsound

Built as `SessionInfo.Prices`, because that is where `MixedPricing` already
lives: the warning and the answer to it belong in the same place.
`profile`, `profile all`, `cache`, `cache all`, `tree` and `tree all` take
`--prices`. Every other command **rejects** it rather than ignoring it — a
flag that silently does nothing is the worst failure available to a CLI an
agent drives.

Two boundaries worth naming.

`report` is not on the list. Its payload feeds the HTML viewer, so pricing
it without touching the template would put a figure in the JSON the browser
does not show, and "nothing is viewer-only" cuts both ways.

Cost *inside* a report stays in EIT — node costs in `tree`, per-cause costs
in `cache`. Converting those needs each node's or cause's cost attributed to
the call, and so the model, that incurred it. Neither the carry tree nor
`analysis.CacheReport` carries that, and a per-node dollar figure would have
to pick one model for the whole tree, which is the error the money total
exists to avoid.

## What is still missing

Per-node and per-cause dollars, as above. The attribution they need is now
partly in place after [`per-call-pricing.md`](per-call-pricing.md).

`--prices` also does not reach the subagent block, which on a fan-out
session is most of the cost — see
[`subagent-dollars.md`](subagent-dollars.md).
