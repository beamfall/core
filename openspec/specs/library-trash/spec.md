# library-trash Specification

## Purpose
Defines recoverable library deletion: trash, list trashed libraries, restore and purge, with the audit trail and encrypted-library refusal.

## Requirements

### Requirement: Admins can list deleted libraries

The server SHALL offer the admin query `trashedLibraries`, available only inside an open Admin
Session. It SHALL return every soft-deleted Library the caller's scope may see, newest deletion
first, each with its id, title, the time it was deleted, the number of Media items it holds, whether
it can be purged, and the time the retention sweep will purge it. A Library that cannot be purged SHALL
have no purge time. A restricted Library SHALL be listed only for a
revealed scope.

#### Scenario: A deleted library is listed with its item count

- **WHEN** an admin deletes a Library titled "Films" that holds 3 Media items and then queries
  `trashedLibraries`
- **THEN** the result has one entry titled "Films" with a Media count of 3, a deletion time and a
  purge time 30 days after the deletion time

#### Scenario: A live library is not listed

- **WHEN** no Library has been deleted
- **THEN** `trashedLibraries` returns an empty list

### Requirement: Restoring a deleted library brings everything back

The server SHALL offer the admin mutation `restoreLibrary(libraryID)`, available only inside an open
Admin Session. It SHALL clear the soft delete and write a `library.restore` activity record naming
the Library. After it succeeds the Library, its Media, watch history and progress, profile grants,
root folders and scan schedule SHALL be visible again exactly as they were before the delete. It
SHALL refuse a Library that does not exist, is not deleted, or is not visible to the caller's scope.

#### Scenario: Restore returns the library and its media

- **WHEN** an admin deletes "Films", which holds 3 Media items and is granted to a profile, and then
  calls `restoreLibrary` for it
- **THEN** "Films" is listed among libraries again, its 3 Media items are readable, the profile's
  grant is intact, `trashedLibraries` is empty and the activity log has a `library.restore` record

#### Scenario: Restoring a live library is refused

- **WHEN** an admin calls `restoreLibrary` for a Library that is not deleted
- **THEN** the mutation returns an error and nothing changes

### Requirement: Watching and scheduled scans stop at delete and resume at restore

When a Library is deleted the server SHALL stop watching its root folders and SHALL stop running its
scan schedule, without waiting for a restart. When a Library is restored the server SHALL watch its
root folders again, SHALL run its scan schedule again, and SHALL queue a scan of each root folder.

#### Scenario: Delete stops the watcher and the schedule

- **WHEN** an admin deletes a Library whose root folder is being watched
- **THEN** the watcher reports no watched root for that Library and a scheduler tick past the
  schedule's due time queues no scan for it

#### Scenario: Restore resumes both and rescans

- **WHEN** the admin restores that Library
- **THEN** the watcher reports its root folder as watched, its scan schedule is due again and a scan
  of the root folder is queued

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

#### Scenario: An encrypted Library cannot be purged

- **WHEN** an admin calls `purgeLibrary` with `confirm: true` for a deleted encrypted Library, whose
  catalog lives in a separate database that a purge does not remove
- **THEN** the mutation returns an error, the retention sweep leaves it alone, and it stays listed
  as not purgeable

#### Scenario: A Custodial Library cannot be purged

- **WHEN** an admin calls `purgeLibrary` with `confirm: true` for a deleted Custodial Library
- **THEN** the mutation returns an error and the Library is still listed in `trashedLibraries`

### Requirement: A purge removes records and never files

A purge SHALL remove, in one catalog transaction, the Library, its Media and their file records,
external ids, embeddings and artwork candidates, its entities, its profile grants, root-folder
records and scan schedule. It SHALL then remove the watch history, progress and other engagement
records of the removed Media and the server-generated artwork of the removed Media and entities. It
SHALL NOT delete, move or modify any file under the Library's root folders.

#### Scenario: Files on disk survive a purge

- **WHEN** an admin purges "Films", whose root folder holds one media file
- **THEN** the media file is still on disk with the same bytes

#### Scenario: History of the purged media is removed

- **WHEN** an admin purges "Films" after a profile recorded playback progress on one of its Media
- **THEN** that progress record no longer exists

