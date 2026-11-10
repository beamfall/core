# Keep the full stream inventory in the web player

Ticket: CORE-0048 (`queue:core:main`). ADR-0010 section 8, ADR-0084, ADR-0138.

## Why

The web player keeps only `streams[0]` of the playback-info response. Core mints `streams[1]`, an HLS
manual-rendition pin, next to a direct-play stream so weak links can step down. Discarding it means
the quality menu shows one "Direct play" row, a decode failure on direct play fails closed instead of
switching to HLS, and a relock on the native route (surfaced as `MEDIA_ERR_NETWORK`) never retires the
identity epoch. The quality menu id and the `hls.levels` lookup also disagree on the rendition key, so
an id-less rendition pins level 0.

## What Changes

- `PlayerState` carries the whole inventory (`streams`) beside the active `stream`.
- On direct play the quality menu is built from the HLS sibling's renditions. Selecting a rendition
  switches the engine to HLS at the current position with that rendition pinned.
- A `MediaError` 3 or 4 during direct play switches once to the HLS sibling at the current position;
  without a usable sibling it keeps the unsupported-codec message.
- A native-route `MEDIA_ERR_NETWORK` probes the stream URL; a 401, 403 or 410 enters reveal-terminal
  and retires the identity epoch, anything else shows the generic failure message.
- One canonical rendition key serves the menu id, the selection and the `hls.levels` lookup. A key
  that matches no level changes nothing.

## Non-goals

- Server changes, new wire fields or new strings.
- Changing the HLS route's own recovery or token-refresh behaviour.

## Failure modes

- No HLS sibling, or no HLS engine in this browser: the direct-play failure messages are unchanged.
- The failover happens at most once per load; an HLS failure afterwards follows the HLS recovery path.
- A failed probe (offline, aborted) is not a relock and shows the generic failure message.

## Acceptance evidence

- `usePlayer.test.tsx`: info with `[direct, hls]` exposes both; `MediaError` 3 on native attaches hls.js
  at the prior position; native `MEDIA_ERR_NETWORK` with a 403 probe enters reveal-terminal.
- `transport.test.ts`: the rendition menu comes from the sibling; an id-less rendition never pins level 0.
- `docs/release/evidence/WEB-STREAM-INVENTORY-1.md`.

## Rollback

Revert the change. It holds no stored state and changes no wire contract.

## Capabilities

### New Capabilities

- `web-stream-inventory`: how the web player uses the server's stream inventory.

### Modified Capabilities

None.
