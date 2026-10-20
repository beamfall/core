#!/usr/bin/env bash
# The gate: everything that must be green before a commit ships.
# Canonical name table: tools/gate/gate.sh (= make gate).
GATE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [ "${BEAMFALL_GATE_FIXTURE_GUARDED:-0}" != 1 ]; then
  exec python3 "$GATE_ROOT/script/gate-fixture-guard.py" -- "$0" "$@"
fi
if [ "${BEAMFALL_GATE_CAPACITY_HELD:-0}" != 1 ]; then
  GATE_CAPACITY="$GATE_ROOT/script/gate-capacity.py"
  if [ ! -f "$GATE_CAPACITY" ]; then
    GATE_INVOCATION_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || true)"
    GATE_CAPACITY="$GATE_INVOCATION_ROOT/script/gate-capacity.py"
  fi
  [ -f "$GATE_CAPACITY" ] || {
    echo "gate capacity coordinator missing: $GATE_CAPACITY" >&2
    exit 2
  }
  export BEAMFALL_GATE_CAPACITY_HELD=1
  exec python3 "$GATE_CAPACITY" -- "$0" "$@"
fi
if [ "${GATE_PROCESS_SUPERVISED:-0}" != 1 ]; then
  GATE_PROCESS_SUPERVISOR="${GATE_PROCESS_SUPERVISOR:-$GATE_ROOT/script/test-process-supervisor.py}"
  [ -f "$GATE_PROCESS_SUPERVISOR" ] || {
    echo "gate process supervisor missing: $GATE_PROCESS_SUPERVISOR" >&2
    exit 2
  }
  export GATE_PROCESS_SUPERVISOR
  exec python3 "$GATE_PROCESS_SUPERVISOR" GATE_PROCESS_SUPERVISED "$0" "$@"
fi
set -euo pipefail
cd "$GATE_ROOT"
# Wall-time vs metric-sum verification (GATE-OUTER-COORDINATION-1)
# BEFORE da43f26b: wall 1752.065426s vs terminal-metric sum 1025.000000s (56 rows) -> discrepancy 727.065426s (41.50%)
# AFTER  769b06f5: wall 2505.540252s vs terminal-metric sum 2524.797579s (68 rows; 3 fail rows are the
#   coordinator's own selftest fixtures fired in a sub-second burst right after the real passing rows)
#   -> discrepancy 19.257327s (0.77%); reduced 97.35%, driven by the three outer checks (release-workflow-pins
#   0.31s, vuln-check 3.16s, contract-registry 0.10s) plus the pre-gate web smoke (185.20s) now recorded under
#   the supervisor; residual ~19s is overlapping-lane double counting plus wrapper overhead.
# Raw evidence (untracked): .agent-evidence/GATE-OUTER-COORDINATION-1-r2-walltime-vs-metrics.txt (+ *gate-runs.jsonl / *gate.log)
export GATE_RUN_ID="${GATE_RUN_ID:-direct-$$}"

configure_sandbox_go() {
  [ -n "${CODEX_SANDBOX:-}" ] || return 0
  export GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/beamfall-go-build}"
  if [ -n "${CODEX_SANDBOX_NETWORK_DISABLED:-}" ]; then
    export GOPROXY="${GATE_SANDBOX_GOPROXY:-${GOPROXY:-off}}"
  fi
}

configure_sandbox_go

# Every top-level gate job owns one process group. Nested wrappers inherit that
# group, keeping the complete descendant tree visible to the coordinator's
# cleanup trap without creating process groups known only to subshells.

gate_signal_group() {
  local signal="$1" group="$2"
  [ -n "$group" ] || return 0
  kill -s "$signal" -- "-$group" 2>/dev/null || true
}

# GATE-PREFLIGHT-1: the cheap preflight lane (GATE_PREFLIGHT=1, i.e.
# `make gate-preflight`) runs a strict subset of this gate, so its evidence must
# never be readable as a full-gate receipt. Every evidence writer keys its kind
# and its record path off this one name.
gate_evidence_kind() {
  if [ "${GATE_PREFLIGHT:-0}" = "1" ]; then
    printf 'make-gate-preflight'
  else
    printf 'make-gate'
  fi
}

GATE_OWNED_GROUPS=""
GATE_PARALLEL_RSS_DIR=""
GATE_FULL_RSS_TRACKER_PID=""
GATE_FULL_RSS_FILE=""
GATE_FULL_RSS_STOP_FILE=""
GATE_FULL_STARTED_NS=""
GATE_HOST_PHYSICAL_KB=0
GATE_HOST_AVAILABLE_KB=0
GATE_HOST_SWAP_TOTAL_KB=0

gate_group_alive() {
  kill -0 -- "-$1" 2>/dev/null
}

gate_group_inventory() {
  local group="$1"
  if [ -f "${GATE_PROCESS_SUPERVISOR:-}" ]; then
    python3 "$GATE_PROCESS_SUPERVISOR" --inventory-group "$group" >&2 || true
    return
  fi
  printf 'PID PPID PGID SID COMMAND\n' >&2
  { ps -ax -o pid=,ppid=,pgid=,sid=,command= 2>/dev/null || true; } \
    | awk -v group="$group" '$3 == group' >&2 || true
}

gate_register_group() {
  local group="$1" held
  for held in ${GATE_OWNED_GROUPS:-}; do [ "$held" = "$group" ] && return 0; done
  GATE_OWNED_GROUPS="$GATE_OWNED_GROUPS $group"
}

gate_unregister_group() {
  local target="$1" held kept=""
  for held in ${GATE_OWNED_GROUPS:-}; do
    [ "$held" = "$target" ] || kept="$kept $held"
  done
  GATE_OWNED_GROUPS="$kept"
}

gate_group_is_registered() {
  local target="$1" held
  for held in ${GATE_OWNED_GROUPS:-}; do [ "$held" = "$target" ] && return 0; done
  return 1
}

# A command can exit while one of its helpers remains in the command's process
# group. The wrapper PID is already reaped at that point, so `jobs -p` no longer
# reports the group. Retain explicit ownership until the whole group is gone.
# Return 0 for an already-empty group, 2 when a residual group was reaped, and 1
# when it could not be made quiescent (the caller keeps it registered for EXIT).
gate_retire_group() {
  local group="$1" ticks
  if ! gate_group_alive "$group"; then
    gate_unregister_group "$group"
    return 0
  fi
  gate_signal_group TERM "$group"
  gate_signal_group CONT "$group"
  ticks="${GATE_TERM_GRACE_TICKS:-20}"
  case "$ticks" in ''|*[!0-9]*) ticks=20 ;; esac
  while [ "$ticks" -gt 0 ] && gate_group_alive "$group"; do
    sleep 0.05
    ticks=$((ticks - 1))
  done
  if gate_group_alive "$group"; then gate_signal_group KILL "$group"; fi
  ticks="${GATE_KILL_WAIT_TICKS:-40}"
  case "$ticks" in ''|*[!0-9]*) ticks=40 ;; esac
  while [ "$ticks" -gt 0 ] && gate_group_alive "$group"; do
    sleep 0.05
    ticks=$((ticks - 1))
  done
  if gate_group_alive "$group"; then return 1; fi
  gate_unregister_group "$group"
  return 2
}

