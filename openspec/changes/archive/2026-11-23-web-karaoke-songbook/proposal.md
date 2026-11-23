# Karaoke Library opens on a songbook and the party

Ticket: CORE-3362 (`queue:core:main`).

## Why

A karaoke host opening the karaoke Library on the web gets the generic Recommended, Library,
Collections and Categories pivots. Finding a song by artist means paging a poster grid sorted by
title, and the party host page that runs singers and the rotation (CORE-2945) has no link from
anywhere: it lives at `/party` only. The tvOS songbook already sorts by artist (CORE-3283); the
server has no artist order or artist search to give the web the same.

## What Changes

- `libraryItems` accepts `sort: "field:<name>"` for a text Media field the Library's plugin
  declares Sortable. Items with no value for the field follow the rest in either direction, and
  the letter index is computed from the field value. Any other field name is a client error. No
  new argument: the wire contract is unchanged.
- The `libraryItems` text filter also matches the Library plugin's Searchable text Media fields,
  so "abba waterloo" finds a song whose artist is ABBA and whose title is Waterloo.
- `libraryEntities` refuses a field sort: Entities carry no Media fields.
- On the web, a Library served by the karaoke plugin opens on a Songbook pivot in place of
  Recommended and Library: one search over artist and title, an artist or title order grouped by
  artist under letter headers, the A-Z rail, and Play and Add for a singer on every song. A rail
  beside it runs tonight's party: start one, the join code and QR, the song on stage, requests,
  the rotation and adding a singer, with a link to the full host page. Collections and Categories
  stay as secondary pivots.
