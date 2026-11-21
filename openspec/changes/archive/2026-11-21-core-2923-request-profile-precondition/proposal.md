# CORE-2923: a stale tab cannot act as another Profile

## Why

Every browser tab shares one session cookie, but each tab keeps its own idea of which Profile is
signed in. After one tab switches the session to Profile B, a second tab still showing Profile A
sends its writes (client preferences, sidebar choices, ratings, watch state, play queue edits)
with the shared cookie, so the server applies them to B. The tab reads B's data the same way and
shows it under A's name. CORE-2177 closed this for play queue writes with a per-mutation
`expectedProfileID`; every other request is still open. Found in review of CORE-2914.

## What changes

- A request MAY carry the header `X-Beamfall-Profile` naming the Profile it was made for. When
  it is present and is not the Profile the request's credential is bound to, the server refuses
  the request before any handler runs, with HTTP 409:
  - on `/api/graphql`: error code `CONFLICT` with `extensions.reason` `PROFILE_MISMATCH`, the
    vocabulary CORE-2177 introduced;
  - on other routes: `{"error": "profile mismatch", "code": "CONFLICT", "reason": "PROFILE_MISMATCH"}`.
- The check runs in the per-route wrapper that already resolves the credential for the
  compatibility decision, so it covers every route that wrapper covers and adds no credential
  read. Pre-subject and `/v1/` routes keep their current behaviour.
- The web app sends the header on every API request once it knows the signed-in Profile, and
  sends none while an identity is loading. A refusal for the Profile the tab currently shows
  starts a new identity epoch, so the tab reloads and shows the Profile the session is now
  bound to. A refusal that arrives after the tab already moved on is ignored.
- Clients that send no header behave exactly as before.

## Non-goals

- Pushing a Profile switch to other tabs as it happens (BroadcastChannel). A stale tab finds
  out on its next request, which is enough to stop it acting as the wrong Profile.
- Requests the browser makes without script (image and media element loads). They are reads
  that carry no header, and the tab reloads on its next scripted request.
- Native clients. They hold one Profile per session and gain nothing from the header.
