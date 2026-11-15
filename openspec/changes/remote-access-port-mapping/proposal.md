# Automatic port mapping and reachability self-test

Ticket: CORE-1969 (`queue:core:main`).

## Why

Direct remote access needs an inbound port forward on the home router. Most operators never set one
up, so the server is reachable only over an overlay or a relay, and the Console cannot tell them
whether a direct path works at all. Other media servers ask the router for the forward themselves
(UPnP IGD or NAT-PMP) and report whether the server can be reached from outside.

## What Changes

- An opt-in Console toggle, stored as `remoteaccess.port_mapping_enabled` in the existing
  `server_config` settings table (no migration). Off by default.
- When on, the server discovers the gateway (UPnP IGD over SSDP and SOAP first, then NAT-PMP per
  RFC 6886), asks for a TCP mapping of its listening port, renews it at half the granted lease,
  and retries with backoff after a failure. A lease that runs out without a renewal is reported as
  expired.
- Turning the toggle off, and server shutdown, delete the mapping. Shutdown removal is bounded by a
  timeout so a silent gateway never holds the process.
- Both protocols are implemented in-repo (no new module). All network I/O sits behind interfaces so
  unit tests run against a fake gateway and never touch a real network.
- A reachability self-test. The server sends a TLS ClientHello that carries a one-time nonce in
  ALPN to the mapped external address and, when a self-hosted relay is configured, to the relay's
  public host. The server's own TLS listener recognises the nonce and aborts the handshake. No
  request, no library data and no credentials are ever sent; the relay forwards the ClientHello
  byte-blind like any other public connection, so the relay path is checked with the routing it
  already has.
- Console Remote access shows the mapping state, protocol, mapped port, external address and lease,
  and a reachability verdict per path with a reason code when a path is unreachable.
- New admin mutations `setRemoteAccessPortMapping(enabled)` and `checkRemoteAccessReachability`,
  both returning `RemoteAccessInfo`. The toggle is audited as a config update.

## Non-goals

- A dial-back from an outside vantage point. The direct check relies on the router looping the
  connection back (NAT hairpin); without hairpin the verdict says `timeout` or `connection_refused`
  even when outside clients could connect.
- UDP mappings, IPv6 pinholes (PCP) and choosing the external port by hand.
- Any third-party reachability service.

## Capabilities

### New Capabilities

- `remote-access-port-mapping`: automatic router port mapping and the reachability self-test.

### Modified Capabilities

None.
