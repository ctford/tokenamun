# Entire as a data source: what we can actually observe

Research notes taken by inspecting real Entire data, not documentation. Entire
CLI **0.10.2**, 8 Claude Code sessions and 41 checkpoints recorded in a private Go monorepo
between 2026-08-25 and 2026-08-28. Sessions are referred to as S1–S8 and all
paths and identifiers below are placeholders; only the measurements are real.

Entire itself was not installed on this machine (no `entire` binary on `PATH`,
no `~/.entire`). Everything below was read directly off disk and out of git.
That is a useful constraint: **Tokenamun does not need the Entire CLI to be
installed, only its data.**

## Where the data lives

```
<repo>/.entire/
  metadata/<session-uuid>/
    full.jsonl        # native Claude Code transcript — the important one
    prompt.txt        # the session's prompt text
  logs/entire.log
  tmp/
```

Plus checkpoints, which are **git refs**, not files:

```
refs/entire/checkpoints/<last-2-chars-of-ulid>/<ulid>
```

Each ref is a commit whose tree contains:

```
metadata.json             # roll-up across the checkpoint's sessions
0/metadata.json           # per-session checkpoint metadata
0/full.jsonl              # transcript slice
0/transcript.jsonl        # "compact" transcript
0/content_hash.txt        # sha256 of transcript content
0/prompt.txt
```

Two commits per checkpoint: `Checkpoint: <ulid>` then
`Finalize transcript for Checkpoint: <ulid>`.

Reference dataset scale: 12,277 transcript lines, 21 MB, 41 checkpoints.

## `full.jsonl` is the native Claude Code transcript

Entire does not invent a format — it stores Claude Code's own JSONL. Entry
types in the reference dataset:

| `type` | count | notes |
| --- | --- | --- |
| `assistant` | 1,132 | one line per content block, **not** per API call |
| `attachment` | 724 | mostly `total_tokens_reminder` |
| `user` | 698 | user prompts and `tool_result` blocks |
| `system` | 208 | `turn_duration`, `stop_hook_summary`, `away_summary` |
| `file-history-snapshot` / `-delta` | 113 | edit tracking |
| other | — | `ai-title`, `permission-mode`, `mode`, `queue-operation`, `agent-name` |

Useful per-entry fields: `uuid` / `parentUuid` (turn threading), `timestamp`,
`requestId`, `isSidechain`, `gitBranch`, `cwd`, `toolUseResult`,
`toolUseID`, `promptId`, `effort`.

### The double-counting trap

**An `assistant` entry is a content block, not an API call.** The reference
session `S1` has 1,132 assistant entries but only **691 unique
`requestId`s**, and every entry sharing a `requestId` repeats the *same*
`message.usage` object verbatim.

Summing usage across assistant entries overstates by ~70%:

| | naive (per entry) | correct (per `requestId`) | overstatement |
| --- | --- | --- | --- |
| cache read | 494,206,381 | 289,418,574 | **+71%** |
| cache creation | 40,091,613 | 24,196,449 | **+66%** |
| output | 829,726 | 439,952 | **+89%** |

`message.id` and `requestId` agree exactly (691 each), so either works as the
deduplication key. **This is the single most important correctness rule in the
codebase.** It gets a test with a fixture that has repeated `requestId`s.

### Usage fields available

```json
{"input_tokens": 2, "cache_creation_input_tokens": 18862,
 "cache_read_input_tokens": 9766, "output_tokens": 596,
 "output_tokens_details": {"thinking_tokens": 273},
 "cache_creation": {"ephemeral_5m_input_tokens": 18862, "ephemeral_1h_input_tokens": 0},
 "server_tool_use": {"web_search_requests": 0, "web_fetch_requests": 0},
 "service_tier": "standard", "speed": "standard", "iterations": [...]}
```

All four token classes the spec asks for are present, plus thinking tokens and
5m/1h cache-TTL split. `iterations[]` appears when a request retried internally.

## Checkpoint metadata

`0/metadata.json` is rich and mostly trustworthy:

```json
{"checkpoint_id": "01CKPT000000000000000001",
 "session_id": "<uuid>", "agent": "Claude Code", "model": "claude-opus-5",
 "created_at": "2026-08-27T10:48:15Z", "branch": "main", "turn_id": "<hex>",
 "files_touched": ["docs/plans/example-plan.md", "..."],
 "checkpoint_transcript_start": 2618, "transcript_lines_at_start": 2618,
 "compact_transcript_start": 554,
 "token_usage": {...}, "session_metrics": {"turn_count": 71},
 "skill_events": [...], "initial_attribution": {...}, "prompt_attributions": [...]}
```

Genuinely valuable, and not available anywhere else:

* **`files_touched`** — ties token spend to the files a change actually landed in.
  This is what makes "changes in subsystem X cost 2.3× more exploration" possible.
