#!/usr/bin/env bash
# Every quality gate, in one place. The pre-commit hook and CI both run this,
# so "it passed locally" and "it passed in CI" mean the same thing.
#
#   scripts/checks.sh          run everything
#   scripts/checks.sh fast     skip the slower checks (used by the hook)
set -euo pipefail
cd "$(dirname "$0")/.."

mode="${1:-full}"
fail=0

# The coverage floor. Stated here rather than buried in the step, because it is
# the one number in this file that is a policy rather than a measurement.
COVERAGE_MIN=80

step() { printf '\033[1m==> %s\033[0m\n' "$1"; }
bad()  { printf '\033[31mFAIL\033[0m %s\n' "$1"; fail=1; }

step "gofmt"
unformatted="$(gofmt -l . || true)"
if [[ -n "$unformatted" ]]; then
  bad "not gofmt'd:"$'\n'"$unformatted"
fi

step "go vet"
go vet ./... || bad "go vet reported problems"

step "go build"
go build ./... || bad "build failed"

step "go test"
if [[ "$mode" == "fast" ]]; then
  go test ./... || bad "tests failed"
else
  go test -race -count=1 ./... || bad "tests failed"
fi

# Published-publicly guard (AGENTS.md). Data from the repositories we profile
# must never enter this repository, so refuse the obvious shapes of it.
step "no private data"
# shellcheck disable=SC2016
patterns=(
  '\.entire/'                                  # a profiled repo's Entire dir
  'tool-results/'                              # Claude Code spilled output
)
for p in "${patterns[@]}"; do
  if git ls-files | grep -qE "$p"; then
    bad "tracked files match private-data pattern: $p"
  fi
done
# Transcript-shaped files are only allowed under testdata/, where they must be
# synthetic or anonymised.
while IFS= read -r f; do
  [[ -z "$f" ]] && continue
  if [[ "$f" != *testdata/* ]]; then
    bad "transcript-shaped file outside testdata/: $f"
  fi
done < <(git ls-files '*.jsonl')

# Names and paths, in file contents and in commit messages.
#
# The filename checks above were the whole of this step for a while, and they
# missed the leak that actually happened: no transcript was ever committed,
# but four source comments and a usage example named the client repositories
# this tool was built by measuring, and one scratch script had a sibling path
# to one of them hard-coded. Commit messages were worse, because nothing had
# ever looked at them at all.
#
# Two rules this check has to obey, or it becomes the leak it prevents.
#
# First, no client name appears in it. A public repository cannot carry a
# denylist of private project names -- that publishes exactly the list it is
# meant to protect. So the built-in patterns are generic shapes that give
# nothing away, and specific names come from .private-names, which is
# gitignored and local. Without that file the generic checks still run.
#
# Second, it never echoes what it matched. Printing the offending line into a
# CI log would move the secret from the source into the build output, so
# failures name the file, the line number and the rule, and stop there. You
# open the file to see it.
step "no leaked names or local paths"
# shellcheck source=scripts/leakscan.sh
source "$(dirname "$0")/leakscan.sh"
scan_tracked
scan_private_names
bash "$(dirname "$0")/test-leak-guard.sh" || bad "the leak guard does not catch what it claims to"

# Dead code. Pinned as a tool dependency rather than fetched at @latest, so a
# new release of the analyser cannot change what this check says about an
# unchanged commit. It is a tool dependency, not a runtime one: nothing in the
# shipped binary imports it.
# Note the limit: deadcode reports unreachable *functions*. An unused method
# can still be reported as reachable, because a method may be called through
# any interface it satisfies. The coverage step below is what catches those --
# an unused method has no test exercising it either.
step "dead code"
if [[ "$mode" != "fast" ]]; then
  dead="$(go tool deadcode -test ./... || true)"
  if [[ -n "$dead" ]]; then
    bad "unreachable code (delete it, or reach it from a test):"$'\n'"$dead"
  fi
else
  echo "skipped in fast mode"
fi

# Oversized files, over-complex functions and duplication, measured by the
# tool's own scanner. The budgets are ratchets set just above where the
# codebase is: tight enough that adding to the worst file fails, loose enough
# that the current state passes. Raise one only with a reason in the commit.
#
# Test files are excluded from the duplication measure. Table-driven tests
# repeat their own shape by design, and counting that trains people to ignore
# the number.
step "code budgets"
go run ./cmd/tokenamun scan . \
  --max-file-lines 800 \
  --max-complexity 25 \
  --max-duplication 3 \
  --skip-duplicates-in _test.go \
  >/dev/null || bad "code budgets exceeded"

# Coverage, measured across the whole module rather than per package: the
# adapters in internal/claudecode are exercised through internal/ingest, and a
# per-package figure would report them as untested and be wrong.
step "coverage"
if [[ "$mode" != "fast" ]]; then
  profile="$(mktemp)"
  trap 'rm -f "$profile"' EXIT
  go test -coverpkg=./... -coverprofile="$profile" ./... >/dev/null || bad "tests failed under coverage"
  total="$(go tool cover -func="$profile" | awk '$1=="total:" {gsub(/%/,"",$3); print $3}')"
  echo "total coverage: ${total}%"
  if awk -v t="$total" -v min="$COVERAGE_MIN" 'BEGIN {exit !(t < min)}'; then
    bad "coverage ${total}% is below the ${COVERAGE_MIN}% floor"
  fi
else
  echo "skipped in fast mode"
fi

step "golangci-lint"
if command -v golangci-lint >/dev/null 2>&1; then
  golangci-lint run || bad "golangci-lint reported problems"
else
  echo "skipped: golangci-lint not installed"
fi

if [[ "$fail" -ne 0 ]]; then
  printf '\n\033[31mchecks failed\033[0m\n'
  exit 1
fi
printf '\n\033[32mall checks passed\033[0m\n'
