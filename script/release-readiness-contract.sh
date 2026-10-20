#!/usr/bin/env bash
# Reports contract-registry release readiness as PASS/FAIL so the reporting can
# be exercised against a fixture registry in tests.
#
# Usage: release-readiness-contract.sh [REGISTRY]
#   REGISTRY defaults to <this-repo-root>/contracts/registry.json and may point
#   anywhere (fixture, vendored copy, ...); --repo-root is always this script's
#   own repo root, since that's where internal/conformance/ actually lives.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REGISTRY="${1:-$ROOT/contracts/registry.json}"

echo "-- contract registry --"
if python3 "$ROOT/script/check-contract-registry.py" --registry "$REGISTRY" --repo-root "$ROOT"; then
  echo "PASS contract registry"
else
  echo "FAIL contract registry"
  exit 1
fi
