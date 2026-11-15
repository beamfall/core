## ADDED Requirements

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
