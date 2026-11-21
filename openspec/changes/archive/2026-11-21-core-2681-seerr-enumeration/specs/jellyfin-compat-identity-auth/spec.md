## MODIFIED Requirements

### Requirement: Compatibility notifier probes disclose no server paths

The server SHALL expose GET `System/Configuration`, GET `Library/MediaFolders` and GET `Items`
with an integration session granting read access. `System/Configuration` SHALL return an empty
JSON object. `Items` with neither a `ParentId` nor an `ids` parameter SHALL return an empty item
list with a total count of zero. No compatibility response SHALL include a server filesystem path.

#### Scenario: A tool looks up the server path of a changed item

- **WHEN** an authenticated caller requests `Items` with a path field and no parent or ids
- **THEN** the server returns an empty item list

### Requirement: Compatibility media updates refresh only the containing roots

An admitted media update SHALL queue one scan per eligible Library root that equals or contains an
updated absolute path, through the existing per-root scan queue, and SHALL queue nothing for paths
outside every eligible root. When every updated path is relative, the update SHALL queue a scan of
every eligible Library root instead. A well-formed request SHALL return an empty 204 response
whether or not any root matched. Matching SHALL use the session scope read after the body arrives
and SHALL run after the response is written.

#### Scenario: Two updates under one root

- **WHEN** an admitted request lists two paths under the same eligible Library root
- **THEN** the server queues one scan for that root and none for any other root

#### Scenario: Session revoked during the upload

- **WHEN** the integration session is revoked while its media update body is arriving
- **THEN** the server returns the uniform 401 and queues no scan

#### Scenario: Update outside every root

- **WHEN** an admitted request lists only a path outside every eligible Library root
- **THEN** the server queues no scan and returns an empty 204 response

#### Scenario: Lidarr posts relative paths

- **WHEN** an admitted request lists only paths relative to the caller's own root folder
- **THEN** the server queues a scan for every eligible Library root

## ADDED Requirements

### Requirement: Compatibility user enumeration lists opted-in Profiles

With an integration session granting read access, the server SHALL serve GET `Users` as the list
of enabled Profiles opted in to compatibility sign-in, each with only its ID and display name.
GET `Users/{id}` SHALL describe one such Profile with an empty `Configuration.GroupedFolders`.
GET `Users/{id}/Images/Primary` SHALL return that Profile's stored picture, or 404 when it has
none. Any other user ID SHALL get 404.

#### Scenario: Seerr imports users

- **WHEN** an authenticated caller lists `Users`
- **THEN** each opted-in enabled Profile appears once and no other Profile appears

#### Scenario: A Profile without a picture

- **WHEN** a caller requests the primary image of an opted-in Profile with no stored picture
- **THEN** the server returns 404

### Requirement: Compatibility media folders list eligible Libraries

GET `Library/MediaFolders` SHALL list each eligible Library the session's Profile can see without
Reveal as a collection folder with its ID, name and the collection type that the Library plugin's
interop declaration maps the Library's scope to. A Library whose scope has no mapping, an
encrypted Library and a Library off the operator allowlist SHALL NOT be listed. No folder SHALL
carry a path.

#### Scenario: One allowlisted and one restricted Library

- **WHEN** the allowlist holds a visible Library and a Reveal-restricted Library
- **THEN** only the visible Library is listed and the total count is one

### Requirement: Compatibility item types come from plugin interop declarations

The server SHALL derive each item's type from the interop declaration of the Library's plugin: a
Media item's type from its declared media field value, and a container's type from its declared
kind. Core SHALL compare declared tokens for equality and SHALL NOT name a domain type. An item
with no declared mapping SHALL NOT appear in any compatibility response.

#### Scenario: A plugin declares no interop

- **WHEN** an eligible Library's plugin declares no interop mapping
- **THEN** its Library and items do not appear in any compatibility response

### Requirement: Compatibility items are filtered before counting and paging

`Items?ParentId=<library>` SHALL return the top-level items of one eligible Library: the topmost
mapped container of each visible Media item, or the Media item itself when it has none, filtered by
`IncludeItemTypes`. Reveal, certification and the allowlist SHALL apply before the total count and
paging. Items SHALL be ordered by title and then ID, `Limit` SHALL be capped at 1000, and an
unbounded request SHALL return at most 50,000 items while reporting the true total.

#### Scenario: Paging is stable

- **WHEN** a caller pages a Library with `StartIndex` and `Limit` and the Library does not change
- **THEN** the pages are disjoint, cover every item once and report the same total

#### Scenario: A certification-restricted item

- **WHEN** a Library holds an item above the session Profile's certification ceiling
- **THEN** that item is absent from every page and from the total count

### Requirement: Compatibility item lookups use one uniform miss

`Items?ids=`, `Items/Latest?ParentId=`, `Shows/{id}/Seasons` and `Shows/{id}/Episodes` SHALL return
only items visible to the session's Profile in eligible Libraries. `Items/Latest` SHALL return the
newest visible Media items, at most 100. Seasons and episodes SHALL be ordered by their declared
ordinal. A parent or container ID that is unknown, not visible or outside every eligible Library
SHALL get the same 404, and an unmatched ID in `ids` SHALL be omitted.

#### Scenario: A Reveal-restricted item by ID

- **WHEN** a caller requests `Items?ids=` with the ID of an item in a Reveal-restricted Library
- **THEN** the response holds no items and a total count of zero

#### Scenario: An unknown show

- **WHEN** a caller requests the seasons of an ID that names no visible container
- **THEN** the server returns 404 with the same body as for any other miss

### Requirement: Compatibility items carry provider IDs and video size only

Each item SHALL carry a `ProviderIds` object, possibly empty, holding the stored external IDs and
the ID from the scraper that identified the item when that scraper's lookup is a by-ID lookup.
A Media item SHALL carry one media source with one video stream holding only its stored width and
height. No item SHALL carry a path, file name, stream URL, artwork or watch state.

#### Scenario: An item identified by a by-ID scraper

- **WHEN** an item was identified by a scraper whose lookup is the provider's by-ID lookup
- **THEN** its `ProviderIds` holds that provider's ID
