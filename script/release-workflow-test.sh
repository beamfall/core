#!/usr/bin/env bash
# Release workflow contract (RELGATE-NONVAC-1/2). Publication must be gated on the
# exact candidate SHA, and the evidence packet must be opened rather than trusted.
#
# Every assertion here is mutation-tested against a sed-edited copy of the real
# workflow: an assertion nobody can break is not an assertion. Bash + grep/sed
# only — `make gate` and the release runner install the declared Go and Node
# toolchains, and this check must not depend on incidental runner packages.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE="$ROOT/.github/workflows/release.yml"
GATE="$ROOT/.github/workflows/release-gate.yml"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/release-workflow-test.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

fail() { echo "release-workflow_test: $*" >&2; exit 1; }

# assert <file> <description> <grep-args...>
assert() {
  local file="$1" what="$2"; shift 2
  grep -q "$@" "$file" || fail "$what"
}

# The `image` job publishes and cosign-signs the OCI image — the trust anchor the
# unattended-upgrade path verifies. It must not start before the exact-SHA
# release-gate resolution in `attest` has succeeded.
image_needs_attest() {
  awk '/^  image:/{f=1} f && /^    needs: attest$/{print; exit}' "$1" | grep -q 'needs: attest'
}

# The gate run must be resolved for THIS commit, not merely for a green run.
sha_filter_present() {
  grep -q 'head_sha=\${COMMIT}' "$1" && grep -q '\.head_sha == \$commit' "$1"
}

# The packet must be opened and bound to this SHA before anything is published.
verify_before_publish() {
  local verify publish
  verify="$(grep -n 'release-gate-evidence.sh verify' "$1" | head -n1 | cut -d: -f1)"
  publish="$(grep -n 'name: publish release artifacts' "$1" | head -n1 | cut -d: -f1)"
  [ -n "$verify" ] && [ -n "$publish" ] && [ "$verify" -lt "$publish" ]
}

expect_commit_bound() {
  grep -q 'verify /tmp/release-gate-signed-input --expect-commit' "$1"
}

require_artifacts_before_aggregate() {
  local require aggregate
  require="$(grep -n 'release-gate-evidence.sh require-artifacts' "$1" | head -n1 | cut -d: -f1)"
  aggregate="$(grep -n 'aggregate-result.json' "$1" | head -n1 | cut -d: -f1)"
  [ -n "$require" ] && [ -n "$aggregate" ] && [ "$require" -lt "$aggregate" ]
}

canary_release_mode() {
  awk '/^  clean-checkout-canary:/{f=1} /^  [a-z-]+:$/ && !/clean-checkout-canary/{if(f)exit} f' "$1" \
    | grep -q 'GATE_MODE: release'
}

gates_release_mode() {
  awk '/name: Run release test gates/{f=1} f && /^  [a-z-]+:$/{exit} f' "$1" \
    | grep -q 'GATE_MODE: release'
}

# mutate <src> <sed-expression> -> path to the mutated copy
mutate() {
  local out="$tmp/mutant.yml"
  sed "$2" "$1" > "$out"
  cmp -s "$1" "$out" && fail "mutation '$2' changed nothing; the control is vacuous"
  printf '%s' "$out"
}

# proves <checker> <src> <sed-expression> <description>
proves() {
  local checker="$1" src="$2" expr="$3" what="$4" mutant
  "$checker" "$src" || fail "$what"
  mutant="$(mutate "$src" "$expr")"
  ! "$checker" "$mutant" || fail "$what — the check survives its own mutation, so it proves nothing"
}

proves image_needs_attest "$RELEASE" '/^    needs: attest$/d' \
  "the image job must declare needs: attest"
proves sha_filter_present "$RELEASE" 's/head_sha=\${COMMIT}/per_page=1/' \
  "the release-gate run must be resolved by this commit's head_sha"
proves verify_before_publish "$RELEASE" '/release-gate-evidence.sh verify/d' \
  "release evidence must be verified before artifacts are published"
proves expect_commit_bound "$RELEASE" 's/--expect-commit/--expect-nothing/' \
  "evidence verification must bind the candidate commit"
proves require_artifacts_before_aggregate "$GATE" '/release-gate-evidence.sh require-artifacts/d' \
  "the aggregate must require every evidence artifact before recording PASS"
# Each mutation is scoped to one job, so a passing control also proves the two
# job-local checks are not reading each other's env block.
canary_mutation='/BEAMFALL_WORKSPACE/,/GATE_MODE: release/ s/GATE_MODE: release/GATE_MODE: normal/'
gates_mutation='/BEAMFALL_SKIP_PLAYWRIGHT_INSTALL/,/GATE_MODE: release/ s/GATE_MODE: release/GATE_MODE: normal/'
proves canary_release_mode "$GATE" "$canary_mutation" \
  "the clean-checkout canary must run in release mode or its pins are unenforced"
gates_release_mode "$(mutate "$GATE" "$canary_mutation")" \
  || fail "the canary and release-gates checks are reading the same env block"
proves gates_release_mode "$GATE" "$gates_mutation" \
  "release test gates must run in release mode or mandatory lanes can skip"
canary_release_mode "$(mutate "$GATE" "$gates_mutation")" \
  || fail "the release-gates and canary checks are reading the same env block"

# Signing integrity is not this ticket's to relax: the fail-closed key check and
# the tamper-rejection step must still be present.
assert "$RELEASE" "the release must still fail closed without a signing key" \
  -F 'BEAMFALL_RELEASE_SIGNING_KEY secret is not set'
assert "$RELEASE" "the tampered-artifact rejection must still run" \
  -F 'FAIL: release verify accepted a tampered artifact'
assert "$RELEASE" "release permissions must stay least-privilege" \
  -F 'contents: write'

echo "release-workflow tests: passed"
