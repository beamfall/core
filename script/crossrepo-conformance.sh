#!/usr/bin/env bash
# ADR-0122 Tier 2: check out the Core producer and a real web consumer, then
# execute the consumer's wire-contract verifier against the producer artifact.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ "${BEAMFALL_CROSSREPO_RUNNER_SUPERVISED:-0}" != "1" ]]; then
  TMP="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-crossrepo-conformance.XXXXXX")"
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
  BEAMFALL_CROSSREPO_TMP="$TMP" \
    python3 "$ROOT/script/test-process-supervisor.py" \
      BEAMFALL_CROSSREPO_RUNNER_SUPERVISED \
      "$ROOT/script/crossrepo-conformance.sh" "$@" &
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

TMP="${BEAMFALL_CROSSREPO_TMP:?supervised runner temp directory is required}"
PRODUCER_SOURCE="${BEAMFALL_CROSSREPO_PRODUCER_SOURCE:-$ROOT}"
PRODUCER_REF="${BEAMFALL_CROSSREPO_PRODUCER_REF:-}"
CONSUMER_REPO="beamfall-web"
CONSUMER_SOURCE="${BEAMFALL_CROSSREPO_CONSUMER_SOURCE:-https://github.com/beamfall/web.git}"
CONSUMER_PIN_REGISTRY="testing/release-gates/gate-commands.tsv"
CONSUMER_PIN_NOTE="branch-main"
CONTRACT_REGISTRY="contracts/registry.json"
CONTRACT_BOUNDARY="client-core-wire"
CONSUMER_CONTRACT_BINDING="test/core-wire/client-core-wire.generated.json"
INJECT_SKEW=0

usage() {
  echo "usage: script/crossrepo-conformance.sh [--inject-skew]" >&2
}

fail() {
  echo "crossrepo-conformance: $*" >&2
  exit 1
}

while (($#)); do
  case "$1" in
    --inject-skew) INJECT_SKEW=1 ;;
    -h|--help) usage; exit 0 ;;
    *) usage; exit 2 ;;
  esac
  shift
done

if [[ "${BEAMFALL_CROSSREPO_BLOCKING_GATE:-0}" == "1" ]]; then
  [[ -z "${BEAMFALL_CROSSREPO_PRODUCER_SOURCE:-}" ]] \
    || fail "producer overrides are forbidden in the blocking gate"
  [[ -z "${BEAMFALL_CROSSREPO_PRODUCER_REF:-}" ]] \
    || fail "producer overrides are forbidden in the blocking gate"
elif [[ "${BEAMFALL_CROSSREPO_FIXTURE_SUPERVISED:-0}" != "1" ]]; then
  [[ -z "${BEAMFALL_CROSSREPO_PRODUCER_SOURCE:-}" ]] \
    || fail "producer overrides require BEAMFALL_CROSSREPO_FIXTURE_SUPERVISED=1"
  [[ -z "${BEAMFALL_CROSSREPO_PRODUCER_REF:-}" ]] \
    || fail "producer overrides require BEAMFALL_CROSSREPO_FIXTURE_SUPERVISED=1"
fi

[[ -z "${BEAMFALL_CROSSREPO_CONSUMER_REF:-}" ]] \
  || fail "runtime consumer ref overrides are forbidden; use the committed $CONSUMER_PIN_REGISTRY pin"

source_ref() {
  local source="$1" requested="$2"
  if [[ -n "$requested" ]]; then
    printf '%s\n' "$requested"
  elif git -C "$source" rev-parse HEAD >/dev/null 2>&1; then
    git -C "$source" rev-parse HEAD
  else
    printf '%s\n' HEAD
  fi
}

checkout_repo() {
  local label="$1" source="$2" requested_ref="$3" destination="$4"
  local ref sha
  ref="$(source_ref "$source" "$requested_ref")"
  if ! git clone --quiet --no-checkout "$source" "$destination"; then
    fail "$label checkout source unavailable: source=$source ref=$ref"
  fi
  if ! git -C "$destination" checkout --quiet --detach "$ref"; then
    if [[ "$label" == consumer ]]; then
      fail "consumer pin unresolvable: pinned_sha=$ref source=$source"
    fi
    fail "$label ref unresolvable: ref=$ref source=$source"
  fi
  sha="$(git -C "$destination" rev-parse HEAD)"
  printf 'CHECKOUT %s sha=%s source=%s\n' "$label" "$sha" "$source"
}

