## ADDED Requirements

### Requirement: The device list tells a signed-out device from an active one

The `DeviceInfo` result SHALL report `signedIn`, true while the device holds an enrolled session it
can use or renew. The Console device list SHALL show an unrevoked device whose `signedIn` is false
as signed out, not active.

#### Scenario: A browser signs out

- **WHEN** an enrolled browser signs out
- **THEN** `devices` reports that device with `revoked: false` and `signedIn: false`
- **AND** the Console device list shows it as "Signed out"

#### Scenario: An expired session can still renew

- **WHEN** a device's enrolled session row has expired but still exists
- **THEN** `devices` reports `signedIn: true`

#### Scenario: Revoked wins

- **WHEN** a device is revoked
- **THEN** the Console device list shows it as revoked whatever `signedIn` says
