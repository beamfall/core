# Recognise every container artwork name

Ticket: CORE-2313 (`queue:core:main`), sidecar part only.

## Why

The local artwork vocabulary recognised only the container poster names (`season01-poster.jpg`,
`season-specials-poster.jpg`). The other container artwork that media managers write beside a
container folder (`season01-banner.jpg`, `season02-fanart.jpg`, `season01-landscape.jpg`,
`season-all-poster.jpg`) was catalogued as image items, so it showed up in "Recently Added" in a
video Library.

## What Changes

- The vocabulary recognises the numbered, `season-specials` and `season-all` container names for
  `banner`, `fanart`, `landscape` and `thumb`, and `season-all-poster`. They are recognised so the
  walk never catalogues them; only the numbered and specials posters fill a slot, as before.
- A rescan marks image items an earlier scan catalogued from these names missing through the
  existing missing-file path.
