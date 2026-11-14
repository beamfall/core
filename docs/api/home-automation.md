# Home automation integration

Beamfall does not ship a first-party Home Assistant integration or a smart-home
Integration Worker. Beamfall owns the stable Developer API, SSE stream, webhook
emitter, and examples; Home Assistant/HACS-specific code belongs outside Core.

This keeps the server generic and avoids binding Beamfall releases to a vendor
integration lifecycle. Reopen ADR-0040 only if documented recipes cannot express
movie mode, presence-aware pause/resume, or Ambient Display without new
privileged server seams.

## Stable surfaces

Use scoped Developer API keys with the smallest scope set that fits the
automation:

| Surface | Scope | Use |
|---|---|---|
| `GET /v1/sessions` | `read-sessions` | Discover active playback sessions, Profile-filtered for Profile-bound keys. |
| `GET /v1/events` | `events-subscribe` | Subscribe to the stable SSE envelope. |
| `POST /v1/playback-control` | `playback-control` | Publish `pause`, `resume`, `skip-media`, or `skip-track` to a visible active session. |
| Webhook Emitter Integration Plugin | operator configured | Push selected SSE event kinds to HMAC-signed HTTP endpoints. |

The stable SSE/webhook envelope is:

```json
{
  "kind": "session.control",
  "aggregateType": "session",
  "aggregateID": "session-default",
  "occurredAt": "2026-07-02T00:00:00Z",
  "payload": {}
}
```

`kind`, `aggregateType`, `aggregateID`, `occurredAt`, and existing payload fields
are part of the `/v1` compatibility promise. Additive fields and new event kinds
are allowed within `/v1`; breaking changes require `/v2` and the ADR-0040
deprecation window.

## Webhook delivery rules

Each endpoint in `webhook-endpoints.json` gets an HMAC-signed POST per matching
event, retried with exponential backoff up to `max_attempts`, then dead-lettered.
Delivery is bounded:

- The endpoint `url` must be an absolute `http` or `https` URL with a host and
  no user info. Put credentials in the path or query token the receiver issues,
  or rely on the HMAC signature. A malformed or unsafe URL stops the server at
  boot. Nothing is sent.
- Redirects are never followed. A `3xx` answer counts as a failed attempt, so
  the event body and `X-Beamfall-Signature` reach only the configured endpoint.
- Unspecified, `0.0.0.0/8`, link-local (including `169.254.169.254`),
  multicast and broadcast addresses are always refused. So are the other
  special-purpose blocks the scraper refuses, such as the documentation
  ranges, `198.18.0.0/15` and `240.0.0.0/4`, and IPv6 forms that carry an IPv4
  address (NAT64 `64:ff9b::/96` and `64:ff9b:1::/48`, 6to4 `2002::/16`,
  IPv4-compatible `::/96`, Teredo `2001::/32`).
- Loopback, private (`10/8`, `172.16/12`, `192.168/16`, `fc00::/7`), shared
  (`100.64/10`) and `fec0::/10` addresses are refused unless the endpoint is
  approved for the local network. This is checked on the address actually
  dialed, after DNS resolution.
- Environment proxy settings are ignored.

A LAN receiver such as Home Assistant is approved for the local network in one
of two ways. Configure it by private IP literal
(`http://192.168.1.10:8123/api/webhook/<id>`), or name it by hostname and set
`allow_private_network`:

```json
{
  "endpoints": [
    {
      "id": "home-assistant",
      "url": "http://homeassistant.local:8123/api/webhook/beamfall",
      "secret_ref": "home-assistant-webhook",
      "event_kinds": ["session.control"],
      "allow_private_network": true
    }
  ]
}
```

Leave `allow_private_network` unset for internet receivers so that a DNS answer
pointing inward is refused.

**Upgrading:** an endpoint configured by LAN hostname before these rules
existed stops receiving deliveries until you set `allow_private_network` on it
or switch its URL to the receiver's private IP literal. Its deliveries fail
with `webhook transport failed` and are dead-lettered. At startup the server
logs one warning per endpoint whose hostname looks local (`localhost`, a
single-label name, or a `.local`, `.lan`, `.home`, `.internal`, `.localdomain`
or `.home.arpa` suffix) and lacks the flag. A hostname under a public domain
that resolves to a LAN address gets no warning, so check the dead-letter after
upgrading.

## Managing webhooks in the console

