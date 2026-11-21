## ADDED Requirements

### Requirement: Manager Subscriptions can be downloaded as OPML

The server SHALL allow a Manager session to download OPML 2.0 for eligible Subscriptions in visible,
unsealed Libraries. The export SHALL include feed URLs and titles as RSS outlines. The Manager
Subscriptions page SHALL offer the download and display localized messages for an expired session
and other download failures.

#### Scenario: Export and import into a fresh server

- **WHEN** a Manager downloads the export and imports it into a fresh store
- **THEN** the imported Subscriptions have the same public feed URLs and titles as the export source

#### Scenario: Exclude private feeds and URLs that can carry credentials

- **WHEN** a Manager exports from a Library with authenticated Subscriptions or feed URLs containing userinfo, a query string, or a fragment
- **THEN** the server skips those outlines

#### Scenario: Exclude sealed Libraries

- **WHEN** a Manager exports while a visible Library is sealed
- **THEN** the export omits that Library's Subscriptions until the Library is unsealed

#### Scenario: Reach the importer's limits

- **WHEN** the next outline would cause the export to exceed 10,000 outlines or 64 MiB
- **THEN** the server stops adding outlines and returns the complete OPML document within both limits

#### Scenario: An unauthorized caller requests export

- **WHEN** a caller without a Manager session requests the download
- **THEN** the server denies the request without returning OPML
