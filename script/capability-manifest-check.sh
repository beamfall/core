#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
"$ROOT/script/cla-docs-check.sh"

tmp="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-capabilities.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

( cd "$ROOT" && go build -o "$tmp/beamfall-default" ./cmd/beamfall )
( cd "$ROOT" && go build -tags beamfall_no_powertools -o "$tmp/beamfall-no-powertools" ./cmd/beamfall )
( cd "$ROOT" && "$tmp/beamfall-default" capabilities > "$tmp/default.json" )
( cd "$ROOT" && "$tmp/beamfall-no-powertools" capabilities > "$tmp/no-powertools.json" )
( cd "$ROOT" && "$tmp/beamfall-no-powertools" capabilities --version > "$tmp/no-powertools-version.json" )

python3 - "$tmp" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
default = json.loads((root / "default.json").read_text(encoding="utf-8"))
disabled = json.loads((root / "no-powertools.json").read_text(encoding="utf-8"))
version = json.loads((root / "no-powertools-version.json").read_text(encoding="utf-8"))

if default.get("capabilities") != ["conversion", "rename"]:
    raise SystemExit(f"default capabilities diverged: {default!r}")
manifest = default.get("capabilityManifest", {})
if manifest.get("schemaVersion") != 1 or manifest.get("namespace") != "flat":
    raise SystemExit(f"capability manifest schema diverged: {manifest!r}")
entries = manifest.get("capabilities", [])
ids = [entry.get("id") for entry in entries]
if len(ids) != len(set(ids)):
    raise SystemExit(f"capability manifest has duplicate IDs: {manifest!r}")
state_ids = [state.get("id") for state in default.get("capabilityStates", [])]
if ids != state_ids:
    raise SystemExit(f"runtime states do not key on manifest IDs: {default!r}")
if not entries or any(not isinstance(entry.get("version"), int) or entry["version"] < 1 for entry in entries):
    raise SystemExit(f"capability manifest versions are not positive integers: {manifest!r}")
if not isinstance(manifest.get("buildTags"), list):
    raise SystemExit(f"capability manifest buildTags missing: {manifest!r}")
if disabled.get("capabilities") not in ([], None):
    raise SystemExit(f"disabled capabilities diverged: {disabled!r}")
if version.get("capabilities") not in ([], None):
    raise SystemExit(f"version disabled capabilities diverged: {version!r}")
if "commit" not in version or "buildTags" not in version:
    raise SystemExit(f"version metadata missing: {version!r}")
PY

# App Store firewall (ADR-0124 §5 / invariant 8): the capability runtime-gating
# control surface must be suppressed BOTH UI and API on an App Store binary.
# Build the beamfall_appstore variant and run the API-suppression conformance
# (the control fields are not advertised in the schema manifest and not reachable
# as top-level fields) — a TEST of the API, not merely UI suppression.
( cd "$ROOT" && go build -tags beamfall_appstore ./... )
( cd "$ROOT" && go test -tags beamfall_appstore -count=1 ./internal/conformance/ -run TestAppStoreSuppressesCapabilityControlAPI )

"$ROOT/script/workspace-contracts-gate.sh"
