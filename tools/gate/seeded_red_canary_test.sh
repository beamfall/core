#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck disable=SC1091
source "$ROOT/script/seeded-red-canary.sh"

sourced_exit_trap="$(trap -p EXIT)"
sourced_exit_handler="${sourced_exit_trap#trap -- \'}"
sourced_exit_handler="${sourced_exit_handler%\' EXIT}"
if [[ "$sourced_exit_handler" != cleanup ]]; then
  echo "unexpected sourced EXIT trap: $sourced_exit_trap" >&2
  exit 1
fi
test_tmp="$(mktemp -d "${TMPDIR:-/tmp}/gcr022-canary-test.XXXXXX")"
cleanup_test_harness() {
  local status=$?
  trap - EXIT
  "$sourced_exit_handler"
  rm -rf "$test_tmp"
  return "$status"
}
trap cleanup_test_harness EXIT

if [[ "${1:-}" == --early-exit-control ]]; then
  : "${GCR022_CANARY_EARLY_ROOT:?early-exit fixture root required}"
  ROOT="$GCR022_CANARY_EARLY_ROOT"
  tmp="$(mktemp -d "$test_tmp/seed-work.XXXXXX")"
  seed_line_coverage
  exit 42
fi

marker="$test_tmp/seed"

seed_test() {
  : >"$marker"
  # shellcheck disable=SC2034
  seeded_path="$marker"
  # shellcheck disable=SC2034
  cleanup_cmd=cleanup_remove
}
test_command() { [[ ! -e "$marker" ]] || { echo "expected seeded failure" >&2; return 1; }; }
infrastructure_failure() {
  echo "expected seeded failure" >&2
  echo "GCR-022 canary infrastructure failure: fixture leak" >&2
  return 1
}
wrong_failure() { echo "unrelated failure" >&2; return 1; }
mutation_no_output_failure() {
  echo "ERROR: gremlins produced no output for ./internal/streamtoken" >&2
  echo "mutation-test: budget breached — see FAIL lines above" >&2
  return 1
}
seeded_failure() { echo "expected seeded failure" >&2; return 1; }
seed_passes() { :; }
clean_fails() { echo "clean failure" >&2; return 1; }
reset_harness() {
  "$cleanup_cmd"
  cleanup_cmd=cleanup_none
  [[ -z "$tmp" ]] || rm -rf "$tmp"
  tmp=""
}

prove harness "expected seeded failure" seed_test test_command test_command \
  | grep -Fq "PASS harness: seeded regression red; restored clean tree green"
[[ ! -e "$marker" ]]

if prove harness-infrastructure "expected seeded failure" seed_test infrastructure_failure test_command \
  >"$test_tmp/infrastructure.out" 2>&1; then
  echo "seeded-red canary accepted a seeded failure with leaked infrastructure" >&2
  exit 1
fi
grep -Fq "seeded check also leaked test infrastructure" "$test_tmp/infrastructure.out"
reset_harness
[[ ! -e "$marker" ]]

if prove harness-wrong "expected seeded failure" seed_test wrong_failure test_command \
  >"$test_tmp/wrong.out" 2>&1; then
  echo "seeded-red canary accepted an unrelated red" >&2
  exit 1
fi
grep -Fq "wrong failure" "$test_tmp/wrong.out"
reset_harness

if prove mutation-empty-output "> budget -1 (ratchet floor breached)" \
  seed_test mutation_no_output_failure test_command >"$test_tmp/mutation-empty.out" 2>&1; then
  echo "seeded-red canary accepted mutation infrastructure failure as the seeded ratchet red" >&2
  exit 1
fi
grep -Fq "gremlins produced no output" "$test_tmp/mutation-empty.out"
grep -Fq "wrong failure" "$test_tmp/mutation-empty.out"
if grep -Eq '^(RED|PASS) mutation-empty-output:' "$test_tmp/mutation-empty.out"; then
  echo "seeded-red canary counted mutation infrastructure failure as the expected result" >&2
  exit 1
fi
reset_harness

if prove harness-green "expected seeded failure" seed_test seed_passes test_command \
  >"$test_tmp/green.out" 2>&1; then
  echo "seeded-red canary accepted a green seed" >&2
  exit 1
fi
grep -Fq "seeded regression passed" "$test_tmp/green.out"
reset_harness

if prove harness-clean "expected seeded failure" seed_test seeded_failure clean_fails \
  >"$test_tmp/clean.out" 2>&1; then
  echo "seeded-red canary accepted a red clean tree" >&2
  exit 1
fi
grep -Fq "clean tree red" "$test_tmp/clean.out"
reset_harness

