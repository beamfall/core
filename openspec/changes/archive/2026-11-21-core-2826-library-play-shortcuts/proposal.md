# Library play shortcuts

Ticket: CORE-2826 (`queue:core:main`).

## Why

A UX time-to-play audit found that playing a known title on web takes five clicks: Home, the
Library, its Library pivot, the poster, then Play. Poster cards offer no Play of their own (only the
More menu), every Library reopens on Recommended (which repeats Home's shelves) even for a viewer
who always browses the full grid, and a container Entity page's primary button says only "Play"
while the child it plays is named in small text below.

## What changes

- **Poster Play.** A playable Media card drawn as a poster or thumbnail (not a row, which already
  plays, and not a locked item) gains a round Play button beside its More trigger. It appears on
  hover and keyboard focus, stays visible without hover (touch), is named "Play <title>" or
  "Resume <title>" from the card's progress, and opens the player through the card's existing play
  path with the same queue. The card link and More menu are unchanged.
- **Remembered pivot.** The web remembers, per Library navigation node and per browser, the last
  pivot shown (Recommended, Library, Collections or Categories). The sidebar's link to that Library
  opens the remembered pivot. An address typed, shared or followed from a pivot still opens exactly
  that pivot.
- **Named primary action.** On a container Entity page whose next-up child has a plugin subtitle,
  the accent button reads "Play <ordinal>" or "Resume <ordinal>", built from the subtitle's parts
  other than the child's title (for example "Play S1 E3"). Its accessible name also carries the
  child's title. Audio listings and children without a subtitle keep the plain label.

## Non-goals

- A Play button on container cards: a card carries no next-up target today, and reading one per
  card would add a request per card.
- Native clients and server or wire changes: the subtitle and progress already reach the web.
- Syncing the remembered pivot across devices; it is a per-browser convenience.

## Failure modes

- Blocked or cleared browser storage falls back to Recommended.
- A stored value that is not a known pivot is ignored.
