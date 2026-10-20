#!/usr/bin/env bash
# Scope-detection selftest: narrow only on a proven immutable revision range;
# invalid ranges fail open so scoped lanes cannot silently skip.
set -euo pipefail

# Keep ordinary diff-scoping assertions deterministic inside release CI; the
# release-mode cases below opt in explicitly.
export GATE_MODE=normal GITHUB_WORKFLOW=

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
fail() { echo "gate_scope_test FAIL: $*" >&2; exit 1; }

# Extract the functions under test from gate.sh verbatim.
FUNCS="$TMP/funcs.sh"
awk '/^gate_release_mode\(\)/,/^\}/' "$ROOT/tools/gate/gate.sh" > "$FUNCS"
awk '/^changed_paths\(\)/,/^\}/' "$ROOT/tools/gate/gate.sh" >> "$FUNCS"
awk '/^configure_race_short_scope\(\)/,/^\}/' "$ROOT/tools/gate/gate.sh" >> "$FUNCS"
awk '/^scope_matches\(\)/,/^\}/' "$ROOT/tools/gate/gate.sh" >> "$FUNCS"
awk '/^run_scoped_step\(\)/,/^\}/' "$ROOT/tools/gate/gate.sh" >> "$FUNCS"
grep -q 'gate_release_mode()' "$FUNCS" && grep -q 'changed_paths()' "$FUNCS" \
  && grep -q 'configure_race_short_scope()' "$FUNCS" && grep -q 'scope_matches()' "$FUNCS" \
  && grep -q 'run_scoped_step()' "$FUNCS" \
  || { echo "gate_scope_test: could not extract functions from gate.sh" >&2; exit 1; }
grep -Fq 'run_scoped_step "support script tests" script ' "$ROOT/tools/gate/gate.sh" \
  && grep -Fq 'python3 script/check_contract_registry_test.py' "$ROOT/tools/gate/gate.sh" \
  || { echo "gate_scope_test: required checker missing from script suite" >&2; exit 1; }
grep -Fq 'run_scoped_step "ADR-0122 cross-repo wire conformance" contract make crossrepo-conformance' "$ROOT/tools/gate/gate.sh" \
  || { echo "gate_scope_test: cross-repo contract lane missing from required gate" >&2; exit 1; }
grep -Fq 'run_scoped_step "web UI E2E smoke (pre-gate)" webe2e make web-ui-e2e-smoke' "$ROOT/tools/gate/gate.sh" \
  && grep -Fq 'run_scoped_step "web UI E2E smoke (second pass)" webe2e make web-ui-e2e-smoke' "$ROOT/tools/gate/gate.sh" \
  || { echo "gate_scope_test: both web UI E2E smoke steps must use webe2e scope" >&2; exit 1; }

# --- GATE-PREFLIGHT-1: the cheap fail-fast lane ------------------------------
#
# `make gate-preflight` exists to return a verdict in minutes instead of paying
# the gate's compile/coverage/race tail first. That is only worth trusting if
# three things hold, and each is asserted below rather than assumed:
#   1. membership is complete — every full-gate step is included by default,
#      with only the explicitly expensive/non-lint inventory excluded;
#   2. each member runs the SAME command the full gate runs for that step, so a
#      green preflight is evidence about the gate and not about a divergent
#      copy of it;
#   3. a red member aborts the lane non-zero — a preflight that cannot fail
#      would be worse than none.

PREFLIGHT_MANIFEST="$TMP/preflight-manifest"
python3 - "$ROOT/tools/gate/gate.sh" "$PREFLIGHT_MANIFEST" <<'PY'
from pathlib import Path
import sys

gate_path, manifest_path = sys.argv[1:]
text = Path(gate_path).read_text()

opener = "run_gate_preflight_steps() {\n"
if opener not in text:
    raise SystemExit("gate_scope_test: run_gate_preflight_steps is missing from gate.sh")
body_start = text.index(opener) + len(opener)
body_end = text.index("\n}\n", body_start)
body = text[body_start:body_end]
rest_of_gate = text[:text.index(opener)] + text[body_end:]

members = []
for line in body.splitlines():
    line = line.strip()
    if not line or line.startswith("#"):
        continue
    for prefix, scoped in (("preflight_scoped_step ", True), ("preflight_step ", False)):
        if line.startswith(prefix):
            tail = line[len(prefix):]
            if not tail.startswith('"'):
                raise SystemExit(f"gate_scope_test: preflight member has no quoted name: {line}")
            closing = tail.index('"', 1)
            name = tail[1:closing]
            command = tail[closing + 1:].strip()
            if scoped:
                # run_scoped_step takes the scope between the name and the command.
                command = command.split(" ", 1)[1]
            members.append((name, command))
            break
    else:
        raise SystemExit(f"gate_scope_test: unrecognised preflight lane statement: {line}")

# 1. Completeness-by-default. Derive the expected lane from the full gate's own
# inventory, then subtract only steps known to be compile-, test-, race-,
# coverage-, image-, or web-build lanes. A newly registered cheap step is
# therefore required in preflight unless this exclusion inventory is
# deliberately updated in the same change.
import re

