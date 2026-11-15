# Identification keeps a parser-owned display title

Ticket: CORE-1713 (`queue:core:main`).

## Why

A plugin's filename parser can give a file its own display title when its parsed title names a
container several files share (`MediaTitler`, CORE-1411). The Film & TV parser uses it for the
episode code (`S01E03-04`, or the air date of a date-named episode). Identification then looks up
the container (a series document) and overwrites the Media title with the container's name, so
identified sibling files become indistinguishable again.

## What Changes

- `identify.Service` gains an optional `MediaTitleSource`. When the plugin's parser supplies a
  per-file title for the Media's primary file, `Apply` writes that title instead of the scraper's.
  Summary, year, plugin fields, entities and artwork are applied as before.
- A blank parser title, a plugin with no `MediaTitler`, or a failed read (for example a Media whose
  files are all missing) keeps the scraper's title. A lookup with no title is not affected.
- The Fix-Match preview proposes the same title `Apply` writes (`Service.AppliedTitle`).
- The composition root wires the compiled filename parsers as the source, reading the primary file
  with the server-internal full-visibility filter, so restricted libraries behave the same.

## Impact

- Code: `internal/identify` (title source, `AppliedTitle`, `Apply`), `internal/graph` (Fix-Match
  preview title), `internal/app` (parser adapter and wiring).
- No store method, migration, GraphQL or wire-contract change. Search and Lookup candidates keep the
  scraper's title: a candidate names what was matched.
