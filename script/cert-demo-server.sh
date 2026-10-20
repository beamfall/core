#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'USAGE'
usage: script/cert-demo-server.sh --data-dir PATH [--addr HOST:PORT] [--public-domain HOST] [--prepare-only]

Prepares the neutral certification demo corpus and starts Beamfall with
--seed=demo plus review-demo enrollment enabled. The review key is stored at
<data-dir>/secrets/review-demo-enrollment-key (0600).
USAGE
}

fail() {
  printf 'FAIL %s: %s\n' "$1" "$2" >&2
  exit 1
}

deny_re='adult|performer(s)?|studio(s)?|scene(s)?|gallery|galleries|porn'

validate_inventory() {
  local entry rel
  [[ ! -L "$data_dir" ]] || fail inventory "data directory is a symlink"
  [[ -d "$data_dir" ]] || return 0
  while IFS= read -r entry; do
    [[ -n "$entry" ]] || continue
    case "${entry##*/}" in
      seed-media|secrets) ;;
      *) fail inventory "unexpected top-level entry" ;;
    esac
  done < <(find "$data_dir" -mindepth 1 -maxdepth 1 -print)
  if [[ -d "$data_dir/secrets" ]]; then
    while IFS= read -r entry; do
      [[ -n "$entry" ]] || continue
      rel="${entry#"$data_dir/secrets/"}"
      [[ "$rel" == "review-demo-enrollment-key" ]] || fail inventory "unexpected secrets entry"
    done < <(find "$data_dir/secrets" -mindepth 1 -print)
  fi
}

validate_key() {
  local mode
  [[ ! -L "$secret" && -f "$secret" ]] || fail key "review key must be a non-symlink regular file"
  [[ -s "$secret" ]] || fail key "review key must be non-empty"
  mode="$(stat -c %a "$secret" 2>/dev/null || stat -f %Lp "$secret")"
  [[ "$mode" == "600" ]] || fail key "review key mode must be 0600"
}

validate_manifest() {
  [[ ! -L "$manifest" && -f "$manifest" && -s "$manifest" ]] || fail manifest "manifest must be a non-empty regular file"
  if ! python3 - "$manifest" "$deny_re" <<'PY'
import json
import re
import sys

path, pattern = sys.argv[1:]
try:
    with open(path, encoding="utf-8") as handle:
        manifest = json.load(handle)
except (OSError, ValueError):
    raise SystemExit(2)
for item in manifest.get("files", []):
    for field in ("mediaID", "path"):
        value = item.get(field, "")
        if not isinstance(value, str) or re.search(pattern, value, re.IGNORECASE):
            raise SystemExit(1)
PY
  then
    fail vocabulary "manifest IDs or paths violate the certification vocabulary"
  fi
}

data_dir=""
addr="${BEAMFALL_ADDR:-127.0.0.1:8800}"
public_domain="${BEAMFALL_PUBLIC_DOMAIN:-}"
prepare_only=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --data-dir)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      data_dir="$2"
      shift 2
      ;;
    --addr)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      addr="$2"
      shift 2
      ;;
    --public-domain)
      [[ $# -ge 2 ]] || { usage; exit 2; }
      public_domain="$2"
      shift 2
      ;;
    --prepare-only)
      prepare_only=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      exit 2
      ;;
  esac
done

if [[ -z "$data_dir" ]]; then
  usage
  exit 2
fi

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "$script_dir/.." && pwd)"
manifest="$data_dir/seed-media/demo/manifest.json"
secret="$data_dir/secrets/review-demo-enrollment-key"

validate_inventory

if [[ ! -f "$manifest" ]]; then
  "$repo_root/script/gen_demo_media.sh" --data-dir "$data_dir"
fi

install -d -m 0700 "$data_dir/secrets"
if [[ ! -f "$secret" ]]; then
  [[ ! -e "$secret" && ! -L "$secret" ]] || fail key "review key must be a non-symlink regular file"
  umask 077
  openssl rand -base64 24 > "$secret"
  chmod 0600 "$secret"
fi
validate_inventory
validate_key
validate_manifest

if [[ "$prepare_only" -eq 1 ]]; then
  echo "prepared demo data in $data_dir"
  exit 0
fi

cmd=("${BEAMFALL_BIN:-go}" "run" "./cmd/beamfall" "serve")
if [[ -n "${BEAMFALL_BIN:-}" ]]; then
  cmd=("$BEAMFALL_BIN" "serve")
fi

args=(
  --data-dir "$data_dir"
  --addr "$addr"
  --seed=demo
  --server-name "Beamfall Review Demo"
  --review-demo-enrollment-enabled
  --review-demo-profile-id profile-demo-default
)
if [[ -n "$public_domain" ]]; then
  args+=(--public-domain "$public_domain" --acme-terms-accepted)
fi

cd "$repo_root"
exec "${cmd[@]}" "${args[@]}"