patterns = (
    re.compile(r'^run_step "([^"$]+)" (.+)$'),
    re.compile(r'^run_scoped_step "([^"$]+)" \S+ (.+)$'),
    re.compile(r'^start_parallel_step \S+ "([^"$]+)" (.+)$'),
)
inventory = []
for line in rest_of_gate.splitlines():
    line = line.strip()
    if not line or line.startswith("#"):
        continue
    for pattern in patterns:
        match = pattern.match(line)
        if match:
            inventory.append(match.groups())
            break

inventory_names = [name for name, _ in inventory]
if len(inventory_names) != len(set(inventory_names)):
    raise SystemExit("gate_scope_test: full gate inventory has duplicate step names")

excluded = {
    "runtime image buildability",
    "web app build",
    "go build",
    "fuzz",
    "capability manifest",
    "go vet",
    "feature-coverage ratchet",
    "go test -tags beamfall_appstore conformance capabilities graph",
    "ADR-0122 cross-repo wire conformance",
    "go test + line-coverage ratchet",
    "playback transcode latency budgets",
    "pinned FFmpeg tone-map conformance",
    "race-prove",
    "race-short",
    "certification demo server test",
    "certification demo server smoke test",
    "web UI E2E smoke (pre-gate)",
    "web UI E2E smoke (second pass)",
    "web app typecheck + tests",
}
stale_exclusions = excluded - set(inventory_names)
if stale_exclusions:
    raise SystemExit(
        f"gate_scope_test: preflight exclusion is not in the full gate: "
        f"{sorted(stale_exclusions)!r}"
    )

expected = [name for name, _ in inventory if name not in excluded]
names = [name for name, _ in members]
if names != expected:
    missing = [name for name in expected if name not in names]
    extra = [name for name in names if name not in expected]
    raise SystemExit(
        f"preflight lane does not match qualifying full-gate inventory: "
        f"missing={missing!r}, extra={extra!r}"
    )

# 2. Every member must be registered in the full gate under the same step name
# AND end in the same command, so the two can never drift apart silently.
gate_lines = [
    line.strip()
    for line in rest_of_gate.splitlines()
    if line.strip() and not line.lstrip().startswith("#")
]
for name, command in members:
    quoted = f'"{name}"'
    matches = [
        line for line in gate_lines if quoted in line and line.endswith(command)
    ]
    if len(matches) != 1:
        raise SystemExit(
            f"preflight member is not registered exactly once in the full gate "
            f"with the same command: {name!r} -> {command!r} ({len(matches)} matches)"
        )

Path(manifest_path).write_text("".join(f"{name}\n" for name in names))
PY

# 3. Planted-red proof. The lane is extracted verbatim and driven with recording
# stubs, so the assertion is about this gate's own wiring rather than a
# paraphrase of it: green when every member passes, and non-zero at the FIRST
# red member (with no later member reached) when any one of them fails.
PREFLIGHT_HARNESS="$TMP/preflight-harness.sh"
{
  echo '#!/usr/bin/env bash'
  echo 'set -euo pipefail'
  grep -E '^preflight(_scoped)?_step\(\) \{' "$ROOT/tools/gate/gate.sh"
  awk '/^run_gate_preflight_steps\(\) \{/,/^\}/' "$ROOT/tools/gate/gate.sh"
  cat <<'STUB'
run_step() {
  printf '%s\n' "$1" >>"$PREFLIGHT_LOG"
  [ "$1" != "${PREFLIGHT_FAIL:-}" ] || return 7
}
run_scoped_step() {
  printf '%s\n' "$1" >>"$PREFLIGHT_LOG"
  [ "$1" != "${PREFLIGHT_FAIL:-}" ] || return 7
}
run_gate_preflight_steps
STUB
} >"$PREFLIGHT_HARNESS"
chmod +x "$PREFLIGHT_HARNESS"
grep -q 'run_gate_preflight_steps() {' "$PREFLIGHT_HARNESS" \
  && grep -q 'preflight_step() {' "$PREFLIGHT_HARNESS" \
  || fail "could not extract the preflight lane from gate.sh"

PREFLIGHT_LOG="$TMP/preflight.log"
export PREFLIGHT_LOG
: >"$PREFLIGHT_LOG"
"$PREFLIGHT_HARNESS" || fail "preflight lane did not complete with every member green"
cmp -s "$PREFLIGHT_MANIFEST" "$PREFLIGHT_LOG" \
  || fail "preflight lane executed a different member set than it declares"

while IFS= read -r member; do
  : >"$PREFLIGHT_LOG"
  if PREFLIGHT_FAIL="$member" "$PREFLIGHT_HARNESS"; then
    fail "preflight lane stayed green with a red member: $member"
  fi
  [ "$(tail -n 1 "$PREFLIGHT_LOG")" = "$member" ] \
    || fail "preflight lane continued past its red member: $member"
done <"$PREFLIGHT_MANIFEST"
unset PREFLIGHT_LOG

