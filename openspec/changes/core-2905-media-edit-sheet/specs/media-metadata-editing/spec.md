## ADDED Requirements

### Requirement: Provider posters outrank covers embedded in video files

For a video Media, identification and metadata refresh SHALL apply the provider's poster over a
poster that came from a cover embedded in the media file, unless the poster slot is locked by a
manual pick or by local sidecar artwork. An embedded cover SHALL remain the poster fallback for a
video Media with no provider poster, ahead of a grabbed frame. For audio and image Media an
embedded cover SHALL keep outranking the provider poster.

#### Scenario: Release cover in a film file

- **WHEN** a video Media's poster was extracted from its file's embedded cover, the slot is not
  locked, and identification returns a provider poster
- **THEN** the provider poster replaces it and its candidate is marked applied

#### Scenario: Album cover in an audio file

- **WHEN** an audio Media has an embedded cover and identification returns a provider poster
- **THEN** the embedded cover stays

#### Scenario: Locked slot

- **WHEN** a video Media's poster slot is locked manually or by a sidecar
- **THEN** identification keeps the existing poster

### Requirement: Manual artwork picks lock their slot

Pinning an artwork candidate or uploading an image for a Media slot SHALL be a manual pick: it
SHALL replace artwork that local sidecar artwork locked, and the server SHALL set the slot's manual
lock (`artworkPoster`, `artworkBackdrop`, `artworkThumb`) in the same request, so a later refresh
or identification keeps it.

#### Scenario: Pin then refresh

- **WHEN** a manager pins a poster candidate and then refreshes the Media's metadata
- **THEN** `artworkPoster` reads locked and the pinned poster stays

#### Scenario: Upload over a sidecar

- **WHEN** a manager uploads a poster for a Media whose poster came from a locked sidecar
- **THEN** the upload replaces it and `artworkPoster` reads locked

### Requirement: Edit details from the media detail page

The web media detail page SHALL offer an Edit details action to a manager device on an adult
profile, and to no one else. It SHALL open an editor for title, sort title, year, summary and the
fields the Media's plugin declares, and for each artwork slot in the fixed vocabulary it SHALL show
the current image, the provider candidates and an upload control.

#### Scenario: Viewer device

- **WHEN** a viewer-role device opens a media detail page
- **THEN** the More menu has no Edit details item

### Requirement: Edits lock and can revert to the provider

A field saved in the editor SHALL be locked. A locked field or artwork slot SHALL offer Revert to
provider, which clears the lock and, when the Media has an identification, refreshes from it.

#### Scenario: Edit and rescan

- **WHEN** a manager edits the title, picks another poster and then refreshes metadata
- **THEN** the edited title and the picked poster remain

#### Scenario: Revert

- **WHEN** a manager chooses Revert to provider on a locked title
- **THEN** the title unlocks and takes the provider's value from the refresh
