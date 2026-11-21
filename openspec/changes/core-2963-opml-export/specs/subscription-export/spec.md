## ADDED Requirements

### Requirement: Manager Subscriptions can be downloaded as OPML

The server SHALL allow a Manager session to download OPML 2.0 for eligible Subscriptions from the
initial list of visible Libraries after excluding sealed Libraries. The export SHALL include feed URLs
and public titles as RSS outlines. The Manager
Subscriptions page SHALL offer the download and display localized messages for an expired session
and other download failures.

#### Scenario: Export and import into a fresh server

- **WHEN** a Manager downloads the export and imports it into a fresh store
- **THEN** the imported Subscriptions have the same public feed URLs and titles as the export source

#### Scenario: Require positive evidence that a feed URL is shareable

- **WHEN** a Manager requests either download mode
- **THEN** the server only exports URLs explicitly approved as shareable in `BEAMFALL_SUBSCRIPTION_EXPORT_URLS`, a whitespace-separated list of exact, credential-free feed URLs
- **AND** the server excludes URLs containing userinfo, a query string, or a fragment and URLs on the server's own host

#### Scenario: Include a private feed by request

- **WHEN** a Subscription uses header authentication and its URL is shareable
- **THEN** the server skips it by default and includes it only with `includePrivate=true`
- **AND** the private outline uses the URL as its text and omits the stored title, authentication reference, and credentials

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
