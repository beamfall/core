#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RACE_TEST="$ROOT/script/race-test.sh"
RACE_TEST_TEST="$ROOT/script/race-test_test.sh"
RACE_SHORT_HOT_TESTS='^(TestEventStreamTerminatesOnDeviceRevocation|TestHLSPrecomputedRenditionGatesSegmentAndRange|TestCanDirectPlayRichProfile|TestPrecomputeConcurrentAdmissionNeverExceedsBudget|TestPrecomputeCancellationCleansStagingAndReleasesReservation|TestPrecomputeDiskFullCleansStagingAndReleasesReservation|TestPrecomputeRejectsIncompleteArtifactBeforePublication|TestPrecomputeRejectsPlaylistWithMissingReferencedSegment|TestPrecomputeReplacementFencesStalePublisher|TestPrecomputeInputGenerationChangeCannotPublish|TestPrecomputeAtomicPublicationRejectsReplacedInput|TestSweepPrecomputeResidueRecoversCrashWithoutDeletingReadyGeneration|TestScanJobHandlerYieldsCheckpointAndResumes|TestBootstrapRejectsConcurrentDoubleSubmit|TestRunShutdownCancelsBlockingJobAtDrainBound|TestRunShutdownLetsFastJobDrainWithoutCancellation|TestRunShutdownRepeatedlyJoinsWorkers|TestShutdownSequenceStopsClaimsBeforeCancelJoinCheckpointAndStoreClose|TestDrainBackgroundJobsDrainsQueueInOneTickWindow|TestJobClaimerStartupBacklogRunsImmediatelyAndCancels|TestIntegrationManagerDeactivateCancelsInFlightWorkerAndReports|TestScanScheduleSpecAbortsWhenSchedulerContextCanceled)$'
PROCESS_STARTUP_TIMEOUT_SECONDS=20
DRIVER_READY_TIMEOUT_SECONDS=$((PROCESS_STARTUP_TIMEOUT_SECONDS + 2))
CLEANUP_GRACE_SECONDS=4
INTERRUPTION_WATCHDOG_SECONDS=$((CLEANUP_GRACE_SECONDS * 3 + 3))
SELF_INTERRUPT_WATCHDOG_SECONDS=$((DRIVER_READY_TIMEOUT_SECONDS + CLEANUP_GRACE_SECONDS + 2))
CUT_WATCHDOG_SECONDS=$((PROCESS_STARTUP_TIMEOUT_SECONDS + CLEANUP_GRACE_SECONDS + 2))
TMP="$(mktemp -d "${RACE_FIXTURE_TMPDIR:-${TMPDIR:-/tmp}}/race-test-fixture.XXXXXX")"
ACTIVE_PIDS=()
ACTIVE_GROUPS=()

run_bash_in_owned_signal_session() {
  exec python3 -c '
import os
import signal
import sys

# A canonical gate supervisor can itself be an asynchronous shell child and
# therefore inherit SIGINT ignored. Define the target disposition explicitly
# before Bash starts: an ignored signal cannot be trapped later.
os.setsid()
signal.signal(signal.SIGINT, signal.SIG_DFL)
signal.signal(signal.SIGTERM, signal.SIG_DFL)
os.execv("/bin/bash", ["bash", *sys.argv[1:]])
' "$@"
}

