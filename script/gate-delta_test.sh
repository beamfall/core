#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GATE_DELTA="$ROOT/script/gate-delta.sh"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/gate-delta-test.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

fail() {
  echo "gate-delta_test: $*" >&2
  exit 1
}

assert_eq() {
  local actual="$1" expected="$2" name="$3"
  if [[ "$actual" != "$expected" ]]; then
    printf 'FAIL %s\nexpected:\n%s\nactual:\n%s\n' "$name" "$expected" "$actual" >&2
    exit 1
  fi
}

new_repo() {
  local name="$1" repo="$TMP/$1"
  mkdir -p "$repo"
  repo="$(cd "$repo" && pwd -P)"
  git -C "$repo" init -q -b main
  git -C "$repo" config user.name "Gate Delta Test"
  git -C "$repo" config user.email "gate-delta@example.invalid"
  git -C "$repo" config commit.gpgsign false
  printf 'baseline\n' >"$repo/baseline.txt"
  git -C "$repo" add baseline.txt
  git -C "$repo" commit -qm baseline
  git -C "$repo" switch -qc lane
  printf '%s\n' "$repo"
}

commit_changes() {
  local repo="$1" path
  shift
  for path in "$@"; do
    mkdir -p "$(dirname "$repo/$path")"
    printf 'changed\n' >"$repo/$path"
  done
  git -C "$repo" add -- "$@"
  git -C "$repo" commit -qm change
}

explain() {
  (cd "$1" && "$GATE_DELTA" --explain)
}

expected_explain() {
  local repo="$1" class="$2" files="$3" plan="$4" base
  base="$(git -C "$repo" rev-parse main)"
  printf 'gate-delta: class=%s files=%s base=%s\nplan: %s' \
    "$class" "$files" "${base:0:9}" "$plan"
}

docs_repo="$(new_repo docs)"
commit_changes "$docs_repo" docs/operator-guide.md
assert_eq \
  "$(explain "$docs_repo")" \
  "$(expected_explain "$docs_repo" docs 1 'make adr-lint')" \
  "docs-only explain output"

web_repo="$(new_repo web)"
commit_changes "$web_repo" internal/web/app/src/gate-delta-fixture.ts
assert_eq \
  "$(explain "$web_repo")" \
  "$(expected_explain "$web_repo" web 1 'web suite (biome/build/tests) + go build ./... + go test -count=1 ./internal/web/... ./internal/server/...')" \
  "web-only explain output"

unknown_repo="$(new_repo unknown)"
commit_changes "$unknown_repo" internal/store/gate_delta_fixture.go
assert_eq \
  "$(explain "$unknown_repo")" \
  "$(expected_explain "$unknown_repo" full 1 'exec canonical full gate (make gate)')" \
  "unknown path fails closed"

mixed_repo="$(new_repo mixed)"
commit_changes \
  "$mixed_repo" \
  internal/web/app/src/gate-delta-fixture.ts \
  internal/store/gate_delta_fixture.go
assert_eq \
  "$(explain "$mixed_repo")" \
  "$(expected_explain "$mixed_repo" full 2 'exec canonical full gate (make gate)')" \
  "mixed diff fails closed"

fake_bin="$TMP/fake-bin"
calls="$TMP/calls"
mkdir -p "$fake_bin"

cat >"$fake_bin/runner" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
name="${0##*/}"
printf 'cwd=%s command=%s' "$PWD" "$name" >>"$GATE_DELTA_CALLS"
printf ' %s' "$@" >>"$GATE_DELTA_CALLS"
printf '\n' >>"$GATE_DELTA_CALLS"
if [[ "$name $*" == "${GATE_DELTA_FAIL_COMMAND:-}" ]]; then
  exit "${GATE_DELTA_FAIL_STATUS:-1}"
fi
EOF
chmod +x "$fake_bin/runner"
ln -s runner "$fake_bin/make"
ln -s runner "$fake_bin/npm"
ln -s runner "$fake_bin/go"

cat >"$fake_bin/python3" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf 'cwd=%s command=python3' "$PWD" >>"$GATE_DELTA_CALLS"
printf ' <%s>' "$@" >>"$GATE_DELTA_CALLS"
printf '\ncapacity-held=%s\n' "${BEAMFALL_GATE_CAPACITY_HELD:-}" >>"$GATE_DELTA_CALLS"
EOF
chmod +x "$fake_bin/python3"

: >"$calls"
full_status=0
(
  cd "$mixed_repo"
  GATE_DELTA_CALLS="$calls" \
    GATE_DELTA_FAIL_COMMAND="make gate" \
    GATE_DELTA_FAIL_STATUS=23 \
    PATH="$fake_bin:$PATH" \
    "$GATE_DELTA" >/dev/null 2>&1
) || full_status=$?
assert_eq "$full_status" 23 "full-gate exec status"
assert_eq \
  "$(<"$calls")" \
  "cwd=$mixed_repo command=make gate" \
  "full-gate exec command"

: >"$calls"
(
  cd "$web_repo"
  BEAMFALL_GATE_CAPACITY_HELD=0 \
    GATE_DELTA_CALLS="$calls" \
    PATH="$fake_bin:$PATH" \
    "$GATE_DELTA" >/dev/null
)
assert_eq \
  "$(<"$calls")" \
  "cwd=$web_repo command=python3 <$web_repo/script/gate-capacity.py> <--> <$GATE_DELTA>
capacity-held=1" \
  "web class enters capacity flock"

: >"$calls"
(
  cd "$web_repo"
  BEAMFALL_GATE_CAPACITY_HELD=1 \
    GATE_DELTA_CALLS="$calls" \
    PATH="$fake_bin:$PATH" \
    "$GATE_DELTA" >/dev/null
)
assert_eq \
  "$(<"$calls")" \
  "cwd=$web_repo command=make web-build
cwd=$web_repo/internal/web/app command=npm run check
cwd=$web_repo/internal/web/app command=npm test
cwd=$web_repo command=go build ./...
cwd=$web_repo command=go test -count=1 ./internal/web/... ./internal/server/..." \
  "web suite preserves go:embed coupling"

[[ -x "$GATE_DELTA" ]] || fail "$GATE_DELTA is not executable"
echo "gate-delta tests: passed"
