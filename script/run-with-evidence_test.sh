#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNNER="$ROOT/script/run-with-evidence.sh"
CAPACITY="$ROOT/script/gate-capacity.py"
SLOT="$ROOT/script/gate-slot.py"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/run-with-evidence-test.XXXXXX")"
# The machine gate slot these runs take is a private one, not the real queue.
export BEAMFALL_GATE_SLOT_DIR="$TMP/slot"
unset BEAMFALL_GATE_SLOT_HELD BEAMFALL_GATE_SLOT_NOWAIT
TEST_PIDS=""

cleanup() {
  local pid
  trap - EXIT INT TERM
  for pid in $TEST_PIDS; do
    kill -TERM "$pid" 2>/dev/null || true
  done
  for pid in $TEST_PIDS; do
    wait "$pid" 2>/dev/null || true
  done
  rm -rf -- "$TMP"
}
trap cleanup EXIT INT TERM

fail() {
  echo "run-with-evidence_test: $*" >&2
  exit 1
}

wait_for_path() {
  local path="$1" deadline=$((SECONDS + 5))
  while [ ! -e "$path" ]; do
    [ "$SECONDS" -lt "$deadline" ] || return 1
    sleep 0.02
  done
}

repo="$TMP/repo"
fake_bin="$TMP/bin"
mkdir -p "$repo" "$fake_bin"
git init -q -b main "$repo"
git -C "$repo" config user.email run-with-evidence@example.invalid
git -C "$repo" config user.name "Run With Evidence Test"
printf 'base\n' >"$repo/tracked"
git -C "$repo" add tracked
git -C "$repo" -c commit.gpgsign=false commit -q -m base

cat >"$fake_bin/make" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
[ "${1:-}" = gate ] || exit 64
[ "${BEAMFALL_GATE_CAPACITY_HELD:-0}" = 1 ] || exit 65
python3 "$FAKE_CAPACITY" --held "$PWD"
printf 'started\n' >"$FAKE_GATE_STARTED"
SH
chmod +x "$fake_bin/make"

holder_started="$TMP/holder-started"
holder_release="$TMP/holder-release"
mkfifo "$holder_release"
(
  cd "$repo"
  exec python3 "$CAPACITY" -- sh -c \
    'touch "$1"; IFS= read -r _ <"$2"' sh "$holder_started" "$holder_release"
) &
holder_pid=$!
TEST_PIDS="$TEST_PIDS $holder_pid"
wait_for_path "$holder_started" || fail "capacity holder did not start"

gate_started="$TMP/gate-started"
(
  cd "$repo"
  export AGENT_EVIDENCE_DIR="$TMP/gate-evidence"
  export FAKE_CAPACITY="$CAPACITY"
  export FAKE_GATE_STARTED="$gate_started"
  export PATH="$fake_bin:$PATH"
  exec "$RUNNER" --label queued-gate -- make gate
) >"$TMP/gate.stdout" 2>"$TMP/gate.stderr" &
gate_pid=$!
TEST_PIDS="$TEST_PIDS $gate_pid"

for _ in $(seq 1 20); do
  [ ! -e "$gate_started" ] || fail "gate overlapped the capacity holder"
  kill -0 "$gate_pid" 2>/dev/null || fail "queued gate exited before capacity was released"
  sleep 0.02
done

non_gate_started="$TMP/non-gate-started"
(
  cd "$repo"
  export AGENT_EVIDENCE_DIR="$TMP/non-gate-evidence"
  exec "$RUNNER" --label non-gate -- sh -c 'touch "$1"' sh "$non_gate_started" make gate
) >"$TMP/non-gate.stdout" 2>"$TMP/non-gate.stderr" &
non_gate_pid=$!
TEST_PIDS="$TEST_PIDS $non_gate_pid"
wait_for_path "$non_gate_started" || fail "gate-like argument data waited for gate capacity"
wait "$non_gate_pid" || fail "non-gate command failed"

printf 'release\n' >"$holder_release"
wait "$holder_pid" || fail "capacity holder failed"
wait "$gate_pid" || fail "queued gate failed after capacity was released"
[ -e "$gate_started" ] || fail "queued gate did not run after capacity was released"

echo "PASS run-with-evidence gate capacity routing"

