# Mutation testing

Status: **not built**, proposed 2026-09-19. Every number below was measured on
this repository at `0d6781d` with gremlins v0.6.0; none of it is estimated
unless it says so.

## Why the coverage floor is not enough

`scripts/checks.sh` has an 80% coverage floor. Coverage asks whether a line
ran. It does not ask whether anything would have noticed if the line were
wrong, and on this codebase those two questions have very different answers.

Two measurements make the case.

`internal/tokens.Calibrate` is **88.6% covered**. Mutation testing kills 39 of
its 52 mutants — **75% efficacy** — and every one of the 13 survivors sits on a
guard that decides whether the function returns a calibrated ratio or falls
back to `uncalibrated`:

```
tokens.go:100   if s.ResultBytes <= 0 || s.PromptGrowth <= 0    <=  ->  <
tokens.go:111   if used < 4                                      <  ->  <=
tokens.go:131   if slope <= 0                                   <=  ->  <
tokens.go:135   if ratio < minRatio || ratio > maxRatio          <  ->  <=
tokens.go:138   if intercept < 0                                 <  ->  <=
```

Each of those is the boundary of a policy: four samples is the minimum fit,
`minRatio`/`maxRatio` is the plausible band. Flip any of them and the suite
stays green — which means the thresholds that choose between a `derived`
number and a fallback have no test holding them to their stated values. That
is rule 1, unguarded.

`internal/analysis/carry.go` is worse and simpler: **90.3% line coverage,
25.6% mutation efficacy**, 64 survivors out of 86 covered mutants. There is no
`carry_test.go`. 488 lines of cache-class span pricing — the machinery behind
rule 4 — are reached by other packages' tests and asserted on by nothing. The
coverage floor cannot see this. It is the single largest hole in the suite and
mutation testing is what found it.

For contrast, `internal/report/tree.go` is 99.0% covered and 53.0% efficacy,
and `cmd/tokenamun` is 100% efficacy across 95 mutants — the end-to-end golden
tests kill everything, because any mutation anywhere shows up as a golden
diff. Worth noting what that score does and does not mean: a golden file is an
excellent detector and a poor localiser. It tells you something changed, never
which claim was violated.

## What the whole module scores today

Whole module, package-scoped, `--timeout-coefficient 30`: **1453 mutants,
9m46s, 70.75% efficacy, 85.17% mutator coverage** (1028 killed, 425 lived, 253
not covered, 5 timed out).

| package | killed | lived | efficacy |
|---|---|---|---|
| `cmd/tokenamun` | 95 | 0 | 100.0% |
| `internal/parsecache` | 23 | 5 | 82.1% |
| `internal/entire` | 69 | 17 | 80.2% |
| `internal/tokens` | 39 | 13 | 75.0% |
| `internal/codescan` | 75 | 26 | 74.3% |
| `internal/content` | 45 | 18 | 71.4% |
| `internal/claudecode` | 34 | 15 | 69.4% |
| `internal/report` | 445 | 197 | 69.3% |
| `internal/model` | 20 | 10 | 66.7% |
| `internal/ingest` | 60 | 34 | 63.8% |
| `internal/cost` | 11 | 7 | 61.1% |
| `internal/analysis` | 112 | 83 | 57.4% |

The ordering is the finding. The CLI surface — the part with the most obvious
tests — is the best covered. The accounting core is the worst. `analysis`,
`ingest`, `cost` and `model` are where the four rules actually live, and they
are the bottom four.

## Two ways this measurement lies, both hit while measuring it

### The default timeout reports success on nothing

First real run, `internal/tokens`, default settings:

```
Killed: 2, Lived: 0, Not covered: 1
Timed out: 50, Not viable: 0, Skipped: 0
Test efficacy: 100.00%
```

Fifty of fifty-three mutants timed out, and gremlins printed **100% efficacy**
on the two that ran, because a timed-out mutant counts as neither killed nor
lived. It is the leak-guard failure exactly: a check whose patterns match
nothing reports success. `--timeout-coefficient 30` fixes it — the same
package then reports 39 killed, 13 lived, 0 timed out, 75% efficacy.

