## ADDED Requirements

### Requirement: Owner-visible credential text never shows the internal ref

The plugin Credential status row and the web provider key label SHALL name a scraper credential by
the owning plugin's display name and the credential's label, or by a generic localized name, and
SHALL NOT show the internal scoped credential ref.

#### Scenario: Plugin Credential status row

- **WHEN** the Console config lists a plugin whose scraper declares a credential
- **THEN** the `credential-` status row's detail is `<plugin display name>: <label>` without the
  `bfplugin-` ref

#### Scenario: Provider key without scraper display names

- **WHEN** the web groups a credential whose scrapers declare no display name
- **THEN** the key row, field label and setup error use a localized generic provider name
