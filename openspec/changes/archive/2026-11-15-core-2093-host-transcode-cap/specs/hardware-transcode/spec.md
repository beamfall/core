## ADDED Requirements

### Requirement: The automatic transcode cap fits the host

When neither an environment variable or flag nor the Console sets a concurrent-transcode cap, the
server SHALL derive the cap from the host at startup. With no verified hardware encoder selected,
the cap SHALL be one session per four usable cores, at least 1 and at most 8. With a verified
hardware encoder selected, the cap SHALL be one session per usable core, at least 2, and at most
the selected backend's limit: 8 for NVENC, 6 for QSV and VA-API, and 4 for VideoToolbox.

#### Scenario: Software transcode on a small host

- **WHEN** no hardware encoder is verified and the host has 4 usable cores
- **THEN** the automatic cap is 1

#### Scenario: Software transcode on a large host

- **WHEN** no hardware encoder is verified and the host has 64 usable cores
- **THEN** the automatic cap is 8

#### Scenario: NVENC is bounded by the driver session limit

- **WHEN** NVENC is the selected encoder and the host has 16 usable cores
- **THEN** the automatic cap is 8

#### Scenario: Hardware transcode on a two-core host

- **WHEN** QSV is the selected encoder and the host has 2 usable cores
- **THEN** the automatic cap is 2

### Requirement: An operator cap overrides the automatic cap

An environment variable or flag cap SHALL take precedence over the stored Console cap, and the
stored Console cap SHALL take precedence over the automatic cap. The Console SHALL report the
automatic cap as the effective cap when no cap is stored.

#### Scenario: Stored cap wins over the automatic cap

- **WHEN** the automatic cap is 8 and the Console stores a cap of 3
- **THEN** the transcoder admits at most 3 sessions
