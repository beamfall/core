# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.27.2

FROM --platform=$BUILDPLATFORM node:26-bookworm-slim@sha256:28147155d47dc31abe0a46e0d12b59b2fb3d0a2dcc5dc65fce76a074180b16fe AS web
WORKDIR /src

COPY internal/web/app/package*.json ./internal/web/app/
RUN npm --prefix internal/web/app ci --no-audit --no-fund

COPY internal/web/app ./internal/web/app
RUN npm --prefix internal/web/app run build

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY --from=web /src/internal/web/app-dist ./internal/web/app-dist

ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
	go build -trimpath -ldflags="-s -w" -o /out/beamfall ./cmd/beamfall

FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 AS runtime

ARG TARGETARCH
COPY internal/ffmpeg/ffmpeg.pin internal/ffmpeg/ffmpeg.lock /tmp/

RUN set -eux; \
	FFMPEG_PIN_SHA256="$(sed -n 's/^FFMPEG_PIN_SHA256=//p' /tmp/ffmpeg.lock)"; \
	FFMPEG_LINUX_AMD64_URL="$(sed -n 's/^FFMPEG_LINUX_AMD64_URL=//p' /tmp/ffmpeg.lock)"; \
	FFMPEG_LINUX_AMD64_SHA256="$(sed -n 's/^FFMPEG_LINUX_AMD64_SHA256=//p' /tmp/ffmpeg.lock)"; \
	FFMPEG_LINUX_ARM64_URL="$(sed -n 's/^FFMPEG_LINUX_ARM64_URL=//p' /tmp/ffmpeg.lock)"; \
	FFMPEG_LINUX_ARM64_SHA256="$(sed -n 's/^FFMPEG_LINUX_ARM64_SHA256=//p' /tmp/ffmpeg.lock)"; \
	test -n "$FFMPEG_PIN_SHA256" -a -n "$FFMPEG_LINUX_AMD64_URL" -a -n "$FFMPEG_LINUX_AMD64_SHA256" -a -n "$FFMPEG_LINUX_ARM64_URL" -a -n "$FFMPEG_LINUX_ARM64_SHA256"; \
	printf '%s  %s\n' "$FFMPEG_PIN_SHA256" /tmp/ffmpeg.pin | sha256sum -c -; \
	apt-get update; \
	apt-get install -y --no-install-recommends ca-certificates curl; \
	case "$TARGETARCH" in \
		amd64) \
			ffmpeg_url="$FFMPEG_LINUX_AMD64_URL"; \
			ffmpeg_sha="$FFMPEG_LINUX_AMD64_SHA256"; \
			;; \
		arm64) \
			ffmpeg_url="$FFMPEG_LINUX_ARM64_URL"; \
			ffmpeg_sha="$FFMPEG_LINUX_ARM64_SHA256"; \
			;; \
		*) \
			echo "unsupported TARGETARCH for jellyfin-ffmpeg: $TARGETARCH" >&2; \
			exit 1; \
			;; \
	esac; \
	ffmpeg_pkg="${ffmpeg_url##*/}"; \
	curl -fsSL "$ffmpeg_url" -o "/tmp/${ffmpeg_pkg}"; \
	printf '%s  %s\n' "$ffmpeg_sha" "/tmp/${ffmpeg_pkg}" | sha256sum -c -; \
	apt-get install -y --no-install-recommends "/tmp/${ffmpeg_pkg}"; \
	if [ -x /usr/lib/jellyfin-ffmpeg/ffmpeg ]; then ln -sf /usr/lib/jellyfin-ffmpeg/ffmpeg /usr/local/bin/ffmpeg; fi; \
	if [ -x /usr/lib/jellyfin-ffmpeg/ffprobe ]; then ln -sf /usr/lib/jellyfin-ffmpeg/ffprobe /usr/local/bin/ffprobe; fi; \
	set -- $(ffmpeg -version | sed -n '1p'); \
	test "$3" = "$(cat /tmp/ffmpeg.pin)"; \
	rm -f "/tmp/${ffmpeg_pkg}"; \
	rm -f /tmp/ffmpeg.pin /tmp/ffmpeg.lock; \
	apt-get clean; \
	rm -rf /var/lib/apt/lists/*

COPY --from=build /out/beamfall /usr/local/bin/beamfall

RUN set -eux; \
	groupadd --system --gid 1000 beamfall; \
	useradd --system --uid 1000 --gid beamfall --home-dir /data --shell /usr/sbin/nologin beamfall; \
	mkdir -p /data /media /transcode; \
	chown beamfall:beamfall /data /transcode

ENV BEAMFALL_ADDR=0.0.0.0:8800 \
	BEAMFALL_DATA_DIR=/data

VOLUME ["/data", "/media", "/transcode"]
EXPOSE 8800
USER beamfall:beamfall

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
	CMD curl -fsS http://127.0.0.1:8800/health >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/beamfall"]
CMD ["serve"]
