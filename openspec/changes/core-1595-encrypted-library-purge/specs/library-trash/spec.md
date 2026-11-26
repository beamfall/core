## MODIFIED Requirements

### Requirement: An admin can permanently remove a deleted library

The server SHALL offer the admin mutation `purgeLibrary(libraryID, confirm)`, available only inside
an open Admin Session. It SHALL refuse unless `confirm` is `true`, unless the Library is already
deleted, and when the Library cannot be purged. When it succeeds it SHALL write a `library.purge`
activity record and SHALL return the Library's id and title and the number of Media items and
entities removed.

#### Scenario: Purge reports what it removed

- **WHEN** an admin deletes "Films", which holds 1 Media item, and then calls `purgeLibrary` for it
  with `confirm: true`
- **THEN** the result names "Films" and reports 1 Media item removed, "Films" is in neither
  `libraries` nor `trashedLibraries`, and the activity log has a `library.purge` record

#### Scenario: Purge without confirmation is refused

- **WHEN** an admin calls `purgeLibrary` for a deleted Library without `confirm: true`
- **THEN** the mutation returns an error and the Library is still listed in `trashedLibraries`

#### Scenario: A live library cannot be purged

- **WHEN** an admin calls `purgeLibrary` with `confirm: true` for a Library that is not deleted
- **THEN** the mutation returns an error and the Library and its Media are unchanged

#### Scenario: An encrypted Library can be purged

- **WHEN** an admin calls `purgeLibrary` with `confirm: true` for a deleted encrypted Library
- **THEN** the Library is in neither `libraries` nor `trashedLibraries`, its private database files
  and key material no longer exist, and the activity log has a `library.purge` record

#### Scenario: An encrypted Library cannot be purged

- **WHEN** an admin calls `purgeLibrary` with `confirm: true` for a deleted encrypted Library while
  the server cannot erase its key material
- **THEN** the mutation returns an error, nothing is removed, and the Library is still listed in
  `trashedLibraries` and can be restored

#### Scenario: A Custodial Library cannot be purged

- **WHEN** an admin calls `purgeLibrary` with `confirm: true` for a deleted Custodial Library
- **THEN** the mutation returns an error and the Library is still listed in `trashedLibraries`

### Requirement: A purge removes records and never files

A purge SHALL remove, in one catalog transaction, the Library, its Media and their file records,
external ids, embeddings and artwork candidates, its entities, its profile grants, root-folder
records and scan schedule. It SHALL then remove the engagement records and server-generated artwork
of the removed Media and entities. It SHALL NOT delete, move or modify any file under the Library's
root folders.

#### Scenario: An encrypted Library's private database is purged after the commit

- **WHEN** an encrypted Library is purged
- **THEN** nothing is removed before the catalog transaction commits, so a failure up to the commit
  leaves the Library restorable
- **AND** after the commit the plaintext working copy with its WAL and SHM files, the ciphertext and
  the pre-migration backups are removed and the Library's key material is destroyed
- **AND** a step that fails is retried by the next retention sweep or server start until all have
  succeeded, so the Library is never left half deleted

#### Scenario: Files on disk survive a purge

- **WHEN** an admin purges "Films", whose root folder holds one media file
- **THEN** the media file is still on disk with the same bytes

#### Scenario: History of the purged media is removed

- **WHEN** an admin purges "Films" after a profile recorded playback progress on one of its Media
- **THEN** that progress record no longer exists

#### Scenario: An interrupted encrypted purge is finished later

- **WHEN** the purge of a deleted encrypted Library commits but removing its private database or
  its keys fails
- **THEN** the Library stays purged and the next retention sweep or server start removes what is
  left
