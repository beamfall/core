## ADDED Requirements

### Requirement: Online subtitle search is opt-in

The server SHALL make no subtitle provider request unless the operator has enabled that provider.
With no provider enabled, the status route SHALL report search as disabled and the search and
download routes SHALL refuse without any outbound request. Requests SHALL carry no install
identifier.

#### Scenario: Stock server

- **WHEN** a viewer asks for subtitle search on a server with no provider enabled
- **THEN** the server answers that search is not enabled and contacts no provider

#### Scenario: Provider enabled without a key

- **WHEN** a provider is enabled but the operator has not written a key to the secrets file
- **THEN** the status names the provider as needing a key and search makes no request

#### Scenario: Release build carries a bundled key

- **WHEN** a release build injects a credential under the OpenSubtitles key ref
- **THEN** subtitle search ignores it and reports the provider as needing a key

### Requirement: Search matches by file hash and generic metadata

A search SHALL send the provider the media file's OpenSubtitles hash and size, the Media title and
year, the stored `sequence` and `index` ordinals when present, and the requested languages. Each
result SHALL report its provider, language, rating, download count, release name, whether the hash
matched, and its SDH and forced flags.

#### Scenario: Hash match

- **WHEN** the provider returns a result flagged as a hash match
- **THEN** the result reports `hashMatch` true and is listed before results that only matched by
  title

### Requirement: Only a permitted Profile session searches and downloads

The routes SHALL require a Profile session, refusing API tokens and developer keys, and SHALL allow
an adult Profile or a manager device. The Media SHALL be visible to the session; an unknown or
hidden Media SHALL read as not found. Sealed, encrypted and custodial Libraries SHALL be refused.

#### Scenario: Child Profile on a viewer device

- **WHEN** a non-adult Profile on a device without the manager role asks to search
- **THEN** the server refuses with 403

### Requirement: A download becomes a sidecar track without overwriting

A download SHALL be checked as SRT text within the size cap, written beside the media file with a
name the sidecar scan recognises (`<stem>.<lang>[.sdh|.forced].srt`), created exclusively so no
existing file is replaced, and recorded as a sidecar caption row so the track is available at once.
The server SHALL write a `subtitle.download` activity row naming the Media and the provider.

#### Scenario: Name already taken

- **WHEN** `<stem>.en.srt` already exists beside the media file
- **THEN** the download is written as `<stem>.en.2.srt`, the existing file is unchanged, and the
  new track is listed with language `en`

#### Scenario: Not a subtitle

- **WHEN** the provider returns bytes that are not SRT text
- **THEN** nothing is written and the route answers with a provider error
