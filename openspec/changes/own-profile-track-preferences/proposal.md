# A Profile saves its own audio and subtitle track preferences

Ticket: CORE-1937 (`queue:core:main`).

## Why

Clients save a manual audio or subtitle pick through `updateProfile`, the only server write for a
Profile's track preferences. That mutation is an Admin Session (Console) field, so a session without
an Admin Session is refused and the client keeps the pick on one device only. The choice does not
follow the Profile to its other devices.

## What Changes

- `updateProfile` accepts a write from an enrolled device session without an Admin Session when the
  `profileID` is the session's own Profile and the request sets only `audioLanguages`,
  `subtitleLanguages`, `subtitleMode` and `preferSDH`.
- Every other `updateProfile` argument (name, kind, disabled, stream cap, remote bitrate cap,
  transcode gate, entry PIN), and any write to another Profile, still requires an Admin Session.
- `updateProfile` stays listed as a Console field; the schema manifest tier and the privileged
  audit trail are unchanged, so a self-service write is still audited.

## Non-goals

- A new mutation or wire field. Clients keep calling `updateProfile` with the same arguments.
- A server field for the subtitle look (size, colour, edge). It is stored on the client today; moving
  it to the server is separate work.
- Client changes. The macOS Viewer's local-only fallback is a beamfall-apple follow-up.

## Capabilities

### New Capabilities

- `profile-track-preferences`: who may write a Profile's audio and subtitle track preferences.

### Modified Capabilities

None.

## Impact

- Code: `internal/graph/graph_dispatch.go` (authorization exception), `internal/graph/graph.go`
  (authorization probes carry the field's input).
- Failure modes: the exception widens to a policy field or another Profile. The exception accepts
  only the four track arguments and the session's own Profile ID, and tests cover each refusal.
- Rollback: revert the commit; `updateProfile` returns to Admin Session only.