# --- GATE-RACE-SCOPE-1: race-short package closure -------------------------
# Exercise the real discovery script in a tiny module while replacing only the
# expensive `go test` execution with a recorder. `go list` remains real, so the
# assertions cover changed-package resolution and the reverse dependency graph.
# An outer worktree may export an absolute GOWORK; this fixture has its own go.mod.
export GOWORK=off
RACE_REPO="$TMP/race-repo"
mkdir -p "$RACE_REPO/script" "$RACE_REPO/internal/foo" \
  "$RACE_REPO/internal/app" "$RACE_REPO/internal/consumer" "$RACE_REPO/internal/embedder" \
  "$RACE_REPO/internal/graph" \
  "$RACE_REPO/internal/unrelated"
cp "$ROOT/script/race-test.sh" "$RACE_REPO/script/race-test.sh"
chmod +x "$RACE_REPO/script/race-test.sh"
cat >"$RACE_REPO/go.mod" <<'EOF'
module github.com/beamfall/core

go 1.26
EOF
cat >"$RACE_REPO/internal/foo/foo.go" <<'EOF'
package foo

func Value() int { return 1 }
EOF
cat >"$RACE_REPO/internal/consumer/consumer.go" <<'EOF'
package consumer

import "github.com/beamfall/core/internal/foo"

func Value() int { return foo.Value() }
EOF
cat >"$RACE_REPO/internal/app/app.go" <<'EOF'
package app

import "github.com/beamfall/core/internal/foo"

func Value() int { return foo.Value() }
EOF
cat >"$RACE_REPO/internal/unrelated/unrelated.go" <<'EOF'
package unrelated

func Value() int { return 2 }
EOF
cat >"$RACE_REPO/internal/graph/graph.go" <<'EOF'
package graph

func Value() int { return 3 }
EOF
cat >"$RACE_REPO/internal/graph/graph_test.go" <<'EOF'
package graph

import "testing"

func TestGraphFixtureRuns(t *testing.T) {
	if Value() != 3 {
		t.Fatal("Value() != 3")
	}
}
EOF
cat >"$RACE_REPO/internal/embedder/embed.go" <<'EOF'
package embedder

import _ "embed"

//go:embed asset.txt
var asset string
EOF
printf 'base\n' >"$RACE_REPO/internal/embedder/asset.txt"
git -C "$RACE_REPO" init -qb main
git -C "$RACE_REPO" add .
git -C "$RACE_REPO" -c user.name=t -c user.email=t@e.com commit -qm base
RACE_BASE="$(git -C "$RACE_REPO" rev-parse HEAD)"
printf '\n// changed\n' >>"$RACE_REPO/internal/foo/foo.go"
git -C "$RACE_REPO" add internal/foo/foo.go
git -C "$RACE_REPO" -c user.name=t -c user.email=t@e.com commit -qm change-foo

RACE_SHIM="$TMP/race-shim"
mkdir -p "$RACE_SHIM"
REAL_GO="$(command -v go)"
cat >"$RACE_SHIM/go" <<EOF
#!/usr/bin/env bash
if [ "\${1:-}" = test ]; then
  printf '%s\n' "\$*" >>"\$RACE_TEST_LOG"
  if [ "\${RACE_TEST_EXECUTE:-0}" != 1 ]; then
    exit 0
  fi
fi
exec "$REAL_GO" "\$@"
EOF
chmod +x "$RACE_SHIM/go"

RACE_TEST_LOG="$TMP/race-test.log"
RACE_EVIDENCE="$TMP/race-evidence"
GOCACHE="$TMP/go-cache"
export GOCACHE RACE_TEST_LOG
: >"$RACE_TEST_LOG"
( cd "$RACE_REPO" && PATH="$RACE_SHIM:$PATH" AGENT_EVIDENCE_DIR="$RACE_EVIDENCE" \
    GATE_DIFF_BASE="$RACE_BASE" GATE_DIFF_HEAD=HEAD script/race-test.sh short ) \
  >"$TMP/race-diff.out" 2>&1 || {
    cat "$TMP/race-diff.out" >&2
    fail "race-short diff scope failed"
  }
python3 - "$RACE_EVIDENCE/race-short-discovery.json" <<'PY'
import json
import sys

evidence = json.load(open(sys.argv[1], encoding="utf-8"))
expected_changed = ["github.com/beamfall/core/internal/foo"]
expected_closure = {
    "github.com/beamfall/core/internal/app",
    "github.com/beamfall/core/internal/foo",
    "github.com/beamfall/core/internal/consumer",
}
if evidence.get("scope") != "diff":
    raise SystemExit(f"gate_scope_test: race-short scope is not diff: {evidence!r}")
if evidence.get("changed_packages") != expected_changed:
    raise SystemExit(f"gate_scope_test: wrong changed packages: {evidence!r}")
if evidence.get("closure_size") != len(expected_closure):
    raise SystemExit(f"gate_scope_test: wrong closure size: {evidence!r}")
if set(evidence.get("closure_packages", [])) != expected_closure:
    raise SystemExit(f"gate_scope_test: wrong reverse-dependency closure: {evidence!r}")
PY
grep -Fq 'github.com/beamfall/core/internal/foo' "$RACE_TEST_LOG" \
  || fail "race-short omitted the changed package"
grep -Fq 'github.com/beamfall/core/internal/consumer' "$RACE_TEST_LOG" \
  || fail "race-short omitted a reverse dependency"
grep -Eq -- '-run .+ github.com/beamfall/core/internal/app' "$RACE_TEST_LOG" \
  || fail "race-short omitted internal/app from the focused hot-package slice"
