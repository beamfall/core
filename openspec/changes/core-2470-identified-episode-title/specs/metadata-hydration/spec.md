## ADDED Requirements

### Requirement: Identification passes its field values to the parser title

When an identification is applied and the library plugin's filename parser builds per-file display
titles from identified values, the server SHALL give it the identification's plugin-declared Media
field values for the Media's primary file and SHALL write the non-blank title it returns. When the
parser has no such hook, returns a blank title or the file cannot be read, the server SHALL write the
title the parser-owned display title rule gives. The Fix-Match preview SHALL propose the same title.

#### Scenario: Identified episode name in the title

- **WHEN** an episode file titled `S03E01` by its parser is identified through a lookup that writes
  the Media field `episode-title` as `Smells Like Mean Spirit`, and the parser builds titles from
  identified values
- **THEN** the Media title is the parser's title for that value, `S03E01 · Smells Like Mean Spirit`

#### Scenario: Parser without the hook

- **WHEN** the plugin's parser builds no title from identified values
- **THEN** the Media keeps the parser's per-file title, or the lookup's title when there is none

#### Scenario: Preview proposes the identified title

- **WHEN** the operator previews that Fix-Match
- **THEN** the preview proposes `S03E01 · Smells Like Mean Spirit`
