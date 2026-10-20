#!/usr/bin/env bash
# GATE-GOCACHE-PERSIST-1: tools/gate/gate_latest.py now defaults GOCACHE to a
# persistent, gitignored directory instead of a fresh-then-discarded tempdir
# (warm `go build ./...` reuse across gate runs). A persistent cache makes
# `go test`'s own result cache load-bearing: any `go test` invocation missing
# -count=1 can silently replay a stale cached PASS instead of re-running —
# green-but-blind. This walks every executable (non-self-test) `go test` call
# under tools/ and script/ and fails on a NEW miss, so future additions fail
# closed.
#
# Excluded by design, not oversight:
#   *_test.sh files       — self-tests that stub/assert against `go`, they
#                            never execute a real `go test` themselves.
#   script/io-e2e.sh      — weekly/manual drift track (see its own header:
#                            "not the inner loop"), deliberately forwards
#                            "$@" so callers control test flags/caching.
# Known non-executing `go test` lines (e.g. -c compile-only) are exempted via
# the self-pruning LINE_ALLOWLIST below.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

SELF="tools/gate/go-test-count1-check.sh"

FILE_EXEMPT=(
  "script/io-e2e.sh"
  # Assertion-fixture files: their 'go test' strings are expected-output data
  # compared by assert_out / heredoc fixtures, never executed by the gate.
  "tools/gate/gate_rerun_test.sh"
)

LINE_ALLOWLIST=(
  # -c only compiles a test binary to run under a Docker fixture; no tests
  # execute here, so there is nothing for -count=1 to protect.
  'go test -c ./internal/ffmpeg -o "$tmp/ffmpeg.test"'
  # Step NAME in gate_scope_test.sh's excluded-inventory set — a quoted data
  # string, never an invocation; the leading double quote keeps this from
  # matching a real call.
  '"go test -tags beamfall_appstore conformance capabilities graph"'
)

is_file_exempt() {
  local rel="$1" f
  for f in ${FILE_EXEMPT[@]+"${FILE_EXEMPT[@]}"}; do
    [ "$f" = "$rel" ] && return 0
  done
  return 1
}

is_line_allowed() {
  local line="$1" a
  for a in ${LINE_ALLOWLIST[@]+"${LINE_ALLOWLIST[@]}"}; do
    [[ "$line" == *"$a"* ]] && return 0
  done
  return 1
}

violations=()
allowlist_seen=()

while IFS= read -r -d '' file; do
  rel="${file#"$ROOT"/}"
  [ "$rel" = "$SELF" ] && continue
  is_file_exempt "$rel" && continue
  while IFS=: read -r lineno content; do
    trimmed="${content#"${content%%[![:space:]]*}"}"
    [ "${trimmed:0:1}" = "#" ] && continue
    if is_line_allowed "$content"; then
      allowlist_seen+=("$content")
      continue
    fi
    # Real invocation signal: "go test" immediately followed by a flag or a
    # package path — filters out label/description strings ("go test +
    # line-coverage ratchet", "$name (go test arguments)") that merely
    # mention the words.
    if [[ "$content" =~ go\ test[[:space:]]+(-|\./) ]]; then
      if [[ "$content" != *"-count=1"* ]]; then
        violations+=("$rel:$lineno: $trimmed")
      fi
    fi
  done < <(grep -n 'go test' "$file" || true)
done < <(find tools script -type f -name '*.sh' ! -name 'go-test-count1-check_test.sh' -print0)

for a in ${LINE_ALLOWLIST[@]+"${LINE_ALLOWLIST[@]}"}; do
  found=0
  for s in ${allowlist_seen[@]+"${allowlist_seen[@]}"}; do
    [[ "$s" == *"$a"* ]] && found=1 && break
  done
  if [ "$found" -eq 0 ]; then
    echo "go-test-count1-check: stale allowlist entry (already fixed or removed, remove it): $a" >&2
    violations+=("<allowlist>: stale entry: $a")
  fi
done

if [ "${#violations[@]}" -gt 0 ]; then
  echo "go-test-count1-check: go test invocation(s) missing -count=1 (a persistent GOCACHE could serve a cached PASS instead of re-running):" >&2
  printf '  %s\n' "${violations[@]}" >&2
  exit 1
fi

echo "go-test-count1-check: clean (${#LINE_ALLOWLIST[@]} known exemption(s))"
