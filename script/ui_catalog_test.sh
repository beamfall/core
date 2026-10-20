#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

FIXTURE="$TMP/repo"
CATALOG="$FIXTURE/tools/ui-catalog"
BIN="$TMP/bin"
mkdir -p "$FIXTURE/script" "$CATALOG" "$BIN"
cp "$ROOT/script/ui-catalog.sh" "$FIXTURE/script/ui-catalog.sh"
chmod +x "$FIXTURE/script/ui-catalog.sh"

cat >"$CATALOG/package.json" <<'JSON'
{"name":"catalog-fixture","private":true,"devDependencies":{"playwright":"1.60.0"}}
JSON
cat >"$CATALOG/package-lock.json" <<'JSON'
{"lockfileVersion":3,"packages":{"node_modules/playwright":{"version":"1.60.0"}}}
JSON

REAL_NODE="$(command -v node)"
export REAL_NODE
printf '%s\n' v24.18.0 >"$TMP/node-version"
printf '%s\n' 11.16.0 >"$TMP/npm-version"
export FAKE_NODE_VERSION_FILE="$TMP/node-version"
export FAKE_NPM_VERSION_FILE="$TMP/npm-version"
export FAKE_BROWSER_PATH="$TMP/chromium"
export FAKE_NPM_CI_LOG="$TMP/npm-ci"
export FAKE_NPM_RUN_LOG="$TMP/npm-run"
export FAKE_PLAYWRIGHT_INSTALL_LOG="$TMP/playwright-install"

cat >"$BIN/node" <<'SH'
#!/usr/bin/env bash
if [[ "${1:-}" == "--version" ]]; then
  cat "$FAKE_NODE_VERSION_FILE"
  exit 0
fi
exec "$REAL_NODE" "$@"
SH

cat >"$BIN/npm" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
case "${1:-}" in
  --version)
    cat "$FAKE_NPM_VERSION_FILE"
    ;;
  ci)
    printf 'ci\n' >>"$FAKE_NPM_CI_LOG"
    version="$("$REAL_NODE" -p "require('./package-lock.json').packages['node_modules/playwright'].version")"
    mkdir -p node_modules/playwright node_modules/playwright-core node_modules/@playwright/test
    printf '{"version":"%s"}\n' "$version" >node_modules/playwright/package.json
    printf '{"browsers":[{"name":"chromium","revision":"1253"}]}\n' >node_modules/playwright-core/browsers.json
    cat >node_modules/@playwright/test/index.js <<'JS'
exports.chromium = { executablePath: () => process.env.FAKE_BROWSER_PATH };
JS
    ;;
  ls)
    expected="$("$REAL_NODE" -p "require('./package-lock.json').packages['node_modules/playwright'].version")"
    installed="$("$REAL_NODE" -p "require('./node_modules/playwright/package.json').version" 2>/dev/null || true)"
    [[ "$installed" == "$expected" ]]
    [[ -f node_modules/playwright-core/browsers.json ]]
    [[ -f node_modules/@playwright/test/index.js ]]
    ;;
  run)
    printf '%s\n' "$2" >>"$FAKE_NPM_RUN_LOG"
    ;;
  *)
    echo "unexpected fake npm command: $*" >&2
    exit 2
    ;;
esac
SH

cat >"$BIN/npx" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
[[ "$*" == "--no-install playwright install chromium" ]]
printf 'install\n' >>"$FAKE_PLAYWRIGHT_INSTALL_LOG"
cat >"$FAKE_BROWSER_PATH" <<'BROWSER'
#!/usr/bin/env bash
echo "Chromium 145.0.0.0"
BROWSER
chmod +x "$FAKE_BROWSER_PATH"
SH
chmod +x "$BIN/node" "$BIN/npm" "$BIN/npx"

run_catalog() {
  PATH="$BIN:$PATH" "$FIXTURE/script/ui-catalog.sh" capture >/dev/null
}

count_lines() {
  local path="$1"
  [[ -f "$path" ]] || {
    printf '0\n'
    return
  }
  wc -l <"$path" | tr -d ' '
}

assert_counts() {
  local npm_ci="$1" browser_install="$2" catalog_runs="$3"
  [[ "$(count_lines "$FAKE_NPM_CI_LOG")" == "$npm_ci" ]]
  [[ "$(count_lines "$FAKE_PLAYWRIGHT_INSTALL_LOG")" == "$browser_install" ]]
  [[ "$(count_lines "$FAKE_NPM_RUN_LOG")" == "$catalog_runs" ]]
}

run_catalog
assert_counts 1 1 1
run_catalog
assert_counts 1 1 2

"$REAL_NODE" -e '
  const fs = require("fs");
  const path = process.argv[1];
  const lock = JSON.parse(fs.readFileSync(path));
  lock.mutation = 1;
  fs.writeFileSync(path, JSON.stringify(lock));
' "$CATALOG/package-lock.json"
run_catalog
assert_counts 2 2 3

printf '%s\n' v24.19.0 >"$FAKE_NODE_VERSION_FILE"
run_catalog
assert_counts 3 3 4

printf '%s\n' 11.17.0 >"$FAKE_NPM_VERSION_FILE"
run_catalog
assert_counts 4 4 5

printf '{"version":"0.0.0"}\n' >"$CATALOG/node_modules/playwright/package.json"
run_catalog
assert_counts 5 5 6

printf '{"browsers":[{"name":"chromium","revision":"1254"}]}\n' \
  >"$CATALOG/node_modules/playwright-core/browsers.json"
run_catalog
assert_counts 5 6 7

rm "$FAKE_BROWSER_PATH"
run_catalog
assert_counts 5 7 8

printf '#!/usr/bin/env bash\nexit 1\n' >"$FAKE_BROWSER_PATH"
chmod +x "$FAKE_BROWSER_PATH"
run_catalog
assert_counts 5 8 9

rm -f "$CATALOG/node_modules/.cache/beamfall-ui-catalog/"*.sha256
BEAMFALL_SKIP_NPM_CI=1 BEAMFALL_SKIP_PLAYWRIGHT_INSTALL=1 run_catalog
assert_counts 5 8 10

printf '%s\n' v24.20.0 >"$FAKE_NODE_VERSION_FILE"
if BEAMFALL_SKIP_NPM_CI=1 BEAMFALL_SKIP_PLAYWRIGHT_INSTALL=1 run_catalog 2>/dev/null; then
  echo "skip flags accepted changed stamped inputs" >&2
  exit 1
fi
assert_counts 5 8 10

echo "ui catalog bootstrap tests: passed"
