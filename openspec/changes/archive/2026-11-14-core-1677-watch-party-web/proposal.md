# Watch Party in the web viewer

Ticket: CORE-1677 (`queue:core:main`). Legal gates: CORE-0531. Design: ADR-0038, ADR-0111.

## Why

The server has had Watch Party mutations for a while (create, invite, accept, transport, request,
auto-skip, leave), but no client uses them on the web. Building the web UI turned up four server
gaps that stop a party from working across two Profiles:

- **Cross-Profile delivery.** A host transition is written as one `group.sync` event gated to the
  host's Profile. The SSE gate delivers group events only to the same Profile, so an invited
  member in another Profile never sees play, pause or seek.
- **Pause.** The party timeline always advances from its anchor. There is no paused state, so
  "pause" can't be told apart from "seek and keep playing", and a late joiner to a paused party
  lands at the wrong position.
- **Roster.** Only `createWatchParty` and `inviteToWatchParty` return members. A member who joins
  from a notification can't see who is in the party, and nobody learns when someone joins or
  leaves.
- **CORE-0531 network gate.** Party mutations accept callers from any network. CORE-0531 limits
  v1 to household, private-performance use until a fresh legal review promotes remote use.

## What Changes

- **Server, conductor:** a party has a paused state. It starts paused at position 0 on its
  content. A host transition sets item, position and paused. The anchor doesn't advance while
  paused.
- **Server, sync channel:** party `group.sync` events go out as one row per distinct member
  Profile (host included), so every member's stream receives them. The SSE Profile gate is
  unchanged. `GroupSyncPayload` gains an additive `paused` flag and `members` list, plus a new
  closed-set signal `roster` (members' display names plus the current anchor). It is emitted on
  create, accept and leave. Clients ignore signals they don't know, so other clients are
  unaffected.
- **Server, GraphQL:** `watchPartyTransport` and `requestWatchPartyTransport` accept an optional
  `paused: Boolean` argument. Result shapes are unchanged, so there is no wire-contract version
  bump. Field arguments aren't contract-covered.
- **Server, network gate (CORE-0531):** `createWatchParty` and `acceptWatchParty` refuse requests
  that don't come from the home network (the same LAN test `/api/group/calibrate` uses), with the
  message "Watch Party is available on your home network only". Invites still go only to a named
  Profile on the same server. There are no public or anonymous join links.
- **Web viewer:**
  - "Start Watch Party" on the player.
  - An invite-by-Profile dialog.
  - A party panel listing members, with Leave.
  - "Join" on a Watch Party invite in the notifications inbox.
  - Play, pause and seek kept in sync from the `group.sync` stream, with local drift correction
    (re-seek above 500 ms, a small rate nudge below) and a visible drift indicator.
  - The host's controls are authoritative. A member's controls send a transport request, which is
    applied at once in a relaxed party or sent to the host as a notification.

## Not in this change

- **Host leaving.** The party keeps going without a host. Handing the host role over, or ending
  the party, is a follow-up ticket.
- **Server-driven drift correction for parties.** `reportDrift` is same-Profile only. The web
  client corrects drift locally against the anchor instead.
- **Remote Watch Party.** It stays refused until CORE-0531's legal review promotes it.
- **tvOS (CORE-0313) and Apple mobile (SharePlay).** Untouched.

## Failure modes

- If an outbox append fails, the transport mutation fails and the client keeps its own state. The
  next transition or roster event re-anchors every member.
- A member that misses events (reconnect) gets a fresh anchor from the next roster or transition
  event, and can rejoin through the notification.
- A remote caller gets a clear refusal, never a half-joined party.

## Acceptance evidence

- **Go tests:**
  - Paused anchor.
  - Per-member-Profile fan-out.
  - Roster on accept and leave.
  - Remote refusal for create and accept.
- **Web vitest:**
  - Sync math (offset, drift and correction choice).
  - Party panel and invite dialog.
  - Join from a notification.
- `openspec validate --all --strict` passes, and so does the core gate on the landing head.

## Rollback

Revert the change. Nothing is persisted: party state is in memory and outbox rows expire as
usual. No migration, no contract version bump.

## Impact

`internal/conductor`, `internal/graph` (Watch Party handlers), `internal/store` (group sync
payload), `internal/auth` (request-origin context marker), `internal/server` (sets the marker on
the GraphQL route), and `internal/web/app` (viewer player, notifications, strings).