if grep -Fq 'github.com/beamfall/core/internal/unrelated' "$RACE_TEST_LOG"; then
  fail "race-short included an unrelated package in diff scope"
fi

# A directly changed package normally excluded from the full-tree short tier is
# promoted to its complete -race -short suite in diff scope.
RACE_GRAPH_BASE="$(git -C "$RACE_REPO" rev-parse HEAD)"
printf '\n// changed graph\n' >>"$RACE_REPO/internal/graph/graph.go"
git -C "$RACE_REPO" add internal/graph/graph.go
git -C "$RACE_REPO" -c user.name=t -c user.email=t@e.com commit -qm change-graph
: >"$RACE_TEST_LOG"
( cd "$RACE_REPO" && PATH="$RACE_SHIM:$PATH" AGENT_EVIDENCE_DIR="$RACE_EVIDENCE" \
    GATE_DIFF_BASE="$RACE_GRAPH_BASE" GATE_DIFF_HEAD=HEAD RACE_TEST_EXECUTE=1 \
    script/race-test.sh short ) \
  >"$TMP/race-graph.out" 2>&1 || fail "race-short graph fixture failed"
PROMOTED_GRAPH_COMMAND='resolved promoted: go test -race -short -count=1 -p 1 -parallel 2 -test.timeout=3600s github.com/beamfall/core/internal/graph'
PROMOTED_GRAPH_ARGS='test -race -short -count=1 -p 1 -parallel 2 -test.timeout=3600s -json github.com/beamfall/core/internal/graph'
grep -Fqx "$PROMOTED_GRAPH_COMMAND" "$TMP/race-graph.out" \
  || fail "race-short did not isolate changed graph package under the promoted policy"
if [ "$(wc -l <"$RACE_TEST_LOG" | tr -d ' ')" -ne 1 ] \
  || ! grep -Fqx "$PROMOTED_GRAPH_ARGS" "$RACE_TEST_LOG"; then
  fail "race-short did not execute graph with the exact promoted arguments"
fi
grep -F "$PROMOTED_GRAPH_COMMAND" "$TMP/race-graph.out"
grep -F '=== RUN   TestGraphFixtureRuns' "$TMP/race-graph.out" \
  || fail "race-short did not execute the graph fixture test"
if grep -Eq -- 'resolved (ordinary|focused):.*internal/graph|resolved promoted:.* -run ' \
  "$TMP/race-graph.out"; then
  fail "race-short weakened the changed graph package's full promoted suite"
fi

# A changed non-Go file consumed through go:embed also invalidates diff scope.
printf 'changed\n' >"$RACE_REPO/internal/embedder/asset.txt"
git -C "$RACE_REPO" add internal/embedder/asset.txt
git -C "$RACE_REPO" -c user.name=t -c user.email=t@e.com commit -qm change-embed
: >"$RACE_TEST_LOG"
( cd "$RACE_REPO" && PATH="$RACE_SHIM:$PATH" AGENT_EVIDENCE_DIR="$RACE_EVIDENCE" \
    GATE_DIFF_BASE="$RACE_BASE" GATE_DIFF_HEAD=HEAD script/race-test.sh short ) \
  >"$TMP/race-embed-full.out" 2>&1 || fail "race-short embed fallback failed"
python3 - "$RACE_EVIDENCE/race-short-discovery.json" <<'PY'
import json
import sys

evidence = json.load(open(sys.argv[1], encoding="utf-8"))
if evidence.get("scope") != "full":
    raise SystemExit(f"gate_scope_test: embedded input did not select full scope: {evidence!r}")
PY

# A module-graph input invalidates package-scoped discovery and must run the
# full race-short tree, including the otherwise unrelated package.
printf '\n' >>"$RACE_REPO/go.mod"
git -C "$RACE_REPO" add go.mod
git -C "$RACE_REPO" -c user.name=t -c user.email=t@e.com commit -qm change-module

# Merge and merge-train provide their sealed, post-rebase base/head pair via
# GATE_BASE_SHA/GATE_HEAD_SHA. That pair must win even when main has advanced.
RACE_HEAD="$(git -C "$RACE_REPO" rev-parse HEAD)"
( cd "$RACE_REPO" && . "$FUNCS" && GATE_BASE_SHA="$RACE_BASE" && \
    GATE_HEAD_SHA="$RACE_HEAD" && configure_race_short_scope && \
    printf '%s\n%s\n' "$GATE_DIFF_BASE" "$GATE_DIFF_HEAD" ) \
  >"$TMP/race-gate-scope"
[ "$(sed -n '1p' "$TMP/race-gate-scope")" = "$RACE_BASE" ] \
  || fail "race-short gate scope did not preserve the sealed rebase base"
[ "$(sed -n '2p' "$TMP/race-gate-scope")" = "$RACE_HEAD" ] \
  || fail "race-short gate scope did not preserve the sealed gate head"

: >"$RACE_TEST_LOG"
( cd "$RACE_REPO" && PATH="$RACE_SHIM:$PATH" AGENT_EVIDENCE_DIR="$RACE_EVIDENCE" \
    GATE_DIFF_BASE="$RACE_BASE" GATE_DIFF_HEAD=HEAD script/race-test.sh short ) \
  >"$TMP/race-full.out" 2>&1 || {
    cat "$TMP/race-full.out" >&2
    fail "race-short full fallback failed"
  }
