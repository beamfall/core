#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/clean-canary-test.XXXXXX")"
trap 'rm -rf "$tmp"' EXIT

mkdir -p "$tmp/ok" "$tmp/red"
git -C "$tmp/ok" init -q
printf '#!/usr/bin/env bash\nexit 0\n' > "$tmp/ok/gate.sh"
chmod +x "$tmp/ok/gate.sh"
git -C "$tmp/ok" add gate.sh
git -C "$tmp/ok" -c user.name=t -c user.email=t@example.com commit -qm init
ok_head="$(git -C "$tmp/ok" rev-parse HEAD)"
printf '#!/usr/bin/env bash\nexit 9\n' > "$tmp/ok/gate.sh"

git -C "$tmp/red" init -q
printf '#!/usr/bin/env bash\necho nope >&2\nexit 7\n' > "$tmp/red/gate.sh"
chmod +x "$tmp/red/gate.sh"
git -C "$tmp/red" add gate.sh
git -C "$tmp/red" -c user.name=t -c user.email=t@example.com commit -qm init

commands="$tmp/gates.tsv"
{
  printf 'ok\t%s\t./gate.sh\tno\n' "$tmp/ok"
  printf 'red\t%s\t./gate.sh\tyes\n' "$tmp/red"
} > "$commands"

"$ROOT/script/clean-checkout-canary.sh" --commands "$commands" --repo ok --dry-run > "$tmp/ok.out"
grep -q "^PASS ok dry_run commit=$ok_head " "$tmp/ok.out"
"$ROOT/script/clean-checkout-canary.sh" --commands "$commands" --repo ok | grep -q '^PASS ok '
if "$ROOT/script/clean-checkout-canary.sh" --commands "$commands" --repo red > "$tmp/red.out" 2>&1; then
  echo "FAIL red repo unexpectedly passed" >&2
  exit 1
fi
grep -q '^FAIL red exit=7 ' "$tmp/red.out"
echo "clean-checkout-canary tests: passed"

# ── release mode (RELGATE-NONVAC-2) ─────────────────────────────────────────
# Release mode is a different contract: every row runs, each at the immutable
# revision its pin names, from the reviewed committed table only. The controls
# below are the inputs today's canary accepted while proving nothing.
rel="$tmp/release"
mkdir -p "$rel/script" "$rel/testing/release-gates"
cp "$ROOT/script/clean-checkout-canary.sh" "$rel/script/"
git -C "$rel" init -q

# A fixture repo with two commits: the OLD one fails its gate, the NEW one passes.
# Pinning to the old commit must therefore RED — that is what proves the pin is
# checked out rather than merely recorded.
mkdir -p "$tmp/pinned"
git -C "$tmp/pinned" init -q
printf '#!/usr/bin/env bash\nexit 3\n' > "$tmp/pinned/gate.sh"
chmod +x "$tmp/pinned/gate.sh"
git -C "$tmp/pinned" add gate.sh
git -C "$tmp/pinned" -c user.name=t -c user.email=t@example.com commit -qm old
old_pin="$(git -C "$tmp/pinned" rev-parse HEAD)"
printf '#!/usr/bin/env bash\nexit 0\n' > "$tmp/pinned/gate.sh"
git -C "$tmp/pinned" add gate.sh
git -C "$tmp/pinned" -c user.name=t -c user.email=t@example.com commit -qm new
new_pin="$(git -C "$tmp/pinned" rev-parse HEAD)"

# The SELF row means "the revision under release", i.e. this checkout's own HEAD,
# so its source must be the fixture repo itself.
printf '#!/usr/bin/env bash\nexit 0\n' > "$rel/gate.sh"
chmod +x "$rel/gate.sh"

release_table() {  # release_table <pin-for-remote-row>
  {
    printf '# repo\tsource\tgate\tcontract_bearing\tpin\n'
    printf 'self\t%s\t./gate.sh\tno\tSELF\n' "$rel"
    printf 'pinned\tfile://%s\t./gate.sh\tyes\t%s\n' "$tmp/pinned" "$1"
  } > "$rel/testing/release-gates/gate-commands.tsv"
}
commit_table() {
  git -C "$rel" add -A
  git -C "$rel" -c user.name=t -c user.email=t@example.com commit -q --allow-empty -m table
}
release_run() { "$rel/script/clean-checkout-canary.sh" --release "$@"; }

