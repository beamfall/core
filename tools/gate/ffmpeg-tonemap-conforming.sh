#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
lock="$root/internal/ffmpeg/ffmpeg.lock"
tmp="$(mktemp -d "$root/.ffmpeg-tonemap.XXXXXX")"
container="beamfall-ffmpeg-tonemap-$$"

cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  docker rm -f "$container" >/dev/null 2>&1 || true
  rm -rf "$tmp"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

# shellcheck disable=SC1090
source "$lock"
arch="$(docker version --format '{{.Server.Arch}}')"
case "$arch" in
  amd64)
    url="$FFMPEG_LINUX_AMD64_URL"
    sha256="$FFMPEG_LINUX_AMD64_SHA256"
    ;;
  arm64)
    url="$FFMPEG_LINUX_ARM64_URL"
    sha256="$FFMPEG_LINUX_ARM64_SHA256"
    ;;
  *)
    echo "unsupported Docker server architecture: $arch" >&2
    exit 1
    ;;
esac

CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
  go test -c ./internal/ffmpeg -o "$tmp/ffmpeg.test"

docker run --rm --interactive --name "$container" --platform "linux/$arch" \
  --volume "$tmp/ffmpeg.test:/work/ffmpeg.test:ro" \
  debian:bookworm-slim bash -s -- "$url" "$sha256" <<'SH'
set -euo pipefail
apt-get update -qq
apt-get install -y -qq ca-certificates curl >/dev/null
curl -fsSL --retry 3 -o /tmp/ffmpeg.deb "$1"
echo "$2  /tmp/ffmpeg.deb" | sha256sum -c -
apt-get install -y -qq /tmp/ffmpeg.deb >/dev/null
PATH="/usr/lib/jellyfin-ffmpeg:$PATH" BEAMFALL_FFMPEG_CONFORMING=1 \
  /work/ffmpeg.test \
    -test.run '^TestProductionToneMapChainRunsOrIsRefused$' \
    -test.count=1 \
    -test.timeout=90s \
    -test.v
SH
