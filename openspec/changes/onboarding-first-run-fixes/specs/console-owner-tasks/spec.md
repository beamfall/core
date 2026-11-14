## ADDED Requirements

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
