## ADDED Requirements

### Requirement: Cards and the media read say when a Media plays on Select

Every Graph read that returns `SourceItem` cards, and the `media(id)` read, SHALL set `selectPlays`
to true on a Media item whose Library plugin declares `Engagement.SelectPlays`. Entities, Media in
a Library whose plugin does not declare it, and Media in a plugin-less Library SHALL omit the
field. Core SHALL NOT derive the flag from a Media class, artwork or a plugin identity.

#### Scenario: Declaring plugin

- **WHEN** a Media item lives in a Library whose plugin declares `Engagement.SelectPlays`
- **THEN** its card, its search card and its `media(id)` read carry `selectPlays: true`

#### Scenario: Non-declaring plugin

- **WHEN** a Media item lives in a Library whose plugin does not declare `Engagement.SelectPlays`
- **THEN** its card, its search card and its `media(id)` read carry no `selectPlays` key
