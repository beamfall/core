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
- A play is the reports sharing one client-generated playback ID, ordered by server receipt. A
  client makes a new random ID each time its player starts an item and keeps it across pauses,
  seeks and reloads, and sends one ID's reports in order, one at a time (only a heartbeat may
  replace the heartbeat queued just before it). Concurrent devices and hand-offs are separate
  plays because their IDs differ. A play's method is the method that covered most of its watched
  time (each step counts for the report that ends it), read from the session's held decision on
  receipt. Reports without a playback ID (older clients, events written before this change) are
  grouped and credited by the CORE-1307 rule exactly.
- Within a play, watched time is the position advance between consecutive reports, capped at the
  time between them (the client clock difference when it ran forward, else the receipt
  difference), and zero after a report that said paused. The web player now sends an optional
  `paused` flag and `playbackID`, reports on resume, and queues reports in strict order; clients
  that do not say paused read as playing. Apple and Android send the playback ID in follow-ups.
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
