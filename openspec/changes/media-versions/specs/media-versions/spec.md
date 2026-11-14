## ADDED Requirements

### Requirement: Scan groups files of one title into Versions

A scan SHALL attach a parsed, non-part, non-image file to the Media of an earlier file from the
same directory and library when both parse to the same class, title, year, sequence and index,
storing it as a Version under the parser's version label. It SHALL import the file as its own Media
when that Version slot is already held by another live file or when the file's content already
belongs to another Media.

#### Scenario: Two resolutions of one title

- **WHEN** a directory holds `Night Harbor (2001) - 1080p.mkv` and `Night Harbor (2001) - 4K.mkv`
  and the parser labels them `1080p` and `4K`
- **THEN** the scan creates one Media with two Versions labelled `1080p` and `4K`

#### Scenario: Unlabelled file with a labelled sibling

- **WHEN** a directory holds `Coastline (1998).mkv` and `Coastline (1998) - Extended.mkv`
- **THEN** the scan creates one Media whose default Version is the unlabelled file and whose
  `Extended` Version is the other

#### Scenario: Different titles stay apart

- **WHEN** two files in one directory parse to different titles or years
- **THEN** the scan creates one Media for each

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

### Requirement: Byte routes serve the chosen Version

A Stream Token minted for a Version other than the primary SHALL carry the chosen file's content
fingerprint, and the direct, HLS, asset and sidecar byte routes SHALL serve that file. A route
SHALL refuse the request when the fingerprint names no live file of the grant's Media.

#### Scenario: Non-primary Version direct-plays

- **WHEN** playbackInfo chooses the `4K` Version and the client fetches the direct URL
- **THEN** the route serves the `4K` file's bytes, not the default Version's

### Requirement: Web player offers a version picker

When playbackInfo reports `versions`, the web player SHALL offer a version menu naming each
Version by its label, or a localized default name for the empty label, and SHALL remember the
choice per profile and item, sending it as `selectedVersion` on the next playback of that item.

#### Scenario: Choice is remembered

- **WHEN** a viewer picks the `Extended` Version and later plays the item again
- **THEN** the request carries the `Extended` Version's id and the player starts that Version
