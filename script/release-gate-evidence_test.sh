#!/usr/bin/env bash
# Non-vacuity controls for the release evidence validator (RELGATE-NONVAC-2).
# Every case here is an input that the previous `test -d` check accepted while the
# tier it claimed was absent, empty, junk, or failed.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECK="$ROOT/script/release-gate-evidence.sh"
COMMIT=1111111111111111111111111111111111111111
tmp="$(mktemp -d "${TMPDIR:-/tmp}/release-gate-evidence-test.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

fail() { echo "release-gate-evidence_test: $*" >&2; exit 1; }

# refuses <case> <args...> — the validator must reject this input.
refuses() {
  local case_name="$1"; shift
  if "$CHECK" "$@" >"$tmp/out" 2>&1; then
    cat "$tmp/out" >&2
    fail "accepted $case_name"
  fi
}

record() {  # record <artifact-dir> <label> <exit_code>
  local dir="$1/20260101T000000Z-$2.abcdef"
  mkdir -p "$dir"
  printf '{"exit_code": %s, "type": "command"}\n' "$3" > "$dir/metadata.json"
}

junit() {  # junit <path> <testcase-count>
  local i=0
  {
    printf '<testsuites><testsuite name="github.com/beamfall/core/internal/ioe2e">'
    while [ "$i" -lt "$2" ]; do printf '<testcase name="TestX"/>'; i=$((i + 1)); done
    printf '</testsuite></testsuites>\n'
  } > "$1"
}

# A complete, honest packet — the shape the release gate really uploads, with the
# run-with-evidence records one directory below the artifact root.
complete() {
  local root="$1"
  rm -rf "$root"
  mkdir -p "$root/release-gate-io-evidence" "$root/release-gate-web-e2e-evidence"
  junit "$root/release-gate-io-evidence/io-journeys-junit.xml" 2
  record "$root/release-gate-io-evidence" io-smoke 0
  record "$root/release-gate-io-evidence" io-journeys-junit 0
  record "$root/release-gate-io-evidence" ioe2e-release-check 0
  record "$root/release-gate-web-e2e-evidence" web-e2e-full 0
  printf '{"commit": "%s", "result": "PASS"}\n' "$COMMIT" > "$root/aggregate-result.json"
}

good="$tmp/good"
complete "$good"
"$CHECK" require-artifacts "$good" >/dev/null || fail "rejected a complete packet"
"$CHECK" verify "$good" --expect-commit "$COMMIT" >/dev/null || fail "rejected a matching aggregate"

# 1) A whole artifact missing.
work="$tmp/w"; complete "$work"; rm -rf "$work/release-gate-web-e2e-evidence"
refuses "a packet missing release-gate-web-e2e-evidence" require-artifacts "$work"

# 2) The artifact directory exists but is EMPTY — the exact input `test -d` passed.
complete "$work"; rm -rf "$work/release-gate-web-e2e-evidence"; mkdir "$work/release-gate-web-e2e-evidence"
refuses "an empty evidence directory" require-artifacts "$work"

# 3) Junk in place of evidence.
complete "$work"; rm -rf "$work/release-gate-web-e2e-evidence"
mkdir "$work/release-gate-web-e2e-evidence"; echo hello > "$work/release-gate-web-e2e-evidence/hello.txt"
refuses "a junk file as web evidence" require-artifacts "$work"

# 4) JUnit present but proving nothing.
complete "$work"; junit "$work/release-gate-io-evidence/io-journeys-junit.xml" 0
refuses "a JUnit with zero testcases" require-artifacts "$work"
complete "$work"; : > "$work/release-gate-io-evidence/io-journeys-junit.xml"
refuses "an empty JUnit" require-artifacts "$work"
complete "$work"; rm -f "$work/release-gate-io-evidence/io-journeys-junit.xml"
refuses "a missing JUnit" require-artifacts "$work"

# 5) A named tier that did not run, or ran and FAILED.
complete "$work"; rm -rf "$work"/release-gate-io-evidence/*ioe2e-release-check*
refuses "an absent ioe2e-release-check record" require-artifacts "$work"
complete "$work"; record "$work/release-gate-io-evidence" ioe2e-release-check 1
refuses "an ioe2e-release-check record with a non-zero exit" require-artifacts "$work"
complete "$work"; rm -rf "$work"/release-gate-web-e2e-evidence/*web-e2e-full*
refuses "an absent web-e2e-full record" require-artifacts "$work"
complete "$work"; rm -rf "$work"/release-gate-web-e2e-evidence/*web-e2e-full*
record "$work/release-gate-web-e2e-evidence" some-other-command 0
refuses "a mislabeled web record" require-artifacts "$work"
complete "$work"; record "$work/release-gate-web-e2e-evidence" web-e2e-full 1
refuses "a failed web-e2e-full record" require-artifacts "$work"

# 6) The exact-SHA binding (RELGATE-NONVAC-1's content-level check).
complete "$work"
refuses "an aggregate bound to another commit" verify "$work" --expect-commit 2222222222222222222222222222222222222222
complete "$work"; printf '{"commit": "%s", "result": "FAIL"}\n' "$COMMIT" > "$work/aggregate-result.json"
refuses "a non-PASS aggregate" verify "$work" --expect-commit "$COMMIT"
complete "$work"; rm -f "$work/aggregate-result.json"
refuses "a packet with no aggregate result" verify "$work" --expect-commit "$COMMIT"
complete "$work"; printf 'not json\n' > "$work/aggregate-result.json"
refuses "an unparseable aggregate" verify "$work" --expect-commit "$COMMIT"
complete "$work"
refuses "an empty --expect-commit" verify "$work" --expect-commit ""

# 7) The producer must actually run the packages the release obligation names.
pkgs="$(cd "$ROOT" && go run ./cmd/ioe2e release-packages)"
for want in ./internal/ioe2e ./internal/backup ./internal/ffmpeg; do
  printf '%s\n' "$pkgs" | grep -qx -- "$want" \
    || fail "release-packages omits $want; its pinned ids could never be proven"
done
grep -q -- '-parser gojson' "$ROOT/script/io-e2e.sh" \
  || fail "journeys-junit must name the gojson parser or the report loses package identity"
grep -q 'release_marker_packages' "$ROOT/script/io-e2e.sh" \
  || fail "journeys-junit must derive its packages from cmd/ioe2e release-packages"

echo "release-gate-evidence tests: passed"
