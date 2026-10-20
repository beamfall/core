#!/bin/sh
# Installs what `make web-ui-e2e-smoke` needs on a Linux CI runner beyond
# Node and Playwright's Chromium:
#   - FFmpeg: the server normalizes uploaded artwork through ffmpeg, so the
#     create-media-artwork journey returns 500 without it. Uses the same
#     authenticated artifact lock as the container image and release-gate.yml.
#   - fonts-liberation: the only font family internal/web/app/e2e/linux-fonts.conf
#     exposes to the browser, which the e2e/goldens/linux baselines were
#     rendered with. Pinned; apt verifies it against the signed archive index.
set -eu

fonts_version=1:2.1.5-3

lock=internal/ffmpeg/ffmpeg.lock
pin_sha=$(sed -n 's/^FFMPEG_PIN_SHA256=//p' "$lock")
url=$(sed -n 's/^FFMPEG_LINUX_AMD64_URL=//p' "$lock")
sha=$(sed -n 's/^FFMPEG_LINUX_AMD64_SHA256=//p' "$lock")
printf '%s  %s\n' "$pin_sha" internal/ffmpeg/ffmpeg.pin | sha256sum -c -

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM
pkg="$tmp/${url##*/}"

sudo apt-get update -qq
sudo apt-get install -y --no-install-recommends ca-certificates curl "fonts-liberation=$fonts_version"
curl -fsSL "$url" -o "$pkg"
printf '%s  %s\n' "$sha" "$pkg" | sha256sum -c -
sudo apt-get install -y --no-install-recommends "$pkg"
sudo ln -sf /usr/lib/jellyfin-ffmpeg/ffmpeg /usr/local/bin/ffmpeg
sudo ln -sf /usr/lib/jellyfin-ffmpeg/ffprobe /usr/local/bin/ffprobe

ffmpeg_version=$(ffmpeg -version | awk 'NR == 1 { print $3 }')
if [ "$ffmpeg_version" != "$(cat internal/ffmpeg/ffmpeg.pin)" ]; then
	echo "ffmpeg $ffmpeg_version installed, want $(cat internal/ffmpeg/ffmpeg.pin)" >&2
	exit 1
fi
installed=$(dpkg-query -W -f '${Version}' fonts-liberation)
if [ "$installed" != "$fonts_version" ]; then
	echo "fonts-liberation $installed installed, want $fonts_version" >&2
	exit 1
fi
