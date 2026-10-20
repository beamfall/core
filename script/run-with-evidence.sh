#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_ROOT="${AGENT_EVIDENCE_DIR:-$ROOT/.agent-evidence}"
GIT_BIN="${AGENT_EVIDENCE_GIT_BIN:-git}"

usage() {
  cat >&2 <<'USAGE'
usage: script/run-with-evidence.sh [--label LABEL] -- COMMAND [ARG...]

Runs COMMAND, captures stdout/stderr, and writes a reviewable evidence packet:
output.log, summary.txt, metadata.json, sha256.txt. Exits with COMMAND's status.
USAGE
}

label=""
while [ $# -gt 0 ]; do
  case "$1" in
    --label)
      [ $# -ge 2 ] && [ "$2" != "--" ] || { usage; exit 2; }
      label="$2"
      shift 2
      ;;
    --) shift; break ;;
    -h|--help) usage; exit 0 ;;
    *) break ;;
  esac
done

[ $# -gt 0 ] || { usage; exit 2; }

command_is_make_gate() {
  [ "${1##*/}" = make ] && [ "${2:-}" = gate ]
}

run_command=("$@")
if [ "${BEAMFALL_GATE_CAPACITY_HELD:-0}" != 1 ] && command_is_make_gate "$@"; then
  capacity="$ROOT/script/gate-capacity.py"
  [ -f "$capacity" ] || {
    echo "run-with-evidence: gate capacity coordinator missing: $capacity" >&2
    exit 70
  }
  export BEAMFALL_GATE_CAPACITY_HELD=1
  run_command=(python3 "$capacity" -- "$@")
fi

repo_marker_present() {
  python3 - "$PWD" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1]).resolve()
for candidate in (path, *path.parents):
    if (candidate / ".git").exists():
        raise SystemExit(0)
raise SystemExit(1)
PY
}

git_root=""
git_start_sha=""
git_start_status_short=""
git_start_status=0
if git_root="$("$GIT_BIN" -C "$PWD" rev-parse --show-toplevel 2>/dev/null)"; then
  if ! git_start_sha="$("$GIT_BIN" -C "$git_root" rev-parse --verify HEAD 2>/dev/null)"; then
    echo "run-with-evidence: cannot read repository HEAD" >&2
    exit 70
  fi
  if ! git_start_status_short="$("$GIT_BIN" -C "$git_root" status --short 2>/dev/null)"; then
    echo "run-with-evidence: cannot inspect repository status before command" >&2
    exit 70
  fi
  git_start_status="$(printf '%s\n' "$git_start_status_short" | awk 'NF { count++ } END { print count + 0 }')"
else
  # A directory with no repository marker is intentionally supported. A marker
  # (or explicit GIT_DIR) plus a failed rev-parse is an inspection error, not a
  # license to serialize false non-repository/clean provenance.
  if repo_marker_present || [ -n "${GIT_DIR:-}" ]; then
    echo "run-with-evidence: cannot resolve repository root" >&2
    exit 70
  fi
  git_root=""
fi

mkdir -p "$OUT_ROOT"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
slug="${label:-$1}"
slug="$(printf '%s' "$slug" | tr -cs 'A-Za-z0-9._-' '-' | sed -E 's/^-+|-+$//g')"
[ -n "$slug" ] || slug="command"
dir="$(mktemp -d "$OUT_ROOT/${stamp}-${slug}.XXXXXX")"
artifact_dir="$(cd "$dir" && pwd -P)"

output="$dir/output.log"
summary="$dir/summary.txt"
meta="$dir/metadata.json"
sumfile="$dir/sha256.txt"
supervisor="$ROOT/script/test-process-supervisor.py"
if [ ! -f "$supervisor" ]; then
  supervisor="$(python3 - "$0" <<'PY'
from pathlib import Path
import sys
print(Path(sys.argv[1]).resolve().with_name("test-process-supervisor.py"))
PY
)"
fi

