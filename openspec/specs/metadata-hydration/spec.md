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

### Requirement: Scraper-declared container membership follows the identification

When a scraper declares a non-container EntityMap onto a container kind's single-holder Media
relation, applying its identification SHALL link the Media to the container Entity the lookup
hydrates for that map, recording the scraper as the link's writer. When that map ran and hydrated
no Entity, the server SHALL remove the Media's link under that relation. A scraper without such a
map SHALL leave the Media's container links as they are.

#### Scenario: Two members of one container

- **WHEN** two Media items are identified through lookups whose documents name the same container
  object
- **THEN** one container Entity exists, titled from the object's name, and holds both items

#### Scenario: The new match belongs to no container

- **WHEN** a Media item held by a container Entity is identified again through a lookup whose
  document names no container object
- **THEN** the Media item is no longer linked to that container Entity

#### Scenario: The new match belongs to another container

- **WHEN** a Media item held by a container Entity is identified again through a lookup whose
  document names a different container object
- **THEN** the Media item is held only by the other container Entity

### Requirement: Scraper container cleanup removes only the scraper's own links

The removal of a stale single-holder link SHALL delete only rows the applying scraper wrote from an
Entity of the map's kind, and SHALL keep every other row, including an operator's link. When the
engine skipped a map because its path names an unknown ordinal, the server SHALL remove nothing
under that map's kind and relation, even when a sibling map onto them hydrated nothing.

#### Scenario: An operator's link survives

- **WHEN** an operator linked a Media item to a container Entity and the item is identified through
  a lookup whose document names no container object
- **THEN** the Media item is still held by that container Entity

#### Scenario: A skipped map keeps the link

- **WHEN** a Media item held by a container Entity is identified again while the map's path names an
  ordinal the parser does not know
- **THEN** the Media item is still held by that container Entity

#### Scenario: A skipped map protects the link from an empty sibling map

- **WHEN** a Media item held by a container Entity is identified again while one map's path names an
  ordinal the parser does not know and a second map onto the same relation hydrates nothing
- **THEN** the Media item is still held by that container Entity

### Requirement: A scraper titles only the container it linked

Linking a Media item to an existing container Entity, trashed or not, SHALL keep that Entity's
stored title, which the store keeps in the same write as the link's fields so a concurrent rename
stands; a new container Entity whose document gives no name SHALL take the provider id as its title. A container
EntityMap SHALL apply its title, fields and artwork only to a container Entity the same lookup
linked through the scraper's single-holder map, when the scraper declares one for that kind.

#### Scenario: A container object without a name keeps the title

- **WHEN** a Media item held by a titled container Entity is identified again through a lookup whose
  container object has an id but no name
- **THEN** the container Entity keeps its title

#### Scenario: A trashed container keeps its title

- **WHEN** a container Entity is trashed, a member is identified through a lookup whose container
  object has its id but no name, and the Entity is restored
- **THEN** the container Entity has the title it had before it was trashed

#### Scenario: A container object without an id leaves an operator's container alone

- **WHEN** an operator linked a Media item to a container Entity and the item is identified through
  a lookup whose container object has a name but no id
- **THEN** the container Entity keeps its title

### Requirement: Scraper-declared container members are ordered by year

A single-holder link written from such a map without an ordinal of its own SHALL take the Media's
stored year, after field precedence, as its sort order, so the container lists its members in
release-year order and a manual year outranks the scraper's. A link whose map supplies an ordinal
SHALL keep that ordinal.

#### Scenario: Members listed in release order

- **WHEN** a container holds an item identified with the year 2003 and an item identified with the
  year 1999
- **THEN** the container's Media list shows the 1999 item first

#### Scenario: A manual year orders the member

- **WHEN** a container member has a manual year of 2010 and its lookup reports 1999, and another
  member is identified with the year 2003
- **THEN** the container's Media list shows the 2003 item first

### Requirement: Sidecar metadata is written at the locked local tier

The hydration seam SHALL write a sidecar's local part at the `local` mutation source, which ranks
below a manual edit and above an operator identification, after validating its fields and
relations against the active bundle. Fields last written at the local tier SHALL read as locked.
A later identification SHALL leave every local field in place.

#### Scenario: An identification keeps the sidecar values

- **WHEN** a Media's title, summary, year and a declared Media field were written from a sidecar
  and an identification later writes different values for them at the operator tier
- **THEN** the Media keeps the sidecar values and reports those fields locked

#### Scenario: A manual edit outranks a sidecar value

- **WHEN** an operator edits a field a sidecar wrote
- **THEN** the manual value lands

### Requirement: A sidecar refresh is one transaction on the Media's catalog

The seam SHALL apply a sidecar's local part in one store transaction on the catalog that holds
the Media, an unsealed encrypted library's included: release the previous local fields to the
scan tier, write the new local values, reconcile the owned relations and record the signature of
the bytes the mapper parsed. A failed apply SHALL leave the previous values, locks and signature.

#### Scenario: A failed refresh keeps the locks

- **WHEN** applying an edited sidecar fails part way
- **THEN** the previous local values stay locked and the old signature stays, so the next scan
  retries

#### Scenario: An encrypted library takes sidecar values

- **WHEN** a Media in an unsealed encrypted library has a sidecar with a title
- **THEN** the title lands at the local tier

#### Scenario: The recorded signature is the parsed sidecar's

- **WHEN** a sidecar is replaced after the mapper read it and before the seam recorded it
- **THEN** the recorded signature is the one the mapper read, and the next scan applies the
  replacement

### Requirement: The recorded sidecar identity names its source

The identity the seam records and compares SHALL be the selected file's path together with the
mapper's signature, so a selection that moves to another file's sidecar refreshes the Media even
when the two sidecars carry the same signature.

