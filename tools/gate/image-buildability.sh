#!/usr/bin/env bash
# Build the production runtime stage without private-module credentials.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BUILD_ROOT="$ROOT"
TMP=""

cleanup() {
  [ -z "$TMP" ] || rm -rf "$TMP"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [ "${1:-}" = "--root" ] && [ -n "${2:-}" ] && [ -z "${3:-}" ]; then
  BUILD_ROOT="$(cd "$2" && pwd)"
elif [ "$#" -ne 0 ]; then
  echo "usage: $0 [--root DIR]" >&2
  exit 2
fi

command -v docker >/dev/null 2>&1 || {
  echo "image buildability: docker is required" >&2
  exit 1
}
if docker buildx version >/dev/null 2>&1; then
  buildx=(docker buildx)
elif command -v docker-buildx >/dev/null 2>&1; then
  buildx=(docker-buildx)
else
  echo "image buildability: docker buildx is required" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64|amd64) platform=linux/amd64 ;;
  arm64|aarch64) platform=linux/arm64 ;;
  *)
    echo "image buildability: unsupported host architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

for input in Dockerfile go.mod internal/ffmpeg/ffmpeg.pin internal/ffmpeg/ffmpeg.lock; do
  [ -f "$BUILD_ROOT/$input" ] || {
    echo "image buildability: missing required input: $BUILD_ROOT/$input" >&2
    exit 1
  }
done

# The build-stage name is intentionally replaced by a named context containing
# only the artifact consumed by runtime. This exercises the production runtime
# stage verbatim without fetching the private plugin module.
TMP="$(mktemp -d "${TMPDIR:-/tmp}/beamfall-image-buildability.XXXXXX")"
mkdir -p "$TMP/build/out"
install -m 0755 /dev/null "$TMP/build/out/beamfall"

(
  cd "$BUILD_ROOT"
  python3 "$ROOT/script/check-release-workflow-pins.py" --verify-toolchain-images
  "${buildx[@]}" build \
    --build-context "build=$TMP/build" \
    --output type=cacheonly \
    --platform "$platform" \
    --target runtime \
    .
)

echo "image buildability: OK ($platform runtime)"