python3 - "$RACE_EVIDENCE/race-short-discovery.json" <<'PY'
import json
import sys

evidence = json.load(open(sys.argv[1], encoding="utf-8"))
if evidence.get("scope") != "full":
    raise SystemExit(f"gate_scope_test: go.mod did not select full scope: {evidence!r}")
PY
grep -Fq 'github.com/beamfall/core/internal/unrelated' "$RACE_TEST_LOG" \
  || fail "race-short go.mod fallback omitted an unrelated full-tree package"
unset RACE_TEST_LOG

# 4. Dispatch proof. `GATE_PREFLIGHT=1` must reach the lane and stop there, and
# leaving it unset must still reach the whole gate — the preflight lane is only
# safe to add because it cannot alter what `make gate` runs. Executed, not
# asserted structurally: the real gate.sh drives its real control flow with its
# step runners swapped for recorders, so the capacity flock, the process
# supervisor and every actual step stay untouched.
DISPATCH_ROOT="$TMP/dispatch"
mkdir -p "$DISPATCH_ROOT/tools/gate"
DISPATCH_GATE="$DISPATCH_ROOT/tools/gate/gate.sh"
sed -E 's/^(run_step|run_scoped_step|start_parallel_step|wait_parallel|gate_full_rss_start|gate_full_rss_stop|run_cert_demo_tests)\(\) \{/recorded_\1_original() {/' \
  "$ROOT/tools/gate/gate.sh" >"$DISPATCH_GATE"
python3 - "$DISPATCH_GATE" <<'PY'
from pathlib import Path
import sys

path = Path(sys.argv[1])
text = path.read_text()
anchor = "\nvalidate_gate_mode\n"
if anchor not in text:
    raise SystemExit("gate_scope_test: gate.sh lost its validate_gate_mode call site")
recorders = """
run_step() { printf '%s\\n' "$1" >>"$GATE_DISPATCH_LOG"; }
run_scoped_step() { printf '%s\\n' "$1" >>"$GATE_DISPATCH_LOG"; }
start_parallel_step() { local slot="$1"; shift; printf '%s\\n' "$1" >>"$GATE_DISPATCH_LOG"; printf -v "$slot" '%s' 0; }
wait_parallel() { :; }
gate_full_rss_start() { :; }
gate_full_rss_stop() { :; }
run_cert_demo_tests() { printf 'certification demo server test\\n' >>"$GATE_DISPATCH_LOG"; }
"""
path.write_text(text.replace(anchor, recorders + anchor, 1))
PY
chmod +x "$DISPATCH_GATE"

run_dispatch() {  # run_dispatch <logfile> <outfile> [env assignments...]
  local log="$1" out="$2"
  shift 2
  : >"$log"
  env -u GATE_PREFLIGHT BEAMFALL_GATE_CAPACITY_HELD=1 GATE_PROCESS_SUPERVISED=1 \
    BEAMFALL_GATE_FIXTURE_GUARDED=1 \
    AGENT_EVIDENCE_DIR="$TMP/dispatch-evidence" GATE_DISPATCH_LOG="$log" \
    "$@" "$DISPATCH_GATE" >"$out" 2>&1
}

run_dispatch "$TMP/dispatch-preflight.log" "$TMP/dispatch-preflight.out" GATE_PREFLIGHT=1 \
  || fail "GATE_PREFLIGHT=1 did not complete the preflight lane"
grep -Fqx "gate-preflight: green" "$TMP/dispatch-preflight.out" \
  || fail "preflight mode did not report its own verdict"
if grep -Fqx "gate: green" "$TMP/dispatch-preflight.out"; then
  fail "preflight mode fell through into the full gate"
fi
cmp -s "$PREFLIGHT_MANIFEST" "$TMP/dispatch-preflight.log" \
  || fail "preflight mode ran a step set other than the declared lane"

run_dispatch "$TMP/dispatch-full.log" "$TMP/dispatch-full.out" GATE_PREFLIGHT=0 \
  || fail "the full gate no longer completes with the preflight branch present"
grep -Fqx "gate: green" "$TMP/dispatch-full.out" \
  || fail "the full gate lost its green verdict"
for expensive in "go build" "race-short" "go test + line-coverage ratchet"; do
  grep -Fqx "$expensive" "$TMP/dispatch-full.log" \
    || fail "the full gate stopped running an expensive lane: $expensive"
  if grep -Fqx "$expensive" "$TMP/dispatch-preflight.log"; then
    fail "the preflight lane pulled in an expensive gate lane: $expensive"
  fi
done
while IFS= read -r member; do
  grep -Fqx "$member" "$TMP/dispatch-full.log" \
    || fail "preflight member is not executed by the full gate: $member"
done <"$PREFLIGHT_MANIFEST"

REPO="$TMP/repo"
mkdir -p "$REPO/script" "$REPO/docs" "$REPO/contracts"
git -C "$REPO" init -qb main
echo base > "$REPO/docs/note.md"
echo base > "$REPO/contracts/registry.json"
git -C "$REPO" add docs/note.md contracts/registry.json
git -C "$REPO" -c user.name=t -c user.email=t@e.com commit -qm base

