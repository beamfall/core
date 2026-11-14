#!/usr/bin/env bash
# ci-install-ffmpeg.sh: install the locked linux/amd64 jellyfin-ffmpeg on a CI runner.
#
# The hosted gate and race jobs run the FFmpeg tests that refuse to skip when
# ffmpeg is missing (TestProductionToneMapChainRunsOrIsRefused), so they need the
# same binary as production. Coordinates come only from internal/ffmpeg/ffmpeg.lock,
# which is parsed line by line, never shell-sourced. The lock must match
# ffmpeg.pin, the package must match its locked SHA-256, and the installed binary
# must report the pinned version.
set -euo pipefail

cd "$(dirname "$0")/.."
lock=internal/ffmpeg/ffmpeg.lock
pin=internal/ffmpeg/ffmpeg.pin

lock_value() {
  local value
  value="$(sed -n "s/^$1=//p" "$lock")"
  if [[ ! "$value" =~ ^[A-Za-z0-9][A-Za-z0-9._:/?-]*$ ]]; then
    echo "ci-install-ffmpeg: $lock has no valid $1" >&2
    exit 1
  fi
  printf '%s' "$value"
}

pin_sha="$(lock_value FFMPEG_PIN_SHA256)"
url="$(lock_value FFMPEG_LINUX_AMD64_URL)"
sha="$(lock_value FFMPEG_LINUX_AMD64_SHA256)"
printf '%s  %s\n' "$pin_sha" "$pin" | sha256sum -c -

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
pkg="$work/${url##*/}"
sudo apt-get update -qq
sudo apt-get install -y --no-install-recommends ca-certificates curl
curl -fsSL "$url" -o "$pkg"
printf '%s  %s\n' "$sha" "$pkg" | sha256sum -c -
sudo apt-get install -y --no-install-recommends "$pkg"
sudo ln -sf /usr/lib/jellyfin-ffmpeg/ffmpeg /usr/local/bin/ffmpeg
sudo ln -sf /usr/lib/jellyfin-ffmpeg/ffprobe /usr/local/bin/ffprobe
version="$(ffmpeg -version | awk 'NR == 1 { print $3 }')"
if [ "$version" != "$(cat "$pin")" ]; then
  echo "ci-install-ffmpeg: installed ffmpeg $version, want $(cat "$pin")" >&2
  exit 1
fi
echo "ci-install-ffmpeg: ffmpeg $version"
