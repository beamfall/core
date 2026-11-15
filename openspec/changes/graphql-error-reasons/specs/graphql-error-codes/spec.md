## ADDED Requirements

### Requirement: A reason refines a code where clients must act differently

A GraphQL error MAY carry `extensions.reason` from a closed vocabulary published in the wire
contract. A reason SHALL only refine the error's `code`, never replace it. Clients SHALL branch on
the reason, not the message text, and SHALL treat a reason they do not know as absent.

#### Scenario: Export with no reveal PIN set

- **WHEN** an adult profile with no reveal PIN requests `myEngagement`
- **THEN** the error carries code `FORBIDDEN` and reason `REVEAL_PIN_NOT_SET`

#### Scenario: PIN missing

- **WHEN** a profile with a reveal PIN requests `myEngagement` or `deleteMyData` without a PIN
- **THEN** the error carries code `FORBIDDEN` and reason `REVEAL_PIN_REQUIRED`

#### Scenario: PIN wrong

- **WHEN** a profile with a reveal PIN requests `myEngagement` or `deleteMyData` with a wrong PIN
- **THEN** the error carries code `FORBIDDEN` and reason `REVEAL_PIN_INCORRECT`

#### Scenario: The vocabulary is closed

- **WHEN** code sets a reason that is not in the published vocabulary
- **THEN** conformance fails
