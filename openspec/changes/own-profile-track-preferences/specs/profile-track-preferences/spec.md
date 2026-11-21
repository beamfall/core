## ADDED Requirements

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
