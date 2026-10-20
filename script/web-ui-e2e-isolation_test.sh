#!/usr/bin/env bash
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "${WEB_UI_E2E_ISOLATION_TEST_SUPERVISED:-0}" != 1 ]]; then
  exec python3 "${ROOT}/script/test-process-supervisor.py" \
    WEB_UI_E2E_ISOLATION_TEST_SUPERVISED "$0" "$@"
fi
set -euo pipefail

APP="${ROOT}/internal/web/app"
AUTH_ROOT="${APP}/e2e/.auth"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-web-e2e-isolation.XXXXXX")"
RUNNER_PIDS=""
SERVER_PIDS=""
AUTH_DIRS=""
SERVER_BINS=""
DATA_DIRS=""

fail() {
  echo "FAIL $*" >&2
  exit 1
}

track_port() {
  local port="$1"
  [[ "$port" =~ ^[0-9]+$ ]] || return 0
  AUTH_DIRS="${AUTH_DIRS} ${AUTH_ROOT}/${port}"
  SERVER_BINS="${SERVER_BINS} /tmp/beamfall-e2e-${port}"
}

track_data_dir() {
  local data_dir="$1"
  [[ "$(basename "$data_dir")" == beamfall-e2e-* ]] || return 0
  DATA_DIRS="${DATA_DIRS} ${data_dir}"
}

process_is_alive() {
  kill -0 "$1" 2>/dev/null
}

wait_for_process_exit() {
  local pid="$1" attempts="${2:-400}"
  local attempt
  for attempt in $(seq 1 "$attempts"); do
    process_is_alive "$pid" || return 0
    sleep 0.05
  done
  ! process_is_alive "$pid"
}

