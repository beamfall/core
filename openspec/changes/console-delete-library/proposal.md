# Delete a library from the web Console

Ticket: CORE-1171 (`queue:core:main`).

## Why

The server has an admin mutation `deleteLibrary(libraryID)`, but the web UI never calls it. An owner
who adds a library by mistake, or retires one, has no way to remove it short of editing the
database.

## What the server does today (verified, unchanged by this change)

- `deleteLibrary` needs an open Admin Session (`roleSession|roleAdmin`); it asks for no further
  step-up.
- It is a soft delete: it stamps `library.trashed_at_ms`. The library and everything in it stop
  appearing in every query at once. Media rows, watch history and progress, the scan schedule and
  profile grants stay in the database.
- It never touches the library's folders or the media files in them.
- It writes a `library.delete` activity record.
- There is no restore or purge mutation. From the UI a delete cannot be undone, so the copy must not
  promise a trash or an undo.

## What Changes

- Every library card in Console > Libraries gets a "Delete library" control in a card footer, apart
  from the everyday actions and in danger styling.
- It opens the app's alert dialog, titled with the library's name. The dialog states what happens:
  the library disappears from Beamfall on every device; the media files on disk are left alone;
  watch history and progress are kept on the server but hidden with the library; Beamfall cannot
  restore a deleted library yet.
- Confirming runs the named GraphQL operation `DeleteLibrary`. The dialog shows progress, keeps
  itself open with the server's error when the call fails, and on success closes and reloads the
  library list.
- Manage > Libraries gets the same control from the same shared component.
- Strings ship in en, fr, de, es and pt.

## Non-goals

- Server or schema changes: a restore or purge mutation, telling the watcher and scan schedule that
  a library was deleted, or a step-up prompt. These are tracked separately.
- Deleting media files from disk.

## Capabilities

### New Capabilities

- `console-library-deletion`: an admin can delete a library from the web UI through a confirmation
  that states the consequences.

### Modified Capabilities

None.