#### Scenario: Two versions carry sidecars with the same signature

- **WHEN** a Media's first version's sidecar is removed and its second version has a sidecar of
  the same name, size and modification time but different values
- **THEN** the next scan applies the second sidecar's values

### Requirement: Sidecar relations are owned by the local writer

Relation rows a sidecar wrote SHALL carry the writer `local`. For each relation the sidecar format
owns, the seam SHALL replace the Media's local-writer rows with the sidecar's rows, emptying a
relation the sidecar no longer lists, and SHALL leave every row an operator or another writer
owns. A scraper's upsert SHALL NOT take a local row over. A row a removed sidecar released SHALL
be taken back by the local writer when a sidecar lists it again.

#### Scenario: A manual credit survives a sidecar edit

- **WHEN** a Media has a sidecar credit and an operator-added credit and the sidecar is edited
- **THEN** both credits remain

#### Scenario: Removing the last listed credit removes the local row

- **WHEN** a sidecar that listed one credit is edited to list none
- **THEN** the Media has no local credit

#### Scenario: A restored sidecar reclaims its released credit

- **WHEN** a sidecar's credit is released by removing the sidecar, the sidecar is restored with the
  same credit and then edited to list none
- **THEN** the Media has no local credit

### Requirement: A sidecar with no local values is still applied

A readable sidecar with a signature and no local values SHALL be applied as an empty plan that
owns the format's relations: its previous local fields are released, its owned relations are
emptied and its identity is recorded.

#### Scenario: A populated sidecar is replaced by one with no values

- **WHEN** a hydrated Media's sidecar is replaced by a readable sidecar that names no values
- **THEN** the previous fields read as unlocked, the sidecar's credits are gone and the next scan
  does not apply it again

### Requirement: An unreadable sidecar is not a removed sidecar

When the mapper reports a sidecar that exists but could not be read, or the signature lookup of
any of the Media's files fails for a reason other than a clean not-found, the seam SHALL release
nothing, apply nothing and record no signature, and SHALL NOT move on to another file's sidecar,
so the next scan retries.

#### Scenario: A sidecar unreadable for one scan

- **WHEN** a hydrated Media's edited sidecar cannot be read during a scan and is readable on the
  next one
- **THEN** the old values stay locked after the first scan and the edited values land on the next

#### Scenario: A sidecar lookup fails

- **WHEN** the lookup of a hydrated Media's only sidecar fails because its directory cannot be
  searched
- **THEN** the values stay locked, the relation rows stay owned and the recorded signature stays

### Requirement: A changed sidecar refreshes every file the scan visits

The scan SHALL refresh the sidecars of an unchanged file, and of a file it attaches as a version,
whose sidecar signature differs from the recorded one, and of each file it imports as one of a group of
parts, without needing a media probe. The Media's
sidecar SHALL be read beside the first of its files, in path order, that has one. A signature
check that fails SHALL leave the file as unchanged.

#### Scenario: An edited sidecar refreshes its fields

- **WHEN** a hydrated file's sidecar is edited and the library is scanned again with the media
  file unchanged
- **THEN** the Media holds the edited sidecar values

#### Scenario: A sidecar beside a secondary version

- **WHEN** a Media has two versions and the only sidecar sits beside the second
- **THEN** the Media takes that sidecar's values

#### Scenario: An edited sidecar beside grouped parts

- **WHEN** a Media imported as a group of parts has its sidecar edited and the library is scanned
  again with the parts unchanged
- **THEN** the Media holds the edited sidecar values

### Requirement: A removed sidecar unlocks its values without clearing them

When none of a Media's files has a sidecar and a signature was recorded, the seam SHALL release
its local fields to the scan tier, clearing any identified mark a released field carries, and its
local relation rows to an unowned writer, keeping the values, and record the empty signature, in
one transaction, whether or not a probe succeeds.

#### Scenario: Removing the sidecar unlocks the fields

- **WHEN** the sidecar of a Media with local fields is removed and the library is scanned again
  without a media probe
- **THEN** the Media keeps the values, the fields read as unlocked, and a later identification
  replaces them

#### Scenario: A rescan after removal takes the file's own title

- **WHEN** a sidecar title is released by removing the sidecar and the file is imported again
  with a title from its name
- **THEN** the Media takes the title from the file name

### Requirement: Identification searches carry the item's search hints

When the server searches a scraper to identify an item, it SHALL substitute each `{hint.<key>}`
placeholder in the scraper's SearchTemplate URL query with the item's hint value for that key,
escaped as a URL query value. A missing or empty hint SHALL substitute the empty string. A scraper
whose required search hints the item does not all supply SHALL be skipped for that search.

#### Scenario: Hint values reach the query escaped

- **WHEN** an item with hint `creator` = `A & B` is searched with a template
  `/search?q={query}&by={hint.creator}`
- **THEN** the request query is `q=<title>&by=A+%26+B`

#### Scenario: Missing optional hint

- **WHEN** the item has no value for a hint the template names and the scraper does not require it
- **THEN** the placeholder renders as the empty string and the search runs

#### Scenario: Missing required hint

- **WHEN** the item has no value for a hint the scraper lists as required
- **THEN** the scraper is not searched for that item, and automatic identification uses the next
  searchable scraper for the item's class

### Requirement: An item's search hints come from its parser and embedded tags

The server SHALL take an item's search hints from its plugin parser, and SHALL fill each hint the
parser leaves empty from the file's non-empty embedded tag that the plugin bundle's embedded-tag
hint map names for it.

#### Scenario: Embedded tags fill parser gaps

- **WHEN** the parser gives no value for a hint and the file has a non-empty embedded tag the
  bundle maps to that hint
- **THEN** the tag value is the hint; a parser-supplied value is kept over the tag
