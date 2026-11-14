# pairing-viewer-codes Specification

## Purpose
Let the operator switch off Viewer-role pairing codes, refusing both new and outstanding ones, without affecting Manager pairing, Manager promotion or demo enrollment.

## Requirements

### Requirement: Operator switch for Viewer pairing codes

The server SHALL persist an operator setting that disables Viewer-role pairing codes, off by
default. While it is on, the server SHALL refuse to mint a Viewer-role pairing code and SHALL
refuse to redeem an outstanding Viewer-role code with the same error as an invalid code. Manager
pairing, Manager promotion and demo enrollment SHALL be unaffected. Each refusal SHALL be recorded
in the activity log.

#### Scenario: Minting while disabled

- **WHEN** the switch is on and an operator requests a Viewer pairing code
- **THEN** the request is refused as forbidden, no code is stored, and an `enroll.denied` entry
  records the refused mint

#### Scenario: Redeeming a code minted before the switch

- **WHEN** a Viewer code was minted, then the switch was turned on, and a device redeems the code
- **THEN** enrollment fails with the invalid-code error, no device is created, and an
  `enroll.denied` entry records the refused redemption

#### Scenario: Manager pairing while disabled

- **WHEN** the switch is on and an operator mints and a device redeems a Manager code
- **THEN** the device enrolls with the Manager role
