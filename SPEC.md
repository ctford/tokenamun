Build an open-source CLI tool called **Tokenamun**.

## Product idea

Tokenamun is a profiler for coding-agent token usage.

Its purpose is to give engineering teams evidence about where coding-agent tokens are being consumed, and to help them evaluate whether proposed token/context optimisations would actually help.

The primary interaction should eventually be that a developer can ask their coding agent questions such as:

* Where did the tokens go in this session?
* How much was spent planning versus implementing versus verifying?
* How much content did I retrieve from source code versus ADRs versus documentation?
* Which files or tool outputs contributed most?
* Did I repeatedly retrieve the same information?
* Would Caveman-style compression have helped? How much?
* Would wrapping an MCP server behind a CLI have helped?
* Why was this change more token-expensive than another one?
* What should we investigate optimising?

Tokenamun should provide the **measurements and evidence** for these answers. The coding agent can provide interpretation.

A useful analogy is a CPU, memory, or disk-space profiler for coding agents.

## Important epistemic principle

Tokenamun must distinguish between:

* **Observed** — directly present in the underlying telemetry/transcript.
* **Derived** — deterministically calculated from observed data, e.g. tokenizing retrieved content.
* **Inferred** — classified or estimated, e.g. deciding that a sequence of turns represents planning.
* **Counterfactual** — estimating what would have happened under an intervention.

Never present inferred or counterfactual values as measurements.

This is important. Tokenamun should be useful for engineering experiments rather than manufacture false precision.

## Data source

For v0.1, use **Entire** as the system of record.

Research Entire's current open-source CLI, storage format, session/checkpoint model, and native transcript storage before designing the implementation.

Do not build another session-capture mechanism unless absolutely necessary.

Tokenamun should read existing Entire data locally.

Pay particular attention to:

* sessions
* checkpoints
* `full.jsonl` / native agent transcripts
* token usage
* tool calls and tool results
* subagents
* Git commits/checkpoint attribution
* cumulative token accounting and the risk of double-counting checkpoints

Start with Claude Code sessions, but design the internal model so other Entire-supported coding agents can be added later.

## Core data model

Design a normalized internal representation around concepts such as:

* Session
* ModelInvocation
* Activity
* ToolCall
* RetrievedContent
* Artifact
* GitChange
* TokenUsage

For token usage, preserve distinctions where available between:

* input
* output
* cache read
* cache creation

For retrieved content, record:

* source
* source type
* byte/token size
* tool responsible
* file path if applicable
* line/range information if available
* timestamp / sequence
* content hash

Content hashes should allow Tokenamun to detect repeated retrieval of identical content.

## Content classification

Classify observed retrieved content into useful categories such as:

* source code
* tests
* ADRs
* specifications
* documentation
* plans
* tool output
* MCP output
* instructions/guides
* other

Prefer deterministic rules where possible.

For example, ADR classification can initially use paths/filenames such as `adr/`, `adrs/`, `architecture-decision*`, etc.

Make classification extensible/configurable.

Do not count an entire file if the transcript shows that only a range or fragment was returned. Tokenize the content actually observed.

## Activity classification

Provide an initial activity taxonomy:

* orientation
* planning
* research
* implementation
* debugging
* verification
* review
* other

Activity is generally **inferred**, not observed.

Start conservatively. Prefer `other` or `mixed` rather than assigning unjustified precision.

Classification should use evidence such as:

* Claude Code modes if observable
* tool calls
* reads/searches
* edits/writes
* test execution
* shell commands
* subagent activity
* prompts/responses

Keep the classifier replaceable so more sophisticated approaches can be introduced later.

## CLI

The CLI itself is the primary product.

It should be pleasant for both humans and coding agents to invoke.

Start with commands along these lines:

```bash
tokenamun profile [session]
tokenamun retrieval [session]
tokenamun activities [session]
tokenamun compare <session-or-checkpoint-a> <session-or-checkpoint-b>
tokenamun what-if <intervention> [session]
```

Support machine-readable JSON output throughout, for example:

```bash
tokenamun profile --json
```

Claude Code should be able to invoke Tokenamun and reason over the structured output.

## `tokenamun profile`

Produce a concise overview such as:

```text
TOKENAMUN

Session
  Model input        ...
  Cache read         ...
  Cache creation     ...
  Output             ...

Activity
  Planning           ...
  Implementation     ...
  Verification       ...
  Research           ...
  Unclassified       ...

Retrieved content
  Source code        ...
  Tests              ...
  ADRs               ...
  Documentation      ...
  Tool output        ...
  Other              ...

Largest retrievals
  ...

Repeated retrieval
  ...
```

Be precise about units.

Do not imply that "retrieved tokens" are identical to billed model-input tokens.

## Treemap

One of Tokenamun's signature views should eventually resemble tools such as disk-space visualizers.

Rectangle area represents **observed retrieved content tokens**, not estimated context-window residency.

Allow hierarchical exploration such as:

```text
all retrieved content
    source code
        src/payment.ts
        src/retry.ts
    tests
    ADRs
        ADR-0042.md
    documentation
    tool output
```

