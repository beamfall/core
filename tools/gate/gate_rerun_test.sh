#!/usr/bin/env bash
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [ "${GATE_RERUN_TEST_SUPERVISED:-0}" != 1 ]; then
  exec python3 "$ROOT/script/test-process-supervisor.py" \
    GATE_RERUN_TEST_SUPERVISED "$0" "$@"
fi
set -euo pipefail
export GATE_PROCESS_GROUP_OWNED=0

python3 - "$ROOT/script/test-process-supervisor.py" <<'PY'
import importlib.util
import sys
from unittest import mock

path = sys.argv[1]
spec = importlib.util.spec_from_file_location("test_process_supervisor", path)
module = importlib.util.module_from_spec(spec)
assert spec.loader is not None
spec.loader.exec_module(module)
with mock.patch.object(module.os, "killpg", side_effect=PermissionError("EPERM")):
    if not module.group_alive(424242):
        raise SystemExit("process supervisor treated EPERM as group absence")
with mock.patch.object(module, "all_process_ids", return_value=[424243]), \
     mock.patch.object(module.os, "getsid", return_value=999), \
     mock.patch.object(module, "process_matches_effective_uid", return_value=True), \
     mock.patch.object(module, "process_has_token", return_value=None), \
     mock.patch.object(module, "linux_process_identity", return_value=(424243, 10)):
    pids, unknown = module.owned_process_ids(424242, "token")
    if pids or not unknown or not module.owned_alive(424242, "token"):
        raise SystemExit("process supervisor hid an indeterminate token probe")
    pids, unknown = module.owned_process_ids(
        424242, "token", frozenset({(424243, 10)})
    )
    if pids or unknown:
        raise SystemExit("process supervisor did not exclude an unchanged pre-launch identity")
    pids, unknown = module.owned_process_ids(
        424242, "token", frozenset({(424243, 9)})
    )
    if pids or not unknown:
        raise SystemExit("process supervisor hid PID reuse behind a stale baseline identity")
with mock.patch.object(module, "all_process_ids", return_value=[424243]), \
     mock.patch.object(module.os, "getsid", return_value=424242), \
     mock.patch.object(module, "process_matches_effective_uid", return_value=True), \
     mock.patch.object(module, "process_has_token", side_effect=AssertionError("owned session must win")):
    pids, unknown = module.owned_process_ids(
        424242, "token", frozenset({(424243, 10)})
    )
    if pids != [424243] or unknown:
        raise SystemExit("process supervisor let baseline identity hide an owned session")
with mock.patch.object(module, "all_process_ids", return_value=[424243]), \
     mock.patch.object(module.os, "getsid", return_value=999), \
     mock.patch.object(module, "process_matches_effective_uid", return_value=True), \
     mock.patch.object(module, "process_has_token", return_value=True), \
     mock.patch.object(module, "linux_process_identity", return_value=(424243, 10)):
    pids, unknown = module.owned_process_ids(
        424242, "token", frozenset({(424243, 10)})
    )
    if pids != [424243] or unknown:
        raise SystemExit("process supervisor let baseline identity hide a matching token")
with mock.patch.object(module, "all_process_ids", return_value=[1]), \
     mock.patch.object(module.os, "getsid", return_value=1), \
     mock.patch.object(module, "process_matches_effective_uid", return_value=False), \
     mock.patch.object(module, "process_has_token", side_effect=AssertionError("must not inspect foreign uid")):
    pids, unknown = module.owned_process_ids(424242, "token")
    if pids or unknown:
        raise SystemExit("process supervisor treated an inaccessible foreign-UID process as owned/unknown")
