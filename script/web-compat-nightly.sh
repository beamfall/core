#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP="$ROOT/internal/web/app"
PROCESS_SUPERVISOR="${BEAMFALL_PROCESS_SUPERVISOR:-$ROOT/script/test-process-supervisor.py}"
active_supervisor_pid=""
launching_supervisor=0
pending_exit=0
output_dir="${BEAMFALL_WEB_COMPAT_OUTPUT_DIR:-}"
cleanup_output=0
provenance_file=""
execution_file=""
pending_receipt=""
receipt_file=""
publisher_file=""

cleanup() {
  local status=$?
  trap - EXIT
  trap '' INT TERM
  if [[ -n "$active_supervisor_pid" ]]; then
    kill -TERM "$active_supervisor_pid" 2>/dev/null || true
    wait "$active_supervisor_pid" 2>/dev/null || true
  fi
  if [[ -n "$pending_receipt" ]]; then
    rm -f "$pending_receipt"
  fi
  if [[ -n "$publisher_file" ]]; then
    rm -f "$publisher_file"
  fi
  if [[ -n "$provenance_file" ]]; then
    rm -f "$provenance_file"
  fi
  if [[ -n "$execution_file" ]]; then
    rm -f "$execution_file"
  fi
  if [[ $cleanup_output -eq 1 && $status -eq 0 ]]; then
    rm -rf "$output_dir"
  elif [[ $cleanup_output -eq 1 ]]; then
    echo "Playwright artifacts kept at $output_dir" >&2
  fi
  exit "$status"
}
trap cleanup EXIT

on_signal() {
  local status="$1"
  if [[ $launching_supervisor -eq 1 ]]; then
    pending_exit="$status"
    return
  fi
  exit "$status"
}

trap 'on_signal 130' INT
trap 'on_signal 143' TERM