gate_cleanup_children() {
  local status=$? group held duplicate any attempt ticks full_rss_status=0
  local had_residual=0 survivors=""
  local groups=()
  trap - EXIT INT TERM
  # Bash 3.2 treats an empty declared array as unbound under `set -u`.
  set +u

  for group in ${GATE_OWNED_GROUPS:-}; do
    [ -n "$group" ] && groups[${#groups[@]}]="$group"
  done
  while IFS= read -r group; do
    [ -n "$group" ] || continue
    duplicate=0
    for held in "${groups[@]}"; do [ "$held" = "$group" ] && duplicate=1; done
    [ "$duplicate" -eq 1 ] || groups[${#groups[@]}]="$group"
  done < <(jobs -p 2>/dev/null || true)

  for group in "${groups[@]}"; do
    if gate_group_alive "$group"; then
      had_residual=1
      if [ "$status" -ne 0 ]; then
        printf 'gate failure process inventory for group %s:\n' "$group" >&2
        gate_group_inventory "$group"
      fi
    fi
  done
  for group in "${groups[@]}"; do gate_signal_group TERM "$group"; done
  # SIGTERM remains pending for stopped processes. Resume the group so default
  # handlers and TERM traps can run during the grace period.
  for group in "${groups[@]}"; do gate_signal_group CONT "$group"; done
  ticks="${GATE_TERM_GRACE_TICKS:-20}"
  case "$ticks" in ''|*[!0-9]*) ticks=20 ;; esac
  for attempt in $(seq 1 "$ticks"); do
    any=0
    for group in "${groups[@]}"; do
      if gate_group_alive "$group"; then
        any=1
        break
      fi
    done
    [ "$any" -eq 1 ] || break
    sleep 0.05
  done
  for group in "${groups[@]}"; do gate_signal_group KILL "$group"; done
  # KILL delivery is asynchronous and grandchildren are reaped by their new
  # parent after the visible wrapper dies. Do not return while the process group
  # is still observable: a replacement coordinator could otherwise overlap it.
  ticks="${GATE_KILL_WAIT_TICKS:-40}"
  case "$ticks" in ''|*[!0-9]*) ticks=40 ;; esac
  for attempt in $(seq 1 "$ticks"); do
    any=0
    for group in "${groups[@]}"; do
      if gate_group_alive "$group"; then
        any=1
        break
      fi
    done
    [ "$any" -eq 1 ] || break
    sleep 0.05
  done
  for group in "${groups[@]}"; do wait "$group" 2>/dev/null || true; done
  for group in "${groups[@]}"; do
    gate_group_alive "$group" && survivors="$survivors $group"
  done
  # A gate that reds before its overlapped lanes are joined never reaches
  # retire_lane, so drop the lane handoff dir here too.
  [ -z "${GATE_LANE_DIR:-}" ] || rm -rf "$GATE_LANE_DIR"
  [ -z "${GATE_PARALLEL_RSS_DIR:-}" ] || rm -rf "$GATE_PARALLEL_RSS_DIR"
  if [ -n "$survivors" ]; then
    echo "ERROR gate cleanup could not reap process groups:${survivors}" >&2
    for group in $survivors; do gate_group_inventory "$group"; done
    [ "$status" -ne 0 ] || status=1
  elif [ "$had_residual" -eq 1 ] && [ "$status" -eq 0 ]; then
    echo "ERROR gate reached successful exit with residual process groups" >&2
    status=1
  fi
  gate_full_rss_stop "$status" || full_rss_status=$?
  [ "$status" -ne 0 ] || status="$full_rss_status"
  exit "$status"
}

trap gate_cleanup_children EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

format_command() {
  local out="" arg
  for arg in "$@"; do
    printf -v out '%s%q ' "$out" "$arg"
  done
  printf '%s' "${out% }"
}

gate_monotonic_ns() {
  # Python 3.9's macOS monotonic_ns() has a process-local epoch; gate timers
  # sample in separate Python processes, so use the shared OS monotonic clock.
  python3 -c 'import time; print(time.clock_gettime_ns(time.CLOCK_MONOTONIC))'
}

gate_elapsed_ns() {  # gate_elapsed_ns <monotonic-start-ns>
  python3 - "$1" <<'PY'
import sys
import time

elapsed_ns = time.clock_gettime_ns(time.CLOCK_MONOTONIC) - int(sys.argv[1])
if elapsed_ns <= 0:
    raise SystemExit("monotonic gate timer did not advance")
print(elapsed_ns)
PY
}

gate_release_mode() {
  [[ "${GATE_MODE:-}" == "release" || "${GITHUB_WORKFLOW:-}" == "release-gate" ]]
}

validate_gate_mode() {
  case "${GATE_MODE:-}" in
    ""|normal|release) ;;
    *) echo "ERROR: GATE_MODE must be normal or release" >&2; return 1 ;;
  esac
  if [[ "${GITHUB_WORKFLOW:-}" == "release-gate" && "${GATE_MODE:-}" == "normal" ]]; then
    echo "ERROR: release-gate workflow cannot disable release mode" >&2
    return 1
  fi
}

# --- per-step peak RSS ------------------------------------------------------
#
# `gate_metric` already recorded elapsed wall time per step; peak RSS was not
# measured at all. Every owned process group here (a run_step command, a
# parallel-batch member, or a prestarted lane) was itself started with job
# control (`set -m`) as its own fork, so the group id equals its leader pid —
# the same fact gate_register_group's callers already rely on. That means
# `ps -g <pgid>` enumerates exactly that step's live process tree, so summing
# its RSS is a per-step measurement, not a host-wide one.

gate_rss_sample_kb() {  # gate_rss_sample_kb <pgid> -> summed RSS in KB, or 0
  local pgid="$1" sum
  sum="$(ps -o rss= -g "$pgid" 2>/dev/null | awk '{s+=$1} END{print s+0}')" || sum=0
  case "$sum" in ''|*[!0-9]*) sum=0 ;; esac
  printf '%s' "$sum"
}

gate_rss_sample_session_kb() {  # gate_rss_sample_session_kb <sid> -> summed RSS in KB, or 0
  local sid="$1" sum
  sum="$(ps -ax -o sess=,rss= 2>/dev/null \
    | awk -v sid="$sid" '$1 == sid {s+=$2} END{print s+0}')" || sum=0
  case "$sum" in ''|*[!0-9]*) sum=0 ;; esac
  printf '%s' "$sum"
}

# Starts a background sampler that polls a process group (or, for the full-gate
# recorder, the whole supervised session) every 0.2s and tracks the
# running maximum in a file (renamed in atomically, so a cross-process reader
# such as join_lane never sees a torn write), so a step whose peak occurs
# mid-run (not at exit) is still captured. pid_var/file_var are set (by name,
# via printf -v) to the tracker's own pid and the file the peak ends up in; an
# explicit 4th argument reuses a caller-owned path instead of a private tmp
# file, for a lane whose peak has to survive into a different process. The
# optional stop file terminates a session sampler whose root shell remains live.
#
# The tracker's own fork is force-isolated into a NEW process group via a
# local `set -m` toggle, regardless of the caller's ambient group. A run_step
# command nested inside an already-owned group (a parallel-batch member) is
# ITSELF a member of the group being watched; backgrounding the tracker there
# without this toggle would join it to that same group under `set +m`, and
# then `gate_group_alive` would never see the group die (the tracker is part
# of what keeps it "alive"), hanging the tracker forever on its own exit
# condition. Verified on a copy: without the toggle this deadlocks; with it,
# the tracker gets a distinct pgid and reports the correct peak.
gate_rss_track_start() {  # gate_rss_track_start <target> <pid_var> <file_var> [outfile] [stopfile] [scope]
  local target="$1" pid_var="$2" file_var="$3" outfile="${4:-}" stopfile="${5:-}"
  local scope="${6:-group}"
  [ -n "$outfile" ] || outfile="$(mktemp)"
  printf '0' >"$outfile"
  set -m
  (
    set +e
    peak=0
    while { [ "$scope" = session ] || gate_group_alive "$target"; } \
      && { [ -z "$stopfile" ] || [ ! -s "$stopfile" ]; }; do
      if [ "$scope" = session ]; then
        cur="$(gate_rss_sample_session_kb "$target")"
      else
        cur="$(gate_rss_sample_kb "$target")"
      fi
      [ "$cur" -gt "$peak" ] 2>/dev/null && peak="$cur"
      printf '%s' "$peak" >"$outfile.partial" 2>/dev/null
      mv -f "$outfile.partial" "$outfile" 2>/dev/null
      sleep 0.2
    done
  ) >/dev/null 2>&1 &
  printf -v "$pid_var" '%s' "$!"
  # Still waitable by PID, but absent from the job table: an interrupted gate
  # retires the watched group and lets the sampler exit without Bash emitting a
  # misleading asynchronous "Terminated" notice. The outer process supervisor
  # remains the fallback containment boundary if group retirement itself fails.
  disown "$!" 2>/dev/null || true
  set +m
  printf -v "$file_var" '%s' "$outfile"
}

# Reaps the tracker. No explicit kill: by the time a caller stops the tracker
# the group it watches is already dead, so its own loop is about to exit on
# its own; killing it here would just make bash print an async "Terminated"
# job notice that has nothing to do with the gate's own status.
#
# MUST be called as a plain statement, never via `$(...)`: `wait` only reaps a
# PID that is a direct child of the shell calling it, and a command
# substitution runs in its own forked subshell.
gate_rss_track_stop() {  # gate_rss_track_stop <tracker_pid>
  local tpid="$1"
  wait "$tpid" 2>/dev/null || true
}

# gate_rss_read_peak <outfile> -> peak KB. Safe to call via `$(...)`: it only
# reads a file, no `wait`.
gate_rss_read_peak() {
  local outfile="$1" peak
  peak="$(cat "$outfile" 2>/dev/null)" || peak=0
  case "$peak" in ''|*[!0-9]*) peak=0 ;; esac
  printf '%s' "$peak"
}

gate_metric() {
  local step="$1" status="$2" exit_code="$3" elapsed_ns="$4" command="$5" peak_rss_kb="${6:-0}" refused_skip=0
  local run_id="${GATE_RUN_ID:-direct-$$}"
  if [[ "$status" == "skip" && "$command" == scope:* ]] && gate_release_mode; then
    status="fail"
    exit_code=1
    command="mandatory release step cannot skip ($command)"
    refused_skip=1
  fi
  case "$peak_rss_kb" in ''|*[!0-9]*) peak_rss_kb=0 ;; esac
  local dir="${AGENT_EVIDENCE_DIR:-$PWD/.agent-evidence}"
  if ! mkdir -p "$dir" 2>/dev/null; then
    if [[ "$refused_skip" -eq 1 ]]; then
      echo "ERROR: mandatory release step skipped without writable evidence: $step" >&2
      return 1
    fi
    return 0
  fi
  local wrote=1
  python3 - "$dir/gate-runs.jsonl" "$step" "$status" "$exit_code" "$elapsed_ns" "$command" "$peak_rss_kb" "$run_id" "$(gate_evidence_kind)" <<'PY' || wrote=0
