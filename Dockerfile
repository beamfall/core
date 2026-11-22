# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.27.4

FROM --platform=$BUILDPLATFORM node:26-bookworm-slim@sha256:add5c17c14752c54d2f3d519ec46a3815684d41c33f198bcb09fb998f7dcbf68 AS web
WORKDIR /src

COPY internal/web/app/package*.json ./internal/web/app/
RUN npm --prefix internal/web/app ci --no-audit --no-fund

COPY internal/web/app ./internal/web/app
RUN npm --prefix internal/web/app run build

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
# internal/server imports the docs/api Go package, which embeds the OpenAPI
# document (CORE-1714); the rest of docs stays out of the context (CORE-2076).
COPY docs/api ./docs/api
# Source identity for an unstamped build (CORE-1096): a digest of the Go
# sources, taken before the web bundle is copied in, so two unstamped images
# built from different trees report different versions.
RUN mkdir -p /out && find go.mod go.sum cmd internal -type f -print0 | LC_ALL=C sort -z \
	| xargs -0 sha256sum | sha256sum | cut -c1-12 >/out/source-id
COPY --from=web /src/internal/web/app-dist ./internal/web/app-dist

ARG TARGETOS
ARG TARGETARCH
# First-party native build catalog (ADR-0063): the value printed by
# `beamfall release build-catalog --manifest FILE`. Empty refuses every native
# client as an unknown build; only the embedded web UI is known. A non-empty
# value is validated with `build-catalog --catalog` (built for the build
# platform) before it is linked, so a malformed one fails the image build
# instead of panicking the server at boot (CORE-1395).
ARG BEAMFALL_FIRST_PARTY_BUILDS=""
# Build identity (CORE-1096, CORE-3055): BEAMFALL_VERSION, BEAMFALL_COMMIT and
# BEAMFALL_BUILD_DATE (RFC3339 UTC) stamp `beamfall version` and /health.
# The date defaults to SOURCE_DATE_EPOCH or the UTC build time.
# The build context excludes .git, so without a version or commit the version is
# development+src.<source digest> and the commit is "development": an unstamped
# build is never mistaken for a release.
ARG BEAMFALL_VERSION=""
ARG BEAMFALL_COMMIT=""
ARG BEAMFALL_BUILD_DATE=""
ARG SOURCE_DATE_EPOCH
# Bundled metadata keys (internal/app.BundledCredentials, ref=value,...) come
# from an optional BuildKit secret, never a build arg, so they stay out of the
# image history: `docker build --secret id=beamfall_bundled_credentials,src=FILE`.
# Without the secret the image bundles none, as a source build does. The secret
# is not part of the layer cache key, so changing it needs a source change or
# --no-cache to take effect.
RUN --mount=type=secret,id=beamfall_bundled_credentials,required=false set -eu; \
	bundled=""; \
	if [ -s /run/secrets/beamfall_bundled_credentials ]; then \
		bundled="$(tr -d ' \t\r\n' </run/secrets/beamfall_bundled_credentials)"; \
	fi; \
	if [ -n "${BEAMFALL_FIRST_PARTY_BUILDS}" ]; then \
		CGO_ENABLED=0 go run ./cmd/beamfall release build-catalog --catalog "${BEAMFALL_FIRST_PARTY_BUILDS}" >/dev/null; \
	fi; \
	version="${BEAMFALL_VERSION:-development+src.$(cat /out/source-id)}"; \
	build_date="${BEAMFALL_BUILD_DATE}"; \
	if [ -z "${build_date}" ]; then \
		if [ "${SOURCE_DATE_EPOCH+x}" = x ]; then \
			build_date="$(date -u -d "@${SOURCE_DATE_EPOCH}" +%Y-%m-%dT%H:%M:%SZ)"; \
		else \
			build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; \
		fi; \
	fi; \
	CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
	go build -trimpath -ldflags="-s -w -X github.com/beamfall/core/internal/capabilities.ReleaseVersion=${version} -X github.com/beamfall/core/internal/capabilities.ReleaseCommit=${BEAMFALL_COMMIT} -X github.com/beamfall/core/internal/capabilities.ReleaseBuildDate=${build_date} -X github.com/beamfall/core/internal/capabilities.FirstPartyBuildManifest=${BEAMFALL_FIRST_PARTY_BUILDS} -X github.com/beamfall/core/internal/app.BundledCredentials=${bundled}" -o /out/beamfall ./cmd/beamfall

# ffmpeg (CORE-2497): jellyfin-ffmpeg's locked source release, patched and
# built against the Debian snapshot named in ffmpeg.lock, GPL-3.0-or-later with
# no nonfree component (no libfdk-aac; core uses the native aac encoder). It
# runs on the build platform and cross-compiles for the target, and it also
# writes the exact runtime package list and the corresponding-source bundle.
FROM --platform=$BUILDPLATFORM debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8 AS ffmpeg
ARG TARGETARCH
COPY internal/ffmpeg/ffmpeg.pin internal/ffmpeg/ffmpeg.lock tools/ffmpeg/build.sh tools/ffmpeg/apt-snapshot.sh /in/
RUN sh /in/build.sh /in/ffmpeg.lock /in/ffmpeg.pin "$TARGETARCH" /out

