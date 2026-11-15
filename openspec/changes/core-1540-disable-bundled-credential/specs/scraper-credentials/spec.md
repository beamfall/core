## MODIFIED Requirements

### Requirement: Official builds may carry default scraper credentials

The server SHALL resolve a scraper AuthRef from the owner's saved secret when one exists, and
otherwise from a credential bundled into the build at link time, unless the owner disabled that
AuthRef. A build with no bundled credential for an AuthRef SHALL behave as before.

#### Scenario: No owner key

- **WHEN** an official build carries a bundled credential for an AuthRef and the owner saved none
- **THEN** the scraper uses the bundled credential and the Console reports it as configured

#### Scenario: Owner key overrides

- **WHEN** the owner saves a secret under that AuthRef
- **THEN** the scraper uses the owner's secret, and clearing it restores the bundled credential

#### Scenario: Owner disables a bundled credential

- **WHEN** the owner disables an AuthRef that has a bundled credential, through the audited
  disable mutation
- **THEN** the scraper sends no request with the bundled credential, the Console reports the
  AuthRef as disabled and says Disable does not fall back to a built-in key, and the opt-out
  holds across restarts

#### Scenario: Owner re-enables with a key

- **WHEN** the owner saves a secret under a disabled AuthRef
- **THEN** the opt-out is lifted and the scraper uses the owner's secret

#### Scenario: Source build

- **WHEN** the build carries no bundled credentials
- **THEN** an AuthRef with no saved secret stays unconfigured
