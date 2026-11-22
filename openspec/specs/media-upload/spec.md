# media-upload Specification

## Purpose
Let an admin upload media files from the web client in resumable chunks, review a proposed placement for each, and commit them into a Library folder for the scan pipeline without silently overwriting anything.

## Requirements

### Requirement: Upload routes are admin-only

The server SHALL accept upload routes only from an admin session that sends the CSRF header, and
SHALL refuse API tokens and API keys on every upload route.

#### Scenario: Not an admin

- **WHEN** a non-admin session, an API token or an API key calls an upload route
- **THEN** the server refuses it with 403 and writes nothing

### Requirement: Uploads are chunked and resumable

An upload session SHALL name a visible library, a file name that is one safe path segment, a
declared size and an optional relative folder of safe segments. Bytes SHALL be accepted only at the
session's current offset, written to a server-owned staging area outside every library root, and
never beyond the declared size.

#### Scenario: Resume after an interrupted chunk

- **WHEN** a chunk upload is interrupted and the client asks for the session status
- **THEN** the server reports the offset of the bytes it holds and accepts the next chunk only at
  that offset

### Requirement: Uploads are size-bounded

The server SHALL refuse a session over the upload size cap, beyond the free space on the staging
filesystem less a safety margin, or beyond the cap on open sessions. Sessions idle for 24 hours
SHALL be removed.

#### Scenario: Too large

- **WHEN** a session declares a size over the cap or beyond the free space less the margin
- **THEN** the server refuses to create it

### Requirement: Each upload gets a proposed assignment

For a complete upload the server SHALL use the library's plugin filename parser to propose the
existing Entity in the library the file joins, with the plugin's display label for its kind, a
destination folder under a library root, and the parse a rescan would see at that path. Core SHALL
NOT name domain kinds itself.

#### Scenario: Episode of a series already in the library

- **WHEN** an uploaded file parses to a title and sequence matching an Entity whose member files
  share a folder for that sequence
- **THEN** the proposal names that Entity and that folder

#### Scenario: No match

- **WHEN** no Entity in the library matches the parsed title
- **THEN** the proposal has no Entity and its destination is the uploaded relative folder, or a
  folder named from the parsed title

### Requirement: Commit places files for the scan pipeline

Committing an upload SHALL place the staged file under the chosen destination inside a visible
library root, creating missing folders, with a no-replace admission that never exposes a partial
file, and SHALL then enqueue the library scan so the file is cataloged by the standard pipeline and
keeps its place across rescans.

#### Scenario: Destination outside the library

- **WHEN** a commit names a destination outside every visible library root, or inside the managed
  trash
- **THEN** the server refuses it and the staged upload stays resumable

### Requirement: Conflicts never overwrite silently

On a name conflict the server SHALL keep both files by renaming the new one, unless the admin chose
Replace, which SHALL move the existing file to the managed trash first. Commits and replaces SHALL
be recorded in the activity log.

#### Scenario: Keep both

- **WHEN** the destination already holds a file with the uploaded name and the admin keeps both
- **THEN** the new file lands under a suffixed name and the existing file is untouched

#### Scenario: Replace

- **WHEN** the admin chooses Replace for a conflicting file
- **THEN** the existing file moves to the managed trash, is restorable, and the activity log
  records the delete and the new file
