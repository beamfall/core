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
