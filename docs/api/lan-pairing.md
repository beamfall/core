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

## Device-initiated pairing (tap to connect)

A device with no credential asks the server for a short code, shows it, and polls. An
administrator sees the request in the Console's pending list and approves or denies it. Nothing
is enrolled until an administrator approves, and the credential goes only to the device that holds
the request secret. Nobody types a code.

```
device                                 server                          administrator
  POST /api/pairing-requests     ──▶  pending entry (10 min)
  ◀── code, requestSecret               │
  show code                             │◀── GET /api/pairing-requests  (Console polls)
  POST …/poll {requestSecret}    ──▶  "pending"
                                        │◀── POST …/approve {code}
  POST …/poll {requestSecret}    ──▶  "approved" + enrollment (once)
```

The six routes are client-neutral; TV, phone, desktop and web clients all use them. Bodies are
JSON. Secrets travel only in request bodies, never in a URL.

### Device routes (no credential)

**`POST /api/pairing-requests`** → `201`

```json
{ "deviceName": "Living Room TV", "distributionClass": "direct" }
```

```json
{ "code": "K7M2QX4D", "requestSecret": "…", "expiresAt": "2026-11-15T12:10:00Z", "pollIntervalSeconds": 3 }
```

- `deviceName` is required: at most 64 characters after trimming, with no control or invisible
  formatting characters (bidi overrides, zero-width characters). Otherwise `400`.
- `distributionClass` is optional: `direct` (default) or `appStore`.
- `code` is 8 characters from `A–Z2–7`. Show it on screen so the administrator can match it.
  It is not a credential: it cannot poll, and it never redeems through `/api/enroll`.
- `requestSecret` is the device's private handle on the request. Keep it in memory only; never
  display or log it.
- The request lives for 10 minutes. Poll every `pollIntervalSeconds`.

**`POST /api/pairing-requests/poll`** → `200`

```json
{ "requestSecret": "…", "clientContract": { … } }
```

`clientContract` is optional and is the same object `/api/enroll` accepts. Responses:

```json
{ "status": "pending", "expiresAt": "2026-11-15T12:10:00Z" }
{ "status": "denied" }
{ "status": "approved", "enrollment": { "deviceID": "…", "deviceSecret": "…", "sessionID": "…",
  "sessionExpiresAt": "…", "profileID": "…", "deviceRole": "viewer", "serverKeyPin": "sha256-…" } }
```

`enrollment` has the same shape as the `/api/enroll` response. `serverKeyPin` is present only when
the device both requested and collected on the LAN. `approved` and `denied` are delivered once:
the request is gone after that poll, and a later poll with the same secret is `401`.

**`POST /api/pairing-requests/regenerate`** → `201`

```json
{ "requestSecret": "…" }
```

Returns a new `code`, `requestSecret` and `expiresAt` (same shape as the request response) for a
pending request. The old code and secret stop working at once. Use it when the code is about to
expire before the administrator gets to it.

### Administrator routes (session with an active Admin Session)

**`GET /api/pairing-requests`** → `200`

```json
{ "requests": [ { "code": "K7M2QX4D", "deviceName": "Living Room TV", "distributionClass": "direct",
  "requestedAt": "2026-11-15T12:00:00Z", "expiresAt": "2026-11-15T12:10:00Z" } ] }
```

Pending requests only, oldest first. Poll it while the pairing screen is open. It never returns a
request secret.

**`POST /api/pairing-requests/approve`** → `200`

```json
{ "code": "K7M2QX4D", "profileID": "profile-1", "deviceRole": "viewer" }
```

```json
{ "code": "K7M2QX4D", "status": "approved", "profileID": "profile-1", "deviceRole": "viewer" }
```

`profileID` defaults to the administrator's own profile and must name an enabled profile.
`deviceRole` (`viewer` or `manager`) defaults to the role the device asked for (`viewer`). The code
is matched case-insensitively.

**`POST /api/pairing-requests/deny`** → `200`

```json
{ "code": "K7M2QX4D" }
```

```json
{ "code": "K7M2QX4D", "status": "denied" }
```

### Status values and errors

`status` is one of `pending`, `approved`, `denied`.

| Status | Device routes | Administrator routes |
|---|---|---|
| `400` | malformed body, bad `deviceName` or `distributionClass` (`invalid pairing request`) | malformed body, missing `code`, unknown or disabled profile, bad role (`invalid pairing request`) |
| `401` | unknown, expired, spent, denied-and-read or regenerated-away secret; remote request with remote enrollment off; Viewer pairing codes disabled (`invalid pairing code`) | no valid session (`invalid session`) |
| `403` | — | session without an active Admin Session (`pairing forbidden`) |
| `404` | — | unknown, expired, already decided or regenerated-away code; Viewer pairing codes disabled (`invalid pairing code`) |
| `429` | too many failed attempts or too many pending requests from this address (`pairing attempts throttled`) | — |

Administrator routes answer an unusable code with `404`, not `401`, so a client does not mistake
it for a lost session.

### Limits and policy

- Device requests and polls share the enrollment throttle, keyed by remote address: five free
  failures, then back-off. Live polls of a real request do not count.
- At most 64 pending requests in total and 3 per remote address; more is `429`. Pending requests
  are held in memory and are lost on restart.
- Off the LAN, requests and collection are refused unless remote enrollment is on in Console
  settings.
- With Viewer pairing codes disabled, a Viewer request, a Viewer approval and the collection of a
  Viewer approval are refused with the invalid-code answer.
- Approvals, denials and enrollments appear in the activity log.
