#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-crossrepo-pin-test.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT INT TERM

commit_all() {
  local repo="$1" message="$2"
  git -C "$repo" add .
  git -C "$repo" \
    -c commit.gpgsign=false \
    -c user.name='Beamfall Test' \
    -c user.email=test@beamfall.invalid \
    commit -qm "$message"
}

CONSUMER="$TMP/consumer"
mkdir -p "$CONSUMER/test/core-wire" "$CONSUMER/script" "$CONSUMER/vitest-fixture"
git -C "$CONSUMER" init -q
printf '%s\n' '// pin-resolution fixture' >"$CONSUMER/test/coreWireContract.test.ts"
printf '%s\n' \
  '{"schemaVersion":1,"artifact":"internal/conformance/wire-contract-v1.0.0.json","artifactVersion":"1.0.0"}' \
  >"$CONSUMER/test/core-wire/client-core-wire.generated.json"
printf '%s\n' '#!/usr/bin/env bash' 'echo "wire-drift-red: OK (fixture)"' \
  >"$CONSUMER/script/wire-drift-red.sh"
chmod +x "$CONSUMER/script/wire-drift-red.sh"
printf '%s\n' \
  '{"name":"consumer-fixture","version":"1.0.0","private":true,"devDependencies":{"vitest":"file:vitest-fixture"}}' \
  >"$CONSUMER/package.json"
printf '%s\n' \
  '{"name":"vitest","version":"0.0.0","bin":{"vitest":"vitest.sh"}}' \
  >"$CONSUMER/vitest-fixture/package.json"
printf '%s\n' '#!/usr/bin/env bash' 'echo "vitest fixture: OK"' \
  >"$CONSUMER/vitest-fixture/vitest.sh"
chmod +x "$CONSUMER/vitest-fixture/vitest.sh"
(
  cd "$CONSUMER"
  npm install --package-lock-only --ignore-scripts --no-audit --no-fund >/dev/null
)
commit_all "$CONSUMER" consumer
CONSUMER_SHA="$(git -C "$CONSUMER" rev-parse HEAD)"

RUNNER="$TMP/runner"
mkdir -p "$RUNNER/contracts" "$RUNNER/script" "$RUNNER/testing/release-gates"
git -C "$RUNNER" init -q
cp "$ROOT/script/crossrepo-conformance.sh" "$RUNNER/script/"
chmod +x "$RUNNER/script/crossrepo-conformance.sh"
printf '%s\n' \
  '{"schemaVersion":1,"boundaries":[{"boundary":"client-core-wire","consumers":[{"repo":"beamfall-web","pinnedVersion":"1.0.0"}]}]}' \
  >"$RUNNER/contracts/registry.json"
printf '%s\n' '# repo\tsource\tgate\tcontract_bearing\tpin' \
  >"$RUNNER/testing/release-gates/gate-commands.tsv"
commit_all "$RUNNER" pin-absent

CASE_STATUS=0
CASE_LOG=""
run_case() {
  local name="$1"
  local run_tmp="$TMP/run-$name"
  CASE_LOG="$TMP/$name.log"
  mkdir "$run_tmp"
  set +e
  BEAMFALL_CROSSREPO_RUNNER_SUPERVISED=1 \
    BEAMFALL_CROSSREPO_FIXTURE_SUPERVISED=1 \
    BEAMFALL_CROSSREPO_TMP="$run_tmp" \
    BEAMFALL_CROSSREPO_PRODUCER_SOURCE="$RUNNER" \
    BEAMFALL_CROSSREPO_CONSUMER_SOURCE="$CONSUMER" \
    "$RUNNER/script/crossrepo-conformance.sh" >"$CASE_LOG" 2>&1
  CASE_STATUS=$?
  set -e
}

fail_case() {
  cat "$CASE_LOG" >&2
  echo "crossrepo-conformance_pin_test: $1" >&2
  exit 1
}

run_case absent
[[ "$CASE_STATUS" -ne 0 ]] \
  || fail_case "missing committed consumer pin did not fail closed"
grep -F "committed consumer pin missing" "$CASE_LOG" >/dev/null \
  || fail_case "missing-pin diagnostic not found"

printf '%s\n' \
  '# repo\tsource\tgate\tcontract_bearing\tpin' \
  $'beamfall-web\t../beamfall-web\tmake gate\tyes\tffffffffffffffffffffffffffffffffffffffff' \
  >"$RUNNER/testing/release-gates/gate-commands.tsv"
commit_all "$RUNNER" pin-unresolvable
run_case unresolvable
[[ "$CASE_STATUS" -ne 0 ]] \
  || fail_case "unresolvable committed consumer pin did not fail closed"
grep -F "consumer pin unresolvable" "$CASE_LOG" >/dev/null \
  || fail_case "unresolvable-pin diagnostic not found"

printf '%s\n' \
  '# repo\tsource\tgate\tcontract_bearing\tpin' \
  $'beamfall-web\t../beamfall-web\tmake gate\tyes\t'"$CONSUMER_SHA" \
  >"$RUNNER/testing/release-gates/gate-commands.tsv"
commit_all "$RUNNER" pin-resolvable
run_case resolvable
[[ "$CASE_STATUS" -eq 0 ]] || fail_case "resolvable committed consumer pin failed"
grep -F \
  "RESOLVE consumer repo=beamfall-web pinned_sha=$CONSUMER_SHA resolved_sha=$CONSUMER_SHA note=branch-main" \
  "$CASE_LOG" >/dev/null \
  || fail_case "resolved consumer SHA was not reported"

echo "crossrepo-conformance_pin_test: OK (missing/unresolvable RED; exact pin resolved $CONSUMER_SHA)"