register_active_pid() {
  ACTIVE_PIDS[${#ACTIVE_PIDS[@]}]="$1"
}

register_active_group() {
  local group="$1" active
  for active in "${ACTIVE_GROUPS[@]+"${ACTIVE_GROUPS[@]}"}"; do
    [[ "$active" != "$group" ]] || return 0
  done
  ACTIVE_GROUPS[${#ACTIVE_GROUPS[@]}]="$group"
}

unregister_active_pid() {
  local target="$1" active
  local kept=()
  for active in "${ACTIVE_PIDS[@]+"${ACTIVE_PIDS[@]}"}"; do
    [[ "$active" == "$target" ]] || kept[${#kept[@]}]="$active"
  done
  ACTIVE_PIDS=("${kept[@]+"${kept[@]}"}")
}

unregister_inactive_groups() {
  local group
  local kept=()
  for group in "${ACTIVE_GROUPS[@]+"${ACTIVE_GROUPS[@]}"}"; do
    if kill -0 -- "-$group" 2>/dev/null; then
      kept[${#kept[@]}]="$group"
    fi
  done
  ACTIVE_GROUPS=("${kept[@]+"${kept[@]}"}")
}

registered_groups_gone() {
  local group
  for group in "${ACTIVE_GROUPS[@]+"${ACTIVE_GROUPS[@]}"}"; do
    if kill -0 -- "-$group" 2>/dev/null; then
      return 1
    fi
  done
  return 0
}

wait_for_registered_groups() {
  local deadline=$((SECONDS + CLEANUP_GRACE_SECONDS))
  while ! registered_groups_gone && [[ "$SECONDS" -lt "$deadline" ]]; do
    sleep 0.05
  done
}

signal_registered_groups() {
  local signal="$1" group
  for group in "${ACTIVE_GROUPS[@]+"${ACTIVE_GROUPS[@]}"}"; do
    kill -"$signal" -- "-$group" 2>/dev/null || true
  done
}

record_cleanup_step() {
  [[ -z "${SELF_INTERRUPT_METADATA_DIR:-}" ]] || \
    printf '%s\n' "$1" >>"$SELF_INTERRUPT_METADATA_DIR/cleanup-trace"
}

cleanup_active_processes() {
  local pid
  local pids=("${ACTIVE_PIDS[@]+"${ACTIVE_PIDS[@]}"}")

  record_cleanup_step start
  signal_registered_groups TERM
  signal_registered_groups CONT
  record_cleanup_step term-cont
  wait_for_registered_groups
  record_cleanup_step grace-complete
  signal_registered_groups KILL
  record_cleanup_step kill
  for pid in "${pids[@]+"${pids[@]}"}"; do
    record_cleanup_step "wait-$pid"
    wait "$pid" 2>/dev/null || true
    record_cleanup_step "reaped-$pid"
    unregister_active_pid "$pid"
  done
  wait_for_registered_groups
  unregister_inactive_groups
  record_cleanup_step complete
}

cleanup() {
  local status=$?
  trap - EXIT
  trap '' INT TERM
  cleanup_active_processes
  rm -rf "$TMP"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [[ -n "${SELF_INTERRUPT_METADATA_DIR:-}" ]]; then
  printf '%s\n' "$TMP" >"$SELF_INTERRUPT_METADATA_DIR/temp-tree"
fi

PASS=0
FAIL=0

PRODUCTION_HOT_TESTS="$(awk -F"'" '$1 == "RACE_SHORT_HOT_TESTS=" {print $2; exit}' "$RACE_TEST")"
if [[ "$PRODUCTION_HOT_TESTS" != "$RACE_SHORT_HOT_TESTS" ]]; then
  echo "FAIL race-test fixture hot selector drifted from production" >&2
  ((FAIL++)) || true
else
  ((PASS++)) || true
fi

assert_exit_eq() {
  local got="$1" want="$2" name="$3"
  if [[ "$got" -ne "$want" ]]; then
    echo "FAIL $name expected=$want got=$got" >&2
    ((FAIL++)) || true
    return 1
  fi
  ((PASS++)) || true
  return 0
}

assert_jsonl() {
  local evidence="$1" status="$2" name="$3"
  if [[ ! -f "$evidence" ]]; then
    echo "FAIL $name missing $evidence" >&2
    ((FAIL++)) || true
    return 1
  fi
  if ! python3 - "$evidence" "$status" <<'PY'
import json
import sys

path = sys.argv[1]
want_status = sys.argv[2]

lines = [line for line in open(path, encoding="utf-8") if line.strip()]
if not lines:
    raise SystemExit(1)
rec = json.loads(lines[0])
if rec.get("status") != want_status:
    raise SystemExit(1)
for key in ("package", "elapsed_seconds", "cached"):
    if key not in rec:
        raise SystemExit(1)
PY
  then
    echo "FAIL $name invalid JSONL payload" >&2
    ((FAIL++)) || true
    return 1
  fi
  ((PASS++)) || true
}

assert_discovery_json() {
  local evidence="$1" status="$2" discovered="$3" runnable="$4" name="$5"
  if [[ ! -f "$evidence" ]]; then
    echo "FAIL $name missing $evidence" >&2
    ((FAIL++)) || true
    return 1
  fi
  if ! python3 - "$evidence" "$status" "$discovered" "$runnable" <<'PY'
import json
import sys

path, status, discovered, runnable = sys.argv[1:]
with open(path, encoding="utf-8") as f:
    rec = json.load(f)
if rec.get("go_list_status") != int(status):
    raise SystemExit(1)
want_discovered = json.loads(discovered)
want_runnable = json.loads(runnable)
if rec.get("discovered_count") != len(want_discovered):
    raise SystemExit(1)
if rec.get("runnable_count") != len(want_runnable):
    raise SystemExit(1)
if rec.get("discovered_packages") != want_discovered:
    raise SystemExit(1)
if rec.get("runnable_packages") != want_runnable:
    raise SystemExit(1)
PY
  then
    echo "FAIL $name invalid discovery JSON payload" >&2
    ((FAIL++)) || true
    return 1
  fi
  ((PASS++)) || true
}

assert_file_eq() {
  local path="$1" expected="$2" name="$3"
  local actual=""
  [[ ! -f "$path" ]] || actual="$(<"$path")"
  if [[ "$actual" != "$expected" ]]; then
    echo "FAIL $name" >&2
    printf 'expected: %s\nactual:   %s\n' "$expected" "$actual" >&2
    ((FAIL++)) || true
    return 1
  fi
  ((PASS++)) || true
}

assert_process_gone() {
  local target="$1" name="$2"
  local attempts=0
  while kill -0 -- "$target" 2>/dev/null && [[ "$attempts" -lt 200 ]]; do
    sleep 0.01
    attempts=$((attempts + 1))
  done
  if kill -0 -- "$target" 2>/dev/null; then
    echo "FAIL $name remains: $target" >&2
    ((FAIL++)) || true
    return 1
  fi
  ((PASS++)) || true
}

make_fake_go() {
  local target="$1"
  mkdir -p "$target"
  cat > "$target/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "env" ]]; then
  shift
  if [[ "${1:-}" == "CC" ]]; then
    echo "${GO_STUB_CC:-sh}"
  else
    echo ""
  fi
  exit 0
fi

if [[ "${1:-}" == "list" ]]; then
  case "${GO_LIST_MODE:-pass}" in
    pass)
      echo "example.com/beamfall/core/internal/racefixture"
      echo "github.com/beamfall/core/internal/app"
      echo "github.com/beamfall/core/internal/server"
      echo "github.com/beamfall/core/internal/graph"
      exit 0
      ;;
    fail)
      echo "go list fixture failure" >&2
      exit 17
      ;;
    empty)
      exit 0
      ;;
    whitespace)
      printf '  \n\t\n'
      exit 0
      ;;
    excluded)
      echo "github.com/beamfall/core/internal/graph"
      exit 0
      ;;
  esac
fi

if [[ "${1:-}" == "test" ]]; then
  phase=ordinary
  [[ " $* " != *" -run "* ]] || phase=focused
  [[ " $* " != *" -p 1 "* ]] || phase=promoted
  if [[ -n "${GO_TEST_ARGS_FILE:-}" ]]; then
    printf '%s\n' "$*" >>"$GO_TEST_ARGS_FILE"
  fi
  if [[ -n "${GO_TEST_LEADER_PID_FILE:-}" && \
        ( -z "${GO_TEST_PROCESS_PHASE:-}" || "${GO_TEST_PROCESS_PHASE}" == "$phase" ) ]]; then
    echo "$$" >"$GO_TEST_LEADER_PID_FILE"
    trap '' INT TERM
    (
      trap '' INT TERM
      while :; do
        sleep 300
      done
    ) &
    descendant_pid=$!
    echo "$descendant_pid" >"$GO_TEST_DESCENDANT_PID_FILE"
    if [[ "${GO_TEST_LEADER_EXITS:-0}" != "1" ]]; then
      while :; do
        wait "$descendant_pid" || true
      done
    fi
  fi
  cat <<JSON
{"Time":"2026-01-01T00:00:00Z","Action":"output","Package":"example.com/beamfall/core/internal/racefixture","Output":"=== RUN   TestFixture\n"}
JSON
  if [[ "${GO_EXIT_CODE:-0}" == "0" ]]; then
    cat <<JSON
{"Time":"2026-01-01T00:00:00Z","Action":"pass","Package":"example.com/beamfall/core/internal/racefixture","Elapsed":0.001,"Cached":true}
JSON
  else
    cat <<JSON
{"Time":"2026-01-01T00:00:00Z","Action":"output","Package":"example.com/beamfall/core/internal/racefixture","Output":"FAIL\texample.com/beamfall/core/internal/racefixture\n"}
{"Time":"2026-01-01T00:00:00Z","Action":"fail","Package":"example.com/beamfall/core/internal/racefixture","Elapsed":0.002}
JSON
  fi
  exit "${GO_EXIT_CODE:-0}"
fi

echo "unsupported go call: $*" >&2
exit 1
EOF
  chmod +x "$target/go"
}

run_leader_exit_descendant_case() {
  local name="timeout-leader-exit-descendant-retired"
  local case_dir="$TMP/$name"
  local fake_go_dir="$TMP/fake-go-$name"
  local leader_pid_file="$case_dir/leader.pid"
  local descendant_pid_file="$case_dir/descendant.pid"
  mkdir -p "$case_dir/evidence"
  make_fake_go "$fake_go_dir"

  PATH="$fake_go_dir:$PATH" \
    GO_STUB_CC=sh \
    GO_TEST_LEADER_PID_FILE="$leader_pid_file" \
    GO_TEST_DESCENDANT_PID_FILE="$descendant_pid_file" \
    GO_TEST_PROCESS_PHASE=ordinary \
    GO_TEST_LEADER_EXITS=1 \
    GO_EXIT_CODE=1 \
    AGENT_EVIDENCE_DIR="$case_dir/evidence" \
    run_bash_in_owned_signal_session "$RACE_TEST" short \
      >"$case_dir/output.txt" 2>"$case_dir/error.txt" &
  local cut_pid=$!
  register_active_pid "$cut_pid"
  register_active_group "$cut_pid"

  set -m
  (
    sleep "$CUT_WATCHDOG_SECONDS"
    kill -KILL -- "-$cut_pid" 2>/dev/null || true
    if [[ -s "$leader_pid_file" ]]; then
      kill -KILL -- "-$(<"$leader_pid_file")" 2>/dev/null || true
    fi
  ) &
  local watchdog_pid=$!
  register_active_pid "$watchdog_pid"
  register_active_group "$watchdog_pid"
  set +m

  local startup_deadline=$((SECONDS + PROCESS_STARTUP_TIMEOUT_SECONDS))
  while [[ ! -s "$leader_pid_file" && "$SECONDS" -lt "$startup_deadline" ]] \
    && kill -0 "$cut_pid" 2>/dev/null; do
    sleep 0.05
  done
  if [[ -s "$leader_pid_file" ]]; then
    register_active_group "$(<"$leader_pid_file")"
  fi

  local status=0
  wait "$cut_pid" || status=$?
  unregister_active_pid "$cut_pid"
  unregister_inactive_groups
  kill -TERM -- "-$watchdog_pid" 2>/dev/null || true
  wait "$watchdog_pid" 2>/dev/null || true
  unregister_active_pid "$watchdog_pid"
  unregister_inactive_groups

  assert_exit_eq "$status" 1 "$name (exit status)"
  if [[ ! -s "$leader_pid_file" || ! -s "$descendant_pid_file" ]]; then
    echo "FAIL $name process tree did not start" >&2
    ((FAIL++)) || true
    return
  fi
  assert_process_gone "$(<"$leader_pid_file")" "$name (leader)"
  assert_process_gone "$(<"$descendant_pid_file")" "$name (descendant)"
  assert_process_gone "-$(<"$leader_pid_file")" "$name (process group)"
}

run_case() {
  local name="$1" expected_exit="$2" expected_pkg_status="$3"
  local case_dir="$TMP/$name"
  mkdir -p "$case_dir/evidence"

  local fake_go_dir="$TMP/fake-go-$name"
  make_fake_go "$fake_go_dir"

  local output="$case_dir/output.txt" err="$case_dir/error.txt" evidence="$case_dir/evidence"
  local args_file="$case_dir/go-test-args.txt"
  local status=0
  if PATH="$fake_go_dir:$PATH" \
    GO_EXIT_CODE="$expected_exit" \
    GO_STUB_CC="sh" \
    GO_TEST_ARGS_FILE="$args_file" \
    AGENT_EVIDENCE_DIR="$evidence" \
    bash "$RACE_TEST" short >"$output" 2>"$err"; then
    status=0
  else
    status=$?
  fi
  if [[ "$status" -ne "$expected_exit" ]]; then
    cat "$err" >&2
  fi
  assert_exit_eq "$status" "$expected_exit" "$name (exit status)"
  assert_jsonl "$evidence/race-short-packages.jsonl" "$expected_pkg_status" "$name (evidence)"
  assert_discovery_json "$evidence/race-short-discovery.json" 0 \
    '["example.com/beamfall/core/internal/racefixture","github.com/beamfall/core/internal/app","github.com/beamfall/core/internal/server","github.com/beamfall/core/internal/graph"]' \
    '["example.com/beamfall/core/internal/racefixture","github.com/beamfall/core/internal/app","github.com/beamfall/core/internal/server"]' \
    "$name (discovery evidence)"
  assert_file_eq "$args_file" \
    $'test -race -short -count=1 -p 4 -parallel 2 -test.timeout=600s -json example.com/beamfall/core/internal/racefixture\ntest -race -short -count=1 -p 4 -parallel 2 -test.timeout=600s -run '"$RACE_SHORT_HOT_TESTS"$' -json github.com/beamfall/core/internal/app github.com/beamfall/core/internal/server' \
    "$name (go test arguments)"

  if [[ "$expected_exit" != "0" ]]; then
    if ! grep -q 'FAIL' "$err"; then
      echo "FAIL $name no FAIL text in error output" >&2
      ((FAIL++)) || true
    else
      ((PASS++)) || true
    fi
  fi
}

run_discovery_failure_case() {
  local name="$1" list_mode="$2" expected_exit="$3" expected_discovered="$4" message="$5"
  local expected_list_status=0
  [[ "$list_mode" == "fail" ]] && expected_list_status="$expected_exit"
  local case_dir="$TMP/$name"
  mkdir -p "$case_dir/evidence"

  local fake_go_dir="$TMP/fake-go-$name"
  make_fake_go "$fake_go_dir"

  local output="$case_dir/output.txt" err="$case_dir/error.txt" evidence="$case_dir/evidence"
  local status=0
  if PATH="$fake_go_dir:$PATH" \
    GO_LIST_MODE="$list_mode" \
    GO_STUB_CC="sh" \
    AGENT_EVIDENCE_DIR="$evidence" \
    bash "$RACE_TEST" short >"$output" 2>"$err"; then
    status=0
  else
    status=$?
  fi
  if [[ "$status" -ne "$expected_exit" ]]; then
    cat "$err" >&2
  fi
  assert_exit_eq "$status" "$expected_exit" "$name (exit status)"
  local discovered_json='[]'
  [[ "$expected_discovered" == "1" ]] && discovered_json='["github.com/beamfall/core/internal/graph"]'
  assert_discovery_json "$evidence/race-short-discovery.json" "$expected_list_status" "$discovered_json" '[]' "$name (discovery evidence)"
  if ! grep -q "$message" "$err"; then
    echo "FAIL $name missing diagnostic: $message" >&2
    ((FAIL++)) || true
  else
    ((PASS++)) || true
  fi
}

run_promoted_phase_case() {
  local name="$1" ordinary_exit="$2" promoted_exit="$3" focused_exit="$4"
  local parser_exit="$5" expected_exit="$6"
  local case_dir="$TMP/$name" repo="$TMP/$name-repo"
  local shim="$TMP/$name-shim" evidence="$case_dir/evidence"
  local args_file="$case_dir/go-test-args.txt" parser_marker="$case_dir/parser-ran"
  mkdir -p "$case_dir" "$evidence" "$shim" \
    "$repo/script" "$repo/internal/foo" "$repo/internal/app" \
    "$repo/internal/graph" "$repo/internal/playback"
  cp "$RACE_TEST" "$repo/script/race-test.sh"
  chmod +x "$repo/script/race-test.sh"
  cat >"$repo/go.mod" <<'EOF'
module github.com/beamfall/core

go 1.26
EOF
  cat >"$repo/internal/foo/foo.go" <<'EOF'
package foo

func Value() int { return 1 }
EOF
  cat >"$repo/internal/app/app.go" <<'EOF'
package app

import "github.com/beamfall/core/internal/foo"

func Value() int { return foo.Value() }
EOF
  cat >"$repo/internal/graph/graph.go" <<'EOF'
package graph

func Value() int { return 1 }
EOF
  cat >"$repo/internal/playback/playback.go" <<'EOF'
package playback

func Value() int { return 1 }
EOF
  git -C "$repo" init -qb main
  git -C "$repo" add .
  git -C "$repo" -c user.name=t -c user.email=t@e.com commit -qm base
  local base
  base="$(git -C "$repo" rev-parse HEAD)"
  printf '\n// direct change\n' >>"$repo/internal/foo/foo.go"
  printf '\n// direct change\n' >>"$repo/internal/graph/graph.go"
  printf '\n// direct change\n' >>"$repo/internal/playback/playback.go"
  git -C "$repo" add internal/foo/foo.go internal/graph/graph.go \
    internal/playback/playback.go
  git -C "$repo" -c user.name=t -c user.email=t@e.com commit -qm changed

  cat >"$shim/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" != test ]]; then
  exec "$REAL_GO" "$@"
fi
status="${GO_ORDINARY_EXIT:-0}"
if [[ " $* " == *" -p 1 "* ]]; then
  status="${GO_PROMOTED_EXIT:-0}"
elif [[ " $* " == *" -run "* ]]; then
  status="${GO_FOCUSED_EXIT:-0}"
fi
printf '%s\n' "$*" >>"$GO_TEST_ARGS_FILE"
if [[ "$status" -eq 0 ]]; then
  action=pass
else
  action=fail
fi
for package in "$@"; do
  case "$package" in
    github.com/beamfall/core/internal/*)
      printf '{"Action":"%s","Package":"%s","Elapsed":0.001}\n' "$action" "$package"
      ;;
  esac
done
exit "$status"
EOF
  chmod +x "$shim/go"
  cat >"$shim/python3" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == */parser.py ]]; then
  : >"$PARSER_MARKER"
  if [[ "${PARSER_EXIT_CODE:-0}" -ne 0 ]]; then
    exit "$PARSER_EXIT_CODE"
  fi
fi
exec "$REAL_PYTHON" "$@"
EOF
  chmod +x "$shim/python3"

  local status=0 real_go real_python
  real_go="$(command -v go)"
  real_python="$(command -v python3)"
  if ( cd "$repo" && \
    PATH="$shim:$PATH" \
    REAL_GO="$real_go" \
    REAL_PYTHON="$real_python" \
    GO_TEST_ARGS_FILE="$args_file" \
    GO_ORDINARY_EXIT="$ordinary_exit" \
    GO_PROMOTED_EXIT="$promoted_exit" \
    GO_FOCUSED_EXIT="$focused_exit" \
    PARSER_EXIT_CODE="$parser_exit" \
    PARSER_MARKER="$parser_marker" \
    AGENT_EVIDENCE_DIR="$evidence" \
    GATE_DIFF_BASE="$base" GATE_DIFF_HEAD=HEAD \
    script/race-test.sh short >"$case_dir/output.txt" 2>"$case_dir/error.txt" ); then
    status=0
  else
    status=$?
  fi
  assert_exit_eq "$status" "$expected_exit" "$name (phase precedence)"
  assert_file_eq "$args_file" \
    $'test -race -short -count=1 -p 4 -parallel 2 -test.timeout=600s -json github.com/beamfall/core/internal/foo\ntest -race -short -count=1 -p 1 -parallel 2 -test.timeout=3600s -json github.com/beamfall/core/internal/graph github.com/beamfall/core/internal/playback\ntest -race -short -count=1 -p 4 -parallel 2 -test.timeout=600s -run '"$RACE_SHORT_HOT_TESTS"$' -json github.com/beamfall/core/internal/app' \
    "$name (all phase arguments)"
  if [[ ! -f "$parser_marker" ]]; then
    echo "FAIL $name parser phase did not execute" >&2
    ((FAIL++)) || true
  else
    ((PASS++)) || true
  fi
  if [[ "$parser_exit" -eq 0 ]]; then
    if ! python3 - "$evidence/race-short-packages.jsonl" \
      "$ordinary_exit" "$promoted_exit" "$focused_exit" <<'PY'
import json
import sys

path = sys.argv[1]
codes = [int(value) for value in sys.argv[2:]]
expected = {
    "github.com/beamfall/core/internal/foo": "pass" if codes[0] == 0 else "fail",
    "github.com/beamfall/core/internal/graph": "pass" if codes[1] == 0 else "fail",
    "github.com/beamfall/core/internal/playback": "pass" if codes[1] == 0 else "fail",
    "github.com/beamfall/core/internal/app": "pass" if codes[2] == 0 else "fail",
}
records = [json.loads(line) for line in open(path, encoding="utf-8") if line.strip()]
actual = {record["package"]: record["status"] for record in records}
if actual != expected:
    raise SystemExit(f"phase evidence mismatch: expected={expected!r} actual={actual!r}")
PY
    then
      echo "FAIL $name package evidence did not include every phase" >&2
      ((FAIL++)) || true
    else
      ((PASS++)) || true
    fi
  fi
}

