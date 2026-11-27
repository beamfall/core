# Feed shows: container Entity and artwork on catalogue ingest

Ticket: CORE-3524 (`queue:core:main`).

## Why

Feed catalogue ingest mints one file-less Media per entry and nothing else, so a podcast Library
shows a flat list of episodes with placeholder icons: no show to browse, no artwork.

## What Changes

- Feed parsing reads the channel and entry `itunes:image` (channel falls back to RSS `<image><url>`),
  plus the channel description, link and author.
- After each catalogue upsert, a Subscription in a Library whose plugin declares an outermost
  container kind with a single-holder Media relation gets one container Entity of that kind,
  titled by the feed, holding its catalogue episodes through that relation. Kind and relation come
  from the plugin bundle; core names no domain vocabulary.
- The channel image is the show's poster. An episode takes its own image as poster and thumb, else
  the show's image as poster. Artwork is best-effort, never refetched once present, and bounded
  per poll.
