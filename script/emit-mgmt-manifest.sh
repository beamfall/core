#!/usr/bin/env bash
# emit-mgmt-manifest.sh — regenerate the Core management-surface manifest.
#
# Emits internal/conformance/mgmt-manifest.json from the live graph.SchemaManifest()
# (and mgmt-manifest-appstore.json from the beamfall_appstore build, which the
# App Store-tagged conformance test reads)
# filtered to admin-tier fields, plus the declared management HTTP routes.
# This is the ADR-0122 Tier-2 parity artifact consumed by beamfall-web's
# script/check-mgmt-parity.sh.
#
# Usage:
#   script/emit-mgmt-manifest.sh           # writes internal/conformance/mgmt-manifest.json
#   script/emit-mgmt-manifest.sh --stdout  # writes to stdout instead
#
# Run from the repo root (same directory as go.mod).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="$REPO_ROOT/internal/conformance/mgmt-manifest.json"

if [[ "${1:-}" == "--stdout" ]]; then
  cd "$REPO_ROOT"
  exec go run ./tools/mgmt-manifest
fi

cd "$REPO_ROOT"
go run ./tools/mgmt-manifest > "$OUT"
echo "==> mgmt-manifest written: $OUT"
APPSTORE_OUT="$REPO_ROOT/internal/conformance/mgmt-manifest-appstore.json"
go run -tags beamfall_appstore ./tools/mgmt-manifest > "$APPSTORE_OUT"
echo "==> mgmt-manifest written: $APPSTORE_OUT"
