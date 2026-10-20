#!/usr/bin/env bash
# check-quarantine_test.sh — unit tests for script/check-quarantine.sh.
#
# Tests the two acceptance criteria from TEST-XCUT-3:
#   1. A deadline-passed fixture entry reds the gate.
#   2. A no-expiresAt (unbounded) fixture entry reds the gate.
#   3. A valid future entry passes.
#   4. An empty manifest passes.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECKER="$ROOT/script/check-quarantine.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

PASS=0
FAIL=0

assert_exit() {
  local name="$1" want_exit="$2" manifest="$3"
  local actual_exit=0
  "$CHECKER" --manifest "$manifest" >/dev/null 2>&1 || actual_exit=$?
  if [[ "$actual_exit" -eq "$want_exit" ]]; then
    echo "PASS $name"
    ((PASS++)) || true
  else
    echo "FAIL $name: expected exit=$want_exit got exit=$actual_exit" >&2
    ((FAIL++)) || true
  fi
}

# --- fixture: empty manifest (no entries) ---
cat > "$TMP/empty.yaml" << 'EOF'
version: 1
maxEntries: 5

entries:
EOF
assert_exit "empty manifest exits 0" 0 "$TMP/empty.yaml"

# --- fixture: valid future entry ---
cat > "$TMP/valid.yaml" << 'EOF'
version: 1
maxEntries: 5

entries:
  - test: internal/foo:TestFutureEntry
    reason: Known flake under high load
    ticket: TEST-VALID-99
    owner: russlewis
    expiresAt: 2099-12-31
EOF
assert_exit "valid future entry exits 0" 0 "$TMP/valid.yaml"

# --- fixture: expired entry (acceptance criterion 1) ---
cat > "$TMP/expired.yaml" << 'EOF'
version: 1
maxEntries: 5

entries:
  - test: internal/foo:TestDeadlinePassedEntry
    reason: Flake during CI contention
    ticket: TEST-EXPIRED-99
    owner: russlewis
    expiresAt: 2020-01-01
EOF
assert_exit "expired entry exits 1 (gate REDS)" 1 "$TMP/expired.yaml"

# --- fixture: unbounded entry / no expiresAt (acceptance criterion 2) ---
cat > "$TMP/unbounded.yaml" << 'EOF'
version: 1
maxEntries: 5

entries:
  - test: internal/foo:TestUnboundedEntry
    reason: We forgot the deadline
    ticket: TEST-UNBOUNDED-99
    owner: russlewis
EOF
assert_exit "no expiresAt exits 1 (gate REDS)" 1 "$TMP/unbounded.yaml"

# --- fixture: expired pathFilter entry (Amendment A §3 — RED-prove) ---
cat > "$TMP/expired-pathfilter.yaml" << 'EOF'
version: 1
maxEntries: 5

entries:
  - pathFilter: gate.yml:shaders
    reason: Shader CI gated on .metal changes only
    ticket: TEST-PATHFILTER-99
    owner: russlewis
    expiresAt: 2020-01-01
EOF
assert_exit "expired pathFilter exits 1 (gate REDS)" 1 "$TMP/expired-pathfilter.yaml"

# --- fixture: valid future envGate entry ---
cat > "$TMP/valid-envgate.yaml" << 'EOF'
version: 1
maxEntries: 5

entries:
  - envGate: BEAMFALL_KEYCHAIN_TESTS:Tests/BeamfallKitTests:CredentialStoreTests
    reason: Real-Keychain tier never enabled in CI
    ticket: AUDIT-APPLEUI-20
    owner: russlewis
    expiresAt: 2099-12-31
EOF
assert_exit "valid future envGate exits 0" 0 "$TMP/valid-envgate.yaml"

# --- fixture: valid future strictFlag entry ---
cat > "$TMP/valid-strictflag.yaml" << 'EOF'
version: 1
maxEntries: 5

entries:
  - strictFlag: compare.py:--strict
    reason: compare.py no-op without --strict
    ticket: AUDIT-DESIGN-6
    owner: russlewis
    expiresAt: 2099-12-31
EOF
assert_exit "valid future strictFlag exits 0" 0 "$TMP/valid-strictflag.yaml"

# --- fixture: entry naming no skip kind (RED-prove) ---
cat > "$TMP/no-kind.yaml" << 'EOF'
version: 1
maxEntries: 5

entries:
  - reason: Forgot to name the skip kind
    ticket: TEST-NOKIND-99
    owner: russlewis
    expiresAt: 2099-12-31
EOF
assert_exit "entry with no skip kind exits 1 (gate REDS)" 1 "$TMP/no-kind.yaml"

# --- fixture: exceeds hard cap ---
cat > "$TMP/overcap.yaml" << 'EOF'
version: 1
maxEntries: 2

entries:
  - test: internal/a:TestA
    reason: reason A
    ticket: T-A
    owner: russlewis
    expiresAt: 2099-01-01
  - test: internal/b:TestB
    reason: reason B
    ticket: T-B
    owner: russlewis
    expiresAt: 2099-01-01
  - test: internal/c:TestC
    reason: reason C
    ticket: T-C
    owner: russlewis
    expiresAt: 2099-01-01
EOF
assert_exit "entry count over cap exits 1" 1 "$TMP/overcap.yaml"

# Summary
echo ""
if [[ "$FAIL" -gt 0 ]]; then
  echo "check-quarantine tests: $PASS passed, $FAIL FAILED" >&2
  exit 1
fi
echo "check-quarantine tests: $PASS passed, 0 failed"
