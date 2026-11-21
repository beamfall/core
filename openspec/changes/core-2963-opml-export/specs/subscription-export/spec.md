## ADDED Requirements

### Requirement: Manager Subscriptions can be downloaded as OPML

The server SHALL allow a Manager session to download OPML 2.0 for all Subscriptions in Libraries
visible to that session. The export SHALL include feed URLs and titles as RSS outlines. The Manager
Subscriptions page SHALL link to the default download.

#### Scenario: Export and import into a fresh server

- **WHEN** a Manager downloads the default export and imports it into a fresh store
- **THEN** the imported Subscriptions have the same public feed URLs and titles as the export source

#### Scenario: Exclude private feeds by default

- **WHEN** a Manager exports without explicitly requesting private Subscriptions
- **THEN** authenticated Subscriptions are omitted and the download contains no credential references, authentication headers, tokens, or server metadata

#### Scenario: Explicitly include private feed URLs

- **WHEN** a Manager exports with `includePrivate=true`
- **THEN** the download may include authenticated Subscriptions using only their feed URLs and titles, and still omits credential references, secrets, and URLs with detectable embedded credentials

#### Scenario: An unauthorized caller requests export

- **WHEN** a caller without a Manager session requests the download
- **THEN** the server denies the request without returning OPML
