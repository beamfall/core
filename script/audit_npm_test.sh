#!/usr/bin/env bash
# audit_npm_test.sh — baseline (planted-red) tests for script/audit-npm.sh.
#
# Drives audit-npm.sh through its test seams with synthetic audit JSON and
# baseline waiver files, asserting it PASSES on a clean tree / valid waiver and
# FAILS CLOSED on: missing npm, no/error audit output, a NEW unwaived advisory,
# a fixable advisory, an EXPIRED waiver, and a STALE waiver.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SCRIPT="$HERE/audit-npm.sh"
WORK="$(mktemp -d)"
trap 'rm -f /tmp/audit_npm_test.out; rm -rf "$WORK"' EXIT
TODAY="2026-07-20"
FAILS=0

# fixtures -------------------------------------------------------------------
clean_json="$WORK/clean.json";   printf '%s' '{"vulnerabilities":{}}' > "$clean_json"
empty_json="$WORK/empty.json";   printf '%s' '' > "$empty_json"
error_json="$WORK/error.json";   printf '%s' '{"error":{"code":"EAUDITNOLOCK","summary":"no lockfile"}}' > "$error_json"
# One high advisory, no fix available, source id 900001.
nofix_json="$WORK/nofix.json";   printf '%s' '{"vulnerabilities":{"pkgx":{"severity":"high","fixAvailable":false,"via":[{"source":900001,"name":"pkgx"}]}}}' > "$nofix_json"
# Same advisory but a fix IS available.
fix_json="$WORK/fix.json";       printf '%s' '{"vulnerabilities":{"pkgx":{"severity":"high","fixAvailable":{"name":"pkgx","version":"2.0.0"},"via":[{"source":900001,"name":"pkgx"}]}}}' > "$fix_json"

# baselines ------------------------------------------------------------------
empty_base="$WORK/base-empty";   printf '%s' '{"waivers":[]}' > "$empty_base"
valid_base="$WORK/base-valid";   printf '%s' '{"waivers":[{"id":900001,"rationale":"upstream fix pending","added":"2026-07-01","expires":"2026-12-31"}]}' > "$valid_base"
expired_base="$WORK/base-exp";   printf '%s' '{"waivers":[{"id":900001,"rationale":"stale waiver","added":"2026-01-01","expires":"2026-06-30"}]}' > "$expired_base"
# Waiver for an advisory that is NOT present in the clean audit (stale).
stale_base="$WORK/base-stale";   printf '%s' '{"waivers":[{"id":900001,"rationale":"orphan","added":"2026-07-01","expires":"2026-12-31"}]}' > "$stale_base"

# harness --------------------------------------------------------------------
run() { # run <expect: pass|fail> <name> <needle> <env-assignments...>
  local expect="$1" name="$2" needle="$3"; shift 3
  local rc=0
  env AUDIT_NPM_TODAY="$TODAY" "$@" bash "$SCRIPT" >/tmp/audit_npm_test.out 2>&1 || rc=$?
  local ok=1
  if [ "$expect" = pass ] && [ "$rc" -ne 0 ]; then ok=0; fi
  if [ "$expect" = fail ] && [ "$rc" -eq 0 ]; then ok=0; fi
  if [ -n "$needle" ] && ! grep -q -- "$needle" /tmp/audit_npm_test.out; then ok=0; fi
  if [ "$ok" -eq 1 ]; then
    echo "ok   - $name (exit=$rc)"
  else
    echo "FAIL - $name (expected=$expect exit=$rc, needle='$needle')"
    sed 's/^/       | /' /tmp/audit_npm_test.out
    FAILS=$((FAILS + 1))
  fi
}

run pass "clean tree passes"              "0 unresolved"       AUDIT_NPM_JSON="$clean_json" AUDIT_NPM_BASELINE="$empty_base"
run pass "valid bounded waiver passes"    "0 unresolved"       AUDIT_NPM_JSON="$nofix_json" AUDIT_NPM_BASELINE="$valid_base"
run fail "missing npm fails closed"       "npm is not available" AUDIT_NPM_NO_NPM=1 AUDIT_NPM_BASELINE="$empty_base"
run fail "empty audit output fails"       "no output"          AUDIT_NPM_JSON="$empty_json" AUDIT_NPM_BASELINE="$empty_base"
run fail "npm audit error fails"          "reported an error"  AUDIT_NPM_JSON="$error_json" AUDIT_NPM_BASELINE="$empty_base"
run fail "new unwaived advisory fails"    "NEW"                AUDIT_NPM_JSON="$nofix_json" AUDIT_NPM_BASELINE="$empty_base"
run fail "fixable advisory fails"         "fix available"      AUDIT_NPM_JSON="$fix_json"   AUDIT_NPM_BASELINE="$valid_base"
run fail "expired waiver fails"           "EXPIRED"            AUDIT_NPM_JSON="$nofix_json" AUDIT_NPM_BASELINE="$expired_base"
run fail "stale waiver fails"             "STALE"              AUDIT_NPM_JSON="$clean_json" AUDIT_NPM_BASELINE="$stale_base"

if [ "$FAILS" -ne 0 ]; then
  echo "audit_npm_test: $FAILS case(s) failed" >&2
  exit 1
fi
echo "audit_npm_test: all cases passed"
