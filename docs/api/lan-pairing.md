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

The seven routes are client-neutral; TV, phone, desktop and web clients all use them. Bodies are
JSON. Secrets travel only in request bodies, never in a URL.

### Device routes (no credential)

**`POST /api/pairing-requests`** → `201`

```json
{ "deviceName": "Living Room TV", "platform": "tvos", "requestedRole": "viewer", "distributionClass": "direct" }
```

```json
{ "code": "K7M2QX4D", "requestSecret": "…", "expiresAt": "2026-11-15T12:10:00Z", "expiresInSeconds": 600,
  "pollIntervalSeconds": 3 }
```

- `deviceName` is required: at most 64 characters after trimming, with no control or invisible
  formatting characters (bidi overrides, zero-width characters). Otherwise `400`.
- `platform` is optional, a free label the administrator sees (for example `tvos`, `android`,
  `macos`, `web`): at most 32 characters after trimming, with the same character rules as
  `deviceName`. Otherwise `400`.
- `requestedRole` is optional: `viewer` (default) or `manager`. It is only a request, shown to the
  administrator: an approval grants `viewer` unless the administrator names `manager`. Any other
  value, `admin` included, is `401`.
- `distributionClass` is optional: `direct` (default) or `appStore`.
- `code` is 8 characters from `A–Z2–7`. Show it on screen so the administrator can match it.
  It is not a credential: it cannot poll, and it never redeems through `/api/enroll`.
- `requestSecret` is the device's private handle on the request. Keep it in memory only; never
  display or log it.
- The request lives for 10 minutes. Poll every `pollIntervalSeconds`. Time the code by
  `expiresInSeconds`, counted from when the answer arrived; do not compare `expiresAt` with the
  device's own clock, which may be wrong.
- A request a browser made (it carries `X-Beamfall-Csrf`, `Origin` or `Sec-Fetch-Site`) is a
  browser request for its whole life: see the browser rules under poll.

**`POST /api/pairing-requests/poll`** → `200`

```json
{ "requestSecret": "…", "clientContract": { … } }
```

`clientContract` is optional and is the same object `/api/enroll` accepts. Responses:

```json
{ "status": "pending", "expiresAt": "2026-11-15T12:10:00Z", "expiresInSeconds": 412 }
{ "status": "denied" }
{ "status": "approved", "enrollment": { "deviceID": "…", "deviceSecret": "…", "sessionID": "…",
  "sessionExpiresAt": "…", "profileID": "…", "deviceRole": "viewer", "serverKeyPin": "sha256-…" } }
```

`enrollment` has the same shape as the `/api/enroll` response. `serverKeyPin` is present only when
the device both requested and collected on the LAN. `approved` and `denied` are delivered once:
the request is gone after that poll, and a later poll with the same secret is `401`.

A browser request is one made with the web app's CSRF header (`X-Beamfall-Csrf`) or with an
`Origin` or `Sec-Fetch-Site` header, which a browser adds itself. Native clients send none of the
three. The server
records that when the request is created and carries it through regenerate, so a later call cannot
shed it by leaving the header off; a call that carries any of them is treated as a browser call too.
A browser request is bound the way `POST /api/web/enroll` binds a browser: request, regenerate and
poll are refused with `403` over plain HTTP from outside the local network before the request is
touched, and an `approved` answer sets the session, device and device-secret cookies (HttpOnly,
SameSite=Strict) and returns `deviceSecret` and `sessionID` as empty strings, so the credential
never reaches page script. Native clients never send the header and keep the credential in the
body.

**`POST /api/pairing-requests/regenerate`** → `201`

```json
{ "requestSecret": "…" }
```

Returns a new `code`, `requestSecret` and `expiresAt` (same shape as the request response) for a
pending request. The old code and secret stop working at once. Use it when the code is about to
expire before the administrator gets to it. Wait for a poll that is on the wire to answer before
regenerating: it may be collecting an approval. Regenerate applies the same remote-enrollment rule
as the request, and the new request counts as on the LAN only if both the original request and the
regenerate were.

**`POST /api/pairing-requests/cancel`** → `204`

```json
{ "requestSecret": "…" }
```

Withdraws a request the device no longer wants, so it leaves the administrator's list at once
instead of waiting out its 10 minutes. Call it when the user cancels, pairs another way or leaves
the pairing screen. It answers `204` whether or not the secret named a live request, and it does
nothing to a request whose approval is being collected by a poll.

### Administrator routes (session with an active Admin Session)

**`GET /api/pairing-requests`** → `200`

```json
{ "requests": [ { "code": "K7M2QX4D", "deviceName": "Living Room TV", "platform": "tvos",
  "requestedRole": "viewer", "lan": true, "distributionClass": "direct",
  "requestedAt": "2026-11-15T12:00:00Z", "expiresAt": "2026-11-15T12:10:00Z" } ] }
```

Pending requests only, oldest first. Poll it while the pairing screen is open. It never returns a
request secret. `platform` is `""` when the device sent none. `requestedRole` is `viewer` or
`manager`. `lan` is derived by the server from the connection (true when the request came from the
local network, not through the relay or an undeclared proxy); a client cannot set it. `deviceName`
and `platform` are untrusted device input: display them as plain text.

**`POST /api/pairing-requests/approve`** → `200`

```json
{ "code": "K7M2QX4D", "profileID": "profile-1", "deviceRole": "viewer" }
```

```json
{ "code": "K7M2QX4D", "status": "approved", "profileID": "profile-1", "deviceRole": "viewer" }
```

`profileID` defaults to the administrator's own profile and must name an enabled profile.
`deviceRole` is `viewer` or `manager` and defaults to `viewer`, whatever the device requested:
`manager` is granted only by an approval that names it. A client that approves should make
granting `manager` a separate, explicit choice and should not put focus on an approving control
when a request appears. The code is matched case-insensitively.

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
| `400` | malformed body, bad `deviceName`, `platform` or `distributionClass` (`invalid pairing request`) | malformed body, missing `code`, unknown or disabled profile, bad role (`invalid pairing request`) |
| `401` | unknown, expired, spent, denied-and-read or regenerated-away secret; remote request with remote enrollment off; Viewer pairing codes disabled; `requestedRole` other than `viewer` or `manager` (`invalid pairing code`) | no valid session (`invalid session`) |
| `403` | — | session without an active Admin Session (`pairing forbidden`) |
| `404` | — | unknown, expired, already decided or regenerated-away code; Viewer pairing codes disabled (`invalid pairing code`) |
| `429` | too many failed attempts or too many pending requests from this address (`pairing attempts throttled`) | — |

Administrator routes answer an unusable code with `404`, not `401`, so a client does not mistake
it for a lost session.

### Limits and policy

- Device requests, polls, regenerates and cancels have their own throttle, keyed by remote address
  and separate from the one `/api/enroll` uses: five free failures, then back-off. Live polls of a
  real request do not count, and a poll with an unknown secret (a device still polling after a
  server restart, say) never locks its address out of code enrollment.
- At most 64 pending requests in total and 3 per remote address; more is `429`, and does not count
  as a failed attempt. Pending requests are held in memory and are lost on restart.
- Off the LAN, requests, regenerates and collection are refused unless remote enrollment is on in
  Console settings.
- With Viewer pairing codes disabled, a Viewer request, a Viewer approval and the collection of a
  Viewer approval are refused with the invalid-code answer. A `manager` request is still accepted
  and still waits for approval.
- No request can obtain the administrator role. A `manager` role is granted only by an
  administrator's approval that names it.
- Approvals, denials and enrollments appear in the activity log.
