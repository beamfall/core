## Purpose

Let a listener browse audio in the web viewer from generic signals core owns (Entity archetype,
Media class, server order and the scan's stored positions), never from plugin Kind names.

## ADDED Requirements

### Requirement: Audio container rows group and number by stored positions

When every Media listed by an audio container has a stored `sequence` field and they hold at least
two values, the web viewer SHALL group the rows by `sequence`, ascending, each group under its own
heading and list. Within a group, or the whole list, rows SHALL be numbered and ordered by stored
`index` when every row has a distinct one, else kept in server order and numbered from 1.

#### Scenario: Two discs

- **WHEN** a listener opens an audio container whose tracks store sequences 1 and 2 with indexes
- **THEN** the page shows a headed list per sequence, ascending, each numbered by stored index

#### Scenario: No stored sequence

- **WHEN** any listed track has no stored sequence, or all share one
- **THEN** the page shows one list without group headings

#### Scenario: Repeated index

- **WHEN** two tracks in a group store the same index
- **THEN** that group keeps server order and is numbered from 1

### Requirement: Play from a grouped listing queues every group

Starting playback from any row of an audio container's listing, or from the page's primary Play
action, SHALL use the listing's display order: the primary action starts the next-up track in that
order, and the player SHALL queue every listed row in display order, across groups.

#### Scenario: Play from the second disc

- **WHEN** a listener plays a track in the second group
- **THEN** the queue holds the first group's rows, then the second group's, in display order

#### Scenario: Primary Play with the second disc first in server order

- **WHEN** server order lists a second-disc track first and the listener uses the page's Play action
- **THEN** playback starts on the first disc's first track with the display order queued
