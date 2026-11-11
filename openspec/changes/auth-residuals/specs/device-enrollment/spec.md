## ADDED Requirements

### Requirement: Demo enrollment obeys the remote-enrollment policy

An enrollment that presents the opt-in demo grant SHALL be subject to the same remote-enrollment
policy as pairing-code enrollment. When remote enrollment is disabled, a demo-grant enrollment that
did not arrive on the local network SHALL be refused with the same answer as an invalid code, and
the refusal SHALL count as a failed attempt.

#### Scenario: Off-LAN demo enrollment with remote enrollment disabled

- **WHEN** remote enrollment is disabled and an off-LAN request presents the demo grant
- **THEN** no Device or Session is created and the request receives the invalid-code answer

#### Scenario: Off-LAN demo enrollment with remote enrollment enabled

- **WHEN** remote enrollment is enabled and an off-LAN request presents the demo grant
- **THEN** a viewer Device is enrolled for the grant's Profile
