#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "${BEAMFALL_CROSSREPO_HARNESS_SUPERVISED:-0}" != "1" ]]; then
  TMP="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-crossrepo-conformance-test.XXXXXX")"
  SUPERVISOR_PID=""
  CLEANUP_SIGNAL=TERM
  PENDING_SIGNAL=""
  PENDING_STATUS=""

  cleanup() {
    local status=$?
    trap - EXIT INT TERM
    if [[ -n "$SUPERVISOR_PID" ]]; then
      kill -s "$CLEANUP_SIGNAL" "$SUPERVISOR_PID" 2>/dev/null || true
      wait "$SUPERVISOR_PID" 2>/dev/null || true
    fi
    rm -rf "$TMP"
    exit "$status"
  }
  interrupt() {
    CLEANUP_SIGNAL="$1"
    exit "$2"
  }
  defer_interrupt() {
    PENDING_SIGNAL="$1"
    PENDING_STATUS="$2"
  }
  trap cleanup EXIT
  trap 'defer_interrupt INT 130' INT
  trap 'defer_interrupt TERM 143' TERM

  set +e
  BEAMFALL_CROSSREPO_HARNESS_TMP="$TMP" \
    python3 "$ROOT/script/test-process-supervisor.py" \
      BEAMFALL_CROSSREPO_HARNESS_SUPERVISED \
      "$ROOT/script/crossrepo-conformance_test.sh" "$@" &
  SUPERVISOR_PID=$!
  trap 'interrupt INT 130' INT
  trap 'interrupt TERM 143' TERM
  if [[ -n "$PENDING_SIGNAL" ]]; then
    interrupt "$PENDING_SIGNAL" "$PENDING_STATUS"
  fi
  wait "$SUPERVISOR_PID"
  status=$?
  SUPERVISOR_PID=""
  set -e
  exit "$status"
fi

TMP="${BEAMFALL_CROSSREPO_HARNESS_TMP:?supervised harness temp directory is required}"
CONSUMER_PIN="$(
  git -C "$ROOT" show HEAD:testing/release-gates/gate-commands.tsv \
    | awk -F '\t' '$1 == "beamfall-web" { print $5 }'
)"
CONSUMER_SOURCE="${BEAMFALL_CROSSREPO_CONSUMER_SOURCE:-https://github.com/beamfall/web.git}"

GREEN="$TMP/green.log"
RED="$TMP/red.log"

if ! TMPDIR="$TMP" "$ROOT/script/crossrepo-conformance.sh" >"$GREEN" 2>&1; then
  cat "$GREEN" >&2
  echo "crossrepo-conformance_test: green control failed" >&2
  exit 1
fi
grep -F "CHECKOUT producer sha=" "$GREEN" >/dev/null
grep -F "CHECKOUT consumer sha=$CONSUMER_PIN " "$GREEN" >/dev/null
grep -F \
  "RESOLVE consumer repo=beamfall-web pinned_sha=$CONSUMER_PIN resolved_sha=$CONSUMER_PIN note=branch-main" \
  "$GREEN" >/dev/null
grep -F "wire-drift-red: OK" "$GREEN" >/dev/null
grep -F "PASS ADR-0122 producer->consumer wire conformance" "$GREEN" >/dev/null

OVERRIDE_PRODUCER="$TMP/override-producer"
git clone --quiet "$ROOT" "$OVERRIDE_PRODUCER"
git -C "$OVERRIDE_PRODUCER" \
  -c commit.gpgsign=false \
  -c user.name='Beamfall Test' \
  -c user.email=test@beamfall.invalid \
  commit --quiet --allow-empty -m 'alternate producer revision fixture'
OVERRIDE_PRODUCER_REF="$(git -C "$OVERRIDE_PRODUCER" rev-parse HEAD)"
[[ "$OVERRIDE_PRODUCER_REF" != "$(git -C "$ROOT" rev-parse HEAD)" ]]

GATE_OVERRIDE="$TMP/gate-override.log"
if BEAMFALL_CROSSREPO_FIXTURE_SUPERVISED=1 \
  BEAMFALL_CROSSREPO_PRODUCER_SOURCE="$OVERRIDE_PRODUCER" \
  BEAMFALL_CROSSREPO_PRODUCER_REF="$OVERRIDE_PRODUCER_REF" \
  BEAMFALL_CROSSREPO_CONSUMER_SOURCE="$CONSUMER_SOURCE" \
  TMPDIR="$TMP" make -s -C "$ROOT" crossrepo-conformance >"$GATE_OVERRIDE" 2>&1; then
  echo "crossrepo-conformance_test: producer override escaped the blocking gate" >&2
  cat "$GATE_OVERRIDE" >&2
  exit 1
