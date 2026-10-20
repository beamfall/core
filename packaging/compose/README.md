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

## Immutable image reference (required)

The templates fail closed: they refuse to start unless `BEAMFALL_IMAGE` is set
to an immutable digest reference (`ghcr.io/beamfall/core@sha256:…`). Mutable
channel tags (`stable`, `beta`) are rejected by release policy because runtime
tag resolution is not what the release signed. Every GitHub release publishes a
`beamfall-deploy-manifests.tar.gz` bundle containing pre-rendered, digest-pinned
copies of these files plus a binding manifest tying that digest to the release's
cosign-signed image — use a rendered file directly, or copy its digest into
`BEAMFALL_IMAGE`.

## Quick start (CPU)

```sh
BEAMFALL_IMAGE=ghcr.io/beamfall/core@sha256:<digest-from-release> \
BEAMFALL_MEDIA_DIR=/srv/media \
  docker compose -f compose.cpu.yml up -d
```

Then open `http://127.0.0.1:8800/` and finish setup in the Beamfall Console.

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
| `BEAMFALL_IMAGE` | — | **Required.** Immutable image reference `ghcr.io/beamfall/core@sha256:…` from the release deployment bundle; the template fails closed without it. |
| `BEAMFALL_MEDIA_DIR` | `/srv/media` | Host media library, mounted read-only. |
| `BEAMFALL_HTTP_BIND` | `127.0.0.1` | Host bind address for the HTTP port. |
| `BEAMFALL_HTTP_PORT` | `8800` | Host HTTP port. |
| `BEAMFALL_TRANSCODE_CAP` | `2` | Max concurrent transcode sessions. |
| `BEAMFALL_RENDER_GID` | `109` | Render group id (Intel/VAAPI overlay). |
| `BEAMFALL_PUBLIC_HOST` | — | HTTPS hostname (required by `compose.public.yml`). |
| `BEAMFALL_KNOWN_PROXIES` | `172.28.90.0/24` | CIDR the server trusts to relay requests (`compose.public.yml` sets this to its pinned proxy subnet; leave the default unless you changed that subnet). |

Server state lives in the `beamfall-data` named volume; the media mount is
always read-only so the server never mutates source files. For a Compose recipe
tuned to a NAS (Unraid, TrueNAS), see [`../unraid/`](../unraid/) and
[`../truenas/`](../truenas/).
