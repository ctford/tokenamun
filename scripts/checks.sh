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
