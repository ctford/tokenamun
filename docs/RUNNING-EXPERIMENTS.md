# Running experiments with Tokenamun

Written against the method in Giles Alexander's
[*Measuring the economic benefit of refactoring for AI agents*](https://martinfowler.com/articles/exploring-gen-ai/refactoring-economic-benefit.html),
because that is a real experiment with a real result and it exposes exactly
which parts a profiler can help with and which it cannot.

## The method it has to support

Three properties carry that design, and an experiment of this shape needs all
three: a **fixed probe task**, which holds the work constant so the codebase is
the only thing varying; a **discard** of each generated change, which keeps the
steps independent; and a **fresh sub-agent** per probe, which stops learning
leaking between runs. Fifteen refactoring steps, one probe after each.

Its result is the worked example below: probe input tokens fell from
**159,564 to 27,360**, an 83% reduction, while output tokens stayed flat. The
refactoring did not make the change smaller, it made finding the place to make
it cheaper.

## What Tokenamun covers

**The token measurement, without the estimation.** His token counts came from a
chars ÷ 4 approximation, and he notes that "accurate token accounting proved
impossible." That is the part Tokenamun removes entirely: per-call
`input_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens` and
`output_tokens` are **observed** in the transcript, straight from the API's own
accounting. No tokenizer, no ratio, no error bar on the headline number. And
because `requestId` deduplication is handled, the count doesn't inflate by the
~70% that naive per-entry summing costs.

**The code metrics, in the same tool.** Total lines and largest-file size are
two of his four metrics, and `tokenamun scan` produces them (plus duplication
and complexity) as part of `internal/codescan`. One command per probe run
instead of a spreadsheet.

**The cost of the refactoring itself — which is the gap.** He could only bound
the refactoring cost at "upper bound five million tokens", and without that
number the economic argument stays one-sided: you know the benefit per future
change but not what you paid to get it. The refactoring work happened in agent
sessions, those sessions have transcripts, and Tokenamun reads them. So the
missing half is measurable:

```
payback (in future changes) = refactoring cost / saving per change
```

With his numbers and a 5M-token refactoring cost, a 132,204-token saving per
change pays back in about **38 changes** to that subsystem. With a 1M-token
refactoring cost it pays back in 8. That is the sentence an economic argument
needs, and the difference between those two answers is why measuring the cost
side matters more than tightening the benefit side.

**Volume versus cost, which his design handles by accident.** A fresh
sub-agent per probe means a cold cache every run, so almost everything is
full-price input and cache writes — volume and cost move together, and his 83%
volume reduction is close to an 83% cost reduction. That is luck, not
robustness. Run the same probe inside a warm session and an 83% volume
reduction could be a much smaller cost reduction, because most of what you
removed was being billed at a tenth of list price. Tokenamun reports both raw
volume and cache-weighted EIT precisely so this distinction cannot be lost
when someone repeats the experiment differently.

**Comparison across a series.** `tokenamun compare` takes two sessions or
checkpoints; a probe series is *n* of those in order, and `tokenamun series`
reads the saved profile JSON of each — median and range per step, and the
payback division against a measured intervention cost.

## What Tokenamun does not cover

**It won't run the experiment.** Applying refactoring steps, spawning the fresh
probe agent, discarding the change, iterating 15 times — that is orchestration,
and Tokenamun is a sensor. It measures a run that happened. Driving the runs is
a script or a harness, and keeping that boundary is what stops this becoming a
framework.

**It cannot tell you the probe succeeded.** He reports that Claude was poor at
identifying suitable refactorings without human guidance, and that the mechanical
refactoring scripts "frequently got confused by indentation". A probe that
produced a broken change consumes fewer tokens than one that worked. Tokenamun
supplies the token numerator and cannot supply the success denominator, so the
experiment still needs an outcome check — does the probe's change compile, pass
tests, actually implement the trait — recorded alongside. This is the
tokens-to-success discipline from [`METHODOLOGY.md`](METHODOLOGY.md#6-counterfactuals)
and it is the one thing a profiler can never do for you.

**It cannot rescue n = 1.** Each of his 15 data points is a single probe run.
Agents are stochastic: the same probe against the same code will not consume
the same tokens twice. A mechanical effect — fewer bytes in a file — is real at
n = 1. A *behavioural* effect — the agent choosing to read less — is not, and
"the agent explored less" is exactly what this experiment measures. An 83%
change is almost certainly signal; a 15% step between two adjacent refactorings
might well be noise, and the curve's shape is where the interesting claims
live. The useful discipline, borrowed from measurement work in this area, is
that mechanical effects are valid at n = 1 while behavioural ones want n ≥ 5
with medians and ranges reported rather than single values.

Tokenamun's contribution here is to make repeats cheap rather than to substitute
for them: if each probe run is one `tokenamun profile --json`, running five and
taking a median costs five probe runs and no extra analysis work. `tokenamun
series` reports median and range rather than a point value for exactly this
reason.

## Wiring it into a driver

Concretely, per probe run:

```bash
# before the run: what the code looks like
tokenamun scan --json > step-07-code.json

# ... driver applies refactoring step 7, runs the probe agent, discards ...

# after the run: what the probe actually consumed
tokenamun profile --json current > step-07-probe.json
```

and once per refactoring session, the cost side that the original experiment
had to leave as a bound:

```bash
tokenamun profile --json <refactoring-session> > step-07-cost.json
```

and once the series is complete, the medians and the payback division:

```bash
tokenamun series step-*-probe.json --cost <refactoring cost in EIT>
```

Three things worth building into the driver rather than hoping to reconstruct
later, all of which are cheap at the time and impossible afterwards:

1. **Record the outcome of every probe**, pass or fail, next to its token
   numbers. Without it the token series is uninterpretable.
2. **Repeat each probe** enough times to see the spread, and report medians.
3. **Keep the probe task byte-identical** across runs, and record it. A probe
   that drifts silently invalidates the whole series, and it is the easiest
   thing in this design to get wrong.
