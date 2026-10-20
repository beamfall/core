#!/usr/bin/env bash
# shellcheck disable=SC2329 # Lifecycle helpers are reached through signal traps.
# web-ui-e2e-full.sh — full Playwright E2E suite over the embedded web UI.
#
# Runs ALL tests (no --grep filter), as opposed to the smoke slice in
# web-ui-e2e-smoke.sh which targets the subset marked for the fast gate.
# Intended for the release gate and nightly CI where full coverage is required.
#
# Usage: script/web-ui-e2e-full.sh [playwright flags...]
#
# Environment:
#   BEAMFALL_E2E_URL   if set, reuse an already-running server at that URL
#                      instead of booting a fresh one (see playwright.config.ts)
#   BEAMFALL_WEB_E2E_OUTPUT_DIR
#                      override the Playwright output/artifact directory
#
# Exit codes: 0 all tests pass, non-zero on any failure.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}/internal/web/app"

suite="${BEAMFALL_WEB_E2E_SUITE:-full}"
case "${suite}" in
  smoke)
    global_timeout_seconds=600
    runner_timeout_seconds=630
    output_prefix=beamfall-web-e2e
    ;;
  full)
    global_timeout_seconds=1800
    runner_timeout_seconds=1830
    output_prefix=beamfall-web-e2e-full
    unset BEAMFALL_WEB_E2E_SMOKE_GATE
    ;;
  *)
    echo "unknown BEAMFALL_WEB_E2E_SUITE=${suite}" >&2
    exit 2
    ;;
esac
export BEAMFALL_WEB_E2E_SUITE="${suite}"
export BEAMFALL_WEB_E2E_RENDER_PHASE=1
export LANG=en_US.UTF-8
export LC_ALL=en_US.UTF-8
export TZ=UTC

# The shell deadline is a final backstop after Playwright's suite timeout. The
# focused lifecycle regression shortens it; production callers use the fixed
# values above.
runner_timeout_seconds="${BEAMFALL_WEB_E2E_RUNNER_TIMEOUT_SECONDS:-${runner_timeout_seconds}}"
case "${runner_timeout_seconds}" in
  ''|*[!0-9]*|0)
    echo "BEAMFALL_WEB_E2E_RUNNER_TIMEOUT_SECONDS must be a positive integer" >&2
    exit 2
    ;;
esac

cleanup_output=0
output_dir="${BEAMFALL_WEB_E2E_OUTPUT_DIR:-}"
if [[ -z "${output_dir}" ]]; then
  output_dir="$(mktemp -d "${TMPDIR:-/tmp}/${output_prefix}.XXXXXX")"
  cleanup_output=1
fi
mkdir -p "${output_dir}"

runner_pid=""
runner_pgid=""
runner_group_file="${output_dir}/runner-process-group"
rm -f -- "${runner_group_file}"
watchdog_pid=""
exit_reason="exit"

group_alive() {
  [[ -n "${1}" ]] && kill -0 -- "-${1}" 2>/dev/null
}

record_diagnostics() {
  local status="$1"
  {
    printf 'suite=%s\n' "${suite}"
    printf 'status=%s\n' "${status}"
    printf 'reason=%s\n' "${exit_reason}"
    printf 'global_timeout_seconds=%s\n' "${global_timeout_seconds}"
    printf 'runner_timeout_seconds=%s\n' "${runner_timeout_seconds}"
    printf 'runner_pid=%s\n' "${runner_pid}"
    printf 'runner_pgid=%s\n' "${runner_pgid}"
    printf 'process_inventory:\n'
    ps -axo pid=,ppid=,pgid=,stat=,command= 2>/dev/null \
      | awk -v owned="${runner_pgid}" '$3 == owned' || true
  } >>"${output_dir}/runner-diagnostics.txt"
}

resolve_runner_group() {
  local recorded=""
  [[ -z "${runner_pgid}" ]] || return 0
  [[ -s "${runner_group_file}" ]] || return 0
  recorded="$(<"${runner_group_file}")"
  [[ "${recorded}" == "${runner_pid}" ]] || return 1
  runner_pgid="${recorded}"
}

