#!/usr/bin/env bash
# Selftest for gate step result reuse (openspec/specs/gate-step-reuse): a listed
# step is reused only for a prior pass on identical inputs, and every case that
# cannot prove that runs the step.
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
set -euo pipefail

# The gate that runs this selftest exports its own diff range and reuse
# settings; none of them describe the fixture repository below.
unset GATE_DIFF_BASE GATE_DIFF_HEAD GATE_MODE GATE_PREFLIGHT RELAY_VECTORS_PATH \
  GATE_STEP_REUSE GATE_STEP_REUSE_DIR GATE_STEP_REUSE_MAX_AGE_S PLAYWRIGHT_BROWSERS_PATH \
  npm_config_playwright_browsers_path npm_package_config_playwright_browsers_path XDG_CACHE_HOME

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
# Browser steps key Playwright's directory under HOME; the host's shared cache
# changes while other jobs install browsers, so the fixture uses its own HOME.
mkdir -p "$TMP/default-home"
export HOME="$TMP/default-home"

python3 - "$ROOT/tools/gate/gate.sh" "$TMP/steps.sh" "$TMP/bound-steps.sh" <<'PY'
from pathlib import Path
import re
import sys

text = Path(sys.argv[1]).read_text()
start = text.index("gate_signal_group() {")
end = text.index("\nchanged_paths() {")
prelude = "#!/usr/bin/env bash\nset -euo pipefail\nexport GATE_PROCESS_GROUP_OWNED=1\n" + text[start:end]
Path(sys.argv[2]).write_text(prelude + '\nrun_step "$@"\n')
# The same gate with one step declared mode-bound, to prove such a step is
# never shared between the preflight and the full gate.
Path(sys.argv[3]).write_text(
    prelude
    + '\ngate_step_reuse_mode_bound() { [ "$1" = "compile budget manifest selftest" ]; }\n'
    + 'run_step "$@"\n'
)

# Every listed step must be a real gate step: a renamed step silently loses
# reuse, and a stale name here would hide that.
policy = text[text.index("gate_step_reuse_policy() {"):text.index("gate_step_reuse_mode_bound() {")]
listed = re.findall(r'"([^"]+)"(?=[|)])', policy)
if len(listed) < 5:
    raise SystemExit(f"step reuse list could not be read: {listed}")
body = text[text.index("gate_step_reuse_record() {"):]
for name in listed:
    if f'"{name}" ' not in body:
        raise SystemExit(f"step reuse lists a step the gate does not run: {name}")
for never in ("fuzz", "vulnerability check", "secret scan (Core + present siblings; best-effort)",
              "ADR-0122 cross-repo wire conformance", "playback transcode latency budgets",
              "client DRM/EME invariant", "App Store firewall static gate", "mutation policy history"):
    if never in listed:
        raise SystemExit(f"step must never be reused: {never}")
# A mode-bound step keeps the lane in its key; one that is not on the reuse
# list would be a declaration with no effect.
bound = text[text.index("gate_step_reuse_mode_bound() {"):text.index("gate_step_reuse_enabled() {")]
for name in re.findall(r'"([^"]+)"(?=[|)])', bound):
    if name not in listed:
        raise SystemExit(f"mode-bound step is not on the reuse list: {name}")
PY
chmod +x "$TMP/steps.sh" "$TMP/bound-steps.sh"

# Preflight passes are shared with the full gate because nothing a step runs
# reads GATE_PREFLIGHT. A new reader is either dispatch, which belongs in this
# list, or a step that now differs between the lanes, which belongs in
# gate_step_reuse_mode_bound first.
if git -C "$ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  readers="$(git -C "$ROOT" grep -l GATE_PREFLIGHT -- . ':!docs' ':!openspec' ':!*.md' |
    grep -Ev '^(Makefile|tools/gate/(gate-entry\.sh|gate\.sh|gate_latest\.py)|.*_test\.(sh|py))$' || true)"
  if [ -n "$readers" ]; then
    echo "step reuse: GATE_PREFLIGHT is read outside the gate's dispatch:" >&2
    printf '%s\n' "$readers" >&2
    exit 1
  fi
