# Keep a web session servable when its own grants change

Ticket: CORE-0042 (`queue:core:main`).

## Why

A web UI session's compatibility decision is bound to the session's authorization state. When that
state changes mid-session (for example, the first-run setup wizard creates a Library, which grants
it to the owner Profile), the stored decision no longer matches and every later request on the
session is refused with `client-too-old` / `unknown-build` until the page reloads and handshakes
again. First-run setup breaks after its first step.

## What Changes

- When a web UI session's only mismatch with its stored decision is a changed authorization state,
  the server re-derives the decision from its own embedded UI contract under the new authorization
  state, the same way it derives a first decision for a session it has never seen.
- The binding itself is unchanged: authorization state stays part of the decision key.

## Non-goals

- Native, API-key and admin-token credentials. They stay fail-closed on an authorization change
  and must handshake again.
- Expired decisions and policy-generation changes on a client-submitted decision. They keep their
  current outcome even when the authorization state also changed.
- Rehabilitating a refused subject. Only a client-submitted handshake can do that.

## Failure modes

- A refused subject (in this process or by a durable refusal) stays refused across an
  authorization change.
- If the re-derived decision is not servable, the request is refused with that decision.
- If the re-derived decision cannot be stored, the request is refused with `client-too-old`.

## Acceptance evidence

- `internal/server/compat_test.go`: a web session that handshakes, creates a Library and then reads
  on the same session is served; a native session doing the same is refused with
  `client-too-old` / `unknown-build`; a refused web subject stays refused across a grant change
  until it submits a servable handshake; a submitted web decision whose generation is stale, and an
  expired provisional decision, stay refused when the grants also changed.
- `go test ./internal/server/... -race`.

## Rollback

Revert the change. No stored state or wire contract changes; web sessions return to needing a
reload after an authorization change.

## Capabilities

### New Capabilities

- `client-compat-binding`: how a stored client compatibility decision follows authorization changes.

### Modified Capabilities

None.
