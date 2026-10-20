#!/usr/bin/env bash
# Orientation: the one-page map an agent reads before touching the repo.
# Canonical name table: tools/orient/orient.sh (= make orient).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

echo "== Beamfall Core — orientation =="
echo
echo "Module: $(head -1 go.mod | cut -d' ' -f2)   Go: $(grep '^go ' go.mod | cut -d' ' -f2)"
echo
branch="$(git branch --show-current 2>/dev/null || true)"
changed="$(git status --short 2>/dev/null | wc -l | tr -d ' ')"
printf 'Git: branch=%s changed_paths=%s\n' "${branch:-detached}" "$changed"
[ "$changed" -eq 0 ] || echo "  Inspect: git status --short (preserve unrelated work)"
echo
echo "-- code seams --"
echo "  cmd/ CLI · internal/ server packages · internal/web/app/ frontend"
echo "  script/ agent/ops tooling · tools/ conformance and gate tooling"
package_count="$(find internal -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
packages="$(find internal -mindepth 1 -maxdepth 1 -type d -exec basename {} \; | sort | awk 'BEGIN { ORS="" } NR > 1 { printf ", " } { printf "%s", $0 } END { print "" }')"
printf '  internal packages (%s):\n' "$package_count"
printf '%s\n' "$packages" | fold -s -w 96 | sed 's/^/    /'
echo
echo "-- next move --"
echo "  1. Read AGENTS.md (canonical; CLAUDE.md links to it)."
echo "  2. Map source: script/repo-map.sh --limit 80 [PATH ...]"
echo "  Open CONTEXT.md and docs/architecture only when the task needs them."
echo
echo "-- verify --"
echo "  Required final gate: make gate"
