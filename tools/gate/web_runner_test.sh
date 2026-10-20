#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

cp "$ROOT/internal/web/app/package.json" "$TMP/package.json"

mkdir -p "$TMP/bin"
for command in biome tsc vite vitest; do
  command_path="$TMP/bin/$command"
  # shellcheck disable=SC2016
  printf '#!/usr/bin/env bash\nprintf "%%s\\n" "$(basename "$0")" >>"$WEB_RUNNER_SPY_LOG"\n' >"$command_path"
  chmod +x "$command_path"
done

export WEB_RUNNER_SPY_LOG="$TMP/commands"
PATH="$TMP/bin:$PATH" npm --prefix "$TMP" run verify:build >/dev/null
PATH="$TMP/bin:$PATH" npm --prefix "$TMP" run test >/dev/null

for command in biome tsc vite vitest; do
  count="$(grep -c "^${command}$" "$WEB_RUNNER_SPY_LOG" || true)"
  [[ "$count" == 1 ]] || {
    echo "web verification graph ran $command $count times, want 1" >&2
    exit 1
  }
done

python3 - "$ROOT/tools/gate/gate.sh" <<'PY'
from pathlib import Path
import sys

gate = Path(sys.argv[1]).read_text()
expected = {
    'run_step "web app build" run_web_app verify:build': 1,
    'run_scoped_step "web app typecheck + tests" web run_web_app test': 1,
}
for command, count in expected.items():
    actual = gate.count(command)
    if actual != count:
        raise SystemExit(f"gate must schedule {command!r} exactly once, got {actual}")
for duplicate in (
    "npm --prefix internal/web/app run check",
    "npm --prefix internal/web/app run typecheck",
    "npm --prefix internal/web/app run build",
):
    if duplicate in gate:
        raise SystemExit(f"gate bypasses the composable web graph: {duplicate}")
PY

echo "web runner tests: passed"
