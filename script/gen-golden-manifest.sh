#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
exec python3 "$repo_root/script/sha256-manifest.py" generate \
  --root "$repo_root/internal/conformance/testdata" \
  graph-response.golden.json \
  manifest.golden.json \
  playback-info.golden.json \
  token-scope.golden.json
