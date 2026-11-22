# Playback reporting: device, method, filters and per-viewer stats

Tickets: CORE-1703 (`queue:core:main`), folding in CORE-1307 (watched time counts only playing time)

## Why

The Console lists plays and ranks profiles and items, but it cannot say which device played or
whether the server played direct, remuxed or transcoded, cannot filter by item, device or method,
and only offers fixed 7/30/90-day windows. Viewers cannot see their own history at all. This is the
local-first equivalent of a playback-reporting plugin: per-profile history and stats kept on the
server and never sent anywhere.

## What changes

- Each playback progress event records the reporting device and the play method the server chose
  for that session (direct, remux or transcode) when one is held. The event payload is JSON, so no
  migration is needed. Events written before this change have neither and read as unknown.
- A play is a run for one profile, item and stream, where the stream key is a one-way hash of the
  authenticated session (reports carry no stream id of their own; the session is what the held
  delivery decision and stream tokens are keyed by). Concurrent devices are separate plays and
  moving between streams starts a new play, every time. Reports are ordered by server receipt. A
  play's method is the method that covered most of its watched time (each step counts for the
  report that ends it), read from the session's held decision on receipt, never from client
  timestamps. Events written before receipt times keep the CORE-1307 rule exactly.
- Watched time is the position advance between consecutive reports, capped at the server receipt
  gap and zero after a report that said paused. The web player now sends an optional `paused` flag
  and reports on resume; clients that do not send it read as playing.
- `playHistory` gains `mediaID`, `deviceID` and `playMethod` filters, and each play carries the
  device ID, device name, device client surface and play method.
- `playStats` accepts a `since`/`until` range (up to 366 days) as well as a 7/30/90-day window,
  takes the same filters as `playHistory`, and returns the total plays and watched time and
  breakdowns by profile, item, device, method and day (days in the caller's UTC offset).
- New viewer fields `myPlayHistory` and `myPlayStats` read the same data for the signed-in profile
  only, with no profile filter, and drop plays of items the profile cannot see.
- Console Dashboard: a date range, item/device/method filters, totals, and device, method and day
  breakdowns. My Data: a "Watch history" section with totals, top items and devices, and recent
  plays.

## Non-goals

- No new table and no migration; no export of reports; no telemetry. Plays are still derived from
  progress events on read.
- No per-play bitrate or transcode reason in history; live sessions keep showing those.
