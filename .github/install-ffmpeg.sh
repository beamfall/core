#!/bin/sh
# Install the pinned jellyfin-ffmpeg from internal/ffmpeg/ffmpeg.lock, verified, as release-gate.yml does.
set -eu
FFMPEG_PIN_SHA256="$(sed -n 's/^FFMPEG_PIN_SHA256=//p' internal/ffmpeg/ffmpeg.lock)"
ffmpeg_url="$(sed -n 's/^FFMPEG_LINUX_AMD64_URL=//p' internal/ffmpeg/ffmpeg.lock)"
ffmpeg_sha="$(sed -n 's/^FFMPEG_LINUX_AMD64_SHA256=//p' internal/ffmpeg/ffmpeg.lock)"
printf '%s  %s\n' "$FFMPEG_PIN_SHA256" internal/ffmpeg/ffmpeg.pin | sha256sum -c -
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