fi

REPO="$TMP/repo"
mkdir "$REPO"
git -C "$REPO" init -q
git -C "$REPO" config user.email gate@example.invalid
git -C "$REPO" config user.name gate
printf 'one\n' >"$REPO/tracked.txt"
printf 'ignored.txt\n' >"$REPO/.gitignore"
git -C "$REPO" add -A
git -C "$REPO" commit -q -m base
BASE="$(git -C "$REPO" rev-parse HEAD)"

COUNT="$TMP/count"
: >"$COUNT"
runs() { wc -l <"$COUNT" | tr -d ' '; }
RUN_ID=0
step() {  # step <name> [command...]; default command appends one line to $COUNT
  local name="$1"
  shift
  [ "$#" -gt 0 ] || set -- bash -c 'echo ran >>"$1"' _ "$COUNT"
  RUN_ID=$((RUN_ID + 1))
  local status=0
  (cd "$REPO" && GATE_RUN_ID="run-$RUN_ID" AGENT_EVIDENCE_DIR="$TMP/evidence" "${STEPS:-$TMP/steps.sh}" "$name" "$@") \
    >"$TMP/out" 2>"$TMP/err" || status=$?
  # The gate's EXIT trap can turn a shell abort into status 0 (bash 3.2 reports
  # $? as 0 after a `set -u` failure), so the status alone cannot prove the
  # run finished: a reuse bug must never abort the gate.
  if grep -Eq 'unbound variable|value too great for base|syntax error' "$TMP/err"; then
    echo "step reuse: $name aborted the shell" >&2
    cat "$TMP/out" "$TMP/err" >&2
    exit 1
  fi
  return "$status"
}
expect_ran() {  # <want-total-runs> <label>
  [ "$(runs)" = "$1" ] || { echo "step reuse: $2: want $1 executions, got $(runs)" >&2; cat "$TMP/out" "$TMP/err" >&2; exit 1; }
}

# Identical inputs: the second run is reused and says which run passed.
step "go vet"; expect_ran 1 "first run executes"
grep -q '^PASS go vet' "$TMP/out"
step "go vet"; expect_ran 1 "identical rerun is reused"
grep -q '^REUSE go vet (run=run-1 ' "$TMP/out" || { echo "reuse line missing the source run" >&2; cat "$TMP/out" >&2; exit 1; }
python3 - "$TMP/evidence/gate-runs.jsonl" <<'PY'
import json
import sys

rows = [json.loads(line) for line in open(sys.argv[1], encoding="utf-8")]
last = rows[-1]
if last["status"] != "reuse" or last["step"] != "go vet" or "run=run-1" not in last["command"]:
    raise SystemExit(f"reused step did not record a reuse evidence row: {last}")
if sum(1 for row in rows if row["status"] == "pass") != 1:
    raise SystemExit("a reused step must not record a fresh pass")
PY
# A record made in one worktree is visible from another worktree of the clone.
git -C "$REPO" worktree add -q "$TMP/other" HEAD
(cd "$TMP/other" && GATE_RUN_ID=other AGENT_EVIDENCE_DIR="$TMP/evidence" "$TMP/steps.sh" "go vet" \
  bash -c 'echo ran >>"$1"' _ "$COUNT") >"$TMP/out" 2>"$TMP/err"
expect_ran 1 "same tree in another worktree is reused"
git -C "$REPO" worktree remove --force "$TMP/other"

# Any input change runs the step.
printf 'two\n' >>"$REPO/tracked.txt"
step "go vet"; expect_ran 2 "tracked edit"
step "go vet"; expect_ran 2 "tracked edit, then identical"
printf 'new\n' >"$REPO/untracked.txt"
step "go vet"; expect_ran 3 "untracked file"
printf 'noise\n' >"$REPO/ignored.txt"
step "go vet"; expect_ran 3 "ignored file is not an input"
step "go vet" bash -c 'echo ran >>"$1"; true' _ "$COUNT"; expect_ran 4 "different command"
GOFLAGS=-mod=mod step "go vet"; expect_ran 5 "gate-relevant environment"
git -C "$REPO" add -A && git -C "$REPO" commit -q -m two
step "go vet"; expect_ran 5 "committing the same content keeps the pass"

