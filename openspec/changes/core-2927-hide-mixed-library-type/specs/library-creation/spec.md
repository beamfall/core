## MODIFIED Requirements

### Requirement: One media-type list for every surface that creates a library

The web app SHALL derive the media types offered when creating a library from the server's plugin
list, in one shared module used by both the first-run setup wizard and Console > Libraries. Every
installed plugin SHALL appear, labelled with its server-provided display name. A plugin that
declares family scopes SHALL appear once per offered scope instead. For the Film & TV plugin the
offered scopes SHALL be Films and TV Shows; its catch-all Mixed scope SHALL NOT be offered.

#### Scenario: Every installed plugin is offered

- **WHEN** the server reports the Film & TV, Music, Photos and Karaoke plugins
- **THEN** both setup and Console offer Films, TV Shows, Music, Photos and Karaoke, and neither
  offers Mixed
