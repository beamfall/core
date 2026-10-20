#!/usr/bin/env bash
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [ "${GATE_CLEANUP_TEST_SUPERVISED:-0}" != 1 ]; then
  exec python3 "$ROOT/script/test-process-supervisor.py" \
    GATE_CLEANUP_TEST_SUPERVISED "$0" "$@"
fi
set -euo pipefail
export GATE_PROCESS_GROUP_OWNED=0

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
TEST_OWNED_GROUPS=""

# The outer supervisor must reap every process group in its test session, not
# only the initial group. Exercise a job-control-created, disowned helper.
SUPERVISOR_PROBE_PID="$TMP/supervisor-multigroup.pid"
set +e
python3 "$ROOT/script/test-process-supervisor.py" GATE_SUPERVISOR_NESTED_PROBE \
  bash -c 'set -m; trap "" HUP TERM; sleep 30 & printf "%s\n" "$!" >"$1"; disown; exit 7' \
  _ "$SUPERVISOR_PROBE_PID" >"$TMP/supervisor-multigroup.out" 2>&1
supervisor_probe_rc=$?
set -e
[ "$supervisor_probe_rc" -eq 7 ] && [ -s "$SUPERVISOR_PROBE_PID" ] || {
  echo "test process supervisor multigroup probe did not run" >&2
  exit 1
}
supervisor_probe_child="$(cat "$SUPERVISOR_PROBE_PID")"
if kill -0 "$supervisor_probe_child" 2>/dev/null; then
  echo "test process supervisor left a disowned process group running" >&2
  kill -KILL "$supervisor_probe_child" 2>/dev/null || true
  exit 1
fi

register_test_group() {
  local group="$1" held
  case "$group" in ''|*[!0-9]*) return 1 ;; esac
  for held in $TEST_OWNED_GROUPS; do [ "$held" = "$group" ] && return 0; done
  TEST_OWNED_GROUPS="$TEST_OWNED_GROUPS $group"
}

unregister_test_group() {
  local target="$1" held kept=""
  for held in $TEST_OWNED_GROUPS; do [ "$held" = "$target" ] || kept="$kept $held"; done
  TEST_OWNED_GROUPS="$kept"
}

discover_test_groups() {
  local file first second
  [ -d "$TMP" ] || return 0
  for file in "$TMP"/cleanup-child-* "$TMP"/parallel-cleanup-ignore \
      "$TMP"/parallel-cleanup-late "$TMP"/parallel-cleanup-fork \
      "$TMP"/gate-supervisor-daemon.pid "$TMP"/nested-supervisor-daemon.pid; do
    [ -s "$file" ] || continue
    read -r first second <"$file" || true
    case "$second" in ''|*[!0-9]*) ;; *) register_test_group "$second" ;; esac
  done
  if [ -s "$TMP/parallel-cleanup-wrappers" ]; then
    while IFS= read -r first; do
      case "$first" in ''|*[!0-9]*) ;; *) register_test_group "$first" ;; esac
    done <"$TMP/parallel-cleanup-wrappers"
  fi
}

cleanup_gate_rerun_test() {
  local rc=$? group attempt alive=0 ticks
  trap - EXIT INT TERM
  set +e
  discover_test_groups
  for group in $TEST_OWNED_GROUPS; do
    kill -TERM -- "-$group" 2>/dev/null || true
    kill -CONT -- "-$group" 2>/dev/null || true
  done
  for attempt in $(seq 1 20); do
    alive=0
    for group in $TEST_OWNED_GROUPS; do kill -0 -- "-$group" 2>/dev/null && alive=1; done
    [ "$alive" -eq 1 ] || break
    sleep 0.05
  done
  for group in $TEST_OWNED_GROUPS; do kill -KILL -- "-$group" 2>/dev/null || true; done
  ticks=40
  for attempt in $(seq 1 "$ticks"); do
    alive=0
    for group in $TEST_OWNED_GROUPS; do kill -0 -- "-$group" 2>/dev/null && alive=1; done
    [ "$alive" -eq 1 ] || break
    sleep 0.05
  done
  if [ "$alive" -eq 1 ]; then
    echo "gate rerun test teardown could not reap all owned process groups" >&2
    [ "$rc" -ne 0 ] || rc=1
  fi
  rm -rf "$TMP"
  exit "$rc"
}
trap cleanup_gate_rerun_test EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Nested supervisors retain every ancestor ownership marker. Killing the outer
# boundary must therefore reap a TERM-resistant grandchild even if the inner
# supervisor is itself killed before completing its own escalation pass.
cat >"$TMP/nested-supervisor-fixture.py" <<'PY'
import subprocess
import sys
import time

