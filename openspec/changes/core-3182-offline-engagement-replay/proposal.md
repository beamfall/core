# Replay offline engagement when a device comes back online

Ticket: CORE-3182 (`queue:core:main`). Design: ADR-0157 Amendment B, section B7. The Apple and
Android download sessions build the client side against this mutation.

## Why

Clients play downloaded Media with no server, so the position, completion and plays recorded
offline never reach the server. Continue Watching, watched state and play counts drift from what
the viewer actually did, unlike Plex and Jellyfin, which sync offline progress on reconnect.

## What Changes

- A new GraphQL mutation `replayOfflineEngagement(events, expectedProfileID)` takes up to 500
  offline engagement events (event ID, Media ID, position, duration, completed, played-at time).
- Each applied `(Profile, Device, eventID)` is recorded in a new events-database ledger in the same
  transaction as its effect, so replaying a batch twice changes nothing the second time.
- Position follows latest-timestamp-wins through the existing playback event-time guard; a
  completed event marks the Media watched whatever its timestamp, and no offline event un-completes
  a watched Media. Plays count once, on the unwatched-to-watched transition.
- Events are applied as ordinary playback projection events, so a projection rebuild reproduces
  them. One `engagement-changed` invalidation is raised per batch that applied anything.
- Profile erasure and per-Media My Data deletion clear the ledger rows with the rest of the
  Profile's engagement.

## Impact

- Events migration 0027 (`offline_engagement_event`), store, graph, wire contract and mgmt
  manifests. No /v1 change.
