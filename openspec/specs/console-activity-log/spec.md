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

### Requirement: Job log progress entries are readable

A job's progress history entry SHALL store the update's current count, total, unit and message. The
Console job log SHALL show a progress entry as the job's progress line rather than its stored
payload, and SHALL show a payload it does not recognise as stored.

#### Scenario: A progress entry shows what the job was doing

- **WHEN** a scan reports 120 of 340 files with the message "Scanning"
- **THEN** its job log entry reads as 120/340 files and "Scanning"

#### Scenario: A status-only payload adds nothing

- **WHEN** an entry's payload only repeats its status
- **THEN** the log shows the entry's type and time and no payload

### Requirement: Server-initiated entries name their system actor

An activity entry the server writes on its own, with no session, Profile, token or key, SHALL name
its system actor from a closed vocabulary, derived from the action and not stored. A `media.merge`
entry with none of those SHALL have the system actor `scan`; a `media.merge` a session, token or key
made SHALL have none. `activityLog` and `activityEntry` SHALL expose it as `systemActor`, and the
activity sentence SHALL name the library scan as the actor.

#### Scenario: An automatic merge reads as the scan's

- **WHEN** a scan merges verified copies and the operator opens the activity log
- **THEN** the `media.merge` row has `systemActor` `scan` and reads as done by the library scan