retire_runner_group() {
  local ticks term_ticks kill_ticks
  resolve_runner_group || {
    echo "Playwright runner recorded an invalid process group" >&2
    return 1
  }
  [[ -n "${runner_pid}" ]] || return 0

  if [[ -z "${runner_pgid}" ]] && kill -0 "${runner_pid}" 2>/dev/null; then
    # The session handshake is written before npm execs or can create
    # descendants. An interruption before that handshake owns only this PID.
    kill -TERM "${runner_pid}" 2>/dev/null || true
    sleep 0.05
    kill -KILL "${runner_pid}" 2>/dev/null || true
  fi

  if [[ -n "${runner_pgid}" ]] && group_alive "${runner_pgid}"; then
    kill -TERM -- "-${runner_pgid}" 2>/dev/null || true
    kill -CONT -- "-${runner_pgid}" 2>/dev/null || true
    term_ticks="${BEAMFALL_WEB_E2E_TERM_GRACE_TICKS:-40}"
    case "${term_ticks}" in ''|*[!0-9]*) term_ticks=40 ;; esac
    ticks="${term_ticks}"
    while [[ "${ticks}" -gt 0 ]] && group_alive "${runner_pgid}"; do
      sleep 0.05
      ticks=$((ticks - 1))
    done
    if group_alive "${runner_pgid}"; then
      kill -KILL -- "-${runner_pgid}" 2>/dev/null || true
    fi
  fi

  wait "${runner_pid}" 2>/dev/null || true

  if [[ -n "${runner_pgid}" ]]; then
    kill_ticks="${BEAMFALL_WEB_E2E_KILL_WAIT_TICKS:-40}"
    case "${kill_ticks}" in ''|*[!0-9]*) kill_ticks=40 ;; esac
    ticks="${kill_ticks}"
    while [[ "${ticks}" -gt 0 ]] && group_alive "${runner_pgid}"; do
      sleep 0.05
      ticks=$((ticks - 1))
    done
    if group_alive "${runner_pgid}"; then
      echo "Playwright runner process group ${runner_pgid} survived cleanup" >&2
      return 1
    fi
  fi
  runner_pid=""
  runner_pgid=""
}

stop_watchdog() {
  [[ -n "${watchdog_pid}" ]] || return 0
  kill -TERM "${watchdog_pid}" 2>/dev/null || true
  wait "${watchdog_pid}" 2>/dev/null || true
  watchdog_pid=""
}

cleanup() {
  local status=$? cleanup_status=0
  trap - EXIT INT TERM ALRM
  trap '' INT TERM ALRM
  set +e
  stop_watchdog
  if [[ "${status}" -ne 0 ]]; then
    record_diagnostics "${status}"
  fi
  retire_runner_group || cleanup_status=$?
  if [[ "${status}" -eq 0 && "${cleanup_status}" -ne 0 ]]; then
    status="${cleanup_status}"
    exit_reason=cleanup
    record_diagnostics "${status}"
  fi
  if [[ "${cleanup_output}" -eq 1 && "${status}" -eq 0 ]]; then
    rm -rf "${output_dir}"
  elif [[ "${cleanup_output}" -eq 1 ]]; then
    echo "Playwright artifacts kept at ${output_dir}" >&2
  fi
  exit "${status}"
}

trap cleanup EXIT
trap 'exit_reason=INT; exit 130' INT
trap 'exit_reason=TERM; exit 143' TERM
trap 'exit_reason=timeout; echo "Playwright runner exceeded ${runner_timeout_seconds}s; artifacts and process diagnostics will be retained" >&2; exit 124' ALRM

python3 -c '
import os
import sys

group_file = sys.argv[1]
command = sys.argv[2:]
os.setsid()
with open(group_file, "w", encoding="utf-8") as handle:
    handle.write(str(os.getpgrp()))
os.execvp(command[0], command)
' "${runner_group_file}" npm run e2e -- --output "${output_dir}" "$@" &
runner_pid=$!

launch_ticks=100
while [[ ! -s "${runner_group_file}" && "${launch_ticks}" -gt 0 ]] && kill -0 "${runner_pid}" 2>/dev/null; do
  sleep 0.01
  launch_ticks=$((launch_ticks - 1))
done
if ! resolve_runner_group || [[ -z "${runner_pgid}" ]]; then
  exit_reason=launch
  echo "Playwright runner failed to establish an isolated process group" >&2
  exit 1
fi

python3 -c '
import os
import signal
import sys
import time

time.sleep(int(sys.argv[1]))
os.kill(int(sys.argv[2]), signal.SIGALRM)
' "${runner_timeout_seconds}" "$$" &
watchdog_pid=$!

runner_status=0
wait "${runner_pid}" || runner_status=$?
stop_watchdog
exit "${runner_status}"