Owners add, edit, test and remove webhooks under **Console → Access →
Webhooks** (an advanced page). This needs an Admin Session. The page writes
the same `webhook-endpoints.json` the server reads at boot, so endpoints added
by hand and in the console live side by side. A save is applied straight away:
the emitter restarts with the new endpoint list and no server restart is needed.
Each save, delete and settings change is logged to the activity log as a
`config.update` with the target type `webhook`.

### Formats

Each endpoint has one format, which decides the request shape:

| Format | Target URL | Secret | Body |
|---|---|---|---|
| `discord` | the channel webhook URL | none (the URL is the credential) | an embed with the headline, the details and the poster attached as an image |
| `slack` | the incoming-webhook URL | none (the URL is the credential) | Block Kit text with the poster as an image accessory |
| `ntfy` | the topic URL, such as `https://ntfy.sh/<topic>` | optional access token, sent as `Authorization: Bearer` | title, message, tags and priority headers; the poster is attached when the server allows attachments |
| `gotify` | the Gotify server URL | the application token, sent as `X-Gotify-Key` | title, message and priority, with the poster as `bigImageUrl` |
| `email` | `smtps://host[:465]` (implicit TLS) or `smtp://host[:587]` (STARTTLS required) | the SMTP password | a plain-text message from **From** to each **To** address, signed in as **Username** |
| `generic` | any receiver | the HMAC key | the JSON event body, signed in `X-Beamfall-Signature` as described above |

Endpoints from files written before formats existed are `generic`. A password
is never sent over an unencrypted SMTP connection: `smtp://` without STARTTLS
is accepted only for a local server reached without a password. All formats
follow the delivery rules above, including the local-network approval.

### Events

An endpoint subscribes to any of these derived events, or to every event when
none is ticked. A `generic` endpoint with no filter also receives the raw event
kinds it received before formats existed (such as `session.control`):

| Event | When |
|---|---|
| `media.added` | a new item is added to a library |
| `playback.started`, `playback.stopped` | a live session starts or stops playing an item |
| `profile.switched` | a device switches its active profile |
| `admin.signed_in`, `admin.sign_in_failed` | an owner signs in to the console, or a sign-in is refused |
| `device.enrolled`, `device.revoked` | a device is paired or its access is revoked |
| `session.terminated` | an owner ends a live session |

Item events carry the title, the media class and year (where known) and an
artwork URL. The artwork URL is built from the **Public server URL** set on the
Webhooks page. Leave that blank and no URL is sent. Fetching it needs a
session, so a chat receiver cannot load it by itself. For that reason, Discord
and ntfy messages attach the poster image instead of linking to it.

### Secrets

Secrets are written to a `0600` file in the server's secret store and
referenced by `secret_ref`. They are never written to `webhook-endpoints.json`
and never returned by the API. The console shows only whether a secret is set.
Leave the field blank on an edit to keep the stored secret. A URL is shown only
as scheme and host, because a Discord or Slack URL, or an ntfy topic, is
itself a credential. Leave the URL blank on an edit to keep it. Deleting an
endpoint also deletes its stored secret. An endpoint whose secret is missing
or whose saved settings no longer validate shows as **Not running** with the
reason, and it sends nothing until fixed.

### Test sends and the delivery log

**Send test** delivers one `webhook.test` notification to that endpoint only,
and shows the result inline: delivered with the round-trip time, or the fixed
failure diagnostic. A test send skips the retry schedule.

The delivery log lists the last 200 deliveries, newest first. Each row gives
the endpoint, the event, the outcome with its status class (`2xx`, `4xx`,
`5xx` or a transport failure), the number of attempts, the duration and the
time. The log holds no event bodies, URLs or response text.

## Recipes

### Movie mode

Subscribe to `/v1/events` or configure the Webhook Emitter for playback/session
event kinds. Let the home-automation system trigger the lighting scene. Beamfall
does not store or execute the scene.

### Presence-aware pause/resume

Let the home-automation system own presence inference. It reads
`GET /v1/sessions`, chooses the target session, and calls
`POST /v1/playback-control` with `pause` or `resume`.

### Ambient Display

Treat Ambient Display as session state, not a smart-home object model. Use
Profile-gated session discovery/events for presence and use playback control for
transport. Ambient Groups remain explicit Beamfall session/group state; external
automations may observe them but must not infer cross-Profile presence.

## Boundaries

- Do not add a first-party Home Assistant worker while the `/v1` and webhook
  surfaces cover the workflow.
- Do not promote Integration Worker mutating seams for smart-home automation;
  ADR-0125 requires loop-containment before any worker mutation seam becomes
  live.
- Do not add Home Assistant, room, scene, or presence schema to Core.
- Do not expose private `/api` or GraphQL routes as integration contracts.
