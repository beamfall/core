# No LAN advert from a loopback-only listener

Ticket: CORE-2290 (`queue:core:main`).

## Why

Discovery is on by default and so is the `127.0.0.1:8800` listen address. A server started that way
advertised `_beamfall._tcp` on the LAN for a port no other device can connect to. Clients listed it,
and choosing it failed with no reachable endpoint. A development server left running on one machine
showed up on a phone as a server that never answered.

## What Changes

- The server does not advertise when its listener is bound to a loopback address only. It logs that
  it skipped the advert and why.
- A listener on a wildcard or LAN address advertises as before.

## Non-goals

- No change to the TXT record or the pairing flow.
- Clients filtering unreachable entries themselves (Android does this in `beamfall-android`).
