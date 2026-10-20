#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LINT="$ROOT/tools/gate/tracked-vs-gitignore.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

substitute_allowlist() {  # substitute_allowlist <src> <dst> <allowlist-body>
  python3 - "$1" "$2" "$3" <<'PY'
import re
import sys

src, dst, body = sys.argv[1:4]
with open(src, encoding="utf-8") as fh:
    text = fh.read()
text = re.sub(r'ALLOWLIST=\(.*?\n\)', f'ALLOWLIST=(\n{body}\n)', text, count=1, flags=re.S)
with open(dst, "w", encoding="utf-8") as fh:
    fh.write(text)
PY
  chmod +x "$2"
}

# The real repo, with its real ALLOWLIST, must stay clean (all current
# drift allowlisted, nothing stale).
"$LINT" >/dev/null

# For the generic mechanic tests below, use a copy with an EMPTY allowlist —
# this repo's real ALLOWLIST names real-repo paths that don't exist in a
# throwaway fixture repo, which would spuriously read as "stale".
substitute_allowlist "$LINT" "$TMP/empty-lint.sh" ""

mkdir -p "$TMP/clean"
git -C "$TMP/clean" init -b main >/dev/null
git -C "$TMP/clean" config user.email gate-test@example.invalid
git -C "$TMP/clean" config user.name "Gate Test"
echo hi >"$TMP/clean/README.md"
git -C "$TMP/clean" add README.md
git -C "$TMP/clean" commit -m init >/dev/null
BEAMFALL_GATE_ROOT="$TMP/clean" "$TMP/empty-lint.sh" >/dev/null

# A repo with a NEW tracked-but-ignored path (not in the allowlist) must fail.
mkdir -p "$TMP/dirty"
git -C "$TMP/dirty" init -b main >/dev/null
git -C "$TMP/dirty" config user.email gate-test@example.invalid
git -C "$TMP/dirty" config user.name "Gate Test"
echo "build/" >"$TMP/dirty/.gitignore"
mkdir -p "$TMP/dirty/build"
echo "artifact" >"$TMP/dirty/build/output.bin"
git -C "$TMP/dirty" add -f .gitignore build/output.bin
git -C "$TMP/dirty" commit -m "half-tracked drift fixture" >/dev/null
if BEAMFALL_GATE_ROOT="$TMP/dirty" "$TMP/empty-lint.sh" >/tmp/tracked-vs-gitignore-1.out 2>/tmp/tracked-vs-gitignore-1.err; then
  echo "expected a new tracked-but-ignored path to fail" >&2
  exit 1
fi
grep -q 'NEW tracked-but-ignored path: build/output.bin' /tmp/tracked-vs-gitignore-1.err

# The same file, allowlisted, must pass — and prove the allowlist mechanism
# itself (not just an empty one) works end to end.
substitute_allowlist "$LINT" "$TMP/allowed-lint.sh" '  "build/output.bin"'
BEAMFALL_GATE_ROOT="$TMP/dirty" "$TMP/allowed-lint.sh" >/dev/null

# A stale allowlist entry (path no longer reproduces) must fail — the
# allowlist is self-pruning, so it can only shrink.
substitute_allowlist "$LINT" "$TMP/stale-lint.sh" '  "never/reproduces/this/path.txt"'
if BEAMFALL_GATE_ROOT="$TMP/clean" "$TMP/stale-lint.sh" >/tmp/tracked-vs-gitignore-2.out 2>/tmp/tracked-vs-gitignore-2.err; then
  echo "expected a stale allowlist entry to fail" >&2
  exit 1
fi
grep -q 'stale allowlist entry.*never/reproduces/this/path.txt' /tmp/tracked-vs-gitignore-2.err

echo "tracked-vs-gitignore_test: OK"
