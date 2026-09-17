# Which optimisations can Tokenamun produce evidence for?

A lot of token-optimisation advice is circulating, and most of it arrives as a
plausible mechanism plus a vendor's percentage. This is the list of candidate
interventions we want to be able to say something *measured* about, and — more
importantly — an honest statement of which ones this data cannot settle.

Verdict column:

| verdict | meaning |
| --- | --- |
| **measurable** | Tokenamun can quantify the target from observed data |
| **boundable** | We can put a ceiling or a floor on it, not a value |
| **detectable** | We can tell you whether the pattern occurred, not what it cost |
| **not from this data** | The evidence isn't in the transcript. Don't ship a number. |

## Compressing what enters the context

| intervention | verdict | what Tokenamun can say |
| --- | --- | --- |
| Compress tool output (Caveman, Headroom, RTK-style proxies) | **measurable** (eligible volume) + **counterfactual** (saving) | Observed eligible output per tool and category; compressed size under a stated model, or under a real compressor via `--replay-with`; the saving multiplied by cost-of-carry. Never the session total. |
| Eliminate repeated retrieval | **measurable** | Byte-identical content retrieved more than once, by content hash. Strongest counterfactual we have: retrieving it once is arithmetic, not modelling. One reference session spent 24% of its tool-result bytes re-fetching identical content; five others spent 0%. |
| Deliberate context compression (in-place, mid-session) | **boundable** | The same eligible-volume measurement, but the *decision* of what is still relevant is exactly what we can't reconstruct. We can show what a retention policy would have dropped; we can't show what the agent then needed back. |
| Compact JSON representations (TOON and similar) | **measurable** for eligible volume | How much observed tool output is uniform structured data, per tool. The re-encoding saving is a counterfactual on the actual payloads. |

## Reducing what is loaded up front

| intervention | verdict | what Tokenamun can say |
| --- | --- | --- |
| Trim agent instructions (`CLAUDE.md`/`AGENTS.md` bloat) | **boundable** | The session preamble is observed exactly — the first call's prompt size, paid again on every call. In one reference session that was 28,630 tokens × 691 calls ≈ 19.8M billed input tokens. We cannot say what share is your instructions versus the system prompt versus tool schemas, and we won't guess. The ceiling is still a useful number: it tells you whether the whole category is worth an hour of your time. |
| Deferred tool loading / tool search | **not from this data** | Tool schemas are not in the transcript. The saving lives entirely inside the preamble we can't decompose. We can count tools *used* versus tools *available* (from the harness's own listing entries when present), which tells you whether the mechanism has anything to bite on — but not its token value. |
| Skill in front of MCP | **not from this data** | Same reason, twice over: schema sizes unobservable, and the reference dataset contains zero `mcp__*` calls. `tokenamun what-if mcp-to-cli` ships as a stub that says so. |
| Code Mode MCP / code execution over MCP | **detectable** | We can identify the pattern the technique targets — large intermediate payloads that arrive in context and are then echoed back out in a subsequent tool input. Tool inputs are observed (537 KB of them in one reference session), so the round-trip is visible. Sizing the fix needs a counterfactual about code the agent never wrote. |
| Fewer, smaller skills | **detectable** | Entire records explicit, confidence-tagged skill invocations, so we can report which skills actually fired. Definition sizes are on disk, not in the transcript, so the cost is a local file measurement rather than an observation. |

## Cache hygiene

Absent from the radar blips entirely, and the largest measured lever in our
reference dataset. Anthropic prompt caching prices reads at 0.1× and writes at
1.25× (5-minute TTL) or 2× (1-hour), so when a prefix expires the whole
conversation is rewritten at the write rate.

