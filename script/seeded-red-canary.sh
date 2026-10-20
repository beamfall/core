#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export GOCACHE="${GOCACHE:-${TMPDIR:-/tmp}/beamfall-gcr022-go-cache}"

if [[ "${BASH_SOURCE[0]}" == "$0" && "${GCR022_CANARY_SUPERVISED:-0}" != 1 ]]; then
  exec python3 "$ROOT/script/test-process-supervisor.py" GCR022_CANARY_SUPERVISED "$0" "$@"
fi

tmp=""
seeded_path=""
cleanup_cmd=cleanup_none
cleanup_none() { :; }
cleanup_remove() { rm -f "$seeded_path"; }
cleanup_restore() { cp "$tmp/original" "$seeded_path"; }
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  "$cleanup_cmd"
  [[ -z "$tmp" ]] || rm -rf "$tmp"
  return "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

capture() {
  local log="$1"
  shift
  set +e
  (cd "$ROOT" && "$@") >"$log" 2>&1
  local status=$?
  set -e
  return "$status"
}

prove() {
  local tier="$1" expected="$2" seed_fn="$3" red_fn="$4" clean_fn="$5"
  local red_status=0
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/gcr022-${tier}.XXXXXX")"
  "$seed_fn"
  if capture "$tmp/red.log" "$red_fn"; then
    cat "$tmp/red.log" >&2
    echo "FAIL $tier: seeded regression passed" >&2
    return 1
  else
    red_status=$?
  fi
  grep -Fq "$expected" "$tmp/red.log" || {
    cat "$tmp/red.log" >&2
    echo "FAIL $tier: wrong failure (wanted: $expected)" >&2
    return 1
  }
  if grep -Fq "GCR-022 canary infrastructure failure" "$tmp/red.log"; then
    cat "$tmp/red.log" >&2
    echo "FAIL $tier: seeded check also leaked test infrastructure" >&2
    return 1
  fi
  echo "RED $tier: command exited $red_status; matched: $expected"
  "$cleanup_cmd"
  cleanup_cmd=cleanup_none
  capture "$tmp/clean.log" "$clean_fn" || {
    cat "$tmp/clean.log" >&2
    echo "FAIL $tier: clean tree red" >&2
    return 1
  }
  echo "PASS $tier: seeded regression red; restored clean tree green"
  rm -rf "$tmp"
  tmp=""
}

seed_gate() {
  local path="$ROOT/tools/gate/gcr022_seeded_red.go"
  [[ ! -e "$path" ]] || { echo "refusing existing $path" >&2; return 2; }
  seeded_path="$path"
  cleanup_cmd=cleanup_remove
  printf 'package gate\n\nfunc gcr022SeededRed( ) { }\n' >"$path"
}
red_gate() { make gate; }
clean_gate() { make gate; }

seed_feature_coverage() {
  local path="$ROOT/testing/feature-coverage-ratchet.json"
  cp "$path" "$tmp/original"
  seeded_path="$path"
  cleanup_cmd=cleanup_restore
  python3 - "$path" <<'PY'
import json, sys
path = sys.argv[1]
data = json.load(open(path))
data["targets"]["server"]["minProven"] = 999999
open(path, "w").write(json.dumps(data, indent=2) + "\n")
PY
}
red_feature_coverage() {
  go run ./cmd/featurecheck -ratchet testing/feature-coverage-ratchet.json
}
clean_coverage() {
  go run ./cmd/featurecheck -ratchet testing/feature-coverage-ratchet.json || return
  go run ./cmd/covratchet -floor testing/coverage-floor.json
}

seed_line_coverage() {
  local path="$ROOT/testing/coverage-floor.json"
  cp "$path" "$tmp/original"
  seeded_path="$path"
  cleanup_cmd=cleanup_restore
  python3 - "$path" <<'PY'
import json, sys
path = sys.argv[1]
data = json.load(open(path))
data["aggregate"] = 101
open(path, "w").write(json.dumps(data, indent=2, sort_keys=True) + "\n")
PY
}
red_line_coverage() {
  go run ./cmd/covratchet -floor testing/coverage-floor.json
}

coverage_canary() {
  prove coverage-feature "below ratchet" seed_feature_coverage red_feature_coverage clean_coverage
  prove coverage-line "below coverage floor" seed_line_coverage red_line_coverage clean_coverage
}

seed_io() {
  local path="$ROOT/internal/ioe2e/gcr022_seeded_red_test.go"
  [[ ! -e "$path" ]] || { echo "refusing existing $path" >&2; return 2; }
  seeded_path="$path"
  cleanup_cmd=cleanup_remove
  cat >"$path" <<'EOF'
//go:build io_e2e

package ioe2e

import "testing"

func TestGCR022SeededRed(t *testing.T) { t.Fatal("GCR-022 seeded I/O regression") }
EOF
}
run_io_e2e() { script/io-e2e.sh "$@"; }
run_selected_io_test() {
  local subcommand="$1" test_name="$2" status=0
  local log="$tmp/io-$test_name.log"
  run_io_e2e "$subcommand" -run "^${test_name}$" -count=1 -v >"$log" 2>&1 || status=$?
  cat "$log"
  grep -Fqx "=== RUN   $test_name" "$log" || {
    echo "GCR-022 canary infrastructure failure: I/O test $test_name did not run" >&2
    return 1
  }
  return "$status"
}
red_io() {
  run_io_e2e fixtures || return
  run_selected_io_test journeys TestGCR022SeededRed
}
clean_io() {
  local status=0 log="$tmp/io-journeys.log"
  run_io_e2e journeys -count=1 -v >"$log" 2>&1 || status=$?
  cat "$log"
  grep -Fqx "=== RUN   TestFrameGrabJourney" "$log" || {
    echo "GCR-022 canary infrastructure failure: I/O journeys tier did not run" >&2
    return 1
  }
  return "$status"
}

