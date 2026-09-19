# Running experiments with Tokenamun

Written against the method in Giles Alexander's
[*Measuring the economic benefit of refactoring for AI agents*](https://martinfowler.com/articles/exploring-gen-ai/refactoring-economic-benefit.html),
because it is a real experiment with a real result and it exposes exactly which
parts a profiler can help with and which it cannot.

## The method it has to support

Three properties carry that design, and an experiment of this shape needs all
three: a **fixed probe task**, which holds the work constant so the codebase is
the only thing varying; a **discard** of each generated change, which keeps the
steps independent; and a **fresh sub-agent** per probe, which stops learning
leaking between runs. Fifteen refactoring steps, one probe after each, and
probe input tokens fell 83% while output stayed flat: the refactoring did not
make the change smaller, it made finding the place to make it cheaper.

## What Tokenamun covers

**The token measurement, without the estimation.** His counts came from a
chars ÷ 4 approximation, and he notes that "accurate token accounting proved
impossible." Per-call `input_tokens`, `cache_read_input_tokens`,
`cache_creation_input_tokens` and `output_tokens` are **observed** in the
transcript, straight from the API's own accounting: no tokenizer, no error bar,
and none of the ~70% inflation that per-entry summing costs.

**The code metrics, in the same tool.** Total lines and largest-file size are
two of his four metrics, and `tokenamun scan` produces both, plus duplication
and complexity.

**The cost of the refactoring itself — which is the gap.** He could only bound
it at "upper bound five million tokens", and without that number the economic
argument stays one-sided: the benefit per future change is known and the price
paid for it is not. That work happened in agent sessions, and those sessions
have transcripts, so the missing half is measurable:

```
payback (in future changes) = refactoring cost / saving per change
```

With his numbers and a 5M-token refactoring cost, the saving per change pays
back in about **38 changes** to that subsystem; at 1M it pays back in 8. The
distance between those two answers is why measuring the cost side beats
tightening the benefit side.

**Volume versus cost, which his design handles by accident.** A fresh
sub-agent per probe means a cold cache every run, so volume and cost move
together and his 83% volume reduction is close to an 83% cost reduction. That
is luck, not robustness: run the same probe inside a warm session and most of
what you removed was billed at a tenth of list price. Tokenamun reports both
raw volume and cache-weighted EIT so a repeat of the experiment cannot lose the
distinction.

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

**It cannot tell you the probe succeeded.** A probe that produced a broken
change consumes fewer tokens than one that worked. Tokenamun supplies the token
numerator and cannot supply the success denominator, so the experiment needs an
outcome check — does the change compile, pass tests, implement the thing —
recorded alongside. This is the tokens-to-success discipline from
[`METHODOLOGY.md`](METHODOLOGY.md#6-counterfactuals), and the one thing a
profiler can never do for you.

**It cannot rescue n = 1.** Each of his 15 data points is a single probe run,
and agents are stochastic. A mechanical effect — fewer bytes in a file — is
real at n = 1; a *behavioural* effect — the agent choosing to read less — is
not, and "the agent explored less" is exactly what this experiment measures. An
83% change is almost certainly signal; a 15% step between two adjacent
refactorings might well be noise, and the curve's shape is where the
interesting claims live. So behavioural effects want n ≥ 5, reported as medians
and ranges. Tokenamun makes repeats cheap rather than unnecessary — each run is
one `tokenamun profile --json` — which is why `series` reports a median and a
range rather than a point value.

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