import datetime as dt
import json
import sys

jsonl, step, status, exit_code, elapsed_ns, command, peak_rss_kb, run_id, kind = sys.argv[1:]
elapsed_ns = int(elapsed_ns)
payload = {
    "type": "gate_step",
    "kind": kind,
    "run_id": run_id,
    "step": step,
    "status": status,
    "exit_code": int(exit_code),
    "elapsed_s": elapsed_ns // 1_000_000_000,
    "elapsed_ns": elapsed_ns,
    "peak_rss_kb": int(peak_rss_kb),
    "command": command,
    "timestamp_utc": dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ"),
}
with open(jsonl, "a", encoding="utf-8") as f:
    f.write(json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n")
PY
  # A writable directory does not guarantee a writable row: `gate-runs.jsonl`
  # can be unwritable or not a regular file. Release evidence that silently
  # records nothing is the vacuity this gate exists to refuse; local runs keep
  # treating metrics as best-effort.
  if [[ "$wrote" -eq 0 ]] && gate_release_mode; then
    echo "ERROR: release evidence row could not be written: $step" >&2
    return 1
  fi
  if [[ "$refused_skip" -eq 1 ]]; then
    echo "ERROR: mandatory release step skipped: $step" >&2
    return 1
  fi
}

gate_host_memory_snapshot() {  # physical-kb available-kb swap-total-kb
  python3 - <<'PY'
import os
from pathlib import Path
import re
import subprocess
import sys

physical_kb = os.sysconf("SC_PAGE_SIZE") * os.sysconf("SC_PHYS_PAGES") // 1024
available_kb = 0
swap_total_kb = 0
if sys.platform == "darwin":
    vm_stat = subprocess.check_output(["vm_stat"], text=True)
    page_size = int(re.search(r"page size of (\d+) bytes", vm_stat).group(1))
    pages = 0
    for name in ("free", "inactive", "speculative", "purgeable"):
        match = re.search(rf"Pages {name}:\s+(\d+)\.", vm_stat)
        pages += int(match.group(1)) if match else 0
    available_kb = pages * page_size // 1024
    try:
        swap = subprocess.check_output(
            ["sysctl", "-n", "vm.swapusage"], text=True, stderr=subprocess.DEVNULL
        )
    except (OSError, subprocess.CalledProcessError):
        swap_total_kb = -1
    else:
        match = re.search(r"total = ([0-9.]+)M", swap)
        swap_total_kb = int(float(match.group(1)) * 1024) if match else -1
else:
    meminfo = {}
    for line in Path("/proc/meminfo").read_text().splitlines():
        key, value = line.split(":", 1)
        meminfo[key] = int(value.split()[0])
    available_kb = meminfo["MemAvailable"]
    swap_total_kb = meminfo["SwapTotal"]
print(physical_kb, available_kb, swap_total_kb)
PY
}

gate_full_rss_start() {
  local sid tracker_pid="" tracker_file=""
  sid="$(ps -o sess= -p "$$" 2>/dev/null | tr -d ' ')"
  [ -n "$sid" ] || {
    echo "ERROR gate cannot resolve its session for full-run RSS measurement" >&2
    return 1
  }
  read -r GATE_HOST_PHYSICAL_KB GATE_HOST_AVAILABLE_KB GATE_HOST_SWAP_TOTAL_KB \
    < <(gate_host_memory_snapshot)
  GATE_FULL_RSS_STOP_FILE="$(mktemp)"
  gate_rss_track_start "$sid" tracker_pid tracker_file \
    "" "$GATE_FULL_RSS_STOP_FILE" session
  GATE_FULL_RSS_TRACKER_PID="$tracker_pid"
  GATE_FULL_RSS_FILE="$tracker_file"
  GATE_FULL_STARTED_NS="$(gate_monotonic_ns)"
}

gate_full_rss_stop() {  # gate_full_rss_stop <gate-status>
  local status="${1:-0}" metric_status peak_rss_kb elapsed_ns ticks=40
  local concurrency_cap=1 projected_peak_rss_kb headroom_kb swap_disabled budget_ok
  local dir="${AGENT_EVIDENCE_DIR:-$PWD/.agent-evidence}" record temporary wrote=1 kind
  local run_id="${GATE_RUN_ID:-direct-$$}"
  [ -n "$GATE_FULL_RSS_TRACKER_PID" ] || return 0
  printf 'stop\n' >"$GATE_FULL_RSS_STOP_FILE"
  gate_rss_track_stop "$GATE_FULL_RSS_TRACKER_PID"
  while [ "$ticks" -gt 0 ] && kill -0 "$GATE_FULL_RSS_TRACKER_PID" 2>/dev/null; do
    sleep 0.05
    ticks=$((ticks - 1))
  done
  if kill -0 "$GATE_FULL_RSS_TRACKER_PID" 2>/dev/null; then
    echo "ERROR full-gate RSS sampler did not stop" >&2
    return 1
  fi
  peak_rss_kb="$(gate_rss_read_peak "$GATE_FULL_RSS_FILE")"
  elapsed_ns="$(gate_elapsed_ns "$GATE_FULL_STARTED_NS")"
  projected_peak_rss_kb=$((peak_rss_kb * concurrency_cap))
  headroom_kb=$((GATE_HOST_AVAILABLE_KB - projected_peak_rss_kb))
  case "$GATE_HOST_SWAP_TOTAL_KB" in
    0) swap_disabled=true ;;
    -1) swap_disabled=null ;;
    *) swap_disabled=false ;;
  esac
  [ "$headroom_kb" -ge 0 ] && budget_ok=true || budget_ok=false
  case "$status" in
    0) metric_status=pass ;;
    75) metric_status=abort ;;
    *) metric_status=fail ;;
  esac
  kind="$(gate_evidence_kind)"
  if [ "$kind" = "make-gate" ]; then
    record="$dir/gate-peak-rss.json"
  else
    record="$dir/gate-peak-rss-preflight.json"
  fi
  temporary="$record.partial.$$"
  if mkdir -p "$dir" 2>/dev/null; then
    printf '{"type":"gate_peak_rss","kind":"%s","run_id":"%s","status":"%s","exit_code":%s,"elapsed_s":%s,"elapsed_ns":%s,"peak_rss_kb":%s,"full_gate_concurrency_cap":%s,"projected_peak_rss_kb":%s,"host_physical_memory_kb":%s,"host_available_kb_at_start":%s,"host_swap_total_kb":%s,"swap_disabled":%s,"headroom_kb":%s,"within_available_memory":%s,"timestamp_utc":"%s"}\n' \
      "$kind" "$run_id" "$metric_status" "$status" "$((elapsed_ns / 1000000000))" "$elapsed_ns" "$peak_rss_kb" "$concurrency_cap" \
      "$projected_peak_rss_kb" "$GATE_HOST_PHYSICAL_KB" "$GATE_HOST_AVAILABLE_KB" \
      "$GATE_HOST_SWAP_TOTAL_KB" "$swap_disabled" "$headroom_kb" "$budget_ok" \
      "$(date -u +%Y%m%dT%H%M%SZ)" \
      >"$temporary" 2>/dev/null && mv -f "$temporary" "$record" 2>/dev/null || wrote=0
  else
    wrote=0
  fi
  rm -f "$temporary"
  echo "gate peak_rss_kb=$peak_rss_kb cap=$concurrency_cap headroom_kb=$headroom_kb swap_kb=$GATE_HOST_SWAP_TOTAL_KB"
  rm -f "$GATE_FULL_RSS_FILE" "$GATE_FULL_RSS_FILE.partial" "$GATE_FULL_RSS_STOP_FILE"
  GATE_FULL_RSS_TRACKER_PID=""
  GATE_FULL_RSS_FILE=""
  GATE_FULL_RSS_STOP_FILE=""
  GATE_FULL_STARTED_NS=""
  if [ "$wrote" -eq 0 ] && gate_release_mode; then
    echo "ERROR full-gate peak RSS evidence could not be written" >&2
    return 1
  fi
}

gate_step_is_infrastructure_abort() {  # gate_step_is_infrastructure_abort <status> <log>
  local status="$1" log="$2"
  if [ "$status" -eq 75 ]; then
    grep -Fq 'gate infrastructure aborted' "$log"
    return
  fi
  case "$status" in 130|137|143) ;; *) return 1 ;; esac
  ! grep -Eiq '\b(FAIL|ERROR|panic|not ok|fatal)\b' "$log"
}

gate_record_infrastructure_abort() {  # gate_record_infrastructure_abort <step> <status>
  local step="$1" status="$2" receipt="${GATE_INFRASTRUCTURE_ABORT_FILE:-}"
  [ -n "$receipt" ] || return 0
  printf 'gate infrastructure aborted\nstep=%s\nstatus=%s\n' "$step" "$status" >"$receipt"
}

