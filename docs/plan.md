# What is deferred, and why

This was the architecture and v0.1 plan. v0.1 is built, so most of it
described the code less accurately than the code does, and it is gone. What
is left is the part a plan is actually for: things deliberately not built, and
the reasoning behind a design that was replaced.

Where the delivered work is now written down:

| what | where |
| --- | --- |
| how every number is computed and labelled | [`METHODOLOGY.md`](../METHODOLOGY.md) |
| the conventions and the quality gates | [`AGENTS.md`](../AGENTS.md) |
| the commands and flags | `tokenamun help` |
| what Entire's data contains | [`ENTIRE.md`](../ENTIRE.md) |
| what people try, and what can be checked | [`interventions.md`](interventions.md) |
| before-and-after experiments | [`experiments.md`](experiments.md) |

## Content classification: removed

There was a category axis -- ADRs, specifications, plans, tests, source --
declared per repository in `.tokenamun.json` and otherwise guessed from
directory naming. It is gone, and the reasoning is worth keeping because it
applies to the next taxonomy someone proposes.

In practice a repository's directory layout already carries the category:
`docs/decisions` *is* the decision records. Once retrieved content was nested
by directory, the tree answered the same question with no configuration and
no guessing about someone else's project. Measured on the reference dataset,
8 of 9 categories were being filled by naming heuristics rather than by
declarations, and only one category's content spanned more than one directory
-- so the thing a category could do that a directory cannot was a rounding
error next to the cost of being wrong about a layout.

What was lost, stated plainly: aggregating content scattered by convention
(Go tests live beside the code they test, so a directory view shows them in
nine places), and the ability to declare that a path is not what it looks
like (`.claude/projects/**/tool-results/*.txt` reads as instructions from its
path but is spilled tool output). If either becomes painful, the answer is a
narrow override file for misleading paths -- not a second taxonomy.

## Activity classification, deferred

Whether a stretch of work was planning or debugging is the weakest thing this
tool could report, and it answers a question nobody has asked yet. Building it
would invite exactly the misreading the epistemics exist to prevent: a
confident-looking "planning: 34%" sitting beside genuinely observed token
figures.

The evidence is there when it is wanted: tool name and arguments;
`permissionMode` and `mode` entries (plan mode is *observed*, which is a
gift); `EnterPlanMode`/`ExitPlanMode` calls; Entire's `skill_events` with
`confidence: "explicit"`; test and build commands in Bash; edit/write density;
`Agent` calls; `turn_duration`.

A taxonomy of orientation, planning, research, implementation, debugging,
verification, review and other. The acceptance criterion is the one that
matters: a window that does not clear a confidence threshold is `other`, and a
test has to assert the classifier does not over-assign.

## Artifact and GitChange, unbuilt

Named in the retired spec's data model and never defined beyond the name. What
they were reaching for is which files a session touched and what it committed,
which Entire records as `files_touched` and which nothing here reads.

That is the shape of the next real question: cost per change, rather than cost
per session.

## CLI first, thin skill later, never an MCP server

The CLI is the product. An agent invoking `tokenamun profile --json` costs
nothing until it runs, needs no configuration, and works the same from a
terminal, a script or CI.

A wrapping **skill** is worth adding afterwards, for one reason a good CLI
cannot cover: an agent reading this output will get the epistemics wrong. It
will see a counterfactual saving of 125,218 and report "this saves 125K
tokens". The CLI can label the field; it cannot stop the agent misreading it.
That instruction -- *observed and derived are measurements, inferred is an
opinion, counterfactual is arithmetic under an assumption, given is your own
figure, and a local reduction is not a session saving* -- belongs in a skill,
alongside a map from question to command. It should be thin: a description, a
question-to-command table, the epistemic rules, nothing else. Progressive
disclosure means the body costs nothing until it is invoked.

Not an MCP server. An MCP server loads tool schemas into the context of every
session it is connected to, whether or not anyone profiles anything -- which
is precisely the overhead
[`interventions.md`](interventions.md#trimming-instructions-and-the-preamble)
says cannot even be measured from a transcript. Shipping a profiler whose own
footprint is invisible to it would be a poor joke.

One consequence worth stating: Tokenamun's own contribution to the sessions it
profiles should stay small enough to be uninteresting, and that should be
checked rather than assumed. `tokenamun profile` run against a session that
itself used Tokenamun is the test.
