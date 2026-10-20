#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [[ "${BEAMFALL_BACKUP_RESTORE_INTERRUPT_TEST_SUPERVISED:-0}" != 1 ]]; then
  exec python3 "$ROOT/script/test-process-supervisor.py" \
    BEAMFALL_BACKUP_RESTORE_INTERRUPT_TEST_SUPERVISED "$0" "$@"
fi

TMP="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-backup-interrupt.XXXXXX")"
RUNNERS=""
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  for runner in $RUNNERS; do
    kill -KILL "$runner" 2>/dev/null || true
    wait "$runner" 2>/dev/null || true
  done
  rm -rf "$TMP"
  exit "$status"
}
trap cleanup EXIT INT TERM

mkdir -p "$TMP/bin" "$TMP/fixture"
cat >"$TMP/bin/go" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
trap '' INT TERM
sh -c '
  trap "" INT TERM
  printf "%s\n" "$$" >"$1.descendant"
  while :; do sleep 1; done
' backup-interrupt-descendant "$BACKUP_INTERRUPT_PID_FILE" &
descendant=$!
printf '%s\n' "$$" >"${BACKUP_INTERRUPT_PID_FILE}.child"
wait "$descendant"
SH
chmod +x "$TMP/bin/go"

wait_for_file() {
  local path="$1"
  for _ in {1..100}; do
    [[ -s "$path" ]] && return 0
    sleep 0.05
  done
  echo "backup restore interruption fixture did not start: $path" >&2
  return 1
}

wait_for_exit() {
  local pid="$1" state
  for _ in {1..100}; do
    kill -0 "$pid" 2>/dev/null || return 0
    state="$(ps -p "$pid" -o stat= 2>/dev/null || true)"
    [[ "$state" == Z* ]] && return 0
    sleep 0.05
  done
  return 1
}

run_interrupt_case() {
  local name="$1"
  shift
  local pid_file="$TMP/$name" runner child descendant status
  PATH="$TMP/bin:$PATH" BACKUP_INTERRUPT_PID_FILE="$pid_file" "$@" &
  runner=$!
  RUNNERS="$runner"
  wait_for_file "$pid_file.child"
  wait_for_file "$pid_file.descendant"
  child="$(<"$pid_file.child")"
  descendant="$(<"$pid_file.descendant")"

  kill -TERM "$runner"
  set +e
  wait "$runner"
  status=$?
  set -e
  RUNNERS=""
  [[ "$status" -ne 0 ]] || {
    echo "$name interruption exited zero" >&2
    return 1
  }
  wait_for_exit "$child" || {
    echo "$name left child $child running" >&2
    return 1
  }
  wait_for_exit "$descendant" || {
    echo "$name left descendant $descendant running" >&2
    return 1
  }
}

run_interrupt_case fixtures "$ROOT/script/backup-fixtures.sh" drill "$TMP/fixture"
run_interrupt_case drill "$ROOT/tools/gate/backup-restore-drill.sh" --mode nightly