cleanup() {
  local status=$? pid path attempt
  trap - EXIT INT TERM
  set +e

  for pid in ${RUNNER_PIDS}; do kill -INT "$pid" 2>/dev/null || true; done
  for attempt in $(seq 1 40); do
    local runner_alive=0
    for pid in ${RUNNER_PIDS}; do process_is_alive "$pid" && runner_alive=1; done
    [[ "$runner_alive" -eq 0 ]] && break
    sleep 0.05
  done
  for pid in ${RUNNER_PIDS}; do kill -KILL "$pid" 2>/dev/null || true; done
  for pid in ${RUNNER_PIDS}; do wait "$pid" 2>/dev/null || true; done

  for pid in ${SERVER_PIDS}; do kill -TERM "$pid" 2>/dev/null || true; done
  for attempt in $(seq 1 100); do
    local server_alive=0
    for pid in ${SERVER_PIDS}; do process_is_alive "$pid" && server_alive=1; done
    [[ "$server_alive" -eq 0 ]] && break
    sleep 0.05
  done
  for pid in ${SERVER_PIDS}; do kill -KILL "$pid" 2>/dev/null || true; done

  for path in ${AUTH_DIRS}; do
    [[ "$path" == "${AUTH_ROOT}/"* ]] && rm -rf -- "$path"
  done
  for path in ${SERVER_BINS}; do
    [[ "$path" == /tmp/beamfall-e2e-[0-9]* ]] && rm -f -- "$path"
  done
  for path in ${DATA_DIRS}; do
    [[ "$(basename "$path")" == beamfall-e2e-* ]] && rm -rf -- "$path"
  done
  rm -rf -- "$TMP"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

free_port() {
  node -e 'const s=require("net").createServer();s.listen(0,"127.0.0.1",()=>{process.stdout.write(String(s.address().port));s.close()})'
}

wait_for_file() {
  local file="$1" attempts="${2:-1800}"
  local attempt
  for attempt in $(seq 1 "$attempts"); do
    [[ -s "$file" ]] && return 0
    sleep 0.05
  done
  [[ -s "$file" ]]
}

selected_port() {
  sed -n 's/.*\[e2e\] isolation: port=\([0-9][0-9]*\).*/\1/p' "$1" | tail -n 1
}

pass_summary() {
  grep -Eo '[0-9]+ passed \([^)]*\)' "$1" | tail -n 1 || true
}

state_value() {
  python3 - "$1" "$2" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    print(json.load(handle)[sys.argv[2]])
PY
}

run_concurrency_regression() {
  local log_a="${TMP}/concurrent-a.log" log_b="${TMP}/concurrent-b.log"
  local out_a="${TMP}/concurrent-a-output" out_b="${TMP}/concurrent-b-output"
  local tmp_a="${TMP}/concurrent-a-tmp" tmp_b="${TMP}/concurrent-b-tmp"
  local runner_a runner_b rc_a rc_b port_a port_b summary_a summary_b
  local auth_a auth_b bin_a bin_b state_a state_b server_a server_b data_a data_b

  mkdir -p "$out_a" "$out_b" "$tmp_a" "$tmp_b"
  env -u BEAMFALL_E2E_PORT \
    TMPDIR="$tmp_a" \
    BEAMFALL_WEB_E2E_OUTPUT_DIR="$out_a" \
    "${ROOT}/script/web-ui-e2e-smoke.sh" >"$log_a" 2>&1 &
  runner_a=$!
  RUNNER_PIDS="${RUNNER_PIDS} ${runner_a}"
  env -u BEAMFALL_E2E_PORT \
    TMPDIR="$tmp_b" \
    BEAMFALL_WEB_E2E_OUTPUT_DIR="$out_b" \
    "${ROOT}/script/web-ui-e2e-smoke.sh" >"$log_b" 2>&1 &
  runner_b=$!
  RUNNER_PIDS="${RUNNER_PIDS} ${runner_b}"

  set +e
  wait "$runner_a"
  rc_a=$?
  wait "$runner_b"
  rc_b=$?
  set -e

  port_a="$(selected_port "$log_a")"
  port_b="$(selected_port "$log_b")"
  [[ -n "$port_a" && -n "$port_b" ]] || {
    tail -n 30 "$log_a" >&2
    tail -n 30 "$log_b" >&2
    fail "concurrency isolation: one or both runs did not report their selected port"
  }
  track_port "$port_a"
  track_port "$port_b"
  [[ "$port_a" != "$port_b" ]] || fail \
    "concurrency isolation: both runs selected port ${port_a}; port/auth/binary collision"

  [[ "$rc_a" -eq 0 && "$rc_b" -eq 0 ]] || {
    tail -n 30 "$log_a" >&2
    tail -n 30 "$log_b" >&2
    fail "concurrency isolation: runs on ports ${port_a}/${port_b} exited ${rc_a}/${rc_b}"
  }

  summary_a="$(pass_summary "$log_a")"
  summary_b="$(pass_summary "$log_b")"
  [[ -n "$summary_a" && -n "$summary_b" ]] || fail \
    "concurrency isolation: both runs exited zero but did not reach green Playwright summaries"

  auth_a="${AUTH_ROOT}/${port_a}"
  auth_b="${AUTH_ROOT}/${port_b}"
  bin_a="/tmp/beamfall-e2e-${port_a}"
  bin_b="/tmp/beamfall-e2e-${port_b}"
  [[ "$auth_a" != "$auth_b" && -f "$auth_a/state.json" && -f "$auth_b/state.json" ]] || fail \
    "concurrency isolation: distinct auth directories were not created for ports ${port_a}/${port_b}"
  [[ "$bin_a" != "$bin_b" && -x "$bin_a" && -x "$bin_b" ]] || fail \
    "concurrency isolation: distinct server binaries were not created for ports ${port_a}/${port_b}"

  state_a="${auth_a}/state.json"
  state_b="${auth_b}/state.json"
  server_a="$(state_value "$state_a" pid)"
  server_b="$(state_value "$state_b" pid)"
  data_a="$(state_value "$state_a" dataDir)"
  data_b="$(state_value "$state_b" dataDir)"
  SERVER_PIDS="${SERVER_PIDS} ${server_a} ${server_b}"
  track_data_dir "$data_a"
  track_data_dir "$data_b"

  wait_for_process_exit "$server_a" || fail \
    "descendant-free completion: server pid ${server_a} on port ${port_a} survived its run"
  wait_for_process_exit "$server_b" || fail \
    "descendant-free completion: server pid ${server_b} on port ${port_b} survived its run"

  echo "PASS concurrency isolation: ports=${port_a},${port_b} auth=${auth_a},${auth_b} binaries=${bin_a},${bin_b} summaries='${summary_a}','${summary_b}'"
  echo "PASS descendant-free completion: server pids ${server_a}/${server_b} exited"
}

run_teardown_regression() {
  local port auth_dir state_file test_dir test_file config_file log rc
  port="$(free_port)"
  track_port "$port"
  auth_dir="${AUTH_ROOT}/${port}"
  state_file="${auth_dir}/state.json"
  test_dir="${auth_dir}/teardown-probe"
  test_file="${test_dir}/probe.spec.ts"
  config_file="${TMP}/teardown-${port}.config.mjs"
  log="${TMP}/teardown-${port}.log"
  mkdir -p "$auth_dir" "$test_dir"
  echo '{}' >"$state_file"
  chmod 000 "$state_file"
  cat >"$test_file" <<'EOF'
import { test } from "@playwright/test";

test("teardown probe", () => {});
EOF
  cat >"$config_file" <<'EOF'
export default {
  testDir: process.env.BEAMFALL_E2E_TEST_DIR,
  outputDir: process.env.BEAMFALL_E2E_OUTPUT_DIR,
  globalTeardown: process.env.BEAMFALL_E2E_GLOBAL_TEARDOWN,
  reporter: [["line"]],
};
EOF

  set +e
    BEAMFALL_E2E_PORT="$port" \
    BEAMFALL_E2E_TEST_DIR="$test_dir" \
    BEAMFALL_E2E_OUTPUT_DIR="${TMP}/teardown-${port}-output" \
    BEAMFALL_E2E_GLOBAL_TEARDOWN="${APP}/e2e/global-teardown.ts" \
    "${APP}/node_modules/.bin/playwright" test --config "$config_file" >"$log" 2>&1
  rc=$?
  set -e
  chmod 600 "$state_file"

  [[ "$rc" -ne 0 ]] || fail \
    "teardown fails closed: present-but-unreadable state returned silently (exit 0)"
  grep -Fq "exists but is unreadable; a booted server may be leaking" "$log" || {
    cat "$log" >&2
    fail "teardown fails closed: teardown raised the wrong error"
  }
  echo "PASS teardown fails closed: present-but-unreadable ${state_file} raised"
}

run_interruption_regression() {
  local port auth_dir state_file ready_file log output_dir run_tmp runner playwright_pid
  local server_pid data_dir recorded_pid state_before_signal runner_rc
  port="$(free_port)"
  track_port "$port"
  auth_dir="${AUTH_ROOT}/${port}"
  state_file="${auth_dir}/state.json"
  ready_file="${TMP}/interrupt-${port}.ready.json"
  log="${TMP}/interrupt-${port}.log"
  output_dir="${TMP}/interrupt-${port}-output"
  run_tmp="${TMP}/interrupt-${port}-tmp"
  mkdir -p "$output_dir" "$run_tmp"

  TMPDIR="$run_tmp" \
  BEAMFALL_E2E_PORT="$port" \
    BEAMFALL_E2E_INTERRUPT_READY_FILE="$ready_file" \
    BEAMFALL_WEB_E2E_OUTPUT_DIR="$output_dir" \
    "${ROOT}/script/web-ui-e2e-smoke.sh" >"$log" 2>&1 &
  runner=$!
  RUNNER_PIDS="${RUNNER_PIDS} ${runner}"

  wait_for_file "$ready_file" || {
    tail -n 40 "$log" >&2
    fail "descendant-free interruption: setup never reached the post-boot interrupt barrier"
  }
  server_pid="$(state_value "$ready_file" pid)"
  data_dir="$(state_value "$ready_file" dataDir)"
  SERVER_PIDS="${SERVER_PIDS} ${server_pid}"
  track_data_dir "$data_dir"

  state_before_signal="missing"
  if [[ -s "$state_file" ]]; then
    state_before_signal="present"
    recorded_pid="$(state_value "$state_file" pid)"
    [[ "$recorded_pid" == "$server_pid" ]] || fail \
      "descendant-free interruption: state pid ${recorded_pid} did not match server pid ${server_pid}"
  fi

  playwright_pid="$(state_value "$ready_file" runnerPid)"
  process_is_alive "$playwright_pid" || fail \
    "descendant-free interruption: Playwright runner pid ${playwright_pid} exited before SIGINT"
  kill -INT "$playwright_pid"
  wait_for_process_exit "$runner" 600 || fail \
    "descendant-free interruption: smoke runner pid ${runner} hung after SIGINT"
  set +e
  wait "$runner"
  runner_rc=$?
  set -e

  if ! wait_for_process_exit "$server_pid"; then
    fail "descendant-free interruption: server pid ${server_pid} on port ${port} survived SIGINT; state was ${state_before_signal} before signal"
  fi
  [[ "$state_before_signal" == present ]] || fail \
    "descendant-free interruption: server pid ${server_pid} exited, but its state was not on disk before SIGINT"
  [[ "$runner_rc" -ne 0 ]] || fail \
    "descendant-free interruption: interrupted runner unexpectedly exited zero"

  echo "PASS descendant-free interruption: state pid ${server_pid} was on disk before SIGINT and no server survived"
}

case "${1:-all}" in
  all)
    run_teardown_regression
    run_concurrency_regression
    run_interruption_regression
    ;;
  concurrency) run_concurrency_regression ;;
  teardown) run_teardown_regression ;;
  descendant) run_interruption_regression ;;
  *)
    echo "usage: $0 [all|concurrency|teardown|descendant]" >&2
    exit 2
    ;;
esac