| intervention | verdict | what Tokenamun can say |
| --- | --- | --- |
| Longer cache TTL (`promptCacheTtl: 1h`) | **measurable** + **counterfactual** | Which TTL each call actually used is observed (`ephemeral_5m` vs `ephemeral_1h`). Misses are attributed per cause, so the expiry share is derived rather than assumed. On the reference dataset TTL expiry was **40% of the effective input bill**, and a 1-hour TTL nets −29.4% after charging the doubled write price. It can also come out negative on short-burst sessions, which is the point of computing it. |
| Avoiding mid-session cache invalidation | **measurable** | Model switches, effort changes, compaction and Claude Code upgrades are each observable and separately priced. "Your four `/model` switches cost X" is a derived number, and `opusplan` makes every plan-mode toggle a model switch. |
| Shorter sessions / fewer long idle gaps | **measurable** | Expiry cost scales with prefix size, so late expiries are the expensive ones. Tokenamun can show the cost of each expiry against where in the session it happened. |
| Shrinking the prefix (instructions, tool output) | **re-valued upward** | Expiry cost is (expiries) × (prefix size at expiry). Compression's biggest effect is not the 0.1× reads it avoids but the 1.25× rewrites it shrinks — which is not how compression is usually sold. |

## Changing how work is structured

| intervention | verdict | what Tokenamun can say |
| --- | --- | --- |
| Subagents to protect orchestrator context | **partly measurable** | Parent-side cost is observed: the `Agent` call's returned report, and the carry avoided by not doing the exploration inline. Subagent-internal spend was **absent** from every session in the reference dataset despite `Agent` being called — reported as missing, never folded in. Without it, a claimed net saving is unverifiable, and Tokenamun says which half it has. |
| Knowledge graphs / code indexing to cut retrieval | **measurable** (the baseline) | We can measure precisely what the intervention claims to replace: how much content was retrieved to find things, by category, how much was re-retrieved, and what it cost to carry. The post-intervention side needs a second session to compare against — which is what `tokenamun compare` is for. |
| Shorter or restructured test output | **measurable** | Test-command output is identifiable from Bash command lines and is observed content with a known carry cost. Verbose test output is a well-formed target: it's large, it's repetitive, and it enters context late when carry is cheapest — Tokenamun will tell you whether that last part is true in your sessions rather than assuming it. |
| ADRs / specs to reduce exploration | **measurable, with a caveat** | Retrieval nests by directory, so "how much came from `docs/decisions` versus `services/`" is answerable per session. Whether *introducing* them reduced exploration is a before/after question across sessions, not something one session can show. |
| Model choice | **measurable** | Model per API call is observed, so per-model token and call distributions are available. Cost requires a price table, which is configuration, not measurement. |

## Anti-pattern to avoid building

**Token spend as a productivity metric.** This is a known and well-documented
anti-pattern — tokens are an input, not an outcome, and treating them as the
latter is the lines-of-code vanity metric with a new unit. It has produced real
damage: at least one gamed internal leaderboard and at least one AI budget
exhausted a third of the way through the year.

It is also the failure mode this tool is closest to. A profiler that reports
"tokens per developer" would be worse than no profiler. Hence:

* No developer dimension in any command's output, including `hotspots`.
* Findings are framed against the engineering system — subsystems, file
  properties, retrieval patterns — not against people.
* Reductions are never reported as improvements without the outcome question
  attached. Spending fewer tokens to do worse work is not a win, and Tokenamun
  cannot see work quality, so it must not imply that it can.

## What this means for where to start

Ranked by evidence quality rather than by claimed upside, which is the inversion
this whole document exists to make possible:

1. **Cache hygiene.** Observed TTL, per-cause miss attribution, and the largest
   number we measured by a wide margin: 40% of the effective input bill went on
   re-creating prefixes that expired while someone was thinking. One setting,
   already available in the Claude Code version those sessions ran. Nothing on
   the radar comes close on this dataset, and nobody is talking about it.
2. **Repeated retrieval** — observed, per-session, with a near-derived
   counterfactual. Also the one most likely to be free: nobody wants the same
   file three times.
