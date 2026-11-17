## ADDED Requirements

### Requirement: The startup log names the effective transcode cap

At startup the server SHALL log the transcode cap in force, its source and the automatic cap. The
source SHALL be `env/flag (BEAMFALL_TRANSCODE_CAP)`, `console` or `automatic`, in the precedence
of "An operator cap overrides the automatic cap".

#### Scenario: Env cap below the automatic cap

- **WHEN** the automatic cap is 6 and `BEAMFALL_TRANSCODE_CAP` is 2
- **THEN** the log reports cap 2 from `env/flag (BEAMFALL_TRANSCODE_CAP)`, with automatic cap 6
