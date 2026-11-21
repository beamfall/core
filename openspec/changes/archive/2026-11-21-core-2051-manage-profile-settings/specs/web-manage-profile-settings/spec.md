## ADDED Requirements

### Requirement: The owner edits Profile settings from Manage

Manage's Switch profile screen SHALL require an active Admin Session to edit an adult Profile's
entry PIN or the current Profile's preferred subtitle languages. It SHALL use the existing reads
and mutations and display localized feedback after an attempted save.

#### Scenario: The owner sets or changes an entry PIN

- **WHEN** the owner selects an adult Profile
- **THEN** the screen offers to set or change its entry PIN according to the server's PIN status
- **AND** a matching new PIN and confirmation save to that Profile without revealing the existing PIN

#### Scenario: The owner edits subtitle languages

- **WHEN** the owner opens Profile settings
- **THEN** the subtitle field shows the current Profile's stored languages in preference order
- **AND** saving replaces the list in that order, and saving an empty field clears the list

#### Scenario: A write is refused

- **WHEN** the server refuses either save
- **THEN** the form shows a localized error and the saved setting is never reported as changed
