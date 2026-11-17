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
  code" regenerates, after any poll on the wire has answered; "Cancel" stops and withdraws the
  request (`POST /api/pairing-requests/cancel`, CORE-0316).
- On approval the browser is bound exactly as typed enrollment binds it: the server sets the same
  three HttpOnly, SameSite=Strict credential cookies `/api/web/enroll` sets, the page runs the
  compatibility rebind and reloads to the app root.
- To make that possible, a pairing request made with the browser CSRF header (`X-Beamfall-Csrf`) is
  a browser request. The server records that at creation and carries it through regenerate, so a
  later poll cannot shed it by leaving the header off; a call that carries the header is a browser
  call too. Request, regenerate and poll apply the `/api/web/enroll` transport rule (HTTPS or LAN)
  before the request is touched, and an `approved` poll sets the credential cookies and blanks
  `deviceSecret` and `sessionID` in the body, so the device secret never reaches page script. The
  response keeps its fields; requests made without the header are unchanged.
- Denied, expired (401, or the server's `expiresInSeconds` run out), throttled (429) and refused
  requests each get their own plain message. The page times the code by `expiresInSeconds`, never by
  comparing `expiresAt` with the browser's clock.
- A request the browser stops wanting is withdrawn: on Cancel, when a typed code is submitted, and
  when the page unmounts, reloads or closes. A server without the routes (404) hides the button and leaves the typed form.
- The typed-code form stays usable. Starting a typed enrollment cancels a pending ask; one attempt
  identity means a stale response is ignored. The request secret stays in memory only; leaving the
  page aborts polling and withdraws the request.
- Strings in every locale the web app ships (en, fr, de, es, pt).

## Non-goals

- No route or wire contract version of its own: cancel and `expiresInSeconds` belong to CORE-0316.
- No change to native clients, which request and poll without the CSRF header and keep the body
  credential.

## Failure modes

- A browser request, regenerate or poll over plain HTTP from off the LAN is refused with `403`
  before the request is touched, as `/api/web/enroll` refuses it; the request is not spent.
- The withdrawal is fire-and-forget. If it never reaches the server, the request expires in 10
  minutes as before.
- "New code" pressed while a poll is collecting an approval keeps the approval: the poll is left to
  answer and the code is replaced only if the request is still pending.
- If the page is closed or cancelled after the server answered `approved`, the cookies may already
  be set; the next load is simply signed in.
- A failed rebind after approval is a notice, never an enrollment error, as for typed enrollment.

## Acceptance evidence

- `internal/server` tests: a CSRF-header poll sets the three cookies with the web attributes, the
  body carries no device secret, the cookies work on `/api/manifest`; a headerless poll of a
  browser request still gets cookies and no body credential; public plain-HTTP browser request,
  regenerate and poll are `403` and leave the request pending; a native poll still gets the body
  credential.
- Vitest: approve, deny, expiry by the server's lifetime, regenerate that waits for the poll and
  keeps an approval, 404 fallback, unmount and page hide withdraw the request, Cancel and typed
  submit withdraw it.
- Conformance `TestVocab|TestWire` unchanged.

## Rollback

Revert the commits. Browsers enrolled through the flow are ordinary devices and stay enrolled.
