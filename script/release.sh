#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST="${ROOT}/script/dist"
VERSION="${VERSION:-$(git -C "${ROOT}" describe --tags --always --dirty)}"
CHANNEL="${CHANNEL:-stable}"
COMMIT="$(git -C "${ROOT}" rev-parse HEAD 2>/dev/null || true)"
# Stamp the binary and source offer with the same reproducible UTC date: the
# commit's, or now outside a checkout (the source offer then refuses anyway).
build_epoch="${SOURCE_DATE_EPOCH-$(git -C "${ROOT}" show -s --format=%ct "${COMMIT:-HEAD}" 2>/dev/null || date -u +%s)}"
BUILD_DATE="$(python3 -c 'import sys; from datetime import datetime, timezone; print(datetime.fromtimestamp(int(sys.argv[1]), timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z"))' "${build_epoch}")"
INVOCATION_ID="${INVOCATION_ID:-}"
if [ -z "${INVOCATION_ID}" ] && [ -n "${GITHUB_RUN_ID:-}" ]; then
  INVOCATION_ID="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:-beamfall/core}/actions/runs/${GITHUB_RUN_ID}/attempts/${GITHUB_RUN_ATTEMPT:-1}"
fi
SOURCE_OFFER_BASE_URL="${SOURCE_OFFER_BASE_URL:-https://github.com/beamfall/core/releases/download/${VERSION}}"

case "${CHANNEL}" in
  stable|beta) ;;
  *)
    echo "CHANNEL must be stable or beta" >&2
    exit 2
    ;;
esac

# First-party native build catalog (ADR-0063 build provenance). A release
# without it refuses every native client (the Mac Manager, Viewer and TV apps),
# so a missing manifest is an error, checked before any build work (CORE-1395).
# The manifest is assembled from the client release pipelines' build records
# with `beamfall release build-catalog --assemble` (docs/release/first-party-builds.md).
# BEAMFALL_ALLOW_NO_FIRST_PARTY_BUILDS=1 opts a local or test build out; a
# signed release cannot.
if [ -n "${FIRST_PARTY_BUILDS:-}" ]; then
  [ -f "${FIRST_PARTY_BUILDS}" ] || { echo "first-party build manifest not found: ${FIRST_PARTY_BUILDS}" >&2; exit 2; }
elif [ "${BEAMFALL_ALLOW_NO_FIRST_PARTY_BUILDS:-}" = 1 ] && [ -n "${BEAMFALL_RELEASE_SIGNING_KEY:-}" ]; then
  echo "FIRST_PARTY_BUILDS is required: a signed release cannot opt out with BEAMFALL_ALLOW_NO_FIRST_PARTY_BUILDS=1" >&2
  exit 2
elif [ "${BEAMFALL_ALLOW_NO_FIRST_PARTY_BUILDS:-}" = 1 ]; then
  echo "WARNING: FIRST_PARTY_BUILDS unset (allowed by BEAMFALL_ALLOW_NO_FIRST_PARTY_BUILDS=1): this build carries no first-party native build catalog and refuses every native client (unknown-build)" >&2
else
  echo "FIRST_PARTY_BUILDS is required: a release without the first-party native build catalog refuses every native client (unknown-build). Point it at the release's beamfall.first-party-builds/v1 manifest, or set BEAMFALL_ALLOW_NO_FIRST_PARTY_BUILDS=1 for a local build." >&2
  exit 2
fi

