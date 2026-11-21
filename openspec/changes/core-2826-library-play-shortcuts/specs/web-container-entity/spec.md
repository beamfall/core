## ADDED Requirements

### Requirement: The primary action names the child it plays

When the next-up child has a plugin subtitle, the container Entity page's accent button SHALL read
Play or Resume followed by the subtitle's parts other than the child's title, joined by spaces, and
its accessible name SHALL also carry the child's title. An audio listing, or a child without a
subtitle, SHALL keep the plain Play or Resume label. The page SHALL NOT name a plugin kind.

#### Scenario: Next-up child with a subtitle

- **WHEN** the next-up child's subtitle is "S1 · E3" and it has no saved position
- **THEN** the button reads "Play S1 E3"

#### Scenario: Child in progress

- **WHEN** that child has a saved position
- **THEN** the button reads "Resume S1 E3"

#### Scenario: No subtitle

- **WHEN** the next-up child has no subtitle
- **THEN** the button reads "Play"
