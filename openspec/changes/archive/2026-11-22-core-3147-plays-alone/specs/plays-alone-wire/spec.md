## ADDED Requirements

### Requirement: Cards and the media read say when a Media plays alone

Every Graph read that returns `SourceItem` cards, and the `media(id)` read, SHALL set `playsAlone`
to true on a Media item whose Library plugin declares `Engagement.PlaysAlone`. Entities, Media in a
Library whose plugin does not declare it, and Media in a plugin-less Library SHALL omit the field.
Core SHALL NOT derive the flag from a Media class or a plugin identity.

#### Scenario: Declaring plugin

- **WHEN** a Media item lives in a Library whose plugin declares `Engagement.PlaysAlone`
- **THEN** its card, its search card and its `media(id)` read carry `playsAlone: true`

#### Scenario: Non-declaring plugin

- **WHEN** a Media item lives in a Library whose plugin does not declare `Engagement.PlaysAlone`
- **THEN** its card and its `media(id)` read carry no `playsAlone` key

### Requirement: Clients never auto-advance into or out of a plays-alone Media

A client that builds a play queue from a list of cards SHALL start a `playsAlone` item with a
one-item queue, and its player SHALL NOT auto-advance out of a `playsAlone` item or into one from
another queued item. A listing whose Media carry `playsAlone` SHALL NOT present as a track list
sharing one queue. A Play Queue the user builds explicitly is unaffected.

#### Scenario: Shelf Quick Play

- **WHEN** the user Quick Plays a `playsAlone` card on a library shelf or its See all page
- **THEN** playback starts with a queue holding only that item

#### Scenario: Mixed queue

- **WHEN** a queue built from a list holds plays-alone and other items and a non-plays-alone item is playing
- **THEN** the player's queue drops the plays-alone items
