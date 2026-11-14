#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(cd "$(mktemp -d "${TMPDIR:-/tmp}/beamfall-crossrepo-pin-test.XXXXXX")" && pwd -P)"
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
  '{"schemaVersion":1,"boundaries":[{"boundary":"client-core-wire","consumers":["beamfall-web"]}]}' \
  >"$RUNNER/contracts/registry.json"
printf '%s\n' '# repo\tsource\tgate\tcontract_bearing\tpin' \
  >"$RUNNER/testing/release-gates/gate-commands.tsv"
commit_all "$RUNNER" pin-absent

# Consumer pins live in the private workspace registry, not core's (CORE-0902).
WORKSPACE="$TMP/workspace"
mkdir -p "$WORKSPACE/contracts"
printf '%s\n' \
  '{"schemaVersion":1,"boundaries":[{"id":"client-core-wire","consumers":[{"repo":"beamfall-web","pinnedVersion":"1.0.0"}]}]}' \
  >"$WORKSPACE/contracts/registry.json"
CASE_WORKSPACE="$WORKSPACE"

CASE_STATUS=0
CASE_LOG=""
CASE_CONSUMER_SOURCE="$CONSUMER"
DEFAULT_CONSUMER_URL="https://github.com/beamfall/web.git"
run_case() {
  local name="$1"
  local run_tmp="$TMP/run-$name"
  CASE_LOG="$TMP/$name.log"
  mkdir "$run_tmp"
  local workspace_env=(-u BEAMFALL_WORKSPACE_ROOT)
  [[ -z "$CASE_WORKSPACE" ]] || workspace_env+=("BEAMFALL_WORKSPACE_ROOT=$CASE_WORKSPACE")
  # With no explicit source, rewrite the default remote to the local fixture so
  # the fallback is observable without the network.
  local source_env=(-u BEAMFALL_CROSSREPO_CONSUMER_SOURCE
    GIT_CONFIG_COUNT=1 "GIT_CONFIG_KEY_0=url.$CONSUMER.insteadOf" "GIT_CONFIG_VALUE_0=$DEFAULT_CONSUMER_URL")
  [[ -z "$CASE_CONSUMER_SOURCE" ]] || source_env=("BEAMFALL_CROSSREPO_CONSUMER_SOURCE=$CASE_CONSUMER_SOURCE")
  set +e
  env "${workspace_env[@]}" "${source_env[@]}" \
    BEAMFALL_CROSSREPO_RUNNER_SUPERVISED=1 \
    BEAMFALL_CROSSREPO_FIXTURE_SUPERVISED=1 \
    BEAMFALL_CROSSREPO_TMP="$run_tmp" \
    BEAMFALL_CROSSREPO_PRODUCER_SOURCE="$RUNNER" \
    "$RUNNER/script/crossrepo-conformance.sh" >"$CASE_LOG" 2>&1
  CASE_STATUS=$?
  set -e
}

fail_case() {
  cat "$CASE_LOG" >&2
  echo "crossrepo-conformance_pin_test: $1" >&2
  exit 1
}

CASE_WORKSPACE=""
run_case no-workspace
[[ "$CASE_STATUS" -ne 0 ]] \
  || fail_case "missing workspace registry did not fail closed"
grep -F "workspace contract registry unresolved: set BEAMFALL_WORKSPACE_ROOT" "$CASE_LOG" >/dev/null \
  || fail_case "missing-workspace diagnostic not found"

CASE_WORKSPACE="$TMP"
run_case workspace-without-registry
[[ "$CASE_STATUS" -ne 0 ]] \
  || fail_case "workspace root without a registry did not fail closed"
grep -F "workspace contract registry missing: $TMP/contracts/registry.json" "$CASE_LOG" >/dev/null \
  || fail_case "missing-registry diagnostic not found"

CASE_WORKSPACE="$WORKSPACE"
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

# corvint-tasks `gate run` strips BEAMFALL_WORKSPACE_ROOT and may run from a
# linked worktree outside the workspace; the registry still resolves through
# the main checkout's git common dir (CORE-1473).
mkdir -p "$WORKSPACE/beamfall"
printf '%s\n' '{"schemaVersion":1}' >"$WORKSPACE/workspace-repos.json"
git clone -q "$RUNNER" "$WORKSPACE/beamfall/core"
git -C "$WORKSPACE/beamfall/core" worktree add -q --detach "$TMP/outside/core-wt"
RUNNER="$TMP/outside/core-wt"
CASE_WORKSPACE=""
run_case linked-worktree
[[ "$CASE_STATUS" -eq 0 ]] || fail_case "linked worktree outside the workspace failed"
grep -F "REGISTRY workspace path=$WORKSPACE/contracts/registry.json" "$CASE_LOG" >/dev/null \
  || fail_case "linked worktree did not resolve the workspace registry via the git common dir"

# A workspace sibling holding the pin is cloned locally instead of the remote;
# one without it falls back to the default remote; an explicit source wins
# (CORE-1550).
CASE_CONSUMER_SOURCE=""
git init -q "$WORKSPACE/beamfall-web"
run_case sibling-without-pin
[[ "$CASE_STATUS" -eq 0 ]] || fail_case "sibling without the pin did not fall back to the default remote"
grep -F "CHECKOUT consumer sha=$CONSUMER_SHA source=$DEFAULT_CONSUMER_URL" "$CASE_LOG" >/dev/null \
  || fail_case "sibling without the pin was used as the consumer source"

rm -rf "$WORKSPACE/beamfall-web"
git clone -q "$CONSUMER" "$WORKSPACE/beamfall-web"
run_case sibling-with-pin
[[ "$CASE_STATUS" -eq 0 ]] || fail_case "sibling holding the pin failed"
grep -F "CHECKOUT consumer sha=$CONSUMER_SHA source=$WORKSPACE/beamfall-web" "$CASE_LOG" >/dev/null \
  || fail_case "sibling holding the pin was not used as the consumer source"

CASE_CONSUMER_SOURCE="$CONSUMER"
run_case explicit-source
[[ "$CASE_STATUS" -eq 0 ]] || fail_case "explicit consumer source failed"
grep -F "CHECKOUT consumer sha=$CONSUMER_SHA source=$CONSUMER" "$CASE_LOG" >/dev/null \
  || fail_case "explicit consumer source did not win over the workspace sibling"

echo "crossrepo-conformance_pin_test: OK (no-workspace/missing-registry/missing/unresolvable RED; exact pin resolved $CONSUMER_SHA against the workspace registry, also from a linked worktree outside it; workspace sibling used only when it holds the pin)"
