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
