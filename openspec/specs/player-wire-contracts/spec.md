# player-wire-contracts Specification

## Purpose
Lets clients and native SDKs tell player-aware controllers apart and decode Player Profile wire fields against shared fixtures, so player rows only reach clients that declare the player-control capability (ADR-0240 D11).

## Requirements

### Requirement: Player rows require the player-control capability

The server SHALL return Player Profile display rows (`playerDisplays`,
`PartySessionInfo.availableDisplays`) only to a connection whose client contract declares
`player-control`. Without it, those listings SHALL be empty, `controlRendererSession` against a
player display SHALL refuse as an unknown session, and `partyScreenAddDisplay` SHALL refuse as an
unknown display.

#### Scenario: An older controller's listing is unchanged

- **WHEN** a controller of a Player Profile queries `playerDisplays` without declaring `player-control`
- **THEN** it receives an empty list, and the same controller declaring `player-control` receives the display

### Requirement: Core owns the player wire fixtures

Core SHALL publish Player Profile wire fixtures at `contracts/player-profile/v1/fixtures.json`
under the `player-profile-wire` registry boundary, and a conformance test SHALL decode each fixture
into the server response types with unknown fields refused and re-encode it to identical JSON.

#### Scenario: A fixture drifts from the server type

- **WHEN** a fixture names a field the server type does not have, or omits one it always emits
- **THEN** the conformance test fails