gate_group_wrapper() {
  local status
  # Exit from a signal trap, rather than from the signal itself, so
  # noninteractive Bash does not print job-control termination notices. The
  # nested command remains in this group for the root cleanup's grace/KILL pass.
  trap 'exit 130' INT
  trap 'exit 143' TERM
  set +m
  set +e
  "$@"
  status=$?
  set -e
  trap - INT TERM
  return "$status"
}

run_step() {
  local name="$1" log status rerun start elapsed_ns elapsed_s failure_dir failure_log slug command_pid
  local owns_group=0 retire_status=0
  local step_pgid="" rss_tracker_pid="" rss_tracker_file="" peak_rss_kb=0 pgid_tries
  shift
  rerun="$(format_command "$@")"
  log="$(mktemp)"
  start="$(gate_monotonic_ns)"
  gate_metric "$name" running 0 0 "$rerun"
  echo "RUN $name"
  set +e
  # Keep even logically foreground work visible to the EXIT/TERM cleanup trap.
  # A root invocation creates an owned group; a nested invocation (notably the
  # two slow-orchestration lanes) stays in its wrapper's existing owned group.
  if [ "${GATE_PROCESS_GROUP_OWNED:-0}" = "1" ]; then
    command_pid=""
    "$@" >"$log" 2>&1
    status=$?
  else
    set -m
    (
      export GATE_PROCESS_GROUP_OWNED=1
      gate_group_wrapper "$@"
    ) >"$log" 2>&1 &
    command_pid=$!
    gate_register_group "$command_pid"
    owns_group=1
    set +m
  fi
  # A nested parallel-batch member cannot stop its outer wrapper's sampler:
  # wait_parallel owns group retirement. It reads that sampler's atomic peak
  # handoff instead, avoiding the cleanup deadlock while retaining per-step RSS.
  if [ "$owns_group" -eq 1 ]; then
    # The step's process group id is its own leader pid (see
    # gate_rss_sample_kb); ask the OS rather than assume, since a freshly
    # backgrounded pid can take a beat to show up in `ps`.
    pgid_tries=10
    while [ "$pgid_tries" -gt 0 ]; do
      step_pgid="$(ps -o pgid= -p "$command_pid" 2>/dev/null | tr -d ' ')"
      [ -n "$step_pgid" ] && break
      pgid_tries=$((pgid_tries - 1))
      sleep 0.05
    done
    [ -n "$step_pgid" ] && gate_rss_track_start "$step_pgid" rss_tracker_pid rss_tracker_file
  elif [ -n "${GATE_INHERITED_RSS_FILE:-}" ]; then
    rss_tracker_file="$GATE_INHERITED_RSS_FILE"
  fi
  if [ -n "$command_pid" ]; then
    wait "$command_pid" 2>/dev/null
    status=$?
  fi
  if [ "$owns_group" -eq 1 ]; then
    gate_retire_group "$command_pid" || retire_status=$?
    if [ "$retire_status" -ne 0 ]; then
      printf 'ERROR gate step left a residual process group: pgid=%s cleanup_status=%s\n' \
        "$command_pid" "$retire_status" >>"$log"
      [ "$status" -ne 0 ] || status=1
    fi
  fi
  if [ -n "$rss_tracker_pid" ]; then
    gate_rss_track_stop "$rss_tracker_pid"
    peak_rss_kb="$(gate_rss_read_peak "$rss_tracker_file")"
    rm -f "$rss_tracker_file" "$rss_tracker_file.partial"
  elif [ -n "$rss_tracker_file" ]; then
    peak_rss_kb="$(gate_rss_read_peak "$rss_tracker_file")"
  fi
  set -e
  elapsed_ns="$(gate_elapsed_ns "$start")"
  elapsed_s="$((elapsed_ns / 1000000000))"
  if gate_step_is_infrastructure_abort "$status" "$log"; then
    gate_metric "$name" abort "$status" "$elapsed_ns" "$rerun" "$peak_rss_kb"
    echo "gate infrastructure aborted: step=$name status=$status" >&2
    echo "rerun: $rerun" >&2
    tail -n "${GATE_LOG_TAIL:-200}" "$log" >&2
    gate_record_infrastructure_abort "$name" "$status"
    rm -f "$log"
    return 75
  fi
  gate_metric "$name" "$([[ "$status" -eq 0 ]] && echo pass || echo fail)" "$status" "$elapsed_ns" "$rerun" "$peak_rss_kb"
  if [[ "$status" -eq 0 ]]; then
    echo "PASS $name elapsed_s=$elapsed_s"
    rm -f "$log"
    return 0
  fi
  echo "FAIL $name elapsed_s=$elapsed_s" >&2
  echo "rerun: $rerun" >&2
  if ! grep -Ei '\b(FAIL|ERROR|panic|not ok|fatal)\b' "$log" | tail -n "${GATE_LOG_TAIL:-200}" >&2; then
    tail -n "${GATE_LOG_TAIL:-200}" "$log" >&2
  fi
  {
    printf '\n[gate failure metadata]\n'
    printf 'step=%s\nstatus=%s\nrerun=%s\n' "$name" "$status" "$rerun"
  } >>"$log"
  failure_dir="${AGENT_EVIDENCE_DIR:-$PWD/.agent-evidence}/gate-failures"
  slug="$(printf '%s' "$name" | tr -cs 'A-Za-z0-9._-' '-')"
  slug="${slug%-}"
  failure_log=""
  if mkdir -p "$failure_dir" 2>/dev/null; then
    failure_log="$(mktemp "$failure_dir/$(date -u +%Y%m%dT%H%M%SZ)-${slug:-step}-${BASHPID:-$$}.XXXXXX" 2>/dev/null || true)"
  fi
  if [ -n "$failure_log" ] && cp "$log" "$failure_log" 2>/dev/null; then
    echo "full log: $failure_log" >&2
    rm -f "$log"
  else
    [ -z "$failure_log" ] || rm -f "$failure_log"
    echo "full log (temporary fallback): $log" >&2
  fi
  return "$status"
}

run_parallel_step() {
  local log status
  log="$(mktemp)"
  if run_step "$@" >"$log" 2>&1; then
    status=0
  else
    status=$?
  fi
  if [[ "$status" -eq 0 ]]; then
    cat "$log"
  else
    cat "$log" >&2
  fi
  rm -f "$log"
  return "$status"
}

start_parallel_step() {
  local pid_variable="$1" rss_tracker_pid rss_tracker_file
  shift
  [ -n "$GATE_PARALLEL_RSS_DIR" ] || GATE_PARALLEL_RSS_DIR="$(mktemp -d)"
  rss_tracker_file="$GATE_PARALLEL_RSS_DIR/$pid_variable.rss_peak"
  # The wrapper, rather than its nested run_step command, must be the process
  # group leader: only wrapper jobs are visible in the root gate's jobs table.
  set -m
  (
    export GATE_PROCESS_GROUP_OWNED=1
    export GATE_INHERITED_RSS_FILE="$rss_tracker_file"
    gate_group_wrapper run_parallel_step "$@"
  ) &
  printf -v "$pid_variable" '%s' "$!"
  gate_register_group "${!pid_variable}"
  set +m
  gate_rss_track_start "${!pid_variable}" rss_tracker_pid rss_tracker_file "$rss_tracker_file"
  printf -v "GATE_PARALLEL_RSS_TRACKER_${!pid_variable}" '%s' "$rss_tracker_pid"
  printf -v "GATE_PARALLEL_RSS_FILE_${!pid_variable}" '%s' "$rss_tracker_file"
}

wait_parallel() {
  local status=0 pid wait_status retire_status tracker_var file_var rss_tracker_pid rss_tracker_file
  for pid in "$@"; do
    wait_status=0
    wait "$pid" 2>/dev/null || wait_status=$?
    [ "$wait_status" -eq 0 ] || status=1
    if gate_group_is_registered "$pid"; then
      retire_status=0
      gate_retire_group "$pid" || retire_status=$?
      if [ "$retire_status" -ne 0 ]; then
        echo "ERROR parallel gate lane left a residual process group: pgid=$pid cleanup_status=$retire_status" >&2
        status=1
      fi
    fi
    tracker_var="GATE_PARALLEL_RSS_TRACKER_$pid"
    file_var="GATE_PARALLEL_RSS_FILE_$pid"
    rss_tracker_pid="${!tracker_var:-}"
    rss_tracker_file="${!file_var:-}"
    if ! gate_group_alive "$pid"; then
      [ -z "$rss_tracker_pid" ] || gate_rss_track_stop "$rss_tracker_pid"
      [ -z "$rss_tracker_file" ] || rm -f "$rss_tracker_file" "$rss_tracker_file.partial"
      unset "$tracker_var" "$file_var"
    fi
  done
  return "$status"
}

