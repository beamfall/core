# Restore or permanently remove a deleted library

Ticket: CORE-1201 (`queue:core:main`).

## Why

Hard invariant 5 says destruction is recoverable by default. `deleteLibrary` breaks it: the delete
is a soft delete on the server, but no client or CLI surface can list a deleted library, bring it
back, or remove it for good. An owner who deletes the wrong library has lost it; an owner who meant
it keeps every catalog row, watch-history row and piece of generated artwork forever.

## What the server does today (verified)

- `deleteLibrary(libraryID)` stamps `library.trashed_at_ms` and writes a `library.delete` activity
  record. Nothing else changes: media rows, entities, watch history and progress, profile grants,
  root folders and the scan schedule row all stay.
- Nothing tells the folder watcher or the scheduler. Until the server restarts, the deleted
  library's folders are still watched and its scan schedule still fires, so scans keep importing
  into a library nobody can see. After a restart the schedule row is marked orphaned and the
  folders are no longer watched.
- Trashed Media already has the full lifecycle: `trashList`, `restoreMedia`, `purgeTrash`, and a
  retention sweep that purges after `TrashRetention` (30 days), is skipped while trash is disabled,
  and never purges Media of a Custodial Library because the server holds its only copy.

## What Changes

Server:

- New admin query `trashedLibraries`: each deleted library with its title, when it was deleted, how
  many Media items it holds, and when it will be removed automatically.
- New admin mutation `restoreLibrary(libraryID)`: clears the soft delete and writes a
  `library.restore` activity record. Media, watch history, progress, grants, root folders and the
  schedule were never removed, so they are back at once. The server then watches the library's
  folders again, re-arms its scan schedule and queues a scan of every root folder, so anything
  that changed on disk while the library was deleted is picked up.
- New admin mutation `purgeLibrary(libraryID, confirm: true)`: permanently removes a library that
  is already deleted, and reports what it removed. It writes a `library.purge` activity record. It
  removes catalog and history records and server-generated artwork only; it never deletes, moves or
  changes a file in the library's folders. It refuses a library that is not deleted, a call without
  `confirm: true`, and a Custodial Library.
- The retention sweep purges libraries deleted longer than `TrashRetention`, under the same rules
  as trashed Media: skipped while trash is disabled, never a Custodial Library.
- `deleteLibrary` now stops the library's folder watcher and scan schedule immediately.

Web Console:

- Console > Libraries gets a "Recently deleted" group listing deleted libraries, each with Restore
  and "Delete permanently".
- "Delete permanently" asks for confirmation in an alert dialog titled with the library's name. The
  dialog says what is removed and that the files on disk are untouched.
- After a library is deleted, the Libraries section confirms it and offers Undo, which restores it.
- The delete dialog no longer says Beamfall cannot restore a deleted library. It says where and
  for how long it can be restored.
- Strings ship in en, fr, de, es and pt.

## Decisions

- **Purge is two steps.** `purgeLibrary` only accepts a library that is already deleted, so no
  single call can go from a live library to an unrecoverable one.
- **A restored library is rescanned.** The folders were not watched while it was deleted, so the
  catalog may be stale. A scan is cheap when nothing changed and correct when something did.
- **Custodial Libraries are never purged**, by the sweep or by hand. The server holds the only copy
  of their bytes, and this change adds no path that deletes media bytes. They stay restorable
  until an owner restores them.
- **No migration.** `library.trashed_at_ms` already exists, and the purge deletes rows in
  dependency order inside one transaction.

## Non-goals

- Deleting media files from disk, or removing a Custodial Library's stored bytes.
- A CLI command for listing, restoring or purging deleted libraries.
- Cancelling scan jobs that were already queued for a library at the moment it is deleted. They run
  to completion against a library nobody can see, and restore makes their results visible.
- Stopping the watcher for a root folder removed from a live library (CORE-0699).
- A step-up prompt on purge beyond the open Admin Session and `confirm: true`.

## Capabilities

### New Capabilities

- `library-trash`: a deleted library can be listed, restored, purged by an admin and purged by the
  retention sweep; its watcher and scan schedule stop at delete and resume at restore; the web
  Console exposes all of it.

### Modified Capabilities

- `web-library-delete`: the confirmation states that the library can be restored and where,
  instead of stating that it cannot be restored.