load_consumer_pin() {
  local registry rows pin
  if ! registry="$(cat "$PRODUCER/$CONSUMER_PIN_REGISTRY" 2>/dev/null)"; then
    fail "committed consumer pin registry missing: $CONSUMER_PIN_REGISTRY"
  fi
  rows="$(printf '%s\n' "$registry" | awk -F '\t' -v repo="$CONSUMER_REPO" '$1 == repo { count++ } END { print count + 0 }')"
  if [[ "$rows" == 0 ]]; then
    fail "committed consumer pin missing for $CONSUMER_REPO in $CONSUMER_PIN_REGISTRY"
  fi
  if [[ "$rows" != 1 ]]; then
    fail "committed consumer pin is ambiguous for $CONSUMER_REPO in $CONSUMER_PIN_REGISTRY"
  fi
  pin="$(printf '%s\n' "$registry" | awk -F '\t' -v repo="$CONSUMER_REPO" '$1 == repo { print $5 }')"
  [[ -n "$pin" ]] \
    || fail "committed consumer pin missing for $CONSUMER_REPO in $CONSUMER_PIN_REGISTRY"
  printf '%s\n' "$pin" | grep -Eq '^[0-9a-f]{40}$' \
    || fail "committed consumer pin invalid for $CONSUMER_REPO: expected immutable 40-hex SHA, got $pin"
  printf '%s\n' "$pin"
}

verify_consumer_contract_version() {
  local consumer_sha="$1"
  python3 - \
    "$PRODUCER/$CONTRACT_REGISTRY" \
    "$CONSUMER/$CONSUMER_CONTRACT_BINDING" \
    "$CONTRACT_BOUNDARY" \
    "$CONSUMER_REPO" \
    "$consumer_sha" <<'PY'
import json
import pathlib
import re
import sys

registry_path, binding_path, boundary_name, consumer_repo, consumer_commit = sys.argv[1:]
semver = re.compile(r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)")


def fail(message):
    print(f"crossrepo-conformance: {message}", file=sys.stderr)
    raise SystemExit(1)


def load_object(path_text, label):
    path = pathlib.Path(path_text)
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        fail(f"{label} contract version unresolved: path={path} consumer_commit={consumer_commit}: {error}")
    if not isinstance(value, dict):
        fail(f"{label} contract version unresolved: path={path} consumer_commit={consumer_commit}: expected object")
    return value


registry = load_object(registry_path, "registry consumer")
boundaries = registry.get("boundaries")
if not isinstance(boundaries, list):
    fail(f"registry consumer contract version unresolved: boundary={boundary_name} consumer_commit={consumer_commit}")
boundary_rows = [
    row for row in boundaries
    if isinstance(row, dict) and row.get("boundary") == boundary_name
]
if len(boundary_rows) != 1:
    fail(
        f"registry consumer contract version unresolved: boundary={boundary_name} "
        f"matching_boundaries={len(boundary_rows)} consumer_commit={consumer_commit}"
    )
consumers = boundary_rows[0].get("consumers")
if not isinstance(consumers, list):
    fail(f"registry consumer contract version unresolved: repo={consumer_repo} consumer_commit={consumer_commit}")
consumer_rows = [
    row for row in consumers
    if isinstance(row, dict) and row.get("repo") == consumer_repo
]
if len(consumer_rows) != 1:
    fail(
        f"registry consumer contract version unresolved: repo={consumer_repo} "
        f"matching_consumers={len(consumer_rows)} consumer_commit={consumer_commit}"
    )
registry_version = consumer_rows[0].get("pinnedVersion")
if not isinstance(registry_version, str) or semver.fullmatch(registry_version) is None:
    fail(
        f"registry consumer contract version unresolved: repo={consumer_repo} "
        f"pinned_version={registry_version!r} consumer_commit={consumer_commit}"
    )

binding = load_object(binding_path, "checked-out consumer")
consumer_version = binding.get("artifactVersion")
if not isinstance(consumer_version, str) or semver.fullmatch(consumer_version) is None:
    fail(
        f"checked-out consumer contract version unresolved: repo={consumer_repo} "
        f"artifact_version={consumer_version!r} consumer_commit={consumer_commit}"
    )
if registry_version != consumer_version:
    fail(
        f"consumer contract version mismatch: registry_pinned_version={registry_version} "
        f"consumer_contract_version={consumer_version} consumer_commit={consumer_commit}"
    )
print(
    f"CONTRACT consumer repo={consumer_repo} registry_pinned_version={registry_version} "
    f"consumer_contract_version={consumer_version} consumer_commit={consumer_commit}"
)
PY
}

