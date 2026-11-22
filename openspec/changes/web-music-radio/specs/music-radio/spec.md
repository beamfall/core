## ADDED Requirements

### Requirement: Station generation expands to related Media

The Station generator SHALL add to the seed's own Media the Media that share an Entity with the
seed (credits, containers, categories), and SHALL rank candidates by shared Entities, closeness of
release era to the seed and the acting Profile's play history. It SHALL NOT use audio analysis. A
track seed SHALL lead its own window. Sealed, hidden and non-music Libraries SHALL stay excluded
before candidates are collected.

#### Scenario: Related artist through a shared category

- **WHEN** a listener starts a station from a track whose category Entity also links a track by
  another artist
- **THEN** the window includes that other track, after the seed track

#### Scenario: Generic seed kinds

- **WHEN** a client starts a station with seed kind `entity` or `media`
- **THEN** the generator treats it like the Entity or track seed it names

### Requirement: Decade and Entity station rules

Station rules SHALL accept `decades`, a list of decade start years, and `anyEntityIDs`, a list of
Entity ids. A candidate SHALL pass only when its year falls in one of the listed decades and, when
`anyEntityIDs` is set, when it links to at least one of the listed Entities. Rules SHALL apply
before ranking.

#### Scenario: Decade rule

- **WHEN** a station is generated with decades [1990]
- **THEN** no candidate released outside 1990–1999 appears in the window

#### Scenario: Category rule

- **WHEN** a station is generated with anyEntityIDs naming a genre Entity
- **THEN** every candidate links to that Entity

### Requirement: Localized station explanations

Each generated item SHALL carry an explanation in the request locale naming its strongest reason:
the seed itself, an Entity it shares with the seed, the seed's era or the listener's play history.
Explanations SHALL never name a Library or another Profile's activity.

#### Scenario: Shared Entity reason

- **WHEN** a candidate is picked because it shares an Entity with the seed
- **THEN** its explanation names that Entity's title

### Requirement: Web viewer radio

The web viewer SHALL offer Start radio on audio Entity pages and in the track menu. Starting SHALL
ask before replacing a play queue that already has items, then play the generated queue in
server-queue mode. While a station plays, the player SHALL show why the current track was picked,
SHALL offer decade and category tuning, and SHALL add a fresh generation to the queue when
playback nears its end.

#### Scenario: Start from an album page

- **WHEN** a listener chooses Start radio on an album page and confirms replacing their queue
- **THEN** playback starts on the generated queue and the player shows the track's reason

#### Scenario: Queue top-up

- **WHEN** a station's current track is within five tracks of the queue's end
- **THEN** the web generates again and appends tracks not already queued
