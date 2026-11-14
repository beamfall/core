## ADDED Requirements

### Requirement: Provider keys offered during setup

The first-run setup Plugins step SHALL list each provider key that an enabled plugin's scrapers
need and that is not yet configured, with an optional field for the owner to enter it.

#### Scenario: Owner enters a key

- **WHEN** the owner types a key for a listed provider and continues past the Plugins step
- **THEN** the wizard saves it with `setScraperCredential` for that AuthRef after activating plugins

#### Scenario: Owner skips the key

- **WHEN** the owner leaves every key field blank and continues
- **THEN** setup proceeds to the Library step without saving any credential

#### Scenario: Plugin disabled

- **WHEN** the owner switches off a plugin
- **THEN** the provider keys only that plugin needs are no longer listed
