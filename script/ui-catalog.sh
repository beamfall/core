#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CATALOG_DIR="${ROOT}/tools/ui-catalog"
CACHE_DIR="${BEAMFALL_UI_CATALOG_CACHE_DIR:-${CATALOG_DIR}/node_modules/.cache/beamfall-ui-catalog}"
DEPENDENCY_STAMP="${CACHE_DIR}/dependencies.sha256"
BROWSER_STAMP="${CACHE_DIR}/chromium.sha256"

usage() {
  cat <<'USAGE'
usage: script/ui-catalog.sh capture|record

Bootstraps the web UI catalog toolchain and runs the requested catalog command.

Commands:
  capture   Capture current UI catalog artifacts and compare against baselines.
  record    Re-bless committed baselines from current renders.
USAGE
}

hash_stream() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
    return
  fi
  shasum -a 256 | awk '{print $1}'
}

hash_file() {
  hash_stream <"$1"
}

dependency_fingerprint() {
  {
    hash_file package.json
    hash_file package-lock.json
    node --version
    npm --version
  } | hash_stream
}

expected_playwright_version() {
  node -p "require('./package-lock.json').packages['node_modules/playwright'].version"
}

installed_playwright_version() {
  node -p "require('./node_modules/playwright/package.json').version"
}

playwright_browser_revision() {
  node -e '
    const browsers = require("./node_modules/playwright-core/browsers.json").browsers;
    const chromium = browsers.find(({ name }) => name === "chromium");
    if (!chromium?.revision) process.exit(1);
    process.stdout.write(String(chromium.revision));
  '
}

chromium_executable_path() {
  node -e '
    const { chromium } = require("@playwright/test");
    process.stdout.write(chromium.executablePath());
  '
}

dependencies_valid() {
  local expected installed
  npm ls --all --ignore-scripts --json >/dev/null 2>&1 || return 1
  expected="$(expected_playwright_version)" || return 1
  installed="$(installed_playwright_version)" || return 1
  [[ "$installed" == "$expected" ]] || return 1
  playwright_browser_revision >/dev/null
}

chromium_healthy() {
  local executable version
  executable="$(chromium_executable_path)" || return 1
  [[ -x "$executable" ]] || return 1
  version="$("$executable" --version 2>/dev/null)" || return 1
  [[ "$version" == *Chromium* || "$version" == *Chrome* ]]
}

write_stamp() {
  local path="$1" value="$2" temporary="${1}.tmp.$$"
  mkdir -p "$(dirname "$path")"
  printf '%s\n' "$value" >"$temporary"
  mv "$temporary" "$path"
}

bootstrap_dependencies() {
  local fingerprint
  fingerprint="$(dependency_fingerprint)"
  if [[ -f "$DEPENDENCY_STAMP" ]] &&
    [[ "$(cat "$DEPENDENCY_STAMP")" == "$fingerprint" ]] &&
    dependencies_valid; then
    return
  fi

  if [[ "${BEAMFALL_SKIP_NPM_CI:-0}" != "1" ]]; then
    npm ci --no-audit --no-fund >/dev/null
  elif [[ -f "$DEPENDENCY_STAMP" ]]; then
    echo "UI catalog dependency inputs changed while BEAMFALL_SKIP_NPM_CI=1" >&2
    return 1
  elif ! dependencies_valid; then
    echo "UI catalog dependencies are stale or invalid while BEAMFALL_SKIP_NPM_CI=1" >&2
    return 1
  fi
  dependencies_valid || {
    echo "npm ci did not produce the lockfile-pinned UI catalog dependency tree" >&2
    return 1
  }
  write_stamp "$DEPENDENCY_STAMP" "$fingerprint"
}

browser_fingerprint() {
  {
    dependency_fingerprint
    installed_playwright_version
    playwright_browser_revision
    node -p "process.platform + '/' + process.arch"
  } | hash_stream
}

bootstrap_browser() {
  local fingerprint
  fingerprint="$(browser_fingerprint)"
  if [[ -f "$BROWSER_STAMP" ]] &&
    [[ "$(cat "$BROWSER_STAMP")" == "$fingerprint" ]] &&
    chromium_healthy; then
    return
  fi

  if [[ "${BEAMFALL_SKIP_PLAYWRIGHT_INSTALL:-0}" != "1" ]]; then
    npx --no-install playwright install chromium >/dev/null
  elif [[ -f "$BROWSER_STAMP" ]]; then
    echo "Playwright Chromium inputs changed while BEAMFALL_SKIP_PLAYWRIGHT_INSTALL=1" >&2
    return 1
  elif ! chromium_healthy; then
    echo "Playwright Chromium is missing or corrupt while BEAMFALL_SKIP_PLAYWRIGHT_INSTALL=1" >&2
    return 1
  fi
  chromium_healthy || {
    echo "Playwright Chromium is missing or corrupt after installation" >&2
    return 1
  }
  write_stamp "$BROWSER_STAMP" "$fingerprint"
}

if [[ $# -ne 1 ]]; then
  usage >&2
  exit 2
fi

case "$1" in
  capture|record)
    cd "$CATALOG_DIR"
    mkdir -p "$CACHE_DIR"
    bootstrap_dependencies
    bootstrap_browser
    npm run "$1"
    ;;
  -h|--help)
    usage
    ;;
  *)
    echo "unknown UI catalog command: $1" >&2
    usage >&2
    exit 2
    ;;
esac
