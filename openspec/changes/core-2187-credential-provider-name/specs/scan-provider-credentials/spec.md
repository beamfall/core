## ADDED Requirements

### Requirement: Scan status names a missing credential by its provider

The completed scan job's message SHALL name each missing provider credential by the owning
plugin's display name and the credential's label, and SHALL say where to add it. It SHALL NOT show
the internal scoped credential ref. A credential no registered plugin declares SHALL be named
generically.

#### Scenario: Plugin with a labelled credential

- **WHEN** a scan ends with a missing credential whose scraper declares a credential label
- **THEN** the job message names `<plugin display name>: <label>` and says to add it in the console
  under Plugins, without the `bfplugin-` ref

#### Scenario: Credential no plugin declares

- **WHEN** a scan ends with a missing credential ref that no registered plugin declares
- **THEN** the job message names it generically and still does not show the ref
