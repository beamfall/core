## MODIFIED Requirements

### Requirement: Provider keys in plugin cards

Console > Plugins SHALL show each plugin's scraper credentials, grouped by AuthRef, inside that
plugin's card under a human label derived from the plugin's scraper display names, with a status
chip and save, validate and disable controls. The raw AuthRef MAY appear only as secondary detail.
A credential the server reports as not required SHALL show "Optional" while unset, and a missing
required key SHALL be called out only when a Library uses the plugin.

#### Scenario: Missing key is visible without scrolling

- **WHEN** a plugin that a Library uses has a required credential whose status is missing
- **THEN** its card header shows a "key needed" chip and the key field is shown without expanding

#### Scenario: Label from plugin data

- **WHEN** two scrapers sharing an AuthRef are named "TMDB (Movies)" and "TMDB (TV)"
- **THEN** the provider key is labelled "TMDB"

#### Scenario: Optional key

- **WHEN** a scraper's credential is unset and the server reports it not required, because the
  plugin's embedded default key covers the same capability
- **THEN** its chip reads "Optional" and no "key needed" chip is shown

#### Scenario: Plugin without a Library

- **WHEN** no Library uses a plugin whose required key is missing
- **THEN** its card shows no "key needed" chip
