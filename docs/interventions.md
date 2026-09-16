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

## Changing how work is structured

| intervention | verdict | what Tokenamun can say |
| --- | --- | --- |
| Subagents to protect orchestrator context | **partly measurable** | Parent-side cost is observed: the `Agent` call's returned report, and the carry avoided by not doing the exploration inline. Subagent-internal spend was **absent** from every session in the reference dataset despite `Agent` being called — reported as missing, never folded in. Without it, a claimed net saving is unverifiable, and Tokenamun says which half it has. |
| Knowledge graphs / code indexing to cut retrieval | **measurable** (the baseline) | We can measure precisely what the intervention claims to replace: how much content was retrieved to find things, by category, how much was re-retrieved, and what it cost to carry. The post-intervention side needs a second session to compare against — which is what `tokenamun compare` is for. |
| Shorter or restructured test output | **measurable** | Test-command output is identifiable from Bash command lines and is observed content with a known carry cost. Verbose test output is a well-formed target: it's large, it's repetitive, and it enters context late when carry is cheapest — Tokenamun will tell you whether that last part is true in your sessions rather than assuming it. |
| ADRs / specs to reduce exploration | **measurable, with a caveat** | Retrieval by category is derived, so "how much came from ADRs versus source" is answerable per session. Whether *introducing* them reduced exploration is a before/after question across sessions, not something one session can show. |
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

1. **Repeated retrieval** — observed, per-session, with a near-derived
   counterfactual. Also the one most likely to be free: nobody wants the same
   file three times.
2. **Cost of carry on large late-arriving content** — observed token cost,
   observed call count, arithmetic in between. Tells you *which* retrievals were
   expensive decisions rather than which were large.
3. **Preamble size** — observed exactly, bounded in decomposition. Cheap to
   check, and the multiplier by call count is usually the surprise.
4. **Tool output compression** — observed eligible volume, counterfactual
   saving, honest unknowns. The upside is real but the behavioural risk is the
   part nobody measures.
5. **MCP and tool-schema interventions** — plausible, possibly large, and
   **unmeasurable from this data**. If you want evidence here, the measurement
   has to happen at the request layer, not the transcript layer. That is a
   different tool, and saying so is more useful than a fabricated percentage.