The coefficient multiplies the package's baseline test duration, and this
repo's packages run in under two seconds, so the default budget is a few
hundred milliseconds against a mutant that has to recompile first.

**A few timeouts are legitimate** — a mutated loop condition can genuinely
hang, and gremlins is right not to score it. What is not legitimate is a run
where the denominator has quietly collapsed. So the gate needs a check on the
timeout *rate*, not on the count: 5 of 1453 (0.34%) at coefficient 30 is
normal; 50 of 53 is a broken measurement wearing a green tick.

> **Decided:** the wrapper fails if timed-out mutants exceed 2% of runnable
> ones, and fails if the total mutant count drops by more than 10% from the
> recorded baseline. Both guard the denominator. An efficacy floor alone
> cannot: it is a ratio, and both of these corrupt it silently.

### Package-scoped test runs understate the accounting packages

By default gremlins runs only the tests of the package the mutant is in
(`go test -failfast <pkg>`). `checks.sh` already had to solve this problem for
coverage, and says so in a comment: coverage is measured with
`-coverpkg=./...` "because the adapters in `internal/claudecode` are exercised
through `internal/ingest`, and a per-package figure would report them as
untested and be wrong."

Mutation testing reintroduces it. Measured on `internal/model`:

| | mutants covered | not covered | efficacy | time |
|---|---|---|---|---|
| package-scoped | 30 | 24 | 66.67% | 7s |
| `-i --coverpkg ./...` | 53 | 1 | **84.91%** | 4m11s |

The cheap number is wrong in both directions. It hides 24 mutants as
uncovered that the module's tests do cover, and it reports 10 survivors where
only 8 survive.

`-i` (integration mode) makes every mutant run `go test ./...`. That is a 35×
multiplier: extrapolated across all 1453 mutants it is roughly **five to six
hours** locally, which is past what a weekly job should cost and close to the
GitHub Actions job ceiling.

The same comparison for the other two packages that look under-tested,
measured the same way:

| package | scoped: covered / efficacy | integration: covered / efficacy | time |
|---|---|---|---|
| `internal/model` | 30 / 66.67% | 53 / **84.91%** | 4m11s |
| `internal/claudecode` | 49 / 69.40% | 65 / **70.77%** | 4m17s |
| `internal/cost` | 18 / 61.11% | 32 / **65.62%** | ~2m |

`internal/model` is the one the cheap number badly misjudged — 18 points, and
"not covered" fell from 24 mutants to 1. The adapter and the pricing tables
move less, but in both the mutant population grows by a third, so the scoped
figure was a ratio over the wrong denominator in all three.

> **Decided:** integration mode for the three packages whose tests live
> elsewhere, package-scoped for the rest. The criterion is visible in the
> data — `internal/model` (44% of mutants "not covered"),
> `internal/claudecode` (31%) and `internal/cost` (44%) are the outliers; every
> other package sits at 10–20%. Those three are also, not coincidentally, the
> quarantine and the model everything downstream consumes. Measured cost of
> the three in integration mode: **10m45s**, plus ~9 min for the rest
> package-scoped — call it 20 minutes, against 9m46s for the cheap-and-wrong
> version and ~5h for integration everywhere.

`internal/cost/costtest` is a test helper — 5 mutants, none reachable by
definition. Exclude it with `-E`.

## Cadence: weekly, and nowhere else

**Not in `scripts/checks.sh`.** The pre-commit hook runs `checks.sh fast` and
has to stay in the seconds. Twenty minutes is not a gate anyone would keep
installed, and a gate people bypass is worse than no gate, because it teaches
`--no-verify`.

**Not on push or pull request** either. See the rejected alternative below —
this was a genuine choice, not an oversight.

**A scheduled weekly workflow**, `.github/workflows/mutation.yml`, separate
from `checks`, for the same reason `prices` is separate: a mutation score
moving must never be confusable with a gate in `checks.sh` going red. It runs
`scripts/mutation.sh`, needs no network and no API key, and fails the run when
a floor is breached. GitHub emails the repository owner when a scheduled
workflow fails, which is the only notification channel this gets — worth
stating plainly, because a red scheduled build nobody reads is not a gate.

