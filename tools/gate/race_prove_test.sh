#!/usr/bin/env bash
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [ "${RACE_PROVE_TEST_SUPERVISED:-0}" != 1 ]; then
  exec python3 "$ROOT/script/test-process-supervisor.py" \
    RACE_PROVE_TEST_SUPERVISED "$0" "$@"
fi
set -euo pipefail
export GATE_PROCESS_GROUP_OWNED=0

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# ── RELGATE-NONVAC-4: `make race-prove` must reject non-race failures ────────
# The old recipe accepted ANY non-zero exit as proof the detector fired, so a
# compile error, a missing C toolchain, a panic, or an ordinary assertion failure
# all certified "the race detector is live". Classification is driven here through
# a deterministic `go` shim so every case is exercised on any host; the one leg
# that needs a real detector is preflighted and skipped loudly if it is absent.
race_shim_dir="$TMP/race-shim"
mkdir -p "$race_shim_dir"

# race_prove_shim <exit-status> <canned output...>  -> runs make race-prove
race_prove_shim() {
  local status="$1"; shift
  {
    printf '#!/usr/bin/env bash\n'
    printf 'cat <<%s\n' "'RACEOUT'"
    printf '%s\n' "$@"
    printf 'RACEOUT\n'
    printf 'exit %s\n' "$status"
  } > "$race_shim_dir/go"
  chmod +x "$race_shim_dir/go"
  PATH="$race_shim_dir:$PATH" make -C "$ROOT" race-prove
}

# race_prove_rejects <description> <expected diagnostic> <exit-status> <output...>
race_prove_rejects() {
  local what="$1" diagnostic="$2" status="$3"; shift 3
  if race_prove_shim "$status" "$@" >"$TMP/race-prove.out" 2>&1; then
    cat "$TMP/race-prove.out" >&2
    echo "race-prove accepted $what" >&2
    exit 1
  fi
  grep -Fq "$diagnostic" "$TMP/race-prove.out" || {
    cat "$TMP/race-prove.out" >&2
    echo "race-prove rejected $what for the wrong reason (wanted: $diagnostic)" >&2
    exit 1
  }
}

race_prove_rejects "a compile failure" "build/toolchain failure" 1 \
  "# github.com/beamfall/core/internal/racefixture" \
  "internal/racefixture/race_fixture_test.go:9:1: syntax error" \
  "FAIL	github.com/beamfall/core/internal/racefixture [build failed]"
race_prove_rejects "a missing package" "build/toolchain failure" 1 \
  "go: cannot find package internal/racefixture"
race_prove_rejects "an ordinary test failure" "WARNING: DATA RACE" 1 \
  "--- FAIL: TestDeliberateDataRace (0.00s)" \
  "    race_fixture_test.go:20: values differ" \
  "FAIL	github.com/beamfall/core/internal/racefixture	0.2s"
race_prove_rejects "a panic" "expected the test-failure exit 1, got 2" 2 \
  "panic: runtime error: invalid memory address" \
  "FAIL	github.com/beamfall/core/internal/racefixture	0.2s"
race_prove_rejects "a green fixture" "did NOT red under -race" 0 \
  "ok  	github.com/beamfall/core/internal/racefixture	0.2s"
race_prove_rejects "a race warning with no test-level marker" "race detected during execution of test" 1 \
  "WARNING: DATA RACE" \
  "--- FAIL: TestDeliberateDataRace (0.00s)" \
  "FAIL	github.com/beamfall/core/internal/racefixture	0.2s"

# A genuine report must still be accepted, or the tightening would just break the
# gate rather than sharpen it.
race_prove_shim 1 \
  "==================" \
  "WARNING: DATA RACE" \
  "Read at 0x00c000012345 by goroutine 8:" \
  "==================" \
  "--- FAIL: TestDeliberateDataRace (0.00s)" \
  "    testing.go:1712: race detected during execution of test" \
  "FAIL	github.com/beamfall/core/internal/racefixture	0.2s" \
  >"$TMP/race-prove.out" 2>&1 || {
    cat "$TMP/race-prove.out" >&2
    echo "race-prove rejected a genuine data-race report" >&2
    exit 1
  }

# The real detector, when this host has one. Preflighted with the raw go command
# rather than through the recipe, so a broken recipe can never be misread as an
# unavailable detector.
# `set -o pipefail` is in force and the fixture is meant to red, so capture the
# output first: piping straight into grep would report every host as detectorless.
( cd "$ROOT" && BEAMFALL_PROVE_RACE=1 CGO_ENABLED=1 go test -race -count=1 ./internal/racefixture ) \
  >"$TMP/race-preflight.out" 2>&1 || true
if grep -q 'WARNING: DATA RACE' "$TMP/race-preflight.out"; then
  make -C "$ROOT" race-prove >"$TMP/race-prove.out" 2>&1 || {
    cat "$TMP/race-prove.out" >&2
    echo "race-prove rejected the real deliberate-race fixture" >&2
    exit 1
  }

  # GATE-RACE-SCOPE-1: the same planted race must remain RED when race-short
  # reaches the fixture through diff-scoped discovery, not only via the
  # dedicated make recipe above.
  scoped_repo="$TMP/scoped-race-repo"
  mkdir -p "$scoped_repo/script" "$scoped_repo/internal/racefixture"
  cp "$ROOT/script/race-test.sh" "$scoped_repo/script/race-test.sh"
  cp "$ROOT/internal/racefixture/race_fixture_test.go" \
    "$scoped_repo/internal/racefixture/race_fixture_test.go"
  cat >"$scoped_repo/go.mod" <<'EOF'
module github.com/beamfall/core

go 1.26
EOF
  git -C "$scoped_repo" init -qb main
  git -C "$scoped_repo" add .
  git -C "$scoped_repo" -c user.name=t -c user.email=t@e.com commit -qm base
  scoped_base="$(git -C "$scoped_repo" rev-parse HEAD)"
  printf '\n// diff-scoped race proof\n' >>"$scoped_repo/internal/racefixture/race_fixture_test.go"
  git -C "$scoped_repo" add internal/racefixture/race_fixture_test.go
  git -C "$scoped_repo" -c user.name=t -c user.email=t@e.com commit -qm change-racefixture
  if ( cd "$scoped_repo" && BEAMFALL_PROVE_RACE=1 \
      AGENT_EVIDENCE_DIR="$TMP/scoped-race-evidence" \
      GATE_DIFF_BASE="$scoped_base" GATE_DIFF_HEAD=HEAD \
      script/race-test.sh short ) >"$TMP/scoped-race.out" 2>&1; then
    cat "$TMP/scoped-race.out" >&2
    echo "race-short diff scope stayed green on the planted race" >&2
    exit 1
  fi
  grep -q 'WARNING: DATA RACE' "$TMP/scoped-race.out" || {
    cat "$TMP/scoped-race.out" >&2
    echo "race-short diff scope failed without detecting the planted race" >&2
    exit 1
  }
  python3 - "$TMP/scoped-race-evidence/race-short-discovery.json" <<'PY'
import json
import sys

evidence = json.load(open(sys.argv[1], encoding="utf-8"))
expected = ["github.com/beamfall/core/internal/racefixture"]
if evidence.get("scope") != "diff" or evidence.get("changed_packages") != expected:
    raise SystemExit(
        f"race_prove_test: planted race was not selected by diff scope: {evidence!r}"
    )
PY
else
  echo "SKIP race-prove controls: real detector unavailable on this host (shim controls still ran)"
fi


echo "race prove test: passed"