early_root="$test_tmp/early-root"
early_target="$early_root/testing/coverage-floor.json"
early_original="$test_tmp/early-coverage-floor.original"
mkdir -p "$early_root/testing"
cp "$ROOT/testing/coverage-floor.json" "$early_target"
cp "$early_target" "$early_original"
early_status=0
GCR022_CANARY_EARLY_ROOT="$early_root" bash "$0" --early-exit-control \
  >"$test_tmp/early-exit.out" 2>&1 || early_status=$?
if [[ "$early_status" -ne 42 ]]; then
  cat "$test_tmp/early-exit.out" >&2
  echo "early-exit control returned $early_status, want 42" >&2
  exit 1
fi
if ! cmp -s "$early_original" "$early_target"; then
  echo "chained test-harness EXIT trap did not restore the fixture byte-for-byte" >&2
  exit 1
fi

interrupt_root="$test_tmp/interrupt-root"
interrupt_target="$interrupt_root/testing/coverage-floor.json"
interrupt_original="$test_tmp/coverage-floor.original"
mkdir -p "$interrupt_root/testing"
cp "$ROOT/testing/coverage-floor.json" "$interrupt_target"
cp "$interrupt_target" "$interrupt_original"
interrupt_status=0
bash -c '
  set -euo pipefail
  # shellcheck disable=SC1090
  source "$1/script/seeded-red-canary.sh"
  ROOT="$2"
  tmp="$(mktemp -d "$3/interrupt-work.XXXXXX")"
  interrupt_original="$4"
  interrupt_target="$5"
  real_python3="$(command -v python3)"
  python3() {
    "$real_python3" "$@"
    if cmp -s "$interrupt_original" "$interrupt_target"; then
      echo "interrupt control did not mutate fixture" >&2
      exit 98
    fi
    kill -TERM "$$"
    exit 99
  }
  seed_line_coverage
' _ "$ROOT" "$interrupt_root" "$test_tmp" "$interrupt_original" "$interrupt_target" \
  >"$test_tmp/interrupt.out" 2>&1 || interrupt_status=$?
if [[ "$interrupt_status" -ne 143 ]]; then
  cat "$test_tmp/interrupt.out" >&2
  echo "interrupt control did not terminate in the seed window: $interrupt_status" >&2
  exit 1
fi
if ! cmp -s "$interrupt_original" "$interrupt_target"; then
  echo "interrupt control did not restore the fixture byte-for-byte" >&2
  exit 1
fi

fuzz_root="$test_tmp/fuzz-root"
fuzz_target="$fuzz_root/internal/ffmpeg/ffmpeg_fuzz_test.go"
mkdir -p "$(dirname "$fuzz_target")"
cp "$ROOT/internal/ffmpeg/ffmpeg_fuzz_test.go" "$fuzz_target"
(
  # shellcheck disable=SC1091
  source "$ROOT/script/seeded-red-canary.sh"
  ROOT="$fuzz_root"
  tmp="$(mktemp -d "$test_tmp/fuzz-work.XXXXXX")"
  seed_fuzz
  grep -Fq 'probe, _ := parseProbe(data, path)' "$fuzz_target"
  grep -Fq 'probe.Container != "GCR-022 seeded fuzz regression"' "$fuzz_target"
  "$cleanup_cmd"
  cleanup_cmd=cleanup_none
)
cmp -s "$ROOT/internal/ffmpeg/ffmpeg_fuzz_test.go" "$fuzz_target"

gate_repo_root="$ROOT"
ROOT="$test_tmp/gate-root"
mkdir -p "$ROOT/tools/gate"
gate_seed="$ROOT/tools/gate/gcr022_seeded_red.go"
# shellcheck disable=SC2329 # invoked indirectly by red_gate/clean_gate
make() {
  [[ "$*" == gate ]] || return 2
  [[ ! -e "$gate_seed" ]] || { echo "gofmt needed" >&2; return 2; }
}
prove gate-control "gofmt needed" seed_gate red_gate clean_gate \
  | grep -Fq "PASS gate-control: seeded regression red; restored clean tree green"
unset -f make
[[ ! -e "$gate_seed" ]]
ROOT="$gate_repo_root"

web_state="$test_tmp/web-state.json"
sleep 0.2 &
web_pid=$!
printf '{"pid":%s}\n' "$web_pid" >"$web_state"
drain_web_server
wait "$web_pid"

web_repo_root="$ROOT"
ROOT="$test_tmp/web-root"
mkdir -p "$ROOT/internal/web/app/e2e"
cp "$web_repo_root/internal/web/app/e2e/smoke.spec.ts" "$ROOT/internal/web/app/e2e/smoke.spec.ts"
run_web() {
  printf 'playwright unavailable: %s\n' "$*" >&2
  return 127
}
if prove web-missing-runner "GCR-022 seeded Playwright regression" seed_web red_web clean_web \
  >"$test_tmp/web-missing.out" 2>&1; then
  echo "seeded-red canary accepted an unavailable Playwright runner" >&2
  exit 1