proc_dir = mock.Mock()
proc_dir.is_dir.return_value = True
with mock.patch.object(module, "all_process_ids", side_effect=PermissionError("EPERM")), \
     mock.patch.object(module, "Path", return_value=proc_dir):
    # Force the /proc-present code path so the indeterminate-inventory guard is
    # exercised on every host, not only Linux (Darwin has no /proc and would
    # otherwise short-circuit to the trusted-empty baseline).
    pids, unknown = module.owned_process_ids(424242, "token")
    if pids or not unknown:
        raise SystemExit("process supervisor hid an indeterminate process inventory")
    if module.preexisting_indeterminate_identities("token") is not None:
        raise SystemExit("process supervisor launched despite indeterminate baseline inventory")
valid_tail = ["S"] + [str(value) for value in range(1, 19)] + ["987654"]
valid_stat = "42 (name with ) embedded parens) " + " ".join(valid_tail)
if module.parse_linux_process_identity(42, valid_stat) != (42, 987654):
    raise SystemExit("process supervisor misparsed proc stat starttime")
for malformed in ("", "42 no-delimiter", "42 (name) S 1 2", "42 (name) " + " ".join(valid_tail[:-1] + ["bad"])):
    if module.parse_linux_process_identity(42, malformed) is not None:
        raise SystemExit("process supervisor accepted malformed proc stat identity")
print("ok   process supervisor keeps EPERM groups live/indeterminate")
PY

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
cp "$ROOT/tools/gate/gate.sh" "$TMP/gate.sh"

cert_only_probe="$TMP/cert-only-probe"
mkdir -p "$cert_only_probe/tools/gate" "$cert_only_probe/script" "$cert_only_probe/bin"
cp "$ROOT/tools/gate/gate.sh" "$cert_only_probe/tools/gate/gate.sh"
for focused in cert-demo-server_test.sh cert-demo-server-smoke_test.sh; do
  cat > "$cert_only_probe/script/$focused" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$(basename "$0")" >> "$CERT_ONLY_EXECUTION_LOG"
SH
  chmod +x "$cert_only_probe/script/$focused"
done
cat > "$cert_only_probe/bin/make" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
: > "$CERT_ONLY_OUTER_MARKER"
exit 97
SH
chmod +x "$cert_only_probe/bin/make"
if ! PATH="$cert_only_probe/bin:$PATH" \
  BEAMFALL_GATE_CAPACITY_HELD=1 GATE_PROCESS_SUPERVISED=1 \
  BEAMFALL_GATE_FIXTURE_GUARDED=1 GATE_CERT_DEMO_ONLY=1 \
  CERT_ONLY_EXECUTION_LOG="$TMP/cert-only-executed" \
  CERT_ONLY_OUTER_MARKER="$TMP/cert-only-outer" \
  AGENT_EVIDENCE_DIR="$TMP/cert-only-evidence" \
  bash "$cert_only_probe/tools/gate/gate.sh" > "$TMP/cert-only.out" 2> "$TMP/cert-only.err"; then
  cat "$TMP/cert-only.out" "$TMP/cert-only.err" >&2
  echo "cert-only gate did not complete its focused checks" >&2
  exit 1
fi
test ! -e "$TMP/cert-only-outer" || {
  echo "cert-only gate executed an outer check" >&2
  exit 1
}
printf '%s\n' cert-demo-server_test.sh cert-demo-server-smoke_test.sh > "$TMP/cert-only-expected"
cmp "$TMP/cert-only-expected" "$TMP/cert-only-executed"

python3 - "$ROOT/tools/gate/gate.sh" "$TMP/sandbox-env-test.sh" <<'PY'
from pathlib import Path
import sys

gate = Path(sys.argv[1]).read_text()
start = gate.index("configure_sandbox_go() {")
end = gate.index("\nconfigure_sandbox_go\n", start)
Path(sys.argv[2]).write_text(
    "#!/usr/bin/env bash\nset -euo pipefail\n"
    + gate[start:end]
    + "\nconfigure_sandbox_go\n"
    + "test \"$GOCACHE\" = \"$1\"\n"
    + "test \"$GOPROXY\" = \"$2\"\n"
)
PY
chmod +x "$TMP/sandbox-env-test.sh"
env -u GOCACHE -u GOPROXY -u GATE_SANDBOX_GOPROXY \
  CODEX_SANDBOX=seatbelt CODEX_SANDBOX_NETWORK_DISABLED=1 \
  "$TMP/sandbox-env-test.sh" "${TMPDIR:-/tmp}/beamfall-go-build" off
