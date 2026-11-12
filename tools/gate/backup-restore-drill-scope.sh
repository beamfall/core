#!/usr/bin/env bash
set -euo pipefail

# Prints this lane's changed paths, one per line: the diff against its
# merge-base with main, or against the parent commit when run directly on
# main (a merge-base of main with itself is empty and would otherwise hide
# the commit that just landed there). The result is unioned with any
# staged/unstaged local edits. Callers feed the output to
# tools/gate/backup-restore-trigger.sh (via --changed-file) to decide whether
# the bounded PR-scope restore drill (tools/gate/backup-restore-drill.sh)
# must run — BKUP-CHAIN-1-RESIDUAL-1 r2 gap 3: the trigger selector already
# existed but nothing computed a real changed-path list to feed it.
#
# CORE-1299: "main" means origin/main when that ref exists. Lanes land by PR,
# so the shared checkout's local main lags origin/main and a merge-base with it
# would attribute every change main gained since to the lane.
main_ref=main
if git rev-parse --verify --quiet 'refs/remotes/origin/main^{commit}' >/dev/null 2>&1; then
  main_ref=refs/remotes/origin/main
fi
base=""
if [ "$(git rev-parse --abbrev-ref HEAD 2>/dev/null)" = "main" ]; then
  base="$(git rev-parse HEAD~1 2>/dev/null || true)"
else
  base="$(git merge-base "$main_ref" HEAD 2>/dev/null || true)"
fi

committed=""
if [ -n "$base" ]; then
  committed="$(git diff --name-only "$base" HEAD 2>/dev/null || true)"
fi
staged="$(git diff --name-only --cached 2>/dev/null || true)"
unstaged="$(git diff --name-only 2>/dev/null || true)"

printf '%s\n%s\n%s\n' "$committed" "$staged" "$unstaged" | sort -u | sed '/^$/d'
