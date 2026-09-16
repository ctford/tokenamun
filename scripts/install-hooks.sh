#!/usr/bin/env bash
# Point git at the hooks tracked in .githooks/.
#
# Using core.hooksPath rather than copying into .git/hooks means the hooks are
# version-controlled and stay in step when they change.
set -euo pipefail
cd "$(dirname "$0")/.."

git config core.hooksPath .githooks
chmod +x .githooks/* scripts/*.sh

echo "hooks installed: $(git config core.hooksPath)"
echo "pre-commit will run scripts/checks.sh fast"
echo "bypass a single commit with: git commit --no-verify"
