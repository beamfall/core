# Runbook: Metrics

Scrape, dashboard, and alert on Beamfall Core.

## Exposed series (`GET /metrics`, Prometheus text v0.0.4)
- `beamfall_up` — liveness (1 when serving).
- `beamfall_jobs{class,status}` — job counts by class/status.
- `beamfall_sessions_active` — active sessions.
- `beamfall_transcode_active` — in-flight transcodes.
- `beamfall_library_media_total` — library size.
- `beamfall_*_error` sentinels — set to 1 when a sub-collector fails.

## Breaking change: every scrape now needs the bearer
**A local scrape without the bearer is refused after upgrade.** `curl http://127.0.0.1:8800/metrics`
from the server's own host used to return the exposition; it now returns 403. Nothing about where a
request comes from earns it a scrape any more. There are exactly two ways in:

1. present the scrape credential (below) — the normal path, for local and remote scrapers alike;
2. set the `--public-metrics` / `BEAMFALL_PUBLIC_METRICS=true` opt-in, which serves every caller
   the proxy lets through with no credential at all.

Fix a local scraper before you upgrade:

```sh
curl -sS -H "Authorization: Bearer $(cat <data-dir>/secrets/metrics-scrape-token)" \
  http://127.0.0.1:8800/metrics
```

**Why the host-local carve-out was withdrawn.** The default bind is `127.0.0.1:8800`, so a reverse
proxy in front of the server runs on the same host and every request it relays arrives with a
loopback peer address. The server can refuse a relayed request it *recognises* — the
`BEAMFALL_KNOWN_PROXIES` allowlist, or the hop headers a relaying proxy adds (`Forwarded`,
`X-Forwarded-For`, `X-Forwarded-Host`, `X-Forwarded-Port`, `X-Forwarded-Proto`, `X-Real-Ip`, `Via`).
But nginx's bare `location / { proxy_pass http://127.0.0.1:8800; }` declares nothing and adds none
of them, so the request it relays from the internet is byte-for-byte what a local client sends. No
check can separate the two, so serving the local one uncredentialed served the relayed one too. The
credential is what a relayed client cannot obtain and a local operator can.

## The scrape credential
- **Where.** `<data-dir>/secrets/metrics-scrape-token`, mode `0600`, in a `0700` directory, both
  owned by the user the server runs as. Being able to read that file is the whole of the
  authorization: it does not depend on the peer address, on what the fronting proxy sends, or on
  `BEAMFALL_KNOWN_PROXIES`.
- **How to present it.** `Authorization: Bearer <the file's contents>`, e.g.
  `curl -sS -H "Authorization: Bearer $(cat <data-dir>/secrets/metrics-scrape-token)" http://127.0.0.1:8800/metrics`.
- **It rotates on every restart.** The server mints a new credential each start and rewrites the
  file; the previous value stops working immediately. A scraper must read the file at scrape time
  (or re-read it on the first 403) rather than caching the value into its own config — a Prometheus
  deployment should point `authorization.credentials_file` at that path, which re-reads it, not
  paste the value into `authorization.credentials`.
- **It is never logged and never echoed.** The refusal body is one constant sentence for a wrong
  credential, an absent one, a host-local caller, and an off-host caller alike, so a 403 tells a
  prober nothing.
- **Send it over TLS if it leaves the host.** A bearer on plain HTTP is readable in transit;
  terminate TLS at the proxy for any scrape that crosses the network.
- **If no credential could be provisioned, `/metrics` closes.** An unwritable secrets directory
  logs `metrics scrape credential unavailable` and leaves the server with none, which refuses every
  bearer. Boot continues; the scrape does not come back until the directory is writable and the
  server restarts.

### Reading a `0600` file as a different user
This is the sharp edge. A scraper that does not run as the server's user cannot read the token, and
cannot be given access by relaxing permissions: the secrets directory is `0700`, so a different user
cannot even traverse into it, and **the server replaces the file (temp + rename, fresh `0600`) on
every start** — so any `chmod`, `chown`, ACL, or bind mount you apply to the file is undone by the
next restart.

- **Republish only the token after every start.** This is the recommended cross-user option. A
  root-owned (or server-user-owned) unit ordered
  `After=` the server copies the token to a path the scraper can read, with ownership and mode the
  scraper needs. It must re-run on every server start, not once at install — a copy taken before a
  restart is a stale credential and the scraper will 403. Treat the copy as a second secret: mode
  `0600`, owned by the scraper's user, never in a world-readable directory. For a container, mount
  only that republished file, or a dedicated directory containing only that file.
- **If the scraper already runs as the server's user, read the original token directly.** Do not
  change a scraper to that user merely to obtain this credential, and do not bind-mount
  `<data-dir>/secrets` into a scraper. That is the process-wide secret store: it also contains
  encrypted-Library DEK wraps and KEK material, the relay carrier token, subscription authorization
  secrets, metadata-scraper credentials, the qBittorrent ingestion password, and Integration Worker
  secrets. Sharing it grants the scraper access far beyond `/metrics`.
- **Skip the credential entirely with `--public-metrics`** and authenticate at the proxy instead —
  appropriate when the scraper is remote and the proxy already terminates TLS and authenticates.
  Do not take this to work around a local permissions problem; it opens the surface to every caller
  the proxy lets through.

## Deny the scrape at the edge
1. Deny the path at the proxy, ahead of the rule that forwards everything else:
   - nginx — `location = /metrics { return 404; }`
   - Caddy — `handle /metrics { respond 404 }`
   - Traefik / HAProxy — publish only the paths you intend to expose; never route `/metrics`.
2. Declaring the proxy in `BEAMFALL_KNOWN_PROXIES` no longer changes who is served `/metrics` —
   the credential decides, in both directions. Set it for the other trust-boundary decisions it
   governs, not for this one.
3. Verify from off-host that the public name does not serve the scrape:
   `curl -sS -o /dev/null -w '%{http_code}\n' https://<public-host>/metrics` — expect the proxy's
   404, or 403 from the server; a 200 means the deny rule is missing or ordered after the
   catch-all forward.

## Procedure
1. Scrape `/metrics` from anywhere, presenting the scrape credential above — including from the
   server's own host, which no longer has a credential-free path. The `--public-metrics` /
   `BEAMFALL_PUBLIC_METRICS=true` opt-in is the alternative, unchanged: it serves every caller the
   proxy lets through with no credential at all, so take it only behind the hardened public
   TLS/reverse-proxy deployment and pair it with authentication at the proxy.
2. Alert on the error sentinels (`beamfall_jobs_stats_error`, `beamfall_sessions_active_error`,
   `beamfall_library_media_total_error`) > 0.
3. Wire the DEC-6 alerts (relay over-cap, transcode-failure, HTTP 5xx > 1%) once those counters
   exist — several are not emitted by core today.

## Targets
- Alert thresholds:
  - relay over-cap: alert on any sustained over-cap event once the counter exists.
  - transcode-failure: alert on any sustained failure run once the counter exists.
  - HTTP 5xx ratio: alert when 5xx responses exceed 1% of requests over the scrape window.
- Counters not yet emitted: 5xx ratio and relay over-cap still need instrumentation; until then,
  operators must derive them from ingress/proxy logs and relay telemetry.
