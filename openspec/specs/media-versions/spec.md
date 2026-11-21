# media-versions Specification

## Purpose
Groups files of one title that differ only in resolution or edition under one Media as Versions at scan, using the labels a plugin parses, and lets a client list those Versions and play the one a viewer picks, with that pick bound into the Stream Token.

## Requirements

### Requirement: Scan groups files of one title into Versions

A scan SHALL attach a parsed, non-part, non-image file to the Media of another file from the same
directory and library when both parse to the same class, title, year, sequence and index, storing it
as a Version under the parser's version label. The other file MAY be one this scan imported or one an
earlier scan cataloged.

#### Scenario: Two resolutions of one title

- **WHEN** a directory holds `Night Harbor (2001) - 1080p.mkv` and `Night Harbor (2001) - 4K.mkv`
  and the parser labels them `1080p` and `4K`
- **THEN** the scan creates one Media with two Versions labelled `1080p` and `4K`

#### Scenario: Unlabelled file with a labelled sibling

- **WHEN** a directory holds `Coastline (1998).mkv` and `Coastline (1998) - Extended.mkv`
- **THEN** the scan creates one Media whose default Version is the unlabelled file and whose
  `Extended` Version is the other

#### Scenario: A file added beside a cataloged Version

- **WHEN** an earlier scan cataloged `Night Harbor (2001) - 4K.mkv` and `Night Harbor (2001) -
  1080p.mkv` is added to the same directory
- **THEN** the next scan adds the new file as the `1080p` Version of the existing Media

#### Scenario: Different titles stay apart

- **WHEN** two files in one directory parse to different titles or years
- **THEN** the scan creates one Media for each

### Requirement: Unlabelled pairs use resolution class then container labels

When neither file carries a parsed version label, the scan SHALL label both from the first of these
that tells them apart and leaves both slots free: the resolution class of the probed frame, then the
container; the file that held the default Version moves to its label.

#### Scenario: Two unlabelled copies of one title

- **WHEN** a directory holds `Disclosure.Day.2026.REPACK.1080p.Remux.mkv`, probed at 1920x800, and
  `Disclosure.Day.2026.2160p.WEB-DL.mkv`, probed at 3840x1600, and the parser reports one title and
  year and no label for both
- **THEN** the scan creates one Media with two Versions labelled `1080p` and `2160p`, and a rescan
  keeps those labels

#### Scenario: Unprobed unlabelled copies

- **WHEN** two unlabelled copies of one title have no probed frame size and are `.mkv` and `.mp4`
- **THEN** their Versions are labelled `MKV` and `MP4`; two copies in one container stay separate
  Media

### Requirement: Name-only differences stay separate

Files that differ only by name SHALL stay separate Media, since they may be different items.

#### Scenario: Names alone do not group files

- **WHEN** two files of one parsed title differ only by name
- **THEN** the scan keeps them as separate Media

### Requirement: Further unlabelled copies use the existing label kind

A further unlabelled copy SHALL join only under a free label of the kind the Media's Versions are
all labelled by.

#### Scenario: Another resolution joins a labelled Media

- **WHEN** a Media has Versions labelled by resolution class as `1080p` and `2160p`, and a further
  unlabelled copy has a probed resolution class of `720p`
- **THEN** the copy joins as the `720p` Version if that slot is free

### Requirement: Unlabelled overwrites keep their Version slot

New bytes without a parsed label written over a Version's file SHALL keep that Version's slot.

#### Scenario: New bytes replace a Version file

- **WHEN** new bytes without a parsed label overwrite the file in a Media's `1080p` Version
- **THEN** the next scan keeps the file in that Version's slot

### Requirement: Missing Version labels stay reserved

A missing Version's label SHALL stay reserved for its return.

#### Scenario: A missing Version reserves its label

- **WHEN** a Media's `1080p` Version is missing and a different unlabelled copy needs that label
- **THEN** the scan keeps the `1080p` label reserved for the missing Version's return

### Requirement: Stored labels survive later scans

A file whose label is stored SHALL keep it on later scans.

#### Scenario: A rescan preserves a stored label

- **WHEN** a file has a stored Version label and is scanned again
- **THEN** the file keeps its stored label

### Requirement: Files with no free Version slot import as their own Media

The scan SHALL import the file as its own Media when no such slot is free.

#### Scenario: All eligible labels are occupied

- **WHEN** an unlabelled copy has no free slot under the label rules above
- **THEN** the scan imports the file as its own Media

### Requirement: Content belonging to another Media imports as its own Media

The scan SHALL import the file as its own Media when the file's content already belongs to another
Media.

#### Scenario: Content already belongs to another Media

- **WHEN** a file otherwise eligible to join a Media has content that already belongs to another Media
- **THEN** the scan imports the file as its own Media

### Requirement: A Version copied into another library detaches

A file whose content already belongs to a Media in another library that holds other live files SHALL
become a Media of its own in the scanned library, in the default slot, leaving the other files where
they are.

#### Scenario: A Version copied into another library

- **WHEN** the `4K` Version's file is copied into another library and scanned there
- **THEN** that library gets a Media holding only the copy, and the original Media keeps its other
  Versions in its own library

### Requirement: playbackInfo lists Versions and honours a selection

playbackInfo SHALL report `versions` when the Media has two or more Versions, each with an opaque
`id`, its stored `label`, its primary video codec, its HDR format and whether it was `selected`.
When the request carries a `selectedVersion` that names one of the Media's Versions, the decision
SHALL use that Version even if it needs a transcode. An unknown `selectedVersion` SHALL be ignored.
A single-Version Media's response SHALL NOT carry `versions`.

#### Scenario: Client selects a Version

