# Library folder picker redesign

Ticket: CORE-1142 (`queue:core:main`).
Branch: `library-folder-picker`, stacked on `admin-findability` (CORE-1131).

## Why

During Unraid dogfooding (2026-11-11) the owner found the folder picker in Console > Libraries
buggy and hard to use:

- The server's `pathPicker` query returns the browsed folder as `entries[0]` and its subfolders in
  `entries[0].children`. The web UI lists `entries` itself, so browsing `/mnt` shows `mnt /mnt`
  under "Folders" (the folder as its own child) and the real subfolders are never shown. There is no
  way to open a subfolder or go up a level.
- "Available roots" and "Folders" are two lists of heavy bordered boxes with no sign of where you
  are.
- The open list sits inside the input row, so it pushes "Add folder" down and to the side.
- "Add as Root" sits next to "Add folder" and reads like the same action. It actually adds the typed
  path to the server's list of browsable locations.

## What Changes

- The shared web path picker (`PathPickerField`, used by Console > Libraries, Manage > Libraries,
  Convert, Rename, Subscriptions and Backup export) becomes a folder browser: a breadcrumb of the
  current location, an Up control, a list of the current folder's subfolders that open on click, and
  one "Use this folder" action for the folder being shown.
- With no folder chosen, the browser starts at "Locations", the server's browsable roots.
- Loading, empty, request-failed, permission-denied, not-visible-to-the-server and
  outside-the-browsable-locations each get their own message. The outside-locations message carries
  the "Allow browsing here" action, which replaces the always-visible "Add as Root" button.
- The browser panel opens below the whole input row, so neighbouring buttons stay in place.
- In Console > Libraries, "Add this folder" in the browser adds the shown folder to the library in
  one step. Typing a path and pressing "Add folder" still works.
- Keyboard: arrow keys move through the folder list, Enter opens, Backspace or Left goes up, Escape
  closes. The list is labelled and location changes are announced to screen readers.

No server change. The `pathPicker` query, the `addPathPickerRoot` mutation and their audit records
are used as they are.
