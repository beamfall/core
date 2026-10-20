#!/usr/bin/env bash
# Tracked-vs-.gitignore reconciliation gate (fixes.md 2026-07-06 "Reviewer-
# flagged guard scripts"). Catches the "half-tracked" drift class where a
# path is both `git`-tracked AND matched by `.gitignore` — usually a sign the
# ignore rule was added after the fact and the tracked snapshot is now stale
# (the `internal/web/app-dist/` case, OSS-16: 13 force-tracked build outputs
# left inconsistent with the current build, so a clean clone 404s).
#
# `git ls-files -ci --exclude-standard` lists exactly the tracked paths a
# fresh `git add .` would skip today — the drift signature. A NEW hit reds
# the gate. Known, already-filed debt is allowlisted below (self-pruning: an
# allowlist entry that no longer reproduces also reds the gate, so it can
# only shrink as tickets land).
set -euo pipefail

ROOT="${BEAMFALL_GATE_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
cd "$ROOT"

# Each entry: exact tracked-but-ignored path, as `git ls-files -ci
# --exclude-standard` reports it (repo-root-relative).
ALLOWLIST=(
)

found=()
while IFS= read -r line; do
  [ -n "$line" ] && found+=("$line")
done < <(git ls-files -ci --exclude-standard)

fail=0

is_allowed() {
  local path="$1" a
  for a in ${ALLOWLIST[@]+"${ALLOWLIST[@]}"}; do
    [ "$a" = "$path" ] && return 0
  done
  return 1
}

for f in ${found[@]+"${found[@]}"}; do
  if ! is_allowed "$f"; then
    echo "tracked-vs-gitignore: NEW tracked-but-ignored path: $f" >&2
    fail=1
  fi
done

for a in ${ALLOWLIST[@]+"${ALLOWLIST[@]}"}; do
  still_present=0
  for f in ${found[@]+"${found[@]}"}; do
    [ "$f" = "$a" ] && still_present=1 && break
  done
  if [ "$still_present" -eq 0 ]; then
    echo "tracked-vs-gitignore: stale allowlist entry (already fixed, remove it): $a" >&2
    fail=1
  fi
done

if [ "$fail" -ne 0 ]; then
  echo "==> tracked-vs-gitignore gate FAILED" >&2
  exit 1
fi

echo "tracked-vs-gitignore gate: clean (${#ALLOWLIST[@]} known allowlisted path(s))"