run_interruption_case() {
  local signal="$1" expected_status="$2"
  local name="interrupted-$signal"
  local case_dir="$TMP/$name"
  local fake_go_dir="$TMP/fake-go-$name"
  local temp_root="$case_dir/tmp"
  local leader_pid_file="$case_dir/leader.pid"
  local descendant_pid_file="$case_dir/descendant.pid"
  mkdir -p "$case_dir/evidence" "$temp_root"
  make_fake_go "$fake_go_dir"

  PATH="$fake_go_dir:$PATH" \
    GO_STUB_CC="sh" \
    GO_TEST_LEADER_PID_FILE="$leader_pid_file" \
    GO_TEST_DESCENDANT_PID_FILE="$descendant_pid_file" \
    TMPDIR="$temp_root" \
    AGENT_EVIDENCE_DIR="$case_dir/evidence" \
    run_bash_in_owned_signal_session "$RACE_TEST" short \
      >"$case_dir/output.txt" 2>"$case_dir/error.txt" &
  local race_pid=$!
  register_active_pid "$race_pid"
  register_active_group "$race_pid"

  local startup_deadline=$((SECONDS + PROCESS_STARTUP_TIMEOUT_SECONDS))
  while [[ ! -s "$leader_pid_file" || ! -s "$descendant_pid_file" ]] && [[ "$SECONDS" -lt "$startup_deadline" ]]; do
    sleep 0.05
  done
  if [[ ! -s "$leader_pid_file" || ! -s "$descendant_pid_file" ]]; then
    echo "FAIL $name process tree did not start" >&2
    cat "$case_dir/error.txt" >&2
    cat "$case_dir/output.txt" >&2
    ((FAIL++)) || true
    if [[ -s "$leader_pid_file" ]]; then
      register_active_group "$(<"$leader_pid_file")"
    fi
    cleanup_active_processes
    return
  fi

  local leader_pid descendant_pid watchdog_pid
  leader_pid="$(<"$leader_pid_file")"
  descendant_pid="$(<"$descendant_pid_file")"
  register_active_group "$leader_pid"
  if [[ -n "${SELF_INTERRUPT_METADATA_DIR:-}" ]]; then
    printf '%s\n' "$race_pid" >"$SELF_INTERRUPT_METADATA_DIR/race.pid"
    printf '%s\n' "$leader_pid" >"$SELF_INTERRUPT_METADATA_DIR/leader.pid"
    printf '%s\n' "$descendant_pid" >"$SELF_INTERRUPT_METADATA_DIR/descendant.pid"
    : >"$SELF_INTERRUPT_METADATA_DIR/watchdog.pid"
    : >"$SELF_INTERRUPT_METADATA_DIR/ready"
    kill -STOP -- "-$race_pid"
    while :; do
      sleep 0.05
    done
  fi
  set -m
  (
    sleep "$INTERRUPTION_WATCHDOG_SECONDS"
    kill -KILL -- "-$race_pid" 2>/dev/null || true
    kill -KILL -- "-$leader_pid" 2>/dev/null || true
  ) &
  watchdog_pid=$!
  register_active_pid "$watchdog_pid"
  register_active_group "$watchdog_pid"
  if [[ -n "${SELF_INTERRUPT_METADATA_DIR:-}" ]]; then
    printf '%s\n' "$watchdog_pid" >"$SELF_INTERRUPT_METADATA_DIR/watchdog.pid"
  fi
  set +m

  kill -"$signal" -- "-$race_pid"
  sleep 0.1
  kill -"$signal" -- "-$race_pid"
  local status=0
  wait "$race_pid" || status=$?
  unregister_active_pid "$race_pid"
  unregister_inactive_groups
  kill -TERM -- "-$watchdog_pid" 2>/dev/null || true
  wait "$watchdog_pid" 2>/dev/null || true
  unregister_active_pid "$watchdog_pid"
  unregister_inactive_groups

  assert_exit_eq "$status" "$expected_status" "$name (exit status)"
  assert_process_gone "$leader_pid" "$name (leader)"
  assert_process_gone "$descendant_pid" "$name (descendant)"
  assert_process_gone "-$leader_pid" "$name (process group)"
  unregister_inactive_groups
  if compgen -G "$temp_root/race-test.*" >/dev/null; then
    echo "FAIL $name temporary residue remains" >&2
    ((FAIL++)) || true
  else
    ((PASS++)) || true
  fi
}