Estimated CI cost: ~21 minutes locally, so 45–60 minutes on a
GitHub-hosted runner. Weekly, that is fine.

The honest cost of weekly-only: a weak test written on Monday is flagged on
Sunday, several commits downstream of the change that caused it. Mutation
testing's feedback is most actionable attached to a diff, and this cadence
detaches it. What weekly buys instead is the whole-module number — the thing
that found `carry.go` — and nobody has to wait for it during review.

## What the gate asserts

**A per-package efficacy floor, ratcheted, not one module number.** The module
figure is dominated by `internal/report`: 755 of 1453 mutants, more than half.
A regression that took `internal/cost` from 61% to 30% would move the module
number by under half a point and pass. The floors therefore live per package,
in a table at the top of `scripts/mutation.sh` — stated there rather than
buried in the step, in the same spirit as `COVERAGE_MIN`, because they are
policy rather than measurement.

Set each floor just below where the package is today, the way the code budgets
are set just above where the code is. Raise one only with the reason in the
commit message; lowering one is the thing that needs an argument.

Three things the floor must not be:

* **Not 100%.** Some mutants are equivalent — they change the source without
  changing behaviour, and no test can kill them. Chasing them produces tests
  that assert on implementation.
* **Not a count of survivors.** Efficacy is a ratio, so it improves when code
  is deleted, which is a real improvement. A survivor count would punish
  refactoring.
* **Not applied to `internal/cost/costtest`.** Excluded outright.

Alongside the floors, the two denominator guards from the timeout section: a
timeout rate over 2%, or a mutant count more than 10% below the recorded
baseline, fails the run regardless of efficacy.

The JSON from `gremlins -o` is what the script reads — it carries
`test_efficacy`, per-file `mutations` with `status`, and the mutator
statistics. Commit the summary per run? No: that is a second description of
the code that nothing keeps true. The baseline counts live in the script
beside the floors.

## Where the tool comes from

gremlins v0.6.0, pinned as a `tool` dependency in `go.mod` next to `deadcode`.
Measured cost: 20 indirect requirements added (cobra, viper, afero and their
tree), `go.sum` from 10 lines to 57. `go tool gremlins` then works for anyone
who clones, with no install step. AGENTS.md's condition still holds — nothing
shipped imports it.

> **Decided:** a `tool` dependency rather than a pinned release binary
> installed by CI, which is the other precedent here. gremlins publishes
> release tarballs, so the golangci-lint route was available and would have
> kept `go.mod` at 13 lines. What it would have cost is the failure this
> repository has already had: golangci-lint was absent locally, `checks.sh`
> skipped it silently, and the gate linted nothing on every run it ever had
> until CI was made to install it. A mutation gate is a worse candidate for
> that trap than a linter, because its output is a percentage — a run that
> skipped and a run that passed look alike at a glance. Twenty lines of
> `go.sum` against a gate that cannot silently not exist.

The version is pinned for the reason `deadcode` and golangci-lint are: a new
release of the analyser must not change the verdict on an unchanged commit. A
mutation score is more sensitive to this than most — a new mutator in a point
release changes the denominator and every floor with it.

Settings that must not drift between a local run and CI go in `.gremlins.yaml`
at the repo root, not in the workflow: `timeout-coefficient`, the mutator
set, the exclusions. The workflow calls `scripts/mutation.sh` and passes
nothing, so "it passed locally" and "it passed in CI" keep meaning the same
thing.

## Rejected: a diff-scoped gate on every push

`gremlins -D <ref>` mutates only lines the diff touched. Measured against a
real five-commit range on this repo that changed ~400 lines of Go: **20
mutants, 19.6 seconds, 15 killed / 3 lived / 2 not covered.** Against a range
touching no Go at all it skipped all 1711 candidates in 0.5s. It is cheap
enough to have run on every push, and it asks the question that is most
useful during review — are the lines you just wrote tested for what they do,
or only for the fact that they run?