code = r'''
import os
import signal
import sys
import time
signal.signal(signal.SIGHUP, signal.SIG_IGN)
signal.signal(signal.SIGTERM, signal.SIG_IGN)
with open(sys.argv[1], "w", encoding="utf-8") as handle:
    handle.write(f"{os.getpid()} {os.getpgrp()}\n")
while True:
    time.sleep(30)
'''
subprocess.Popen([sys.executable, "-c", code, sys.argv[1]], start_new_session=True)
while True:
    time.sleep(30)
PY
python3 "$ROOT/script/test-process-supervisor.py" GATE_SUPERVISOR_OUTER_PROBE \
  python3 "$ROOT/script/test-process-supervisor.py" GATE_SUPERVISOR_INNER_PROBE \
  python3 "$TMP/nested-supervisor-fixture.py" "$TMP/nested-supervisor-daemon.pid" \
  >"$TMP/nested-supervisor.out" 2>&1 &
nested_supervisor_pid=$!
for _ in $(seq 1 200); do
  [ -s "$TMP/nested-supervisor-daemon.pid" ] && break
  sleep 0.01
done
[ -s "$TMP/nested-supervisor-daemon.pid" ] || {
  echo "nested process supervisor fixture did not start its daemon" >&2
  exit 1
}
read -r nested_daemon_pid nested_daemon_group <"$TMP/nested-supervisor-daemon.pid"
register_test_group "$nested_daemon_group"
kill -TERM "$nested_supervisor_pid"
wait "$nested_supervisor_pid" 2>/dev/null || true
for _ in $(seq 1 100); do
  kill -0 "$nested_daemon_pid" 2>/dev/null || break
  sleep 0.01
done
if kill -0 "$nested_daemon_pid" 2>/dev/null; then
  echo "outer supervisor left a nested setsid grandchild running" >&2
  exit 1
fi
unregister_test_group "$nested_daemon_group"

# Production gate supervision is the outer containment boundary. A gate step
# may create a setsid helper outside every registered lane group; terminating
# the gate must still reap it through the inherited owner token.
python3 - "$ROOT/tools/gate/gate.sh" "$TMP/gate-supervisor-fixture.sh" <<'PY'
from pathlib import Path
import sys

gate = Path(sys.argv[1]).read_text()
end = gate.index("set -euo pipefail") + len("set -euo pipefail")
Path(sys.argv[2]).write_text(
    gate[:end]
    + r"""

pidfile="$1"
python3 - "$pidfile" <<'INNER'
import signal
import subprocess
import sys
import time

pidfile = sys.argv[1]
code = r'''
import os
import signal
import sys
import time
signal.signal(signal.SIGHUP, signal.SIG_IGN)
signal.signal(signal.SIGTERM, signal.SIG_IGN)
with open(sys.argv[1], "w", encoding="utf-8") as handle:
    handle.write(f"{os.getpid()} {os.getpgrp()}\n")
while True:
    time.sleep(30)
'''
subprocess.Popen([sys.executable, "-c", code, pidfile], start_new_session=True)
while True:
    time.sleep(30)
INNER
"""
)
PY
chmod +x "$TMP/gate-supervisor-fixture.sh"
BEAMFALL_GATE_CAPACITY_HELD=1 GATE_PROCESS_SUPERVISED=0 \
  GATE_PROCESS_SUPERVISOR="$ROOT/script/test-process-supervisor.py" \
  "$TMP/gate-supervisor-fixture.sh" "$TMP/gate-supervisor-daemon.pid" \
  >"$TMP/gate-supervisor.out" 2>&1 &
gate_supervisor_pid=$!
for _ in $(seq 1 200); do
  [ -s "$TMP/gate-supervisor-daemon.pid" ] && break
  sleep 0.01
done
[ -s "$TMP/gate-supervisor-daemon.pid" ] || {
  echo "production gate supervisor fixture did not start its daemon" >&2
  kill -KILL "$gate_supervisor_pid" 2>/dev/null || true
  wait "$gate_supervisor_pid" 2>/dev/null || true
  exit 1
}
read -r gate_daemon_pid gate_daemon_group <"$TMP/gate-supervisor-daemon.pid"
register_test_group "$gate_daemon_group"
kill -TERM "$gate_supervisor_pid"
wait "$gate_supervisor_pid" 2>/dev/null || true
for _ in $(seq 1 100); do
  kill -0 "$gate_daemon_pid" 2>/dev/null || break
  sleep 0.01
done
if kill -0 "$gate_daemon_pid" 2>/dev/null; then
  echo "terminated production gate left a setsid helper running" >&2
  exit 1
