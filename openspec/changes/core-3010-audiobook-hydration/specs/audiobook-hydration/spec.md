## ADDED Requirements

### Requirement: Audiobook files hydrate credits, series and genres once
When the audiobook bundle is active, Core SHALL hydrate each audiobook Media from the plugin's
tag-and-folder plan, writing only relations the bundle declares, and SHALL set the
`audiobook-hydrated` marker so a later scan does not hydrate the Media again.

#### Scenario: Tagged book
- **WHEN** a file tagged with an author, a narrator, a series with part "4.5" and a genre is scanned
- **THEN** the Media gains `credited-on` links with roles author and narrator, a `contains-book`
  link with sequence "4.5" and a `genre-of` link, and is marked hydrated

#### Scenario: Untagged book
- **WHEN** an untagged file in a plain folder is scanned
- **THEN** no relation is written and the Media is still marked hydrated
