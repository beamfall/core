#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROCESS_SUPERVISOR="${BEAMFALL_PROCESS_SUPERVISOR:-$ROOT/script/test-process-supervisor.py}"
active_supervisor_pid=""
launching_supervisor=0
pending_exit=0
watchdog_pid=""
exit_reason="exit"
cleanup_output=0
output_dir="${BEAMFALL_WEB_E2E_OUTPUT_DIR:-}"
pending_receipt=""
receipt_file=""
publisher_file=""
global_timeout_seconds=600
runner_timeout_seconds="${BEAMFALL_WEB_E2E_RUNNER_TIMEOUT_SECONDS:-630}"

case "${runner_timeout_seconds}" in
  ''|*[!0-9]*|0)
    echo "BEAMFALL_WEB_E2E_RUNNER_TIMEOUT_SECONDS must be a positive integer" >&2
    exit 2
    ;;
esac

record_diagnostics() {
  local status="$1"
  {
    printf 'suite=smoke\n'
    printf 'status=%s\n' "${status}"
    printf 'reason=%s\n' "${exit_reason}"
    printf 'global_timeout_seconds=%s\n' "${global_timeout_seconds}"
    printf 'runner_timeout_seconds=%s\n' "${runner_timeout_seconds}"
    printf 'active_supervisor_pid=%s\n' "${active_supervisor_pid}"
    printf 'process_inventory:\n'
    if [[ -n "${active_supervisor_pid}" ]]; then
      ps -axo pid=,ppid=,pgid=,stat=,command= 2>/dev/null \
        | awk -v owner="${active_supervisor_pid}" '$1 == owner || $2 == owner' || true
    fi
  } >>"${output_dir}/runner-diagnostics.txt"
}

stop_watchdog() {
  [[ -n "${watchdog_pid}" ]] || return 0
  kill -TERM "${watchdog_pid}" 2>/dev/null || true
  wait "${watchdog_pid}" 2>/dev/null || true
  watchdog_pid=""
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM ALRM
  trap '' INT TERM ALRM
  set +e
  stop_watchdog
  if [[ ${status} -ne 0 && -n "${output_dir}" && -d "${output_dir}" ]]; then
    record_diagnostics "${status}"
  fi
  if [[ -n "${active_supervisor_pid}" ]]; then
    kill -TERM "${active_supervisor_pid}" 2>/dev/null || true
    wait "${active_supervisor_pid}" 2>/dev/null || true
  fi
  if [[ -n "${pending_receipt}" ]]; then
    rm -f "${pending_receipt}"
  fi
  if [[ -n "${publisher_file}" ]]; then
    rm -f "${publisher_file}"
  fi
  if [[ ${cleanup_output} -eq 1 && ${status} -eq 0 ]]; then
    rm -rf "${output_dir}"
  elif [[ ${cleanup_output} -eq 1 ]]; then
    echo "Playwright artifacts kept at ${output_dir}" >&2
  fi
  exit "${status}"
}
trap cleanup EXIT

on_signal() {
  local status="$1" reason="$2"
  exit_reason="${reason}"
  if [[ ${launching_supervisor} -eq 1 ]]; then
    pending_exit="${status}"
    return
  fi
  exit "${status}"
}

trap 'on_signal 130 INT' INT
trap 'on_signal 143 TERM' TERM
trap 'echo "Playwright smoke runner exceeded ${runner_timeout_seconds}s; artifacts and process diagnostics will be retained" >&2; on_signal 124 timeout' ALRM

