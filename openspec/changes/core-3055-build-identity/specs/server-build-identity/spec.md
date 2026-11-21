## ADDED Requirements

### Requirement: Core reports its build version and date

Core builds SHALL carry a version and report a build date as a second-precision UTC RFC3339
timestamp when available. `beamfall version` and the capabilities object in `GET /health`
SHALL expose that identity. The health response SHALL retain the enabled capability IDs in
the report and provide the build identity even when the server is not ready.

#### Scenario: Reproducible release timestamp

- **WHEN** a release build has `SOURCE_DATE_EPOCH` set
- **THEN** the release uses that timestamp as its build date
- **AND** a release built without `SOURCE_DATE_EPOCH` uses the release commit's committer time

#### Scenario: Source timestamp fallback

- **WHEN** a build has no valid linked build date
- **THEN** Core uses the VCS commit time if present and valid
- **AND** Core omits the date if neither timestamp is available

### Requirement: The shared web top bar identifies the running Core build

The shared web top bar SHALL show the Core version and localized build date at desktop and
tablet widths. It SHALL show a concise development label with a short commit when available.
The accessible label and tooltip SHALL provide the full build details that are available.

#### Scenario: The build date is unavailable

- **WHEN** the health response provides a version and no build date
- **THEN** the top bar shows the version without a date

#### Scenario: The phone layout is active

- **WHEN** the viewport uses the phone layout
- **THEN** the top bar hides the build label
