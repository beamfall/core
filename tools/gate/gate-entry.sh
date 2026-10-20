#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/../.."

if [ "${BEAMFALL_GATE_FIXTURE_GUARDED:-0}" != 1 ]; then
  exec python3 script/gate-fixture-guard.py -- "$0" "$@"
fi

if [ "${GATE_PREFLIGHT:-0}" != 1 ]; then
  make seeded-red-canary-controls
  make relay-vector-freshness-prove relay-vector-freshness
  python3 script/consumer_pin_freshness_test.py
  python3 script/consumer-pin-freshness.py
fi
exec python3 tools/gate/gate_latest.py -- tools/gate/gate.sh "$@"
