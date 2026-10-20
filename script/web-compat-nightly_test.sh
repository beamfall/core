#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "${WEB_COMPAT_NIGHTLY_TEST_PROCESS_SUPERVISED:-0}" != 1 ]]; then
  exec python3 "$ROOT/script/test-process-supervisor.py" \
    WEB_COMPAT_NIGHTLY_TEST_PROCESS_SUPERVISED "$0" "$@"
fi
tmp="$(mktemp -d "${TMPDIR:-/tmp}/web-compat-nightly-test.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir -p "$tmp/bin" "$tmp/output"

cat >"$tmp/bin/npm" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$WEB_COMPAT_CALLS"
if [[ "${WEB_COMPAT_INTERRUPT:-0}" -eq 1 && "$*" == "run e2e -- "* ]]; then
  trap '' INT TERM
  printf '%s\n' "$$" >"${WEB_COMPAT_LEADER_PID_FILE}"
  (
    trap '' INT TERM
    exec sleep 60
  ) &
  descendant_pid=$!
  printf '%s\n' "${descendant_pid}" >"${WEB_COMPAT_DESCENDANT_PID_FILE}"
  wait "${descendant_pid}"
fi
if [[ "${WEB_COMPAT_ORPHAN:-0}" -eq 1 && "$*" == "run e2e -- "* ]]; then
  (
    trap '' INT TERM
    exec sleep 60
  ) &
  printf '%s\n' "$!" >"${WEB_COMPAT_ORPHAN_PID_FILE}"
  exit 0
fi
if [[ "${WEB_COMPAT_FAIL:-0}" -eq 1 ]]; then
  exit 1
fi
if [[ "$*" == "run e2e -- "* && -n "${BEAMFALL_WEB_COMPAT_EXECUTION_PROVENANCE:-}" ]]; then
  case "${WEB_COMPAT_EXECUTION_MODE:-valid}" in
    valid) printf '{"schemaVersion":1,"executedTestCount":4}\n' >"${BEAMFALL_WEB_COMPAT_EXECUTION_PROVENANCE}" ;;
    zero) printf '{"schemaVersion":1,"executedTestCount":0}\n' >"${BEAMFALL_WEB_COMPAT_EXECUTION_PROVENANCE}" ;;
    absent) ;;
    *) exit 64 ;;
  esac
fi
STUB
cat >"$tmp/bin/git" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${WEB_COMPAT_PUBLICATION_HEAD_CHANGE:-0}" -eq 1 && $# -eq 4 && \
  "$1" = -C && "$2" = "${WEB_COMPAT_TEST_REPO}" && "$3" = rev-parse && "$4" = HEAD ]]; then
  count=0
  if [[ -s "${WEB_COMPAT_GIT_COUNTER}" ]]; then
    count="$(<"${WEB_COMPAT_GIT_COUNTER}")"
  fi
  count=$((count + 1))
  printf '%s\n' "$count" >"${WEB_COMPAT_GIT_COUNTER}"
  if [[ $count -ge 4 ]]; then
    printf '%040d\n' 1
    exit 0
  fi
fi
exec "${WEB_COMPAT_REAL_GIT}" "$@"
STUB
chmod +x "$tmp/bin/npm" "$tmp/bin/git"

calls="$tmp/calls"
export WEB_COMPAT_REAL_GIT="$(command -v git)"
export WEB_COMPAT_TEST_REPO="$ROOT"
set +e
PATH="$tmp/bin:$PATH" \
  WEB_COMPAT_CALLS="$calls" \
  BEAMFALL_WEB_COMPAT_OUTPUT_DIR="$tmp/output" \
  RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp/evidence" \
  "$ROOT/script/web-compat-nightly.sh" --grep filtered \
  >"$tmp/filter.stdout" 2>"$tmp/filter.stderr"
filter_status=$?
set -e
test "$filter_status" -eq 2
grep -Fxq \
  'FAIL web-compat-nightly filtered/custom Playwright arguments cannot publish evidence' \
  "$tmp/filter.stderr"
test ! -s "$calls"
test ! -e "$tmp/evidence/web-compat-nightly.json"

PATH="$tmp/bin:$PATH" \
  WEB_COMPAT_CALLS="$calls" \
  BEAMFALL_WEB_COMPAT_OUTPUT_DIR="$tmp/output" \
  RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp/evidence" \
  "$ROOT/script/web-compat-nightly.sh"

