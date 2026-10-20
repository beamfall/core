# Runbook: Deploy

Stand up or update a Beamfall Core server instance.

## Procedure
1. Pull the target release image/binary (pin by digest/tag).
2. Provide config + secrets via the secure store or `0600` files — never plaintext in VCS (inv. 5).
3. Bind the service per the hardened posture: compose publishes `127.0.0.1` by default. Public
   exposure is opt-in only via the hardened profile, for example
   `BEAMFALL_PUBLIC_HOST=beamfall.example.com docker compose -f packaging/compose/compose.cpu.yml -f packaging/compose/compose.public.yml --profile public-bind up -d`.
   The profile publishes Caddy on public HTTP/HTTPS for TLS termination and reverse-proxies to the
   internal `beamfall:8800` service; never publish Beamfall itself as the copy-paste default. The
   profile also pins that internal network to a fixed subnet and sets `BEAMFALL_KNOWN_PROXIES` to
   it automatically (BUGREV0925-CORE-LAN-PROXY-1) — without a declared known proxy the server
   refuses to treat any relayed request as LAN, so every client of an undeclared proxy would fail
   the pairing LAN gate. Fronting with a different or external reverse proxy instead of the shipped
   profile means setting `BEAMFALL_KNOWN_PROXIES` yourself to that proxy's address or CIDR.
4. Start the server; confirm `GET /metrics` returns `beamfall_up 1`. **Every scrape needs the
   bearer, including from loopback** — an uncredentialed local scrape that worked before an upgrade
   now returns 403:
   `curl -sS -H "Authorization: Bearer $(cat <data-dir>/secrets/metrics-scrape-token)" http://127.0.0.1:8800/metrics`.
   The token is `0600` in a `0700` directory owned by the server's user and is rewritten on every
   start, so a scraper running as a different user must run as the server's user or have the token
   republished after each restart — see [`metrics.md`](metrics.md). The explicit `--public-metrics`
   / `BEAMFALL_PUBLIC_METRICS=true` opt-in is the alternative and serves every caller with no
   credential.
5. Run the post-deploy smoke (health + a read query) before routing traffic.

## Targets
- Maintenance window / rollout cadence: pre-launch releases may roll during an announced operator
  window; take a fresh backup first so RPO remains <= 24h.
- Post-deploy smoke definition: confirm `/health`, `beamfall_up 1`, error sentinels at 0, and one
  representative read query. ORG-39 can expand this into an automated deploy-smoke command.