if [[ $# -ne 0 ]]; then
  echo "FAIL web-compat-nightly filtered/custom Playwright arguments cannot publish evidence" >&2
  exit 2
fi

provenance_file="$(mktemp "${TMPDIR:-/tmp}/beamfall-web-compat-provenance.XXXXXX")"
execution_file="$(mktemp "${TMPDIR:-/tmp}/beamfall-web-compat-execution.XXXXXX")"
rm -f "$execution_file"
if [[ -z "$output_dir" ]]; then
  output_dir="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-web-compat.XXXXXX")"
  cleanup_output=1
fi

run_tracked() {
  local status=0
  launching_supervisor=1
  python3 "$PROCESS_SUPERVISOR" BEAMFALL_WEB_COMPAT_CHILD_PROCESS_SUPERVISED "$@" &
  active_supervisor_pid=$!
  launching_supervisor=0
  if [[ $pending_exit -ne 0 ]]; then
    exit "$pending_exit"
  fi
  wait "$active_supervisor_pid" || status=$?
  active_supervisor_pid=""
  return "$status"
}

cd "$APP"
source_commit="$(git -C "$ROOT" rev-parse HEAD)"
run_tracked npm exec playwright install firefox webkit
node --input-type=module - "$APP" >"$provenance_file" <<'NODE'
import { existsSync, readFileSync } from "node:fs";
import { arch, platform, release } from "node:os";
import { join } from "node:path";
import process from "node:process";
import { firefox, webkit } from "playwright";

const app = process.argv[2];
const lock = JSON.parse(readFileSync(join(app, "package-lock.json"), "utf8"));
const registry = JSON.parse(
  readFileSync(join(app, "node_modules", "playwright-core", "browsers.json"), "utf8"),
);
const playwrightVersion = lock.packages?.["node_modules/@playwright/test"]?.version;
if (typeof playwrightVersion !== "string" || playwrightVersion.length === 0) {
  throw new Error("package-lock.json does not pin @playwright/test");
}
const installedPlaywrightVersion = JSON.parse(
  readFileSync(join(app, "node_modules", "@playwright", "test", "package.json"), "utf8"),
).version;
if (installedPlaywrightVersion !== playwrightVersion) {
  throw new Error(
    `installed Playwright ${installedPlaywrightVersion} differs from lockfile ${playwrightVersion}`,
  );
}

const browserTypes = { firefox, webkit };
const browsers = registry.browsers
  .filter(({ name }) => name === "firefox" || name === "webkit")
  .map(({ name, revision, browserVersion }) => {
    if (!existsSync(browserTypes[name].executablePath())) {
      throw new Error(`${name} executable is not installed`);
    }
    return { name, revision, browserVersion };
  });
if (browsers.length !== 2) {
  throw new Error("Playwright browser registry is missing Firefox or WebKit");
}

process.stdout.write(
  `${JSON.stringify({
    host: { os: platform(), release: release(), arch: arch() },
    playwrightVersion,
    source: "package-lock.json + playwright-core/browsers.json",
    browsers,
  }, null, 2)}\n`,
);
NODE
echo "web-compat provenance:"
cat "$provenance_file"
export BEAMFALL_WEB_COMPAT_EXECUTION_PROVENANCE="$execution_file"
run_tracked npm run e2e -- \
  --project web-compat-firefox \
  --project web-compat-webkit \
  --output "$output_dir" \
  --reporter "list,./e2e/lib/require-executed-tests.mjs"
if [[ ! -s "$execution_file" ]]; then
  echo "FAIL web-compat-nightly missing executed-test provenance" >&2
  exit 1
fi
current_commit="$(git -C "$ROOT" rev-parse HEAD)"
if [[ "$current_commit" != "$source_commit" ]]; then
  echo "FAIL web-compat-nightly source changed during run before=$source_commit after=$current_commit" >&2
  exit 1
fi

evidence_dir="${RELEASE_NIGHTLY_EVIDENCE_DIR:-$ROOT/.agent-evidence/nightly-last-green}"
receipt_file="$evidence_dir/web-compat-nightly.json"
timestamp="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
mkdir -p "$evidence_dir"
pending_receipt="$(mktemp "$evidence_dir/.web-compat-nightly.XXXXXX")"
publisher_file="$(mktemp "${TMPDIR:-/tmp}/beamfall-web-compat-publisher.XXXXXX.py")"
cat >"$publisher_file" <<'PY'
import fcntl
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

(
    path,
    pending,
    lock_path,
    root,
    source_commit,
    timestamp,
    provenance_path,
    execution_path,
) = sys.argv[1:]
with open(provenance_path, encoding="utf-8") as source:
    provenance = json.load(source)
with open(execution_path, encoding="utf-8") as source:
    execution = json.load(source)
if set(execution) != {"schemaVersion", "executedTestCount"}:
    raise SystemExit("web-compat execution provenance has an invalid schema")
if execution.get("schemaVersion") != 1:
    raise SystemExit("web-compat execution provenance has an invalid schema version")
executed_test_count = execution.get("executedTestCount")
if type(executed_test_count) is not int or executed_test_count <= 0:
    raise SystemExit("web-compat execution provenance executed zero tests")
provenance["executedTestCount"] = executed_test_count

def interrupted(signum, _frame):
    raise SystemExit(128 + signum)

def current_head():
    return subprocess.check_output(
        ["git", "-C", root, "rev-parse", "HEAD"], text=True
    ).strip()

for caught_signal in (signal.SIGHUP, signal.SIGINT, signal.SIGTERM):
    signal.signal(caught_signal, interrupted)

installed = False
with open(lock_path, "a+", encoding="utf-8") as publication_lock:
    fcntl.flock(publication_lock, fcntl.LOCK_EX)
    try:
        head = current_head()
        if head != source_commit:
            raise SystemExit(
                f"FAIL web-compat-nightly source changed before publication "
                f"expected={source_commit} actual={head}"
            )
        with open(pending, "w", encoding="utf-8") as destination:
            json.dump(
                {
                    "commit": source_commit,
                    "sourceCommit": source_commit,
                    "repo": "beamfall",
                    "status": "green",
                    "tier": "web-compat-nightly",
                    "timestamp": timestamp,
                    "provenance": provenance,
                },
                destination,
                indent=2,
            )
            destination.write("\n")
        blocked_signals = {signal.SIGHUP, signal.SIGINT, signal.SIGTERM}
        previous_mask = signal.pthread_sigmask(signal.SIG_BLOCK, blocked_signals)
        try:
            os.replace(pending, path)
            if os.environ.get("BEAMFALL_WEB_COMPAT_SIGNAL_AFTER_REPLACE") == "1":
                os.kill(os.getpid(), signal.SIGTERM)
            installed = True
        finally:
            signal.pthread_sigmask(signal.SIG_SETMASK, previous_mask)
        barrier = os.environ.get("BEAMFALL_WEB_COMPAT_PUBLISH_BARRIER")
        if barrier:
            Path(f"{barrier}.ready").write_text(f"{os.getpid()}\n", encoding="utf-8")
            while not Path(f"{barrier}.go").exists():
                time.sleep(0.01)
        head = current_head()
        if head != source_commit:
            raise SystemExit(
                f"FAIL web-compat-nightly source changed at publication "
                f"expected={source_commit} actual={head}"
            )
    except BaseException:
        try:
            os.unlink(pending)
        except FileNotFoundError:
            pass
        if installed:
            try:
                os.unlink(path)
            except FileNotFoundError:
                pass
        raise
PY
run_tracked python3 "$publisher_file" \
  "$receipt_file" "$pending_receipt" "$evidence_dir/.web-compat-nightly.publish.lock" \
  "$ROOT" "$source_commit" "$timestamp" "$provenance_file" "$execution_file"
rm -f "$publisher_file"
publisher_file=""
pending_receipt=""
echo "$receipt_file"
