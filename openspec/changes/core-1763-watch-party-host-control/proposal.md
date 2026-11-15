# Watch Party host hand-off, requests and drift

Tickets: CORE-1763, CORE-1765, CORE-1766 (`queue:core:main`). Follows CORE-1677. Legal gates:
CORE-0531. Design: ADR-0038, ADR-0111.

## Why

CORE-1677 shipped Watch Party in the web viewer. Three gaps remained:

- **CORE-1763, host leave and dropped members.** When the host left, the party kept a host
  session nobody held. Every member's transport became an unanswerable request. A closed tab
  never left, so a member stayed in the roster until the process restarted.
- **CORE-1765, requests.** A member's seek in a host-only party went out as a
  `watch-party-invite` notification, so its Join button opened the party instead of deciding the
  request. The host could not approve or decline it, and the web always started relaxed parties.
- **CORE-1766, drift and Up Next.** The web player measured its drift but never reported it, and
  `reportDrift` accepted only same-Profile Ambient Group members. On a member's player, Up Next
  and autoplay advanced locally and pulled that member away from the party.

## What Changes

- **Conductor:** the party records join order. When the host leaves, the host role passes to the
  earliest member still present (`LeaveParty` reports the new host or that the party dissolved).
  `EndParty` lets the host end it for everyone. Per-session event-stream presence lets the server
  expire a party member whose last stream closed and did not reopen within a 20-second grace.
  Pending transport requests live with the party, keyed by an opaque id, and are dropped when the
  host role moves. `PartyReportDrift` classifies a party member's drift without re-anchoring the
  party.
- **Sync channel:** `GroupSyncPayload` gains additive `host`, `hostName`, `handoff`, `request`
  and `from` fields and two closed-set signals, `ended` and `request`. A hand-off is a `roster`
  with `handoff` and the new host's display name. Only the new host's own Profile row names its
  session (ADR-0111: members see each other by display name only). A `request` row goes only to
  the host's Profile. Clients ignore signals they don't know.
- **Notifications:** a new kind, `watch-party-request`. Its `ref` is `<partyID>:<requestID>` and
  its body is the requester's display name.
- **GraphQL (argument additions only):** `leaveWatchParty(end: Boolean)` ends the party for
  everyone when the host passes it. `watchPartyTransport(request: String, decline: Boolean)`
  approves (applies the stored transport and moves every member) or declines a pending request.
  `item` is not required when `request` is given. `reportDrift` accepts a Watch Party member of
  any Profile. Result shapes are unchanged, so there is no wire-contract change.
- **Server, event stream:** the SSE handler tells an observer when a session-backed stream opens
  and closes. The graph handler uses this for the expiry. API keys and tokens are not tracked.
- **Web:** a `pagehide` handler leaves with a keepalive POST. The player shows "X is now hosting",
  "You're now hosting" or "X ended the Watch Party". The host gets "End for everyone". Start offers
  relaxed or host-only control. The host sees pending requests with Approve and Decline in the
  panel, and the notifications page shows the same buttons for the new kind. While playing, the
  player reports its drift at most once every 5 seconds. On a member's player Up Next and the
  still-watching autoplay defer to the party, and the host's advance moves everyone through the
  existing follow path.
- **CORE-0531:** unchanged. Create and accept still refuse non-home-network callers. Hand-off
  stays within the existing members, so no new Profile joins and the household caps are unchanged.

## Impact

- Code: `internal/conductor`, `internal/graph` (Watch Party, `reportDrift`), `internal/store`
  (group sync payload, notification kind), `internal/server` (event-stream observer),
  `internal/app` (wiring), and the web viewer player, notifications page and strings.
- No migration. Parties stay ephemeral and in memory.
- Wire contract: no change. Only arguments, payload fields, signals and a notification-kind value
  were added.
