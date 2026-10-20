#!/usr/bin/env bash
# Optional cross-repository contract lane. Runs only when a sibling workspace
# checkout with contracts/gate.sh and beamfall-web is present; otherwise skips.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
common_dir="$(git -C "$root" rev-parse --path-format=absolute --git-common-dir)"
workspace="$(cd "$(dirname "$common_dir")/.." && pwd)"
contracts_root="$workspace"
web_root="$workspace/beamfall-web"

lane="$contracts_root/contracts/gate.sh"
if [[ ! -f "$lane" || ! -f "$web_root/script/check-mgmt-parity.sh" ]]; then
  echo "SKIP workspace contracts lane: sibling workspace checkout unavailable"
  exit 0
fi

echo "RUN workspace contracts lane"
BEAMFALL_SERVER_REPO="$root" BEAMFALL_WEB_REPO="$web_root" bash "$lane" "$@"
echo "PASS workspace contracts lane"
