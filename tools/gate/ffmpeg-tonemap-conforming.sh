#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

arch="$(docker version --format '{{.Server.Arch}}')"
case "$arch" in
  amd64 | arm64) ;;
  *)
    echo "unsupported Docker server architecture: $arch" >&2
    exit 1
    ;;
esac

if ! docker buildx version >/dev/null 2>&1; then
  cat >&2 <<'MSG'
FFmpeg tone-map conformance: Docker Buildx is required (docker buildx version failed).
Install the Buildx CLI plugin (for example, brew install docker-buildx) and link
docker-buildx into ~/.docker/cli-plugins.
MSG
  exit 1
fi

tmp="$(mktemp -d "$root/.ffmpeg-tonemap.XXXXXX")"
container="beamfall-ffmpeg-tonemap-$$"
image="beamfall-ffmpeg-runtime:tonemap-$$"

cleanup() {
  status=$?
  trap - EXIT INT TERM HUP
  docker rm -f "$container" >/dev/null 2>&1 || true
  docker image rm -f "$image" >/dev/null 2>&1 || true
  rm -rf "$tmp"
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

# The image's own ffmpeg stage, built from ffmpeg.lock by tools/ffmpeg/build.sh
# (CORE-2497); the layer cache makes reruns on an unchanged lock cheap.
docker buildx build --load --quiet --platform "linux/$arch" --target ffmpeg-runtime \
  --tag "$image" --file "$root/Dockerfile" "$root" >/dev/null

CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
  go test -c -tags tonemap_conformance ./internal/ffmpeg -o "$tmp/ffmpeg.test"

docker run --rm --name "$container" --platform "linux/$arch" \
  --volume "$tmp/ffmpeg.test:/work/ffmpeg.test:ro" \
  --env BEAMFALL_FFMPEG_CONFORMING=1 \
  "$image" /work/ffmpeg.test \
    -test.run '^TestProductionToneMapChainRunsOrIsRefused$' \
    -test.count=1 \
    -test.timeout=90s \
    -test.v
