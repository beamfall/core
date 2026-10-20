#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
exec python3 "$repo_root/script/sha256-manifest.py" generate \
  --root "$repo_root/internal/conformance/testdata/subtitle_selection" \
  --comment 'sha256 of the canonical ADR-0194 §3 subtitle-selection corpus.' \
  --comment 'Paths are relative to internal/conformance/testdata/subtitle_selection/.' \
  --glob '*.json'