For v0.1, investigate the simplest useful implementation. A standalone HTML report is acceptable.

Do not mislabel this as an exact visualization of the model context window.

## Counterfactual analysis

Design a plugin/strategy abstraction for "what-if" analyses.

The intended interaction is:

> Would optimisation X have helped in this session? How much?

A what-if result should explicitly separate:

```text
observed
derived
counterfactual
unknown
```

For example:

```text
Intervention: output-compression

Observed:
  applicable_output_tokens: 186421

Counterfactual:
  compressed_tokens: 61203
  potential_reduction: 125218
  potential_reduction_pct: 67.2

Unknown:
  behavioural_change
  additional_tool_calls
  recovery_requests
```

Do not turn a local reduction into a claim about whole-session savings unless the evidence supports it.

## Caveman

Investigate the current Caveman project and determine whether its compression mechanism can be invoked or reproduced locally against historical tool outputs.

If practical, implement:

```bash
tokenamun what-if caveman
```

The strongest implementation would replay observed eligible outputs through Caveman's actual compression mechanism and compare token counts.

Report:

* observed eligible content
* original token count
* compressed token count
* local potential saving
* which content categories contributed
* what cannot be known retrospectively

Do not claim that the whole session would have consumed exactly that many fewer tokens. Agent behaviour could have changed.

If integrating Caveman directly is inappropriate for v0.1, design the interface and provide a clearly labelled prototype estimator.

## MCP-to-CLI analysis

Investigate whether Entire/Claude Code transcripts expose enough information about MCP tools and their usage to estimate the opportunity from putting an MCP server behind a CLI.

The intended future command is something like:

```bash
tokenamun what-if mcp-to-cli github
```

Useful evidence might include:

* MCP tools available
* tool/schema size where observable
* number of tools actually used
* frequency of use
* results returned
* potential CLI equivalents

Be conservative where schema exposure cannot be measured.

## Team optimisation

Do not design Tokenamun as a developer leaderboard.

Dimensions such as developer, repository, model and team may eventually be useful for identifying variation, but Tokenamun's purpose is to improve the engineering system.

Prefer findings such as:

> Changes in subsystem X require 2.3× more code exploration than comparable changes elsewhere.

over:

> Developer X uses 2.3× more tokens than Developer Y.

Tokenamun is a **sensor**, not a judge.

## Experiments

Design the architecture with future before/after experiments in mind.

Eventually we should be able to evaluate interventions such as:

* compressing tool output
* MCP → CLI
* shorter/modular guides
* introducing ADRs/specifications
* changing retrieval strategies
* changing test output
* introducing skills
* changing models

We want to distinguish:

1. Did the intervention reduce the thing it targeted?
2. Did agent behaviour change in response?
3. Did overall resource consumption change?
4. Did engineering outcomes remain acceptable?

Do not build a large experiment framework in v0.1, but avoid architectural choices that make this difficult later.

## Scope discipline

Keep v0.1 small.

The first useful milestone is:

> Point Tokenamun at an existing Entire-recorded Claude Code session and understand where the session's token usage and retrieved content went.

Prioritize:

1. Understanding Entire's actual data format.
2. Correct parsing and token accounting.
3. Retrieved-content classification.
4. Conservative activity attribution.
5. Useful CLI output.
6. JSON output.
7. Tests against real/representative Entire sessions.

Then implement one useful counterfactual analysis if feasible.

Avoid initially building:

* SaaS infrastructure
* accounts/authentication
* centralized telemetry
* elaborate dashboards
* custom agent instrumentation
* exact context-window reconstruction
* developer productivity scoring

## Engineering approach

Before writing significant implementation code:

1. Research Entire's current repository and documentation.
2. Research Caveman's current implementation.
3. Inspect locally available Entire data if present.
4. Document what can be **observed**, **derived**, and only **inferred** from Entire.
5. Identify the minimum architecture.
6. Write a short implementation plan.
7. Then implement incrementally with tests.

Where assumptions about Entire's format are necessary, isolate them behind an adapter and document them.

Prefer a simple, maintainable implementation over a framework-heavy one.

## README positioning

Use this provisional positioning:

# Tokenamun

**A profiler for coding-agent token usage.**

Tokenamun uses agent session data to show where tokens go, what work they support, and whether proposed optimisations might actually help.

Ask questions like:

* Where did my tokens go?
* How much did I spend exploring code?
* How much content came from ADRs?
* What did verification cost?
* What did I retrieve repeatedly?
* Would compressing tool output have helped?
* Would moving an MCP server behind a CLI have helped?

Tokenamun provides evidence. You decide what to optimise.

## First deliverable

Produce:

1. A short research summary of Entire and the available evidence.
2. The proposed architecture.
3. The v0.1 scope.
4. An implementation plan.
5. Then build the working v0.1 CLI.
6. Include automated tests and example output.
7. Run it against a real Entire session if one is available locally and report what you learn.

Do not stop after producing the plan unless there is a genuine blocker. Proceed through implementation and validation.

