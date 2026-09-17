#!/usr/bin/env bash
# Does the leak guard catch what it claims to?
#
# This test exists because the guard shipped broken twice, and both times it
# reported success. Once its patterns used Perl syntax that BSD grep does not
# support, so nothing matched; once the rules were packed into
# "label|regex|allow" strings and split on "|", which truncated every pattern
# at its first alternation. A guard that silently matches nothing is worse
# than no guard, because the green tick is read as evidence.
#
# So every rule is exercised both ways: a string it must catch, and a string
# it must not. The must-not cases are the ones that stop the guard being
# quietly disabled by somebody adding an allowlist entry that swallows
# everything.
set -uo pipefail
cd "$(dirname "$0")/.."
# shellcheck source=scripts/leakscan.sh
source "$(dirname "$0")/leakscan.sh"

found=0
bad() { found=$((found + 1)); }

fail=0
# check <expected-count> <label> <content...>
check() {
  local want="$1" label="$2"; shift 2
  local tmp; tmp="$(mktemp)"
  printf '%s\n' "$@" > "$tmp"
  found=0
  scan_file "$tmp" "a real home directory" "$home_re" "$home_ok"
  scan_file "$tmp" "a sibling project path" "$sibling_re"
  rm -f "$tmp"
  if [[ "$found" -ne "$want" ]]; then
    printf 'FAIL leak guard: %s -- wanted %s finding(s), got %s\n' "$label" "$want" "$found"
    fail=1
  fi
}

check 1 "a real home directory" '/Users/a-real-name/Codigo/thing'
check 1 "a real home directory on Linux" 'cd /home/a-real-name/src'
check 0 "the placeholders already in use" \
  '/Users/x/repo/f.go' '/home/you/f.go' '/Users/<someone>/f.go' 'turned /Users/... into Users/...'
check 1 "a sibling project path" 'tokenamun profile all --dir ../some-client-repo'
check 0 "relative paths that are about this repo" \
  'see ../cmd and ../.. and ../ and ./scripts'
check 2 "both rules at once" '/Users/a-real-name/x' '--dir ../some-client-repo'
check 0 "an ordinary source line" 'func main() { fmt.Println("hello") }'

# The denylist file must never be tracked: that is the failure mode where the
# guard publishes the names it protects.
if git ls-files --error-unmatch .private-names >/dev/null 2>&1; then
  printf 'FAIL leak guard: .private-names is tracked\n'
  fail=1
fi

exit "$fail"