* **`skill_events`** — explicit, confidence-tagged skill invocations with
  `transcript_anchor` line ranges. Better evidence than sniffing for the `Skill`
  tool, and it is marked `confidence: "explicit"` by Entire itself.
* **`initial_attribution`** — agent vs human lines added/removed/modified.
* **`checkpoint_transcript_start`** — a line offset into the session transcript.
* **`agent`** — the seam for supporting non-Claude-Code agents later.

### `token_usage` on a checkpoint is not safe to sum

This is the trap the spec warns about, and it is real. Of 41 checkpoints:

* 28 have a `checkpoint_transcript_start` and a small `token_usage` that behaves
  like a **delta** for that checkpoint.
* 13 have **no** `checkpoint_transcript_start` (and `compact_transcript_start: 0`)
  and a `token_usage` that is **cumulative from session start**.

Session `S2` is the pathological case: ten consecutive checkpoints with no
offset and monotonically growing counts —

```
cache_read: 40,517,851 → 63,118,436 → 95,467,291 → 111,851,124 → 121,048,279
          → 152,355,366 → 170,798,681 → 191,046,373 → 221,954,982 → 300,123,650
api_calls:         201 →         262 →         339 →         373 →         390 → ...
```

Summing those ten gives ~1.47 **billion** cache-read tokens for a session whose
transcript accounts for 443 million. All 41 checkpoints report the same
`cli_version`, so version-sniffing won't save us and there is no field that
declares which semantics apply.

**Design consequence, and it is load-bearing:** Tokenamun derives *all* token
accounting from `full.jsonl` (deduplicated by `requestId`) and never from
checkpoint `token_usage`. Checkpoints are used for slicing, git attribution,
`files_touched` and skill events only. Where a checkpoint lacks an offset, the
slice boundary is unknown and the CLI says so rather than guessing.

## Tool calls and tool results

Tool calls are `tool_use` blocks in assistant content; results arrive as
`tool_result` blocks in the following user message, **and** in a parallel
`toolUseResult` field on that entry. `toolUseResult` is the richer of the two:

| shape | count | tool |
| --- | --- | --- |
| `stdout`,`stderr`,`interrupted`,`isImage`,`noOutputExpected` | 470 | Bash |
| `content`,`filePath`,`originalFile`,`structuredPatch`,`userModified` | 34 | Write |
| `filePath`,`oldString`,`newString`,`structuredPatch`,`replaceAll` | 12 | Edit |
| `file: {content, filePath, numLines, startLine, totalLines}` | 3 | Read |
| `persistedOutputPath`,`persistedOutputSize` | 5 | Bash, output spilled to a file |

**The spec's "don't count the whole file if only a range was read" requirement is
directly satisfiable:** Read results carry `startLine`, `numLines` and
`totalLines`, so we know exactly which fragment was returned. Edit/Write carry
`structuredPatch`, so we can size a change without sizing the file.

Two truncation artefacts to respect: Bash `stdout` is capped (many results land
at exactly 30,000 chars), and large outputs are spilled to
`persistedOutputPath` with only a size in the transcript. Both must be reported
as truncated rather than counted as complete content.

### Tool mix in the reference dataset

| tool | calls |
| --- | --- |
| Bash | 1,607 |
| Edit | 85 |
| Write | 81 |
| Read | 35 |
| ToolSearch / Agent / AskUserQuestion | 9 / 9 / 7 |
| Monitor / Skill / others | 6 / 4 / 10 |

**Zero `mcp__*` tool calls across all 8 sessions.** MCP-to-CLI analysis has no
test data here — see [`COMMON-INTERVENTIONS.md`](COMMON-INTERVENTIONS.md).

Bash at 87% of calls is a property of *this* dataset (the repo runs Claude Code
in auto mode, which pushes file reads through `cat`/`sed`). It matters for
design: a classifier that keys off `Read`/`Grep`/`Glob` tool names would see
almost nothing. Retrieved-content classification has to parse Bash command
lines for the paths they touch.

## Retrieved content, measured

Unique `tool_result` payload bytes per session, with the share that is
byte-identical to content retrieved earlier in the same session:

| session | tool-result bytes | repeated-identical | largest contributor |
| --- | --- | --- | --- |
| `S1` | 1,238,793 | **24.1%** | Read 636 KB in 3 calls (avg 212 KB) |
| `S2` | 667,981 | 1.6% | Bash 596 KB in 667 calls |
| `S3` | 317,817 | 0.2% | Bash 226 KB in 181 calls |
| `S4` | 86,212 | 0% | Bash only |
| `S5` | 79,950 | 0% | Bash only |
| `S6` | 76,540 | 0% | Bash only |
| `S7` | 72,088 | 0% | Bash only |
| `S8` | 20,876 | 0% | Bash only |
| **total** | **2,560,257** | | |

