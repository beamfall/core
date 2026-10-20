#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
LINT="$ROOT/tools/gate/go-test-count1-check.sh"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

substitute_line_allowlist() {  # substitute_line_allowlist <src> <dst> <allowlist-body>
  python3 - "$1" "$2" "$3" <<'PY'
import re
import sys

src, dst, body = sys.argv[1:4]
with open(src, encoding="utf-8") as fh:
    text = fh.read()
text = re.sub(r'LINE_ALLOWLIST=\(.*?\n\)', f'LINE_ALLOWLIST=(\n{body}\n)', text, count=1, flags=re.S)
with open(dst, "w", encoding="utf-8") as fh:
    fh.write(text)
PY
  chmod +x "$2"
}

new_fixture() {  # new_fixture <name> -> echoes fixture root path
  local name="$1"
  local dir="$TMP/$name"
  mkdir -p "$dir/tools/gate" "$dir/script"
  echo "$dir"
}

# The real repo, with its real LINE_ALLOWLIST, must stay clean (every
# non-test-file go test invocation already carries -count=1).
"$LINT" >/dev/null

# For the generic mechanic tests below, use a copy with an EMPTY
# LINE_ALLOWLIST — the real allowlist names a real-repo line (the ffmpeg -c
# fixture) that doesn't exist in a throwaway fixture repo, which would
# spuriously read as "stale".

# --- catches a missing -count=1 -------------------------------------------
fixture="$(new_fixture missing)"
substitute_line_allowlist "$LINT" "$fixture/tools/gate/go-test-count1-check.sh" ""
cat >"$fixture/script/example.sh" <<'SH'
#!/usr/bin/env bash
go test -race ./internal/example/...
SH
if (cd "$fixture" && ./tools/gate/go-test-count1-check.sh) >/tmp/go-test-count1-check-1.out 2>/tmp/go-test-count1-check-1.err; then
  echo "expected a go test invocation missing -count=1 to fail" >&2
  exit 1
fi
grep -q 'script/example.sh:2:.*go test -race' /tmp/go-test-count1-check-1.err

# --- accepts -count=1, ignores comments and label-only strings ------------
fixture="$(new_fixture clean)"
substitute_line_allowlist "$LINT" "$fixture/tools/gate/go-test-count1-check.sh" ""
cat >"$fixture/script/example.sh" <<'SH'
#!/usr/bin/env bash
# comment mentioning go test -race ./internal/example/... must not count
start_parallel_step pid_x "go test + line-coverage ratchet" go run ./cmd/covratchet
go test -race -count=1 ./internal/example/...
SH
(cd "$fixture" && ./tools/gate/go-test-count1-check.sh) >/dev/null

# --- test scripts are audited too (only the checker's own self-test is exempt)
fixture="$(new_fixture selftest)"
substitute_line_allowlist "$LINT" "$fixture/tools/gate/go-test-count1-check.sh" ""
cat >"$fixture/script/example_test.sh" <<'SH'
#!/usr/bin/env bash
go test -race ./internal/example/...
SH
if (cd "$fixture" && ./tools/gate/go-test-count1-check.sh) >/dev/null 2>&1; then
  echo "FAIL: uncounted go test inside an executable *_test.sh escaped the audit" >&2
  exit 1
fi
cat >"$fixture/script/example_test.sh" <<'SH'
#!/usr/bin/env bash
go test -race -count=1 ./internal/example/...
SH
(cd "$fixture" && ./tools/gate/go-test-count1-check.sh) >/dev/null

# --- honors the named FILE_EXEMPT paths ------------------------------------
fixture="$(new_fixture exempt)"
substitute_line_allowlist "$LINT" "$fixture/tools/gate/go-test-count1-check.sh" ""
cat >"$fixture/script/io-e2e.sh" <<'SH'
#!/usr/bin/env bash
go test -tags io_e2e ./internal/ioe2e/... "$@"
SH
(cd "$fixture" && ./tools/gate/go-test-count1-check.sh) >/dev/null

# --- LINE_ALLOWLIST is self-pruning: a stale entry fails -------------------
fixture="$(new_fixture stale)"
substitute_line_allowlist "$LINT" "$fixture/tools/gate/go-test-count1-check.sh" \
  "  'go test -c ./never/reproduces/this'"
cat >"$fixture/script/example.sh" <<'SH'
#!/usr/bin/env bash
go test -race -count=1 ./internal/example/...
SH
if (cd "$fixture" && ./tools/gate/go-test-count1-check.sh) >/tmp/go-test-count1-check-2.out 2>/tmp/go-test-count1-check-2.err; then
  echo "expected a stale LINE_ALLOWLIST entry to fail" >&2
  exit 1
fi
grep -q 'stale allowlist entry' /tmp/go-test-count1-check-2.err

echo "go-test-count1-check_test: OK"
