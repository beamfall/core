# Karaoke Library opens on a songbook and the party

Tickets: CORE-3362, with follow-ups CORE-3367, CORE-3368 and CORE-3369 (`queue:core:main`).

## Why

A karaoke host opening the karaoke Library on the web gets the generic Recommended, Library,
Collections and Categories pivots. Finding a song by artist means paging a poster grid sorted by
title, and the party host page that runs singers and the rotation (CORE-2945) has no link from
anywhere: it lives at `/party` only. The tvOS songbook already sorts by artist (CORE-3283); the
server has no artist order or artist search to give the web the same.

## What Changes

- `libraryItems` accepts `sort: "field:<name>"` for a text Media field the Library's plugin
  declares Sortable. Items with no value for the field follow the rest in either direction, and
  the letter index is computed from the field value. Any other field name is a client error. The
  published `sort` argument and the agent catalog name the `field:<name>` form (CORE-3367).
- `libraryItems` takes `fieldFilters` (a Filterable text Media field to one exact value, ANDed)
  and `facetFields` (Filterable text fields whose values and item counts come back under
  `facets.fields`), so a client offers only the formats and singer counts the Library has
  (CORE-3369). These change the wire contract: the landing stack publishes the next
  `client-core-wire` minor.
- The `libraryItems` text filter also matches the Library plugin's Searchable text Media fields,
  so "abba waterloo" finds a song whose artist is ABBA and whose title is Waterloo.
- `libraryEntities` refuses a field sort: Entities carry no Media fields.
- On the web, a Library served by the karaoke plugin opens on a Songbook pivot in place of
  Recommended and Library: one search over artist and title, an artist or title order grouped by
  artist under letter headers, the A-Z rail, and Play and Add for a singer on every song. A rail
  beside it runs tonight's party: start one, the join code and QR, the song on stage, requests,
  the rotation and adding a singer, with a link to the full host page. Collections and Categories
  stay as secondary pivots. The A-Z rail marks the letter of the first song on screen
  (CORE-3368), and chips filter by format (CDG, LRC, video, UltraStar), duet and guide vocal when
  the Library has them (CORE-3369).