if [ -n "${BEAMFALL_RELEASE_SIGNING_KEY:-}" ]; then
  [ -n "${INVOCATION_ID}" ] || { echo "signed releases require INVOCATION_ID" >&2; exit 2; }
  [ -n "${RELEASE_GATE_EVIDENCE_ARCHIVE:-}" ] || { echo "signed releases require RELEASE_GATE_EVIDENCE_ARCHIVE" >&2; exit 2; }
  [ -f "${RELEASE_GATE_EVIDENCE_ARCHIVE}" ] || { echo "release evidence archive not found: ${RELEASE_GATE_EVIDENCE_ARCHIVE}" >&2; exit 2; }
  case "${RELEASE_GATE_EVIDENCE_ARCHIVE}" in
    "${DIST}"/*) echo "release evidence archive must be outside ${DIST}" >&2; exit 2 ;;
  esac
  tag_commit="$(git -C "${ROOT}" rev-list -n 1 "${VERSION}" 2>/dev/null || true)"
  [ -n "${tag_commit}" ] || { echo "signed release version is not a git ref: ${VERSION}" >&2; exit 2; }
  [ "${tag_commit}" = "${COMMIT}" ] || { echo "release tag ${VERSION} does not resolve to commit ${COMMIT}" >&2; exit 2; }
  [ -z "$(git -C "${ROOT}" status --porcelain --untracked-files=normal)" ] || { echo "signed releases require a clean worktree" >&2; exit 2; }
fi

# Launch artifact matrix. Windows is
# intentionally absent: internal/store still uses unguarded unix syscalls
# (syscall.Statfs_t, O_NOFOLLOW), so windows/* does not compile — it is deferred
# until those call sites are platform-split, not silently shipped broken.
targets=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
)

rm -rf "${DIST}"
mkdir -p "${DIST}"

npm --prefix "${ROOT}/internal/web/app" run build

# First-party native build catalog (ADR-0063 build provenance). FIRST_PARTY_BUILDS
# names a beamfall.first-party-builds/v1 manifest of the native client builds this
# release ships with; it is validated and linked into the binary so a production
# server knows those builds at boot. Without it the server knows only its own
# embedded web UI and refuses every native client as an unknown build.
first_party_ldflag=""
if [ -n "${FIRST_PARTY_BUILDS:-}" ]; then
  first_party_catalog="$(cd "${ROOT}" && go run ./cmd/beamfall release build-catalog --manifest "${FIRST_PARTY_BUILDS}")"
  first_party_ldflag=" -X github.com/beamfall/core/internal/capabilities.FirstPartyBuildManifest=${first_party_catalog}"
fi

for target in "${targets[@]}"; do
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  name="beamfall-core_${VERSION}_${CHANNEL}_${GOOS}_${GOARCH}"
  work="${DIST}/${name}"
  offer_url="${SOURCE_OFFER_BASE_URL%/}/${name}.source.json"
  mkdir -p "${work}"

  # BEAMFALL_BUNDLED_CREDENTIALS (ref=value,...) carries Beamfall's own
  # metadata project keys into official builds (internal/app.BundledCredentials);
  # unset, the build ships none and owners register their own.
  CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" \
    go build -trimpath \
      -ldflags="-s -w -X github.com/beamfall/core/internal/capabilities.ReleaseVersion=${VERSION} -X github.com/beamfall/core/internal/capabilities.ReleaseBuildDate=${BUILD_DATE} -X github.com/beamfall/core/internal/capabilities.SourceOfferURL=${offer_url}${first_party_ldflag} -X github.com/beamfall/core/internal/app.BundledCredentials=${BEAMFALL_BUNDLED_CREDENTIALS:-}" \
      -o "${work}/beamfall" ./cmd/beamfall

  python3 "${ROOT}/script/source-artifact.py" write \
    --root "${ROOT}" \
    --binary "${work}/beamfall" \
    --out "${work}/source.json" \
    --variant "${name}" \
    --version "${VERSION}" \
    --channel "${CHANNEL}" \
    --git-tag "${VERSION}" \
    --git-commit "${COMMIT}" \
    --build-date "${BUILD_DATE}" \
    --goos "${GOOS}" \
    --goarch "${GOARCH}" \
    --offer-url "${offer_url}"
  cp "${work}/source.json" "${DIST}/${name}.source.json"

  cp "${ROOT}/LICENSE" "${work}/LICENSE"
  {
    echo "Beamfall Core ${VERSION} (${CHANNEL})"
    echo
    echo "Run:"
    echo "  ./beamfall serve --data-dir /path/to/data"
  } >"${work}/README.txt"

  tar -C "${DIST}" -czf "${DIST}/${name}.tar.gz" "${name}"
  rm -rf "${work}"
done

"${ROOT}/script/source-artifact-check.sh" "${DIST}"

(
  cd "${DIST}"
  shasum -a 256 ./*.tar.gz ./*.source.json >SHA256SUMS
)

echo "release archives written to ${DIST}"

go run ./tools/wire-contract --version "${VERSION}" >"${DIST}/wire-contract.json"
if [ -n "${RELEASE_GATE_EVIDENCE_ARCHIVE:-}" ]; then
  cp "${RELEASE_GATE_EVIDENCE_ARCHIVE}" "${DIST}/release-gate-evidence.tar.gz"
fi

# Supply-chain attestation (PANEL-11, ADR-0013 §A): sign the release manifest and
# emit an SBOM + SLSA provenance alongside the archives. Fail-closed — without a
# signing key the release is refused, never silently produced unsigned.
#   BEAMFALL_RELEASE_SIGNING_KEY  base64 ed25519 32-byte seed (CI secret)
#   RELEASE_ALLOW_UNSIGNED=1      dev-only escape: emit an unsigned manifest that
#                                 `beamfall release verify` will reject
TOOL="${TMPDIR:-/tmp}/beamfall-attest-tool.$$"
trap 'rm -f "${TOOL}"' EXIT
CGO_ENABLED=0 go build -o "${TOOL}" ./cmd/beamfall

attest_args=(
  release attest
  --dist "${DIST}"
  --version "${VERSION}"
  --channel "${CHANNEL}"
  --timestamp "${BUILD_DATE}"
  --artifact wire-contract.json
)
if [ -n "${COMMIT}" ]; then
  attest_args+=(--commit "${COMMIT}")
fi
if [ -n "${INVOCATION_ID}" ]; then
  attest_args+=(--invocation-id "${INVOCATION_ID}")
fi
if [ -f "${DIST}/release-gate-evidence.tar.gz" ]; then
  attest_args+=(--artifact release-gate-evidence.tar.gz)
fi

if [ -z "${BEAMFALL_RELEASE_SIGNING_KEY:-}" ]; then
  if [ "${RELEASE_ALLOW_UNSIGNED:-0}" = "1" ]; then
    echo "WARNING: BEAMFALL_RELEASE_SIGNING_KEY unset and RELEASE_ALLOW_UNSIGNED=1 — producing an UNSIGNED dev release" >&2
    attest_args+=(--allow-unsigned)
  else
    echo "ERROR: BEAMFALL_RELEASE_SIGNING_KEY is unset — refusing to produce a release without a signature." >&2
    echo "       Set the signing key, or RELEASE_ALLOW_UNSIGNED=1 for an explicit local dev build." >&2
    rm -f "${TOOL}"
    exit 3
  fi
fi

"${TOOL}" "${attest_args[@]}"

# Self-verify when signed (a signed release must verify against the pinned key).
if [ -n "${BEAMFALL_RELEASE_SIGNING_KEY:-}" ]; then
  "${TOOL}" release verify --dist "${DIST}"
fi

rm -f "${TOOL}"

echo "release artifacts (archives + SHA256SUMS + signed manifest/sbom/provenance/wire/evidence) written to ${DIST}"