if [[ $# -ne 0 ]]; then
  echo "FAIL web-ui-e2e-smoke caller arguments are not supported expected=0 actual=$#" >&2
  exit 1
fi

cd "${ROOT}/internal/web/app"

export BEAMFALL_WEB_E2E_SUITE=smoke
if [[ -n "${BEAMFALL_E2E_URL:-}" ]]; then
  unset BEAMFALL_WEB_E2E_SMOKE_GATE
else
  export BEAMFALL_WEB_E2E_SMOKE_GATE=1
fi
export LANG=en_US.UTF-8
export LC_ALL=en_US.UTF-8
export TZ=UTC

# Gates in sibling worktrees run concurrently and each boots its own server, so
# a fixed port makes them collide. Ask the kernel for a free one unless the
# caller pinned it.
if [[ -z "${BEAMFALL_E2E_PORT:-}" ]]; then
  if [[ -n "${BEAMFALL_E2E_URL:-}" ]]; then
    BEAMFALL_E2E_PORT=8830
  else
    BEAMFALL_E2E_PORT="$(node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{process.stdout.write(String(s.address().port));s.close()})')"
  fi
fi
export BEAMFALL_E2E_PORT
echo "[e2e] isolation: port=${BEAMFALL_E2E_PORT} auth=${ROOT}/internal/web/app/e2e/.auth/${BEAMFALL_E2E_PORT} binary=/tmp/beamfall-e2e-${BEAMFALL_E2E_PORT}"

if [[ -z "${output_dir}" ]]; then
  output_dir="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-web-e2e.XXXXXX")"
  cleanup_output=1
fi
mkdir -p "${output_dir}"

python3 -c '
import os
import signal
import sys
import time

time.sleep(int(sys.argv[1]))
os.kill(int(sys.argv[2]), signal.SIGALRM)
' "${runner_timeout_seconds}" "$$" &
watchdog_pid=$!

run_tracked() {
  local status=0
  launching_supervisor=1
  python3 "${PROCESS_SUPERVISOR}" BEAMFALL_WEB_E2E_SMOKE_CHILD_PROCESS_SUPERVISED "$@" &
  active_supervisor_pid=$!
  launching_supervisor=0
  if [[ ${pending_exit} -ne 0 ]]; then
    exit "${pending_exit}"
  fi
  wait "${active_supervisor_pid}" || status=$?
  active_supervisor_pid=""
  return "${status}"
}

provenance_file="${output_dir}/visual-parity-provenance.json"
rm -f -- "${provenance_file}"
export BEAMFALL_WEB_E2E_SMOKE_PROVENANCE="${provenance_file}"
source_commit="$(git -C "$ROOT" rev-parse HEAD)"
# RENDER_PHASE pins workers=1 so the finish-parity rendering contract test
# (and every visual capture) sees the same deterministic scheduling here as in
# the phased runs below.
run_tracked env BEAMFALL_WEB_E2E_RENDER_PHASE=1 npm run e2e -- \
  --project unauth \
  --project viewer \
  --project player-desktop \
  --project player-mobile \
  --project manage \
  --project console \
  --project text-expansion \
  --project a11y-browser \
  --project auth-matrix \
  --output "${output_dir}" \
  --reporter "list,./e2e/lib/require-visual-parity-states.mjs"
if [[ ! -s "${provenance_file}" ]]; then
  echo "FAIL web-ui-e2e-smoke missing full-inventory provenance=${provenance_file}" >&2
  exit 1
fi
run_tracked node ./e2e/lib/validate-visual-parity-provenance.mjs "${provenance_file}"
current_commit="$(git -C "$ROOT" rev-parse HEAD)"
if [[ "${current_commit}" != "${source_commit}" ]]; then
  echo "FAIL web-ui-e2e-smoke source changed during run before=${source_commit} after=${current_commit}" >&2
  exit 1
fi


render_pattern="visual parity|ADR-0100 a11y real-browser parity pass|ADR-0034 text-expansion browser overflow arm|accessibility variant|finish parity rendering contract"
functional_pattern="console|manage|smoke|accessibility variant|non-English browser locale|auth boundary|auth matrix"

# Screenshot, reflow, and clipping checks share mutable server fixtures with
# the functional smoke. Run them first against a fresh harness and one worker;
# the remaining tests retain their normal parallelism against a second fresh
# harness. This makes the same target independent of finish-gate scheduling.
run_tracked env BEAMFALL_WEB_E2E_RENDER_PHASE=1 npm run e2e -- \
  --output "${output_dir}/render" \
  --grep "${render_pattern}" \
  "$@"
run_tracked npm run e2e -- \
  --output "${output_dir}/functional" \
  --grep "${functional_pattern}" \
  --grep-invert "${render_pattern}" \
  "$@"
# Publish here, inside the command that just completed the unfiltered run. The
# generic freshness recorder intentionally rejects green web-ui-e2e-smoke
# records so a copied provenance payload cannot be re-stamped at a newer HEAD.
evidence_dir="${RELEASE_NIGHTLY_EVIDENCE_DIR:-$ROOT/.agent-evidence/nightly-last-green}"
receipt_file="${evidence_dir}/web-ui-e2e-smoke.json"
timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
mkdir -p "${evidence_dir}"
pending_receipt="$(mktemp "${evidence_dir}/.web-ui-e2e-smoke.XXXXXX")"
publisher_file="$(mktemp "${TMPDIR:-/tmp}/beamfall-web-ui-publisher.XXXXXX.py")"
cat >"${publisher_file}" <<'PY'
import fcntl
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

path, pending, lock_path, root, source_commit, timestamp, provenance_path = sys.argv[1:]
with open(provenance_path, encoding="utf-8") as source:
    provenance = json.load(source)
if not isinstance(provenance, dict):
    raise SystemExit("web-ui smoke provenance must be a JSON object")

def interrupted(signum, _frame):
    raise SystemExit(128 + signum)

def current_head():
    return subprocess.check_output(
        ["git", "-C", root, "rev-parse", "HEAD"], text=True
    ).strip()

for caught_signal in (signal.SIGHUP, signal.SIGINT, signal.SIGTERM):
    signal.signal(caught_signal, interrupted)

installed = False
with open(lock_path, "a+", encoding="utf-8") as publication_lock:
    fcntl.flock(publication_lock, fcntl.LOCK_EX)
    try:
        head = current_head()
        if head != source_commit:
            raise SystemExit(
                f"FAIL web-ui-e2e-smoke source changed before publication "
                f"expected={source_commit} actual={head}"
            )
        with open(pending, "w", encoding="utf-8") as destination:
            json.dump(
                {
                    "commit": source_commit,
                    "sourceCommit": source_commit,
                    "repo": "beamfall",
                    "status": "green",
                    "tier": "web-ui-e2e-smoke",
                    "timestamp": timestamp,
                    "provenance": provenance,
                },
                destination,
                indent=2,
            )
            destination.write("\n")
        blocked_signals = {signal.SIGHUP, signal.SIGINT, signal.SIGTERM}
        previous_mask = signal.pthread_sigmask(signal.SIG_BLOCK, blocked_signals)
        try:
            os.replace(pending, path)
            if os.environ.get("BEAMFALL_WEB_E2E_SIGNAL_AFTER_REPLACE") == "1":
                os.kill(os.getpid(), signal.SIGTERM)
            installed = True
        finally:
            signal.pthread_sigmask(signal.SIG_SETMASK, previous_mask)
        barrier = os.environ.get("BEAMFALL_WEB_E2E_PUBLISH_BARRIER")
        if barrier:
            Path(f"{barrier}.ready").write_text(f"{os.getpid()}\n", encoding="utf-8")
            while not Path(f"{barrier}.go").exists():
                time.sleep(0.01)
        head = current_head()
        if head != source_commit:
            raise SystemExit(
                f"FAIL web-ui-e2e-smoke source changed at publication "
                f"expected={source_commit} actual={head}"
            )
    except BaseException:
        try:
            os.unlink(pending)
        except FileNotFoundError:
            pass
        if installed:
            try:
                os.unlink(path)
            except FileNotFoundError:
                pass
        raise
PY
run_tracked python3 "${publisher_file}" \
  "${receipt_file}" "${pending_receipt}" "${evidence_dir}/.web-ui-e2e-smoke.publish.lock" \
  "${ROOT}" "${source_commit}" "${timestamp}" "${provenance_file}"
rm -f "${publisher_file}"
publisher_file=""
pending_receipt=""
stop_watchdog
echo "${receipt_file}"
