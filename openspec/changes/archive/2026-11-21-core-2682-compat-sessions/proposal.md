# Read-only now-playing sessions for compatibility dashboards (CORE-2682)

## Why

ADR-0171 §2.3 lists dashboard widgets as a compatibility consumer, served by a live, read-only
`GET /Sessions` now-playing snapshot. Homepage's Jellyfin widget and similar dashboards poll it to
show what is playing. Today the route falls through, so these dashboards show nothing. They also
fetch `GET /Items/Counts` and do not render until it answers.

## What changes

- `GET /Sessions` lists the playbacks active within the live-session activity window, built from
  the existing Stream Token ledger (`store.ActiveSessions`), for opted-in adult Profiles only.
- A session is listed only when its Media is a visible item of an eligible, mapped Library, under
  the same filter as the catalog routes. Reveal-restricted, sealed, encrypted and unmapped items
  never appear. Each entry carries an opaque session ID, the Profile's compatibility user ID and
  name, the playing item (title, type, series and index numbers, runtime) and the play position.
  Native session and Device IDs, device names and play-method fields are not exposed.
- `GET /Items/Counts` reports, for each declared item type, how many visible items of that type
  the eligible Libraries hold, filtered before counting. The field name is the item type followed
  by `Count`; core names no item type.
- No session-control route is added. Remote control, `/socket`, QuickConnect and every playback
  route stay unserved, and a route-inventory test proves it.
- ADR-0171 §2.3's dashboard row names both routes.
