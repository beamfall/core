#!/bin/sh
# Install the pinned jellyfin-ffmpeg version on the ubuntu-latest (noble) runner.
# internal/ffmpeg/ffmpeg.lock locks the bookworm build for the container image; that
# deb needs libvpx7, which noble lacks, so CI installs Jellyfin's noble build of the
# same release, verified against the hash below and the pinned version string.
set -eu
FFMPEG_PIN_SHA256="$(sed -n 's/^FFMPEG_PIN_SHA256=//p' internal/ffmpeg/ffmpeg.lock)"
bookworm_url="$(sed -n 's/^FFMPEG_LINUX_AMD64_URL=//p' internal/ffmpeg/ffmpeg.lock)"
printf '%s  %s\n' "$FFMPEG_PIN_SHA256" internal/ffmpeg/ffmpeg.pin | sha256sum -c -
ffmpeg_url="$(printf '%s' "$bookworm_url" | sed 's#/ffmpeg/debian/#/ffmpeg/ubuntu/#; s#-bookworm_amd64[.]deb$#-noble_amd64.deb#')"
test "$ffmpeg_url" = "https://repo.jellyfin.org/files/ffmpeg/ubuntu/7.x/7.1.4-3/amd64/jellyfin-ffmpeg7_7.1.4-3-noble_amd64.deb" || {
  echo "ffmpeg.lock moved past 7.1.4-3: update the noble URL and hash in $0" >&2
  exit 1
}
ffmpeg_sha=bbaa1a5fea4fe0a23df1bfd9050af6a4a5f7fc934ebbca997d687e528a0931a6
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