### Requirement: The retention sweep purges long-deleted libraries

The retention sweep SHALL purge every Library deleted longer ago than `TrashRetention`, the same
window used for trashed Media, with the same effect and activity record as `purgeLibrary`. It SHALL
NOT purge a Library that cannot be purged, nor one deleted again after the sweep chose it. While trash is disabled it SHALL purge nothing and SHALL report that
confirmation is required.

#### Scenario: A library past retention is purged

- **WHEN** the sweep runs with a cutoff later than the time "Films" was deleted
- **THEN** "Films" and its Media are removed and a `library.purge` activity record is written

#### Scenario: A recently deleted library is kept

- **WHEN** the sweep runs with a cutoff earlier than the time "Films" was deleted
- **THEN** "Films" is still listed in `trashedLibraries`

#### Scenario: Trash disabled

- **WHEN** trash is disabled and the sweep runs
- **THEN** no Library is purged and the sweep reports that confirmation is required

### Requirement: Console lists recently deleted libraries

Console > Libraries SHALL show a "Recently deleted" group whenever at least one deleted Library
exists, and SHALL NOT show it otherwise. Each entry SHALL show the Library's title, when it was
deleted, its Media count and when it will be removed automatically, and SHALL offer a "Restore"
control and, when the Library can be purged, a "Delete permanently" control. Both controls'
accessible names SHALL include the Library's title.

#### Scenario: Deleted library is listed

- **WHEN** an admin opens Console > Libraries and the server reports "Films" as deleted
- **THEN** a "Recently deleted" group lists "Films" with controls named "Restore Films" and
  "Delete Films permanently"

#### Scenario: Nothing deleted

- **WHEN** the server reports no deleted Library
- **THEN** Console > Libraries shows no "Recently deleted" group

### Requirement: Restore from the Console

Choosing "Restore" SHALL send the named GraphQL operation `RestoreLibrary` for that Library without
a confirmation step, SHALL disable the entry's controls while it runs, and on success SHALL reload
both the Library list and the "Recently deleted" group. On failure it SHALL show the server's error
next to the entry and leave the entry in place.

#### Scenario: Restore succeeds

- **WHEN** an admin chooses "Restore" on "Films" and the server returns true
- **THEN** "Films" appears in the Library list and leaves "Recently deleted"

### Requirement: Permanent delete from the Console is confirmed

Choosing "Delete permanently" SHALL open an alert dialog titled with the Library's title. The dialog
SHALL state that the Library's catalog records, watch history and generated artwork are removed and
cannot be recovered, and that the media files on disk are not deleted or changed. Cancel and Escape
SHALL close it without calling the server.

#### Scenario: Dialog states the consequences

- **WHEN** an admin chooses "Delete permanently" on "Films"
- **THEN** the dialog title names "Films" and the body says the files on disk stay untouched

#### Scenario: Admin cancels

- **WHEN** an admin opens the dialog for "Films" and chooses Cancel
- **THEN** no mutation is sent and "Films" is still listed in "Recently deleted"

### Requirement: Confirmed permanent delete runs once and reports its result

Confirming SHALL send the named GraphQL operation `PurgeLibrary` for that Library with
`confirm: true` and SHALL disable the dialog's buttons while it runs. On success the dialog SHALL
close and the "Recently deleted" group SHALL reload. On failure the dialog SHALL stay open and show
the error.

#### Scenario: Admin confirms a permanent delete

- **WHEN** an admin chooses "Delete permanently" on "Films" and confirms
- **THEN** `PurgeLibrary` is sent once with `confirm: true` and "Films" leaves "Recently deleted"

#### Scenario: Permanent delete fails

- **WHEN** the server returns an error
- **THEN** the dialog stays open and shows the error, and "Films" is still listed

### Requirement: Deleting a library offers Undo

After a Library is deleted from Console > Libraries, the section SHALL announce that it was deleted
and SHALL offer an "Undo" control that sends `RestoreLibrary` for that Library. After a successful
Undo the Library SHALL be listed again and the announcement SHALL be dismissed.

#### Scenario: Undo a delete

- **WHEN** an admin deletes "Films" and then chooses "Undo"
- **THEN** `RestoreLibrary` is sent for "Films" and the reloaded list shows "Films" again
