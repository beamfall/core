#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
  echo "cla-docs-check: $*" >&2
  exit 1
}

for path in CONTRIBUTING.md CLA.md cla/plugin-CONTRIBUTING-template.md cla/plugin-propagation-checklist.md; do
  [[ -s "$ROOT/$path" ]] || fail "missing $path"
done

rg -q '\[CLA\.md\]\(CLA\.md\)' "$ROOT/CONTRIBUTING.md" || fail "CONTRIBUTING.md must link to CLA.md"
rg -q 'CONTRIBUTING\.md|contribution gate' "$ROOT/CLA.md" || fail "CLA.md must reference CONTRIBUTING.md or the contribution gate"
rg -q 'signed Beamfall Contributor License Agreement|signed CLA' "$ROOT/CONTRIBUTING.md" || fail "CONTRIBUTING.md must require a signed CLA"
rg -q 'relicense|relicensing' "$ROOT/CLA.md" || fail "CLA.md must grant relicensing rights"
rg -q 'plugin-sdk' "$ROOT/cla/plugin-propagation-checklist.md" || fail "propagation checklist must cover plugin-sdk"
rg -q 'plugin-\*|plugin-' "$ROOT/cla/plugin-propagation-checklist.md" || fail "propagation checklist must cover plugin-* repos"

echo "cla-docs-check: PASS"
