#!/bin/sh
# Install the pinned jellyfin-ffmpeg on GitHub's ubuntu-latest (noble) runner, for
# gate.yml and release-gate.yml. The container image installs the locked bookworm
# deb; that deb needs libvpx7, which noble lacks, so CI installs the lock's noble
# build of the same release, verified by hash and by the pinned version string.
set -eu
python3 script/check-release-workflow-pins.py
FFMPEG_PIN_SHA256="$(sed -n 's/^FFMPEG_PIN_SHA256=//p' internal/ffmpeg/ffmpeg.lock)"
FFMPEG_UBUNTU_NOBLE_AMD64_URL="$(sed -n 's/^FFMPEG_UBUNTU_NOBLE_AMD64_URL=//p' internal/ffmpeg/ffmpeg.lock)"
FFMPEG_UBUNTU_NOBLE_AMD64_SHA256="$(sed -n 's/^FFMPEG_UBUNTU_NOBLE_AMD64_SHA256=//p' internal/ffmpeg/ffmpeg.lock)"
printf '%s  %s\n' "$FFMPEG_PIN_SHA256" internal/ffmpeg/ffmpeg.pin | sha256sum -c -
ffmpeg_url="$FFMPEG_UBUNTU_NOBLE_AMD64_URL"
ffmpeg_sha="$FFMPEG_UBUNTU_NOBLE_AMD64_SHA256"
ffmpeg_pkg="${ffmpeg_url##*/}"
sudo apt-get update -qq
sudo apt-get install -y --no-install-recommends ca-certificates curl
curl -fsSL "$ffmpeg_url" -o "/tmp/${ffmpeg_pkg}"
printf '%s  %s\n' "$ffmpeg_sha" "/tmp/${ffmpeg_pkg}" | sha256sum -c -
sudo apt-get install -y --no-install-recommends "/tmp/${ffmpeg_pkg}"
sudo ln -sf /usr/lib/jellyfin-ffmpeg/ffmpeg /usr/local/bin/ffmpeg
sudo ln -sf /usr/lib/jellyfin-ffmpeg/ffprobe /usr/local/bin/ffprobe
test "$(ffmpeg -version | awk 'NR == 1 { print $3 }')" = "$(cat internal/ffmpeg/ffmpeg.pin)"
rm -f "/tmp/${ffmpeg_pkg}"
