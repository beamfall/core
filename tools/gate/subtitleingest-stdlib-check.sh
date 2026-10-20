#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PACKAGE="${1:-$ROOT/internal/playback/subtitleingest}"

if ! imports="$(cd "$PACKAGE" && go list -f '{{range .Imports}}{{.}}{{"\n"}}{{end}}' .)"; then
  echo "subtitleingest-stdlib-check: package load failed" >&2
  exit 1
fi
if external="$(printf '%s\n' "$imports" | grep '\.' || true)" && [ -n "$external" ]; then
  printf 'subtitleingest-stdlib-check: non-stdlib import(s):\n%s\n' "$external" >&2
  exit 1
fi
echo "subtitleingest-stdlib-check: OK"