env -u GATE_SANDBOX_GOPROXY GOCACHE="$TMP/custom-cache" GOPROXY=file:///tmp/proxy \
  CODEX_SANDBOX=seatbelt CODEX_SANDBOX_NETWORK_DISABLED=1 \
  "$TMP/sandbox-env-test.sh" "$TMP/custom-cache" file:///tmp/proxy
GATE_SANDBOX_GOPROXY=file:///tmp/gate-proxy \
  CODEX_SANDBOX=seatbelt CODEX_SANDBOX_NETWORK_DISABLED=1 \
  "$TMP/sandbox-env-test.sh" "${GOCACHE:-${TMPDIR:-/tmp}/beamfall-go-build}" file:///tmp/gate-proxy

python3 - "$ROOT/tools/gate/gate.sh" "$TMP/metric-test.sh" <<'PY'
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text()
start = text.index("gate_signal_group() {")
end = text.index("\nchanged_paths() {")
Path(sys.argv[2]).write_text(
    "#!/usr/bin/env bash\nset -euo pipefail\nexport GATE_PROCESS_GROUP_OWNED=1\n"
    + text[start:end]
    + "\nrun_step 'fast metric' true\n"
)
PY
chmod +x "$TMP/metric-test.sh"
for generation in 41 42; do
  GATE_RUN_ID="$generation" AGENT_EVIDENCE_DIR="$TMP/metric-evidence" \
    "$TMP/metric-test.sh" >"$TMP/metric-$generation.out" 2>"$TMP/metric-$generation.err"
done
python3 - "$TMP/metric-evidence/gate-runs.jsonl" <<'PY'
import json
import sys

records = [json.loads(line) for line in open(sys.argv[1], encoding="utf-8") if line.strip()]
if len(records) != 4:
    raise SystemExit(f"metric fixture must emit two records per generation: {records}")
for run_id in ("41", "42"):
    generation = [record for record in records if record["run_id"] == run_id]
    if [record["status"] for record in generation] != ["running", "pass"]:
        raise SystemExit(f"metric generation {run_id} is incomplete: {generation}")
    terminal = generation[-1]
    if terminal["elapsed_s"] != 0 or not 0 < terminal["elapsed_ns"] < 1_000_000_000:
        raise SystemExit(f"sub-second metric lost monotonic precision: {terminal}")
PY

python3 - "$TMP/gate.sh" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
text = path.read_text()
start = text.index("gate_signal_group() {")
end = text.index("\nchanged_paths() {")
path.write_text("#!/usr/bin/env bash\nset -euo pipefail\n" + text[start:end] + "\nrun_step 'quoted fail' bash -c 'echo nope >&2; exit 7'\n")
PY

chmod +x "$TMP/gate.sh"
if AGENT_EVIDENCE_DIR="$TMP/evidence" "$TMP/gate.sh" >"$TMP/out" 2>"$TMP/err"; then
  echo "FAIL gate rerun hint exits nonzero" >&2
  exit 1
fi

grep -q '^FAIL quoted fail elapsed_s=' "$TMP/err"
grep -Fxq 'rerun: bash -c echo\ nope\ \>\&2\;\ exit\ 7' "$TMP/err"
grep -q '^nope$' "$TMP/err"
failure_log="$(sed -n 's/^full log: //p' "$TMP/err" | tail -n 1)"
[ -n "$failure_log" ] && [ -s "$failure_log" ] || {
  echo "failed gate step did not retain its complete log" >&2
  exit 1
}
grep -q '^nope$' "$failure_log"
grep -q '"step":"quoted fail"' "$TMP/evidence/gate-runs.jsonl"
grep -q '"status":"fail"' "$TMP/evidence/gate-runs.jsonl"

