#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCOPE="$ROOT/tools/gate/backup-restore-drill-scope.sh"
TRIGGER="$ROOT/tools/gate/backup-restore-trigger.sh"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

git -C "$TMP" init -q -b main
git -C "$TMP" config user.email test@example.com
git -C "$TMP" config user.name test
mkdir -p "$TMP/internal/backup" "$TMP/internal/other"
echo one >"$TMP/README.md"
git -C "$TMP" add README.md
git -C "$TMP" commit -q -m init

select_for() {
  local branch="$1" args=() path
  git -C "$TMP" checkout -q "$branch"
  while IFS= read -r path; do
    [ -n "$path" ] && args+=(--changed-file "$path")
  done < <(cd "$TMP" && "$SCOPE")
  "$TRIGGER" --mode pr "${args[@]}"
}

# A branch that only touches backup code must select the bounded PR corpus.
git -C "$TMP" checkout -q -b lane-matching
echo two >"$TMP/internal/backup/x.go"
git -C "$TMP" add internal/backup/x.go
git -C "$TMP" commit -q -m "touch backup"
test "$(select_for lane-matching)" = "oldest-supported latest-1 latest"

# A branch that never touches backup code must not select the drill.
git -C "$TMP" checkout -q main
git -C "$TMP" checkout -q -b lane-nonmatching
echo two >"$TMP/internal/other/x.go"
git -C "$TMP" add internal/other/x.go
git -C "$TMP" commit -q -m "touch other"
test "$(select_for lane-nonmatching)" = none

# Running directly on main (no branch ahead of itself) must fall back to the
# parent commit so a just-merged backup-touching commit still selects.
git -C "$TMP" checkout -q main
mkdir -p "$TMP/internal/backup"
echo three >"$TMP/internal/backup/y.go"
git -C "$TMP" add internal/backup/y.go
git -C "$TMP" commit -q -m "touch backup on main"
test "$(select_for main)" = "oldest-supported latest-1 latest"

# CORE-1299: a local main behind origin/main must not widen the lane's scope.
# origin/main gains a backup commit the stale local main never saw; a lane cut
# from origin/main that touches only other code must not select the drill.
git -C "$TMP" checkout -q -b upstream
echo four >"$TMP/internal/backup/z.go"
git -C "$TMP" add internal/backup/z.go
git -C "$TMP" commit -q -m "backup change landed by PR"
git -C "$TMP" update-ref refs/remotes/origin/main HEAD
git -C "$TMP" checkout -q -b lane-stale-main
mkdir -p "$TMP/internal/other"
echo five >"$TMP/internal/other/w.go"
git -C "$TMP" add internal/other/w.go
git -C "$TMP" commit -q -m "touch other on top of origin/main"
test "$(select_for lane-stale-main)" = none
# Control: without origin/main the stale local main is the base and over-selects.
git -C "$TMP" update-ref -d refs/remotes/origin/main
test "$(select_for lane-stale-main)" = "oldest-supported latest-1 latest"