fi
unregister_test_group "$gate_daemon_group"

# A coordinator replacement sends TERM to the old gate. The gate must reap its
# complete background tree instead of leaving long-running go/swift tests behind.
python3 - "$ROOT/tools/gate/gate.sh" "$TMP/cleanup-test.sh" <<'PY'
from pathlib import Path
import sys

gate = Path(sys.argv[1]).read_text()
start = gate.index("gate_signal_group() {")
end = gate.index("\nrun_parallel_step() {", start)
Path(sys.argv[2]).write_text(
    "#!/usr/bin/env bash\nset -euo pipefail\n"
    + gate[start:end]
    + r'''
trap gate_cleanup_children EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
gate_metric() { :; }
run_step "cleanup fixture" python3 "$4" "$1" "$2" "$3"
'''
)
PY
chmod +x "$TMP/cleanup-test.sh"
cat >"$TMP/cleanup-fixture.py" <<'PY'
import os
import signal
import sys
import time

mode, child_file, late_file = sys.argv[1:]

if mode in {"detached-success", "detached-failure"}:
    child = os.fork()
    if child == 0:
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        signal.signal(signal.SIGHUP, signal.SIG_IGN)
        while True:
            time.sleep(30)
    with open(child_file, "w", encoding="utf-8") as handle:
        handle.write(f"{child} {os.getpgid(child)}\n")
    raise SystemExit(0 if mode == "detached-success" else 7)

with open(child_file, "w", encoding="utf-8") as handle:
    handle.write(f"{os.getpid()} {os.getpgrp()}\n")

if mode == "success":
    raise SystemExit(0)

if mode == "ignore":
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
elif mode == "stopped":
    os.kill(os.getpid(), signal.SIGSTOP)
elif mode == "late-fork":
    def on_term(_signum, _frame):
        child = os.fork()
        if child == 0:
            time.sleep(30)
            os._exit(0)
        with open(late_file, "w", encoding="utf-8") as handle:
            handle.write(f"{child}\n")
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
    signal.signal(signal.SIGTERM, on_term)

while True:
    time.sleep(30)
PY

python3 - "$ROOT/tools/gate/gate.sh" "$TMP/parallel-detached-test.sh" <<'PY'
from pathlib import Path
import sys

gate = Path(sys.argv[1]).read_text()
start = gate.index("gate_signal_group() {")
end = gate.index("\nchanged_paths() {", start)
Path(sys.argv[2]).write_text(
    "#!/usr/bin/env bash\nset -euo pipefail\n"
    + gate[start:end]
    + r'''
mode="$1"
child_file="$2"
late_file="$3"
fixture="$4"
start_parallel_step wrapper "parallel detached $mode" \
  python3 "$fixture" "$mode" "$child_file" "$late_file"
wait_parallel "$wrapper"
'''
)
PY
chmod +x "$TMP/parallel-detached-test.sh"

assert_cleanup() {
  local mode="$1" child_file late_file
  local cleanup_harness cleanup_child cleanup_group late_child=""
  child_file="$TMP/cleanup-child-$mode"
  late_file="$TMP/cleanup-late-$mode"
  "$TMP/cleanup-test.sh" "$mode" "$child_file" "$late_file" "$TMP/cleanup-fixture.py" &
  cleanup_harness=$!
  for _ in $(seq 1 100); do
    [ -s "$child_file" ] && break
    sleep 0.01
  done
  [ -s "$child_file" ] || {
    echo "gate cleanup fixture $mode did not start" >&2
    kill -KILL "$cleanup_harness" 2>/dev/null || true
    return 1
  }
  read -r cleanup_child cleanup_group <"$child_file"
  register_test_group "$cleanup_group"
  kill -TERM "$cleanup_harness"
  for _ in $(seq 1 200); do
    kill -0 "$cleanup_harness" 2>/dev/null || break
    sleep 0.01
  done
  if kill -0 "$cleanup_harness" 2>/dev/null; then
    echo "terminated $mode gate command delayed coordinator cleanup" >&2
    kill -KILL "$cleanup_harness" "$cleanup_child" 2>/dev/null || true
    wait "$cleanup_harness" 2>/dev/null || true
    return 1
  fi
  wait "$cleanup_harness" 2>/dev/null || true
  [ ! -s "$late_file" ] || late_child="$(cat "$late_file")"
  for cleanup_child in "$cleanup_child" ${late_child:+"$late_child"}; do
    if kill -0 "$cleanup_child" 2>/dev/null; then
      echo "terminated $mode gate orphaned child process $cleanup_child" >&2
      kill -KILL "$cleanup_child" 2>/dev/null || true
      return 1
    fi
  done
  unregister_test_group "$cleanup_group"
  rm -f "$child_file" "$late_file"
}

