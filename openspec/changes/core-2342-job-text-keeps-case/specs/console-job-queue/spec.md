## ADDED Requirements

### Requirement: Console job text keeps its case

The Console SHALL humanize only enum-like wire tokens it has no label for (lowercase words joined by
`.`, `_` or `-`), reading them as words. Free-text values the server wrote, such as job progress
messages, error text, paths, titles and provider names, SHALL be shown as sent, with their case kept.

#### Scenario: An unknown status token

- **WHEN** the Console shows a job status `waiting_for_media` it has no label for
- **THEN** it reads "Waiting for media"

#### Scenario: A free-text message

- **WHEN** a job reports the message "Reading /Volumes/Media/Films"
- **THEN** the Console shows "Reading /Volumes/Media/Films" with its case kept
