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

# The commit-message rule, under the caller's shell options.
#
# This file sets pipefail deliberately, because the quality gates do and the
# bug it is catching only exists there. The rule was written as
# `git log | grep -q`; grep exits at the first match, git takes SIGPIPE, and
# pipefail turns the pipeline's status into git's 141. Every match read as a
# miss. It went unnoticed because the first test of it ran in a shell without
# pipefail, where it passed.
#
# The probes use text this repository's own messages certainly do and
# certainly do not contain, so the mechanism is tested without any private
# name appearing here.
if ! messages_match 'co-authored-by'; then
  printf 'FAIL leak guard: messages_match found nothing in a history full of matches\n'
  fail=1
fi
if messages_match 'zzz-not-in-any-commit-message-zzz'; then
  printf 'FAIL leak guard: messages_match matched a string that is not there\n'
  fail=1
fi

# The denylist file must never be tracked: that is the failure mode where the
# guard publishes the names it protects.
if git ls-files --error-unmatch .private-names >/dev/null 2>&1; then
  printf 'FAIL leak guard: .private-names is tracked\n'
  fail=1
fi

exit "$fail"
