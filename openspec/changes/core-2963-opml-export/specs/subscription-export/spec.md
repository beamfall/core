## ADDED Requirements

### Requirement: Manager Subscriptions can be downloaded as OPML

The server SHALL allow a Manager session to download OPML 2.0 for eligible Subscriptions from the
initial list of visible Libraries after excluding sealed Libraries. The export SHALL read every
Subscription from the store and include feed URLs and public titles as RSS outlines. The Manager
Subscriptions page SHALL offer the download and display localized messages for an expired session
and other download failures.

#### Scenario: Export and import into a fresh server

- **WHEN** a Manager downloads the export and imports it into a fresh store
- **THEN** the imported Subscriptions have the same public feed URLs and titles as the export source

#### Scenario: Export only feeds with evidence of being public by default

- **WHEN** a Manager downloads the export without `includePrivate=true`
- **THEN** it includes only Subscriptions with positive evidence of being public: an http(s) feed URL
  with no authentication reference, userinfo, query string or fragment
- **AND** omits every other Subscription as unclassified, without inspecting URL values for secrets

#### Scenario: Include unclassified and authenticated feeds on request

- **WHEN** a Manager downloads the export with `includePrivate=true`
- **THEN** it also includes unclassified Subscriptions and those with an authentication reference
- **AND** each such outline carries the complete feed URL with only userinfo removed, uses the URL as
  its text and omits the stored title, so feeds that differ only in a query parameter import as
  distinct Subscriptions

#### Scenario: Never export credentials or the server's URL

- **WHEN** a Manager requests either download mode
- **THEN** no outline contains an authentication reference or URL userinfo
- **AND** the server skips feeds on its own request host or `BEAMFALL_BASE_URL` host, comparing IP
  literals as addresses so every spelling of one address matches

#### Scenario: Exclude sealed Libraries

- **WHEN** a Manager exports while a visible Library is sealed
- **THEN** the export omits that Library's Subscriptions until the Library is unsealed
- **AND** a Library restored after enumeration is excluded from that download

#### Scenario: Reach the importer's limits

- **WHEN** the next outline would cause the export to exceed 10,000 outlines or 64 MiB
- **THEN** the server returns an error without an attachment
- **AND** a full document exactly at either limit is downloadable and can be imported

#### Scenario: An unauthorized caller requests export

- **WHEN** a caller without a Manager session requests the download
- **THEN** the server denies the request without returning OPML
