## ADDED Requirements

### Requirement: Scan groups files of one title into Versions

A scan SHALL attach a parsed, non-part, non-image file to the Media of another file from the same
directory and library when both parse to the same class, title, year, sequence and index, storing it
as a Version under the parser's version label. The other file MAY be one this scan imported or one an
earlier scan cataloged. It SHALL import the file as its own Media when that Version slot is already
held by another live file or when the file's content already belongs to another Media.

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

### Requirement: An alternate Version of unknown height is transcoded

Frame height is stored once per Media, for its first Version in label order. When a client caps the
height for a Version's video codec, playback SHALL NOT direct play or direct stream any other
Version, whose height it cannot know, and SHALL transcode it instead.

#### Scenario: 4K alternate on a 1080p-capped client

- **WHEN** a Media holds a 1080p first Version and a `4K` HEVC Version, and a client capped at
  1080p for HEVC selects the `4K` Version
- **THEN** the decision is a transcode, and the first Version still direct-plays

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
