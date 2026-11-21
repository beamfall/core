#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# Every wait below polls for its event (a readiness file, a process exit, a stand-in's exec) and
# returns as soon as it happens, so a fast host finishes in the time the events take. Two factors
# size the bounds:
# - ceiling_scale stretches the ceilings, which a passing case never reaches. It uses the CORE-1062
#   rule of script/web-ui-e2e-smoke_test.sh (1 at or below one runnable task per CPU, load1/ncpu
#   above that, capped at 10); BEAMFALL_WEB_E2E_RUNNER_TEST_LOAD1 overrides load1.
# - scale (BEAMFALL_WEB_E2E_RUNNER_TEST_SCALE, default 1) multiplies everything, the timers a case
#   really waits out included, so a gate can raise every budget at once.
# Ordering the assertions rely on: the hang-mode runner timer is far below the independent harness
# deadline, so "the runner's watchdog fired" (124) stays distinct from "the harness had to kill it".
scale="${BEAMFALL_WEB_E2E_RUNNER_TEST_SCALE:-1}"
case "${scale}" in
  ''|*[!0-9]*|0)
    echo "BEAMFALL_WEB_E2E_RUNNER_TEST_SCALE must be a positive integer" >&2
    exit 2
    ;;
esac
ceiling_scale="$(python3 -c '
import math, os, sys
load1 = float(sys.argv[1]) if sys.argv[1] else os.getloadavg()[0]
print(int(math.ceil(min(10.0, max(1.0, load1 / (os.cpu_count() or 1))))))
' "${BEAMFALL_WEB_E2E_RUNNER_TEST_LOAD1:-}")"
ceiling_scale=$((ceiling_scale * scale))
# Timers a passing case waits out. Hang: the smoke stall budget counts from the fake npm's last
# heartbeat (written up to readiness); the full runner's wall clock counts from launch.
HANG_RUNNER_TIMEOUT_SECONDS=$((10 * scale))
# Stall budget for the slow case. The first fake npm invocation keeps beating for longer than
# this; the runner's own silent steps between invocations (node, python) must fit within it.
SLOW_STALL_SECONDS=$((20 * scale))
# Ceilings. Runner timer for cases that must not time out (success, failure, leftover, livesibling):
LONG_RUNNER_TIMEOUT_SECONDS=$((60 * ceiling_scale))
# Independent backstop that SIGKILLs a case's whole process group; also bounds readiness waits.
HARNESS_DEADLINE_SECONDS=$((90 * ceiling_scale))
# Runner timer for the INT and TERM cases, and the smoke absolute ceiling for every case: past the
# harness deadline, so neither can race the signal.
SIGNAL_RUNNER_TIMEOUT_SECONDS=$((HARNESS_DEADLINE_SECONDS + 60 * ceiling_scale))
# Ceiling for a retired process to disappear.
EXIT_WAIT_SECONDS=$((30 * ceiling_scale))
# Full runner's post-SIGKILL group wait, in its 0.05s ticks (30s per ceiling unit).
KILL_WAIT_TICKS=$((600 * ceiling_scale))
TMP="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-web-e2e-runner-test.XXXXXX")"
ACTIVE_PIDS=()
ACTIVE_GROUPS=()
CURRENT_CASE_METADATA=""
PASS=0
FAIL=0

