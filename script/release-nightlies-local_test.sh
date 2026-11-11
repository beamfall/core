#!/usr/bin/env bash
# Tests for script/release-nightlies-local.sh (CORE-0539). Every tier command is
# a fake in a throwaway repository: the runner and the real
# script/nightly-freshness.sh are copied into it, and fake Makefile targets and
# scripts stand in for the six tier commands.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/release-nightlies-local-test.XXXXXX")"
# bash 3.2 (macOS) runs the EXIT trap with status 0 after a `set -u` expansion
# error, and the script then exits 0; only reaching the last line passes.
completed=0
trap 'rm -rf "$tmp"; [ "$completed" = 1 ] || exit 1' EXIT
unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GITHUB_ACTIONS GITHUB_REPOSITORY \
  NIGHTLY_FRESHNESS_SOURCE_REPO NIGHTLY_FRESHNESS_TIERS NIGHTLY_FRESHNESS_MAX_HOURS \
  RELEASE_NIGHTLY_EVIDENCE_DIR AGENT_EVIDENCE_DIR MUTATION_EVIDENCE_DIR

fake="$tmp/root"
mkdir -p "$fake/script"
cp "$ROOT/script/release-nightlies-local.sh" "$ROOT/script/nightly-freshness.sh" "$fake/script/"
printf '.agent-evidence/\n' >"$fake/.gitignore"

# Each fake records its invocation, then fails with exit 3 when its tier is
# named in FAKE_FAIL.
cat >"$fake/script/fake-tier.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
tier="$1"
shift
printf '%s %s\n' "$tier" "$*" >>"$FAKE_CALLS"
case " ${FAKE_FAIL:-} " in *" $tier "*) echo "fake $tier failing"; exit 3 ;; esac
if [ "$tier" = fuzz-nightly ] && [ "${FAKE_MOVE_HEAD:-0}" = 1 ]; then
  git commit -q --allow-empty -m moved
fi
echo "fake $tier passed"
SH
cat >"$fake/Makefile" <<'MAKE'
fuzz-active:
	@script/fake-tier.sh fuzz-nightly
ui-catalog:
	@script/fake-tier.sh ui-catalog-nightly
vuln-check:
	@script/fake-tier.sh vuln-nightly
MAKE
cat >"$fake/script/mutation-test.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
[ "$*" = named ] || { echo "unexpected mutation args: $*" >&2; exit 2; }
script/fake-tier.sh mutation-nightly "$@"
mkdir -p "$MUTATION_EVIDENCE_DIR"
printf '{"status": "%s"}\n' "${FAKE_MUTATION_STATUS:-COMPLETE}" >"$MUTATION_EVIDENCE_DIR/status.json"
SH
cat >"$fake/script/race-test.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
[ "$*" = full ] || { echo "unexpected race args: $*" >&2; exit 2; }
exec script/fake-tier.sh race-nightly "$@"
SH
# Stands in for the owning script: publishes a receipt with the provenance shape
# `check` validates, unless FAKE_WEB_COMPAT=silent.
cat >"$fake/script/web-compat-nightly.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script/fake-tier.sh web-compat-nightly
[ "${FAKE_WEB_COMPAT:-publish}" = publish ] || exit 0
evidence_dir="${RELEASE_NIGHTLY_EVIDENCE_DIR:-$ROOT/.agent-evidence/nightly-last-green}"
mkdir -p "$evidence_dir"
python3 - "$evidence_dir/web-compat-nightly.json" "$(git -C "$ROOT" rev-parse HEAD)" <<'PY'
import datetime as dt
import json
import sys

path, commit = sys.argv[1:]
json.dump(
    {
        "commit": commit,
        "sourceCommit": commit,
        "repo": "beamfall",
        "status": "green",
        "tier": "web-compat-nightly",
        "timestamp": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "provenance": {
            "host": {"os": "linux", "release": "test", "arch": "x86_64"},
            "playwrightVersion": "1.60.0",
            "source": "package-lock.json + playwright-core/browsers.json",
            "browsers": [
                {"name": "firefox", "revision": "1", "browserVersion": "1"},
                {"name": "webkit", "revision": "2", "browserVersion": "2"},
            ],
            "executedTestCount": 4,
        },
    },
    open(path, "w", encoding="utf-8"),
)
PY
SH
chmod +x "$fake/script/"*.sh
git init -q -b main "$fake"
git -C "$fake" config user.name "Release Nightlies Test"
git -C "$fake" config user.email "release-nightlies@example.invalid"
git -C "$fake" add -A
git -C "$fake" commit -q -m fake-root

