#!/usr/bin/env bash
# check-quarantine.sh — ADR-0121 §6 (+ Amendment A §3) quarantine manifest checker.
#
# Reads testing/quarantine.yaml and fails if:
#   1. Any entry has no expiresAt (unbounded quarantine is prohibited).
#   2. Any entry's expiresAt is today or in the past (deadline passed).
#   3. The total entry count exceeds the manifest's maxEntries cap.
#   4. Any entry names no governed skip kind, or more than one.
#
# Governed skip kinds (an entry sets exactly one — see the manifest header):
#   test / pathFilter / envGate / strictFlag. Amendment A §3 extends the §6
#   green-skip ban beyond in-test skips to workflow paths: filters, env-gated
#   tiers whose variable is never set in CI, and default-off strictness flags.
#
# Usage: script/check-quarantine.sh [--manifest PATH]
#   --manifest PATH   Path to quarantine YAML (default: testing/quarantine.yaml)
#
# Exit codes: 0 = all clear; 1 = violation found; 2 = usage/parse error.
#
# Multi-repo adoption: copy this script verbatim into each repo's script/
# directory, add a testing/quarantine.yaml manifest, and call
# `script/check-quarantine.sh` from the repo's gate. No other changes needed.
set -euo pipefail

MANIFEST="testing/quarantine.yaml"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --manifest) MANIFEST="$2"; shift 2 ;;
    --manifest=*) MANIFEST="${1#*=}"; shift ;;
    *) echo "check-quarantine: unknown option: $1" >&2; exit 2 ;;
  esac
done

if [[ ! -f "$MANIFEST" ]]; then
  echo "check-quarantine: manifest not found: $MANIFEST" >&2
  exit 2
fi

# Require python3 for YAML parsing (available everywhere we run).
if ! command -v python3 >/dev/null 2>&1; then
  echo "check-quarantine: python3 not found" >&2
  exit 2
fi

exec python3 "$(dirname "${BASH_SOURCE[0]}")/check-quarantine.py" "$MANIFEST"