run_support_orchestration_tests() {
  (
    unset GATE_PREFLIGHT
    script/check-quarantine_test.sh &&
    python3 script/check_contract_registry_test.py &&
    script/client-drm-eme-check_test.sh &&
    python3 script/source_artifact_test.py &&
    script/clean-checkout-canary_test.sh &&
    script/release-gate-evidence_test.sh &&
    script/release-workflow-test.sh &&
    script/nightly-freshness_test.sh &&
    script/crossrepo-conformance_pin_test.sh &&
    python3 script/gate_capacity_test.py &&
    python3 tools/gate/gate_latest_test.py &&
    tools/gate/web_runner_test.sh &&
    script/ui_catalog_test.sh &&
    tools/gate/race_short_cache_test.sh &&
    tools/gate/gate_rerun_test.sh &&
    tools/gate/gate_cleanup_test.sh &&
    tools/gate/race_prove_test.sh
  )
}

run_gate_scope_selftest() {
  (
    unset GATE_PREFLIGHT
    tools/gate/gate_scope_test.sh
  )
}

# --- overlapped script-scoped lanes ----------------------------------------
#
# The two script-scoped orchestration lanes are latency-bound, not CPU-bound:
# they drove the roadmap dispatcher (retired by ADR-0227; these helpers now
# have no call site) through hundreds of fixture invocations plus
# barrier waits on a single thread. Measured 2026-07-25 on this host, the "slow
# orchestration" step is ~730-870s and roughly half of that is subprocess
# turnaround. Everything else in the gate is CPU-bound Go work. Running the two
# profiles back to back therefore charges the gate their full wall clock on a
# machine that is idle in exactly the dimension they need.
#
# These helpers start those lanes ahead of the gate's serial tail (see the
# prestart_lane call site for why that point and not the top of the gate) so the
# two profiles overlap. Nothing about WHAT runs changes: the same commands run
# once, in the same order, and the run_scoped_step calls further down still own
# the pass/fail verdict, the evidence row and the failure-log capture — they
# join a lane that is already running instead of starting one. When the scope
# check says the lane does not apply, nothing is prestarted and the join falls
# back to running the command inline, so the step is correct either way.

GATE_LANE_DIR=""

prestart_lane() {  # prestart_lane <slot> <command...>
  local slot="$1" pid rss_tracker_pid rss_tracker_file
  shift
  [ -n "$GATE_LANE_DIR" ] || GATE_LANE_DIR="$(mktemp -d)"
  # Same ownership shape as start_parallel_step: the wrapper subshell leads the
  # process group so the root EXIT/TERM cleanup can reap the whole tree if an
  # earlier step reds the gate while the lane is still running.
  set -m
  (
    export GATE_PROCESS_GROUP_OWNED=1
    lane_status=0
    gate_group_wrapper "$@" >"$GATE_LANE_DIR/$slot.log" 2>&1 || lane_status=$?
    printf '%s' "$lane_status" >"$GATE_LANE_DIR/$slot.status.partial"
    mv "$GATE_LANE_DIR/$slot.status.partial" "$GATE_LANE_DIR/$slot.status"
  ) &
  pid=$!
  set +m
  printf '%s' "$pid" >"$GATE_LANE_DIR/$slot.pid"
  gate_monotonic_ns >"$GATE_LANE_DIR/$slot.start"
  gate_register_group "$pid"
  # The lane's own pid is its pgid (same `set -m` fork shape as run_step and
  # start_parallel_step), so it is RSS-sampled the same way run_step samples
  # its own command. join_lane runs in a different process (it is itself a
  # run_step command), so the peak is handed off through a file at a known
  # path instead of a shell variable.
  gate_rss_track_start "$pid" rss_tracker_pid rss_tracker_file "$GATE_LANE_DIR/$slot.rss_peak"
  printf '%s' "$rss_tracker_pid" >"$GATE_LANE_DIR/$slot.rss_tracker_pid"
  echo "PRESTART $slot"
}

# Runs as the step body, i.e. inside run_step's own child process. A child
# cannot `wait` on its parent's other children, so the lane's completion is
# observed through its process group and its verdict is read from the status
# file the lane writes last. A lane that dies without writing one fails closed.
join_lane() {  # join_lane <slot> <fallback-command...>
  local slot="$1" pid status wall_ns wall_s peak_rss_kb=0
  shift
  [ -n "$GATE_LANE_DIR" ] && [ -f "$GATE_LANE_DIR/$slot.pid" ] || { "$@"; return $?; }
  pid="$(cat "$GATE_LANE_DIR/$slot.pid")"
  while gate_group_alive "$pid"; do sleep 0.2; done
  # Give the lane's own RSS sampler (same 0.2s cadence, prestart_lane) one more
  # tick to land its atomic rename of the final peak before this reads it.
  sleep 0.3
  wall_ns="$(gate_elapsed_ns "$(cat "$GATE_LANE_DIR/$slot.start")")"
  wall_s="$((wall_ns / 1000000000))"
  cat "$GATE_LANE_DIR/$slot.log" 2>/dev/null || true
  if [ ! -f "$GATE_LANE_DIR/$slot.status" ]; then
    echo "gate infrastructure aborted: overlapped lane $slot exited without recording a verdict" >&2
    return 75
  fi
  status="$(cat "$GATE_LANE_DIR/$slot.status")"
  peak_rss_kb="$(cat "$GATE_LANE_DIR/$slot.rss_peak" 2>/dev/null)"
  case "$peak_rss_kb" in ''|*[!0-9]*) peak_rss_kb=0 ;; esac
  # The run_step row above this measures only the join, so record the lane's own
  # wall clock too: the per-step profile is what this gate is tuned against.
  gate_metric "$slot orchestration lane (overlapped)" \
    "$([ "$status" -eq 0 ] && echo pass || echo fail)" "$status" "$wall_ns" "overlap:$slot" "$peak_rss_kb"
  echo "overlapped lane $slot wall_s=$wall_s peak_rss_kb=$peak_rss_kb"
  return "$status"
}

# The lane is drained by the time join_lane returns; retiring it here (in the
# shell that owns the group list) keeps gate_cleanup_children from reporting a
# residual process group on an otherwise green exit.
retire_lane() {  # retire_lane <slot>
  local slot="$1" pid retire_status=0 rss_tracker_pid
  [ -n "$GATE_LANE_DIR" ] && [ -f "$GATE_LANE_DIR/$slot.pid" ] || return 0
  pid="$(cat "$GATE_LANE_DIR/$slot.pid")"
  if gate_group_is_registered "$pid"; then
    gate_retire_group "$pid" || retire_status=$?
  fi
  # The RSS sampler is this shell's own background job (prestart_lane forked it
  # here, not inside the lane's group), so it is reaped the same way run_step
  # reaps its own tracker: wait for its natural exit, never kill -> no bogus
  # "Terminated" notice, and no zombie left for gate_cleanup_children to flag
  # as a residual process group.
  if [ -f "$GATE_LANE_DIR/$slot.rss_tracker_pid" ]; then
    rss_tracker_pid="$(cat "$GATE_LANE_DIR/$slot.rss_tracker_pid" 2>/dev/null)"
    [ -n "$rss_tracker_pid" ] && gate_rss_track_stop "$rss_tracker_pid"
  fi
  # The lane's handoff files are scratch, and a gate that leaked one mktemp dir
  # per run would quietly fill TMPDIR on a host that runs this gate all day.
  rm -f "$GATE_LANE_DIR/$slot.pid" "$GATE_LANE_DIR/$slot.start" \
    "$GATE_LANE_DIR/$slot.status" "$GATE_LANE_DIR/$slot.log" \
    "$GATE_LANE_DIR/$slot.rss_peak" "$GATE_LANE_DIR/$slot.rss_peak.partial" \
    "$GATE_LANE_DIR/$slot.rss_tracker_pid"
  rmdir "$GATE_LANE_DIR" 2>/dev/null || true
  if [ "$retire_status" -eq 1 ]; then
    echo "ERROR overlapped lane $slot left a residual process group: pgid=$pid" >&2
    return 1
  fi
  return 0
}

changed_paths() {
  local base="${GATE_BASE_SHA:-}" head="${GATE_HEAD_SHA:-}" base_sha head_sha merge_base_sha
  local committed staged unstaged
  if [ -n "$base$head" ]; then
    [ -n "$base" ] && [ -n "$head" ] || return 1
    base_sha="$(git rev-parse --verify "$base^{commit}" 2>/dev/null)" || return 1
    head_sha="$(git rev-parse --verify "$head^{commit}" 2>/dev/null)" || return 1
    git merge-base --is-ancestor "$base_sha" "$head_sha" 2>/dev/null || return 1
    git diff --name-only "$base_sha" "$head_sha" || return 1
    return 0
  fi
  staged="$(git diff --name-only --cached 2>/dev/null)" || return 1
  unstaged="$(git diff --name-only 2>/dev/null)" || return 1
  # Local scope is the union of committed branch changes and tracked dirt. Diff
  # branch commits from the merge base so a dirty worktree cannot hide them.
  # Untracked-only trees do not add paths, but still retain the committed range.
  head_sha="$(git rev-parse --verify 'HEAD^{commit}' 2>/dev/null)" || return 1
  base_sha="$(git rev-parse --verify 'refs/heads/main^{commit}' 2>/dev/null)" || return 1
  merge_base_sha="$(git merge-base "$base_sha" "$head_sha" 2>/dev/null)" || return 1
  committed="$(git diff --name-only "$merge_base_sha" "$head_sha" 2>/dev/null)" || return 1
  printf '%s\n%s\n%s\n' "$committed" "$staged" "$unstaged" | sort -u | sed '/^$/d'
}