PRODUCER="$TMP/producer"
CONSUMER="$TMP/consumer"
checkout_repo producer "$PRODUCER_SOURCE" "$PRODUCER_REF" "$PRODUCER"

# A local gate may run before commit. Preserve checkout isolation while applying
# the producer's tracked working-tree delta so the consumer verifies the change
# being gated, not merely the producer's previous HEAD.
if [[ -z "$PRODUCER_REF" ]] && git -C "$PRODUCER_SOURCE" diff --quiet HEAD -- 2>/dev/null; then
  :
elif [[ -z "$PRODUCER_REF" ]] && git -C "$PRODUCER_SOURCE" rev-parse HEAD >/dev/null 2>&1; then
  git -C "$PRODUCER_SOURCE" diff --binary HEAD -- | git -C "$PRODUCER" apply --whitespace=nowarn
  echo "APPLY producer tracked working-tree delta"
fi

CONSUMER_PIN="$(load_consumer_pin)"
printf 'PIN consumer repo=%s pinned_sha=%s note=%s authority=%s\n' \
  "$CONSUMER_REPO" "$CONSUMER_PIN" "$CONSUMER_PIN_NOTE" "$CONSUMER_PIN_REGISTRY"
checkout_repo consumer "$CONSUMER_SOURCE" "$CONSUMER_PIN" "$CONSUMER"
RESOLVED_CONSUMER_SHA="$(git -C "$CONSUMER" rev-parse HEAD)"
[[ "$RESOLVED_CONSUMER_SHA" == "$CONSUMER_PIN" ]] \
  || fail "consumer pin mismatch: pinned_sha=$CONSUMER_PIN resolved_sha=$RESOLVED_CONSUMER_SHA"
printf 'RESOLVE consumer repo=%s pinned_sha=%s resolved_sha=%s note=%s\n' \
  "$CONSUMER_REPO" "$CONSUMER_PIN" "$RESOLVED_CONSUMER_SHA" "$CONSUMER_PIN_NOTE"
verify_consumer_contract_version "$RESOLVED_CONSUMER_SHA"

command -v npm >/dev/null 2>&1 || {
  echo "crossrepo-conformance: npm is required to install the consumer verifier" >&2
  exit 1
}
(
  cd "$CONSUMER"
  TMPDIR="$TMP" npm ci --no-audit --no-fund
)

VITEST="$CONSUMER/node_modules/.bin/vitest"
[[ -x "$VITEST" ]] || {
  echo "crossrepo-conformance: consumer verifier missing at $VITEST" >&2
  exit 1
}
[[ -f "$CONSUMER/test/coreWireContract.test.ts" ]] || {
  echo "crossrepo-conformance: consumer wire test is missing" >&2
  exit 1
}
[[ -x "$CONSUMER/script/wire-drift-red.sh" ]] || {
  echo "crossrepo-conformance: consumer RED-proof runner is missing" >&2
  exit 1
}

if ((INJECT_SKEW)); then
  ARTIFACT_REL="$(
    python3 - "$CONSUMER/test/core-wire/client-core-wire.generated.json" <<'PY'
import json
import pathlib
import sys

binding = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
artifact = binding.get("artifact", "")
if not isinstance(artifact, str) or not artifact.startswith("internal/conformance/wire-contract-v"):
    raise SystemExit(f"invalid consumer artifact binding: {artifact!r}")
print(artifact)
PY
  )"
  ARTIFACT="$PRODUCER/$ARTIFACT_REL"
  [[ -f "$ARTIFACT" ]] || {
    echo "crossrepo-conformance: producer artifact is missing: $ARTIFACT_REL" >&2
    exit 1
  }
  printf ' ' >>"$ARTIFACT"
  echo "INJECT producer artifact SHA skew: $ARTIFACT_REL"
fi

echo "RUN producer->consumer wire conformance"
(
  cd "$CONSUMER"
  BEAMFALL_SERVER_REPO="$PRODUCER" "$VITEST" run test/coreWireContract.test.ts
)

echo "RUN consumer injected-drift RED proof"
BEAMFALL_SERVER_REPO="$PRODUCER" "$CONSUMER/script/wire-drift-red.sh"
echo "PASS ADR-0122 producer->consumer wire conformance"
