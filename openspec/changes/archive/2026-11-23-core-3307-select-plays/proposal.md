# Tell clients which Media play on Select (CORE-3307)

## Why

People pick a karaoke song by title to sing it, so Select should start it rather than open a
detail page. Nothing on the wire says so, and core names no domain, so the tvOS client guessed
songs from artless audio. Video songs and songs with thumbnails therefore opened detail and fell
out of the songbook and song finder.

## What changes

- Plugin SDK v0.4.13 adds `Engagement.SelectPlays`; the first-party karaoke plugin v0.1.14
  declares it. Core pins both.
- `SourceItem` (and so search cards) and `MediaInfo` (`media(id)`) gain optional `selectPlays`,
  true when the owning Library's plugin declares `Engagement.SelectPlays`, absent otherwise, for
  entities and for plugin-less Libraries. Core passes the flag through and names no domain.
- Additive client-core-wire minor 2.52.0.

## Impact

`go.mod` pins, `internal/store` (SourceItem field), `internal/graph` (the annotation that already
carries `playsAlone`), and the wire contract artifacts. Clients adopt the field in their own
changes; the tvOS client does so under the same ticket.