# Switches and modes that must always run.
GATE_STEP_REUSE=0 step "go vet"; expect_ran 6 "GATE_STEP_REUSE=0"
GATE_MODE=release step "go vet"; expect_ran 7 "release mode"
GATE_MODE=release step "go vet"; expect_ran 8 "release mode never records for reuse"
step "unlisted step"; step "unlisted step"; expect_ran 10 "unlisted steps"

# Only a pass is recorded.
if step "go build" bash -c 'echo ran >>"$1"; exit 3' _ "$COUNT"; then echo "failing step passed" >&2; exit 1; fi
if step "go build" bash -c 'echo ran >>"$1"; exit 3' _ "$COUNT"; then echo "failing step passed" >&2; exit 1; fi
expect_ran 12 "failures are never reused"
! grep -q REUSE "$TMP/out"

# A step that changes its own inputs has no input state to bind the pass to.
step "race-prove" bash -c 'echo ran >>"$1"; echo moved >>tracked.txt' _ "$COUNT"
grep -q '^NOREUSE race-prove' "$TMP/out" || { echo "moving inputs were recorded for reuse" >&2; exit 1; }
git -C "$REPO" checkout -q -- tracked.txt
step "race-prove" bash -c 'echo ran >>"$1"; echo moved >>tracked.txt' _ "$COUNT"
expect_ran 14 "inputs that moved during the step"
git -C "$REPO" checkout -q -- tracked.txt

# Damaged, mismatched, future-dated or expired records run the step.
STORE="$(git -C "$REPO" rev-parse --git-common-dir)"
case "$STORE" in /*) ;; *) STORE="$REPO/$STORE" ;; esac
STORE="$STORE/gate-step-reuse"
step "go vet"; expect_ran 14 "baseline record still reusable"
record="$STORE/$(tail -n 1 "$TMP/evidence/gate-runs.jsonl" | sed -n 's/.* key=\([0-9a-f]*\)\].*/\1/p')"
test -f "$record" || { echo "reuse evidence row does not name its record" >&2; exit 1; }
cp "$record" "$TMP/good-record"
tamper() {  # <label> <sed-expression>
  sed "$2" "$TMP/good-record" >"$record"
  local before
  before="$(runs)"
  step "go vet"
  expect_ran "$((before + 1))" "$1"
  cp "$TMP/good-record" "$record"   # the fresh pass rewrote it; restore the probe
}
tamper "expired record" 's/^passed_at=.*/passed_at=1000/'
tamper "future-dated record" 's/^passed_at=.*/passed_at=99999999999/'
tamper "record for another key" 's/^key=.*/key=0000/'
tamper "non-pass record" 's/^status=pass/status=fail/'
tamper "unknown record format" '1s/.*/gate-step-reuse-record\/3/'
tamper "record from before the lane was recorded" '1s/.*/gate-step-reuse-record\/1/'
tamper "record without the lane that passed" '/^kind=/d'
: >"$record"
before="$(runs)"; step "go vet"; expect_ran "$((before + 1))" "empty record"
before="$(runs)"; GATE_STEP_REUSE_MAX_AGE_S=nope step "go vet"; expect_ran "$((before + 1))" "unparseable age limit"

# A malformed age limit never reuses, never records and never aborts the run;
# the pass still lands. A zero-padded limit is decimal, not octal.
printf 'age\n' >>"$REPO/tracked.txt"
before="$(runs)"
for bad in nope -1 +5 1e3 ' 60' 60s; do
  GATE_STEP_REUSE_MAX_AGE_S="$bad" step "go vet" || { echo "age limit '$bad' failed the step" >&2; exit 1; }
  if ! grep -q '^PASS go vet' "$TMP/out" || ! grep -q '^NOREUSE go vet (GATE_STEP_REUSE_MAX_AGE_S' "$TMP/out"; then
    echo "age limit '$bad' did not skip recording" >&2; cat "$TMP/out" "$TMP/err" >&2; exit 1
  fi