start_epoch="$(date +%s)"
supervisor_pid=""
interrupt_signal=""
interrupt_status=""
interrupt() {
  [ -n "$interrupt_status" ] || {
    interrupt_signal="$1"
    interrupt_status="$2"
  }
  [ -z "$supervisor_pid" ] || kill -"$1" "$supervisor_pid" 2>/dev/null || true
}
cleanup_supervisor() {
  local saved_status=$?
  trap - EXIT INT TERM
  if [ -n "$supervisor_pid" ] && kill -0 "$supervisor_pid" 2>/dev/null; then
    kill -TERM "$supervisor_pid" 2>/dev/null || true
    while kill -0 "$supervisor_pid" 2>/dev/null; do
      wait "$supervisor_pid" 2>/dev/null || true
    done
  fi
  return "$saved_status"
}
trap cleanup_supervisor EXIT
trap 'interrupt INT 130' INT
trap 'interrupt TERM 143' TERM

set +e
python3 -c '
import os
from pathlib import Path
import runpy
import signal
import subprocess
import sys
import time

supervisor, env_flag, *command = sys.argv[1:]
blocked = {signal.SIGHUP, signal.SIGINT, signal.SIGTERM}
# Preserve termination delivered after spawn but before the shared supervisor
# installs its forwarding handlers. The command shim restores its normal mask.
signal.pthread_sigmask(signal.SIG_BLOCK, blocked)

real_signal = signal.signal
installed = set()

def install(sig, handler):
    previous = real_signal(sig, handler)
    if sig in blocked and callable(handler):
        installed.add(sig)
        if installed == blocked:
            signal.pthread_sigmask(signal.SIG_UNBLOCK, blocked)
    return previous

signal.signal = install

barrier = os.environ.get("RUN_WITH_EVIDENCE_SUPERVISOR_STARTUP_BARRIER")
real_popen = subprocess.Popen

def spawn(*args, **kwargs):
    process = real_popen(*args, **kwargs)
    if barrier:
        Path(f"{barrier}.ready").write_text("ready\n", encoding="utf-8")
        while not Path(f"{barrier}.go").exists():
            time.sleep(0.01)
    return process

subprocess.Popen = spawn
unblock_command = (
    "import os,signal,sys;"
    "signal.pthread_sigmask(signal.SIG_UNBLOCK,"
    "{signal.SIGHUP,signal.SIGINT,signal.SIGTERM});"
    "os.execvp(sys.argv[1],sys.argv[1:])"
)
sys.argv = [
    supervisor,
    env_flag,
    sys.executable,
    "-c",
    unblock_command,
    *command,
]
runpy.run_path(supervisor, run_name="__main__")
' "$supervisor" RUN_WITH_EVIDENCE_PROCESS_SUPERVISED "${run_command[@]}" >"$output" 2>&1 &
supervisor_pid=$!
[ -z "$interrupt_signal" ] || kill -"$interrupt_signal" "$supervisor_pid" 2>/dev/null || true
while :; do
  wait "$supervisor_pid"
  status=$?
  kill -0 "$supervisor_pid" 2>/dev/null || break
done
[ -z "$interrupt_status" ] || status="$interrupt_status"
supervisor_pid=""
trap - EXIT INT TERM
set -e
end_epoch="$(date +%s)"
elapsed=$((end_epoch - start_epoch))

python3 - "$output" "$summary" "$status" "$elapsed" "$@" <<'PY'
import re
import sys

output, summary, status, elapsed, *cmd = sys.argv[1:]
status = int(status)
with open(output, encoding="utf-8", errors="replace") as f:
    lines = [line.rstrip("\n") for line in f]

step_re = re.compile(r"^(PASS|FAIL|SKIP)\s+(.+)$")
steps = []
for line in lines:
    match = step_re.match(line)
    if match:
        steps.append((match.group(1), match.group(2)))

failures = [
    line
    for line in lines
    if re.search(r"\b(FAIL|ERROR|panic|not ok|fatal:)\b", line, re.IGNORECASE)
]

out = []
if steps:
    out.extend(f"{state} {name}" for state, name in steps[:9])
    passed = sum(1 for state, _ in steps if state == "PASS")
    failed = sum(1 for state, _ in steps if state == "FAIL")
    skipped = sum(1 for state, _ in steps if state == "SKIP")
    out.append(f"result: pass={passed} fail={failed} skip={skipped} exit={status}")