release_table "$new_pin"
commit_table

# positive: both rows pass and every line records the revision actually tested.
release_run > "$tmp/rel.out" || { cat "$tmp/rel.out" >&2; echo "release canary rejected a valid pinned table" >&2; exit 1; }
grep -q "^PASS pinned .*commit=$new_pin " "$tmp/rel.out" || { cat "$tmp/rel.out" >&2; echo "release PASS line must record the pinned commit" >&2; exit 1; }
grep -q '^PASS self .*commit=' "$tmp/rel.out" || { cat "$tmp/rel.out" >&2; echo "SELF row must record its commit" >&2; exit 1; }

refuses() {  # refuses <description> <args...>
  local what="$1"; shift
  if release_run "$@" > "$tmp/rel.out" 2>&1; then
    cat "$tmp/rel.out" >&2
    echo "release canary accepted $what" >&2
    exit 1
  fi
}
refuses "--dry-run" --dry-run
refuses "a --repo filter" --repo self
refuses "a caller-supplied command table" --commands "$commands"
if CLEAN_CHECKOUT_COMMANDS="$commands" release_run > "$tmp/rel.out" 2>&1; then
  echo "release canary accepted a command table from the environment" >&2; exit 1
fi

# The pin must be checked out, not just recorded: the old commit's gate exits 3.
release_table "$old_pin"; commit_table
if release_run > "$tmp/rel.out" 2>&1; then
  cat "$tmp/rel.out" >&2
  echo "release canary passed while pinned to a failing revision — the pin is not checked out" >&2
  exit 1
fi
grep -q "^FAIL pinned exit=3 .*commit=$old_pin " "$tmp/rel.out" || { cat "$tmp/rel.out" >&2; echo "expected the pinned revision's own gate result" >&2; exit 1; }

# An empty pin is the state every sibling row is in today.
release_table ""; commit_table
release_run > "$tmp/rel.out" 2>&1 || true
grep -q '^FAIL pinned unpinned ' "$tmp/rel.out" || { cat "$tmp/rel.out" >&2; echo "an unpinned row must fail in release mode" >&2; exit 1; }

# A well-formed pin that does not exist must fail loudly, never fall back.
release_table 0123456789012345678901234567890123456789; commit_table
release_run > "$tmp/rel.out" 2>&1 || true
grep -q '^FAIL pinned pin_unresolved=' "$tmp/rel.out" || { cat "$tmp/rel.out" >&2; echo "an unresolvable pin must fail" >&2; exit 1; }

# A working-tree edit must not be able to shrink the row set or swap a gate.
release_table "$new_pin"; commit_table
printf '# repo\tsource\tgate\tcontract_bearing\tpin\nself\t%s\ttrue\tno\tSELF\n' "$tmp/ok" \
  > "$rel/testing/release-gates/gate-commands.tsv"
if release_run > "$tmp/rel.out" 2>&1; then
  cat "$tmp/rel.out" >&2
  echo "release canary accepted an uncommitted command table" >&2
  exit 1
fi
grep -q 'uncommitted edits' "$tmp/rel.out" || { cat "$tmp/rel.out" >&2; echo "expected the uncommitted-table refusal" >&2; exit 1; }

# A duplicate row must not silently double-count toward the inventory.
{
  printf '# repo\tsource\tgate\tcontract_bearing\tpin\n'
  printf 'self\t%s\t./gate.sh\tno\tSELF\n' "$tmp/ok"
  printf 'self\t%s\t./gate.sh\tno\tSELF\n' "$tmp/ok"
} > "$rel/testing/release-gates/gate-commands.tsv"
commit_table
release_run > "$tmp/rel.out" 2>&1 || true
grep -q '^FAIL self duplicate_row' "$tmp/rel.out" || { cat "$tmp/rel.out" >&2; echo "a duplicate row must be rejected" >&2; exit 1; }

echo "clean-checkout-canary release tests: passed"