done
expect_ran "$((before + 6))" "malformed age limits"
step "go vet"; expect_ran "$((before + 7))" "a malformed age limit leaves no record"
step "go vet"; expect_ran "$((before + 7))" "a valid age limit records again"
printf 'padded\n' >>"$REPO/tracked.txt"
GATE_STEP_REUSE_MAX_AGE_S=09 step "go vet"; expect_ran "$((before + 8))" "zero-padded age limit runs"
step "go vet"; expect_ran "$((before + 8))" "zero-padded age limit records"
git -C "$REPO" checkout -q -- tracked.txt

# The support selftests are a listed step; a step off the list always runs.
before="$(runs)"
step "support script tests"; step "support script tests"
expect_ran "$((before + 1))" "support script tests on identical inputs"
step "gofmt"; step "gofmt"
expect_ran "$((before + 3))" "an unlisted step"
mkdir -p "$TMP/pybin"
printf '#!/bin/sh\n[ "$1" = --version ] && { echo "Python 0.0.0"; exit 0; }\nexec %s "$@"\n' "$(command -v python3)" >"$TMP/pybin/python3"
chmod +x "$TMP/pybin/python3"
PATH="$TMP/pybin:$PATH" step "support script tests"
expect_ran "$((before + 4))" "a different python3"

# A preflight pass is reused by the full gate, and a full-gate pass by a
# preflight: the lane is not an input, and neither are the variables that only
# say which coordinator lock or machine slot the run holds. The reuse line and
# its evidence row name the run that passed and its lane.
last_row() {  # <want-status> <want-kind> <text the command must carry>
  python3 - "$TMP/evidence/gate-runs.jsonl" "$@" <<'PY'
import json
import sys

row = json.loads(open(sys.argv[1], encoding="utf-8").read().splitlines()[-1])
status, kind, needle = sys.argv[2:]
if row["status"] != status or row["kind"] != kind or needle not in row["command"]:
    raise SystemExit(f"want status={status} kind={kind} command with {needle!r}, got {row}")
PY
}
printf 'lanes\n' >>"$REPO/tracked.txt"
before="$(runs)"
GATE_PREFLIGHT=1 BEAMFALL_GATE_CAPACITY_HELD=1 step "support script tests"
preflight_run="run-$RUN_ID"
expect_ran "$((before + 1))" "preflight runs the step"
BEAMFALL_GATE_CAPACITY_HELD=1 BEAMFALL_GATE_CAPACITY_FD=9 BEAMFALL_GATE_SLOT_HELD="$TMP/slot" \
  step "support script tests"
expect_ran "$((before + 1))" "full gate after a preflight pass"
grep -q "^REUSE support script tests (run=$preflight_run kind=make-gate-preflight " "$TMP/out" ||
  { echo "full gate did not name the preflight run it reused" >&2; cat "$TMP/out" >&2; exit 1; }
last_row reuse make-gate "run=$preflight_run kind=make-gate-preflight "
step "golangci-lint (staticcheck + gosec)"
gate_run="run-$RUN_ID"
GATE_PREFLIGHT=1 step "golangci-lint (staticcheck + gosec)"
expect_ran "$((before + 2))" "preflight after a full-gate pass"
grep -q "^REUSE golangci-lint (staticcheck + gosec) (run=$gate_run kind=make-gate " "$TMP/out" ||
  { echo "preflight did not name the gate run it reused" >&2; cat "$TMP/out" >&2; exit 1; }
last_row reuse make-gate-preflight "run=$gate_run kind=make-gate "

# A step that runs a different command in the preflight than in the full gate
# shares nothing: the command is part of the key in both lanes.
narrow=(bash -c 'echo ran >>"$1"; : preflight variant' _ "$COUNT")
before="$(runs)"
GATE_PREFLIGHT=1 step "gate scope selftest" "${narrow[@]}"
step "gate scope selftest"
expect_ran "$((before + 2))" "a different command in the full gate than in the preflight"
! grep -q REUSE "$TMP/out" || { echo "full gate reused a pass for another command" >&2; exit 1; }
GATE_PREFLIGHT=1 step "gate scope selftest" "${narrow[@]}"
step "gate scope selftest"
expect_ran "$((before + 2))" "each command is still reused for itself"

