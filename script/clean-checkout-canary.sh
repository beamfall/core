#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CANONICAL_COMMANDS="$ROOT/testing/release-gates/gate-commands.tsv"
COMMANDS="${CLEAN_CHECKOUT_COMMANDS:-$CANONICAL_COMMANDS}"
default_workspace="$(cd "$ROOT/.." && pwd)"
case "$ROOT" in
  */.roadmap-worktrees/*/*) default_workspace="${ROOT%%/.roadmap-worktrees/*}" ;;
esac
WORKSPACE="${BEAMFALL_WORKSPACE:-$default_workspace}"

usage() {
  cat >&2 <<'USAGE'
usage: script/clean-checkout-canary.sh [--repo NAME] [--commands FILE] [--dry-run] [--release]

Clones each recorded repo into a temp dir, runs its recorded gate in that clean
checkout, and reports PASS/FAIL lines. Set CLEAN_CANARY_KEEP_TMP=1 to keep clones.

--release (implied by GATE_MODE=release) is the release contract: every row runs,
each at the immutable revision its `pin` column names, from the canonical command
table only. Overrides that could shrink the proof are refused, not honoured.
USAGE
}

only_repo=""
dry_run=0
release=0
[ "${GATE_MODE:-}" = release ] && release=1
commands_overridden=0
[ -n "${CLEAN_CHECKOUT_COMMANDS:-}" ] && commands_overridden=1
while [ $# -gt 0 ]; do
  case "$1" in
    --repo) only_repo="$2"; shift 2 ;;
    --commands) COMMANDS="$2"; commands_overridden=1; shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    --release) release=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage; exit 2 ;;
  esac
done

# Release mode takes no runtime override. A caller-supplied table, a single-repo
# filter, or a dry run would each let a green result be produced without the work
# being done — the same vacuity the pins exist to close.
release_refuse() {
  echo "clean-checkout-canary: release mode refuses $1" >&2
  exit 2
}
if [ "$release" = 1 ]; then
  [ "$commands_overridden" = 0 ] || release_refuse "a caller-supplied command table"
  [ -z "$only_repo" ] || release_refuse "a --repo filter; every row must run"
  [ "$dry_run" = 0 ] || release_refuse "--dry-run; it proves nothing"
fi

[ -f "$COMMANDS" ] || { echo "clean-checkout-canary: missing command table: $COMMANDS" >&2; exit 2; }

# The table is the inventory AND the per-repo gate command, so in release mode it
# must be the reviewed, committed one: a working-tree edit could otherwise delete
# rows or swap a gate for `true` at run time.
require_tracked_table() {
  git -C "$ROOT" ls-files --error-unmatch "$COMMANDS" >/dev/null 2>&1 \
    || { echo "clean-checkout-canary: command table is not git-tracked: $COMMANDS" >&2; exit 2; }
  git -C "$ROOT" diff --quiet HEAD -- "$COMMANDS" \
    || { echo "clean-checkout-canary: command table has uncommitted edits: $COMMANDS" >&2; exit 2; }
}
[ "$release" = 0 ] || require_tracked_table

self_head="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || true)"

# resolve_pin turns a table pin into the concrete revision to test. SELF names the
# revision under release, which is the invoking checkout's HEAD.
resolve_pin() {
  local pin="$1"
  [ "$pin" != SELF ] || { printf '%s' "$self_head"; return 0; }
  printf '%s' "$pin"
}

pin_is_immutable() {
  printf '%s' "$1" | grep -Eq '^[0-9a-f]{40}$'
}

is_git_repo() {
  git -C "$1" rev-parse --git-dir >/dev/null 2>&1
}

tmp="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-clean-canary.XXXXXX")"
cleanup() {
  [ "${CLEAN_CANARY_KEEP_TMP:-0}" = 1 ] || rm -rf "$tmp"
}
trap cleanup EXIT

fail=0
seen=0
expected=0
declare_seen=""
while IFS=$'\t' read -r repo source gate contract pin; do
  case "$repo" in ""|\#*) continue ;; esac
  expected=$((expected + 1))
  [ -z "$only_repo" ] || [ "$repo" = "$only_repo" ] || continue
  case " $declare_seen " in
    *" $repo "*) echo "FAIL $repo duplicate_row"; fail=1; continue ;;
  esac
  declare_seen="$declare_seen $repo"
  seen=$((seen + 1))
  src="$source"
  case "$src" in
    /*|file://*) ;;
    *) src="$WORKSPACE/${source#../}" ;;
  esac
  case "$src" in
    file://*) ;;
    *) is_git_repo "$src" || src="${CLEAN_CANARY_REMOTE_PREFIX:-https://github.com/beamfall}/$repo.git" ;;
  esac
  revision=""
  if [ "$release" = 1 ]; then
    revision="$(resolve_pin "${pin:-}")"
    if ! pin_is_immutable "$revision"; then
      echo "FAIL $repo unpinned pin=${pin:-<empty>} contract_bearing=$contract"
      fail=1
      continue
    fi
  fi
  clone="$tmp/$repo"
  if ! git clone --quiet --no-local "$src" "$clone"; then
    echo "FAIL $repo clone_source=$src contract_bearing=$contract"
    fail=1
    continue
  fi
  # Release mode tests the pinned revision and nothing else. Outside it, a local
  # source may be on a detached Actions checkout while its symbolic HEAD still
  # names the default branch: test the exact commit that triggered the run.
  if [ "$release" = 1 ]; then
    if ! git -C "$clone" checkout --quiet --detach "$revision" 2>/dev/null; then
      echo "FAIL $repo pin_unresolved=$revision clone_source=$src contract_bearing=$contract"
      fail=1
      continue
    fi
  elif is_git_repo "$src"; then
    source_head="$(git -C "$src" rev-parse HEAD)"
    if ! git -C "$clone" checkout --quiet --detach "$source_head"; then
      echo "FAIL $repo checkout=$source_head clone_source=$src contract_bearing=$contract"
      fail=1
      continue
    fi
  fi
  commit="$(git -C "$clone" rev-parse HEAD)"
  if [ "$release" = 1 ] && [ "$commit" != "$revision" ]; then
    echo "FAIL $repo pin_mismatch=$revision checked_out=$commit contract_bearing=$contract"
    fail=1
    continue
  fi
  if [ "$dry_run" = 1 ]; then
    echo "PASS $repo dry_run commit=$commit gate=$gate contract_bearing=$contract"
    continue
  fi
  start="$SECONDS"
  log="$tmp/$repo.log"
  # GATE_MODE=release is Core's own gate contract. Sibling gates must observe
  # neither release signal: under Actions every child would otherwise inherit
  # GITHUB_WORKFLOW=release-gate and silently switch to a mode defined and tested
  # here only for Core.
  row_mode=""
  [ "$release" = 1 ] && [ "${pin:-}" = SELF ] && row_mode=release
  set +e
  ( cd "$clone" && GATE_MODE="$row_mode" GITHUB_WORKFLOW="" eval "$gate" ) >"$log" 2>&1
  status=$?
  set -e
  elapsed=$((SECONDS - start))
  if [ "$status" -eq 0 ]; then
    echo "PASS $repo elapsed_s=$elapsed commit=$commit gate=$gate contract_bearing=$contract"
  else
    echo "FAIL $repo exit=$status elapsed_s=$elapsed commit=$commit gate=$gate contract_bearing=$contract"
    tail -n "${CLEAN_CANARY_LOG_TAIL:-40}" "$log" | sed "s/^/  $repo | /"
    fail=1
  fi
done < "$COMMANDS"

[ "$seen" -gt 0 ] || { echo "clean-checkout-canary: no repos matched" >&2; exit 2; }
if [ "$release" = 1 ] && [ "$seen" -ne "$expected" ]; then
  echo "clean-checkout-canary: release mode ran $seen of $expected rows" >&2
  exit 2
fi
exit "$fail"