grep -q '^exec playwright install firefox webkit$' "$calls"
grep -q -- '--project web-compat-firefox --project web-compat-webkit' "$calls"
grep -q -- '--reporter list,./e2e/lib/require-executed-tests.mjs' "$calls"
test -f "$tmp/evidence/web-compat-nightly.json"
python3 - "$tmp/evidence/web-compat-nightly.json" <<'PY'
import json
import sys

evidence = json.load(open(sys.argv[1], encoding="utf-8"))
provenance = evidence["provenance"]
assert provenance["host"]["os"]
assert provenance["host"]["release"]
assert provenance["host"]["arch"]
assert provenance["playwrightVersion"]
assert provenance["source"] == "package-lock.json + playwright-core/browsers.json"
assert {browser["name"] for browser in provenance["browsers"]} == {"firefox", "webkit"}
assert all(browser["revision"] for browser in provenance["browsers"])
assert provenance["executedTestCount"] == 4
PY

rm -rf "$tmp/evidence"
rm -f "$tmp/git-counter"
if PATH="$tmp/bin:$PATH" \
  WEB_COMPAT_CALLS="$calls" \
  WEB_COMPAT_PUBLICATION_HEAD_CHANGE=1 \
  WEB_COMPAT_GIT_COUNTER="$tmp/git-counter" \
  BEAMFALL_WEB_COMPAT_OUTPUT_DIR="$tmp/output" \
  RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp/evidence" \
  "$ROOT/script/web-compat-nightly.sh" >"$tmp/publication-head-change.stdout" \
  2>"$tmp/publication-head-change.stderr"; then
  echo "FAIL web-compat-nightly retained freshness after publication HEAD changed" >&2
  exit 1
fi
grep -Fq 'FAIL web-compat-nightly source changed at publication' \
  "$tmp/publication-head-change.stderr"
test ! -e "$tmp/evidence/web-compat-nightly.json"

rm -rf "$tmp/evidence"
set +e
PATH="$tmp/bin:$PATH" \
  WEB_COMPAT_CALLS="$calls" \
  BEAMFALL_WEB_COMPAT_SIGNAL_AFTER_REPLACE=1 \
  BEAMFALL_WEB_COMPAT_OUTPUT_DIR="$tmp/output-promotion-signal" \
  RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp/evidence" \
  "$ROOT/script/web-compat-nightly.sh" >"$tmp/promotion-signal.stdout" \
  2>"$tmp/promotion-signal.stderr"
promotion_signal_status=$?
set -e
if [[ $promotion_signal_status -ne 143 ]]; then
  echo "FAIL web-compat-nightly promotion-edge signal status expected=143 actual=$promotion_signal_status" >&2
  exit 1
fi
test ! -e "$tmp/evidence/web-compat-nightly.json"

rm -rf "$tmp/evidence"
publisher_barrier="$tmp/publisher-ownership"
PATH="$tmp/bin:$PATH" \
  WEB_COMPAT_CALLS="$calls" \
  BEAMFALL_WEB_COMPAT_PUBLISH_BARRIER="$publisher_barrier" \
  BEAMFALL_WEB_COMPAT_OUTPUT_DIR="$tmp/output-publisher-a" \
  RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp/evidence" \
  "$ROOT/script/web-compat-nightly.sh" >"$tmp/publisher-a.stdout" \
  2>"$tmp/publisher-a.stderr" &
publisher_a_pid=$!
for _ in $(seq 1 100); do
  [[ -s "$publisher_barrier.ready" ]] && break
  sleep 0.05
done
if [[ ! -s "$publisher_barrier.ready" ]]; then
  echo "FAIL web-compat-nightly first concurrent publisher did not start" >&2
  exit 1
fi
PATH="$tmp/bin:$PATH" \
  WEB_COMPAT_CALLS="$calls" \
  BEAMFALL_WEB_COMPAT_OUTPUT_DIR="$tmp/output-publisher-b" \
  RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp/evidence" \
  "$ROOT/script/web-compat-nightly.sh" >"$tmp/publisher-b.stdout" \
  2>"$tmp/publisher-b.stderr" &
