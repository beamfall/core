# console-activity-log Specification

## Purpose
The Console Activity log tells an operator what people, devices and plugins changed on the server: each entry as a plain sentence, at a time in the operator's own locale and time zone, with category filters, text search and the raw event key on demand.

## Requirements

### Requirement: Activity rows read as sentences

The Console Activity page SHALL show each activity entry as a plain-language sentence that names
the actor, the action and the target where the entry identifies them. Every action in the
server's closed activity vocabulary SHALL have a sentence. An action without one SHALL be shown
as a readable form of its key.

#### Scenario: Known action

- **WHEN** the log holds a `media.trash` entry by the Profile named Alex
- **THEN** the row reads "Alex moved an item to Trash"

#### Scenario: Unknown action

- **WHEN** the log holds an entry whose action the web app does not know, such as
  `widget.frobnicate_all`
- **THEN** the row reads "Widget frobnicate all"

#### Scenario: New server action without a sentence

- **WHEN** an action is added to the server vocabulary without a sentence in the web app
- **THEN** the web test suite fails and names the action

### Requirement: Raw entry details are available on demand

Each row SHALL offer a control that shows the raw action key, the target type and id, the acting
identifiers the entry carries and the exact UTC time.

#### Scenario: Show details

- **WHEN** the operator opens the details of a row
- **THEN** the raw action key and the exact UTC time are shown

### Requirement: Times are local with exact UTC on hover

Each row SHALL show the entry's time as relative time and as a date and time in the viewer's
locale and time zone, and SHALL expose the exact UTC time on hover.

#### Scenario: Local time

- **WHEN** a viewer in a non-UTC time zone opens the page
- **THEN** each row shows a relative time and the local date and time, and hovering the time
  shows the UTC time

### Requirement: The log can be filtered and searched

The page SHALL filter rows by event category and by a text search over the sentence, the raw
action key and the names and identifiers of the entry.

#### Scenario: Category filter

- **WHEN** the operator picks the Security category
- **THEN** only entries whose action belongs to that category remain

#### Scenario: Search without matches

- **WHEN** the search text matches no entry
- **THEN** the page says there are no matches
