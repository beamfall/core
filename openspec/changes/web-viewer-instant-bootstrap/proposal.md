# Web viewer instant bootstrap and back navigation

Ticket: CORE-0964 (`queue:core:main`, PERF1110-WEB-BOOTSTRAP-1).

## Why

The embedded web viewer renders nothing until the whole session bootstrap chain (compatibility
handshake, current session, then manifest and Profile roster) settles, and every return to an
already loaded section or entity refetches it from scratch behind a skeleton. The viewer should
paint its chrome immediately and make back navigation instant without weakening the identity-epoch
guarantees of ADR-0004 (Reveal) and ADR-0082 (stale-callback guard).

## What Changes

- While the session bootstrap is pending, the Viewer area renders the app chrome with a skeleton
  instead of a blank screen. Viewer routes still do not mount until a session is bound, so no
  route-local request runs without an identity, and unenrolled or failed bootstraps still end at
  the enroll, setup or retry surfaces they reach today.
- An in-memory, size-bounded viewer data cache, scoped to the bound identity (device role, Profile
  and Reveal state), serves already loaded section items and entity details immediately and
  revalidates them in the background. It is cleared at every identity-epoch transition and on any
  authorization rejection, and it is never persisted.
- Section and entity fetches are aborted when their view is abandoned.
- The entity page renders a title from the identity-scoped cache or from identity-matched
  navigation state while the entity loads, and starts its related-media request in parallel when
  that request does not depend on the entity result.

## Non-goals

- Persisting the Navigation Manifest, session, roster or catalogue data across reloads. The session
  cookie is shared by every tab and is not readable by the page, so a persisted copy cannot be
  matched to the current identity before a server round trip, and a revealed manifest must never
  be at rest.
- Reordering the session bootstrap chain or changing the atomic session, manifest and roster swap.
- Server, player, service worker or card changes.

## Capabilities

### New Capabilities

- `web-viewer-loading`: what the web viewer shows while it loads and how it reuses loaded data.

### Modified Capabilities

None.

## Impact

- Files: `internal/web/app/src/lib/session.tsx`, a new `internal/web/app/src/lib/viewerCache.ts`,
  the Viewer area root, `HydratedSection`, `EntityPage`, and optional `signal` parameters on the
  section and entity fetch helpers.
- Failure modes: cached data for one identity is shown to another (prevented by identity-keyed
  entries plus clearing at every epoch transition); protected chrome is shown to a signed-out
  browser (the skeleton chrome renders no session-scoped data); a background revalidation failure
  hides content (cached items stay and the existing connection-lost notice is raised).
- Acceptance evidence: web unit tests for the loading skeleton, cache hit without skeleton plus
  revalidation, abort on unmount, identity isolation and clearing on epoch transitions; web lint,
  typecheck and the full unit suite pass.
- Rollback: revert the change; no stored state or wire contract depends on it.
