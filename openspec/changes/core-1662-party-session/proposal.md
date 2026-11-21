## Why

Tickets: CORE-1662 (party session server and web), CORE-2945 (host home), CORE-2946 (pop-out
stage), CORE-2947 (joined screens with drift reporting), CORE-2948 (guest phone page), all in
`queue:core:main`. ADR-0237 (party session, relay stage sync) is the binding design.

A household running a party wants guests to put their own requests in from their phones, a fair
rotation the host can see and steer, and a stage screen that plays the current entry's composed
picture. Today a Profile has only its personal Play Queue (CORE-1630) and a Watch Party that syncs
players but has no requests, rotation or guests.

## What Changes

- A **party session** owned by a host Profile: one live session per host, a rotatable four
  character join code, a QR join link, and settings for fairness (round-robin by participant or
  first-in first-out), auto-add or host approval, and whether joining is open.
- **Entries** reuse the Play Queue entry model (durable entry IDs, the same Media more than once)
  in a session-scoped list, so a party never rewrites the host's personal Play Queue. The server
  orders waiting entries by the fairness policy around entries the host pinned with a move, and
  computes each entry's ETA from the remaining time of the current entry, the durations ahead and a
  changeover allowance.
- **Participants** are named by the host or join as guests. A guest joins with the code and a name
  and receives a scoped, expiring guest token bound to the session. The token reads the rotation,
  searches the Libraries the host opened for the session and submits or cancels its own requests.
  It cannot play, stream or download, and it expires when the session ends, after 12 hours or when
  the host removes the guest.
- **Limits**: three waiting requests per guest, a per-guest request rate limit, join attempts
  rate-limited per source address, a cap of 50 guests, and a wrong or closed code is
  indistinguishable from an unknown one.
- **Stage screens**: a pop-out window from the host's browser or a joined household client. Each
  screen registers, follows the server's playback anchor, reports its clock offset, and has a
  ticker flag stored on the server (off by default on the lead screen). Guests never get a stage.
- **Graph** host and screen operations (session-scoped, the caller's Profile is the host) and
  **HTTP** guest routes under `/api/party/` authenticated by the guest token, not a session.
- **Web**: a host page with live monitor, transport, rotation, request approval, find-and-add and
  screens; a stage pop-out window; and an unauthenticated phone-width guest page at `/join/:code`.

## Deviations from ADR-0237

- The ADR calls the party session "a mode of the server play queue". Entries live in their own
  session-scoped table using the same entry identity model, because writing party entries into the
  host's personal Play Queue would destroy it on every party. The Play Queue contract is unchanged.
- Relay screens report their carrier and measured offset; the server does not yet measure the
  carrier itself. The web never claims "in sync" for a relay screen until an offset inside the
  ±40 ms budget has been reported.
