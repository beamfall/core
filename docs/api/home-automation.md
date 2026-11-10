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
  multicast and broadcast addresses are always refused.
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
