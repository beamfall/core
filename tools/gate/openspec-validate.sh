#!/usr/bin/env bash
# CORE-0959: every OpenSpec change and spec under openspec/ must validate in
# strict mode (openspec/changes/adopt-openspec, requirement "Validation and
# archive"). Fails closed when the CLI is absent: a skipped validation would
# read as a green one.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

if ! command -v openspec >/dev/null 2>&1; then
  echo "openspec validate gate: openspec CLI not found on PATH (npm install -g @fission-ai/openspec)" >&2
  exit 1
fi

OPENSPEC_TELEMETRY=0 openspec validate --all --strict --no-interactive
echo "openspec validate gate: clean"
