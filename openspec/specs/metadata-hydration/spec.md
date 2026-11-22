# metadata-hydration Specification

## Purpose
How core drives the metadata hydration hook when a file's content changes: hydration is keyed on the file's content fingerprint so re-tagged files are re-hydrated, while operator locks and manual edits always win.

## Requirements

### Requirement: Hydration is keyed on the file's content fingerprint

The scan SHALL pass the content fingerprint of the file it hydrates to the local metadata
hydration hook. When the hook's mapper declares a fingerprint field, the hydration seam SHALL read
the fingerprint recorded at the last hydration and ask the mapper how to write: skip, write absent
fields only, or replace. Both write cases SHALL record the current fingerprint. A mapper that
declares no fingerprint field SHALL keep the absent-only write.

#### Scenario: A re-tagged file shows its new tags

- **WHEN** a hydrated file's tags change (new title and album, new bytes, later mtime) and the
  library is scanned again
- **THEN** the Media's title and album fields hold the new tag values and the recorded
  fingerprint is the new one

#### Scenario: An unchanged file is not re-hydrated

- **WHEN** a hydrated file is scanned again with the same bytes, even after its mtime changed
- **THEN** the hydration seam writes nothing

#### Scenario: A file hydrated before fingerprints were recorded

- **WHEN** a hydrated file has no recorded fingerprint
- **THEN** the hydration seam writes absent fields only and records the current fingerprint

### Requirement: Re-hydration never overrides operator or manual values

A replace write SHALL go through the source-ranked field upsert at the scan tier. It SHALL leave
in place any field whose recorded source ranks above the scan tier (an operator identification
or a manual edit) and any field the operator locked (ADR-0072).

#### Scenario: A locked title survives a re-tag

- **WHEN** the operator locked a Media's title and the seam re-hydrates that Media with a new
  fingerprint and a different title and album
- **THEN** the album is updated and the locked title is kept

#### Scenario: A manually edited title survives a re-tag

- **WHEN** a Media's title was last written by a manual edit and the seam re-hydrates that Media
  with a new fingerprint
- **THEN** the manually edited title is kept

#### Scenario: Unreadable locks fail closed

- **WHEN** the seam would replace a Media's fields but cannot read that Media's field locks
- **THEN** it writes only absent fields and keeps the recorded fingerprint, so a later scan
  retries the replace

### Requirement: An exact top-score identification tie is queued for review

When automatic identification scores two or more candidates with different provider IDs at the
same highest score, the server SHALL NOT apply any of them, whatever their popularity, and SHALL
queue the item for review with the first candidate at that score, as it queues a result below the
plugin's threshold. Candidates that share one provider ID SHALL count as one item.

#### Scenario: Two different items tie at the top

- **WHEN** a file titled `The Office` is identified and two different series named `The Office`
  score the same and above the threshold
- **THEN** neither is applied and the item is queued for review with its top candidate

#### Scenario: Duplicates of one item

- **WHEN** the search returns the same provider ID twice at the highest score
- **THEN** that item is applied

#### Scenario: Distinct scores

- **WHEN** the highest-scoring candidate scores above every other and clears the threshold
- **THEN** it is applied, even when a lower-scoring candidate is more popular

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

### Requirement: Local-file extractors receive the class the file actually has

The scanner SHALL offer a file to plugin local-metadata extractors with class `image` for a still-image extension and `video` for a file whose probe found a video stream, and SHALL NOT offer audio-only or unprobed non-image files. A file that produced local metadata SHALL import standalone, not as a queued Part or attached Version, so its extracted fields are applied. An unchanged file on the unprobed fast path is not re-extracted until it changes.

#### Scenario: A camera-roll clip gets its capture date

- **WHEN** a scan finds a `.mov` clip with a video stream and a plugin extractor that reads its capture date
- **THEN** the extractor is called with class `video` and the clip's Media carries the extracted capture date

#### Scenario: An audio file is not offered to extractors

- **WHEN** a scan finds an audio-only file
- **THEN** no local-metadata extractor is called for it
