# Libraries in the Console config document

Ticket: CORE-1099 (`queue:core:main`).

## Why

An unattended install (Unraid template, compose file, provisioning script) cannot create
libraries: the config document only carries background workers and plugin activation, and the
online `apply-config` needs a Console session that does not exist before first-run setup. Every
new server therefore needs a manual Console step before anything scans.

## What Changes

- The config document gains an optional `libraries` list of `{title, plugin, roots}` entries.
  The document version stays 1: the field is additive and omitted when empty.
- `apply-config` matches a library by exact (case-sensitive) title plus plugin, creates missing
  libraries through `CreateLibraryWithRoots`, adds missing roots, and never removes a library
  or a root. Re-applying is a no-op.
- `export-config` emits non-restricted libraries sorted by title then plugin, roots sorted, so
  export → apply → export is stable.
- Validation before any write: known plugin, absolute and clean roots, no duplicate roots or
  libraries, non-blank title.
- `export-config` and `apply-config` gain an offline `--data-dir` mode (refuses a live
  `server.lock`, like `reset-admin-password`). Offline-created libraries are granted to the
  owner profile; before first run they are created ungranted and first-run setup grants them.

## Non-goals

- Removing or renaming libraries, or replacing root sets, from a document.
- Restricted libraries in export, library settings beyond title, plugin and roots.
- Triggering an immediate scan; the default schedule and watcher apply as in the Console.
