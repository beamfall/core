#!/usr/bin/env bash
# Re-record the live macOS-rendered Playwright visual goldens under
# internal/web/app/e2e/goldens/ without modifying the immutable archived
# parity baselines or patching expectArchivedVisualParity (CORE-3518).
#
# Usage: script/web-ui-e2e-rerecord-goldens.sh [playwright flags...]
#   (defaults to every project and test that writes e2e/goldens/; pass
#    e.g. `--project=manage e2e/manage.spec.ts` to re-record a slice)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP_DIR="${ROOT}/internal/web/app"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "FAIL web-ui-e2e-rerecord-goldens: visual goldens are macOS-rendered (CORE-1112); run on macOS" >&2
  exit 1
fi

cd "${APP_DIR}"

if ! node e2e/lib/require-pinned-chromium.mjs --package-only >/dev/null 2>&1; then
  npm ci --no-audit --no-fund
fi
if ! node e2e/lib/require-pinned-chromium.mjs >/dev/null 2>&1; then
  npm exec playwright install chromium
  node e2e/lib/require-pinned-chromium.mjs
fi

if [[ -z "${BEAMFALL_E2E_URL:-}" && -z "${BEAMFALL_E2E_PORT:-}" ]]; then
  BEAMFALL_E2E_PORT="$(node -e 'const net=require("node:net");const s=net.createServer();s.listen(0,"127.0.0.1",()=>{process.stdout.write(String(s.address().port));s.close();});')"
  export BEAMFALL_E2E_PORT
fi

export BEAMFALL_WEB_E2E_SUITE="${BEAMFALL_WEB_E2E_SUITE:-smoke}"
export BEAMFALL_WEB_E2E_RENDER_PHASE=1
export BEAMFALL_UPDATE_WEB_GOLDENS=1
export LANG=en_US.UTF-8
export LC_ALL=en_US.UTF-8
export TZ=UTC

extra_args=()
has_grep=false
has_scope=false
expect_value=false
for arg in "$@"; do
  if [[ "${expect_value}" == true ]]; then
    expect_value=false
    continue
  fi
  case "${arg}" in
    --grep|-g|--grep-invert)
      has_grep=true
      expect_value=true
      ;;
    --grep=*|-g=*|--grep-invert=*)
      has_grep=true
      ;;
    --project)
      has_scope=true
      expect_value=true
      ;;
    --project=*)
      has_scope=true
      ;;
    -*)
      ;;
    *)
      has_scope=true
      ;;
  esac
done

if [[ "${has_scope}" == false ]]; then
  extra_args+=(
    --project=console
    --project=manage
    --project=player-desktop
    --project=player-mobile
    --project=viewer
    --project=ui-states
  )
fi
if [[ "${has_grep}" == false ]]; then
  extra_args+=(
    --grep
    "visual parity|Home features artwork and fits square, landscape and portrait cards|UI state inventory .*golden"
  )
fi

exec npm run e2e -- "${extra_args[@]}" --update-snapshots "$@"
