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
is done, with live status taken from the server rather than fixed text.

#### Scenario: Provider key missing

- **WHEN** an enabled plugin has a required provider key with status missing
- **THEN** the provider-keys tile shows "Key needed" and links to Console > Plugins

### Requirement: Console reachable from the header

The app header SHALL show a "Console" link to any signed-in adult Profile that can unlock the
Console.

#### Scenario: Manager opens the app

- **WHEN** a manager's browser shows the Viewer
- **THEN** the header has both "Manage" and "Console" links

### Requirement: Server activity visible from the header

The app header SHALL show managers an activity link to Console > Operations that shows how many
jobs are running.

#### Scenario: A scan is running

- **WHEN** a manager is on any page while a scan job runs
- **THEN** the header activity link reads "1 job running" and opens Console > Operations

### Requirement: Auto-scan status shown where owners rescan

Console > Libraries cards and the Console home Rescan tile SHALL show whether new files are picked
up automatically, from the existing watcher status.

#### Scenario: Library folder is watched

- **WHEN** a library's watcher state is "watching"
- **THEN** its card says "Picks up new files automatically" next to Rescan library
