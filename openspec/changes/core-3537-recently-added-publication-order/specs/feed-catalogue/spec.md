## ADDED Requirements

### Requirement: Recently-added orders feed Media by publication time

The `recently-added` source SHALL list Media that have a subscription entry newest-published first,
using the entry's feed publish time (first-seen time when the feed gave none), regardless of the
order they were ingested. Media without a subscription entry SHALL follow them in ingest order,
newest first. Libraries holding no subscription entries SHALL keep ingest order alone.

#### Scenario: Out-of-order ingest

- **WHEN** a feed holding an older newest-episode is catalogued after a feed holding a newer one
- **THEN** `recently-added` lists the newer-published episode first

#### Scenario: Library without feeds

- **WHEN** no visible Library holds subscription entries
- **THEN** `recently-added` lists by ingest order as before
