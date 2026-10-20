#!/bin/sh
# Installs what `make web-ui-e2e-smoke` needs on a Linux CI runner beyond
# Node and Playwright's Chromium:
#   - FFmpeg: the server normalizes uploaded artwork through ffmpeg, so the
#     create-media-artwork journey returns 500 without it. Installed by
#     .github/install-ffmpeg.sh, the lock's Ubuntu noble build of the pinned
#     release that gate.yml and release-gate.yml use.
#   - fonts-liberation: the only font family internal/web/app/e2e/linux-fonts.conf
#     exposes to the browser, which the e2e/goldens/linux baselines were
#     rendered with. Pinned; apt verifies it against the signed archive index.
set -eu

fonts_version=1:2.1.5-3
sudo apt-get update -qq
sudo apt-get install -y --no-install-recommends ca-certificates curl "fonts-liberation=$fonts_version"
sh .github/install-ffmpeg.sh
installed=$(dpkg-query -W -f '${Version}' fonts-liberation)
if [ "$installed" != "$fonts_version" ]; then
	echo "fonts-liberation $installed installed, want $fonts_version" >&2
	exit 1
fi
