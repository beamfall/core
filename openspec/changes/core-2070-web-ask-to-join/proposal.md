# Ask to join from the web enroll page

Ticket: CORE-2070 (`queue:core:main`)

## Why

A browser joins today only with a pairing code someone minted and read out. Device-initiated
pairing (CORE-0316) lets a device ask and an administrator approve with one click, and the Console
already approves (CORE-0317, CORE-2064). The web enroll page should ask too, so a browser connects
with nothing typed.

## What changes

- The enroll page gets an "Ask to join" button. It posts `/api/pairing-requests` with
  `platform: "web"` and `requestedRole: "viewer"`, displays the 8-character code with a line telling
  the user to approve it in the Console on the server, and polls every `pollIntervalSeconds`. "New
  code" regenerates; "Cancel" stops.
- On approval the browser is bound exactly as typed enrollment binds it: the server sets the same
  three HttpOnly, SameSite=Strict credential cookies `/api/web/enroll` sets, the page runs the
  compatibility rebind and reloads to the app root.
- To make that possible, `POST /api/pairing-requests/poll` treats a request carrying the browser
  CSRF header (`X-Beamfall-Csrf`) as a browser poll: it applies the `/api/web/enroll` transport rule
  (HTTPS or LAN) before polling, and on `approved` sets the credential cookies and blanks
  `deviceSecret` and `sessionID` in the body, so the device secret never reaches page script. The
  response keeps its fields; requests without the header are unchanged. No route is added and no
  request or response field changes, so the wire contract does not change.
- Denied, expired (401 or past `expiresAt`), throttled (429) and refused requests each get their own
  plain message. A server without the routes (404) hides the button and leaves the typed form.
- The typed-code form stays usable. Starting a typed enrollment cancels a pending ask; one attempt
  identity means a stale response is ignored. The request secret stays in memory only; leaving the
  page aborts polling.
- Strings in every locale the web app ships (en, fr, de, es, pt).

## Non-goals

- No new route, no request or response field, no wire contract version.
- No server-side cancel of a pending request: Cancel abandons it and it expires in 10 minutes.
- No change to native clients, which poll without the CSRF header and keep the body credential.

## Failure modes

- A browser poll over plain HTTP from off the LAN is refused with `403` before the request is
  touched, as `/api/web/enroll` refuses it; the request is not spent.
- If the page is closed or cancelled after the server answered `approved`, the cookies may already
  be set; the next load is simply signed in.
- A failed rebind after approval is a notice, never an enrollment error, as for typed enrollment.

## Acceptance evidence

- `internal/server` tests: a CSRF-header poll sets the three cookies with the web attributes, the
  body carries no device secret, the cookies work on `/api/manifest`; a public plain-HTTP browser
  poll is `403` and leaves the request pending; a native poll still gets the body credential.
- Vitest: approve, deny, expiry, regenerate, 404 fallback, unmount stops polling, typed submit
  cancels the ask.
- Conformance `TestVocab|TestWire` unchanged.

## Rollback

Revert the commits. Browsers enrolled through the flow are ordinary devices and stay enrolled.
