#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-web-e2e-runner-test.XXXXXX")"
ACTIVE_PIDS=()
ACTIVE_GROUPS=()
PASS=0
FAIL=0

cleanup() {
  local pgid pid
  trap - EXIT INT TERM
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

wait_for_file() {
  local file="$1" owner="$2" ticks=200
  while [[ ! -e "${file}" && "${ticks}" -gt 0 ]] && kill -0 "${owner}" 2>/dev/null; do
    sleep 0.02
    ticks=$((ticks - 1))
  done
  [[ -e "${file}" ]]
}

assert_process_gone() {
  local pid="$1" label="$2" ticks=100
  while kill -0 "${pid}" 2>/dev/null && [[ "${ticks}" -gt 0 ]]; do
    sleep 0.02
    ticks=$((ticks - 1))
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
if [[ "${BEAMFALL_RUNNER_TEST_MODE:?}" == success ]]; then
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

browser_role="browser-${invocation}"
"${BEAMFALL_RUNNER_TEST_DESCENDANT}" "${browser_role}" "${metadata}" &
browser_pid=$!
if [[ -z "${BEAMFALL_E2E_URL:-}" ]]; then
  server_role="server-${invocation}"
  "${BEAMFALL_RUNNER_TEST_DESCENDANT}" "${server_role}" "${metadata}" &
  server_pid=$!
fi
while [[ ! -s "${metadata}/${browser_role}-child.pid" ]]; do sleep 0.01; done
if [[ -z "${BEAMFALL_E2E_URL:-}" ]]; then
  while [[ ! -s "${metadata}/${server_role}-child.pid" ]]; do sleep 0.01; done
fi
touch "${metadata}/ready"

case "${BEAMFALL_RUNNER_TEST_MODE:?}" in
  success)
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
  failure) exit 23 ;;
  hang) while :; do sleep 1; done ;;
  *) exit 2 ;;
esac
SH
chmod +x "${TMP}/bin/npm"

sleep 120 &
sentinel_pid=$!
register_pid "${sentinel_pid}"

run_case() {
  local runner_name="$1" mode="$2" signal="${3:-}" expected="$4"
  local runner="${ROOT}/script/web-ui-e2e-${runner_name}.sh"
  local case_dir="${TMP}/${runner_name}-${mode}-${signal:-none}"
  local runner_pid watchdog_pid status=0 pid_file pid runner_timeout_seconds=15
  if [[ "${mode}" == hang ]]; then
    runner_timeout_seconds=1
  fi
  mkdir -p "${case_dir}/metadata" "${case_dir}/output"

  PATH="${TMP}/bin:${PATH}" \
    BEAMFALL_E2E_URL= \
    BEAMFALL_RUNNER_TEST_METADATA="${case_dir}/metadata" \
    BEAMFALL_RUNNER_TEST_DESCENDANT="${TMP}/fake-descendant.sh" \
    BEAMFALL_RUNNER_TEST_MODE="${mode}" \
    BEAMFALL_RUNNER_TEST_VALID_PROVENANCE="${TMP}/valid-provenance.json" \
    BEAMFALL_E2E_PORT=49124 \
    BEAMFALL_WEB_E2E_OUTPUT_DIR="${case_dir}/output" \
    RELEASE_NIGHTLY_EVIDENCE_DIR="${case_dir}/evidence" \
    BEAMFALL_WEB_E2E_RUNNER_TIMEOUT_SECONDS="${runner_timeout_seconds}" \
    BEAMFALL_WEB_E2E_TERM_GRACE_TICKS=2 \
    BEAMFALL_WEB_E2E_KILL_WAIT_TICKS=20 \
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

time.sleep(20)
with open(sys.argv[2], "w", encoding="utf-8"):
    pass
try:
    os.killpg(int(sys.argv[1]), signal.SIGKILL)
except ProcessLookupError:
    pass
' "${runner_pid}" "${case_dir}/harness-timeout" &
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

  if [[ -e "${case_dir}/harness-timeout" ]]; then
    bad "${runner_name} ${mode} exceeded the independent test deadline"
  fi

  if [[ "${status}" -eq "${expected}" ]]; then
    ok "${runner_name} ${mode} exits ${expected}"
  else
    bad "${runner_name} ${mode} exits ${expected} (got ${status})"
  fi

  for pid_file in "${case_dir}/metadata/"*.pid; do
    [[ -e "${pid_file}" ]] || continue
    pid="$(<"${pid_file}")"
    assert_process_gone "${pid}" "${runner_name} ${mode} $(basename "${pid_file}")"
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
  kill -KILL -- "-${runner_pid}" 2>/dev/null || true
  forget_group "${runner_pid}"
}

for runner_name in smoke full; do
  run_case "${runner_name}" success "" 0
  run_case "${runner_name}" failure "" 23
  run_case "${runner_name}" hang "" 124
  run_case "${runner_name}" hang INT 130
  run_case "${runner_name}" hang TERM 143
done

for runner_name in smoke full; do
  reuse_dir="${TMP}/${runner_name}-reuse"
  mkdir -p "${reuse_dir}/output"
  status=0
  for attempt in 1 2; do
    attempt_metadata="${reuse_dir}/metadata-${attempt}"
    mkdir -p "${attempt_metadata}"
    PATH="${TMP}/bin:${PATH}" \
      BEAMFALL_E2E_URL="http://127.0.0.1:49123" \
      BEAMFALL_RUNNER_TEST_METADATA="${attempt_metadata}" \
      BEAMFALL_RUNNER_TEST_DESCENDANT="${TMP}/fake-descendant.sh" \
      BEAMFALL_RUNNER_TEST_MODE=success \
      BEAMFALL_RUNNER_TEST_VALID_PROVENANCE="${TMP}/valid-provenance.json" \
      BEAMFALL_E2E_PORT=49124 \
      BEAMFALL_WEB_E2E_OUTPUT_DIR="${reuse_dir}/output" \
      RELEASE_NIGHTLY_EVIDENCE_DIR="${reuse_dir}/evidence-${attempt}" \
      BEAMFALL_WEB_E2E_TERM_GRACE_TICKS=2 \
      "${ROOT}/script/web-ui-e2e-${runner_name}.sh" \
      >"${reuse_dir}/stdout-${attempt}" 2>"${reuse_dir}/stderr-${attempt}" \
      || status=$?
  done
  final_metadata="${reuse_dir}/metadata-2"
  if [[ "${status}" -eq 0 ]] \
    && [[ "$(<"${final_metadata}/url")" == "http://127.0.0.1:49123" ]] \
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
