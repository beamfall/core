# Console delete library

Ticket: CORE-1171 (`queue:core:main`).

## Why

The server has long had the admin mutation `deleteLibrary(libraryID)`, but the web app has no way
to call it. An owner who added a library by mistake cannot remove it.

The mutation is a soft delete. It stamps `library.trashed_at_ms`, which hides the library from every
store query. Media rows, watch history, the scan schedule and profile grants stay in the database,
and nothing on disk is touched. It writes a `library.delete` activity record and needs an open Admin
Session. There is no restore or purge mutation yet, so from the UI the delete cannot be undone, and
the copy must say so instead of promising a trash.

## What Changes

- **Web GraphQL client**: a named `DeleteLibrary` operation calling `deleteLibrary(libraryID)`.
- **Shared component** `components/beamfall/LibraryDelete.tsx`: a danger-styled "Delete library"
  control that opens an alert dialog titled with the library name. The dialog says the library
  disappears on every device, files on disk are untouched, watch history is kept but hidden, and
  there is no restore yet. While the mutation runs both buttons are disabled; on success the dialog
  closes and the list reloads; on failure the dialog stays open, shows the error and allows a retry.
- **Console > Libraries** and **Manage > Libraries** place the control in a footer on each library
  card, apart from Rescan and Fix unmatched.
- **Strings** in en, fr, de, es and pt.

No server, schema or wire-contract change.

## Impact

- `internal/web/app/src/lib/graphql/manage.ts`
- `internal/web/app/src/components/beamfall/LibraryDelete.tsx` (new) and its test
- `internal/web/app/src/areas/console/sections/Libraries.tsx`
- `internal/web/app/src/areas/manage/pages/LibrariesPage.tsx`
- `internal/web/app/src/strings/managed-files.ts` and the strings registry baseline
