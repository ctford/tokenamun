#!/usr/bin/env bash
# Refresh the pinned LiteLLM price extract.
#
#   scripts/refresh-prices.sh          rewrite the extract from upstream
#   scripts/refresh-prices.sh --check  fail if upstream prices have moved
#
# This is the only thing in the repository that touches the network, and it is
# never run by the binary. A price fetched while a report is rendered would
# make the report of an unchanged session depend on the day you ran it, which
# is the opposite of what a profiler is for. Same reasoning as the pinned tool
# dependencies in scripts/checks.sh: vendor it, refresh it deliberately.
#
# Upstream is 2.8 MB and 4,318 entries. What lands here is the ~30 rows whose
# key begins "claude" and the commit they came from. The bare keys only:
# nineteen upstream keys mention opus-5 -- anthropic.claude-opus-5,
# eu.anthropic..., bedrock/..., databricks/..., aihubmix/... -- and they are
# the same model resold, not models this tool needs to tell apart.
#
# This script is also the quarantine boundary for LiteLLM's field names, the
# way internal/claudecode is for Claude Code's. Nothing in Go ever sees
# "cache_creation_input_token_cost_above_1hr"; the names it reads are the ones
# cost.Weights already uses.
set -euo pipefail
cd "$(dirname "$0")/.."

REPO=BerriAI/litellm
FILE=model_prices_and_context_window.json
OUT=internal/cost/litellm-prices.json

check=0
[[ "${1:-}" == "--check" ]] && check=1

need() { command -v "$1" >/dev/null 2>&1 || { echo "refresh-prices: needs $1" >&2; exit 2; }; }
need curl
need jq
need git

# Pin by commit, not by branch. Fetching raw from main gives you whatever main
# said at that second and no way to say what that was; resolving the tip first
# and fetching *that* SHA means the extract below is reproducible from the
# pin alone.
sha="$(git ls-remote "https://github.com/${REPO}" HEAD | cut -f1)"
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || { echo "refresh-prices: no upstream commit resolved" >&2; exit 1; }

full="$(mktemp)"; extract="$(mktemp)"
trap 'rm -f "$full" "$extract"' EXIT
curl -sSfL -o "$full" "https://raw.githubusercontent.com/${REPO}/${sha}/${FILE}"

# A missing rate on a model we price is a broken extract, not a cheap model,
# so it stops here rather than becoming a zero downstream. The 1h write rate
# is the one exception: upstream genuinely omits it on a couple of legacy
# aliases, and it is dropped rather than invented.
jq --arg sha "$sha" --arg date "$(date -u +%F)" '
  def rate($f): if has($f) and .[$f] != null then .[$f] else null end;
  {
    upstream: {
      repo: "'"$REPO"'",
      file: "'"$FILE"'",
      commit: $sha,
      retrieved: $date
    },
    price_unit: "usd_per_token",
    models: (
      to_entries
      | map(select(.key | startswith("claude")))
      | sort_by(.key)
      | map(
          . as $e
          | ($e.value | {
              input:          rate("input_cost_per_token"),
              cache_read:     rate("cache_read_input_token_cost"),
              cache_write_5m: rate("cache_creation_input_token_cost"),
              cache_write_1h: rate("cache_creation_input_token_cost_above_1hr"),
              output:         rate("output_cost_per_token")
            }) as $p
          | if ($p.input == null or $p.cache_read == null
                or $p.cache_write_5m == null or $p.output == null)
            then error("missing rate on \($e.key)")
            else {key: $e.key, value: ($p | with_entries(select(.value != null)))}
            end
        )
      | from_entries
    )
  }
' "$full" > "$extract"

count="$(jq '.models | length' "$extract")"
[[ "$count" -ge 20 ]] || { echo "refresh-prices: only $count claude rows, upstream shape changed" >&2; exit 1; }

if [[ "$check" -eq 1 ]]; then
  # Prices only. The commit moves many times a day on a repository this
  # active, and a gate that goes red because somebody upstream touched an
  # unrelated provider is a gate people learn to ignore. What must not move
  # without us noticing is a rate.
  if ! diff -u <(jq -S .models "$OUT") <(jq -S .models "$extract") >/dev/null; then
    echo "price drift against ${REPO}@${sha}:" >&2
    diff -u <(jq -S .models "$OUT") <(jq -S .models "$extract") >&2 || true
    echo >&2
    echo "run scripts/refresh-prices.sh, read the diff, and check internal/cost/cost.go still agrees" >&2
    exit 1
  fi
  echo "prices agree with ${REPO}@${sha} (${count} models)"
  exit 0
fi

mv "$extract" "$OUT"
trap 'rm -f "$full"' EXIT
echo "wrote $OUT: ${count} models from ${REPO}@${sha}"
