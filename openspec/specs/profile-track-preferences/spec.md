# profile-track-preferences Specification

## Purpose
Let a Profile set its own audio and subtitle track preferences without needing an Admin Session.

## Requirements

### Requirement: A Profile writes its own track preferences without an Admin Session

The server SHALL accept an `updateProfile` write from an enrolled device session that holds no
Admin Session when the `profileID` names the session's own Profile and the write sets only
`audioLanguages`, `subtitleLanguages`, `subtitleMode` or `preferSDH`. Any other `updateProfile`
argument, and any write to another Profile, SHALL still require an Admin Session.

#### Scenario: Own track preferences follow the Profile to another device

- **WHEN** a session without an Admin Session writes its own Profile's audio languages, subtitle
  languages, subtitle mode and SDH preference through `updateProfile`
- **THEN** the write succeeds
- **AND** `currentSession` on another device's session for that Profile returns the written values

#### Scenario: Another policy field stays Console-only

- **WHEN** a session without an Admin Session writes its own Profile's name, kind, disabled flag,
  stream cap, transcode gate or entry PIN, alone or together with track preferences
- **THEN** the reply is `admin session required`
- **AND** the Profile is unchanged

#### Scenario: Another Profile's preferences stay Console-only

- **WHEN** a session without an Admin Session writes another Profile's track preferences, directly
  or in a second `updateProfile` occurrence of the same operation
- **THEN** the reply is `admin session required`
- **AND** neither Profile changes

#### Scenario: A renamed variable binding is the same write

- **WHEN** a session without an Admin Session binds its own Profile ID through a variable of another
  name (`profileID: $id`) and sets only track preferences
- **THEN** the write succeeds, because authorization and the resolver read the same effective
  arguments

#### Scenario: A self-service write keeps a concurrent Admin change

- **WHEN** a Profile's own track preference write and an Admin change to its kind, disabled flag or
  stream cap race
- **THEN** both writes survive, because each write updates only the columns it supplies

#### Scenario: A self-service reply hides restricted Library grants

- **WHEN** an unrevealed session writes its own track preferences and its Profile holds a restricted
  Library grant
- **THEN** the reply's `libraryIDs` omits the restricted Library

#### Scenario: The audit names the Profile the write ran as

- **WHEN** an Admin Session switches to another Profile and then writes that Profile's track
  preferences in the same operation
- **THEN** the privileged audit record for `updateProfile` names the Profile switched to