# The runtime base: ffmpeg and everything it loads. The tone-map gate and the
# release workflow's I/O job build this stage alone to run the shipped ffmpeg.
FROM debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8 AS ffmpeg-runtime

# Every library ffmpeg links, and on amd64 the Intel VA-API, QSV and OpenCL
# runtimes (CORE-2156), at the exact versions the builder recorded from the
# same Debian snapshot. The corresponding source of all of it is kept under
# /usr/share/beamfall/source/ffmpeg, served by GET /source/ffmpeg and named
# in NOTICE.
COPY internal/ffmpeg/ffmpeg.pin internal/ffmpeg/ffmpeg.lock tools/ffmpeg/apt-snapshot.sh /tmp/
COPY --from=ffmpeg /out/runtime-packages.txt /tmp/
RUN set -eux; \
	sh /tmp/apt-snapshot.sh /tmp/ffmpeg.lock; \
	apt-get update; \
	apt-get install -y --no-install-recommends ca-certificates curl; \
	dpkg-query -W -f '${Package}=${Version}\n' | sort >/tmp/before; \
	apt-get install -y --no-install-recommends $(cat /tmp/runtime-packages.txt); \
	dpkg-query -W -f '${Package}=${Version}\n' | sort >/tmp/after; \
	sort /tmp/runtime-packages.txt >/tmp/recorded; \
	unrecorded="$(comm -13 /tmp/before /tmp/after | comm -23 - /tmp/recorded)"; \
	test -z "$unrecorded" || { echo "packages without bundled source: $unrecorded" >&2; exit 1; }; \
	rm -f /tmp/before /tmp/after /tmp/recorded; \
	apt-get clean; \
	rm -rf /var/lib/apt/lists/*
COPY --from=ffmpeg /out/root/usr/lib/jellyfin-ffmpeg /usr/lib/jellyfin-ffmpeg
COPY --from=ffmpeg /out/SHA256SUMS /out/*-source.tar /usr/share/beamfall/source/ffmpeg/
RUN set -eux; \
	ln -s /usr/lib/jellyfin-ffmpeg/ffmpeg /usr/local/bin/ffmpeg; \
	ln -s /usr/lib/jellyfin-ffmpeg/ffprobe /usr/local/bin/ffprobe; \
	set -- $(ffmpeg -version | sed -n '1p'); \
	test "$3" = "$(cat /tmp/ffmpeg.pin)"; \
	if ffmpeg -hide_banner -buildconf | grep -q -e '--enable-nonfree' -e 'libfdk'; then exit 1; fi; \
	(cd /usr/share/beamfall/source/ffmpeg && sha256sum -c SHA256SUMS); \
	rm -f /tmp/ffmpeg.pin /tmp/ffmpeg.lock /tmp/apt-snapshot.sh /tmp/runtime-packages.txt

FROM ffmpeg-runtime AS runtime

COPY --from=build /out/beamfall /usr/local/bin/beamfall

# The apt sources inherited from ffmpeg-runtime point at the pinned snapshot.
RUN set -eux; \
	apt-get update; \
	apt-get install -y --no-install-recommends tini; \
	apt-get clean; \
	rm -rf /var/lib/apt/lists/*; \
	groupadd --system --gid 1000 beamfall; \
	useradd --system --uid 1000 --gid beamfall --home-dir /data --shell /usr/sbin/nologin beamfall; \
	mkdir -p /data /media /transcode; \
	chown beamfall:beamfall /data /transcode

# The same identity on the image (OCI annotations) and in /health's buildSHA.
ARG BEAMFALL_VERSION=""
ARG BEAMFALL_COMMIT=""
LABEL org.opencontainers.image.title="Beamfall Core" \
	org.opencontainers.image.source="https://github.com/beamfall/core" \
	org.opencontainers.image.version="${BEAMFALL_VERSION:-development}" \
	org.opencontainers.image.revision="${BEAMFALL_COMMIT}"

ENV BEAMFALL_ADDR=0.0.0.0:8800 \
	BEAMFALL_DATA_DIR=/data \
	BEAMFALL_BUILD_SHA=${BEAMFALL_COMMIT}

VOLUME ["/data", "/media", "/transcode"]
EXPOSE 8800
USER beamfall:beamfall

# Until the first passing probe, probe every 2s for up to 5 minutes, so the
# health state turns healthy about 2s after /health first answers, even after a
# slow start (a long migration). Failures before that first pass do not count
# while the start period lasts. After the first pass, or once the 5 minutes are
# up, probe every 30s, and 3 failures in a row mark the container unhealthy.
# --start-interval needs Docker Engine 25 or later (CORE-2164).
HEALTHCHECK --interval=30s --timeout=5s --start-period=5m --start-interval=2s --retries=3 \
	CMD curl -fsS http://127.0.0.1:8800/health >/dev/null || exit 1

ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/beamfall"]
CMD ["serve"]