# Each probe runs in a subshell inside the fixture repo with the extracted funcs.
probe() {  # probe <scope> -> exit status of scope_matches
  ( cd "$REPO" && unset GATE_BASE_SHA GATE_HEAD_SHA && . "$FUNCS" && scope_matches "$1" )
}

assert_script_path_matches() {
  local candidate="$1"
  (
    cd "$REPO"
    . "$FUNCS"
    changed_paths() { printf '%s\n' "$candidate"; }
    scope_matches script
  ) || fail "orchestration input did not match script scope: $candidate"
}

assert_script_path_skips() {
  local candidate="$1"
  if (
    cd "$REPO"
    . "$FUNCS"
    changed_paths() { printf '%s\n' "$candidate"; }
    scope_matches script
  ); then
    fail "proven-unrelated path matched script scope: $candidate"
  fi
}

smoke_dispatch_for_path() {
  local candidate="$1"
  (
    cd "$REPO"
    . "$FUNCS"
    changed_paths() { printf '%s\n' "$candidate"; }
    run_step() { printf 'RUN\t%s\n' "$1"; }
    gate_metric() { printf 'METRIC\t%s\t%s\t%s\n' "$1" "$2" "$5"; }
    run_scoped_step "web UI E2E smoke (pre-gate)" webe2e make web-ui-e2e-smoke
    run_scoped_step "web UI E2E smoke (second pass)" webe2e make web-ui-e2e-smoke
  )
}

assert_smoke_path_runs() {
  local candidate="$1" output step
  output="$(smoke_dispatch_for_path "$candidate")" \
    || fail "web UI E2E smoke dispatch failed for covered input: $candidate"
  for step in "web UI E2E smoke (pre-gate)" "web UI E2E smoke (second pass)"; do
    grep -Fqx $'RUN\t'"$step" <<<"$output" \
      || fail "covered input did not RUN $step: $candidate"
  done
  if grep -Eq '^(SKIP|METRIC)' <<<"$output"; then
    fail "covered input recorded a smoke skip: $candidate"
  fi
}

assert_smoke_path_skips() {
  local candidate="$1" output step
  output="$(smoke_dispatch_for_path "$candidate")" \
    || fail "web UI E2E smoke dispatch failed for unrelated input: $candidate"
  for step in "web UI E2E smoke (pre-gate)" "web UI E2E smoke (second pass)"; do
    grep -Fqx $'METRIC\t'"$step"$'\tskip\tscope:webe2e' <<<"$output" \
      || fail "unrelated input did not record SKIP evidence for $step: $candidate"
    grep -Fqx "SKIP $step elapsed_s=0 (no webe2e changes)" <<<"$output" \
      || fail "unrelated input did not log scoped SKIP for $step: $candidate"
  done
  if grep -Eq '(^|[[:space:]])PASS([[:space:]]|$)|^RUN' <<<"$output"; then
    fail "unrelated input recorded smoke PASS/RUN instead of SKIP: $candidate"
  fi
}

# Every repository input class consumed by the orchestration support suite
# selects its script-scoped step.
for covered in \
  script/check-contract-registry.py \
  tools/gate/gate.sh \
  tools/orient/orient.sh \
  AGENTS.md \
  Makefile \
  go.mod \
  .github/workflows/release.yml \
  contracts/registry.json \
  internal/conformance/wire-contract-v1.json \
  cmd/ioe2e/main.go \
  internal/racefixture/race_fixture_test.go \
  internal/server/server.go \
  internal/web/app/package.json; do
  assert_script_path_matches "$covered"
done

# `cmd/ioe2e release-packages` walks internal/ and cmd/ for *_test.go, so every Go test file
# under them is a consumed suite input even though their non-test sources are skipped below.
# Without these cases the broad internal/cmd skip silently blinds the suites to their own inputs.
for consumed_test in \
  internal/backup/s3bucket_test.go \
  internal/ioe2e/lifecycle_test.go \
  cmd/beamfalld/main_test.go; do
  assert_script_path_matches "$consumed_test"
done

# Narrow docs and store changes cannot reach the orchestration support suite.
assert_script_path_skips docs/note.md
assert_script_path_skips internal/store/embeddings.go
# ...but only their non-test sources: the sibling _test.go above must still run.
assert_script_path_skips internal/store/embeddings_helper.go
assert_script_path_skips cmd/beamfalld/flags.go

# Unknown path classes fail broad until they are explicitly proven unrelated.
assert_script_path_matches new-area/input.txt

# The Playwright smoke drives the embedded app through the real Go server. Its
# scheduling scope therefore covers both sides plus the direct runner/config
# inputs, while a store-only change skips both smoke passes with SKIP evidence.
for webe2e_input in \
  internal/web/app/src/App.tsx \
  internal/web/app/playwright.config.ts \
  internal/web/app/package-lock.json \
  internal/server/server.go \
  cmd/beamfall/main.go \
  Makefile \
  go.mod \
  script/web-ui-e2e-smoke.sh \
  script/test-process-supervisor.py \
  tools/gate/web_runner_test.sh; do
  assert_smoke_path_runs "$webe2e_input"
