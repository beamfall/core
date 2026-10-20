#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECK="$ROOT/script/cla-docs-check.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/cla" "$TMP/script"
cp "$CHECK" "$TMP/script/cla-docs-check.sh"
cat > "$TMP/CONTRIBUTING.md" <<'DOC'
Read and sign [CLA.md](CLA.md). A signed Beamfall Contributor License Agreement is required.
DOC
cat > "$TMP/CLA.md" <<'DOC'
This CLA grants relicensing rights. See CONTRIBUTING.md.
DOC
cat > "$TMP/cla/plugin-CONTRIBUTING-template.md" <<'DOC'
External contributions require a signed CLA and CLA.md.
DOC
cat > "$TMP/cla/plugin-propagation-checklist.md" <<'DOC'
Targets: plugin-sdk and plugin-* repos.
DOC

"$TMP/script/cla-docs-check.sh" >/dev/null

rm "$TMP/CLA.md"
if "$TMP/script/cla-docs-check.sh" >/dev/null 2>&1; then
  echo "cla-docs-check_test: missing CLA.md did not fail" >&2
  exit 1
fi

echo "cla-docs-check_test: all assertions passed"