python3 - "$ROOT/tools/gate/gate.sh" "$TMP/infrastructure-abort-test.sh" <<'PY'
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text()
start = text.index("gate_signal_group() {")
end = text.index("\nchanged_paths() {")
Path(sys.argv[2]).write_text(
    "#!/usr/bin/env bash\nset -euo pipefail\n"
    + text[start:end]
    + r'''
for signal in INT KILL TERM; do
  abort_receipt="$1/$signal.receipt"
  status=0
  GATE_INFRASTRUCTURE_ABORT_FILE="$abort_receipt" \
    run_step "silent $signal" bash -c "kill -$signal \$\$" || status=$?
  [ "$status" -eq 75 ] || {
    echo "silent $signal returned $status, want infrastructure-abort 75" >&2
    exit 1
  }
  case "$signal" in
    INT) expected_status=130 ;;
    KILL) expected_status=137 ;;
    TERM) expected_status=143 ;;
  esac
  grep -Fq "status=$expected_status" "$abort_receipt"
done
'''
)
PY
chmod +x "$TMP/infrastructure-abort-test.sh"
AGENT_EVIDENCE_DIR="$TMP/abort-evidence" \
  "$TMP/infrastructure-abort-test.sh" "$TMP" >"$TMP/abort-out" 2>"$TMP/abort-err"
grep -q '^gate infrastructure aborted: step=silent INT status=130$' "$TMP/abort-err"
grep -q '^gate infrastructure aborted: step=silent KILL status=137$' "$TMP/abort-err"
grep -q '^gate infrastructure aborted: step=silent TERM status=143$' "$TMP/abort-err"
if grep -q '^FAIL silent ' "$TMP/abort-err"; then
  echo "signal abort was reported as a red gate step" >&2
  exit 1
fi
python3 - "$TMP/abort-evidence/gate-runs.jsonl" <<'PY'
import json
import sys

records = [json.loads(line) for line in open(sys.argv[1], encoding="utf-8") if line.strip()]
terminal = [record for record in records if record["status"] == "abort"]
if [record["exit_code"] for record in terminal] != [130, 137, 143]:
    raise SystemExit(f"signal abort metrics lost raw statuses: {terminal}")
PY

python3 - "$ROOT/tools/gate/gate.sh" "$TMP/full-rss-test.sh" <<'PY'
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text()
start = text.index("gate_signal_group() {")
end = text.index("\nchanged_paths() {")
Path(sys.argv[2]).write_text(
    "#!/usr/bin/env bash\nset -euo pipefail\n"
    + text[start:end]
    + r'''
gate_full_rss_start
python3 -c 'import time; payload = bytearray(16 * 1024 * 1024); time.sleep(0.5)'
gate_full_rss_stop 0
'''
)
PY
chmod +x "$TMP/full-rss-test.sh"
full_rss_rc=0
AGENT_EVIDENCE_DIR="$TMP/full-rss-evidence" \
  env -u GATE_PREFLIGHT \
  "$TMP/full-rss-test.sh" >"$TMP/full-rss-out" 2>"$TMP/full-rss-err" || full_rss_rc=$?
[ "$full_rss_rc" -eq 0 ] || {
  cat "$TMP/full-rss-out" "$TMP/full-rss-err" >&2
  echo "full gate RSS harness exited $full_rss_rc" >&2
  exit 1
}
python3 - "$TMP/full-rss-evidence/gate-peak-rss.json" <<'PY'
import json
import sys

def is_full_gate_record(record):
    return record["type"] == "gate_peak_rss" \
        and record["kind"] == "make-gate" \
        and record["status"] == "pass" \
        and record["peak_rss_kb"] > 16_384

full = json.load(open(sys.argv[1], encoding="utf-8"))
if not is_full_gate_record(full):
    raise SystemExit(f"full-gate peak RSS was not recorded: {full}")
