## ADDED Requirements

### Requirement: Resume-eligible audio finishes on remaining time

The server SHALL mark resume-eligible audio played only when 30 seconds or less of its duration
remain (and at least the audio played fraction has played), or when the client reports it
finished. Before that it SHALL keep the reported resume position. Audio that is not
resume-eligible SHALL keep the 80% played rule, and video SHALL keep the 95% rule.

#### Scenario: Long audiobook at 80%

- **WHEN** a Profile reports 16 hours into a 20-hour resume-eligible audio Media
- **THEN** the Media stays unplayed, keeps its position, and stays in Continue Watching

#### Scenario: Book end

- **WHEN** the same Media is reported with 20 seconds remaining
- **THEN** it is marked played, counts one play, and its resume position is cleared

#### Scenario: Music unchanged

- **WHEN** audio that is not resume-eligible is reported at 80%
- **THEN** it is marked played
