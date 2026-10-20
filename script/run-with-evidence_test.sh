#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNNER="$ROOT/script/run-with-evidence.sh"
CAPACITY="$ROOT/script/gate-capacity.py"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/run-with-evidence-test.XXXXXX")"
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