fi
grep -F "producer overrides are forbidden in the blocking gate" "$GATE_OVERRIDE" >/dev/null

UNSUPERVISED_OVERRIDE="$TMP/unsupervised-override.log"
if BEAMFALL_CROSSREPO_FIXTURE_SUPERVISED=0 \
  BEAMFALL_CROSSREPO_PRODUCER_SOURCE="$OVERRIDE_PRODUCER" \
  BEAMFALL_CROSSREPO_PRODUCER_REF="$OVERRIDE_PRODUCER_REF" \
  TMPDIR="$TMP" "$ROOT/script/crossrepo-conformance.sh" >"$UNSUPERVISED_OVERRIDE" 2>&1; then
  echo "crossrepo-conformance_test: unsupervised producer override was accepted" >&2
  cat "$UNSUPERVISED_OVERRIDE" >&2
  exit 1
fi
grep -F \
  "producer overrides require BEAMFALL_CROSSREPO_FIXTURE_SUPERVISED=1" \
  "$UNSUPERVISED_OVERRIDE" >/dev/null

FIXTURE_OVERRIDE="$TMP/fixture-override.log"
if ! BEAMFALL_CROSSREPO_FIXTURE_SUPERVISED=1 \
  BEAMFALL_CROSSREPO_PRODUCER_SOURCE="$OVERRIDE_PRODUCER" \
  BEAMFALL_CROSSREPO_PRODUCER_REF="$OVERRIDE_PRODUCER_REF" \
  BEAMFALL_CROSSREPO_CONSUMER_SOURCE="$CONSUMER_SOURCE" \
  TMPDIR="$TMP" "$ROOT/script/crossrepo-conformance.sh" >"$FIXTURE_OVERRIDE" 2>&1; then
  cat "$FIXTURE_OVERRIDE" >&2
  echo "crossrepo-conformance_test: supervised producer override fixture failed" >&2
  exit 1
fi
grep -F \
  "CHECKOUT producer sha=$OVERRIDE_PRODUCER_REF source=$OVERRIDE_PRODUCER" \
  "$FIXTURE_OVERRIDE" >/dev/null
grep -F "PASS ADR-0122 producer->consumer wire conformance" "$FIXTURE_OVERRIDE" >/dev/null

read -r PIN_COHERENCE_CONSUMER_VERSION PIN_COHERENCE_REGISTRY_VERSION < <(
python3 - "$OVERRIDE_PRODUCER/contracts/registry.json" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
registry = json.loads(path.read_text(encoding="utf-8"))
boundary = next(row for row in registry["boundaries"] if row["boundary"] == "client-core-wire")
consumer = next(row for row in boundary["consumers"] if row["repo"] == "beamfall-web")
old_version = consumer["pinnedVersion"]
major, minor, _ = (int(part) for part in old_version.split("."))
new_version = f"{major}.{minor + 1}.0"
consumer["pinnedVersion"] = new_version
path.write_text(json.dumps(registry, indent=2) + "\n", encoding="utf-8")
print(old_version, new_version)
PY
)
python3 "$ROOT/script/check-contract-registry.py" \
  --registry "$OVERRIDE_PRODUCER/contracts/registry.json" \
  --repo-root "$OVERRIDE_PRODUCER" >/dev/null

PIN_COHERENCE_RED="$TMP/pin-coherence-red.log"
set +e
BEAMFALL_CROSSREPO_FIXTURE_SUPERVISED=1 \
BEAMFALL_CROSSREPO_PRODUCER_SOURCE="$OVERRIDE_PRODUCER" \
BEAMFALL_CROSSREPO_CONSUMER_SOURCE="$CONSUMER_SOURCE" \
TMPDIR="$TMP" "$ROOT/script/crossrepo-conformance.sh" >"$PIN_COHERENCE_RED" 2>&1
pin_coherence_status=$?
set -e

if [[ "$pin_coherence_status" -eq 0 ]]; then
  echo "crossrepo-conformance_test: incoherent registry and consumer contract pins passed" >&2
  cat "$PIN_COHERENCE_RED" >&2
  exit 1
fi
grep -F "APPLY producer tracked working-tree delta" "$PIN_COHERENCE_RED" >/dev/null
if ! grep -F \
    "consumer contract version mismatch: registry_pinned_version=$PIN_COHERENCE_REGISTRY_VERSION consumer_contract_version=$PIN_COHERENCE_CONSUMER_VERSION consumer_commit=$CONSUMER_PIN" \
    "$PIN_COHERENCE_RED" >/dev/null; then
  echo "crossrepo-conformance_test: pin incoherence diagnostic omitted either version or the consumer commit" >&2
  cat "$PIN_COHERENCE_RED" >&2
  exit 1
