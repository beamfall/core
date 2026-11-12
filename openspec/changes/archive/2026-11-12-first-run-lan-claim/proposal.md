# Let a local-network browser complete first-run setup without the setup token

Tickets: CORE-1105, CORE-1097 (`queue:core:main`).

## Why

A new server can only be set up by someone who copies the one-time setup token out of the server's
console output. On a NAS, in a container or on a headless box that means finding the container log
or the systemd journal, and the printed URL names `127.0.0.1` when the server listens on every
interface, so it does not even open from another machine (CORE-1097). Every other part of setup
already works from a phone or laptop on the same network. The token step is the one place where a
normal home user gets stuck.

## What Changes

- While first-run setup is pending (no owner yet), a setup request that arrives on the local
  network may complete setup without presenting the setup token. "Local network" is the server's
  existing LAN test: a loopback, private or link-local source address, not relayed, and not
  forwarded except through a declared proxy.
- A request that is not on the local network still needs the setup token, exactly as today.
- A supplied token is still checked, so a wrong token is refused even on the local network.
- The setup throttle, the single-use window and the "setup already done" refusal are unchanged:
  once setup completes, no request can run it again, with or without a token.
- `GET /api/setup` also reports whether this request would need the token, so the web setup wizard
  skips its token step on the local network.
- When the server listens on every interface, the startup output prints the setup URL with the
  machine's reachable local addresses instead of `127.0.0.1`, plus a plain `/web/` address for a
  browser on the local network.

## Risk accepted

Anyone on the same local network can claim an unclaimed server in the window between first start
and setup. This is the same trust that already lets local-network devices pair without remote
enrollment, and the one most home media servers use. The window closes for good when setup
completes. A server reached through a port forward keeps its real source address and so still needs
the token; a deployment that rewrites every source to a private address (for example a container
userland proxy in front of an internet-facing port) already treats all traffic as local for
enrollment, and is documented as needing the token-required path closed by finishing setup before
exposing the port.

## Impact

- `internal/pairing`: `SetupRequest` gains a server-set LAN flag; `Bootstrap` accepts an empty
  token only when that flag is set.
- `internal/server`: the setup handlers set the flag and report `tokenRequired`.
- `internal/app`: the startup setup URL.
- `internal/web/app`: the setup wizard.
- ADR-0007 §2 is amended in the private ADR set.
