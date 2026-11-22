# Beamfall Core — Docker Compose reference deployment

Docker Compose is the **reference deployment** for self-hosting Beamfall Core
(ADR-0159 §5). Each file deploys the published container image
`ghcr.io/beamfall/core` — pick the overlay that matches your transcode hardware.

## Overlays

| File | Use when |
|------|----------|
| [`compose.cpu.yml`](./compose.cpu.yml) | CPU-only transcode (default; no GPU). |
| [`compose.intel-vaapi.yml`](./compose.intel-vaapi.yml) | Intel QSV / VAAPI via `/dev/dri`. |
| [`compose.nvidia.yml`](./compose.nvidia.yml) | NVIDIA NVENC via the container toolkit. |
| [`compose.public.yml`](./compose.public.yml) | Add-on: Caddy TLS reverse proxy for public bind (`public-bind` profile). |

The four base overlays are mutually exclusive — run exactly one. Layer
`compose.public.yml` on top of a base overlay to expose the server over HTTPS.

## Image reference (required)

Every GitHub release publishes a `beamfall-deploy-manifests.tar.gz` bundle
containing pre-rendered copies of these files pinned to the release's image
digest, plus a binding manifest tying that digest to the release's
cosign-signed image. Running a rendered file from that bundle is the supported
way to run a release.

The source templates in this directory refuse to start unless `BEAMFALL_IMAGE`
is set, but they accept any image reference; they do not check that it is a
digest. For a published image, set it to the release digest
(`ghcr.io/beamfall/core@sha256:…`) from the bundle. For an image you built from
source, set it to your own tag (for example `beamfall-core:local`). Do not use
a mutable channel tag (`stable`, `beta`) for a release: what the tag resolves to
at pull time is not what the release signed.

## Quick start (CPU)

```sh
BEAMFALL_IMAGE=ghcr.io/beamfall/core@sha256:<digest-from-release> \
BEAMFALL_MEDIA_DIR=/srv/media \
  docker compose -f compose.cpu.yml up -d
```

Then open `http://127.0.0.1:2326/` and finish setup in the Beamfall Console.
The first-run setup URLs, including the one-time setup token, are the last
lines the server prints once it is listening: read them with
`docker logs beamfall`. A browser on the server's local network needs no
token; the token URL is for this machine. Each restart prints a new token.

## Public HTTPS

```sh
BEAMFALL_IMAGE=ghcr.io/beamfall/core@sha256:<digest-from-release> \
BEAMFALL_MEDIA_DIR=/srv/media \
BEAMFALL_PUBLIC_HOST=beamfall.example.com \
  docker compose -f compose.cpu.yml -f compose.public.yml --profile public-bind up -d
```

Caddy provisions a certificate for `BEAMFALL_PUBLIC_HOST` and reverse-proxies to
the server on the internal network. `compose.public.yml` pins that internal
network to a fixed subnet and sets `BEAMFALL_KNOWN_PROXIES` to it automatically
(BUGREV0925-CORE-LAN-PROXY-1), so every client of the proxy is correctly read
as non-LAN — the server refuses to treat a relayed request as LAN unless it can
attribute the relay to a declared proxy.

## Environment variables

| Variable | Default | Meaning |
|----------|---------|---------|
| `BEAMFALL_IMAGE` | — | **Required.** Image reference; the template refuses to start without it but accepts any value. Use `ghcr.io/beamfall/core@sha256:…` from the release deployment bundle, or your own tag for a source build. |
| `BEAMFALL_MEDIA_DIR` | `/srv/media` | Host media library, mounted read-only. |
| `BEAMFALL_HTTP_BIND` | `127.0.0.1` | Host bind address for the HTTP port. |
| `BEAMFALL_HTTP_PORT` | `2326` | Host HTTP port. |
| `BEAMFALL_TRANSCODE_CAP` | `2` | Max concurrent transcode sessions. |
| `BEAMFALL_RENDER_GID` | `109` | Render group id (Intel/VAAPI overlay). |
| `BEAMFALL_PUBLIC_HOST` | — | HTTPS hostname (required by `compose.public.yml`). |
| `BEAMFALL_KNOWN_PROXIES` | `172.28.90.0/24` | CIDR the server trusts to relay requests (`compose.public.yml` sets this to its pinned proxy subnet; leave the default unless you changed that subnet). |

Server state lives in the `beamfall-data` named volume; the media mount is
always read-only so the server never mutates source files. For a Compose recipe
tuned to a NAS (Unraid, TrueNAS), see [`../unraid/`](../unraid/) and
[`../truenas/`](../truenas/).