# A step declared mode-bound checks less in the preflight under the same
# command text, so its pass stays inside the lane that produced it.
before="$(runs)"
GATE_PREFLIGHT=1 STEPS="$TMP/bound-steps.sh" step "compile budget manifest selftest"
STEPS="$TMP/bound-steps.sh" step "compile budget manifest selftest"
expect_ran "$((before + 2))" "a mode-bound step after a preflight pass"
! grep -q REUSE "$TMP/out" || { echo "full gate reused a mode-bound preflight pass" >&2; exit 1; }
STEPS="$TMP/bound-steps.sh" step "compile budget manifest selftest"
GATE_PREFLIGHT=1 STEPS="$TMP/bound-steps.sh" step "compile budget manifest selftest"
expect_ran "$((before + 2))" "a mode-bound step is reused inside its own lane"
# The declaration is per step: the same gate still shares every other step.
GATE_PREFLIGHT=1 STEPS="$TMP/bound-steps.sh" step "secret scan selftest"
STEPS="$TMP/bound-steps.sh" step "secret scan selftest"
expect_ran "$((before + 3))" "an unbound step beside a mode-bound one"
git -C "$REPO" checkout -q -- tracked.txt

# race-short selects packages from the diff base, so the base is part of its key.
before="$(runs)"
step "race-short"; step "race-short"
expect_ran "$((before + 2))" "race-short without a resolved diff base"
GATE_DIFF_BASE="$BASE" step "race-short"; GATE_DIFF_BASE="$BASE" step "race-short"
expect_ran "$((before + 3))" "race-short with the same base"
GATE_DIFF_BASE="$(git -C "$REPO" rev-parse HEAD)" step "race-short"
expect_ran "$((before + 4))" "race-short with a different base"
GATE_DIFF_BASE=not-a-commit step "race-short"
expect_ran "$((before + 5))" "race-short with an unresolvable base"
GATE_DIFF_BASE="$BASE" GATE_DIFF_HEAD=not-a-commit step "race-short"
expect_ran "$((before + 6))" "race-short with an unresolvable head"

# race-short scopes from base..HEAD commits, so an edit that was uncommitted when
# it passed has not been raced: committing it must run the step again.
printf 'raced later\n' >>"$REPO/tracked.txt"
before="$(runs)"
GATE_DIFF_BASE="$BASE" step "race-short"
git -C "$REPO" add -A
git -C "$REPO" commit -q -m "commit the edit"
GATE_DIFF_BASE="$BASE" step "race-short"
expect_ran "$((before + 2))" "race-short after committing an edit it passed with uncommitted"
GATE_DIFF_BASE="$BASE" step "race-short"
expect_ran "$((before + 2))" "race-short with the same base and head"

# The built web bundle is ignored by git but embedded by the Go build. A step
# that writes it is reused only while the bundle it left behind is in place,
# and no step is reused over a missing or different bundle.
printf 'internal/web/app-dist/\n' >>"$REPO/.gitignore"
BUNDLE="$REPO/internal/web/app-dist/production"
build() { step "web app build" bash -c 'echo ran >>"$1"; mkdir -p "$2"; printf built >"$2/index.html"' _ "$COUNT" "$BUNDLE"; }
before="$(runs)"
build; build
expect_ran "$((before + 1))" "a step that writes the bundle is reused while the bundle stands"
step "go build"; step "go build"
expect_ran "$((before + 2))" "go build over an unchanged bundle"
printf stale >"$BUNDLE/index.html"
step "go build"
expect_ran "$((before + 3))" "go build over a different bundle"
build
expect_ran "$((before + 4))" "web app build over a different bundle"
rm -rf "$REPO/internal"
build
expect_ran "$((before + 5))" "web app build with the bundle missing"
step "go build"
expect_ran "$((before + 5))" "go build once the same bundle is back"