- **WHEN** a client requests playbackInfo with the `id` of the `4K` Version
- **THEN** the response marks that Version `selected` and its streams play that file

#### Scenario: Unknown selection

- **WHEN** `selectedVersion` names no Version of the Media
- **THEN** the decision is the one made without a selection

### Requirement: Each Version is judged by its own file facts

When a Media has two or more Versions, playback SHALL judge each Version by its own container, named
from its path, and its own video codec, from its stream inventory, not by the Media row's.

#### Scenario: Version in another container

- **WHEN** an MKV-only client selects a Version stored as MP4
- **THEN** that Version is not offered as direct play

### Requirement: The Media row describes its first Version

Frame height is stored once per Media, for its first Version in label order, counting a file marked
missing. A re-import of another Version SHALL NOT replace those facts, and a first Version that joins
without a probe SHALL leave the frame size unknown rather than keep the previous file's. An unprobed
re-import of the first Version's own file SHALL keep its frame size.

#### Scenario: A rescan re-imports a later Version

- **WHEN** a rescan re-imports the `Extended` Version of a Media whose first Version is unlabelled
- **THEN** the row keeps the unlabelled file's frame size, codec and duration

### Requirement: An alternate Version of unknown height is transcoded

When a client caps the height for a Version's video codec, playback SHALL NOT direct play or direct
stream any Version of unknown height, including every Version beside a missing first Version and a
Version left as the only live file of its Media, and SHALL transcode it instead.

#### Scenario: 4K alternate on a 1080p-capped client

- **WHEN** a Media holds a 1080p first Version and a `4K` HEVC Version, and a client capped at
  1080p for HEVC selects the `4K` Version
- **THEN** the decision is a transcode, and the first Version still direct-plays

#### Scenario: First Version's file goes missing

- **WHEN** the 1080p first Version's file is marked missing and the same client plays the `4K`
  Version
- **THEN** the decision is a transcode, not a direct play judged by the missing file's height

#### Scenario: Only one Version is still live

- **WHEN** an unprobed `4K` first Version's only sibling is marked missing and a 1080p-capped client
  plays the item
- **THEN** the decision is a transcode

### Requirement: Byte routes serve the chosen Version

A Stream Token minted for a Media with two or more Versions SHALL carry the chosen file's content
fingerprint, the primary Version's included, and the direct, HLS, asset and sidecar byte routes SHALL
serve that file. A route SHALL refuse the request when the fingerprint names no live file of the
grant's Media, so a token never switches to another Version's bytes.

#### Scenario: Non-primary Version direct-plays

- **WHEN** playbackInfo chooses the `4K` Version and the client fetches the direct URL
- **THEN** the route serves the `4K` file's bytes, not the default Version's

#### Scenario: The chosen file goes missing

- **WHEN** a token was minted for the default Version and that file is then marked missing
- **THEN** the byte routes refuse the token instead of serving another Version

### Requirement: Web player offers a version picker

When playbackInfo reports `versions`, the web player SHALL offer a version menu naming each
Version by its label, or a localized default name for the empty label, and SHALL remember the
choice per profile and item, sending it as `selectedVersion` on the next playback of that item.

#### Scenario: Choice is remembered

- **WHEN** a viewer picks the `Extended` Version and later plays the item again
- **THEN** the request carries the `Extended` Version's id and the player starts that Version

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

#### Scenario: Undo of an older merge that trashed the merged copy

- **WHEN** a duplicate merge trashed a Media, the Media was restored and later merged as a Version,
  and the older duplicate merge is undone while the version merge is not
- **THEN** that undo is refused with a message to undo the version merge first, and the Media stays
  in the trash

### Requirement: Scan folds verified legacy and sampled copies into Versions

After the scan walk, the scanner SHALL find pairs of live `sample1:` and `sha256:` files
with equal nonempty OSHash and equal size on distinct untrashed Media in the same Library.
The scanner SHALL verify the sampled file's full digest against the legacy fingerprint
before attempting an undoable merge. Encrypted Libraries, Libraries scanned with trash
disabled, Extras, Media in a relation, multipart Media, and Media restored by a version
merge undo SHALL be excluded.

#### Scenario: Verified copies in different directories

- **WHEN** the sampled file's full digest matches the legacy fingerprint in the same Library
- **THEN** the scan keeps the sampled Media and its first Version's probe facts
- **AND** both paths and fingerprints are retained as independently playable Versions
- **AND** attaching a later labelled Version keeps a sampled anchor in the default slot
- **AND** the survivor's play count sums both Media's plays and its last-played time is the later of the two
- **AND** each Profile keeps the survivor's position and watched state if it has a progress row, or takes them from the legacy row otherwise

#### Scenario: Verification does not match

- **WHEN** files share an OSHash and size but the sampled file's full digest differs from the legacy fingerprint
- **THEN** the scan does not fold them as copies
- **AND** the files are distinct content, so the default same-title Version rule applies to them as to any other file

#### Scenario: Verification is interrupted by a file read error

- **WHEN** the scanner cannot read a possible copy's full digest
- **THEN** both Media sit out title grouping in that scan, so a later scan can retry verification
- **AND** title grouping still merges the title's other Media, taking at most one store call fewer than their count

#### Scenario: The catalog commits before a playback write fails

- **WHEN** a verified merge's events transaction does not commit
- **THEN** recovery retries the journaled playback contributions individually by Profile and Media pair
- **AND** an engagement erase cancels contributions for the erased Profile and Media in the same transaction

#### Scenario: Undo restores the split

- **WHEN** a user undoes the version merge
- **THEN** both Media and their file assignments are restored
- **AND** later scans keep the restored Media separate
