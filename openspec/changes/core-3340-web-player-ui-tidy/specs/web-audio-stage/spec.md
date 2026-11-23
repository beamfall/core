## ADDED Requirements

### Requirement: Listening controls live in the Settings sheet

On the audio stage, the Speed, Remove long pauses and Sleep timer controls SHALL appear as rows in
the transport's Settings sheet, not on the stage. Their pickers SHALL open within the sheet, and
choosing from them SHALL NOT close it. Radio tuning controls SHALL sit in the player bar beside
Back and SHALL NOT overlap it.

#### Scenario: Opening Settings on the audio stage

- **WHEN** a listener opens Settings while an audio track plays on the audio stage
- **THEN** the sheet lists Playback speed, Remove long pauses and Sleep timer rows, and the stage shows none of them

#### Scenario: Radio station playing

- **WHEN** a radio station plays on the audio stage
- **THEN** its tuning selects appear in the player bar after Back, without overlapping it
