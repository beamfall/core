#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHECK="$ROOT/tools/gate/subtitleingest-stdlib-check.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

"$CHECK" >/dev/null

printf 'module fixture\n\ngo 1.26\n' >"$TMP/go.mod"
printf 'package fixture\n\nimport _ "example.com/external"\n' >"$TMP/external.go"
if GOWORK=off "$CHECK" "$TMP" >"$TMP/out" 2>"$TMP/err"; then
  echo "subtitleingest stdlib check accepted an injected external import" >&2
  exit 1
fi
grep -q 'example.com/external' "$TMP/err"

if GOWORK="$ROOT/go.work" "$CHECK" "$TMP" >"$TMP/out" 2>"$TMP/err"; then
  echo "subtitleingest stdlib check accepted an unloadable package" >&2
  exit 1
fi
grep -q 'package load failed' "$TMP/err"

echo "subtitleingest-stdlib-check_test: OK"
