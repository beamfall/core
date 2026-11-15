## ADDED Requirements

### Requirement: Setup wizard radios have human-readable names

Each radio in the setup wizard SHALL have an accessible name made of readable words, never an internal
identifier.

#### Scenario: Sign-in security radios

- **WHEN** the security step is read by a screen reader
- **THEN** each radio is announced by its label, such as the mode's name, and the group is named by
  its heading
