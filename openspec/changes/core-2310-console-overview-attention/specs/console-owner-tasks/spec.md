## ADDED Requirements

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

## MODIFIED Requirements

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
