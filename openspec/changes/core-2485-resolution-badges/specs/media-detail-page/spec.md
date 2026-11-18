## ADDED Requirements

### Requirement: Resolution and HDR badges

Each Version listed by `mediaTracks` SHALL carry the resolution label of its stored frame size when
that size is known. The web Media detail page SHALL show the shown Version's resolution label and,
when the Version is HDR, its HDR format (HDR10, HDR10+, HLG or Dolby Vision) as badges, and SHALL
show no badge for an unknown resolution or an SDR Version.

#### Scenario: 4K Dolby Vision title

- **WHEN** a title's only Version is 3840x2160 Dolby Vision
- **THEN** the detail page shows `4K` and `Dolby Vision` badges

#### Scenario: Unknown frame size

- **WHEN** the Version's frame size is not stored
- **THEN** no resolution badge is shown