# The machine gate slot comes before repository capacity: a wrapped gate that
# waits for the slot must leave capacity free for the gate that holds the slot.
slot_started="$TMP/slot-started"
slot_release="$TMP/slot-release"
mkfifo "$slot_release"
(
  cd "$TMP"
  exec python3 "$SLOT" -- sh -c \
    'touch "$1"; IFS= read -r _ <"$2"' sh "$slot_started" "$slot_release"
) 2>"$TMP/slot-holder.stderr" &
slot_pid=$!
TEST_PIDS="$TEST_PIDS $slot_pid"
wait_for_path "$slot_started" || fail "slot holder did not start"

slot_gate_started="$TMP/slot-gate-started"
(
  cd "$repo"
  export AGENT_EVIDENCE_DIR="$TMP/slot-gate-evidence"
  export FAKE_CAPACITY="$CAPACITY"
  export FAKE_GATE_STARTED="$slot_gate_started"
  export PATH="$fake_bin:$PATH"
  exec "$RUNNER" --label slot-gate -- make gate
) >"$TMP/slot-gate.stdout" 2>"$TMP/slot-gate.stderr" &
slot_gate_pid=$!
TEST_PIDS="$TEST_PIDS $slot_gate_pid"

queued() {
  find "$BEAMFALL_GATE_SLOT_DIR/queue" -name '*.json' 2>/dev/null | wc -l | tr -d ' '
}
deadline=$((SECONDS + 10))
while [ "$(queued)" != 2 ]; do
  [ "$SECONDS" -lt "$deadline" ] || fail "wrapped gate did not queue for the machine gate slot"
  sleep 0.02
done
for _ in $(seq 1 20); do
  [ ! -e "$slot_gate_started" ] || fail "wrapped gate overlapped the slot holder"
  if python3 "$CAPACITY" --held "$repo"; then
    fail "wrapped gate held repository capacity while it waited for the slot"
  fi
  kill -0 "$slot_gate_pid" 2>/dev/null || fail "queued gate exited before the slot was released"
  sleep 0.02
done

printf 'release\n' >"$slot_release"
wait "$slot_pid" || fail "slot holder failed"
wait "$slot_gate_pid" || fail "queued gate failed after the slot was released"
[ -e "$slot_gate_started" ] || fail "queued gate did not run after the slot was released"

echo "PASS run-with-evidence takes the machine gate slot before capacity"

# A caller that already holds repository capacity must not queue for the slot
# with it: the wrapped gate runs at once, next to the slot holder.
busy_started="$TMP/busy-started"
busy_release="$TMP/busy-release"
mkfifo "$busy_release"
(
  cd "$TMP"
  exec python3 "$SLOT" -- sh -c \
    'touch "$1"; IFS= read -r _ <"$2"' sh "$busy_started" "$busy_release"
) 2>"$TMP/busy-holder.stderr" &
busy_pid=$!
TEST_PIDS="$TEST_PIDS $busy_pid"
wait_for_path "$busy_started" || fail "second slot holder did not start"

inherited_started="$TMP/inherited-started"
(
  cd "$repo"
  export AGENT_EVIDENCE_DIR="$TMP/inherited-evidence"
  export FAKE_CAPACITY="$CAPACITY"
  export FAKE_GATE_STARTED="$inherited_started"
  export PATH="$fake_bin:$PATH"
  exec python3 "$CAPACITY" -- "$RUNNER" --label inherited-gate -- make gate
) >"$TMP/inherited.stdout" 2>"$TMP/inherited.stderr" &
inherited_pid=$!
TEST_PIDS="$TEST_PIDS $inherited_pid"
deadline=$((SECONDS + 10))
while kill -0 "$inherited_pid" 2>/dev/null; do
  [ "$SECONDS" -lt "$deadline" ] || fail "gate under inherited capacity waited for the slot"
  sleep 0.02
done
wait "$inherited_pid" || fail "gate under inherited capacity failed: $(cat "$TMP/inherited.stderr")"
[ -e "$inherited_started" ] || fail "gate under inherited capacity did not run"
[ "$(queued)" = 1 ] || fail "gate under inherited capacity joined the slot queue"
kill -0 "$busy_pid" 2>/dev/null || fail "slot holder ended before the gate under inherited capacity"

printf 'release\n' >"$busy_release"
wait "$busy_pid" || fail "second slot holder failed"

echo "PASS run-with-evidence does not queue for the slot under inherited capacity"