# A source-only step never reads the bundle, so a preflight pass made before
# the bundle existed is still good once the gate has built one. A step that
# embeds the bundle runs.
rm -rf "$REPO/internal"
before="$(runs)"
GATE_PREFLIGHT=1 step "gate scope selftest"; step "go build"
expect_ran "$((before + 2))" "first runs with no bundle"
mkdir -p "$BUNDLE"
printf fresh >"$BUNDLE/index.html"
step "gate scope selftest"
expect_ran "$((before + 2))" "a source-only step after the bundle is built"
grep -q '^REUSE gate scope selftest (run=[^ ]* kind=make-gate-preflight ' "$TMP/out" ||
  { echo "source-only step did not reuse the preflight pass" >&2; cat "$TMP/out" >&2; exit 1; }
step "go build"
expect_ran "$((before + 3))" "go build over a bundle it never saw"

# Inputs outside the worktree that cannot be content-addressed stop reuse.
before="$(runs)"
RELAY_VECTORS_PATH="$TMP/vectors.json" step "go build"
expect_ran "$((before + 1))" "external relay vectors"
GOWORK="$TMP/go.work" step "go build"
expect_ran "$((before + 2))" "active go.work"
# Go finds a go.work in any parent directory without being told.
: >"$TMP/go.work"
step "go build"
expect_ran "$((before + 3))" "go.work discovered in a parent directory"
rm -f "$TMP/go.work"
mkdir -p "$TMP/bin"
printf '#!/bin/sh\necho "ffmpeg version fixture"\n' >"$TMP/bin/ffmpeg"
chmod +x "$TMP/bin/ffmpeg"
PATH="$TMP/bin:$PATH" step "go build"
expect_ran "$((before + 4))" "a different ffmpeg on PATH"
mkdir -p "$REPO/internal/web/app/node_modules"
printf 'node_modules\n' >>"$REPO/.git/info/exclude"
step "go build"
expect_ran "$((before + 4))" "an empty node_modules is not an input"
printf '{}\n' >"$REPO/internal/web/app/node_modules/.package-lock.json"
step "go build"
expect_ran "$((before + 5))" "a different installed dependency set"
step "gate scope selftest"
expect_ran "$((before + 5))" "a source-only step ignores the installed dependency set"

# Two listed steps ask git what is tracked and staged, so staging or unstaging
# a path is an input change for them even though the worktree is byte-identical.
# A source-only step does not care, and the real index is only ever read.
AGPL="AGPL forbidden-symbol gate"
printf 'staged\n' >"$REPO/staged.txt"
before="$(runs)"
step "$AGPL"; step "$AGPL"
expect_ran "$((before + 1))" "an index-reading step on identical inputs"
step "gate scope selftest"
expect_ran "$((before + 2))" "a source-only step with a new untracked file"
git -C "$REPO" add staged.txt
index_before="$(git -C "$REPO" hash-object .git/index)"
step "$AGPL"
expect_ran "$((before + 3))" "an index-reading step after staging a file"
[ "$(git -C "$REPO" hash-object .git/index)" = "$index_before" ] ||
  { echo "step reuse rewrote the real index" >&2; exit 1; }
step "gate scope selftest"
expect_ran "$((before + 3))" "staging alone is not an input to a source-only step"
git -C "$REPO" rm -q --cached staged.txt
step "$AGPL"
expect_ran "$((before + 3))" "unstaging returns to the state that already passed"
step "secret scan selftest"; step "secret scan selftest"
expect_ran "$((before + 4))" "the secret-scan selftest is an index-reading step"
rm -f "$REPO/staged.txt"