kind_mismatch = {**full, "kind": "make-gate-preflight"}
if is_full_gate_record(kind_mismatch):
    raise SystemExit(f"preflight peak RSS was accepted as full-gate evidence: {kind_mismatch}")
if full["full_gate_concurrency_cap"] != 1 \
        or full["projected_peak_rss_kb"] != full["peak_rss_kb"]:
    raise SystemExit(f"full-gate concurrency budget is not cap=1: {full}")
if full["host_physical_memory_kb"] <= 0 or full["host_available_kb_at_start"] <= 0:
    raise SystemExit(f"host memory budget was not recorded: {full}")
if full["headroom_kb"] != (
        full["host_available_kb_at_start"] - full["projected_peak_rss_kb"]
):
    raise SystemExit(f"full-gate memory headroom is inconsistent: {full}")
expected_swap_disabled = (
    None if full["host_swap_total_kb"] < 0 else full["host_swap_total_kb"] == 0
)
if full["swap_disabled"] != expected_swap_disabled \
        or full["within_available_memory"] != (full["headroom_kb"] >= 0):
    raise SystemExit(f"full-gate memory budget flags are inconsistent: {full}")
PY

python3 - "$ROOT/tools/gate/gate.sh" "$TMP/failure-retention-test.sh" <<'PY'
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text()
start = text.index("gate_signal_group() {")
end = text.index("\nchanged_paths() {")
Path(sys.argv[2]).write_text(
    "#!/usr/bin/env bash\nset -euo pipefail\n"
    + text[start:end]
    + r'''
run_step 'silent fail' false >/dev/null 2>&1 || true
run_parallel_step 'duplicate fail' bash -c 'echo first >&2; exit 8' >/dev/null 2>&1 &
p1=$!
run_parallel_step 'duplicate fail' bash -c 'echo second >&2; exit 9' >/dev/null 2>&1 &
p2=$!
wait "$p1" || true
wait "$p2" || true
'''
)
PY
chmod +x "$TMP/failure-retention-test.sh"
AGENT_EVIDENCE_DIR="$TMP/retention-evidence" "$TMP/failure-retention-test.sh"
retained=("$TMP/retention-evidence/gate-failures"/*)
[ "${#retained[@]}" -eq 3 ] || {
  echo "failure logs collided or went missing: ${retained[*]}" >&2
  exit 1
}
for failure_log in "${retained[@]}"; do
  [ -s "$failure_log" ] && grep -q '^\[gate failure metadata\]$' "$failure_log" || {
    echo "failure log is empty or lacks metadata: $failure_log" >&2
    exit 1
  }
done
grep -Fq 'run_step "web app build" run_web_app verify:build' "$ROOT/tools/gate/gate.sh"
grep -Fq 'tools/gate/gate-entry.sh' "$ROOT/Makefile" || {
  echo "gate target must use the guarded gate entry" >&2
  exit 1
}
grep -Fq 'golangci-lint run --allow-serial-runners ./...' "$ROOT/Makefile" || {
  echo "lint target must serialize concurrent repository gates" >&2
  exit 1
}

python3 - "$ROOT/Makefile" "$ROOT/tools/gate/gate-entry.sh" "$ROOT/tools/gate/gate.sh" "$ROOT/tools/gate/targets.mk" <<'PY'
from pathlib import Path
import sys

makefile = Path(sys.argv[1]).read_text()
entry = Path(sys.argv[2]).read_text()
gate = Path(sys.argv[3]).read_text()
targets = Path(sys.argv[4]).read_text()
# Split on "\ngate:" rather than "\ngate:\n" so the prerequisite list is part of
# the inspected block: a canonical check smuggled back as a prerequisite must red
# this check just as one smuggled back into the recipe body does.
recipe = makefile.split("\ngate:", 1)[1].split("\n\n", 1)[0]
entrypoint = "tools/gate/gate-entry.sh"
if recipe.count(entrypoint) != 1:
    raise SystemExit(f"make gate must enter the guarded entry exactly once: {recipe}")
if any(line.startswith("gate:") for line in targets.splitlines()):
    raise SystemExit("included make fragments must not add prerequisites before guarded gate entry")
guard = 'exec python3 script/gate-fixture-guard.py -- "$0" "$@"'
controls = "make seeded-red-canary-controls"
if entry.count(guard) != 1 or entry.count(controls) != 1 or entry.index(guard) > entry.index(controls):
    raise SystemExit("seeded-red canary controls must run exactly once inside the outer fixture guard")
coordinator = "python3 tools/gate/gate_latest.py -- tools/gate/gate.sh"
if entry.count(coordinator) != 1:
    raise SystemExit(f"gate entry must enter the coordinator exactly once: {entry}")
for outer in (
    "web-ui-e2e-smoke",
    "release-workflow-pins-check",
    "vuln-check",
    "check-contract-registry.py",
):
    if outer in recipe:
        raise SystemExit(f"canonical check remains outside the coordinator: {outer}")

ordered = [
    'run_scoped_step "web UI E2E smoke (pre-gate)" webe2e make web-ui-e2e-smoke',
    'run_step "release workflow pins check" make release-workflow-pins-check',
    'full_gate_only_step "vulnerability check" make vuln-check',
    'run_step "contract registry check" python3 script/check-contract-registry.py',
]
for invocation in ordered:
    if gate.count(invocation) != 1:
        raise SystemExit(f"supervised check must be registered exactly once: {invocation}")
if [gate.index(invocation) for invocation in ordered] != sorted(
    gate.index(invocation) for invocation in ordered
):
    raise SystemExit("supervised pre-body checks changed order")
post = 'run_scoped_step "web UI E2E smoke (second pass)" webe2e make web-ui-e2e-smoke'
web_tests = 'run_scoped_step "web app typecheck + tests" web run_web_app test'
if gate.count(post) != 1 or gate.index(post) < gate.index(web_tests):
    raise SystemExit("post-gate web smoke is missing or displaced before the web app tests")
PY

python3 - "$ROOT/tools/gate/gate.sh" <<'PY'
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text()
collapsed = 'run_step "go test + line-coverage ratchet" go run ./cmd/covratchet -floor testing/coverage-floor.json'
if text.count(collapsed) != 1:
    raise SystemExit("collapsed package-test/coverage step must appear exactly once")
if 'run_step "go test" go test ./...' in text:
    raise SystemExit("standalone package-test gate step must stay collapsed into covratchet")
playback_perf = 'run_step "playback transcode latency budgets" env BEAMFALL_PERF=1 BEAMFALL_PERF_LOAD_WORKERS=1 GOMAXPROCS=2 go test ./internal/playback'
if text.count(playback_perf) != 1:
    raise SystemExit("blocking playback performance lane must appear exactly once")
if text.index(playback_perf) < text.index(collapsed):
    raise SystemExit("playback performance lane must run after competing mandatory lanes drain")
for required in ("TranscodeLatencyBudgetsOnReferenceClip", "InjectedPlaybackLatencyRegressionReds"):
    if required not in text[text.index(playback_perf):]:
        raise SystemExit(f"playback performance lane must run {required}")
PY

python3 - "$ROOT/tools/gate/gate.sh" "$TMP/scope-test.sh" <<'PY'
from pathlib import Path
import sys

gate = Path(sys.argv[1]).read_text()
release_start = gate.index("gate_release_mode() {")
release_end = gate.index("\nvalidate_gate_mode() {", release_start)
start = gate.index("scope_matches() {")
end = gate.index("\nrun_scoped_step() {")
Path(sys.argv[2]).write_text(
    "#!/usr/bin/env bash\nset -euo pipefail\n"
    + gate[release_start:release_end]
    + "\n"
    "changed_paths() { printf '%s\\n' \"$TEST_CHANGED_PATH\"; }\n"
    + gate[start:end]
    + r'''
TEST_CHANGED_PATH=tools/gate/gate.sh scope_matches script
TEST_CHANGED_PATH=AGENTS.md scope_matches script
if TEST_CHANGED_PATH=README.md scope_matches script; then
  echo "unrelated root documentation matched script scope" >&2
  exit 1
fi
'''
)
PY
chmod +x "$TMP/scope-test.sh"
GATE_MODE=normal GITHUB_WORKFLOW= "$TMP/scope-test.sh"

python3 - "$ROOT/tools/gate/gate.sh" "$TMP/release-skip-test.sh" <<'PY'
from pathlib import Path
import sys

gate = Path(sys.argv[1]).read_text()
start = gate.index("gate_release_mode() {")
end = gate.index("\nrun_step() {", start)
Path(sys.argv[2]).write_text(
    "#!/usr/bin/env bash\nset -euo pipefail\n" + gate[start:end] + r'''
if GATE_MODE=typo validate_gate_mode; then
  echo "invalid gate mode was accepted" >&2
  exit 1
fi
if GITHUB_WORKFLOW=release-gate GATE_MODE=normal validate_gate_mode; then
  echo "release workflow accepted normal mode" >&2
  exit 1
fi
GATE_MODE=release gate_metric "$1" skip 0 0 "$2"
'''
)
PY
chmod +x "$TMP/release-skip-test.sh"
python3 - "$ROOT/tools/gate/gate.sh" "$TMP/mandatory-release-steps.tsv" <<'PY'
from pathlib import Path
import re
import sys

gate = Path(sys.argv[1]).read_text()

def mandatory_steps(text):
    steps = re.findall(
        r'^\s*run_scoped_step "([^"]+)" (script|web|webe2e|image)\b',
        text,
        re.MULTILINE,
    )
    cert = re.search(r'^run_cert_demo_tests\(\) \{\n(.*?)^\}', text, re.MULTILINE | re.DOTALL)
    if cert is None or not re.search(
        r'^if scope_matches cert; then\n\s+run_cert_demo_tests\nelse$', text, re.MULTILINE
    ):
        raise SystemExit("certification demo tests are not called from the cert-scoped main path")
    steps += [
        (step, "cert")
        for step in re.findall(r'^\s*run_step "([^"]+)"(?:\s|$)', cert.group(1), re.MULTILINE)
    ]
    return steps

steps = mandatory_steps(gate)
expected = {
    ("support script tests", "script"),
    ("certification demo server test", "cert"),
    ("certification demo server smoke test", "cert"),
    ("design token drift gate", "web"),
    ("web app typecheck + tests", "web"),
    ("web UI E2E smoke (pre-gate)", "webe2e"),
    ("web UI E2E smoke (second pass)", "webe2e"),
    ("runtime image buildability", "image"),
}
if set(steps) != expected or len(steps) != len(expected):
    raise SystemExit(
        f"mandatory scoped release lanes changed: derived={steps}, expected={sorted(expected)}"
    )
mutated = re.sub(
    r'(^if scope_matches cert; then\n)\s+run_cert_demo_tests\n', r'\1', gate, count=1, flags=re.MULTILINE
)
if mutated == gate:
    raise SystemExit("live certification call mutation did not apply")
try:
    mandatory_steps(mutated)
except SystemExit:
    pass
else:
    raise SystemExit("mandatory inventory survived deletion of the live certification call")
Path(sys.argv[2]).write_text("".join(f"{step}\tscope:{scope}\n" for step, scope in steps))
PY
while IFS=$'\t' read -r step scope; do
  if AGENT_EVIDENCE_DIR="$TMP/release-skip-evidence" "$TMP/release-skip-test.sh" "$step" "$scope" \
    >"$TMP/release-skip-out" 2>>"$TMP/release-skip-err"; then
    echo "mandatory release skip did not fail: $step" >&2
    exit 1
  fi
  grep -Fq "ERROR: mandatory release step skipped: $step" "$TMP/release-skip-err"
done < "$TMP/mandatory-release-steps.tsv"

: > "$TMP/not-an-evidence-directory"
if AGENT_EVIDENCE_DIR="$TMP/not-an-evidence-directory" \
  "$TMP/release-skip-test.sh" "unwritable evidence" "scope:script" \
  >"$TMP/release-skip-out" 2>"$TMP/unwritable-evidence-err"; then
  echo "mandatory release skip passed with unwritable evidence" >&2
  exit 1
fi
grep -Fq "ERROR: mandatory release step skipped without writable evidence: unwritable evidence" \
  "$TMP/unwritable-evidence-err"

AGENT_EVIDENCE_DIR="$TMP/release-skip-evidence" "$TMP/release-skip-test.sh" "race-short" "make gate-race" \
  >"$TMP/release-skip-out" 2>>"$TMP/release-skip-err"
python3 - "$TMP/release-skip-evidence/gate-runs.jsonl" "$TMP/mandatory-release-steps.tsv" <<'PY'
import json
from pathlib import Path
import sys

records = [json.loads(line) for line in Path(sys.argv[1]).read_text().splitlines()]
failed = {record["step"] for record in records if record["status"] == "fail"}
expected = {line.split("\t", 1)[0] for line in Path(sys.argv[2]).read_text().splitlines()}
if failed != expected:
    raise SystemExit(f"mandatory release skip evidence mismatch: failed={failed}, expected={expected}")
PY

python3 - "$ROOT/tools/gate/gate.sh" "$TMP/parallel-test.sh" <<'PY'
from pathlib import Path
import sys

gate = Path(sys.argv[1]).read_text()
start = gate.index("gate_signal_group() {")
end = gate.index("\nchanged_paths() {")
Path(sys.argv[2]).write_text(
    "#!/usr/bin/env bash\nset -euo pipefail\n"
    + gate[start:end]
    + r'''
marker="$1"
run_parallel_step 'parallel fail' bash -c 'echo bad >&2; exit 9' &
p1=$!
run_parallel_step 'parallel marker' bash -c 'sleep 0.1; echo done > "$1"' _ "$marker" &
p2=$!
if wait_parallel "$p1" "$p2"; then
  echo "FAIL wait_parallel hid failure" >&2
  exit 1
fi
test -f "$marker"
'''
)
PY
chmod +x "$TMP/parallel-test.sh"
AGENT_EVIDENCE_DIR="$TMP/parallel-evidence" "$TMP/parallel-test.sh" "$TMP/parallel-marker" >"$TMP/parallel-out" 2>"$TMP/parallel-err"
grep -q '^FAIL parallel fail elapsed_s=' "$TMP/parallel-err"
grep -q '^bad$' "$TMP/parallel-err"
python3 - "$TMP/parallel-evidence/gate-runs.jsonl" <<'PY'
import json
import sys

records = [json.loads(line) for line in open(sys.argv[1], encoding="utf-8") if line.strip()]
if len(records) != 4:
    raise SystemExit(f"parallel metrics must contain four complete JSON records: {records}")
PY

python3 - "$ROOT/tools/gate/gate.sh" <<'PY'
from pathlib import Path
import sys

text = Path(sys.argv[1]).read_text()
if "run_roadmap_orchestration_tests" in text or "run_slow_orchestration_step" in text:
    raise SystemExit("the retired roadmap dispatcher suites must not return to the gate (ADR-0227)")
support_start = text.index("run_support_orchestration_tests() {")
support = text[support_start:text.index("\n}\n", support_start)]
if "unset GATE_PREFLIGHT" not in support:
    raise SystemExit("support selftests inherited the parent gate's focused mode")
scope_selftest_start = text.index("run_gate_scope_selftest() {")
scope_selftest = text[scope_selftest_start:text.index("\n}\n", scope_selftest_start)]
if "unset GATE_PREFLIGHT" not in scope_selftest:
    raise SystemExit("gate scope selftest inherited the parent gate's focused mode")
PY


echo "gate rerun hint test: passed"