done
assert_smoke_path_skips internal/store/embeddings.go

# Certification keeps the old broad source coverage while orchestration narrows.
for covered in docs/note.md internal/store/embeddings.go new-area/input.txt; do
  (
    cd "$REPO"
    . "$FUNCS"
    changed_paths() { printf '%s\n' "$covered"; }
    scope_matches cert
  ) || fail "cert scope stopped matching its prior broad input: $covered"
done

# The image build is deliberately narrower than the broad script suite. Prove
# every production input selects it and a nearby non-input does not.
for covered in Dockerfile go.mod internal/ffmpeg/ffmpeg.pin internal/ffmpeg/ffmpeg.lock; do
  (
    cd "$REPO"
    . "$FUNCS"
    changed_paths() { printf '%s\n' "$covered"; }
    scope_matches image
  ) || fail "covered image input did not match image scope: $covered"
done
if (
  cd "$REPO"
  . "$FUNCS"
  changed_paths() { printf '%s\n' go.sum; }
  scope_matches image
); then
  fail "uncovered input matched image scope"
fi

# 1) clean tree at main: no changes -> scoped lane skips.
if probe script; then fail "clean tree at main must not match script scope"; fi

# Release mode makes every mandatory scope non-vacuous without a diff range.
( cd "$REPO" && . "$FUNCS" && GATE_MODE=release scope_matches script ) \
  || fail "clean release checkout must match script scope"
( cd "$REPO" && . "$FUNCS" && GATE_MODE=release scope_matches cert ) \
  || fail "clean release checkout must match cert scope"
( cd "$REPO" && . "$FUNCS" && GATE_MODE=release scope_matches web ) \
  || fail "clean release checkout must match web scope"
( cd "$REPO" && . "$FUNCS" && GATE_MODE=release scope_matches webe2e ) \
  || fail "GATE_MODE=release must force both web UI E2E smoke steps to run"
( cd "$REPO" && . "$FUNCS" && GATE_MODE=release scope_matches contract ) \
  || fail "clean release checkout must match contract scope"
( cd "$REPO" && . "$FUNCS" && GATE_MODE=normal GITHUB_WORKFLOW=release-gate scope_matches script ) \
  || fail "release workflow must force release scope"
( cd "$REPO" && . "$FUNCS" && GATE_MODE=normal GITHUB_WORKFLOW=release-gate scope_matches webe2e ) \
  || fail "release-gate workflow must force both web UI E2E smoke steps to run"

# 2) committed-clean branch changing script/: lane must RUN (the RMAUD-10 fix).
git -C "$REPO" checkout -qb run/T1
echo x > "$REPO/script/foo.sh"
git -C "$REPO" add script/foo.sh
git -C "$REPO" -c user.name=t -c user.email=t@e.com commit -qm t1
probe script || fail "committed-clean branch changing script/ must match script scope"

# 2b) the release-workflow and race-prove regressions live in the script lane, so
# a workflow-only or Makefile-only change must be able to trigger it.
git -C "$REPO" checkout -q main
git -C "$REPO" checkout -qb run/T1-workflow
mkdir -p "$REPO/.github/workflows"
echo w > "$REPO/.github/workflows/release.yml"
git -C "$REPO" add .github/workflows/release.yml
git -C "$REPO" -c user.name=t -c user.email=t@e.com commit -qm t1-workflow
probe script || fail "workflow-only branch must match script scope"
if probe web; then fail "workflow-only branch must not match web scope"; fi

git -C "$REPO" checkout -q main
git -C "$REPO" checkout -qb run/T1-makefile
echo m > "$REPO/Makefile"
git -C "$REPO" add Makefile
git -C "$REPO" -c user.name=t -c user.email=t@e.com commit -qm t1-makefile
probe script || fail "Makefile-only branch must match script scope"

# 2c) checker input outside script/: a state-only contract-registry change must
# run the combined suite containing check_contract_registry_test.py.
git -C "$REPO" checkout -q main
git -C "$REPO" checkout -qb run/T1-contract
mkdir -p "$REPO/contracts"
echo '{}' > "$REPO/contracts/registry.json"
git -C "$REPO" add contracts/registry.json
git -C "$REPO" -c user.name=t -c user.email=t@e.com commit -qm t1-contract
probe script || fail "contract-registry-only branch must match script scope"
probe contract || fail "contract-registry-only branch must match contract scope"
echo mixed >> "$REPO/docs/note.md"
probe contract || fail "committed contract change must remain scoped with unrelated tracked dirt"
git -C "$REPO" checkout -q -- docs/note.md

for covered in \
  contracts/registry.json \
  internal/conformance/wire-contract-v1.8.0.json \
  internal/graph/schema.graphqls \
  tools/wire-contract/main.go \
  script/crossrepo-conformance.sh \
  script/crossrepo-conformance_pin_test.sh \
  script/crossrepo-conformance_interrupt_test.py \
  testing/release-gates/gate-commands.tsv \
  tools/gate/gate.sh \
  Makefile
do
  (
    cd "$REPO"
    . "$FUNCS"
    changed_paths() { printf '%s\n' "$covered"; }
    scope_matches contract
  ) || fail "contract input did not match contract scope: $covered"