# The web E2E step keys the browsers in Playwright's registry directory for
# this host, and runs whenever that directory cannot be established; other
# steps stay reusable without it.
os() {  # os <uname -s> <step...>: run a step on a host reporting that system
  mkdir -p "$TMP/os-$1"
  printf '#!/bin/sh\n[ "$1" = -s ] && { echo %s; exit 0; }\necho "%s fixture"\n' "$1" "$1" >"$TMP/os-$1/uname"
  chmod +x "$TMP/os-$1/uname"
  local system="$1"
  shift
  PATH="$TMP/os-$system:$PATH" step "$@"
}
SMOKE="web UI E2E smoke (pre-gate)"
mkdir -p "$TMP/xdg/ms-playwright/chromium-1" "$TMP/home/Library/Caches/ms-playwright/chromium-1" \
  "$TMP/home/.cache/ms-playwright/chromium-1"
before="$(runs)"
os Plan9 "$SMOKE"; os Plan9 "$SMOKE"
expect_ran "$((before + 2))" "web E2E smoke on a host without a known browser directory"
os Plan9 "go build"; os Plan9 "go build"
expect_ran "$((before + 3))" "go build on a host without a known browser directory"
HOME="" os Linux "$SMOKE"; HOME="" os Linux "$SMOKE"
expect_ran "$((before + 5))" "web E2E smoke on Linux without HOME or XDG_CACHE_HOME"
before="$(runs)"
XDG_CACHE_HOME="$TMP/xdg" os Linux "$SMOKE"; XDG_CACHE_HOME="$TMP/xdg" os Linux "$SMOKE"
expect_ran "$((before + 1))" "web E2E smoke on Linux with the same browsers"
mkdir "$TMP/xdg/ms-playwright/chromium-2"
XDG_CACHE_HOME="$TMP/xdg" os Linux "$SMOKE"
expect_ran "$((before + 2))" "web E2E smoke on Linux after a browser install under XDG_CACHE_HOME"
HOME="$TMP/home" os Linux "$SMOKE"; HOME="$TMP/home" os Linux "$SMOKE"
expect_ran "$((before + 3))" "web E2E smoke on Linux keyed from ~/.cache"
mkdir "$TMP/home/.cache/ms-playwright/chromium-2"
HOME="$TMP/home" os Linux "$SMOKE"
expect_ran "$((before + 4))" "web E2E smoke on Linux after a browser install under ~/.cache"
HOME="$TMP/home" os Darwin "$SMOKE"; HOME="$TMP/home" os Darwin "$SMOKE"
expect_ran "$((before + 5))" "web E2E smoke on macOS with the same browsers"
mkdir "$TMP/home/Library/Caches/ms-playwright/chromium-2"
HOME="$TMP/home" os Darwin "$SMOKE"
expect_ran "$((before + 6))" "web E2E smoke on macOS after a browser install"
# PLAYWRIGHT_BROWSERS_PATH=0 keeps the browsers inside the installed package.
LOCAL="$REPO/internal/web/app/node_modules/playwright-core/.local-browsers"
mkdir -p "$LOCAL/chromium-1"
before="$(runs)"
PLAYWRIGHT_BROWSERS_PATH=0 os Plan9 "$SMOKE"; PLAYWRIGHT_BROWSERS_PATH=0 os Plan9 "$SMOKE"
expect_ran "$((before + 1))" "web E2E smoke with in-package browsers"
mkdir "$LOCAL/chromium-2"
PLAYWRIGHT_BROWSERS_PATH=0 os Plan9 "$SMOKE"
expect_ran "$((before + 2))" "web E2E smoke after an in-package browser install"
PLAYWRIGHT_BROWSERS_PATH="$TMP/xdg/ms-playwright" os Plan9 "$SMOKE"
PLAYWRIGHT_BROWSERS_PATH="$TMP/xdg/ms-playwright" os Plan9 "$SMOKE"
expect_ran "$((before + 3))" "web E2E smoke with an explicit browser directory"

# No usable store means the step runs; reuse never turns into a failure.
before="$(runs)"
GATE_STEP_REUSE_DIR="$TMP/count/not-a-dir" step "go vet"
GATE_STEP_REUSE_DIR="$TMP/count/not-a-dir" step "go vet"
expect_ran "$((before + 2))" "unwritable store"

# The real index is never touched by the content-address computation.
git -C "$REPO" diff --cached --quiet || { echo "step reuse staged files in the real index" >&2; exit 1; }

echo "gate step reuse test: passed"