run_fixture_self_interruption_case() {
  local signal="$1" expected_status="$2"
  local name="fixture-interrupted-$signal"
  local case_dir="$TMP/$name"
  local metadata="$case_dir/metadata"
  local temp_root="$case_dir/temp-root"
  mkdir -p "$metadata" "$temp_root"

  SELF_INTERRUPT_METADATA_DIR="$metadata" \
    RACE_FIXTURE_TMPDIR="$temp_root" \
    run_bash_in_owned_signal_session "$RACE_TEST_TEST" self-interruption-target \
      >"$case_dir/output.txt" 2>"$case_dir/error.txt" &
  local suite_pid=$!
  register_active_pid "$suite_pid"
  register_active_group "$suite_pid"

  set -m
  (
    sleep "$SELF_INTERRUPT_WATCHDOG_SECONDS"
    kill -KILL -- "-$suite_pid" 2>/dev/null || true
    if [[ -s "$metadata/race.pid" ]]; then
      kill -KILL -- "-$(<"$metadata/race.pid")" 2>/dev/null || true
    fi
    if [[ -s "$metadata/leader.pid" ]]; then
      kill -KILL -- "-$(<"$metadata/leader.pid")" 2>/dev/null || true
    fi
    if [[ -s "$metadata/watchdog.pid" ]]; then
      kill -KILL -- "-$(<"$metadata/watchdog.pid")" 2>/dev/null || true
    fi
  ) &
  local watchdog_pid=$!
  register_active_pid "$watchdog_pid"
  register_active_group "$watchdog_pid"
  set +m

  local ready_deadline=$((SECONDS + DRIVER_READY_TIMEOUT_SECONDS))
  while [[ ! -f "$metadata/ready" && "$SECONDS" -lt "$ready_deadline" ]]; do
    sleep 0.05
  done
  if [[ ! -f "$metadata/ready" ]]; then
    echo "FAIL $name did not reach pre-watchdog interruption point" >&2
    cat "$case_dir/error.txt" >&2
    ((FAIL++)) || true
    if [[ -s "$metadata/race.pid" ]]; then
      register_active_group "$(<"$metadata/race.pid")"
    fi
    if [[ -s "$metadata/leader.pid" ]]; then
      register_active_group "$(<"$metadata/leader.pid")"
    fi
    cleanup_active_processes
    return
  fi

  local race_pid leader_pid descendant_pid target_tmp
  race_pid="$(<"$metadata/race.pid")"
  leader_pid="$(<"$metadata/leader.pid")"
  descendant_pid="$(<"$metadata/descendant.pid")"
  target_tmp="$(<"$metadata/temp-tree")"
  register_active_group "$race_pid"
  register_active_group "$leader_pid"

  kill -"$signal" "$suite_pid"
  sleep 0.02
  kill -"$signal" "$suite_pid" 2>/dev/null || true

  local status=0
  wait "$suite_pid" || status=$?
  unregister_active_pid "$suite_pid"
  unregister_inactive_groups
  if [[ "$status" -ne "$expected_status" ]]; then
    cat "$metadata/cleanup-trace" 2>/dev/null || true
    cat "$case_dir/error.txt" 2>/dev/null || true
  fi

  local target_watchdog_pid=""
  if [[ -s "$metadata/watchdog.pid" ]]; then
    target_watchdog_pid="$(<"$metadata/watchdog.pid")"
    register_active_group "$target_watchdog_pid"
  fi

  kill -TERM -- "-$watchdog_pid" 2>/dev/null || true
  wait "$watchdog_pid" 2>/dev/null || true
  unregister_active_pid "$watchdog_pid"
  unregister_inactive_groups

  assert_exit_eq "$status" "$expected_status" "$name (exit status)"
  assert_process_gone "$suite_pid" "$name (suite)"
  assert_process_gone "-$suite_pid" "$name (suite group)"
  assert_process_gone "$race_pid" "$name (race)"
  assert_process_gone "-$race_pid" "$name (race group)"
  assert_process_gone "$leader_pid" "$name (leader)"
  assert_process_gone "$descendant_pid" "$name (descendant)"
  assert_process_gone "-$leader_pid" "$name (leader group)"
  assert_process_gone "$watchdog_pid" "$name (external watchdog)"
  assert_process_gone "-$watchdog_pid" "$name (external watchdog group)"
  if [[ -n "$target_watchdog_pid" ]]; then
    assert_process_gone "$target_watchdog_pid" "$name (target watchdog)"
    assert_process_gone "-$target_watchdog_pid" "$name (target watchdog group)"
  fi
  unregister_inactive_groups
  if [[ -e "$target_tmp" ]]; then
    echo "FAIL $name temporary tree remains: $target_tmp" >&2
    ((FAIL++)) || true
  else
    ((PASS++)) || true
  fi
}