evidence="$fake/.agent-evidence/nightly-last-green"
tiers=(fuzz-nightly mutation-nightly race-nightly ui-catalog-nightly vuln-nightly web-compat-nightly)

# run_runner LABEL [VAR=VALUE ...] [-- RUNNER-ARGS ...]
run_runner() {
  local label="$1" status=0 envs=()
  shift
  while [ $# -gt 0 ] && [ "$1" != -- ]; do
    envs+=("$1")
    shift
  done
  [ $# -eq 0 ] || shift
  : >"$tmp/calls"
  # ${envs[@]+...}: bash 3.2 treats an empty array as unset under `set -u`.
  env FAKE_CALLS="$tmp/calls" ${envs[@]+"${envs[@]}"} "$fake/script/release-nightlies-local.sh" "$@" \
    >"$tmp/$label.out" 2>&1 || status=$?
  cp "$tmp/$label.out" "$tmp/current.out"
  return "$status"
}

fail() {
  echo "FAIL $*" >&2
  [ ! -f "$tmp/current.out" ] || cat "$tmp/current.out" >&2
  exit 1
}

# field TIER PYTHON-EXPR — evaluates EXPR against the tier record as `d`.
field() {
  python3 - "$evidence/$1.json" "$2" <<'PY'
import json
import sys

d = json.load(open(sys.argv[1], encoding="utf-8"))
print(eval(sys.argv[2], {"d": d}))
PY
}

check_all() {
  local args=() tier
  for tier in "${tiers[@]}"; do
    args+=(--tier "$tier")
  done
  env NIGHTLY_FRESHNESS_SOURCE_REPO="$fake" RELEASE_NIGHTLY_EVIDENCE_DIR="$evidence" \
    "$fake/script/nightly-freshness.sh" check "${args[@]}"
}

check_passes() { grep -c '^PASS .* age_hours=' "$1" || true; }

head="$(git -C "$fake" rev-parse HEAD)"

# 1. All six tiers pass: every record is green for HEAD and check exits 0.
run_runner all-green || fail "all-green run exited non-zero"
[ "$(check_passes "$tmp/all-green.out")" -eq 6 ] || fail "runner's closing check did not pass six tiers"
check_all >"$tmp/check.out" 2>&1 || { cat "$tmp/check.out" >&2; fail "check failed after a passing run"; }
[ "$(check_passes "$tmp/check.out")" -eq 6 ] || fail "check did not pass all six tiers"
for tier in "${tiers[@]}"; do
  grep -q "^$tier" "$tmp/calls" || fail "$tier command did not run"
  [ "$(field "$tier" 'd["status"]')" = green ] || fail "$tier not green"
  [ "$(field "$tier" 'd["sourceCommit"]')" = "$head" ] || fail "$tier not bound to HEAD"
done
grep -qx 'mutation-nightly named' "$tmp/calls" || fail "mutation tier not run as 'named'"
grep -qx 'race-nightly full' "$tmp/calls" || fail "race tier not run as 'full'"
[ "$(field fuzz-nightly 'd["provenance"]["command"]')" = "make fuzz-active FUZZ_TIME=5m" ] || fail "fuzz provenance command"
[ "$(field fuzz-nightly 'd["provenance"]["exitStatus"]')" = 0 ] || fail "fuzz provenance exit status"
[ "$(field mutation-nightly 'd["provenance"]["mutationStatus"]')" = COMPLETE ] || fail "mutation provenance status"
# web-compat-nightly keeps the owning script's receipt, not one from the runner.
[ "$(field web-compat-nightly 'd["provenance"]["executedTestCount"]')" = 4 ] || fail "web-compat receipt replaced"
[ "$(field web-compat-nightly '"producer" in d["provenance"]')" = False ] || fail "runner wrote web-compat green"

# 2. A tier that fails locally is recorded red over its earlier green, never green.
if run_runner race-red FAKE_FAIL=race-nightly; then fail "runner passed with a failing tier"; fi
[ "$(field race-nightly 'd["status"]')" = red ] || fail "failed race tier not recorded red"
[ "$(field race-nightly 'd["provenance"]["exitStatus"]')" = 3 ] || fail "red provenance lacks exit status"
[ "$(field race-nightly 'd["provenance"]["command"]')" = "script/race-test.sh full" ] || fail "red provenance command"
[ "$(field fuzz-nightly 'd["status"]')" = green ] || fail "passing tier not green alongside a red one"
grep -q '^FAIL race-nightly status=red ' "$tmp/race-red.out" || fail "check did not report race red"
if check_all >/dev/null 2>&1; then fail "check passed with a red tier"; fi

# 3. Every tier failing: all six are red, including web-compat-nightly.
if run_runner all-red FAKE_FAIL="${tiers[*]}"; then fail "runner passed with all tiers failing"; fi
for tier in "${tiers[@]}"; do
  [ "$(field "$tier" 'd["status"]')" = red ] || fail "$tier not recorded red after failing"
done

# 4. A time-boxed mutation run exits 0 but is not COMPLETE: recorded red.
if run_runner mutation-timeboxed FAKE_MUTATION_STATUS=TIMEBOXED; then
  fail "runner passed with a time-boxed mutation run"
fi
[ "$(field mutation-nightly 'd["status"]')" = red ] || fail "time-boxed mutation not red"
[ "$(field mutation-nightly 'd["provenance"]["mutationStatus"]')" = TIMEBOXED ] || fail "mutation status provenance"

# 5. web-compat exiting 0 without publishing is never turned into a green, even
#    with an earlier green receipt for the same commit in place.
run_runner all-green-again || fail "second passing run failed"
[ "$(field web-compat-nightly 'd["status"]')" = green ] || fail "web-compat not green before silent run"
sleep 1 # the silent run must start after the earlier receipt's timestamp
if run_runner web-compat-silent FAKE_WEB_COMPAT=silent; then
  fail "runner passed when web-compat published nothing"
fi
[ "$(field web-compat-nightly 'd["status"]')" = red ] || fail "silent web-compat not recorded red"
grep -q 'without publishing' "$tmp/web-compat-silent.out" || fail "silent web-compat reason missing"

# 6. --tier reruns one tier; the closing check still covers all six.
run_runner rerun -- --tier web-compat-nightly || fail "single-tier rerun failed"
[ "$(wc -l <"$tmp/calls")" -eq 1 ] || fail "single-tier rerun ran other tiers"
[ "$(check_passes "$tmp/rerun.out")" -eq 6 ] || fail "closing check did not cover six tiers"
if run_runner bad-tier -- --tier not-a-tier; then fail "unknown tier accepted"; fi
[ ! -s "$tmp/calls" ] || fail "a tier ran after an unknown tier was named"

# 7. A dirty worktree is refused before any tier runs.
rm -rf "$fake/.agent-evidence"
touch "$fake/untracked-change"
if run_runner dirty; then fail "dirty worktree accepted"; fi
grep -q 'uncommitted changes' "$tmp/dirty.out" || fail "dirty refusal message missing"
[ ! -s "$tmp/calls" ] || fail "a tier ran on a dirty worktree"
[ ! -d "$evidence" ] || fail "records written on a dirty worktree"
rm "$fake/untracked-change"

# 8. HEAD moving during a tier: nothing recorded for it, no later tier runs.
if run_runner head-moved FAKE_MOVE_HEAD=1; then fail "runner passed after HEAD moved"; fi
grep -q 'HEAD moved during fuzz-nightly' "$tmp/head-moved.out" || fail "HEAD-move message missing"
[ ! -e "$evidence/fuzz-nightly.json" ] || fail "record written after HEAD moved"
[ "$(wc -l <"$tmp/calls")" -eq 1 ] || fail "tiers kept running after HEAD moved"

# 9. A relative evidence directory is resolved once, against the caller's
# directory, so every tier's record lands in the same place.
mkdir -p "$tmp/relcwd"
(cd "$tmp/relcwd" && run_runner relative-dir RELEASE_NIGHTLY_EVIDENCE_DIR=rel-evidence) ||
  fail "relative-dir run exited non-zero"
for tier in "${tiers[@]}"; do
  [ -f "$tmp/relcwd/rel-evidence/$tier.json" ] || fail "relative evidence dir: no $tier record in the caller's directory"
done

completed=1
echo "release-nightlies-local tests: passed"
