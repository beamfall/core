# Plugin-declared placeholder icon (CORE-3336)

## Why

A Media item without artwork draws a glyph chosen from its media class, so a karaoke song shows the
same music note as a music track. Core cannot tell them apart without naming a domain, and nothing
on the wire lets a plugin choose the glyph.

## What changes

- Plugin SDK v0.4.14 adds `LibraryDefaults.Icon`, a name from the existing closed Library icon
  vocabulary (CORE-1200). The first-party karaoke plugin v0.1.15 declares `mic`. Core pins both.
- A plugin's declared icon becomes its Libraries' default icon in place of the class-derived one. A
  user-chosen icon still wins.
- `SourceItem` (cards and search cards) and `MediaInfo` (`media(id)`) gain optional
  `placeholderIcon`: the declared icon of the item's Library plugin. It is absent for entities,
  plugin-less Libraries and plugins that declare none. Core passes the name through and names no
  domain.
- The web client draws `placeholderIcon` as the artless poster glyph when it names a known icon,
  and keeps the media-class glyph otherwise.
- Additive client-core-wire minor.

## Impact

`go.mod` pins, `internal/store` (default Library icon, SourceItem field), `internal/graph` and
`internal/manifest` (Library icon basis, card and `media(id)` annotation), the wire contract
artifacts, and the web poster glyph. Native clients draw no class glyph for artless cards today
(Apple shows the title, Android the class label), so they need no change.
