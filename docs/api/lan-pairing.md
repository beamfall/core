# LAN discovery and pairing

How a client finds a Beamfall server on the local network and pairs with it. These routes are part
of the private client surface (`/api/...`), not the versioned `/v1` Developer API; the wire
contract in `internal/conformance` is authoritative.

## Discovery (mDNS / DNS-SD)

When discovery is enabled (on by default; `--discovery-enabled=false` or `BEAMFALL_DISCOVERY_ENABLED=false` turns it off), the server advertises one
`_beamfall._tcp` service in `local.` on the port it listens on. The instance name is the server
name. The TXT record carries:

| Key | Value | Meaning |
|---|---|---|
| `version` | empty | Marks a Beamfall record. Clients must not parse a value. |
| `path` | `/` | Base path of the server on the advertised port. |
| `scheme` | `http` or `https` | What the advertised port answers to a LAN client. `http` on a default install; `https` when direct TLS (the operator's own domain) is on. Build the base URL as `<scheme>://<host>:<port><path>`. |
| `id` | 32 lowercase hex characters | Stable, public server id. Use it to recognise the same server across address changes and to de-duplicate records seen on several interfaces. |

The `id` is a one-way, domain-separated hash of the server's public key pin. It is not a
credential and not key material: knowing it grants nothing, and it changes only if the server's
identity key changes.

The record never says whether the server has been set up. Advertising that a server is unclaimed
would tell everyone on the network it is open to be claimed. A client learns setup state from the
server itself after connecting.
