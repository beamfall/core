# Beamfall Core on TrueNAS SCALE

TrueNAS SCALE catalog listing for Beamfall Core. The listing deploys the
published container image `ghcr.io/beamfall/core` — there is no separate
TrueNAS build.

## Install as a custom app (self-service)

TrueNAS SCALE **Electric Eel (24.10)** and later install custom apps from a
Docker Compose spec:

1. **Apps → Discover Apps → Custom App → Install via YAML**.
2. Paste [`docker-compose.yaml`](./docker-compose.yaml) — or the pre-rendered,
   digest-pinned copy from the release's `beamfall-deploy-manifests.tar.gz`
   bundle, which already carries the image reference.
3. In the environment section, set:
   - `BEAMFALL_IMAGE` — **required** when pasting this source template:
     the immutable `ghcr.io/beamfall/core@sha256:…` reference from the release
     deployment bundle. The template fails closed without it; mutable channel
     tags are rejected by release policy.
   - `BEAMFALL_DATA_HOST_PATH` — server state + SQLite (read-write dataset).
   - `BEAMFALL_MEDIA_HOST_PATH` — media library (mounted read-only).
   - `BEAMFALL_TRANSCODE_HOST_PATH` — transcode scratch (read-write dataset).
4. Optional overrides: `BEAMFALL_HTTP_PORT`, `BEAMFALL_HTTP_BIND`
   (default `127.0.0.1`), `BEAMFALL_TRANSCODE_CAP`, `BEAMFALL_KNOWN_PROXIES`
   (see [Network exposure](#network-exposure-beamfall_http_bind)).
5. Open the Web UI at `http://127.0.0.1:8800/` from the TrueNAS host (or
   through an SSH tunnel: `ssh -L 8800:127.0.0.1:8800 <truenas-host>`) and
   finish setup in the Beamfall Console. Add further Library roots from the
   Console — the compose file mounts one read-only media dataset by default.
   To reach the UI from other machines, see
   [Network exposure](#network-exposure-beamfall_http_bind).

## Network exposure (`BEAMFALL_HTTP_BIND`)

The management HTTP port is **plaintext** (no TLS), and it carries bootstrap
and session traffic. The template therefore publishes it only on the host
loopback interface by default: `BEAMFALL_HTTP_BIND` (default `127.0.0.1`).

Exposing the port beyond loopback is an explicit operator decision, not a
default. Set `BEAMFALL_HTTP_BIND` to a specific interface address only when an
intentional TLS boundary fronts it — a reverse proxy (Caddy, nginx, Traefik,
or the TrueNAS built-in certificates + proxy) that terminates TLS and forwards
to `127.0.0.1:8800`. Binding to `0.0.0.0` on a shared or untrusted network
sends credentials and session tokens in the clear.

When a reverse proxy fronts this container, also set `BEAMFALL_KNOWN_PROXIES`
to that proxy's address or CIDR (default: unset, meaning no proxy is trusted).
The server refuses to treat a relayed request as LAN unless it can attribute
the relay to a declared proxy — undeclared, every client of that proxy would
otherwise read as a direct LAN peer, skipping the pairing LAN gate and getting
a non-`Secure` session cookie.

Create the `data` and `transcode` datasets before install; mount your media
dataset read-only so the server can never mutate source files.

## Catalog submission (maintainers)

The public **Discover Apps** listing is a PR to the
[`truenas/apps`](https://github.com/truenas/apps) catalog repository. This
directory is the source of truth for that submission:

- The rendered app template wraps [`docker-compose.yaml`](./docker-compose.yaml)
  (same image, ports, and volume contract as the compose reference).
- Map the three host paths to `hostPath`/`ixVolume` questions, and the four
  optional env vars to their catalog question equivalents.
- Keep metadata domain-agnostic — "media server", never a media-domain vocabulary
  term (Beamfall Core hard invariant 1).

Record the submitted catalog PR link as the LIBAUTO-5 check-off evidence.
