# Manual video quality picker with a remembered per-device default

Ticket: CORE-1971 (`queue:core:main`).

## Why

The web player's quality menu pins a rendition on the client only. The pick is forgotten when the
player closes, a browser that plays HLS natively cannot pin at all, and the server keeps
negotiating as if the viewer had chosen nothing. A viewer on a poor link cannot set a maximum
quality once and have every later playback on that device honour it, as Plex players do.

## What Changes

- No server or wire change. Playback info already takes the viewer's ceiling as
  `capabilityProfile.maxStreamingBitrate` (CORE-1674), and each advertised rendition's `bandwidth`
  is the exact ceiling that keeps that rung on top, so a ladder step needs no separate resolution
  field. New negotiation tests pin that a ladder step tops out at that step, that the admin's remote
  cap still wins when tighter (CORE-1717), and that Auto (no ceiling) negotiates as today.
- The web player remembers the viewer's quality pick per browser (local storage, guarded so a
  blocked store means Auto) as a rendition id and its bandwidth, and sends the bandwidth as
  `maxStreamingBitrate` on every playback-info request from that browser.
- Picking a step re-requests playback info at the current position under the new ceiling. Auto, or
  Original on direct play, forgets the pick and re-requests without a ceiling. The menu marks the
  remembered step and offers Auto beside Original on direct play while a pick is remembered.
- The Apple clients' menu is out of scope here and is a follow-up in their repository.

## Impact

- `internal/web/app` (viewer playback request, quality menu model, player transport) and
  `internal/playback` tests only.
- No wire contract, GraphQL or golden change.
