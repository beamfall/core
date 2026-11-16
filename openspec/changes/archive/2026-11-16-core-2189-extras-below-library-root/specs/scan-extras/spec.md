## ADDED Requirements

### Requirement: Extras folder conventions are bounded by the Library root

The scan SHALL treat a file as an Extra by folder convention only when a folder strictly below the
Library root, between the root and the file, has an Extras convention name. The Library root and
the folders above it SHALL NOT count. The scan, its Extra linking and the first-scan estimate
SHALL apply the same rule.

#### Scenario: Library stored under a folder named Trailers

- **WHEN** a Library rooted at `<parent>/Trailers/Films` is scanned
- **THEN** its files import as main Media and appear in browse

#### Scenario: Extras folder inside the Library

- **WHEN** the same Library holds `<title>/Extras/<clip>`
- **THEN** that clip is an Extra linked to the main Media in `<title>`, and the estimate does not
  count it

### Requirement: A subfolder scan and a rescan use the configured Library root

The Library root that bounds the Extras folder conventions SHALL be the configured root of the
Library that holds the scanned folder, also when a scan targets a subfolder. A scan SHALL set the
stored Extra flag of each file it imports from this rule, clearing a flag that no longer applies.

#### Scenario: Scan of a subfolder inside the Library

- **WHEN** a Library rooted at `<root>` is scanned only at `<root>/<title>/Extras`
- **THEN** a new file in that folder imports as an Extra, not as main Media

#### Scenario: Rescan repairs a stale Extra flag

- **WHEN** a file was stored as an Extra because of a folder at or above its Library root, and the
  Library is scanned again
- **THEN** the file is main Media and appears in browse
