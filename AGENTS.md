# AGENTS.md

Guidance for coding agents working in this repository.

Tokenamun is an **experimental** profiler for coding-agent token usage.
[`docs/METHODOLOGY.md`](docs/METHODOLOGY.md) is canonical on how every number is
computed; [`docs/ENTIRE.md`](docs/ENTIRE.md) on what the
data contains. Read the latter before touching an adapter — most non-obvious
decisions there follow from something measured in it.

`plans/` is where a change is argued before it is made, and it holds only
work that is not built yet. Each file opens with a `Status:` line, and a
decision taken along the way goes in a `**Decided:**` blockquote.

**Delete a plan when it ships.** It is a scratchpad, not a record: a
directory of finished plans is a second description of the code that
nothing keeps true, and one plan is made stale by the next one building
what it called missing. Git history has the file if anyone wants it.

The condition on deleting is that the reasoning has somewhere else to live
first. An alternative that was rejected, and what it would have cost, is
the part no diff keeps — so it belongs in the doc comment beside the code
that took the other road, in `docs/METHODOLOGY.md` if it is about how a
number is computed, or in the commit message if it is about the change
rather than the result. Move it, then delete the plan. A decision that
exists only in `plans/` is one deletion away from being lost.

## Everything committed here is published publicly

Treat every commit as public the moment it is made. Commit messages included.

* **No data from the repositories we profile.** No transcripts, no `.entire/`
  directories, no prompts, no tool output, no source from another project,
  including inside documentation and tests.
* **Never name a profiled project** — not in code, documentation, a fixture,
  or a commit message.
* **Never attribute a measurement to a project.** A number measured from
  private work does not become publishable by being an aggregate: "131
  sessions and 1.5B cost-weighted tokens" is that team's week, and the name
  beside it hands over their volume, headcount and bill. Keep the lesson,
  drop the measurement.
* **No identifiers from private work**: session UUIDs, checkpoint ULIDs,
  paths, branch names, commit SHAs, author names. No credentials.
* **This repository's own sessions are fine.** The distinction is whose data
  it is, not how large the number is.

`.gitignore` is a backstop, not the control. If something private is already
committed, say so rather than fixing it forward: it needs history rewriting
before any push.

### The guard contains no private names

`scripts/leakscan.sh` scans tracked file contents and every commit message.

A public repository carrying a denylist of client names publishes the list it
exists to protect. So the built-in patterns are generic shapes — a real home
directory, a path out into a named sibling checkout — and specific names live
in `.private-names`: one regex per line, gitignored, a failure if ever
tracked. Without it the generic rules still run. It never prints what it
matched, because that copies the secret into the CI log.

**The name check is local by construction, so it is the pre-commit hook that
enforces it.** CI has no `.private-names` — that is the point of the file —
so there it prints `generic checks only` and the commit-message half does not
run at all. A name that reaches a push has already passed the only gate that
could have seen it. Don't read a green CI run as the names having been
checked; install the hook (`scripts/install-hooks.sh`) and keep the file
current.

`scripts/test-leak-guard.sh` exercises it against strings it must catch and
must not, under the shell options the gates use. Keep it that way: a guard
whose patterns quietly match nothing reports success, so the only evidence it
works is a case where it fails.

### Fixtures are synthetic

Real transcripts teach you the format's *shape*; they do not go in
`testdata/`. Write fixtures by hand for a specific shape — repeated
`requestId`s, a partial Read, a truncated Bash result — with filler content
sized to the counts a test asserts on. Never commit a fixture copied from a
real transcript, even partially, even in a comment.

## The four rules the tests protect

1. **Every number carries a provenance label** — `observed`, `derived`,
   `derived-approx`, `inferred`, `counterfactual` or `given`. It is a type,
   not a comment, and the renderer cannot print an unlabelled number.
2. **Token accounting comes from the transcript, deduplicated by `requestId`.**
   An `assistant` entry is a content block, not an API call. Never derive
   totals from checkpoint `token_usage`: it is cumulative in some checkpoints
   and a delta in others, with nothing to distinguish them.
3. **Retrieved-content tokens and billed tokens are different quantities**, and
   are never added together.
4. **Volume is not cost.** Rankings are in cost-weighted tokens, priced at the
   cache class each re-send was actually billed at. Raw volume may appear
   alongside; never alone, and nothing is called expensive on volume alone.

Changing one of these means changing [`docs/METHODOLOGY.md`](docs/METHODOLOGY.md) in the
same commit.

## Conventions

* **Go only.** One static binary, no runtime dependencies. `go.mod` may carry
  pinned `tool` dependencies for the checks; nothing shipped imports them.