else:
    out.append(f"command: {' '.join(cmd)}")
    out.append(f"result: exit={status} elapsed_s={elapsed} output_lines={len(lines)}")

if status != 0:
    out.append("failures:")
    detail = failures[-6:] if failures else lines[-6:]
    out.extend(detail)

with open(summary, "w", encoding="utf-8") as f:
    f.write("\n".join(out[:10]))
    f.write("\n")
PY

sum="$(shasum -a 256 "$output" | awk '{print $1}')"
summary_sum="$(shasum -a 256 "$summary" | awk '{print $1}')"
output_bytes="$(wc -c < "$output" | tr -d ' ')"
summary_bytes="$(wc -c < "$summary" | tr -d ' ')"
git_sha=""
git_status_short=""
git_status=0
if [ -n "$git_root" ]; then
  if ! git_sha="$("$GIT_BIN" -C "$git_root" rev-parse --verify HEAD 2>/dev/null)"; then
    echo "run-with-evidence: cannot read repository HEAD after command" >&2
    exit 70
  fi
  if ! git_status_short="$("$GIT_BIN" -C "$git_root" status --short 2>/dev/null)"; then
    echo "run-with-evidence: cannot inspect repository status after command" >&2
    exit 70
  fi
  git_status="$(printf '%s\n' "$git_status_short" | awk 'NF { count++ } END { print count + 0 }')"
fi

python3 - "$meta" "$status" "$sum" "$summary_sum" "$git_root" "$git_start_sha" "$git_start_status" "$git_start_status_short" "$git_sha" "$git_status" "$stamp" "$elapsed" "$output_bytes" "$summary_bytes" "$artifact_dir" "$git_status_short" "${VERSION:-}" "${CHANNEL:-}" "${COMMIT:-}" "${INVOCATION_ID:-}" "$@" <<'PY'
import json
import os
import sys

path, status, sha, summary_sha, git_root, git_start_sha, git_start_status, git_start_status_short, git_sha, git_status, stamp, elapsed, output_bytes, summary_bytes, artifact_dir, git_status_short, version, channel, commit, invocation_id, *cmd = sys.argv[1:]
payload = {
    "artifact_dir": artifact_dir,
    "command": cmd,
    "elapsed_s": int(elapsed),
    "exit_code": int(status),
    "git_end_sha": git_sha,
    "git_root": git_root,
    "git_sha": git_sha,
    "git_start_sha": git_start_sha,
    "git_start_status_paths": int(git_start_status or 0),
    "git_start_status_short": git_start_status_short.splitlines(),
    "git_status_paths": int(git_status or 0),
    "git_status_short": git_status_short.splitlines(),
    "output": "output.log",
    "output_bytes": int(output_bytes),
    "sha256": sha,
    "summary": "summary.txt",
    "summary_bytes": int(summary_bytes),
    "summary_sha256": summary_sha,
    "timestamp_utc": stamp,
    "type": "command",
}
release_identity = {
    key: value
    for key, value in {
        "version": version,
        "channel": channel,
        "commit": commit,
        "invocation_id": invocation_id,
    }.items()
    if value
}
if release_identity:
    payload["release_identity"] = release_identity
with open(path, "w", encoding="utf-8") as f:
    json.dump(payload, f, indent=2, sort_keys=True)
    f.write("\n")
jsonl = os.path.join(os.path.dirname(os.path.dirname(path)), "runs.jsonl")
with open(jsonl, "a", encoding="utf-8") as f:
    f.write(json.dumps(payload, sort_keys=True, separators=(",", ":")))
    f.write("\n")
PY

meta_sum="$(shasum -a 256 "$meta" | awk '{print $1}')"
printf '%s  output.log\n' "$sum" > "$sumfile"
printf '%s  summary.txt\n' "$summary_sum" >> "$sumfile"
printf '%s  metadata.json\n' "$meta_sum" >> "$sumfile"

cat "$summary" >&2
printf 'evidence: %s\nsha256: %s\nexit: %s\n' "$dir" "$sum" "$status" >&2
exit "$status"
