## ADDED Requirements

### Requirement: Console manages transcoder resources

The transcode config SHALL expose the maximum number of simultaneous transcodes, the transcoder
temp directory and an HDR tone-map switch. For the cap and the temp directory it SHALL report the
value in effect, whether an environment variable or flag overrides the saved value, and whether a
restart is required to apply it. A write SHALL be recorded as a `config.update` activity on
`transcode`.

#### Scenario: The cap applies live

- **WHEN** a manager saves `maxConcurrentTranscodes` 3 with no `BEAMFALL_TRANSCODE_CAP` set
- **THEN** the running transcoder admits at most 3 transcodes without a restart
- **AND** `effectiveMaxConcurrentTranscodes` is 3 and `maxConcurrentTranscodesRestartRequired` is false

#### Scenario: An environment variable overrides the saved cap

- **WHEN** `BEAMFALL_TRANSCODE_CAP` is 6 and a manager saves a cap of 2
- **THEN** the cap 2 is stored, `effectiveMaxConcurrentTranscodes` stays 6
- **AND** `maxConcurrentTranscodesEnvOverride` is true

#### Scenario: A zero or negative cap is rejected

- **WHEN** a manager saves `maxConcurrentTranscodes` 0 or -2
- **THEN** the write fails with a client error and nothing is stored or applied

#### Scenario: A changed temp directory needs a restart

- **WHEN** a manager saves an existing, writable absolute `tempDir` other than the one in use
- **THEN** it is stored, `effectiveTempDir` still names the directory in use
- **AND** `tempDirRestartRequired` is true

#### Scenario: An unusable temp directory is rejected

- **WHEN** a manager saves a `tempDir` that does not exist, is relative or is not writable
- **THEN** the write fails with a client error and nothing is stored

### Requirement: The tone-map switch refuses instead of skipping conversion

With tone mapping turned off, a transcode that needs HDR-to-SDR conversion SHALL be refused with
reason `hdr-tone-map-disabled`; the server SHALL NOT encode it without conversion. The switch
SHALL apply without a restart.

#### Scenario: Tone mapping off

- **WHEN** tone mapping is off and a client that cannot display HDR requests an HDR video that must be re-encoded
- **THEN** playback is `unavailable` with reason `hdr-tone-map-disabled`
