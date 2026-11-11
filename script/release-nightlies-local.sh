#!/usr/bin/env bash
# Local producer for the six nightly freshness tiers the release gate checks
# (CORE-0539). Hosted CI is disabled, so the release ceremony runs each tier's
# existing local command against the current checkout and records the result
# through `script/nightly-freshness.sh record`, with provenance naming the
# command and its exit status. See docs/store/release-checklist.md for where
# this sits in the ceremony.
#
#   fuzz-nightly        make fuzz-active FUZZ_TIME=5m  (active fuzzing, as the nightly workflow)
#   mutation-nightly    script/mutation-test.sh named  (green only on COMPLETE)
#   race-nightly        script/race-test.sh full
#   ui-catalog-nightly  make ui-catalog
#   vuln-nightly        make vuln-check
#   web-compat-nightly  script/web-compat-nightly.sh   (publishes its own green)
#
# A tier that fails is recorded red; nothing here ever records a green for a
# tier that did not run and pass. web-compat-nightly's green is left to its
# owning script (the record verb refuses a hand-written one); this runner only
# records it red when that script fails or exits 0 without publishing.
#
# Usage: script/release-nightlies-local.sh [--tier NAME ...]
#   With no --tier, every tier runs. The run always ends with
#   `script/nightly-freshness.sh check` over all six tiers.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FRESHNESS="$ROOT/script/nightly-freshness.sh"
EVIDENCE_DIR="${RELEASE_NIGHTLY_EVIDENCE_DIR:-$ROOT/.agent-evidence/nightly-last-green}"
# Tiers run from different directories (web-compat-nightly.sh changes into
# internal/web/app), so a relative override must not be re-resolved per tier.
case "$EVIDENCE_DIR" in /*) ;; *) EVIDENCE_DIR="$PWD/$EVIDENCE_DIR" ;; esac
export RELEASE_NIGHTLY_EVIDENCE_DIR="$EVIDENCE_DIR"
ALL_TIERS=(fuzz-nightly mutation-nightly race-nightly ui-catalog-nightly vuln-nightly web-compat-nightly)

# The tiers run in ROOT, so their records must bind ROOT's HEAD.
export NIGHTLY_FRESHNESS_SOURCE_REPO="$ROOT"

usage() {
  echo "usage: script/release-nightlies-local.sh [--tier NAME ...]" >&2
  echo "tiers: ${ALL_TIERS[*]}" >&2
}

tier_command() {
  case "$1" in
    fuzz-nightly) TIER_CMD=(make fuzz-active FUZZ_TIME=5m) ;;
    mutation-nightly) TIER_CMD=(script/mutation-test.sh named) ;;
    race-nightly) TIER_CMD=(script/race-test.sh full) ;;
    ui-catalog-nightly) TIER_CMD=(make ui-catalog) ;;
    vuln-nightly) TIER_CMD=(make vuln-check) ;;
    web-compat-nightly) TIER_CMD=(script/web-compat-nightly.sh) ;;
    *) return 1 ;;
  esac
}

selected=()
while [ $# -gt 0 ]; do
  case "$1" in
    --tier)
      if [ $# -lt 2 ] || ! tier_command "$2"; then
        usage
        exit 2
      fi
      selected+=("$2")
      shift 2
      ;;
    -h|--help) usage; exit 0 ;;
    *) usage; exit 2 ;;
  esac
done
[ "${#selected[@]}" -gt 0 ] || selected=("${ALL_TIERS[@]}")

head_commit() { git -C "$ROOT" rev-parse HEAD; }

start_head="$(head_commit)"
dirty="$(git -C "$ROOT" status --porcelain --untracked-files=normal)"
if [ -n "$dirty" ]; then
  echo "FAIL release-nightlies-local worktree has uncommitted changes; records would bind $start_head without testing it" >&2
  printf '%s\n' "$dirty" >&2
  exit 1
fi

log_root="${AGENT_EVIDENCE_DIR:-$ROOT/.agent-evidence}/release-nightlies-local"
mkdir -p "$log_root"
log_dir="$(mktemp -d "$log_root/$(date -u +%Y%m%dT%H%M%SZ).XXXXXX")"
echo "release-nightlies-local: commit=$start_head logs=$log_dir" >&2

now_utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }

# write_provenance FILE TIER COMMAND EXIT STARTED FINISHED LOG REASON [MUTATION_STATUS]
write_provenance() {
  python3 - "$@" "$(uname -s)" "$(uname -r)" "$(uname -m)" <<'PY'
import json
import sys

(path, tier, command, exit_status, started, finished, log, reason, mutation_status,
 host_os, host_release, host_arch) = sys.argv[1:]
payload = {
    "schemaVersion": 1,
    "producer": "script/release-nightlies-local.sh",
    "tier": tier,
    "command": command,
    "exitStatus": int(exit_status),
    "startedAt": started,
    "finishedAt": finished,
    "host": {"os": host_os, "release": host_release, "arch": host_arch},
    "log": log,
}
if reason:
    payload["reason"] = reason
if mutation_status:
    payload["mutationStatus"] = mutation_status
with open(path, "w", encoding="utf-8") as destination:
    json.dump(payload, destination, indent=2)
    destination.write("\n")
PY
}

mutation_status() {
  python3 - "$1" <<'PY'
import json
import sys

try:
    with open(sys.argv[1], encoding="utf-8") as source:
        status = json.load(source).get("status")
except (OSError, ValueError, AttributeError):
    status = None
print(status if isinstance(status, str) and status else "MISSING")
PY
}

# Exit 0 only when the owning script left a receipt for this commit, written
# no earlier than this run's start (both stamps are second-resolution UTC).
web_compat_published() {
  python3 - "$EVIDENCE_DIR/web-compat-nightly.json" "$1" "$2" <<'PY'
import json
import sys

path, commit, started = sys.argv[1:]
try:
    with open(path, encoding="utf-8") as source:
        data = json.load(source)
except (OSError, ValueError):
    raise SystemExit(1)
fresh = (
    isinstance(data, dict)
    and data.get("status") == "green"
    and data.get("tier") == "web-compat-nightly"
    and data.get("sourceCommit") == commit
    and isinstance(data.get("timestamp"), str)
    and data["timestamp"] >= started
)
raise SystemExit(0 if fresh else 1)
PY
}

failed=()
for tier in "${selected[@]}"; do
  tier_command "$tier"
  command_text="${TIER_CMD[*]}"
  log="$log_dir/$tier.log"
  mutation_dir="$log_dir/mutation-run"
  echo "==> [release-nightlies-local] $tier: $command_text" >&2
  started="$(now_utc)"
  set +e
  (
    cd "$ROOT"
    if [ "$tier" = "mutation-nightly" ]; then
      export MUTATION_EVIDENCE_DIR="$mutation_dir"
    fi
    exec "${TIER_CMD[@]}" </dev/null
  ) 2>&1 | tee "$log"
  exit_status="${PIPESTATUS[0]}"
  set -e
  finished="$(now_utc)"

  current_head="$(head_commit)"
  if [ "$current_head" != "$start_head" ]; then
    echo "FAIL release-nightlies-local HEAD moved during $tier before=$start_head after=$current_head; nothing recorded for it" >&2
    exit 1
  fi

  status=red reason="" mutation=""
  if [ "$exit_status" -ne 0 ]; then
    reason="command exited $exit_status"
  elif [ "$tier" = "mutation-nightly" ]; then
    mutation="$(mutation_status "$mutation_dir/status.json")"
    if [ "$mutation" = "COMPLETE" ]; then
      status=green
    else
      reason="mutation run status $mutation, not COMPLETE"
    fi
  elif [ "$tier" = "web-compat-nightly" ]; then
    if web_compat_published "$start_head" "$started"; then
      status=green
    else
      reason="command exited 0 without publishing a web-compat-nightly record for $start_head"
    fi
  else
    status=green
  fi

  if [ "$tier" = "web-compat-nightly" ] && [ "$status" = "green" ]; then
    # Already published by script/web-compat-nightly.sh with its own provenance.
    printf 'PASS %s published-by=script/web-compat-nightly.sh\n' "$tier" >&2
    continue
  fi
  provenance="$log_dir/$tier.provenance.json"
  write_provenance "$provenance" "$tier" "$command_text" "$exit_status" "$started" "$finished" "$log" "$reason" "$mutation"
  "$FRESHNESS" record --tier "$tier" --status "$status" --provenance "$provenance" >/dev/null
  if [ "$status" = "green" ]; then
    printf 'PASS %s exit=%s\n' "$tier" "$exit_status" >&2
  else
    printf 'FAIL %s recorded red: %s\n' "$tier" "$reason" >&2
    failed+=("$tier")
  fi
done

check_args=()
for tier in "${ALL_TIERS[@]}"; do
  check_args+=(--tier "$tier")
done
check_status=0
"$FRESHNESS" check "${check_args[@]}" || check_status=$?

if [ "${#failed[@]}" -gt 0 ]; then
  echo "release-nightlies-local: FAIL red tiers: ${failed[*]} (logs=$log_dir)" >&2
  exit 1
fi
if [ "$check_status" -ne 0 ]; then
  echo "release-nightlies-local: FAIL nightly-freshness check exited $check_status (logs=$log_dir)" >&2
  exit 1
fi
echo "release-nightlies-local: PASS commit=$start_head logs=$log_dir" >&2