Content hashing works and finds something real: one session re-retrieved 298 KB
of identical content. The other sessions are near-clean, which is itself a
finding — repeated retrieval is a *session-shaped* problem, not a universal one,
so this analysis needs to be run per session rather than averaged.

## The thing that reframes the whole tool

Observed, deduplicated, across all 8 sessions:

| | tokens |
| --- | --- |
| Billed-equivalent input (`input` + `cache_creation` + `cache_read`) | **856,400,609** |
| Output | 1,537,275 |
| API calls | 2,040 |
| Unique tool-result content (≈ bytes/3.6) | ~711,000 |

Roughly **711K tokens of content sat behind 856M tokens of billed input.**

The lever is not the size of what gets retrieved. It is how long it stays
resident and how many calls re-send it. A 5K-token read at call 20 of `S1` is
re-sent over its remaining 671 calls: 3.4M tokens of volume, ~336K
cost-weighted while the prefix stays warm at 0.1×.

This is derivable, not speculative, because **the prompt size of every API call
is observed**: `input_tokens + cache_read_input_tokens +
cache_creation_input_tokens` is exactly what the request cost. The trajectory
for session `S1`:

```
call    0: 28,630      <- session preamble: system prompt + tool schemas + AGENTS.md + skills
call    1: 31,643
call    5: 37,296
call  507: 0           <- API error entry (model "<synthetic>"), must be excluded
call  690: 853,459
```

Two things fall straight out of this.

**1. The session preamble is observed, and it is paid on every call.** The first
call's prompt size is everything that exists before any work happens. For
`S1` that is 28,630 tokens × 691 calls ≈ **19.8M tokens of billed input
just carrying the preamble**. We cannot decompose it (no system prompt or tool
schemas in the transcript), but we can measure it exactly and multiply. That is
the honest ceiling for every "reduce the instructions/tool schemas" intervention.

**2. Context growth can be attributed to the tool calls that caused it.** The
delta between consecutive prompt sizes is the tokens added in between. Validated
against session `S5` (22 calls), with content estimated at 3.6 bytes/token:

```
  k    prompt    delta  prev_output  result_bytes  explained  residual
  1    40,954    5,025          765        13,741      4,582       443
  2    46,924    5,970          687        13,634      4,474     1,496
  4    55,278    5,253        4,791           970      5,060       193
 13    81,737    3,461        2,088         3,014      2,925       536
total growth 80,270   explained 69,500   ratio 0.87
```

**87% of observed context growth is explained by observed output tokens plus
observed tool-result content.** The residual is system-reminders, attachments
and message-envelope overhead — reportable as `unattributed` rather than
silently distributed.

This gives per-item token costs that come from the API's own accounting rather
than from a tokenizer, which is a much stronger epistemic position than
tokenizing content and hoping. It also makes the local tokenizer
*self-calibrating*: fit bytes-per-token by minimising the residual against
observed deltas, per session.

## Tokenizer

There is no public Claude tokenizer, so token counts for *content* come from
the bytes-per-token estimator calibrated above, labelled `derived-approx` with
its calibration residual printed. Good enough for ranking, never presented as
exact. Why not `tiktoken` is in the `internal/tokens` package comment.

## Observed / derived / inferred, summarised

**Observed** — in the telemetry, no interpretation:
per-API-call `input`/`output`/`cache_read`/`cache_creation`/`thinking` tokens;
prompt size per call; API call count; model per call; tool names, inputs, ids,
timestamps; tool-result payloads and their truncation flags; Read line ranges;
`structuredPatch` diffs; user prompts; `files_touched`; agent-vs-human line
attribution; explicit skill invocations; git branch and commit; permission mode
and `effort`.

**Derived** — deterministic arithmetic over the above:
deduplicated session totals; per-call context deltas and their attribution to
tool calls; preamble carry cost; content hashes and repeated-retrieval volume;
estimated token counts of content; per-channel and per-command retrieval
totals; cost-of-carry per retrieval; residency spans.

**Inferred** — a classifier's opinion, always labelled:
which Bash invocations were retrieval rather than mutation, and the file paths
parsed out of their command lines. Activity phases and a semantic content
taxonomy were both candidates here; neither was built, and the reasons are in
[`METHODOLOGY.md`](METHODOLOGY.md#7-what-it-cannot-measure) and the
`internal/content` package comment.

**Not available at all** — say so, don't estimate:
system prompt, tool schemas and skill-definition sizes; the
`CLAUDE.md`/`AGENTS.md` share of the preamble; exact context-window residency
after Claude Code's own compaction/context-editing; subagent internal token
usage when sidechains are absent; anything about what the agent *would* have
done differently.