* **Stream, don't slurp.** Transcripts reach 9 MB. Nothing loads a whole one.
* **The adapters are a quarantine.** Only `internal/entire` and
  `internal/claudecode` may know a field name from someone else's format.
  Everything downstream consumes `internal/model`. New format knowledge goes
  in the adapter's package comment, beside the assumption it sits with and a
  note on how we detect it breaking; `tokenamun version` names the versions
  the adapters were read off.
* **Output is terse; the argument goes where there is room.** No paragraphs in
  a table cell, tooltip, legend, or beside a number. Say the one thing in a
  sentence and put the reasoning in the field that exists for it — `caveat`
  with `caveat_detail`, `Detail` with `DetailMore`, the legend with
  `tokenamun tree`. Caveats are capped at 64 characters, tooltips at 280. A
  node's description is a definition: what is in the box, not why it matters.

  Terse is not silent: a short caveat must still be a claim — "A ceiling, not
  an estimate." — because a one-word label qualifies nothing. Code comments
  are the exception and stay as long as they need to be.
* **`docs/` argues once.** A claim has one home and everywhere else links to
  it: stated twice it drifts, and the copies disagree before anyone notices.
  Why a number is computed the way it is belongs in the doc comment beside the
  code — `docs/` says what it means. One illustration gives the same figure
  everywhere it appears, on the same pricing basis. A command name is a claim,
  so check it against `tokenamun help`; a "not yet built" section is a plan,
  so it lives in `plans/`. Then stop at the load-bearing sentence: one that
  adds no claim, number or consequence is a restatement, however well it
  reads, and one epigram a section is the ration.
* **Filter by developer, never report by developer.** Scoping to whose
  sessions you look at is how you help somebody. A dimension that ranks people
  is the failure mode this tool is closest to.
* **Measure; do not model.** No named techniques: a vendor's figure applied to
  your session is that vendor's claim wearing this tool's authority. The one
  counterfactual is `optimise`, where the caller supplies both the change and
  the reason, and the unknown section always prints. A new question needing an
  assumed parameter belongs to the caller.
* **Nothing is viewer-only.** The HTML report and the CLI answer the same
  questions, from the same payload, and a test asserts they cannot diverge. A
  finding only a browser can show is one the agent must ask a human to read
  out.
* **The checks are gates, not reports.** `scripts/checks.sh` runs gofmt, vet,
  race tests, golangci-lint, the private-data guards, dead code, an 80%
  coverage floor and file-length, complexity and duplication budgets from the
  tool's own scanner. The budgets are ratchets set just above where the code
  is; raise one only with the reason in the commit message. golangci-lint is
  the one step that skips when it is missing locally, which is why CI
  installs a pinned copy before running the script.
* **Tests before green.** Table-driven unit tests per package; golden-file
  tests over `testdata/` with no network and no API key. `-update` regenerates
  goldens — read the diff.
* **Accounting invariants are property tests**, not fixed numbers: deduplicated
  totals never exceed naive ones; carry never exceeds summed prompt tokens; a
  counterfactual reduction never exceeds the volume it applies to.
* **JSON output is an API** — it is how agents consume this tool. It has a
  `schema_version` and golden tests; changing a key is a breaking change.
* **Commit as you go.** Small working commits at each natural checkpoint, with
  the message about *why*. Don't push without being asked.
* **Iconography: no pyramids.** Tutankhamun reigned around 1330 BC, twelve
  centuries after the pyramid age — New Kingdom references only.

## Things not to build

* No session capture, hooks, or proxy. Tokenamun reads; Entire records.
* No SaaS, accounts, telemetry, or dashboards.
* No exact context-window reconstruction: report observed prompt sizes and say
  what cannot be decomposed.
* No developer-level metrics or leaderboards.
* No fabricated precision. Where the evidence is not in the data, the command
  says `not measurable from this data`. That output is a feature.
* **No MCP server.** It would load tool schemas into every session it is
  connected to, whether or not anyone profiles anything — which is precisely
  the overhead
  [`docs/COMMON-INTERVENTIONS.md`](docs/COMMON-INTERVENTIONS.md#trimming-instructions-and-the-preamble)
  says cannot be measured from a transcript. A profiler whose own footprint is
  invisible to it would be a poor joke. If a wrapper is ever wanted it is a
  thin skill: a question-to-command table and the epistemic rules, nothing
  else.
* **No content-category taxonomy.** There was one — ADRs, specs, plans, tests,
  source — declared per repository and otherwise guessed from directory names.
  A repository's layout already carries the category: `docs/decisions` *is* the
  decision records. On the reference dataset 8 of 9 categories were being
  filled by naming heuristics rather than declarations, and only one category's
  content spanned more than one directory. What it cost was the ability to
  aggregate content scattered by convention, and to declare that a path is not
  what it looks like. If a misleading path ever justifies it, the answer is a
  narrow override file, not a second taxonomy.