assert_cleanup ignore
assert_cleanup stopped
assert_cleanup late-fork

assert_detached_cleanup() {
  local mode="$1" expected_rc="$2" child_file late_file output
  local harness child group rc
  child_file="$TMP/cleanup-child-$mode"
  late_file="$TMP/cleanup-late-$mode"
  output="$TMP/cleanup-$mode.out"
  GATE_TERM_GRACE_TICKS=2 GATE_KILL_WAIT_TICKS=40 \
    "$TMP/cleanup-test.sh" "$mode" "$child_file" "$late_file" "$TMP/cleanup-fixture.py" \
    >"$output" 2>&1 &
  harness=$!
  for _ in $(seq 1 200); do [ -s "$child_file" ] && break; sleep 0.01; done
  [ -s "$child_file" ] || { echo "detached gate fixture $mode did not publish its helper" >&2; return 1; }
  read -r child group <"$child_file"
  register_test_group "$group"
  set +e
  wait "$harness"
  rc=$?
  set -e
  [ "$rc" -eq "$expected_rc" ] \
    || { echo "detached gate fixture $mode returned $rc, want $expected_rc" >&2; return 1; }
  grep -q 'residual process group' "$output"
  if kill -0 "$child" 2>/dev/null; then
    echo "detached gate fixture $mode orphaned helper $child" >&2
    return 1
  fi
  unregister_test_group "$group"
  rm -f "$child_file" "$late_file"
}

assert_detached_cleanup detached-success 1
assert_detached_cleanup detached-failure 7

assert_parallel_detached_cleanup() {
  local mode="$1" child_file late_file output harness child group rc
  child_file="$TMP/cleanup-child-parallel-$mode"
  late_file="$TMP/cleanup-late-parallel-$mode"
  output="$TMP/parallel-detached-$mode.out"
  GATE_TERM_GRACE_TICKS=2 GATE_KILL_WAIT_TICKS=40 \
    "$TMP/parallel-detached-test.sh" "$mode" "$child_file" "$late_file" \
      "$TMP/cleanup-fixture.py" >"$output" 2>&1 &
  harness=$!
  for _ in $(seq 1 200); do [ -s "$child_file" ] && break; sleep 0.01; done
  [ -s "$child_file" ] \
    || { echo "parallel detached gate fixture $mode did not publish its helper" >&2; return 1; }
  read -r child group <"$child_file"
  register_test_group "$group"
  set +e
  wait "$harness"
  rc=$?
  set -e
  [ "$rc" -eq 1 ] \
    || { echo "parallel detached gate fixture $mode returned $rc, want 1" >&2; return 1; }
  grep -q 'parallel gate lane left a residual process group' "$output"
  if kill -0 "$child" 2>/dev/null; then
    echo "parallel detached gate fixture $mode orphaned helper $child" >&2
    return 1
  fi
  unregister_test_group "$group"
  rm -f "$child_file" "$late_file"
}

assert_parallel_detached_cleanup detached-success
assert_parallel_detached_cleanup detached-failure

"$TMP/cleanup-test.sh" success "$TMP/cleanup-child-success" "$TMP/cleanup-late-success" \
  "$TMP/cleanup-fixture.py" >"$TMP/cleanup-success.out" 2>&1
rm -f "$TMP/cleanup-child-success" "$TMP/cleanup-late-success"
if grep -Eq '^\[[0-9]+\].*(Done|Terminated|Killed)' "$TMP/cleanup-success.out"; then
  echo "successful gate step emitted job-control noise: $(cat "$TMP/cleanup-success.out")" >&2
  exit 1
fi

# Parallel gate lanes must make their visible wrapper the process-group leader.
# Their nested run_step commands and TERM-time forks stay in that same group, so
# replacing the coordinator cannot leave either lane's descendants behind.
python3 - "$ROOT/tools/gate/gate.sh" "$TMP/parallel-cleanup-test.sh" <<'PY'
from pathlib import Path
import sys

gate = Path(sys.argv[1]).read_text()
start = gate.index("gate_signal_group() {")
end = gate.index("\nchanged_paths() {", start)
Path(sys.argv[2]).write_text(
    "#!/usr/bin/env bash\nset -euo pipefail\n"
    + gate[start:end]
    + r'''
wrapper_file="$1"
ignore_file="$2"
late_file="$3"
fork_file="$4"
fixture="$5"
start_parallel_step ignore_wrapper "ignore lane" \
  python3 "$fixture" ignore "$ignore_file" "$fork_file"
start_parallel_step late_wrapper "late-fork lane" \
  python3 "$fixture" late-fork "$late_file" "$fork_file"
printf '%s\n%s\n' "$ignore_wrapper" "$late_wrapper" >"$wrapper_file"
wait_parallel "$ignore_wrapper" "$late_wrapper"
'''
)
PY
chmod +x "$TMP/parallel-cleanup-test.sh"
cat >"$TMP/parallel-cleanup-fixture.py" <<'PY'
import os
import signal
import sys
import time

