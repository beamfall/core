#!/usr/bin/env bash
# Fail-closed and planted-red selftest for the race-short content cache.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [ "${RACE_SHORT_CACHE_TEST_SUPERVISED:-0}" != 1 ]; then
  exec python3 "$ROOT/script/test-process-supervisor.py" \
    RACE_SHORT_CACHE_TEST_SUPERVISED "$0" "$@"
fi

TMP="$(mktemp -d)"
cleanup() { rm -rf "$TMP"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
FIXTURE="$TMP/repo"
CACHE="$TMP/cache"
MARKER="$TMP/executions"
RUNNER="$TMP/run-race-short.sh"
HELPER="$FIXTURE/tools/gate/slow_orchestration_cache.py"

fail() {
  echo "race_short_cache_test FAIL: $*" >&2
  exit 1
}

mkdir -p "$FIXTURE/tools/gate" "$FIXTURE/script" \
  "$FIXTURE/internal/wired" "$FIXTURE/internal/graph" \
  "$FIXTURE/internal/store" "$FIXTURE/internal/web" \
  "$FIXTURE/internal/racefixture"
cp "$ROOT/tools/gate/slow_orchestration_cache.py" "$HELPER"
cat >"$FIXTURE/go.mod" <<'EOF'
module github.com/beamfall/core

go 1.23
EOF
: >"$FIXTURE/go.sum"
cat >"$FIXTURE/Makefile" <<'EOF'
include tools/gate/targets.mk
EOF
cat >"$FIXTURE/tools/gate/targets.mk" <<'EOF'
race-short:
	@script/race-test.sh short
EOF
cat >"$FIXTURE/script/race-test.sh" <<'EOF'
#!/usr/bin/env bash
RACE_SHORT_EXCLUDED_PKGS=(
  github.com/beamfall/core/internal/graph
  github.com/beamfall/core/internal/store
  github.com/beamfall/core/internal/web
)
EOF
printf '#!/usr/bin/env bash\nexit 0\n' >"$FIXTURE/tools/gate/race_prove_test.sh"
cat >"$FIXTURE/internal/graph/graph.go" <<'EOF'
package graph

const Value = "dependency"
EOF
printf 'package store\n' >"$FIXTURE/internal/store/store.go"
printf 'package web\n' >"$FIXTURE/internal/web/web.go"
cat >"$FIXTURE/internal/wired/wired.go" <<'EOF'
package wired

import "github.com/beamfall/core/internal/graph"

func Value() string { return graph.Value }
EOF
cat >"$FIXTURE/internal/wired/wired_test.go" <<'EOF'
package wired

import "testing"

func TestValue(t *testing.T) {
	if Value() == "" {
		t.Fatal("missing value")
	}
}
EOF
printf 'package racefixture\n' >"$FIXTURE/internal/racefixture/fixture.go"
printf 'fixture-data\n' >"$FIXTURE/internal/racefixture/input.txt"
chmod +x "$FIXTURE/script/race-test.sh" "$FIXTURE/tools/gate/race_prove_test.sh"

git -C "$FIXTURE" init -qb main
git -C "$FIXTURE" add .
git -C "$FIXTURE" -c user.name=test -c user.email=test@example.invalid commit -qm fixture

cat >"$RUNNER" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
root="$1"
marker="$2"
printf 'executed\n' >>"$marker"
if grep -Fq PLANTED_RED "$root/internal/wired/wired.go"; then
  echo "planted race-short failure" >&2
  exit 23
fi
EOF
chmod +x "$RUNNER" "$HELPER"
export GATE_STEP_CACHE_DIR="$CACHE"
export GOCACHE="$TMP/gocache"

run_cache_step() {
  python3 "$HELPER" run --root "$FIXTURE" --cache race-short -- \
    "$RUNNER" "$FIXTURE" "$MARKER"
}

hash_inputs() {
  python3 "$HELPER" hash --root "$FIXTURE" --cache race-short
}

execution_count() {
  if [ -f "$MARKER" ]; then
    wc -l <"$MARKER" | tr -d ' '
  else
    printf '0'
  fi
}

manifest="$TMP/manifest"
python3 "$HELPER" manifest --root "$FIXTURE" --cache race-short >"$manifest"
for required in \
  go.mod go.sum Makefile script/race-test.sh tools/gate/targets.mk \
  tools/gate/race_prove_test.sh internal/wired/wired.go \
  internal/wired/wired_test.go internal/graph/graph.go \
  internal/racefixture/fixture.go internal/racefixture/input.txt; do
  grep -Fxq "$required" "$manifest" || fail "manifest omitted $required"
done

base_hash="$(hash_inputs)"
head_sha="$(git -C "$FIXTURE" rev-parse HEAD)"
receipt="$CACHE/race-short/$base_hash.json"
EXPLAIN=1 run_cache_step >"$TMP/explain.out"
[ "$(execution_count)" = 1 ] || fail "explain mode did not execute race-short"
[ ! -e "$receipt" ] || fail "explain mode wrote a green cache receipt"

run_cache_step >"$TMP/first.out"
[ "$(execution_count)" = 2 ] || fail "missing cache did not execute race-short"
[ -f "$receipt" ] || fail "green race-short did not create a receipt"
python3 - "$receipt" "$base_hash" "$head_sha" <<'PY'
import datetime as dt
import json
from pathlib import Path
import sys

payload = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
if payload["closure_name"] != "race-short":
    raise SystemExit("receipt closure name mismatch")
if payload["closure_hash"] != sys.argv[2] or payload["head_sha"] != sys.argv[3]:
    raise SystemExit("receipt is not bound to the closure hash and head SHA")
dt.datetime.strptime(payload["timestamp_utc"], "%Y%m%dT%H%M%SZ")
if payload["receipt"]["status"] != "pass" or not payload["receipt"]["packages"]:
    raise SystemExit("receipt does not bind a green wired package list")
PY

run_cache_step >"$TMP/hit.out"
[ "$(execution_count)" = 2 ] || fail "unchanged race-short closure did not hit"
grep -Fq "PASS race-short (cached: $base_hash from $head_sha; green: " "$TMP/hit.out" \
  || fail "cache hit did not print the bound green receipt"
EXPLAIN=1 run_cache_step >"$TMP/explain-hit.out"
[ "$(execution_count)" = 3 ] || fail "explain mode consumed an existing cache receipt"
if grep -Fq '(cached:' "$TMP/explain-hit.out"; then
  fail "explain mode printed a cache hit"
fi

assert_mutation_misses() {
  local target="$1" label="$2" saved="$TMP/saved"
  cp "$target" "$saved"
  printf '\n' >>"$target"
  local mutated_hash
  mutated_hash="$(hash_inputs)"
  [ "$mutated_hash" != "$base_hash" ] || fail "$label mutation did not change the hash"
  run_cache_step >"$TMP/$label.out"
  mv "$saved" "$target"
}

assert_mutation_misses "$FIXTURE/internal/wired/wired.go" wired
[ "$(execution_count)" = 4 ] || fail "wired package mutation did not execute race-short"
assert_mutation_misses "$FIXTURE/internal/graph/graph.go" dependency
[ "$(execution_count)" = 5 ] || fail "transitive dependency mutation did not execute race-short"
assert_mutation_misses "$FIXTURE/go.sum" go-sum
[ "$(execution_count)" = 6 ] || fail "go.sum mutation did not execute race-short"
assert_mutation_misses "$FIXTURE/internal/racefixture/input.txt" racefixture
[ "$(execution_count)" = 7 ] || fail "racefixture mutation did not execute race-short"

cp "$FIXTURE/internal/wired/wired.go" "$TMP/wired.go"
printf '// PLANTED_RED\n' >>"$FIXTURE/internal/wired/wired.go"
red_hash="$(hash_inputs)"
if run_cache_step >"$TMP/red.out" 2>"$TMP/red.err"; then
  fail "planted red passed"
fi
[ "$(execution_count)" = 8 ] || fail "planted red did not execute race-short"
[ ! -e "$CACHE/race-short/$red_hash.json" ] || fail "failing race-short wrote a receipt"
grep -Fq "planted race-short failure" "$TMP/red.err" \
  || fail "planted red did not preserve the fresh failure"
mv "$TMP/wired.go" "$FIXTURE/internal/wired/wired.go"

GATE_MODE=release run_cache_step >"$TMP/release-one.out"
GATE_MODE=release run_cache_step >"$TMP/release-two.out"
[ "$(execution_count)" = 10 ] || fail "release mode consumed a race-short cache receipt"
if grep -Fq '(cached:' "$TMP/release-one.out" "$TMP/release-two.out"; then
  fail "release mode printed a cache hit"
fi

run_cache_step >"$TMP/restored-hit.out"
[ "$(execution_count)" = 10 ] || fail "restored closure did not reuse its original green"

echo "race-short cache test: passed"
