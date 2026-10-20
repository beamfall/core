#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

mkdir -p "$TMP/bin"
cat >"$TMP/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"$MOCK_DOCKER_LOG"
case "$1 ${2:-}" in
  "buildx version") ;;
  "run --rm")
    if [[ "$*" == *" go env GOVERSION" ]]; then
      printf '%s\n' "${MOCK_GO_VERSION:-go1.27.2}"
    else
      printf '%s\n' "v22.17.0"
    fi
    ;;
  "rm --force") ;;
  "buildx build") exit "${MOCK_BUILD_STATUS:-0}" ;;
  *) echo "unexpected docker command: $*" >&2; exit 97 ;;
esac
EOF
chmod +x "$TMP/bin/docker"

run_check() {
  MOCK_DOCKER_LOG="$TMP/docker.log" PATH="$TMP/bin:$PATH" \
    "$ROOT/tools/gate/image-buildability.sh"
}

: >"$TMP/docker.log"
run_check >"$TMP/pass.out"
grep -Fq "run --rm --name beamfall-node-toolchain-check-" "$TMP/docker.log"
grep -Fq "run --rm --name beamfall-golang-toolchain-check-" "$TMP/docker.log"
grep -Fq "buildx build --build-context build=" "$TMP/docker.log"
grep -Fq -- "--output type=cacheonly --platform linux/" "$TMP/docker.log"
grep -Fq -- "--target runtime ." "$TMP/docker.log"
grep -Fq "image buildability: OK (linux/" "$TMP/pass.out"

: >"$TMP/docker.log"
if MOCK_GO_VERSION=go1.27.1 run_check >"$TMP/go.out" 2>"$TMP/go.err"; then
  echo "image buildability accepted a mismatched Go toolchain" >&2
  exit 1
fi
grep -Fq "reports golang 1.27.1, expected 1.27.2" "$TMP/go.err"
if grep -Fq "buildx build" "$TMP/docker.log"; then
  echo "image buildability continued after toolchain failure" >&2
  exit 1
fi

: >"$TMP/docker.log"
if MOCK_BUILD_STATUS=23 run_check >"$TMP/build.out" 2>"$TMP/build.err"; then
  echo "image buildability hid a runtime image build failure" >&2
  exit 1
fi
grep -Fq "buildx build" "$TMP/docker.log"

grep -Fq 'run_scoped_step "runtime image buildability" image make image-buildability-check' \
  "$ROOT/tools/gate/gate.sh"

echo "image_buildability_test: OK"
