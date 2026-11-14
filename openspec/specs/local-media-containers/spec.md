# local-media-containers Specification

## Purpose
How local files with no metadata provider are still grouped: a plugin's declared containers are built from the file layout alone, and a parser may supply the per-file Media title.

## Requirements

### Requirement: Local files are grouped into plugin containers without a provider

When a Library's plugin supplies a hydration mapper that groups a file into container Entities,
a scan SHALL write those Entities and their relations from the local path alone, with no provider
credentials, and SHALL mark the file complete so later scans do not rewrite them. Media already in
the Library without the completion mark SHALL be grouped by a backfill scan the server queues once,
on its first start with the mapper, for each Library holding such Media.

#### Scenario: Episode files with no provider keys

- **WHEN** a Library of the Film & TV plugin holds `Show/Season 1/Show - S01E02 - Title.mkv` and no provider is configured
- **THEN** after a scan a show Entity named `Show` has a season Entity numbered 1 that holds the Media in episode order

#### Scenario: Movie file

- **WHEN** the scanned file is not an episode
- **THEN** no container Entity is written and the Media is marked complete

#### Scenario: Library scanned before the change

- **WHEN** an episode Media imported earlier lacks the completion mark
- **THEN** the server's first start with the mapper queues one backfill scan of its Library, which groups it into its show and season
- **AND** later starts queue no further backfill for that Library

### Requirement: A parser may supply the per-file Media title

When the Library's parser supplies a non-empty per-file title for a path, the imported Media SHALL
take that title instead of the parsed title. Identification SHALL still receive the parsed title,
and a title written by identification SHALL be kept across rescans.

#### Scenario: Episode title from the filename

- **WHEN** a file parses to the series `Show` and the parser supplies the per-file title `Title`
- **THEN** the Media title is `Title`

#### Scenario: No per-file title

- **WHEN** the parser supplies an empty per-file title
- **THEN** the Media title is the parsed title