3. **Cost of carry on large late-arriving content** — observed token cost,
   observed call count, arithmetic in between. Tells you *which* retrievals were
   expensive decisions rather than which were large.
4. **Preamble size** — observed exactly, bounded in decomposition. Cheap to
   check, and the multiplier by call count is usually the surprise.
5. **Tool output compression** — observed eligible volume, counterfactual
   saving, honest unknowns. The upside is real but the behavioural risk is the
   part nobody measures.
6. **MCP and tool-schema interventions** — plausible, possibly large, and
   **unmeasurable from this data**. If you want evidence here, the measurement
   has to happen at the request layer, not the transcript layer. That is a
   different tool, and saying so is more useful than a fabricated percentage.

## Writing your own intervention

An intervention is an executable. Tokenamun ships seven, and none of them can
do anything an extension cannot: they see the same evidence, they are held to
the same rules, and they appear in the same table. If you want to ask a
question this tool does not ask, that is a fifty-line script, not a fork.

Put it in `~/.config/tokenamun/interventions/` (or name it with
`--intervention PATH`, or list directories in `TOKENAMUN_INTERVENTIONS`), make
it executable, and `tokenamun interventions` will list it.

Deliberately *not* searched: the repository being analysed. Tokenamun is
routinely pointed at a checkout you did not write — that is most of what it is
for — and a tool that executes scripts it finds in the subject of its analysis
runs a stranger's code because you asked a question about their tokens.

### The protocol

Two invocations of the same program, JSON on stdout both times:

```
script describe              → {"name": "...", "description": "..."}
script estimate < evidence   → a result document
```

Anything on stderr goes to the terminal, so you can log freely. A non-zero
exit, unparseable output or a rule violation becomes a row saying the
intervention failed — it does not take down the rest of the report.

### The evidence document

Exactly what a built-in sees, which is the point of the interface:

| field | what it is |
| --- | --- |
| `schema_version` | `1`. Refuse a version you do not know rather than guess at a moved field. |
| `session` | the parsed transcript: `invocations` (one per API call, with `usage`), `retrievals`, `repeats`, `prompt_entries`, `token_estimator` |
| `cache` | why each prefix rebuild happened, attributed to a cause, and what it cost |
| `carry` | what it cost to *keep* content rather than fetch it: `prompt_cost_eit`, `preamble_tokens`, `items` |
| `weights` | this model's token-class multipliers. Use these rather than hardcoding prices, so your row is comparable with the others. |
| `compression_ratio` | the assumed surviving fraction, from `--ratio`. If you assume a ratio, assume this one. |
| `replay` | present when `--replay-with` measured real compression. Prefer it over the assumption. |

`tokenamun what-if cache-ttl --json` prints a result in the shape yours must
take. `examples/interventions/thinking-carry` is a complete, commented one in
about sixty lines of Python.

### The rules, which are enforced

The three rules at the top of `internal/whatif/whatif.go` are checked on what
your script returns, and a result that breaks one is rejected rather than
printed:

- **`unknown` may not be empty.** Every counterfactual must say what it cannot
  know, starting with whether the task still succeeded. An agent that fails
  consumes the fewest tokens of all, so a reduction is not an improvement.
- **A headline needs a caveat.** If you nominate a number as your bottom line,
  `caveat` is the sentence printed beside it. An unqualified percentage is
  precisely how the published claims went wrong.
- **`applicable: false` needs `not_measurable`.** Saying nothing can be said is
  a legitimate result; saying it without saying why is not.
- **Provenance must be honest.** Every quantity carries `observed`, `derived`,
  `derived-approx`, `inferred` or `counterfactual`, and anything in the
  `counterfactual` section must be labelled as one. This is what stops a guess
  being laundered into a measurement.

Units are `tokens`, `eit`, `bytes`, `calls` or `ratio`. `eit` is a
cost-weighted token: every token class on one scale where 1 is a full-price
input token of this model.
