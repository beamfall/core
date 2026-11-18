## ADDED Requirements

### Requirement: A rescan merges an already-split copy into its title's Media

When a scan finds cataloged files of one title in one directory, none with a parsed version label,
on more than one live Media, it SHALL merge them. The Media with the most files, or the earliest
imported on a tie, survives. Each other Media whose single live file is unlabelled SHALL have that
file moved onto the survivor under the derived label a fresh scan would give it, and SHALL move to
the trash in the same journaled, undoable batch. A Media no derived label can place SHALL stay as it
is.

#### Scenario: A split from an earlier scan heals on rescan

- **WHEN** an earlier scan imported `Disclosure.Day.2026.REPACK.1080p.Remux.mkv` (1920x800) and
  `Disclosure.Day.2026.2160p.WEB-DL.mkv` (3840x1600) as two Media, and the library is rescanned
- **THEN** one Media holds both files as Versions labelled `1080p` and `2160p`, and the other Media
  is in the trash

#### Scenario: Undo restores the split

- **WHEN** the merge batch is undone
- **THEN** each Media again holds its own unlabelled file, the merged Media leaves the trash, and a
  later rescan leaves the two Media apart

### Requirement: A version merge keeps user state and stays within its limits

A version merge SHALL copy the merged Media's Collection membership, tags, playback progress,
ratings, favourites and watchlist entries to the survivor only where the survivor has none, and SHALL
point its queue entries at the survivor unless the survivor is already queued. It SHALL emit
`media.deleted` for each merged Media and `media.updated` for the survivor. It SHALL NOT run in an
encrypted library or while trash is disabled.

#### Scenario: User state survives the merge

- **WHEN** a profile has progress, a rating and a queue entry on the merged Media and its own rating
  on the survivor
- **THEN** the survivor shows the merged Media's progress and queue entry and keeps its own rating

#### Scenario: Encrypted library

- **WHEN** the split is in an encrypted library
- **THEN** the rescan leaves both Media as they are

#### Scenario: Explicit Editions and Extras stay apart

- **WHEN** the split copies are linked by an `edition-of` or `extra-of` relation, or one is an Extra
- **THEN** the rescan leaves both Media as they are

#### Scenario: Trash Restore of a merged copy

- **WHEN** a user restores the merged Media from Trash while its merge batch is not undone
- **THEN** the restore is refused with a message to undo the merge, and the survivor keeps the file

#### Scenario: Undo from a scope that cannot see the merge

- **WHEN** a caller whose Library scope hides the merged Media undoes the merge batch
- **THEN** the undo is refused as if the batch did not exist, and the batch stays undoable from a
  scope that can see it
