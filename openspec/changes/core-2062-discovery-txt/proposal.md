# Discovery TXT record: scheme and server id

Ticket: CORE-2062 (`queue:core:main`)

## Why

A client that finds a server over mDNS gets a host and a port but not the scheme, so it has to
guess http or https. It also has no stable way to tell that two records, or a record seen after a
DHCP change, are the same server. Tap-to-connect needs both before the device asks to pair.

## What changes

- The `_beamfall._tcp` TXT record gains `scheme` and `id` next to the existing `version` and
  `path=/` entries.
- `scheme` follows the rule the server already uses for its advertised LAN endpoint
  (`remoteAccess.endpoints`): `https` when direct TLS is active, otherwise `http`. On a default
  install the port answers cleartext HTTP to LAN clients, so the record says `http`.
- `id` is the first 16 bytes, hex, of SHA-256 over a fixed domain-separation label and the
  server's persistent self-signed public key pin. It is derived one-way from public data, so it
  is stable across restarts and address changes and is not key material. Core had no existing
  public server identifier to reuse (`serverInfo` exposes only name and version).
- The record never carries setup state.
- `docs/api/lan-pairing.md` documents the TXT keys for client authors.

## Non-goals

- No setup or claim state in the record (`setup=required` is deliberately absent).
- No change to the service type, domain, instance name or port.
- No client change; reading the new keys is client work.

## Failure modes

- If the key pin cannot be read, the server logs a warning and does not advertise, as it already
  does when registration fails. HTTP serving is unaffected.
- A client that ignores unknown TXT keys keeps working; old servers simply lack `scheme` and `id`.

## Acceptance evidence

- `internal/discovery` tests pin the TXT entries, reject bad schemes and ids, and check that the
  id is stable lowercase hex that does not expose the pin.
- Confirming a real device reads the record (CORE-1611) is client-side and out of scope here.

## Rollback

Revert the commit. The TXT record returns to `version` and `path=/`; no stored state changes.
