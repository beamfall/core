# Owner tasks up front

Ticket: CORE-1131 (`queue:core:main`).
Branch: `admin-findability`, stacked on `provider-keys` (CORE-1115).

## Why

During Unraid dogfooding (2026-11-11) the owner could not find how to rescan a library. The web UI
is organised by subsystem (Monitor, Library, Access, Maintain; 19 flat Manage links) rather than by
what an owner comes to do, so common jobs are buried, duplicated or unlabelled.

## Owner jobs, by how often they come up

| # | Job | Where it is today | Problem |
|---|---|---|---|
| 1 | Rescan a library after adding files | Console > Libraries card: "Folder to scan" dropdown + Scan, below the roots editor; also a Scan column in Manage > Libraries | Nothing says "Rescan"; a folder must be picked; no progress or result is shown |
| 2 | See what the server is doing (scan running, done, failed) | Console > Monitor > Operations | Not linked from the library; Overview "Jobs" tile is hard-coded "Ready" |
| 3 | Fix unmatched or wrong matches | Manage > Unmatched (8th of 19 flat links) | Only reachable through Manage; nothing in the Console points at it; no count |
| 4 | Add a library | Console > Libraries top form (title + plugin), then folders in the new card | Unlabelled form; the new library needs Refresh (CORE-1102) |
| 5 | Set metadata provider keys | Console > Plugins, inside each plugin card | Fine once there, but nothing says keys are missing unless you open Plugins |
| 6 | People, devices and sign-in | Console > Access | Reasonable |
| 7 | Remote access, backups, updates, server config | Console > Access / Maintain | Rare; fine where they are |
| — | Get to the Console at all | Typed URL, or two links on an empty home screen | The header has a Manage link for managers but no Console link |

## What Changes

- **Rescan**: every library card (Console > Libraries and Manage > Libraries) gets a primary
  "Rescan library" button in its header. It scans all of the library's folders (the server already
  does this when `scanLibrary` gets an empty root) and shows live status on the card: scanning with
  progress, finished, or failed with the reason. Single-folder scan moves behind "Scan one folder".
- **Console home is the owner's task list**: Overview shows task tiles for jobs 1-6 with live
  status (scans running, keys needed, libraries count) and links, replacing the hard-coded "Ready"
  tiles. "Rescan all libraries" runs from there.
- **Header**: an admin-eligible session sees a "Console" link next to "Manage", plus an activity
  link to Console > Operations showing the running-job count.
- **Auto-scan status**: library cards and the Rescan tile say whether new files are picked up
  automatically (existing watcher status, ADR-0032). Add library and Rescan all sit in the
  Libraries header. These follow organisation patterns seen in Plex (verbs on the thing, page verbs
  in the header, one global activity indicator), not its design.
- **Add a library**: the create form gets a heading and adds the new library to the list without
  Refresh.
- **Unmatched**: each library card and the Console home link to Manage > Unmatched.

## Non-goals

- Restyling every Console/Manage page (CORE-1122 and siblings); reorganising Manage's nav.
- Server or GraphQL changes; per-library job attribution on the server.
- The Viewer/Manage/Console shell switch redesign (CORE-0058).

## Capabilities

### New Capabilities

- `console-owner-tasks`: owner jobs are reachable from the header and Console home, with status.

### Modified Capabilities

None.
