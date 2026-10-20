#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ "${BEAMFALL_WEB_E2E_SMOKE_TEST_PROCESS_SUPERVISED:-0}" != 1 ]]; then
  exec python3 "$ROOT/script/test-process-supervisor.py" \
    BEAMFALL_WEB_E2E_SMOKE_TEST_PROCESS_SUPERVISED "$0" "$@"
fi
fixture="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-web-ui-e2e-smoke-test.XXXXXX")"
cleanup() {
  rm -rf "${fixture}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "${fixture}/bin" "${fixture}/script" "${fixture}/internal/web/app/e2e/lib"
cp "${ROOT}/script/web-ui-e2e-smoke.sh" "${fixture}/script/web-ui-e2e-smoke.sh"
cp "${ROOT}/script/test-process-supervisor.py" "${fixture}/script/test-process-supervisor.py"
cp \
  "${ROOT}/internal/web/app/e2e/lib/required-visual-parity-states.mjs" \
  "${ROOT}/internal/web/app/e2e/lib/validate-visual-parity-provenance.mjs" \
  "${fixture}/internal/web/app/e2e/lib/"

cat >"${fixture}/bin/npm" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'npm render=%s %s\n' "${BEAMFALL_WEB_E2E_RENDER_PHASE:-0}" "$*" >>"${SMOKE_TEST_CALLS}"
if [[ "${SMOKE_TEST_INTERRUPT:-0}" -eq 1 ]]; then
  trap '' INT TERM
  printf '%s\n' "$$" >"${SMOKE_TEST_LEADER_PID_FILE}"
  (
    trap '' INT TERM
    exec sleep 60
  ) &
  descendant_pid=$!
  printf '%s\n' "${descendant_pid}" >"${SMOKE_TEST_DESCENDANT_PID_FILE}"
  wait "${descendant_pid}"
fi
if [[ "${SMOKE_TEST_ORPHAN:-0}" -eq 1 ]]; then
  (
    trap '' INT TERM
    exec sleep 60
  ) &
  printf '%s\n' "$!" >"${SMOKE_TEST_ORPHAN_PID_FILE}"
  exit 0
fi
if [[ "${SMOKE_TEST_ADVANCE_HEAD:-0}" -eq 1 ]]; then
  git -C "${SMOKE_TEST_REPO}" commit -q --allow-empty -m advanced-during-smoke
fi
inventory_contract_ready=1
if [[ " $* " == *" --reporter list,./e2e/lib/require-visual-parity-states.mjs "* ]]; then
  if [[ "${BEAMFALL_WEB_E2E_RENDER_PHASE:-0}" -ne 1 ]]; then
    inventory_contract_ready=0
  fi
  while IFS= read -r project; do
    if [[ -n "${project}" && " $* " != *" --project ${project} "* ]]; then
      inventory_contract_ready=0
    fi
  done <"${SMOKE_TEST_REQUIRED_VISUAL_PROJECTS}"
fi
if [[ "${SMOKE_TEST_NPM_STATUS:-0}" -eq 0 && \
  "${SMOKE_TEST_PROVENANCE_MODE:-valid}" != absent && ${inventory_contract_ready} -eq 1 ]]; then
  mkdir -p "$(dirname "${BEAMFALL_WEB_E2E_SMOKE_PROVENANCE}")"
  if [[ "${SMOKE_TEST_PROVENANCE_MODE:-valid}" = minimal ]]; then
    printf '{"requiredCount":33,"passedCount":33}\n' >"${BEAMFALL_WEB_E2E_SMOKE_PROVENANCE}"
  else
    cp "${SMOKE_TEST_VALID_PROVENANCE}" "${BEAMFALL_WEB_E2E_SMOKE_PROVENANCE}"
  fi
fi
exit "${SMOKE_TEST_NPM_STATUS:-0}"
EOF

cat >"${fixture}/bin/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [[ "${SMOKE_TEST_PUBLICATION_HEAD_CHANGE:-0}" -eq 1 && $# -eq 4 && \
  "$1" = -C && "$2" = "${SMOKE_TEST_REPO}" && "$3" = rev-parse && "$4" = HEAD ]]; then
  count=0
  if [[ -s "${SMOKE_TEST_GIT_COUNTER}" ]]; then
    count="$(<"${SMOKE_TEST_GIT_COUNTER}")"
  fi
  count=$((count + 1))
  printf '%s\n' "${count}" >"${SMOKE_TEST_GIT_COUNTER}"
  if [[ ${count} -ge 4 ]]; then
    printf '%040d\n' 1
    exit 0
  fi
fi
exec "${SMOKE_TEST_REAL_GIT}" "$@"
EOF

cat >"${fixture}/bin/stubborn-process-tree" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
barrier="$1"
descendant_file="$2"
trap '' INT TERM
(
  trap '' INT TERM
  exec sleep 60
) &
descendant_pid=$!
printf '%s\n' "${descendant_pid}" >"${descendant_file}"
: >"${barrier}.fail"
wait "${descendant_pid}"
EOF

chmod +x "${fixture}/bin/npm" "${fixture}/bin/git" \
  "${fixture}/bin/stubborn-process-tree"

git init -q -b main "${fixture}"
git -C "${fixture}" config user.name "Web Smoke Test"
git -C "${fixture}" config user.email "web-smoke@example.invalid"
git -C "${fixture}" commit -q --allow-empty -m source
fixture_commit="$(git -C "${fixture}" rev-parse HEAD)"

export SMOKE_TEST_REAL_GIT="$(command -v git)"
export PATH="${fixture}/bin:${PATH}"
export SMOKE_TEST_CALLS="${fixture}/calls"
export BEAMFALL_WEB_E2E_OUTPUT_DIR="${fixture}/output"
export RELEASE_NIGHTLY_EVIDENCE_DIR="${fixture}/evidence"
export SMOKE_TEST_VALID_PROVENANCE="${fixture}/valid-provenance.json"
export SMOKE_TEST_REQUIRED_VISUAL_PROJECTS="${fixture}/required-visual-projects.txt"
receipt="${RELEASE_NIGHTLY_EVIDENCE_DIR}/web-ui-e2e-smoke.json"
node --input-type=module - \
  "${ROOT}/internal/web/app/e2e/lib/required-visual-parity-states.mjs" \
  "${SMOKE_TEST_VALID_PROVENANCE}" \
  "${SMOKE_TEST_REQUIRED_VISUAL_PROJECTS}" <<'NODE'
import { createHash } from "node:crypto";
import { writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const { REQUIRED_VISUAL_PARITY_STATE_ATTACHMENTS: required } = await import(
  pathToFileURL(process.argv[2]).href
);
const prefix = "visual-parity-state:";
const requiredProjects = [
  ...new Set(required.map((state) => state.slice(prefix.length).split("/", 1)[0])),
];
writeFileSync(
  process.argv[3],
  JSON.stringify({
    schemaVersion: 1,
    manifestSHA256: createHash("sha256").update(JSON.stringify(required)).digest("hex"),
    requiredCount: required.length,
    passedCount: required.length,
    states: required.map((id) => ({ id, status: "passed" })),
  }),
);
writeFileSync(process.argv[4], `${requiredProjects.join("\n")}\n`);
NODE
: >"${SMOKE_TEST_CALLS}"

if "${fixture}/script/web-ui-e2e-smoke.sh" e2e/player.spec.ts \
  >"${fixture}/stdout" 2>"${fixture}/stderr"; then
  echo "FAIL web-ui-e2e-smoke accepted caller narrowing" >&2
  exit 1
fi

if [[ -e "${receipt}" ]]; then
  echo "FAIL web-ui-e2e-smoke published receipt after caller narrowing" >&2
  exit 1
fi
if [[ -s "${SMOKE_TEST_CALLS}" ]]; then
  echo "FAIL web-ui-e2e-smoke ran commands after rejecting caller narrowing" >&2
  exit 1
fi

export SMOKE_TEST_NPM_STATUS=23
: >"${SMOKE_TEST_CALLS}"
if "${fixture}/script/web-ui-e2e-smoke.sh" >"${fixture}/stdout" 2>"${fixture}/stderr"; then
  echo "FAIL web-ui-e2e-smoke accepted incomplete inventory" >&2
  exit 1
fi
if [[ -e "${receipt}" ]]; then
  echo "FAIL web-ui-e2e-smoke published receipt after incomplete inventory" >&2
  exit 1
fi

export SMOKE_TEST_NPM_STATUS=0
export SMOKE_TEST_PROVENANCE_MODE=absent
mkdir -p "${fixture}/output"
printf '{"stale":true}\n' >"${fixture}/output/visual-parity-provenance.json"
: >"${SMOKE_TEST_CALLS}"
if "${fixture}/script/web-ui-e2e-smoke.sh" >"${fixture}/stdout" 2>"${fixture}/stderr"; then
  echo "FAIL web-ui-e2e-smoke accepted missing full-inventory provenance" >&2
  exit 1
fi
if [[ -e "${receipt}" ]]; then
  echo "FAIL web-ui-e2e-smoke published receipt without full-inventory provenance" >&2
  exit 1
fi
if [[ -e "${fixture}/output/visual-parity-provenance.json" ]]; then
  echo "FAIL web-ui-e2e-smoke retained stale full-inventory provenance" >&2
  exit 1
fi

export SMOKE_TEST_PROVENANCE_MODE=minimal
: >"${SMOKE_TEST_CALLS}"
if "${fixture}/script/web-ui-e2e-smoke.sh" >"${fixture}/stdout" 2>"${fixture}/stderr"; then
  echo "FAIL web-ui-e2e-smoke accepted count-only provenance" >&2
  exit 1
fi
if [[ -e "${receipt}" ]]; then
  echo "FAIL web-ui-e2e-smoke published count-only provenance receipt" >&2
  exit 1
fi

export SMOKE_TEST_PROVENANCE_MODE=valid
: >"${SMOKE_TEST_CALLS}"
"${fixture}/script/web-ui-e2e-smoke.sh" >"${fixture}/stdout" 2>"${fixture}/stderr"

# The inventory run is the unfiltered command: every Chromium project, the
# parity reporter, real execution. WEB-SMOKE-FINISH-PARITY-1 additionally runs
# a render phase (workers=1) and a functional phase, both --grep-filtered, so
# render-sensitive specs cannot be perturbed by finish-context scheduling.
# Those phases run ALONGSIDE the inventory and must never stand in for it, so
# assert the inventory's completeness on the unfiltered line specifically.
inventory="${fixture}/inventory-calls"
phases="${fixture}/phase-calls"
grep '^npm ' "${SMOKE_TEST_CALLS}" | grep -v -- '--grep' >"${inventory}" || true
grep '^npm ' "${SMOKE_TEST_CALLS}" | grep -- '--grep' >"${phases}" || true

if [[ "$(wc -l <"${inventory}" | tr -d ' ')" -ne 1 ]]; then
  echo "FAIL web-ui-e2e-smoke did not run exactly one unfiltered inventory command" >&2
  exit 1
fi
if ! grep -q -- '--reporter list,./e2e/lib/require-visual-parity-states.mjs' "${inventory}"; then
  echo "FAIL web-ui-e2e-smoke omitted the inventory reporter" >&2
  exit 1
fi
if ! grep -q '^npm render=1 ' "${inventory}"; then
  echo "FAIL web-ui-e2e-smoke inventory did not pin deterministic rendering" >&2
  exit 1
fi
if grep -q -- '--list' "${SMOKE_TEST_CALLS}"; then
  echo "FAIL web-ui-e2e-smoke substituted discovery for execution" >&2
  exit 1
fi
expected_projects=(
  unauth viewer player-desktop player-mobile manage console text-expansion a11y-browser auth-matrix
)
for project in "${expected_projects[@]}"; do
  if [[ "$(grep -oE -- "--project ${project}([[:space:]]|$)" "${inventory}" | wc -l | tr -d ' ')" -ne 1 ]]; then
    echo "FAIL web-ui-e2e-smoke omitted Chromium project ${project} from the inventory" >&2
    exit 1
  fi
done
while IFS= read -r project; do
  if [[ -n "${project}" && \
    "$(grep -oE -- "--project ${project}([[:space:]]|$)" "${inventory}" | wc -l | tr -d ' ')" -ne 1 ]]; then
    echo "FAIL web-ui-e2e-smoke omitted manifest project ${project} from the inventory" >&2
    exit 1
  fi
done <"${SMOKE_TEST_REQUIRED_VISUAL_PROJECTS}"
if [[ "$(grep -o -- '--project [^ ]*' "${inventory}" | wc -l | tr -d ' ')" -ne 9 ]]; then
  echo "FAIL web-ui-e2e-smoke inventory did not select exactly nine Chromium projects" >&2
  exit 1
fi
if grep -Eq -- '--project web-compat-|\.spec\.ts' "${SMOKE_TEST_CALLS}"; then
  echo "FAIL web-ui-e2e-smoke selected a non-Chromium or file-scoped inventory" >&2
  exit 1
fi
if [[ ! -s "${receipt}" ]]; then
  echo "FAIL web-ui-e2e-smoke did not publish receipt after inventory success" >&2
  exit 1
fi
if [[ "$(wc -l <"${phases}" | tr -d ' ')" -ne 2 ]]; then
  echo "FAIL web-ui-e2e-smoke did not run the render and functional phases" >&2
  exit 1
fi
if [[ "$(wc -l <"${SMOKE_TEST_CALLS}" | tr -d ' ')" -ne 3 ]]; then
  echo "FAIL web-ui-e2e-smoke ran unexpected success-path commands" >&2
  exit 1
fi
node --input-type=module - \
  "${fixture}/internal/web/app/e2e/lib/validate-visual-parity-provenance.mjs" \
  "${receipt}" \
  "${fixture_commit}" <<'NODE'
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const validator = await import(pathToFileURL(process.argv[2]).href);
const receipt = JSON.parse(readFileSync(process.argv[3], "utf8"));
assert.equal(receipt.commit, process.argv[4]);
assert.equal(receipt.sourceCommit, process.argv[4]);
assert.equal(receipt.repo, "beamfall");
assert.equal(receipt.status, "green");
assert.equal(receipt.tier, "web-ui-e2e-smoke");
validator.validateVisualParityProvenance(receipt.provenance);
NODE
if find "${RELEASE_NIGHTLY_EVIDENCE_DIR}" -name '*.tmp' -print -quit | grep -q .; then
  echo "FAIL web-ui-e2e-smoke left a partial receipt" >&2
  exit 1
fi

rm -f "${receipt}"
export SMOKE_TEST_ADVANCE_HEAD=1
export SMOKE_TEST_REPO="$(cd "${fixture}" && pwd)"
if "${fixture}/script/web-ui-e2e-smoke.sh" >"${fixture}/head-change.stdout" \
  2>"${fixture}/head-change.stderr"; then
  echo "FAIL web-ui-e2e-smoke published after its source HEAD changed" >&2
  exit 1
fi
grep -Fq 'FAIL web-ui-e2e-smoke source changed during run' "${fixture}/head-change.stderr"
if [[ -e "${receipt}" ]]; then
  echo "FAIL web-ui-e2e-smoke source change published freshness" >&2
  exit 1
fi
unset SMOKE_TEST_ADVANCE_HEAD

rm -f "${receipt}" "${fixture}/git-counter"
export SMOKE_TEST_PUBLICATION_HEAD_CHANGE=1
export SMOKE_TEST_GIT_COUNTER="${fixture}/git-counter"
if "${fixture}/script/web-ui-e2e-smoke.sh" >"${fixture}/publication-head-change.stdout" \
  2>"${fixture}/publication-head-change.stderr"; then
  echo "FAIL web-ui-e2e-smoke retained freshness after publication HEAD changed" >&2
  exit 1
fi
grep -Fq 'FAIL web-ui-e2e-smoke source changed at publication' \
  "${fixture}/publication-head-change.stderr"
if [[ -e "${receipt}" ]]; then
  echo "FAIL web-ui-e2e-smoke publication HEAD change retained freshness" >&2
  exit 1
fi
unset SMOKE_TEST_PUBLICATION_HEAD_CHANGE

rm -f "${receipt}"
set +e
BEAMFALL_WEB_E2E_SIGNAL_AFTER_REPLACE=1 \
  BEAMFALL_WEB_E2E_OUTPUT_DIR="${fixture}/output-promotion-signal" \
  "${fixture}/script/web-ui-e2e-smoke.sh" >"${fixture}/promotion-signal.stdout" \
  2>"${fixture}/promotion-signal.stderr"
promotion_signal_status=$?
set -e
if [[ ${promotion_signal_status} -ne 143 ]]; then
  echo "FAIL web-ui-e2e-smoke promotion-edge signal status expected=143 actual=${promotion_signal_status}" >&2
  exit 1
fi
if [[ -e "${receipt}" ]]; then
  echo "FAIL web-ui-e2e-smoke promotion-edge signal retained freshness" >&2
  exit 1
fi

rm -f "${receipt}"
publisher_barrier="${fixture}/publisher-ownership"
BEAMFALL_WEB_E2E_PUBLISH_BARRIER="${publisher_barrier}" \
  BEAMFALL_WEB_E2E_OUTPUT_DIR="${fixture}/output-publisher-a" \
  "${fixture}/script/web-ui-e2e-smoke.sh" >"${fixture}/publisher-a.stdout" \
  2>"${fixture}/publisher-a.stderr" &
publisher_a_pid=$!
for _ in $(seq 1 100); do
  [[ -s "${publisher_barrier}.ready" ]] && break
  sleep 0.05
done
if [[ ! -s "${publisher_barrier}.ready" ]]; then
  echo "FAIL web-ui-e2e-smoke first concurrent publisher did not start" >&2
  exit 1
fi
BEAMFALL_WEB_E2E_OUTPUT_DIR="${fixture}/output-publisher-b" \
  "${fixture}/script/web-ui-e2e-smoke.sh" >"${fixture}/publisher-b.stdout" \
  2>"${fixture}/publisher-b.stderr" &
publisher_b_pid=$!
sleep 0.1
kill -TERM "${publisher_a_pid}"
set +e
wait "${publisher_a_pid}"
publisher_a_status=$?
wait "${publisher_b_pid}"
publisher_b_status=$?
set -e
if [[ ${publisher_a_status} -ne 143 || ${publisher_b_status} -ne 0 ]]; then
  echo "FAIL web-ui-e2e-smoke concurrent publication statuses a=${publisher_a_status} b=${publisher_b_status}" >&2
  exit 1
fi
if [[ ! -s "${receipt}" ]]; then
  echo "FAIL web-ui-e2e-smoke interrupted publisher removed its successor receipt" >&2
  exit 1
fi
rm -f "${receipt}"

export SMOKE_TEST_ORPHAN=1
export SMOKE_TEST_ORPHAN_PID_FILE="${fixture}/orphan.pid"
if "${fixture}/script/web-ui-e2e-smoke.sh" >"${fixture}/orphan.stdout" \
  2>"${fixture}/orphan.stderr"; then
  echo "FAIL web-ui-e2e-smoke accepted a residual descendant" >&2
  exit 1
fi
orphan_pid="$(<"${SMOKE_TEST_ORPHAN_PID_FILE}")"
if kill -0 "${orphan_pid}" 2>/dev/null; then
  echo "FAIL web-ui-e2e-smoke normal exit left descendant pid=${orphan_pid}" >&2
  exit 1
fi
if [[ -e "${receipt}" ]]; then
  echo "FAIL web-ui-e2e-smoke residual descendant published freshness" >&2
  exit 1
fi
unset SMOKE_TEST_ORPHAN

export SMOKE_TEST_INTERRUPT=1
export SMOKE_TEST_LEADER_PID_FILE="${fixture}/startup-leader.pid"
export SMOKE_TEST_DESCENDANT_PID_FILE="${fixture}/startup-descendant.pid"
startup_barrier="${fixture}/supervisor-startup"
export BEAMFALL_PROCESS_SUPERVISOR_STARTUP_BARRIER="${startup_barrier}"
"${fixture}/script/web-ui-e2e-smoke.sh" >"${fixture}/startup.stdout" \
  2>"${fixture}/startup.stderr" &
runner_pid=$!
for _ in $(seq 1 100); do
  [[ -s "${startup_barrier}.ready" ]] && break
  sleep 0.05
done
if [[ ! -s "${startup_barrier}.ready" ]]; then
  echo "FAIL web-ui-e2e-smoke supervisor startup fixture did not start" >&2
  exit 1
fi
startup_child_pid="$(<"${startup_barrier}.ready")"
kill -TERM "${runner_pid}"
set +e
wait "${runner_pid}"
startup_status=$?
set -e
if [[ ${startup_status} -ne 143 ]]; then
  echo "FAIL web-ui-e2e-smoke startup interruption status expected=143 actual=${startup_status}" >&2
  exit 1
fi
if kill -0 "${startup_child_pid}" 2>/dev/null; then
  echo "FAIL web-ui-e2e-smoke startup interruption left child pid=${startup_child_pid}" >&2
  exit 1
fi
if [[ -e "${receipt}" ]]; then
  echo "FAIL web-ui-e2e-smoke startup interruption published freshness" >&2
  exit 1
fi
unset BEAMFALL_PROCESS_SUPERVISOR_STARTUP_BARRIER

failure_barrier="${fixture}/supervisor-failure"
failure_descendant_file="${fixture}/failure-descendant.pid"
set +e
BEAMFALL_PROCESS_SUPERVISOR_STARTUP_BARRIER="${failure_barrier}" \
  python3 "${fixture}/script/test-process-supervisor.py" \
  BEAMFALL_WEB_E2E_FAILURE_CHILD_PROCESS_SUPERVISED \
  "${fixture}/bin/stubborn-process-tree" "${failure_barrier}" "${failure_descendant_file}" \
  >"${fixture}/failure.stdout" 2>"${fixture}/failure.stderr"
failure_status=$?
set -e
if [[ ${failure_status} -eq 0 || ! -s "${failure_barrier}.ready" || \
  ! -s "${failure_descendant_file}" ]]; then
  echo "FAIL process-supervisor startup failure fixture did not fail after spawning its tree" >&2
  exit 1
fi
failure_leader_pid="$(<"${failure_barrier}.ready")"
failure_descendant_pid="$(<"${failure_descendant_file}")"
for pid in "${failure_leader_pid}" "${failure_descendant_pid}"; do
  if kill -0 "${pid}" 2>/dev/null; then
    echo "FAIL process-supervisor startup failure left descendant pid=${pid}" >&2
    exit 1
  fi
done

rm -f "${SMOKE_TEST_LEADER_PID_FILE}" "${SMOKE_TEST_DESCENDANT_PID_FILE}"
export SMOKE_TEST_INTERRUPT=1
export SMOKE_TEST_LEADER_PID_FILE="${fixture}/interrupt-leader.pid"
export SMOKE_TEST_DESCENDANT_PID_FILE="${fixture}/interrupt-descendant.pid"
"${fixture}/script/web-ui-e2e-smoke.sh" >"${fixture}/interrupt.stdout" \
  2>"${fixture}/interrupt.stderr" &
runner_pid=$!
for _ in $(seq 1 100); do
  [[ -s "${SMOKE_TEST_LEADER_PID_FILE}" && -s "${SMOKE_TEST_DESCENDANT_PID_FILE}" ]] && break
  sleep 0.05
done
if [[ ! -s "${SMOKE_TEST_LEADER_PID_FILE}" || ! -s "${SMOKE_TEST_DESCENDANT_PID_FILE}" ]]; then
  echo "FAIL web-ui-e2e-smoke interruption fixture did not start" >&2
  sed -n '1,80p' "${fixture}/interrupt.stderr" >&2
  exit 1
fi
leader_pid="$(<"${SMOKE_TEST_LEADER_PID_FILE}")"
descendant_pid="$(<"${SMOKE_TEST_DESCENDANT_PID_FILE}")"
kill -TERM "${runner_pid}"
set +e
wait "${runner_pid}"
interrupt_status=$?
set -e
if [[ ${interrupt_status} -ne 143 ]]; then
  echo "FAIL web-ui-e2e-smoke interruption status expected=143 actual=${interrupt_status}" >&2
  exit 1
fi
for pid in "${leader_pid}" "${descendant_pid}"; do
  if kill -0 "${pid}" 2>/dev/null; then
    echo "FAIL web-ui-e2e-smoke interruption left descendant pid=${pid}" >&2
    exit 1
  fi
done
if [[ -e "${receipt}" ]]; then
  echo "FAIL web-ui-e2e-smoke interruption published freshness" >&2
  exit 1
fi

echo "PASS web-ui-e2e-smoke rejects caller narrowing before freshness"
echo "PASS web-ui-e2e-smoke withholds freshness until the reporter passes"
echo "PASS web-ui-e2e-smoke refuses a changed source HEAD"
echo "PASS web-ui-e2e-smoke rolls back publication after a final HEAD change"
echo "PASS web-ui-e2e-smoke rolls back an atomic-promotion signal"
echo "PASS web-ui-e2e-smoke serializes concurrent receipt ownership"
echo "PASS web-ui-e2e-smoke normal exit reaps residual descendants"
echo "PASS web-ui-e2e-smoke startup interruption reaps its child"
echo "PASS process-supervisor startup failure reaps its child tree"
echo "PASS web-ui-e2e-smoke interruption reaps descendants"
