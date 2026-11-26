## MODIFIED Requirements

### Requirement: Folder art reaches the outer container Entity

The scan SHALL map a folder `poster`, `folder` or `cover` image in the library's top-level folder
that holds the Media to the poster of the outermost container Entity when wrapping an inner
container, and SHALL resolve it from the Media's directory (or its parent inside the library root for
a disc subfolder) when a sole unordered container holds the Media directly.

#### Scenario: An outer container poster

- **WHEN** `Show/poster.jpg` exists and a Media in `Show/Season 01/` resolves to an ordered
  container Entity inside an outer container Entity
- **THEN** the outer Entity's poster holds the bytes of `poster.jpg`

#### Scenario: Direct container cover in nested or multi-disc folders

- **WHEN** `Artist/Album/cover.jpg` exists beside tracks in `Artist/Album/` or `Artist/Album/CD 1/` and each track resolves directly to that single album container Entity
- **THEN** the album Entity's poster holds the bytes of `cover.jpg` and is locked against lower-precedence writers

#### Scenario: Artist folder art does not leak onto nested albums

- **WHEN** `Artist/poster.jpg` exists and `Artist/Album/01.mp3` resolves directly to an album container Entity without `Artist/Album/cover.jpg`
- **THEN** the album Entity does not receive `Artist/poster.jpg`

#### Scenario: A folder outside the library root

- **WHEN** the library root is `Show/Season 01` and `Show/poster.jpg` exists above it
- **THEN** no container Entity receives that image

## ADDED Requirements

### Requirement: Direct container folder posters reach member Media before identification

For a Media held directly by a single unordered container Entity, folder `poster`, `folder` and
`cover` images in its directory (or its parent inside the library root for a disc subfolder) SHALL
fill `poster` when no file-level poster exists. Media-level local artwork SHALL be applied before
auto-identification so provider artwork never supersedes it.

#### Scenario: Album folder cover across multiple tracks

- **WHEN** an album folder holds multiple audio tracks beside `cover.jpg` and no `<stem>-poster` sidecar
- **THEN** each track belonging directly to that album container imports `cover.jpg` into its locally locked `poster` slot before auto-identification runs

#### Scenario: Folder art in a shared folder without a container

- **WHEN** a folder holds two video files beside `poster.jpg` and neither resolves to a sole unordered container Entity
- **THEN** neither Media takes `poster.jpg` as its own poster

### Requirement: Audio local and embedded posters outrank provider thumb and skip frame grabs

When an audio Media's `poster` holds locally locked or embedded artwork (`APIC` or `covr`),
identification SHALL NOT write provider `thumb` artwork, and local poster import, embedded extraction,
identification, and artwork sweeps SHALL remove any unlocked non-manual provider `thumb` so requests
for `thumb` fall back to `poster`. Poster generation for audio without embedded cover SHALL skip
video frame grabs.

#### Scenario: Embedded picture with no MusicBrainz match

- **WHEN** an audio track with an embedded cover picture (`APIC` or `covr`) and no provider match is scanned and swept
- **THEN** the track receives the embedded cover as its `poster` and the album Entity inherits it

#### Scenario: Folder cover outranks embedded picture and Cover Art Archive

- **WHEN** an album folder contains tracks carrying embedded pictures, a `cover.jpg` or `folder.jpg` sidecar, and matches a provider supplying Cover Art Archive `poster` and `thumb` URLs
- **THEN** both the tracks and the album Entity serve the folder sidecar, no Cover Art Archive artwork is applied to `poster` or `thumb`, and requests for `thumb` fall back to the folder cover

#### Scenario: Audio track without embedded artwork or sidecar

- **WHEN** an audio track with no embedded picture or sidecar is processed by poster generation
- **THEN** no frame grab runs and the sweep records no generation failure
