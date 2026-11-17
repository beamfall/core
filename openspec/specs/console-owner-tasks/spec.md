# console-owner-tasks Specification

## Purpose
The Console is organised around the jobs an owner comes to do rather than by subsystem: its home lists owner tasks, it is reachable from the header, server activity and auto-scan status are visible where owners act, and a library rescan takes one click.

## Requirements

### Requirement: One-click library rescan

Every library card in Console > Libraries and Manage > Libraries SHALL offer a "Rescan library"
button in the card header that scans all of the library's folders without asking for a folder, and
SHALL show the scan's state on the card: scanning (with progress when the server reports it),
finished, or failed with the server's reason. Scanning a single folder MAY remain as a secondary
control.

#### Scenario: Rescan from the card

- **WHEN** an admin clicks "Rescan library" on a library with two folders
- **THEN** both folders are queued for scanning and the card shows "Scanning" until the job ends

#### Scenario: Library without folders

- **WHEN** a library has no folders
- **THEN** the Rescan button is disabled and the card says to add a folder first

### Requirement: Console home lists owner tasks

The Console home SHALL show the owner's common tasks (rescan all libraries, add a library, fix
unmatched items, metadata provider keys, people and sign-in) as tiles that link to where each task
is done, with live status taken from the server rather than fixed text. The unmatched tile SHALL
count every unmatched item the Library health report holds, and the provider-keys tile SHALL count
only required keys of plugins a Library uses.

#### Scenario: Provider key missing

- **WHEN** an enabled plugin that a Library uses has a required provider key with status missing
- **THEN** the provider-keys tile shows "Key needed" and links to Console > Plugins

#### Scenario: No Library uses the plugin

- **WHEN** a plugin's required key is missing but no Library uses that plugin
- **THEN** the provider-keys tile does not ask for it

### Requirement: Console reachable from the header

The app header SHALL show a "Console" link to any signed-in adult Profile that can unlock the
Console.

#### Scenario: Manager opens the app

- **WHEN** a manager's browser shows the Viewer
- **THEN** the header has both "Manage" and "Console" links

### Requirement: Server activity visible from the header

The app header SHALL show managers an activity link to Console > Operations that shows how many
jobs are running. It SHALL read the jobs only while an Admin Session is known active; otherwise the
count is unknown and the jobs read is not sent.

#### Scenario: A scan is running

- **WHEN** a manager with an active Admin Session is on any page while a scan job runs
- **THEN** the header activity link reads "1 job running" and opens Console > Operations

#### Scenario: Console locked

- **WHEN** no Admin Session is active
- **THEN** the header sends no jobs read and shows no running count

### Requirement: Auto-scan status shown where owners rescan

Console > Libraries cards and the Console home Rescan tile SHALL show whether new files are picked
up automatically, from the existing watcher status.

#### Scenario: Library folder is watched

- **WHEN** a library's watcher state is "watching"
- **THEN** its card says "Picks up new files automatically" next to Rescan library

### Requirement: Library folder and first-scan health are reported

The Console SHALL NOT mark the Library step of the first-value checklist done until the catalog has
browsable content that a completed scan brought in or that came with no scan on record, and its
Refresh SHALL reload everything the checklist shows. A Library whose folder is missing or unreadable SHALL be
marked on its card and listed on Overview as needing attention, with the checklist first until first
value is reached. A failure to check a folder (offline, locked) SHALL report nothing.

#### Scenario: Folder removed after creation

- **WHEN** a Library's folder no longer exists
- **THEN** Overview lists it under needing attention and its card shows "Folder not found"

#### Scenario: Library with no successful scan

- **WHEN** a Library exists but no scan has added an item
- **THEN** the checklist step is not Done

#### Scenario: Scan added items but did not complete

- **WHEN** a scan added items to the catalog and then failed or was canceled, and no scan that
  brought files in has completed (an earlier completed scan that found nothing does not count)
- **THEN** the checklist step is not Done and the checklist does not say setup is finished

#### Scenario: Content with no scan on record

- **WHEN** the catalog has browsable content and the job list holds no scan
- **THEN** the checklist step is Done

#### Scenario: Scan finishes after the checklist loaded

- **WHEN** a scan completes with files after the checklist was shown and the owner presses Refresh
- **THEN** the checklist step shows Done

### Requirement: The Add a device panel says how to use the code

After minting a pairing code, the Devices panel SHALL show the role it grants, a countdown to its
expiry, the address to open, and a copyable link that carries the code in the URL fragment, never the
query string. Opening the link SHALL prefill the code, clear it from the address bar and SHALL NOT
submit it. An expired code SHALL be replaced by a message to make a new one.

#### Scenario: Copy link

- **WHEN** the owner copies the link for a minted code
- **THEN** the link is the enroll address followed by `#code=` and the code, with no query string

#### Scenario: Clipboard unavailable

- **WHEN** the owner copies the link where the browser's clipboard is unavailable or refuses, such as
  a plain-HTTP LAN address
- **THEN** the full link, code fragment included, is shown selected in a read-only field with a hint
  to copy it

#### Scenario: Link opened

- **WHEN** the enroll page loads with a `#code=` fragment
- **THEN** the code field is filled, the fragment is removed from the address bar and nothing is
  submitted

### Requirement: Overview attention reflects server state

The Console Overview "Needs attention" list SHALL include every unavailable Library folder (linking
to that Library's folders), every failed job that no later run of the same kind and folder finished
cleanly (with its error, credentials and URLs redacted), each Library health dimension the owner
acts on with a non-zero total over all Libraries, and a backup key that is not saved yet. It SHALL
say nothing needs attention only when none of these apply.

#### Scenario: A scan of a Library folder failed

- **WHEN** the newest scan of a Library folder failed
- **THEN** the Overview lists it with its error and links to that Library's folders on the
  Libraries page, which opens that Library's card

#### Scenario: Library health has problems

- **WHEN** two Libraries report 3 and 2 unmatched items
- **THEN** the Overview lists "Unmatched: 5 items" linking to the unmatched queue

### Requirement: First-value step follows scans in progress

The Console first-value step SHALL show a running scan as Scanning, SHALL NOT say there is nothing
to browse while items are browsable or a scan runs, and SHALL count browsable content as landed when
no scan on record stopped after bringing files in.

#### Scenario: The only scan on record failed at once

- **WHEN** items are browsable and the only scan on record failed before finding files
- **THEN** the first-scan step is Done

#### Scenario: A scan is running

- **WHEN** a scan is running
- **THEN** the first-scan step shows Scanning and the footer says a scan is running