What rejecting it costs is that feedback. New under-tested code is not caught
at the point it is written; it is caught by the weekly run, up to a week and
several commits later, when the person who wrote it has moved on. The
`internal/tokens` boundary survivors above are exactly the shape of thing a
diff gate catches on the commit that introduces them.

What it buys is one job instead of two, no second threshold to tune, and no
new way for a routine push to go red. A diff-scoped score is also noisy at
small diffs — three survivors out of twenty mutants is 85% efficacy, and a
one-mutant diff is 0% or 100% with nothing in between, so the gate would have
to be "no new survivors" rather than a percentage, which is a different
metric needing its own justification.

If the weekly run turns out to mostly report regressions that a diff gate
would have caught a week earlier, that is the signal to build this after all.
The measurement above is the starting point; it will not need re-taking.

## Rejected: writing our own mutator

`internal/codescan` already walks Go ASTs for complexity, so the parsing half
exists. The execution half — copying the module, applying a mutation,
compiling, running tests under a timeout, and doing it 1453 times with a
worker pool — does not, and it is the part with the bugs. gremlins is
maintained (v0.6.0, December 2025; the repository is active and not
archived), builds under Go 1.26, and its five default mutators are the
standard set. Nothing about this codebase needs a mutator the standard set
lacks.

## Work

1. `go get -tool github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0`.
   One commit, `go.mod` and `go.sum` only, with the trade-off from the
   "Decided" above in the message.
2. `.gremlins.yaml`: `timeout-coefficient: 30`, default mutators, exclude
   `costtest`. Comment the coefficient with what the default did, or somebody
   will lower it back.
3. `scripts/mutation.sh`: two gremlins invocations (integration mode for
   `model`, `claudecode`, `cost`; package-scoped for the rest), merge the two
   JSON outputs, apply the per-package floors, the 2% timeout rate and the
   10% mutant-count drop. Each floor is the measured figure rounded down to
   the nearest five, or the next five below that where rounding left under
   two points of headroom:

   | | floor | | floor | | floor |
   |---|---|---|---|---|---|
   | `cmd/tokenamun` | 95 | `internal/codescan` | 70 | `internal/model` † | 80 |
   | `internal/parsecache` | 80 | `internal/content` | 65 | `internal/claudecode` † | 65 |
   | `internal/entire` | 75 | `internal/report` | 65 | `internal/cost` † | 60 |
   | `internal/tokens` | 70 | `internal/ingest` | 60 | `internal/analysis` | 55 |

   († integration mode.) Two need revisiting once items 5 and 6 land:
   `internal/analysis` at 55 ratifies `carry.go`, and `internal/tokens` at 75
   ratifies the `Calibrate` boundaries. Set them where the tests leave them,
   in the commit that adds the tests.
4. `.github/workflows/mutation.yml`: `schedule` weekly plus
   `workflow_dispatch`, checkout, setup-go, run the script, upload the JSON
   as an artifact.
5. Write `internal/analysis/carry_test.go`. This is the point of the whole
   exercise and should land before the gate does, because a floor set at
   25.6% ratifies the hole. Target the span-pricing boundaries the survivors
   cluster on: `from >= stop`, the reset handling in `stopAt`, and the
   `pe.Bytes == 0 || pe.InvocationSeq < 0` skip in `carryTyped`.
6. Boundary tests for `Calibrate`'s five thresholds. Small, and they pin
   numbers that decide a provenance label.
7. A paragraph in AGENTS.md under "The checks are gates, not reports": what
   the weekly job is, that it is not in `checks.sh`, and that the floors are
   ratchets. `docs/METHODOLOGY.md` needs nothing — this changes no number.

## Not doing

* **No mutation score in the tool's own output.** Tokenamun profiles token
  usage; a test-quality number in its report is a second product.
* **No per-developer attribution of survivors.** `git blame` over a mutation
  report is a leaderboard, which is the failure mode this tool is closest to.
* **No badge.** A percentage on the README invites moving the number rather
  than the tests under it.