configure_race_short_scope() {
  local base_sha head_sha
  export GATE_DIFF_BASE="" GATE_DIFF_HEAD=""
  gate_release_mode && return 0

  if [ -n "${GATE_BASE_SHA:-}${GATE_HEAD_SHA:-}" ]; then
    [ -n "${GATE_BASE_SHA:-}" ] && [ -n "${GATE_HEAD_SHA:-}" ] || return 0
    base_sha="$(git rev-parse --verify "$GATE_BASE_SHA^{commit}" 2>/dev/null)" || return 0
    head_sha="$(git rev-parse --verify "$GATE_HEAD_SHA^{commit}" 2>/dev/null)" || return 0
    git merge-base --is-ancestor "$base_sha" "$head_sha" 2>/dev/null || return 0
  else
    head_sha="$(git rev-parse --verify 'HEAD^{commit}' 2>/dev/null)" || return 0
    base_sha="$(git merge-base 'refs/heads/main' "$head_sha" 2>/dev/null)" || return 0
  fi
  export GATE_DIFF_BASE="$base_sha" GATE_DIFF_HEAD="$head_sha"
}

scope_matches() {
  local scope="$1" paths path
  if gate_release_mode; then
    return 0
  fi
  if ! paths="$(changed_paths)"; then
    return 0
  fi
  [ -n "$paths" ] || return 1
  while IFS= read -r path; do
    case "$scope:$path" in
      # Inputs read or executed by the support orchestration suite.
      script:script/*|script:tools/gate/*|script:tools/orient/*) return 0 ;;
      script:AGENTS.md|script:Makefile|script:go.mod|script:go.sum|script:go.work) return 0 ;;
      script:.github/workflows/release.yml|script:.github/workflows/release-gate.yml) return 0 ;;
      script:contracts/*|script:internal/conformance/*) return 0 ;;
      script:cmd/ioe2e/*|script:internal/racefixture/*) return 0 ;;
      script:internal/server/*|script:internal/web/app/package.json) return 0 ;;
      # script/release-gate-evidence_test.sh runs `cmd/ioe2e release-packages`, which walks
      # internal/ and cmd/ for *_test.go (cmd/ioe2e/main.go:71-72). Those test files are suite
      # inputs, so they must RUN — this case has to precede the broad internal/cmd skip below.
      script:internal/*_test.go|script:cmd/*_test.go) return 0 ;;
      # The remaining non-test sources under these classes cannot affect the orchestration tooling.
      # Anything outside them remains unclassified and therefore fails broad.
      script:README.md|script:docs/*|script:internal/*|script:cmd/*) ;;
      script:*) return 0 ;;
      # Certification builds and exercises the server, so preserve its previous
      # broad scope independently of the orchestration-only script scope.
      cert:README.md) ;;
      cert:*) return 0 ;;
      web:internal/web/*) return 0 ;;
      # The browser smoke owns a wider closure than the app-only web checks: it
      # builds and drives the real server through /web/ and its API handlers.
      webe2e:internal/web/*|webe2e:internal/server/*|webe2e:cmd/beamfall/*) return 0 ;;
      webe2e:Makefile|webe2e:go.mod|webe2e:go.sum|webe2e:go.work) return 0 ;;
      webe2e:script/web-ui-*|webe2e:script/test-process-supervisor.py) return 0 ;;
      webe2e:tools/gate/web-*|webe2e:tools/gate/web_*) return 0 ;;
      image:Dockerfile|image:go.mod|image:internal/ffmpeg/ffmpeg.pin|image:internal/ffmpeg/ffmpeg.lock) return 0 ;;
      contract:contracts/*|contract:internal/conformance/*|contract:internal/graph/*|contract:tools/wire-contract/*) return 0 ;;
      contract:Makefile|contract:testing/release-gates/gate-commands.tsv|contract:script/crossrepo-conformance.sh|contract:script/crossrepo-conformance_test.sh|contract:script/crossrepo-conformance_pin_test.sh|contract:script/crossrepo-conformance_interrupt_test.py) return 0 ;;
      contract:tools/gate/gate.sh|contract:tools/gate/gate_scope_test.sh) return 0 ;;
    esac
  done <<<"$paths"
  return 1
}

run_scoped_step() {
  local name="$1" scope="$2"
  shift 2
  if scope_matches "$scope"; then
    run_step "$name" "$@"
  else
    gate_metric "$name" skip 0 0 "scope:$scope"
    echo "SKIP $name elapsed_s=0 (no $scope changes)"
  fi
}

run_web_app() {
  local script="$1"
  if ! command -v npm >/dev/null 2>&1; then
    echo "ERROR: npm not found — canonical web app checks are release-gating; install npm" >&2
    return 1
  fi
  if [[ ! -d internal/web/app/node_modules ]]; then
    npm --prefix internal/web/app ci --no-audit --no-fund
  fi
  npm --prefix internal/web/app run "$script"
}

run_cert_demo_tests() {
  run_step "certification demo server test" script/cert-demo-server_test.sh
  run_step "certification demo server smoke test" script/cert-demo-server-smoke_test.sh
}

# GATE-PREFLIGHT-1: the fail-fast lane (`make gate-preflight`).
#
# On 2026-07-30 five of six LANDCHAIN gate rounds died in a step that costs
# seconds — a roadmap data lint, the compile-budget schedule golden, the
# AGENTS.md line cap, gosec — after the run had already paid up to 30 minutes of
# compile, coverage and race lanes. Every one of those verdicts was available
# before the expensive tail started.
#
# Membership rule: a step belongs here when it is a selftest or a static
# data/convention lint whose measured median in `.agent-evidence/gate-runs.jsonl`
# is seconds, and it is NOT a Go test, race, coverage, image-build or web-build
# lane. `make lint` is in: gosec is one of the named 2026-07-30 casualties and a
# linter is none of the excluded lanes (it is the only member whose cold-cache
# cost is minutes rather than seconds).
#
# This lane does not replace the gate and is not a receipt: it is a strict
# subset, it runs the SAME command for each member as the full gate does (proved
# line by line by tools/gate/gate_scope_test.sh), and its evidence is written
# under a distinct kind (gate_evidence_kind) so it can never be read as one.
#
# Membership is spelled `preflight_step` rather than `run_step` for one reason:
# the compile-budget manifest selftest asserts that each gate step is REGISTERED
# in the gate body exactly once, and a second literal `run_step "<name>" <cmd>`
# line would break that structural invariant while changing nothing about what
# runs. The wrapper keeps registration single-sourced and makes membership in
# this lane greppable.
full_gate_only_step() { run_step "$@"; }
preflight_step() { run_step "$@"; }
preflight_scoped_step() { run_scoped_step "$@"; }

run_gate_preflight_steps() {
  preflight_step "release workflow pins check" make release-workflow-pins-check
  preflight_step "contract registry check" python3 script/check-contract-registry.py
  preflight_step "quarantine manifest" script/check-quarantine.sh
  preflight_step "gate fixture guard selftest" python3 script/gate_fixture_guard_test.py
  preflight_step "API docs drift selftest" python3 script/check_api_docs_test.py
  preflight_step "API docs drift" python3 script/check-api-docs.py
  preflight_step "fuzz manifest selftest" python3 script/fuzz_manifest_test.py
  preflight_step "fuzz manifest" go run ./tools/fuzzmanifest
  preflight_step "mutation policy selftest" python3 script/mutation_policy_test.py
  preflight_step "mutation policy history" python3 script/mutation-policy.py
  preflight_step "mutation runner selftest" python3 script/mutation_runner_test.py
  preflight_step "process supervisor token selftest" python3 script/process_supervisor_token_test.py
  preflight_step "SHA-256 manifest selftest" python3 script/sha256_manifest_test.py
  preflight_step "SHA-256 manifest validation" python3 script/sha256-manifest.py validate internal/conformance/testdata/goldens.sha256
  preflight_step "subtitle manifest validation" python3 script/sha256-manifest.py validate internal/conformance/testdata/subtitle_selection/manifest.sha256
  preflight_step "runtime image buildability selftest" tools/gate/image-buildability_test.sh
  preflight_step "gofmt" bash -c 'unformatted=$(gofmt -l cmd internal tools 2>/dev/null || true); if [[ -n "$unformatted" ]]; then echo "gofmt needed:" >&2; echo "$unformatted" >&2; exit 1; fi'
  preflight_step "golangci-lint (staticcheck + gosec)" make lint
  preflight_step "client DRM/EME invariant" script/client-drm-eme-check.sh
  preflight_step "scenario ledger" go run ./cmd/scenariocheck -strict
  preflight_step "E2E smoke (io-ops ledger)" go run ./cmd/ioe2e check
  preflight_step "egress silent-gap linter" go run ./cmd/ioe2e egress-lint
  preflight_scoped_step "support script tests" script run_support_orchestration_tests
  preflight_step "hardcoded-string gate" tools/gate/hardcoded-strings.sh
  preflight_step "AGPL forbidden-symbol gate selftest" tools/gate/agpl-forbidden-symbols_test.sh
  preflight_step "AGPL forbidden-symbol gate" tools/gate/agpl-forbidden-symbols.sh
  preflight_step "secret scan selftest" tools/gate/secret-scan_test.sh
  preflight_step "secret scan (Core + present siblings; best-effort)" tools/gate/secret-scan.py
  preflight_step "App Store firewall static gate selftest" script/app-store-firewall-static-check_test.sh
  preflight_step "App Store firewall static gate" script/app-store-firewall-static-check.sh
  preflight_step "vidparity HDR tone-map gate selftest" tools/gate/vidparity-hdr-tonemap-gate_test.sh
  preflight_step "doc-links gate selftest" tools/gate/check-doc-links_test.sh
  preflight_step "doc-links gate" tools/gate/check-doc-links.sh
  preflight_step "gate scope selftest" run_gate_scope_selftest
  preflight_step "tracked-vs-gitignore gate selftest" tools/gate/tracked-vs-gitignore_test.sh
  preflight_step "tracked-vs-gitignore gate" tools/gate/tracked-vs-gitignore.sh
  preflight_step "go-test -count=1 audit gate selftest" tools/gate/go-test-count1-check_test.sh
  preflight_step "go-test -count=1 audit gate" tools/gate/go-test-count1-check.sh
  preflight_step "compile budget manifest selftest" tools/gate/compile-budget-manifest_test.sh
  preflight_step "subtitleingest stdlib-only selftest" tools/gate/subtitleingest-stdlib-check_test.sh
  preflight_step "subtitleingest stdlib-only gate" tools/gate/subtitleingest-stdlib-check.sh
  preflight_step "scale fixture throughput gate selftest" tools/gate/scale-fixture-throughput_test.sh
  preflight_step "backup AGPL firewall gate selftest" tools/gate/backup-agpl-firewall_test.sh
  preflight_step "backup AGPL firewall gate" tools/gate/backup-agpl-firewall.sh
  # BKUP-CHAIN-1-RESIDUAL-1: the preflight lane must mirror every non-excluded full-gate
  # step (gate scope selftest rule 1), so the three bounded backup selftests and the
  # PR-scoped drill registered below are members here too, same names + commands.
  preflight_step "backup fixture staleness gate selftest" tools/gate/backup-fixture-staleness_test.sh
  preflight_step "backup restore trigger gate selftest" tools/gate/backup-restore-trigger_test.sh
  preflight_step "backup restore drill scope gate selftest" tools/gate/backup-restore-drill-scope_test.sh
  preflight_step "backup restore drill (PR-scoped)" tools/gate/backup-restore-drill-pr.sh
  preflight_step "internet-exposure gate" tools/gate/internet-exposure.sh
  preflight_scoped_step "design token drift gate" web node tools/gate/design-token-drift.mjs
}

run_outer_checks() {
  if [[ "${GATE_PREFLIGHT:-0}" == "1" ]]; then
    return
  fi
  run_scoped_step "web UI E2E smoke (pre-gate)" webe2e make web-ui-e2e-smoke
  run_step "release workflow pins check" make release-workflow-pins-check
  full_gate_only_step "vulnerability check" make vuln-check
  run_step "contract registry check" python3 script/check-contract-registry.py
}

validate_gate_mode
gate_full_rss_start

if [[ "${GATE_CERT_DEMO_ONLY:-}" == "1" ]]; then
  run_cert_demo_tests
  exit 0
fi

run_outer_checks

# The preflight lane exits here: nothing below this point is reachable in
# preflight mode, so the full gate's own body is untouched by it.
if [[ "${GATE_PREFLIGHT:-0}" == "1" ]]; then
  run_gate_preflight_steps
  echo "gate-preflight: green"
  exit 0
fi

# ADR-0121 §6: quarantine manifest — fails if any entry is expired or has no
# expiresAt (unbounded quarantine is prohibited); also fails if entry count
# exceeds the hard cap. Runs first so a stale quarantine is caught before tests.
run_step "quarantine manifest" script/check-quarantine.sh
run_step "gate fixture guard selftest" python3 script/gate_fixture_guard_test.py
run_step "API docs drift selftest" python3 script/check_api_docs_test.py
run_step "API docs drift" python3 script/check-api-docs.py
run_step "fuzz manifest selftest" python3 script/fuzz_manifest_test.py
run_step "fuzz manifest" go run ./tools/fuzzmanifest
run_step "mutation policy selftest" python3 script/mutation_policy_test.py
run_step "mutation policy history" python3 script/mutation-policy.py
run_step "mutation runner selftest" python3 script/mutation_runner_test.py
run_step "process supervisor token selftest" python3 script/process_supervisor_token_test.py
run_step "SHA-256 manifest selftest" python3 script/sha256_manifest_test.py
run_step "SHA-256 manifest validation" python3 script/sha256-manifest.py validate internal/conformance/testdata/goldens.sha256
run_step "subtitle manifest validation" python3 script/sha256-manifest.py validate internal/conformance/testdata/subtitle_selection/manifest.sha256

# Build only the host-architecture production runtime stage when one of its
# version-bearing inputs changes. The synthetic build context avoids private
# module credentials; the target separately executes the pinned toolchains.
run_step "runtime image buildability selftest" tools/gate/image-buildability_test.sh
run_scoped_step "runtime image buildability" image make image-buildability-check

run_step "gofmt" bash -c 'unformatted=$(gofmt -l cmd internal tools 2>/dev/null || true); if [[ -n "$unformatted" ]]; then echo "gofmt needed:" >&2; echo "$unformatted" >&2; exit 1; fi'
run_step "web app build" run_web_app verify:build

# GATE-COMPILE-BUDGET-1: keep at most four compile-heavy jobs live. Launching
# all eight at once drove load near 96 and made each compiler graph slower while
# multiplying its resident set. Refill each slot as its measured incumbent
# finishes instead of imposing a full-wave barrier.
start_parallel_step pid_golangci_lint "golangci-lint (staticcheck + gosec)" make lint
start_parallel_step pid_go_build "go build" go build ./...

# ADR-0121 Pillar B (TEST-CORE-2): fuzz targets run against committed seed
# corpora deterministically to catch immediate crashes on seeds. Active fuzzing
# runs in the nightly/release tiers, not the local serial gate.
start_parallel_step pid_fuzz "fuzz" make fuzz
start_parallel_step pid_capability_manifest "capability manifest" script/capability-manifest-check.sh
wait_parallel "$pid_fuzz"
start_parallel_step pid_go_vet "go vet" go vet ./...
wait_parallel "$pid_go_build"
start_parallel_step pid_client_drm "client DRM/EME invariant" script/client-drm-eme-check.sh
wait_parallel "$pid_golangci_lint"

# ADR-0121 Pillar A: the feature-coverage ratchet is the per-target
# anti-regression floor committed in testing/feature-coverage-ratchet.json.
# Per-area strictness fails closed on any missing cell in a named area. API,
# ingest, legal, and onboarding are 100% today, so locking them costs nothing
# and is strictly stronger than the ratchet's aggregate per-target count: one
# proven cell cannot be silently traded for a newly proven cell. Adding an area
# requires that area to be at 100%, or the gate goes red.
start_parallel_step pid_feature_coverage "feature-coverage ratchet" go run ./cmd/featurecheck -ratchet testing/feature-coverage-ratchet.json -strict-areas api,ingest,legal,onboarding
wait_parallel "$pid_capability_manifest"
start_parallel_step pid_scenario_ledger "scenario ledger" go run ./cmd/scenariocheck -strict
wait_parallel "$pid_go_vet" "$pid_client_drm" "$pid_feature_coverage" "$pid_scenario_ledger"

# ADR-0121 §4: per-package line-coverage floor — fails if any package drops
# below its committed floor in testing/coverage-floor.json. Seeded 3pp below
# the measured baseline; lower a floor intentionally with a commit message
# explaining the regression. This also runs `go test -coverprofile ./...`, so
# there is no separate uninstrumented `go test ./...` pass above.
# Run the tagged lane first: capability-manifest-check has just warmed that
# build graph. Sequencing prevents the tagged and coverage compilers from
# peaking together and lets both reuse the persistent Go build cache.
run_step "go test -tags beamfall_appstore conformance capabilities graph" go test -tags beamfall_appstore -count=1 ./internal/conformance ./internal/capabilities ./internal/graph
run_scoped_step "ADR-0122 cross-repo wire conformance" contract make crossrepo-conformance
run_step "go test + line-coverage ratchet" go run ./cmd/covratchet -floor testing/coverage-floor.json

# Real-encoder wall-clock SLOs run only after mandatory parallel work drains;
# coverage still exercises the deterministic budget classification above.
run_step "playback transcode latency budgets" env BEAMFALL_PERF=1 BEAMFALL_PERF_LOAD_WORKERS=1 GOMAXPROCS=2 go test ./internal/playback -run '^Test(TranscodeLatencyBudgetsOnReferenceClip|InjectedPlaybackLatencyRegressionReds)$' -v -count=1 -timeout 45s
run_step "pinned FFmpeg tone-map conformance" make ffmpeg-tonemap-conforming

# The script-scoped orchestration suites are process-heavy. Keep them in the
# serial tail: overlapping roadmap fixtures with race-short previously made
# both slower and multiplied their resident-memory demand on swapless hosts.

# ADR-0121 §3 (TEST-CORE-1): the `-race` subset runs IN this gate. It was
# previously skipped here on the grounds that a separate CI lane preserved a
# merge-blocking signal — but hosted GitHub Actions is not a merge gate
# (operator decision, 2026-07-22; see AGENTS.md §Testing contract), so that lane
# was no signal and `-race` was running nowhere in the merge path. §3 prescribes
# exactly this shape: a `-short` race subset in-gate, full `-race ./...` nightly
# (`make race-full`). Measured 2026-07-24: race-short ~8m under load, race-prove
# ~0.5s. graph/store stay excluded — they time out under the detector (§3).
#
# race-prove runs FIRST and is nearly free: it asserts the detector actually
# fires on a deliberately-racy fixture. Without it a green race-short cannot be
# distinguished from a detector that silently is not running.
run_step "race-prove" make race-prove
configure_race_short_scope
run_step "race-short" make race-short

# ADR-0121 §5 (TEST-CORE-4): E2E smoke — io-ops ledger completeness check.
# Runs `cmd/ioe2e check`: cross-references every exported *ffmpeg.Runner method,
# `// io:<id>` markers in test sources, and testing/io-ops.yaml; fails if any
# op is uncovered, un-ignored, and undocumented-blocked. No ffmpeg or network
# required — pure static analysis + YAML. Fast (<2 s). A missing marker or
# undocumented gap reds this step and therefore reds the gate.
run_step "E2E smoke (io-ops ledger)" go run ./cmd/ioe2e check

# D2 egress silent-gap linter: AST-scans the production internal/ tree for raw
# http.Client constructions and reds the gate on any whose package has no egress
# op in testing/io-ops.yaml and no //io:ignore — catching a new egress added
# without a ledger entry. Pure static analysis, no network. Fast (<2 s).
run_step "egress silent-gap linter" go run ./cmd/ioe2e egress-lint

run_scoped_step "support script tests" script run_support_orchestration_tests

if scope_matches cert; then
  run_cert_demo_tests
else
  gate_metric "certification demo server test" skip 0 0 "scope:cert"
  echo "SKIP certification demo server test elapsed_s=0 (no cert changes)"
  gate_metric "certification demo server smoke test" skip 0 0 "scope:cert"
  echo "SKIP certification demo server smoke test elapsed_s=0 (no cert changes)"
fi

# ADR-0034 §4: a hardcoded user-facing string is a CI-greppable defect. Pure
# grep over the React web UI source, anchored on the locale catalog
# (strings.ts). Plugin display strings are
# locale maps enforced by the Go type system (go build, above), so they need no
# grep here. Deterministic and fast; see the script header for full scope.
run_step "hardcoded-string gate" tools/gate/hardcoded-strings.sh

# ADR-0024 / invariant #7: entitlement, billing, account, and licensing code
# belongs in the proprietary cloud/client repos, never in AGPL Core.
run_step "AGPL forbidden-symbol gate selftest" tools/gate/agpl-forbidden-symbols_test.sh
run_step "AGPL forbidden-symbol gate" tools/gate/agpl-forbidden-symbols.sh
run_step "secret scan selftest" tools/gate/secret-scan_test.sh
run_step "secret scan (Core + present siblings; best-effort)" tools/gate/secret-scan.py
run_step "App Store firewall static gate selftest" script/app-store-firewall-static-check_test.sh
run_step "App Store firewall static gate" script/app-store-firewall-static-check.sh

# ADR-0075 Amendment 2026-07-26: planted-red coverage for the GA hardware HDR
# tone-mapping launch gate. The gate itself is release-only (it stays RED until
# a named approver signs real two-host hardware runs), so its SELFTEST is what
# keeps it honest in the merge gate: every case plants one defect into evidence
# the gate accepts, and a green run proves it discriminates rather than that it
# is inert.
run_step "vidparity HDR tone-map gate selftest" tools/gate/vidparity-hdr-tonemap-gate_test.sh


# fixes.md 2026-07-06 "Reviewer-flagged guard scripts": would have caught
# ARCHITECTURE.md/TESTING.md's phantom `internal/ffmpeg/ffmpeg.pin` citation
# before ab70b0ab fixed it docs-side. Walks current onboarding, architecture,
# API, glossary, design, operations, security, ADR-index, and plans-index docs
# for dangling repo-relative Markdown links and backtick-quoted tree paths.
# Historical/future corpora stay outside this current-doc gate; known designed
# gaps are allowlisted in the script itself.
run_step "doc-links gate selftest" tools/gate/check-doc-links_test.sh
run_step "doc-links gate" tools/gate/check-doc-links.sh

# fixes.md 2026-07-06 "Reviewer-flagged guard scripts": catches half-tracked
# drift where a path is both `git`-tracked and `.gitignore`-matched (the
# `internal/web/app-dist/` / OSS-16 class — a stale tracked snapshot behind
# an ignore rule added after the fact). Known debt is allowlisted in the
# script; a NEW hit reds the gate.
run_step "gate scope selftest" run_gate_scope_selftest
run_step "tracked-vs-gitignore gate selftest" tools/gate/tracked-vs-gitignore_test.sh
run_step "tracked-vs-gitignore gate" tools/gate/tracked-vs-gitignore.sh

# GATE-GOCACHE-PERSIST-1: gate_latest.py defaults GOCACHE to a persistent,
# reused directory instead of a fresh-then-discarded tempdir. That makes
# `go test`'s own result cache load-bearing, so every executable `go test`
# call under tools/ and script/ must carry -count=1 (else a warm cache could
# silently replay a stale PASS instead of re-running) — fails closed on
# future additions that omit it.
run_step "go-test -count=1 audit gate selftest" tools/gate/go-test-count1-check_test.sh
run_step "go-test -count=1 audit gate" tools/gate/go-test-count1-check.sh
run_step "compile budget manifest selftest" tools/gate/compile-budget-manifest_test.sh
run_step "subtitleingest stdlib-only selftest" tools/gate/subtitleingest-stdlib-check_test.sh
run_step "subtitleingest stdlib-only gate" tools/gate/subtitleingest-stdlib-check.sh

# GCR-038-A: prove the DSCALE-1 cached-corpus throughput evaluator accepts
# healthy raw timings and rejects a deterministic regression fixture.
run_step "scale fixture throughput gate selftest" tools/gate/scale-fixture-throughput_test.sh

# ADR-0115 §5: Core backup exposes only accountless, vendor-neutral BYO
# primitives. Hosted Store/Premium/account decisions stay cloud/client-only.
run_step "backup AGPL firewall gate selftest" tools/gate/backup-agpl-firewall_test.sh
run_step "backup AGPL firewall gate" tools/gate/backup-agpl-firewall.sh

# BKUP-CHAIN-1-RESIDUAL-1 r2 gap 2: these two selftests proved their own
# red (staleness drift, trigger selection) but were never registered here —
# both are bounded shell selftests (seconds), so they run unconditionally,
# no env-var opt-in.
run_step "backup fixture staleness gate selftest" tools/gate/backup-fixture-staleness_test.sh
run_step "backup restore trigger gate selftest" tools/gate/backup-restore-trigger_test.sh
run_step "backup restore drill scope gate selftest" tools/gate/backup-restore-drill-scope_test.sh

# BKUP-CHAIN-1-RESIDUAL-1 r2 gap 3: the trigger selector (above) could only
# ever be invoked by hand — nothing computed this lane's real changed-path
# list and fed it in, so the restore drill never actually blocked a merge.
# tools/gate/backup-restore-drill-pr.sh computes that list via
# backup-restore-drill-scope.sh and runs backup-restore-drill.sh --mode pr
# (bounded 3-fixture PR drill for matching lanes, sub-second "none" otherwise;
# the nightly full corpus stays release-gate-only via `make backup-restore-drill`).
# It is a script so the same single-line command registers in the preflight lane.
run_step "backup restore drill (PR-scoped)" tools/gate/backup-restore-drill-pr.sh

run_step "internet-exposure gate" tools/gate/internet-exposure.sh

run_scoped_step "design token drift gate" web node tools/gate/design-token-drift.mjs

# The canonical React app (internal/web/app, ADR-0007) is the post-cutover web
# surface served at /web/. Static verification and the production build ran
# above before Go embedded app-dist; web-scoped changes still run unit tests.
run_scoped_step "web app typecheck + tests" web run_web_app test

run_scoped_step "web UI E2E smoke (second pass)" webe2e make web-ui-e2e-smoke

echo "gate: green"