done

if (
  cd "$REPO"
  . "$FUNCS"
  changed_paths() { printf '%s\n' internal/playback/session.go; }
  scope_matches contract
); then
  fail "unrelated Core path matched contract scope"
fi

# 2d) Root README is the one proven unrelated tracked state path.
git -C "$REPO" checkout -q main
git -C "$REPO" checkout -qb run/T1-readme
echo readme > "$REPO/README.md"
git -C "$REPO" add README.md
git -C "$REPO" -c user.name=t -c user.email=t@e.com commit -qm t1-readme
if probe script; then fail "README-only branch must not match script scope"; fi
if probe contract; then fail "README-only branch must not match contract scope"; fi
echo dirty >> "$REPO/contracts/registry.json"
probe contract || fail "dirty contract change must scope with unrelated committed change"
git -C "$REPO" checkout -q -- contracts/registry.json

git -C "$REPO" checkout -q run/T1

# 3) same branch: an unrelated scope still skips.
if probe web; then fail "script-only branch must not match web scope"; fi

# 4) dirty paths are unioned with committed branch paths.
echo y >> "$REPO/docs/note.md"
probe script || fail "committed script/ change must remain scoped with unrelated tracked dirt"
# Script dirt does too.
echo z >> "$REPO/script/foo.sh"
probe script || fail "dirty script/ change must match script scope"
git -C "$REPO" checkout -q -- script/foo.sh docs/note.md

# 5) untracked-only trees are CLEAN (git diff never saw untracked files): the
# committed branch changes still scope — leaf worktrees carry untracked
# .agent-evidence/* and must not regress to lane-skipping.
echo stray > "$REPO/untracked.txt"
probe script || fail "untracked-only tree must still scope committed script/ changes"
rm -f "$REPO/untracked.txt"

# 6) explicit pins stay authoritative over the clean-tree fallback, including
# valid empty and non-empty ranges.
base="$(git -C "$REPO" rev-parse main)"; head="$(git -C "$REPO" rev-parse HEAD)"
( cd "$REPO" && . "$FUNCS" && GATE_BASE_SHA="$base" GATE_HEAD_SHA="$head" scope_matches script ) \
  || fail "explicit sha pins must scope the pinned range"
( cd "$REPO" && . "$FUNCS" && GATE_BASE_SHA="$head" GATE_HEAD_SHA="$head" scope_matches script ) \
  && fail "identical pinned shas must not match any scope" || true

# 7) inherited CI pins cannot leak into local fallback probes.
( export GATE_BASE_SHA=missing GATE_HEAD_SHA=missing; probe script ) \
  || fail "local fallback must ignore inherited test-process pins"

# 8) first-push, missing, and partial explicit ranges fail open.
zero=0000000000000000000000000000000000000000
( cd "$REPO" && . "$FUNCS" && GATE_BASE_SHA="$zero" GATE_HEAD_SHA="$head" scope_matches web ) \
  || fail "first-push zero base must fail open"
( cd "$REPO" && . "$FUNCS" && GATE_BASE_SHA=missing GATE_HEAD_SHA="$head" scope_matches web ) \
  || fail "missing explicit commit must fail open"
( cd "$REPO" && . "$FUNCS" && GATE_BASE_SHA="$base" scope_matches web ) \
  || fail "partial explicit range must fail open"

# 9) unrelated explicit and local-main ranges fail open.
unrelated="$(printf unrelated | git -C "$REPO" commit-tree "$(git -C "$REPO" rev-parse "$base^{tree}")")"
( cd "$REPO" && . "$FUNCS" && GATE_BASE_SHA="$unrelated" GATE_HEAD_SHA="$head" scope_matches web ) \
  || fail "unrelated explicit range must fail open"
git -C "$REPO" update-ref refs/heads/main "$unrelated"
probe web || fail "unrelated local main must fail open"
git -C "$REPO" update-ref refs/heads/main "$base"
git -C "$REPO" update-ref -d refs/heads/main
probe web || fail "missing local main must fail open"
git -C "$REPO" update-ref refs/heads/main "$base"

# 10) endpoints are resolved once: moving a named base after resolution cannot
# turn a non-empty trusted range into an empty one.
git -C "$REPO" update-ref refs/heads/moving-base "$base"
git -C "$REPO" update-ref refs/heads/moving-head "$head"
(
  cd "$REPO" && . "$FUNCS"
  git() {
    local output
    output="$(command git "$@")" || return
    if [ "$1" = rev-parse ] && [ "${3:-}" = 'moving-head^{commit}' ]; then
      command git update-ref refs/heads/moving-base "$head"
    fi
    printf '%s\n' "$output"
  }
  GATE_BASE_SHA=moving-base GATE_HEAD_SHA=moving-head scope_matches script
) || fail "moving refs must be diffed by resolved commit ids"

# 11) a diff execution failure fails open.
(
  cd "$REPO" && . "$FUNCS"
  git() {
    if [ "$1" = diff ] && [ "$2" = --name-only ]; then return 9; fi
    command git "$@"
  }
  GATE_BASE_SHA="$base" GATE_HEAD_SHA="$head" scope_matches web
) || fail "revision diff failure must fail open"

echo "gate_scope_test: OK"