mode, child_file, fork_file = sys.argv[1:]
with open(child_file, "w", encoding="utf-8") as handle:
    handle.write(f"{os.getpid()} {os.getpgrp()}\n")

if mode == "ignore":
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
elif mode == "late-fork":
    def on_term(_signum, _frame):
        child = os.fork()
        if child == 0:
            signal.signal(signal.SIGTERM, signal.SIG_IGN)
            while True:
                time.sleep(30)
        with open(fork_file, "w", encoding="utf-8") as handle:
            handle.write(f"{child} {os.getpgid(child)}\n")
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
    signal.signal(signal.SIGTERM, on_term)

while True:
    time.sleep(30)
PY

parallel_wrappers="$TMP/parallel-cleanup-wrappers"
parallel_ignore="$TMP/parallel-cleanup-ignore"
parallel_late="$TMP/parallel-cleanup-late"
parallel_fork="$TMP/parallel-cleanup-fork"
"$TMP/parallel-cleanup-test.sh" \
  "$parallel_wrappers" "$parallel_ignore" "$parallel_late" "$parallel_fork" \
  "$TMP/parallel-cleanup-fixture.py" >"$TMP/parallel-cleanup.out" 2>&1 &
parallel_harness=$!
for _ in $(seq 1 200); do
  [ -s "$parallel_wrappers" ] && [ -s "$parallel_ignore" ] && [ -s "$parallel_late" ] && break
  sleep 0.01
done
if [ ! -s "$parallel_wrappers" ] || [ ! -s "$parallel_ignore" ] || [ ! -s "$parallel_late" ]; then
  echo "parallel gate cleanup fixtures did not start" >&2
  kill -KILL "$parallel_harness" 2>/dev/null || true
  exit 1
fi
read -r ignore_wrapper <"$parallel_wrappers"
late_wrapper="$(sed -n '2p' "$parallel_wrappers")"
read -r ignore_child ignore_group <"$parallel_ignore"
read -r late_child late_group <"$parallel_late"
register_test_group "$ignore_group"
register_test_group "$late_group"
[ "$ignore_group" = "$ignore_wrapper" ] && [ "$late_group" = "$late_wrapper" ] || {
  echo "parallel gate commands escaped their visible wrapper groups" >&2
  kill -KILL "$parallel_harness" "$ignore_child" "$late_child" 2>/dev/null || true
  exit 1
}
kill -TERM "$parallel_harness"
for _ in $(seq 1 300); do
  kill -0 "$parallel_harness" 2>/dev/null || break
  sleep 0.01
done
if kill -0 "$parallel_harness" 2>/dev/null; then
  echo "parallel gate cleanup delayed coordinator replacement" >&2
  kill -KILL "$parallel_harness" "$ignore_child" "$late_child" 2>/dev/null || true
  exit 1
fi
wait "$parallel_harness" 2>/dev/null || true
[ -s "$parallel_fork" ] || {
  echo "late-fork parallel lane did not exercise TERM-time descendant cleanup" >&2
  exit 1
}
read -r fork_child fork_group <"$parallel_fork"
[ "$fork_group" = "$late_wrapper" ] || {
  echo "TERM-time descendant escaped the parallel lane group" >&2
  kill -KILL "$fork_child" 2>/dev/null || true
  exit 1
}
for child in "$ignore_wrapper" "$late_wrapper" "$ignore_child" "$late_child" "$fork_child"; do
  if kill -0 "$child" 2>/dev/null; then
    echo "parallel gate cleanup orphaned process $child" >&2
    kill -KILL "$child" 2>/dev/null || true
    exit 1
  fi
done
unregister_test_group "$ignore_group"
unregister_test_group "$late_group"
rm -f "$parallel_wrappers" "$parallel_ignore" "$parallel_late" "$parallel_fork"
if grep -Eq '^\[[0-9]+\]|Terminated: [0-9]+|(^|[[:space:]])(Done|Killed)[[:space:]]' \
  "$TMP/parallel-cleanup.out"; then
  echo "parallel gate cleanup emitted job-control noise: $(cat "$TMP/parallel-cleanup.out")" >&2
  exit 1
fi

echo "gate cleanup test: passed"