seed_web() {
  local path="$ROOT/internal/web/app/e2e/smoke.spec.ts"
  cp "$path" "$tmp/original"
  seeded_path="$path"
  cleanup_cmd=cleanup_restore
  cat >>"$path" <<'EOF'

test("GCR-022 seeded Playwright canary", async () => {
  throw new Error("GCR-022 seeded Playwright regression");
});
EOF
}
web_state="$ROOT/internal/web/app/e2e/.auth/state.json"
drain_web_server() {
  local pid=""
  if [[ -f "$web_state" ]]; then
    pid="$(python3 - "$web_state" <<'PY'
import json, sys
try:
    value = json.load(open(sys.argv[1])).get("pid")
except (OSError, ValueError):
    value = None
print(value if isinstance(value, int) and value > 1 else "")
PY
)"
  fi
  [[ -n "$pid" ]] || return 0
  for _ in {1..20}; do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.05
  done
  kill -TERM "$pid" 2>/dev/null || true
  for _ in {1..40}; do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.05
  done
  kill -KILL "$pid" 2>/dev/null || true
  for _ in {1..40}; do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 0.05
  done
  echo "GCR-022 canary infrastructure failure: Playwright server $pid did not exit" >&2
  return 1
}
run_web() {
  rm -f "$web_state"
  local status=0
  script/web-ui-e2e-full.sh "$@" || status=$?
  drain_web_server || return 1
  return "$status"
}
red_web() {
  run_web --project unauth --grep 'GCR-022 seeded Playwright canary'
}
run_full_web() {
  local status=0 log="$tmp/web-full.log"
  run_web >"$log" 2>&1 || status=$?
  cat "$log"
  grep -Eq 'Running [1-9][0-9]* tests? using [1-9][0-9]* workers?' "$log" || {
    echo "GCR-022 canary infrastructure failure: full Playwright tier did not run" >&2
    return 1
  }
  return "$status"
}
clean_web() {
  run_full_web
}

seed_bench() {
  local path="$ROOT/internal/store/benchfixture_test.go"
  cp "$path" "$tmp/original"
  seeded_path="$path"
  cleanup_cmd=cleanup_restore
  python3 - "$path" <<'PY'
import sys
path = sys.argv[1]
text = open(path).read()
old = '"source/recently-added":       1 * time.Millisecond'
if text.count(old) != 1:
    raise SystemExit("bench seed anchor missing or ambiguous")
open(path, "w").write(text.replace(old, '"source/recently-added":       0 * time.Millisecond'))
PY
}
red_bench() { make bench; }
clean_bench() { make bench; }

seed_fuzz() {
  local path="$ROOT/internal/ffmpeg/ffmpeg_fuzz_test.go"
  cp "$path" "$tmp/original"
  seeded_path="$path"
  cleanup_cmd=cleanup_restore
  python3 - "$path" <<'PY'
import sys
path = sys.argv[1]
text = open(path).read()
old = "\t\t_, _ = parseProbe(data, path)"
if text.count(old) != 1:
    raise SystemExit("fuzz seed anchor missing or ambiguous")
new = '''\t\tprobe, _ := parseProbe(data, path)
\t\tif path == "/media/Test Pattern.mp4" && probe.Container != "GCR-022 seeded fuzz regression" {
\t\t\tt.Fatalf("GCR-022 seeded fuzz regression: Container = %q", probe.Container)
\t\t}'''
open(path, "w").write(text.replace(old, new))
PY
}
red_fuzz() { env FUZZ_TIME=1x FUZZ_JOBS=1 make fuzz-active; }
clean_fuzz() { env FUZZ_TIME=1x FUZZ_JOBS=1 make fuzz-active; }

seed_mutation() {
  local path="$ROOT/docs/testing/mutation-budget.json"
  cp "$path" "$tmp/original"
  seeded_path="$path"
  cleanup_cmd=cleanup_restore
  python3 - "$path" <<'PY'
import json, sys
path = sys.argv[1]
data = json.load(open(path))
data["packages"]["./internal/streamtoken"]["max_surviving"] = -1
open(path, "w").write(json.dumps(data, indent=2) + "\n")
PY
}
red_mutation() { script/mutation-test.sh streamtoken; }
clean_mutation() { script/mutation-test.sh streamtoken; }

run_canary() {
  case "$1" in
    gate) prove gate "gofmt needed" seed_gate red_gate clean_gate ;;
    coverage) coverage_canary ;;
    io) prove io "GCR-022 seeded I/O regression" seed_io red_io clean_io ;;
    web) prove web "GCR-022 seeded Playwright regression" seed_web red_web clean_web ;;
    bench) prove bench "exceeds budget 0s" seed_bench red_bench clean_bench ;;
    fuzz) prove fuzz "GCR-022 seeded fuzz regression" seed_fuzz red_fuzz clean_fuzz ;;
    mutation) prove mutation "> budget -1 (ratchet floor breached)" seed_mutation red_mutation clean_mutation ;;
    *) echo "usage: $0 {gate|coverage|io|web|bench|fuzz|mutation|all}" >&2; return 2 ;;
  esac
}

main() {
  [[ $# -eq 1 ]] || { echo "usage: $0 {gate|coverage|io|web|bench|fuzz|mutation|all}" >&2; return 2; }
  if [[ "$1" == all ]]; then
    local tier
    for tier in gate coverage io web bench fuzz mutation; do run_canary "$tier"; done
    return
  fi
  run_canary "$1"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
