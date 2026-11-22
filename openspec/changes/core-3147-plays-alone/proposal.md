# Tell clients which Media play alone (CORE-3147)

## Why

ADR-0055 §2.4 says some Media never auto-advance into the next one. The web client still ran one
item straight into the next from library shelves, See all, a standalone item's detail Play and
person pages without credit roles, because it builds queues from whatever list a card sits in and
nothing on the wire says an item should play alone. Resume eligibility is not that signal: podcast
episodes resume but keep queue auto-advance (ADR-0030 §5). ADR-0055 Amendment B.

## What changes

- Plugin SDK v0.4.11 adds `Engagement.PlaysAlone`; the first-party audiobook plugin v0.1.20 declares
  it. Core pins both.
- `SourceItem` (and so search cards) and `MediaInfo` (`media(id)`) gain optional `playsAlone`, true
  when the owning Library's plugin declares `Engagement.PlaysAlone`. It is absent otherwise, for
  entities and for plugin-less Libraries. Core passes the flag through and names no domain.
- The web client honours it: a plays-alone item starts with a one-item queue, the player neither
  auto-advances out of it nor into it from another queued item, and a listing whose items play
  alone is not a track list. The Play Queue a user builds and the manual next-in-sequence offer are
  unchanged.
- Additive client-core-wire minor (published by the landing stack).

## Impact

`go.mod` pins, `internal/store` (SourceItem field), `internal/graph` (card, search card and
`media(id)` annotation), the wire contract artifacts, and the web client queue builders and player.
Native clients may adopt the field in their own changes.
