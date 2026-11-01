#!/usr/bin/env bash
# CORE-0944: script/gate-affected.sh against a stub corvint and a stub make —
# BASE resolution and refusal, the dry-run/run manifest binding, exit-status
# passthrough, the gate-preflight dedupe and signal exit codes.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/gate-affected-test.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

fail() {
  echo "gate-affected_test: $*" >&2
  exit 1
}

repo="$TMP/repo"
mkdir -p "$repo/script" "$repo/.github/cishards" "$TMP/bin"
cp "$ROOT/script/gate-affected.sh" "$repo/script/"
cp "$ROOT/.github/cishards/test-commands.json" "$repo/.github/cishards/"
git -C "$repo" init -q -b main
git -C "$repo" config user.name "Gate Affected Test"
git -C "$repo" config user.email "gate-affected@example.invalid"
git -C "$repo" config commit.gpgsign false
git -C "$repo" add -A
git -C "$repo" commit -qm base
main_sha="$(git -C "$repo" rev-parse HEAD)"
git -C "$repo" switch -qc lane
echo change > "$repo/change.txt"
git -C "$repo" add change.txt
git -C "$repo" commit -qm change

# The stub corvint writes STUB_UNITS into the dry-run manifest, logs each call,
# and on the bound run exits STUB_STATUS (or sleeps for the signal case).
cat > "$TMP/bin/corvint" <<'STUB'
#!/usr/bin/env bash
echo "corvint $*" >> "$STUB_LOG"
manifest=""
dry=0
while [ $# -gt 0 ]; do
  case "$1" in
    --manifest) manifest="$2"; shift ;;
    --dry-run) dry=1 ;;
  esac
  shift
done
if [ "$dry" = 1 ]; then
  python3 -c 'import json,sys; json.dump({"units":[{"unit":u} for u in sys.argv[2].split()]}, open(sys.argv[1],"w"))' "$manifest" "${STUB_UNITS:-}"
  exit 0
fi
[ -f "$manifest" ] || { echo "stub: bound manifest missing" >&2; exit 9; }
if [ -n "${STUB_SLEEP:-}" ]; then
  trap 'echo "corvint got TERM" >> "$STUB_LOG"; exit 143' TERM
  echo ready > "$STUB_READY"
  sleep "$STUB_SLEEP" & wait $!
fi
exit "${STUB_STATUS:-0}"
STUB
cat > "$TMP/bin/make" <<'STUB'
#!/usr/bin/env bash
echo "make $*" >> "$STUB_LOG"
exit "${STUB_MAKE_STATUS:-0}"
STUB
chmod +x "$TMP/bin/corvint" "$TMP/bin/make"
export PATH="$TMP/bin:$PATH" STUB_LOG="$TMP/log" STUB_READY="$TMP/ready"

run() {
  : > "$STUB_LOG"
  status=0
  (cd "$repo" && script/gate-affected.sh "$@") > "$TMP/out" 2>&1 || status=$?
}

# BASE refusals exit 2 before corvint runs.
run -x
[ "$status" = 2 ] && [ ! -s "$STUB_LOG" ] || fail "an option-shaped BASE must exit 2 (got $status)"
run no-such-commit
[ "$status" = 2 ] && [ ! -s "$STUB_LOG" ] || fail "an unresolvable BASE must exit 2 (got $status)"

# Default BASE is the merge-base with main; the run is bound to the dry-run's
# manifest; a plan without a preflight language runs gate-preflight after.
STUB_UNITS="go:example/pkg" run
[ "$status" = 0 ] || fail "passing run exited $status: $(cat "$TMP/out")"
grep -q "gate-affected: base $main_sha (" "$TMP/out" || fail "default BASE is not the merge-base with main"
dry_manifest="$(sed -n 1p "$STUB_LOG" | sed 's/.*--manifest //')"
grep -qx "corvint test --base $main_sha --dry-run --summary --manifest $dry_manifest" "$STUB_LOG" || fail "dry-run call wrong: $(cat "$STUB_LOG")"
grep -qx "corvint test --base $main_sha --manifest $dry_manifest" "$STUB_LOG" || fail "run is not bound to the dry-run manifest"
[ "$(tail -1 "$STUB_LOG")" = "make gate-preflight" ] || fail "gate-preflight did not run after a go-only plan"
[ ! -e "$dry_manifest" ] || fail "manifest workdir not removed before exec"

# gate-preflight's own status is the result.
STUB_UNITS="go:example/pkg" STUB_MAKE_STATUS=1 run
[ "$status" = 1 ] || fail "gate-preflight failure must fail gate-affected (got $status)"

# A plan whose declared language command is gate-preflight does not run it twice.
STUB_UNITS="go:example/pkg declared:shell" run "$main_sha"
[ "$status" = 0 ] || fail "dedupe run exited $status"
! grep -q '^make' "$STUB_LOG" || fail "gate-preflight ran twice"
grep -q 'already ran as declared:shell' "$TMP/out" || fail "dedupe not reported"

# corvint's status passes through and gate-preflight is skipped.
for want in 1 3; do
  STUB_UNITS="go:example/pkg" STUB_STATUS="$want" run
  [ "$status" = "$want" ] || fail "corvint exit $want became $status"
  ! grep -q '^make' "$STUB_LOG" || fail "gate-preflight ran after corvint exit $want"
done

# TERM during the run is forwarded to corvint and exits 143.
: > "$STUB_LOG"
rm -f "$STUB_READY"
(cd "$repo" && STUB_UNITS="go:example/pkg" STUB_SLEEP=30 exec script/gate-affected.sh) > "$TMP/out" 2>&1 &
pid=$!
for _ in $(seq 100); do [ -f "$STUB_READY" ] && break; sleep 0.1; done
[ -f "$STUB_READY" ] || fail "stub corvint never started"
kill -TERM "$pid"
status=0
wait "$pid" || status=$?
[ "$status" = 143 ] || fail "TERM must exit 143 (got $status)"
grep -q 'corvint got TERM' "$STUB_LOG" || fail "TERM not forwarded to corvint"

echo "gate-affected_test: PASS"
