# scraper-credentials Specification

## Purpose
Defines how official builds carry default scraper credentials and how a scraper login holds more than one secret.

## Requirements

### Requirement: Official builds may carry default scraper credentials

The server SHALL resolve a scraper AuthRef from the owner's saved secret when one exists, and
otherwise from a credential bundled into the build at link time. A build with no bundled
credential for an AuthRef SHALL behave as before.

#### Scenario: No owner key

- **WHEN** an official build carries a bundled credential for an AuthRef and the owner saved none
- **THEN** the scraper uses the bundled credential and the Console reports it as configured

#### Scenario: Owner key overrides

- **WHEN** the owner saves a secret under that AuthRef
- **THEN** the scraper uses the owner's secret, and clearing it restores the bundled credential

#### Scenario: Source build

- **WHEN** the build carries no bundled credentials
- **THEN** an AuthRef with no saved secret stays unconfigured

### Requirement: A scraper login may carry more than one secret

A login-bearer exchange SHALL send its `SecretField` secret and every secret named by its
`SecretFields`, each resolved like the scraper's AuthRef, and SHALL fail without a request when any
of them is missing. A cached login token SHALL be dropped when any credential it used is
invalidated.

#### Scenario: Project key beside a user secret

- **WHEN** a login declares `SecretField` "pin" and `SecretFields` {"apikey": "project-key"}
- **THEN** the exchange posts both values in one JSON body

#### Scenario: Missing second secret

- **WHEN** the secret named in `SecretFields` cannot be resolved
- **THEN** the exchange fails and no login request is sent

#### Scenario: Rotating either secret

- **WHEN** the owner rotates the project key or the user secret
- **THEN** the next scrape runs the login exchange again

### Requirement: The Libraries page shows and sets missing provider keys

The console Libraries page SHALL list each provider key that an active plugin requires and that is
not configured, naming the provider and the hosts the key is sent to, and SHALL let the owner set
the key through `setScraperCredential`. The secret field SHALL be write-only and SHALL never show a
stored value.

#### Scenario: Key missing

- **WHEN** an active plugin requires a key that is not configured and the owner opens Libraries
- **THEN** the page names the provider and offers a password field to set the key

#### Scenario: Key set

- **WHEN** the owner saves a key from the Libraries page
- **THEN** the row reports the key as configured, the field is empty, and the stored value is
  never shown

#### Scenario: Key cleared

- **WHEN** the owner disables a key that was set on the same visit
- **THEN** the row reports the key as missing again and offers the field to set it

#### Scenario: Nothing missing

- **WHEN** every required key of the active plugins is configured
- **THEN** the Libraries page shows no provider-key panel

### Requirement: A provider key changed on the Libraries page stays for the visit

A row the owner changed on the Libraries page SHALL stay for the rest of the visit, showing the
server-reported status, and SHALL offer Validate and Disable once a key is stored.

#### Scenario: Cleared on the same visit

- **WHEN** the owner sets a key and then disables it before leaving the page
- **THEN** the row stays and reports the key as missing

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
