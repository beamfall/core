# audiobook-hydration Specification

## Purpose
Hydrate audiobook credits, series and genres from file tags and folders once per book, without repeated rewrites on later scans.

## Requirements

### Requirement: Audiobook files hydrate credits, series and genres once
When the audiobook bundle is active, Core SHALL hydrate each audiobook Media from the plugin's
tag-and-folder plan, writing only relations the bundle declares, and SHALL set the
`audiobook-hydrated` marker. A file unchanged on disk SHALL NOT be hydrated again. A Media made
of several Parts SHALL hydrate from its first Part's tags and path when that Part is new or
changed on disk, or when the one-time backfill finds the Media lacking the marker.

#### Scenario: Tagged book
- **WHEN** a file tagged with an author, a narrator, a series with part "4.5" and a genre is scanned
- **THEN** the Media gains `credited-on` links with roles author and narrator, a `contains-book`
  link with sequence "4.5" and a `genre-of` link, and is marked hydrated

#### Scenario: Untagged book
- **WHEN** an untagged file in a plain folder is scanned
- **THEN** no relation is written and the Media is still marked hydrated

#### Scenario: Multi-part book
- **WHEN** a book of numbered files `01.mp3`, `02.mp3` tagged with an author is scanned
- **THEN** the one Media gains the author's `credited-on` link and is marked hydrated