if [[ "${1:-}" == "self-interruption-target" ]]; then
  run_interruption_case TERM 143
  exit 1
fi

run_case pass 0 pass
run_case fail 1 fail
run_promoted_phase_case phase-green 0 0 0 0 0
run_promoted_phase_case ordinary-fail-precedence 11 0 0 0 11
run_promoted_phase_case promoted-fail-precedence 0 12 0 0 12
run_promoted_phase_case focused-fail-precedence 0 0 13 0 13
run_promoted_phase_case parser-fail-precedence 0 0 0 14 14
run_promoted_phase_case mixed-fail-precedence 11 12 13 14 11
run_promoted_phase_case mixed-promoted-over-focused-parser 0 12 13 14 12
run_promoted_phase_case mixed-focused-over-parser 0 0 13 14 13
run_discovery_failure_case list-fail fail 17 0 'package discovery failed'
run_discovery_failure_case list-empty empty 1 0 'zero packages'
run_discovery_failure_case list-whitespace whitespace 1 0 'zero packages'
run_discovery_failure_case zero-runnable excluded 1 1 'zero runnable packages'
run_leader_exit_descendant_case
run_interruption_case TERM 143
run_interruption_case INT 130
run_fixture_self_interruption_case TERM 143
run_fixture_self_interruption_case INT 130

echo ""
if [[ "$FAIL" -gt 0 ]]; then
  echo "race-test short JSONL fixture tests: $PASS passed, $FAIL failed" >&2
  exit 1
fi
echo "race-test short JSONL fixture tests: $PASS passed, 0 failed"
