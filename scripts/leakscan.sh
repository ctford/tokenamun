#!/usr/bin/env bash
# The leaked-names-and-paths scan, in a file of its own so that both the
# quality gates and a test can run it.
#
# Factored out because it shipped broken twice: once matching nothing at all
# because the patterns used Perl syntax that BSD grep does not support, and
# once truncating every pattern at its first alternation because the rules
# were packed into "label|regex|allow" strings. Both times the step passed on
# a file that was deliberately leaking, which is the worst way for a guard to
# fail -- it does not merely miss things, it certifies them. So the rules are
# now exercised by scripts/test-leak-guard.sh against files that are known to
# leak and files that are known not to.
#
# The caller supplies bad(); this file only decides what is wrong.


# Extended regular expressions, not Perl ones. The first version of these
# used -P and a negative lookahead, which BSD grep does not support: every
# rule matched nothing and the step passed on a file that was deliberately
# A real home directory in a tracked file is a machine-specific path that
# should have been a placeholder. Placeholders are allowed by subtraction
# afterwards, since ERE cannot say "not these".
home_re='/(Users|home)/[A-Za-z0-9._<>-]+'
home_ok='/(Users|home)/(x|you|someone|\.\.\.|<[^>]*>)(/|$)'
# A sibling project directory: the shape of "--dir ../their-repo" left behind
# after a session of profiling something else. Bare ../ and ../.. are fine,
# and so is ../cmd; it is the multi-word named sibling that is a local
# assumption about one machine's filesystem.
sibling_re='\.\./[a-z][a-z0-9]+(-[a-z0-9]+)+'

# scan_file <file> <label> <regex> [allowlist-regex]
#
# Arguments rather than a packed "label|regex|allow" string: the first version
# packed them and split on "|", which is also what ERE alternation is written
# with, so every pattern was silently truncated at its first branch. The
# regexes here all contain alternation, so any delimiter inside them is a
# delimiter waiting to do that again.
scan_file() {
  local f="$1" label="$2" re="$3" allow="${4:-}" hits
  hits="$(grep -nIoE "$re" "$f" 2>/dev/null || true)"
  [[ -n "$allow" && -n "$hits" ]] && hits="$(printf '%s\n' "$hits" | grep -vE "$allow" || true)"
  [[ -z "$hits" ]] && return 0
  while IFS=: read -r n _; do
    [[ -n "$n" ]] && bad "$f:$n matches $label (open the file; not printed, so this log stays clean)"
  done < <(printf '%s\n' "$hits")
}

# scan_tracked applies every generic rule to every tracked file.
#
# testdata/ is exempt: a fixture's job is to have the shape of real input, and
# a path in a transcript fixture is part of that shape. It must still be
# anonymised, which is a review matter rather than a grep one.
scan_tracked() {
  local f
  while IFS= read -r f; do
    [[ -z "$f" ]] && continue
    [[ "$f" == scripts/leakscan.sh || "$f" == scripts/test-leak-guard.sh ]] && continue
    [[ "$f" == *testdata/* ]] && continue
    scan_file "$f" "a real home directory" "$home_re" "$home_ok"
    scan_file "$f" "a sibling project path" "$sibling_re"
  done < <(git ls-files)
}

# scan_private_names checks tracked contents and every commit message against
# the local denylist.
#
# .private-names holds one extended regex per line: the projects whose data
# this tool has been pointed at. It is local and gitignored, and its being
# tracked is itself a failure -- a public repository carrying a list of
# private project names publishes the list it was meant to protect, which is
# why the names are not in this file.
#
# Commit messages are included because they are committed content, and a
# filename check never sees them.
# messages_match <regex> -- true when any commit message on any ref matches.
#
# Written as a single grep against a here-string, and not as
# `git log ... | grep -q`, which is how it was written and is the third way
# this guard has managed to report success over a leak. grep -q exits at the
# first match, git gets SIGPIPE, and under `set -o pipefail` -- which the
# quality gates run with -- the pipeline's status becomes git's 141 rather
# than grep's 0. So a match read as "no match", and only when the leak was
# large enough for git to still be writing. The isolated test passed because
# its shell had no pipefail. One command in the pipeline, no such trap.
messages_match() {
  if [[ -z "${_message_cache+x}" ]]; then
    _message_cache="$(git log --format=%B --all 2>/dev/null || true)"
  fi
  grep -qiE "$1" <<<"$_message_cache"
}

scan_private_names() {
  local name f
  if git ls-files --error-unmatch .private-names >/dev/null 2>&1; then
    bad ".private-names is tracked; it must stay local and gitignored"
  fi
  if [[ ! -f .private-names ]]; then
    echo "  .private-names absent; generic checks only"
    return 0
  fi
  while IFS= read -r name; do
    [[ -z "$name" || "$name" == \#* ]] && continue
    while IFS= read -r f; do
      [[ -z "$f" ]] && continue
      bad "$f names a private project (matched .private-names; not printed)"
    done < <(git grep -lIiE "$name" -- . ':!.private-names' 2>/dev/null || true)
    if messages_match "$name"; then
      bad "a commit message names a private project (matched .private-names; not printed)"
    fi
  done < .private-names
}