fi
grep -Fq "wrong failure" "$test_tmp/web-missing.out"
reset_harness
if grep -Fq "GCR-022 seeded Playwright canary" "$ROOT/internal/web/app/e2e/smoke.spec.ts"; then
  echo "seeded-red canary did not restore the Playwright spec" >&2
  exit 1
fi
ROOT="$web_repo_root"

tmp="$test_tmp/web-clean-zero"
mkdir -p "$tmp"
run_web() {
  echo "Running 0 tests using 1 worker"
}
if clean_web >"$test_tmp/web-clean-zero.out" 2>&1; then
  echo "seeded-red canary accepted a clean full Playwright tier that ran zero tests" >&2
  exit 1
fi
grep -Fq "full Playwright tier did not run" "$test_tmp/web-clean-zero.out"
rm -rf "$tmp"
tmp=""

web_clean_calls=0
run_web() {
  web_clean_calls=$((web_clean_calls + 1))
  if [[ $# -ne 0 ]]; then
    echo "unexpected clean Playwright filter: $*" >&2
    return 2
  fi
  echo "Running 12 tests using 1 worker"
  echo "12 passed (0.1s)"
}
tmp="$test_tmp/web-clean"
mkdir -p "$tmp"
clean_web >"$test_tmp/web-clean.out"
[[ "$web_clean_calls" -eq 1 ]]
grep -Fq "Running 12 tests using 1 worker" "$test_tmp/web-clean.out"
rm -rf "$tmp"
tmp=""

coverage_calls=0
go() {
  coverage_calls=$((coverage_calls + 1))
  [[ "$coverage_calls" -ne 1 ]]
}
if clean_coverage; then
  echo "seeded-red canary masked a failing clean coverage command" >&2
  exit 1
fi
[[ "$coverage_calls" -eq 1 ]]
unset -f go

io_calls=0
run_io_e2e() {
  io_calls=$((io_calls + 1))
  if [[ "$1" == fixtures ]]; then
    echo "fixture setup failure" >&2
    return 1
  fi
  echo "GCR-022 seeded I/O regression" >&2
  return 1
}
if red_io >"$test_tmp/io-setup.out" 2>&1; then
  echo "seeded-red canary accepted a failing I/O fixture setup" >&2
  exit 1
fi
[[ "$io_calls" -eq 1 ]]
grep -Fq "fixture setup failure" "$test_tmp/io-setup.out"
if grep -Fq "GCR-022 seeded I/O regression" "$test_tmp/io-setup.out"; then
  echo "seeded-red canary ran the I/O seed after fixture setup failed" >&2
  exit 1
fi

tmp="$test_tmp/io-zero"
mkdir -p "$tmp"
run_io_e2e() {
  echo "testing: warning: no tests to run"
  return 0
}
if run_selected_io_test journeys TestGCR022Missing >"$test_tmp/io-zero.out" 2>&1; then
  echo "seeded-red canary accepted an I/O selector that ran zero tests" >&2
  exit 1
fi
grep -Fq "I/O test TestGCR022Missing did not run" "$test_tmp/io-zero.out"
rm -rf "$tmp"
tmp=""

run_io_e2e() {
  echo "testing: warning: no tests to run"
  return 0
}
tmp="$test_tmp/io-clean-zero"
mkdir -p "$tmp"
if clean_io >"$test_tmp/io-clean-zero.out" 2>&1; then
  echo "seeded-red canary accepted a clean I/O journeys tier that ran zero tests" >&2
  exit 1
fi
grep -Fq "I/O journeys tier did not run" "$test_tmp/io-clean-zero.out"
rm -rf "$tmp"
tmp=""

io_clean_calls=0
run_io_e2e() {
  io_clean_calls=$((io_clean_calls + 1))
  if [[ "$*" != "journeys -count=1 -v" ]]; then
    echo "unexpected clean I/O selector: $*" >&2
    return 2
  fi
  echo "=== RUN   TestFrameGrabJourney"
  echo "--- PASS: TestFrameGrabJourney (0.00s)"
}
tmp="$test_tmp/io-clean"
mkdir -p "$tmp"
clean_io >"$test_tmp/io-clean.out"
[[ "$io_clean_calls" -eq 1 ]]
grep -Fxq "=== RUN   TestFrameGrabJourney" "$test_tmp/io-clean.out"
rm -rf "$tmp"
tmp=""

echo "seeded-red canary controls: passed"