cleanup() {
  local pgid pid
  trap - EXIT INT TERM
  # CORE-0014: the supervisor moves each tracked command into its own session, which the runner
  # group kill below never reaches. A finished case was already reaped and checked; only the case
  # in flight (the selftest itself was interrupted) can still own live sessions. Its records are
  # fresh, so a recycled pid cannot stand in for one of them.
  if [[ -n "${CURRENT_CASE_METADATA}" ]]; then reap_case_processes "${CURRENT_CASE_METADATA}"; fi
  for pgid in "${ACTIVE_GROUPS[@]+"${ACTIVE_GROUPS[@]}"}"; do
    kill -KILL -- "-${pgid}" 2>/dev/null || true
    wait "${pgid}" 2>/dev/null || true
  done
  for pid in "${ACTIVE_PIDS[@]+"${ACTIVE_PIDS[@]}"}"; do
    kill -KILL "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
  done
  rm -rf "${TMP}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Process groups recorded by a case: every *.group file, and the group of every live *.pid. Never the
# selftest's own group; never looked up by command name (concurrent copies of this test share names).
case_groups() {
  local metadata="$1" file pgid own
  own="$(ps -o pgid= -p "$$" | tr -d ' ')"
  for file in "${metadata}"/*.group "${metadata}"/*.pid; do
    [[ -s "${file}" ]] || continue
    if [[ "${file}" == *.group ]]; then
      pgid="$(<"${file}")"
    else
      pgid="$(ps -o pgid= -p "$(<"${file}")" 2>/dev/null | tr -d ' ' || true)"
    fi
    if [[ "${pgid}" =~ ^[0-9]+$ && "${pgid}" -gt 1 && "${pgid}" != "${own}" ]]; then
      printf '%s\n' "${pgid}"
    fi
  done | sort -u
}

group_alive() {
  ps -A -o pgid= | awk -v g="$1" '$1 == g { found = 1 } END { exit !found }'
}

# SIGKILLs every process a case recorded, by group and by pid. A SIGKILLed runner skips its EXIT
# trap, so this is the only path that stops the sessions its supervisor created.
reap_case_processes() {
  local metadata="$1" pgid file
  for pgid in $(case_groups "${metadata}"); do
    kill -KILL -- "-${pgid}" 2>/dev/null || true
  done
  for file in "${metadata}"/*.pid; do
    if [[ -s "${file}" ]]; then kill -KILL "$(<"${file}")" 2>/dev/null || true; fi
  done
}

ok() {
  PASS=$((PASS + 1))
  printf 'ok   %s\n' "$1"
}

bad() {
  FAIL=$((FAIL + 1))
  printf 'FAIL %s\n' "$1" >&2
}

register_pid() {
  ACTIVE_PIDS[${#ACTIVE_PIDS[@]}]="$1"
}

register_group() {
  ACTIVE_GROUPS[${#ACTIVE_GROUPS[@]}]="$1"
}

forget_pid() {
  local target="$1" pid kept=()
  for pid in "${ACTIVE_PIDS[@]+"${ACTIVE_PIDS[@]}"}"; do
    [[ "${pid}" == "${target}" ]] || kept[${#kept[@]}]="${pid}"
  done
  ACTIVE_PIDS=("${kept[@]+"${kept[@]}"}")
}

forget_group() {
  local target="$1" pgid kept=()
  for pgid in "${ACTIVE_GROUPS[@]+"${ACTIVE_GROUPS[@]}"}"; do
    [[ "${pgid}" == "${target}" ]] || kept[${#kept[@]}]="${pgid}"
  done
  ACTIVE_GROUPS=("${kept[@]+"${kept[@]}"}")
}

# Waits for the file while its owner lives; the harness deadline (which kills the owner) bounds it.
wait_for_file() {
  local file="$1" owner="$2" deadline=$((SECONDS + HARNESS_DEADLINE_SECONDS + 5))
  while [[ ! -e "${file}" && "${SECONDS}" -lt "${deadline}" ]] && kill -0 "${owner}" 2>/dev/null; do
    sleep 0.02
  done
  [[ -e "${file}" ]]
}

# Waits until a just-started stand-in process has exec'd into the server binary name, so the
# runner's process-table snapshot sees the name rather than the pre-exec bash.
wait_for_command() {
  local pid="$1" want="$2" deadline=$((SECONDS + EXIT_WAIT_SECONDS)) command
  while [[ "${SECONDS}" -lt "${deadline}" ]]; do
    command="$(ps -o command= -p "${pid}" 2>/dev/null || true)"
    [[ "${command%% *}" == "${want}" ]] && return 0
    sleep 0.02
  done
  echo "process ${pid} never became ${want}" >&2
  return 1
}

# Distinct free loopback ports chosen at run time (bound together, then released).
free_ports() {
  python3 -c '
import socket
import sys

held = []
for _ in range(int(sys.argv[1])):
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    s.bind(("127.0.0.1", 0))
    held.append(s)
print(" ".join(str(s.getsockname()[1]) for s in held))
for s in held:
    s.close()
' "$1"
}

assert_process_gone() {
  local pid="$1" label="$2" deadline=$((SECONDS + EXIT_WAIT_SECONDS))
  while kill -0 "${pid}" 2>/dev/null && [[ "${SECONDS}" -lt "${deadline}" ]]; do
    sleep 0.02
  done
  if kill -0 "${pid}" 2>/dev/null; then
    bad "${label} survived"
  else
    ok "${label} retired"
  fi
}

mkdir -p "${TMP}/bin"
node --input-type=module - \
  "${ROOT}/internal/web/app/e2e/lib/required-visual-parity-states.mjs" \
  "${TMP}/valid-provenance.json" <<'NODE'
import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const { REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS: required } = await import(
  pathToFileURL(process.argv[2]).href
);
writeFileSync(
  process.argv[3],
  JSON.stringify({
    schemaVersion: 1,
    manifestSHA256: createHash("sha256").update(JSON.stringify(required)).digest("hex"),
    requiredCount: required.length,
    passedCount: required.length,
    states: required.map((id) => ({ id, status: "passed" })),
  }),
);
NODE

cat >"${TMP}/fake-descendant.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
trap '' INT TERM
role="$1"
metadata="$2"
printf '%s\n' "$$" >"${metadata}/${role}.pid"
if [[ "${BEAMFALL_RUNNER_TEST_MODE:?}" == success || "${BEAMFALL_RUNNER_TEST_MODE}" == slow ]]; then
  bash -c 'printf "%s\n" "$$" >"$1"; sleep 0.05' \
    "${role}-child" "${metadata}/${role}-child.pid" &
  wait "$!"
  exit 0
fi
bash -c 'trap "" INT TERM; printf "%s\n" "$$" >"$1"; while :; do sleep 1; done' \
  "${role}-child" "${metadata}/${role}-child.pid" &
while :; do sleep 1; done
SH
chmod +x "${TMP}/fake-descendant.sh"

cat >"${TMP}/bin/npm" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
metadata="${BEAMFALL_RUNNER_TEST_METADATA:?}"
invocation=0
if [[ -s "${metadata}/invocations" ]]; then
  invocation="$(<"${metadata}/invocations")"
fi
invocation=$((invocation + 1))
printf '%s\n' "${invocation}" >"${metadata}/invocations"
printf '%s\n' "$@" >>"${metadata}/args"
printf '%s\n' "${BEAMFALL_E2E_URL:-}" >"${metadata}/url"
printf '%s\n' "${BEAMFALL_WEB_E2E_SMOKE_GATE:-}" >"${metadata}/smoke-gate"
# CORE-0014: the group this invocation runs in (the supervisor's session), so the harness can reap
# it when a case is killed at the deadline.
ps -o pgid= -p "$$" | tr -d ' ' >"${metadata}/npm-${invocation}.group"

# Progress report the smoke runner's stall watchdog reads (the full runner sets no heartbeat).
# Startup beats, so the stall budget counts from readiness and not from how long a loaded host
# took to fork; the hang mode then goes silent.
beat() {
  if [[ -n "${BEAMFALL_WEB_E2E_HEARTBEAT:-}" ]]; then
    printf '%s\n' "${RANDOM}" >"${BEAMFALL_WEB_E2E_HEARTBEAT}" 2>/dev/null || true
  fi
}
beat

# Verification knob: a slowed startup (still reporting progress) delays readiness.
startup_end=$((SECONDS + ${BEAMFALL_RUNNER_TEST_STARTUP_DELAY_SECONDS:-0}))
while [[ "${SECONDS}" -lt "${startup_end}" ]]; do
  beat
  sleep 0.2
done

browser_role="browser-${invocation}"
"${BEAMFALL_RUNNER_TEST_DESCENDANT}" "${browser_role}" "${metadata}" &
browser_pid=$!
if [[ -z "${BEAMFALL_E2E_URL:-}" ]]; then
  server_role="server-${invocation}"
  "${BEAMFALL_RUNNER_TEST_DESCENDANT}" "${server_role}" "${metadata}" &
  server_pid=$!
fi
while [[ ! -s "${metadata}/${browser_role}-child.pid" ]]; do beat; sleep 0.02; done
if [[ -z "${BEAMFALL_E2E_URL:-}" ]]; then
  while [[ ! -s "${metadata}/${server_role}-child.pid" ]]; do beat; sleep 0.02; done
fi
beat
touch "${metadata}/ready"

case "${BEAMFALL_RUNNER_TEST_MODE:?}" in
  success|slow)
    # CORE-0941: a slow run that keeps reporting progress must outlive the stall budget. The
    # first invocation alone runs longer than the budget (SECONDS ticks whole seconds, so
    # stall + 1); the runner's own steps between invocations stay silent and must fit within it.
    if [[ "${BEAMFALL_RUNNER_TEST_MODE}" == slow && "${invocation}" -eq 1 \
      && -n "${BEAMFALL_WEB_E2E_HEARTBEAT:-}" ]]; then
      slow_end=$((SECONDS + ${BEAMFALL_RUNNER_TEST_SLOW_STALL_SECONDS:?} + 1))
      while [[ "${SECONDS}" -lt "${slow_end}" ]]; do
        beat
        sleep 0.2
      done
    fi
    wait "${browser_pid}"
    if [[ -z "${BEAMFALL_E2E_URL:-}" ]]; then
      wait "${server_pid}"
    fi
    if [[ -n "${BEAMFALL_WEB_E2E_SMOKE_PROVENANCE:-}" ]]; then
      cp "${BEAMFALL_RUNNER_TEST_VALID_PROVENANCE}" \
        "${BEAMFALL_WEB_E2E_SMOKE_PROVENANCE}"
    fi
    exit 0
    ;;
  failure|leftover|livesibling) exit 23 ;;
  hang) while :; do sleep 1; done ;;
  *) exit 2 ;;
esac
SH
chmod +x "${TMP}/bin/npm"

# Outlives any scaled run; cleanup kills it.
sleep 86400 &
sentinel_pid=$!
register_pid "${sentinel_pid}"

# Ports are chosen at run time so concurrent gates (and concurrent copies of this selftest) never
# share them: CASE_PORT is the pinned port of the lifecycle cases, whose leftover and sibling
# stand-ins are named after it; the reuse cases get a supplied URL port and a pinned port.
read -r CASE_PORT REUSE_URL_PORT REUSE_PORT <<<"$(free_ports 3)"
CASE_SERVER_BINARY="/tmp/beamfall-e2e-${CASE_PORT}"

run_case() {
  local runner_name="$1" mode="$2" signal="${3:-}" expected="$4"
  # CORE-1063: the load-attribution threshold defaults far above any real host load so the hang
  # cases stay hangs on a loaded machine; 0 makes every watchdog timeout load-attributed.
  local load_attribution="${5:-1000000}"
  local runner="${ROOT}/script/web-ui-e2e-${runner_name}.sh"
  local case_dir="${TMP}/${runner_name}-${mode}-${signal:-none}${5:+-load${5}}"
  local runner_pid watchdog_pid status=0 pid_file pid pgid deadline
  local runner_timeout_seconds="${LONG_RUNNER_TIMEOUT_SECONDS}"
  # Only the silent hang case without a signal tests the runner's timer. The INT and TERM cases
  # test signal handling: their timer outlasts the harness deadline, which bounds the readiness
  # wait, so the timer can never fire before the harness delivers the signal.
  if [[ "${mode}" == hang && -n "${signal}" ]]; then
    runner_timeout_seconds="${SIGNAL_RUNNER_TIMEOUT_SECONDS}"
  elif [[ "${mode}" == hang ]]; then
    runner_timeout_seconds="${HANG_RUNNER_TIMEOUT_SECONDS}"
  elif [[ "${mode}" == slow ]]; then
    runner_timeout_seconds="${SLOW_STALL_SECONDS}"
  fi
  mkdir -p "${case_dir}/metadata" "${case_dir}/output"
  CURRENT_CASE_METADATA="${case_dir}/metadata"

  local leftover_pid=""
  local sibling_pid=""
  if [[ "${mode}" == livesibling ]]; then
    # A live sibling gate on the same pinned port: its server keeps a live parent and is not ours.
    bash -c 'exec -a "$1" sleep 86400' sibling "${CASE_SERVER_BINARY}" &
    sibling_pid=$!
    register_pid "${sibling_pid}"
    wait_for_command "${sibling_pid}" "${CASE_SERVER_BINARY}" || bad "${runner_name} ${mode} sibling stand-in started"
  fi
  if [[ "${mode}" == leftover ]]; then
    # CORE-0941: a server binary orphaned by an earlier killed run holds the port; the runner reaps it.
    # Started from a subshell so it is re-parented to launchd, like a killed run's server.
    ( bash -c 'exec -a "$1" sleep 86400' leftover "${CASE_SERVER_BINARY}" & echo $! >"${case_dir}/leftover.pid" )
    leftover_pid="$(cat "${case_dir}/leftover.pid")"
    register_pid "${leftover_pid}"
    wait_for_command "${leftover_pid}" "${CASE_SERVER_BINARY}" || bad "${runner_name} ${mode} leftover stand-in started"
  fi

  PATH="${TMP}/bin:${PATH}" \
    BEAMFALL_E2E_URL= \
    BEAMFALL_RUNNER_TEST_METADATA="${case_dir}/metadata" \
    BEAMFALL_RUNNER_TEST_DESCENDANT="${TMP}/fake-descendant.sh" \
    BEAMFALL_RUNNER_TEST_MODE="${mode}" \
    BEAMFALL_RUNNER_TEST_VALID_PROVENANCE="${TMP}/valid-provenance.json" \
    BEAMFALL_RUNNER_TEST_SLOW_STALL_SECONDS="${SLOW_STALL_SECONDS}" \
    BEAMFALL_E2E_PORT="${CASE_PORT}" \
    BEAMFALL_WEB_E2E_OUTPUT_DIR="${case_dir}/output" \
    RELEASE_NIGHTLY_EVIDENCE_DIR="${case_dir}/evidence" \
    BEAMFALL_WEB_E2E_RUNNER_TIMEOUT_SECONDS="${runner_timeout_seconds}" \
    BEAMFALL_WEB_E2E_RUNNER_CEILING_SECONDS="${SIGNAL_RUNNER_TIMEOUT_SECONDS}" \
    BEAMFALL_WEB_E2E_LOAD_ATTRIBUTION_PER_CPU="${load_attribution}" \
    BEAMFALL_WEB_E2E_TERM_GRACE_TICKS=2 \
    BEAMFALL_WEB_E2E_KILL_WAIT_TICKS="${KILL_WAIT_TICKS}" \
    python3 -c '
import os
import signal
import sys

os.setsid()
signal.signal(signal.SIGINT, signal.SIG_DFL)
os.execv(sys.argv[1], sys.argv[1:])
' "${runner}" >"${case_dir}/stdout" 2>"${case_dir}/stderr" &
  runner_pid=$!
  register_group "${runner_pid}"

  python3 -c '
import os
import signal
import sys
import time

time.sleep(int(sys.argv[3]))
with open(sys.argv[2], "w", encoding="utf-8"):
    pass
try:
    os.killpg(int(sys.argv[1]), signal.SIGKILL)
except ProcessLookupError:
    pass
' "${runner_pid}" "${case_dir}/harness-timeout" "${HARNESS_DEADLINE_SECONDS}" &
  watchdog_pid=$!
  register_pid "${watchdog_pid}"

  if ! wait_for_file "${case_dir}/metadata/ready" "${runner_pid}"; then
    bad "${runner_name} ${mode} became ready"
    sed -n '1,40p' "${case_dir}/stderr" >&2
  fi
  if [[ -n "${signal}" ]]; then
    kill -"${signal}" "${runner_pid}" 2>/dev/null || true
  fi
  wait "${runner_pid}" || status=$?
  kill -TERM "${watchdog_pid}" 2>/dev/null || true
  wait "${watchdog_pid}" 2>/dev/null || true
  forget_pid "${watchdog_pid}"

  local case_groups_before
  case_groups_before="$(case_groups "${case_dir}/metadata")"
  if [[ -e "${case_dir}/harness-timeout" ]]; then
    bad "${runner_name} ${mode} exceeded the independent test deadline"
    # The deadline SIGKILLed the runner's group only: stop the supervised sessions it left behind.
    reap_case_processes "${case_dir}/metadata"
  fi

  if [[ "${status}" -eq "${expected}" ]]; then
    ok "${runner_name} ${mode} exits ${expected}"
  else
    bad "${runner_name} ${mode} exits ${expected} (got ${status})"
    tail -n 40 "${case_dir}/stderr" >&2
  fi

  for pid_file in "${case_dir}/metadata/"*.pid; do
    [[ -e "${pid_file}" ]] || continue
    pid="$(<"${pid_file}")"
    assert_process_gone "${pid}" "${runner_name} ${mode} $(basename "${pid_file}")"
  done
  # CORE-0014: no process group the case started survives it.
  for pgid in ${case_groups_before}; do
    deadline=$((SECONDS + EXIT_WAIT_SECONDS))
    while group_alive "${pgid}" && [[ "${SECONDS}" -lt "${deadline}" ]]; do sleep 0.02; done
    if group_alive "${pgid}"; then
      bad "${runner_name} ${mode} process group ${pgid} survived"
    else
      ok "${runner_name} ${mode} process group ${pgid} retired"
    fi
  done
  if kill -0 "${sentinel_pid}" 2>/dev/null; then
    ok "${runner_name} ${mode} leaves unrelated process"
  else
    bad "${runner_name} ${mode} killed unrelated process"
  fi
  if [[ "${expected}" -ne 0 && -s "${case_dir}/output/runner-diagnostics.txt" ]]; then
    ok "${runner_name} ${mode} retains diagnostics"
  elif [[ "${expected}" -ne 0 ]]; then
    bad "${runner_name} ${mode} retains diagnostics"
  fi
  if [[ -n "${leftover_pid}" ]]; then
    assert_process_gone "${leftover_pid}" "${runner_name} ${mode} leftover server binary reaped"
    if grep -q "^reaped_leftover_server_pids=.*${leftover_pid}" "${case_dir}/output/runner-diagnostics.txt" 2>/dev/null; then
      ok "${runner_name} ${mode} records reaped pid"
    else
      bad "${runner_name} ${mode} records reaped pid"
    fi
    wait "${leftover_pid}" 2>/dev/null || true
    forget_pid "${leftover_pid}"
  fi
  if [[ -n "${sibling_pid}" ]]; then
    if kill -0 "${sibling_pid}" 2>/dev/null; then
      ok "${runner_name} ${mode} leaves a live sibling server on the pinned port"
    else
      bad "${runner_name} ${mode} killed a live sibling server on the pinned port"
    fi
    kill -KILL "${sibling_pid}" 2>/dev/null || true
    wait "${sibling_pid}" 2>/dev/null || true
    forget_pid "${sibling_pid}"
  fi
  kill -KILL -- "-${runner_pid}" 2>/dev/null || true
  forget_group "${runner_pid}"
  CURRENT_CASE_METADATA=""
}

# CORE-0941: a progressing run (its first invocation alone longer than the stall budget) still passes
# (the smoke's watchdog reads the heartbeat); the silent hang case below still fails closed with
# 124, so progress is what the runner trusts, not elapsed time.
run_case smoke slow "" 0
run_case smoke leftover "" 23
run_case smoke livesibling "" 23
# CORE-1063: the same silent hang on a host at or above the load threshold exits 125, not 124,
# and names itself load-attributed in the retained diagnostics.
run_case smoke hang "" 125 0
if grep -q '^reason=timeout-load$' "${TMP}/smoke-hang-none-load0/output/runner-diagnostics.txt" \
  && grep -q '(load-attributed)' "${TMP}/smoke-hang-none-load0/output/runner-diagnostics.txt"; then
  ok "smoke load-attributed timeout is recorded distinctly"
else
  bad "smoke load-attributed timeout is recorded distinctly"
fi

for runner_name in smoke full; do
  run_case "${runner_name}" success "" 0
  run_case "${runner_name}" failure "" 23
  run_case "${runner_name}" hang "" 124
  run_case "${runner_name}" hang INT 130
  run_case "${runner_name}" hang TERM 143
done
if grep -q '^reason=timeout$' "${TMP}/smoke-hang-none/output/runner-diagnostics.txt" \
  && grep -q '(hang)' "${TMP}/smoke-hang-none/output/runner-diagnostics.txt"; then
  ok "smoke timeout below the load threshold is recorded as a hang"
else
  bad "smoke timeout below the load threshold is recorded as a hang"
fi

for runner_name in smoke full; do
  reuse_dir="${TMP}/${runner_name}-reuse"
  mkdir -p "${reuse_dir}/output"
  status=0
  for attempt in 1 2; do
    attempt_metadata="${reuse_dir}/metadata-${attempt}"
    mkdir -p "${attempt_metadata}"
    PATH="${TMP}/bin:${PATH}" \
      BEAMFALL_E2E_URL="http://127.0.0.1:${REUSE_URL_PORT}" \
      BEAMFALL_RUNNER_TEST_METADATA="${attempt_metadata}" \
      BEAMFALL_RUNNER_TEST_DESCENDANT="${TMP}/fake-descendant.sh" \
      BEAMFALL_RUNNER_TEST_MODE=success \
      BEAMFALL_RUNNER_TEST_VALID_PROVENANCE="${TMP}/valid-provenance.json" \
      BEAMFALL_E2E_PORT="${REUSE_PORT}" \
      BEAMFALL_WEB_E2E_OUTPUT_DIR="${reuse_dir}/output" \
      RELEASE_NIGHTLY_EVIDENCE_DIR="${reuse_dir}/evidence-${attempt}" \
      BEAMFALL_WEB_E2E_TERM_GRACE_TICKS=2 \
      BEAMFALL_WEB_E2E_KILL_WAIT_TICKS="${KILL_WAIT_TICKS}" \
      "${ROOT}/script/web-ui-e2e-${runner_name}.sh" \
      >"${reuse_dir}/stdout-${attempt}" 2>"${reuse_dir}/stderr-${attempt}" \
      || status=$?
  done
  final_metadata="${reuse_dir}/metadata-2"
  if [[ "${status}" -eq 0 ]] \
    && [[ "$(<"${final_metadata}/url")" == "http://127.0.0.1:${REUSE_URL_PORT}" ]] \
    && ! compgen -G "${final_metadata}/server-*.pid" >/dev/null; then
    ok "${runner_name} reuses supplied URL and output directory without local server"
  else
    bad "${runner_name} reuses supplied URL and output directory without local server"
  fi
  if [[ "${runner_name}" != smoke ]] || [[ -z "$(<"${final_metadata}/smoke-gate")" ]]; then
    ok "${runner_name} external URL does not require owned-server gate"
  else
    bad "${runner_name} external URL retained owned-server gate"
  fi
done

smoke_args="${TMP}/smoke-success-none/metadata/args"
full_args="${TMP}/full-success-none/metadata/args"
if grep -Fxq -- "--grep" "${smoke_args}" \
  && grep -Fxq -- "console|manage|smoke|accessibility variant|non-English browser locale|auth boundary|auth matrix" "${smoke_args}"; then
  ok "smoke preserves its grep coverage"
else
  bad "smoke preserves its grep coverage"
fi
if ! grep -Fxq -- "--grep" "${full_args}"; then
  ok "full runner remains unfiltered"
else
  bad "full runner remains unfiltered"
fi
if [[ "$(<"${TMP}/smoke-success-none/metadata/smoke-gate")" == 1 ]]; then
  ok "smoke owned-server run keeps strict smoke gate"
else
  bad "smoke owned-server run keeps strict smoke gate"
fi

printf '%s passed, %s failed\n' "${PASS}" "${FAIL}"
[[ "${FAIL}" -eq 0 ]]

# Evidence — WEB-RUNNER-LIFECYCLE-1 (2026-08-20)
# Code-under-test HEAD: 9d862fe517d0e138af8a9f30744d0bffab94d133.
# Seeded red (temporary `web-ui-e2e-full.sh` status propagation break, restored before green):
# command: bash script/web-ui-e2e-runner_test.sh >/tmp/WEB-RUNNER-LIFECYCLE-1-seeded-red.log 2>&1
# UTC: 2026-08-20T17:23:23Z..2026-08-20T17:24:14Z; elapsed=51s; exit=1;
# observed: `FAIL full success exits 0 (got 24)`; log=/tmp/WEB-RUNNER-LIFECYCLE-1-seeded-red.log.
# Lifecycle green (success, failure, hang, INT, TERM, descendant retirement, unrelated-process
# preservation, external-URL reuse, and smoke/full coverage assertions):
# command: make web-ui-e2e-runner-test >/tmp/WEB-RUNNER-LIFECYCLE-1-runner-test-green.log 2>&1
# UTC: 2026-08-20T17:35:04Z..2026-08-20T17:35:39Z; elapsed=35s; exit=0;
# observed: `83 passed, 0 failed`; log=/tmp/WEB-RUNNER-LIFECYCLE-1-runner-test-green.log.
# Focused web build/unit/typecheck:
# command: bash -c 'npm --prefix internal/web/app run build && npm --prefix internal/web/app test && npm --prefix internal/web/app run typecheck' >/tmp/WEB-RUNNER-LIFECYCLE-1-web-focused.log 2>&1
# UTC: 2026-08-20T17:21:41Z..2026-08-20T17:22:30Z; elapsed=49s; exit=0;
# observed: `Test Files 92 passed (92)` / `Tests 615 passed (615)`; log=/tmp/WEB-RUNNER-LIFECYCLE-1-web-focused.log.
# Playwright smoke config:
# command: env BEAMFALL_WEB_E2E_SUITE=smoke node --experimental-strip-types --input-type=module -e 'const c=(await import("./internal/web/app/playwright.config.ts")).default; if(c.globalTimeout!==600000) throw new Error(`smoke globalTimeout=${c.globalTimeout}`); if(c.timeout!==undefined) throw new Error(`smoke per-test timeout changed=${c.timeout}`); console.log(`smoke globalTimeout=${c.globalTimeout} perTestTimeout=default projects=${c.projects.length}`)' >/tmp/WEB-RUNNER-LIFECYCLE-1-playwright-smoke-config.log 2>&1
# UTC: 2026-08-20T17:36:10Z..2026-08-20T17:36:21Z; elapsed=11s; exit=0;
# observed: `smoke globalTimeout=600000 perTestTimeout=default projects=11`; log=/tmp/WEB-RUNNER-LIFECYCLE-1-playwright-smoke-config.log.
# Playwright full config:
# command: env BEAMFALL_WEB_E2E_SUITE=full node --experimental-strip-types --input-type=module -e 'const c=(await import("./internal/web/app/playwright.config.ts")).default; if(c.globalTimeout!==1800000) throw new Error(`full globalTimeout=${c.globalTimeout}`); if(c.timeout!==undefined) throw new Error(`full per-test timeout changed=${c.timeout}`); console.log(`full globalTimeout=${c.globalTimeout} perTestTimeout=default projects=${c.projects.length}`)' >/tmp/WEB-RUNNER-LIFECYCLE-1-playwright-full-config.log 2>&1
# UTC: 2026-08-20T17:36:25Z..2026-08-20T17:36:40Z; elapsed=15s; exit=0;
# observed: `full globalTimeout=1800000 perTestTimeout=default projects=11`; log=/tmp/WEB-RUNNER-LIFECYCLE-1-playwright-full-config.log.
# Canonical `make gate` is intentionally not builder-run: this lane's hard rule reserves it for
# the orchestrator, which records the exact post-commit gate receipt.