publisher_b_pid=$!
sleep 0.1
kill -TERM "$publisher_a_pid"
set +e
wait "$publisher_a_pid"
publisher_a_status=$?
wait "$publisher_b_pid"
publisher_b_status=$?
set -e
if [[ $publisher_a_status -ne 143 || $publisher_b_status -ne 0 ]]; then
  echo "FAIL web-compat-nightly concurrent publication statuses a=$publisher_a_status b=$publisher_b_status" >&2
  exit 1
fi
if [[ ! -s "$tmp/evidence/web-compat-nightly.json" ]]; then
  echo "FAIL web-compat-nightly interrupted publisher removed its successor receipt" >&2
  exit 1
fi

node --input-type=module - "$ROOT/internal/web/app/e2e/lib/require-executed-tests.mjs" <<'NODE'
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const Reporter = (await import(pathToFileURL(process.argv[2]))).default;
const testCase = (project, id) => ({ id, parent: { project: () => ({ name: project }) } });
const executionDirectory = mkdtempSync(join(tmpdir(), "web-compat-execution-reporter-test."));
const executionPath = join(executionDirectory, "execution.json");
process.env.BEAMFALL_WEB_COMPAT_EXECUTION_PROVENANCE = executionPath;
const missing = new Reporter();
missing.onTestEnd(testCase("web-compat-firefox", "firefox-1"), { status: "passed" });
missing.onTestEnd(testCase("web-compat-firefox", "firefox-2"), { status: "passed" });
missing.onTestEnd(testCase("web-compat-webkit", "webkit-1"), { status: "skipped" });

let stderr = "";
const write = process.stderr.write;
process.stderr.write = (chunk) => {
  stderr += String(chunk);
  return true;
};
const missingResult = missing.onEnd();
process.stderr.write = write;

if (missingResult?.status !== "failed") throw new Error("zero-test browser did not fail");
if (
  stderr !==
  "FAIL web-compat-nightly required browser web-compat-webkit expected=2 executed=0\n"
) {
  throw new Error(`zero-test failure did not name only WebKit: ${JSON.stringify(stderr)}`);
}
if (existsSync(executionPath)) throw new Error("incomplete run published execution provenance");

const partial = new Reporter();
partial.onTestEnd(testCase("web-compat-firefox", "firefox-1"), { status: "passed" });
partial.onTestEnd(testCase("web-compat-webkit", "webkit-1"), { status: "passed" });
stderr = "";
process.stderr.write = (chunk) => {
  stderr += String(chunk);
  return true;
};
const partialResult = partial.onEnd();
process.stderr.write = write;
if (partialResult?.status !== "failed") throw new Error("filtered subset did not fail");
if (!stderr.includes("web-compat-firefox expected=2 executed=1")) {
  throw new Error(`filtered subset did not report Firefox: ${JSON.stringify(stderr)}`);
}
if (!stderr.includes("web-compat-webkit expected=2 executed=1")) {
  throw new Error(`filtered subset did not report WebKit: ${JSON.stringify(stderr)}`);
}
if (existsSync(executionPath)) throw new Error("partial run published execution provenance");

const complete = new Reporter();
complete.onTestEnd(testCase("web-compat-firefox", "firefox-1"), { status: "passed" });
complete.onTestEnd(testCase("web-compat-firefox", "firefox-2"), { status: "passed" });
complete.onTestEnd(testCase("web-compat-webkit", "webkit-1"), { status: "failed" });
complete.onTestEnd(testCase("web-compat-webkit", "webkit-2"), { status: "passed" });
if (complete.onEnd() !== undefined) throw new Error("executed required browsers did not pass");
const execution = JSON.parse(readFileSync(executionPath, "utf8"));
if (execution.schemaVersion !== 1 || execution.executedTestCount !== 4) {
  throw new Error(`actual execution count was not published: ${JSON.stringify(execution)}`);
}
delete process.env.BEAMFALL_WEB_COMPAT_EXECUTION_PROVENANCE;
rmSync(executionDirectory, { recursive: true });
NODE

rm -rf "$tmp/evidence"
if PATH="$tmp/bin:$PATH" \
  WEB_COMPAT_CALLS="$calls" \
  WEB_COMPAT_ORPHAN=1 \
  WEB_COMPAT_ORPHAN_PID_FILE="$tmp/orphan.pid" \
  BEAMFALL_WEB_COMPAT_OUTPUT_DIR="$tmp/output" \
  RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp/evidence" \
  "$ROOT/script/web-compat-nightly.sh" >"$tmp/orphan.stdout" 2>"$tmp/orphan.stderr"; then
  echo "FAIL residual browser descendant published green evidence" >&2
  exit 1
