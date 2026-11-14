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