fi
if grep -F "RUN producer->consumer wire conformance" "$PIN_COHERENCE_RED" >/dev/null; then
  echo "crossrepo-conformance_test: pin incoherence reached the consumer verifier" >&2
  cat "$PIN_COHERENCE_RED" >&2
  exit 1
fi

set +e
TMPDIR="$TMP" "$ROOT/script/crossrepo-conformance.sh" --inject-skew >"$RED" 2>&1
status=$?
set -e

if [[ "$status" -eq 0 ]]; then
  echo "crossrepo-conformance_test: injected producer skew did not red the lane" >&2
  cat "$RED" >&2
  exit 1
fi
grep -F "INJECT producer artifact SHA skew:" "$RED" >/dev/null
grep -F \
  "RESOLVE consumer repo=beamfall-web pinned_sha=$CONSUMER_PIN resolved_sha=$CONSUMER_PIN note=branch-main" \
  "$RED" >/dev/null
grep -F "verifyCoreWireArtifact" "$RED" >/dev/null
grep -F "Core wire artifact guard" "$RED" >/dev/null

DIRTY_PRODUCER="$TMP/dirty-producer"
DIRTY_CONSUMER="$TMP/dirty-consumer"
git clone --quiet "$ROOT" "$DIRTY_PRODUCER"
git clone --quiet "$CONSUMER_SOURCE" "$DIRTY_CONSUMER"
git -C "$DIRTY_CONSUMER" checkout --quiet -B dirty-pin-skew "$CONSUMER_PIN"
python3 - "$DIRTY_CONSUMER/test/core-wire/client-core-wire.generated.json" <<'PY'
import json
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
binding = json.loads(path.read_text(encoding="utf-8"))
binding["artifactSha256"] = "0" * 64
path.write_text(json.dumps(binding, indent=2) + "\n", encoding="utf-8")
PY
git -C "$DIRTY_CONSUMER" add test/core-wire/client-core-wire.generated.json
git -C "$DIRTY_CONSUMER" \
  -c commit.gpgsign=false \
  -c user.name='Beamfall Test' \
  -c user.email=test@beamfall.invalid \
  commit --quiet -m 'dirty consumer pin skew fixture'
DIRTY_CONSUMER_PIN="$(git -C "$DIRTY_CONSUMER" rev-parse HEAD)"

awk -F '\t' -v OFS='\t' -v pin="$DIRTY_CONSUMER_PIN" \
  '$1 == "beamfall-web" { $5 = pin } { print }' \
  "$DIRTY_PRODUCER/testing/release-gates/gate-commands.tsv" \
  >"$TMP/gate-commands.dirty.tsv"
mv "$TMP/gate-commands.dirty.tsv" \
  "$DIRTY_PRODUCER/testing/release-gates/gate-commands.tsv"

DIRTY_PIN_RED="$TMP/dirty-pin-red.log"
set +e
BEAMFALL_CROSSREPO_FIXTURE_SUPERVISED=1 \
BEAMFALL_CROSSREPO_PRODUCER_SOURCE="$DIRTY_PRODUCER" \
BEAMFALL_CROSSREPO_CONSUMER_SOURCE="$DIRTY_CONSUMER" \
TMPDIR="$TMP" "$ROOT/script/crossrepo-conformance.sh" >"$DIRTY_PIN_RED" 2>&1
dirty_pin_status=$?
set -e

if [[ "$dirty_pin_status" -eq 0 ]]; then
  echo "crossrepo-conformance_test: dirty skewed consumer pin was not validated" >&2
  cat "$DIRTY_PIN_RED" >&2
  exit 1
fi
grep -F "APPLY producer tracked working-tree delta" "$DIRTY_PIN_RED" >/dev/null
grep -F "CHECKOUT consumer sha=$DIRTY_CONSUMER_PIN " "$DIRTY_PIN_RED" >/dev/null
grep -F \
  "RESOLVE consumer repo=beamfall-web pinned_sha=$DIRTY_CONSUMER_PIN resolved_sha=$DIRTY_CONSUMER_PIN note=branch-main" \
  "$DIRTY_PIN_RED" >/dev/null
grep -F "verifyCoreWireArtifact" "$DIRTY_PIN_RED" >/dev/null
grep -F "Core wire artifact guard" "$DIRTY_PIN_RED" >/dev/null

echo "crossrepo-conformance_test: OK (consumer=$CONSUMER_PIN; green control; injected producer skew RED rc=$status; dirty pin=$DIRTY_CONSUMER_PIN skew RED rc=$dirty_pin_status)"