fi
orphan_pid="$(<"$tmp/orphan.pid")"
if kill -0 "$orphan_pid" 2>/dev/null; then
  echo "FAIL web-compat-nightly normal exit left descendant pid=$orphan_pid" >&2
  exit 1
fi
test ! -e "$tmp/evidence/web-compat-nightly.json"

rm -rf "$tmp/evidence"
if PATH="$tmp/bin:$PATH" \
  WEB_COMPAT_CALLS="$calls" \
  WEB_COMPAT_EXECUTION_MODE=zero \
  BEAMFALL_WEB_COMPAT_OUTPUT_DIR="$tmp/output" \
  RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp/evidence" \
  "$ROOT/script/web-compat-nightly.sh" >"$tmp/zero.stdout" 2>"$tmp/zero.stderr"; then
  echo "FAIL zero browser execution count published green evidence" >&2
  exit 1
fi
grep -Fq 'web-compat execution provenance executed zero tests' "$tmp/zero.stderr"
test ! -e "$tmp/evidence/web-compat-nightly.json"

rm -rf "$tmp/evidence"
if PATH="$tmp/bin:$PATH" \
  WEB_COMPAT_CALLS="$calls" \
  WEB_COMPAT_FAIL=1 \
  BEAMFALL_WEB_COMPAT_OUTPUT_DIR="$tmp/output" \
  RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp/evidence" \
  "$ROOT/script/web-compat-nightly.sh" >/dev/null 2>&1; then
  echo "FAIL browser failure published green evidence" >&2
  exit 1
fi
test ! -e "$tmp/evidence/web-compat-nightly.json"

export WEB_COMPAT_INTERRUPT=1
export WEB_COMPAT_LEADER_PID_FILE="$tmp/interrupt-leader.pid"
export WEB_COMPAT_DESCENDANT_PID_FILE="$tmp/interrupt-descendant.pid"
PATH="$tmp/bin:$PATH" \
  WEB_COMPAT_CALLS="$calls" \
  BEAMFALL_WEB_COMPAT_OUTPUT_DIR="$tmp/output" \
  RELEASE_NIGHTLY_EVIDENCE_DIR="$tmp/evidence" \
  "$ROOT/script/web-compat-nightly.sh" >"$tmp/interrupt.stdout" 2>"$tmp/interrupt.stderr" &
runner_pid=$!
for _ in $(seq 1 100); do
  [[ -s "$WEB_COMPAT_LEADER_PID_FILE" && -s "$WEB_COMPAT_DESCENDANT_PID_FILE" ]] && break
  sleep 0.05
done
if [[ ! -s "$WEB_COMPAT_LEADER_PID_FILE" || ! -s "$WEB_COMPAT_DESCENDANT_PID_FILE" ]]; then
  echo "FAIL web-compat-nightly interruption fixture did not start" >&2
  sed -n '1,80p' "$tmp/interrupt.stderr" >&2
  exit 1
fi
leader_pid="$(<"$WEB_COMPAT_LEADER_PID_FILE")"
descendant_pid="$(<"$WEB_COMPAT_DESCENDANT_PID_FILE")"
kill -TERM "$runner_pid"
set +e
wait "$runner_pid"
interrupt_status=$?
set -e
if [[ $interrupt_status -ne 143 ]]; then
  echo "FAIL web-compat-nightly interruption status expected=143 actual=$interrupt_status" >&2
  exit 1
fi
for pid in "$leader_pid" "$descendant_pid"; do
  if kill -0 "$pid" 2>/dev/null; then
    echo "FAIL web-compat-nightly interruption left descendant pid=$pid" >&2
    exit 1
  fi
done
test ! -e "$tmp/evidence/web-compat-nightly.json"

echo "PASS web-compat-nightly normal exit reaps residual descendants"
echo "PASS web-compat-nightly rolls back publication after a final HEAD change"
echo "PASS web-compat-nightly rolls back an atomic-promotion signal"
echo "PASS web-compat-nightly serializes concurrent receipt ownership"
echo "PASS web-compat-nightly interruption reaps descendants"
echo "web-compat-nightly tests: passed"
