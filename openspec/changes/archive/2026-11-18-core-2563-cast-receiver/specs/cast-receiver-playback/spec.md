## ADDED Requirements

### Requirement: The Default Media Receiver resolves from the shipped baseline

The release-embedded quirks database SHALL carry a selector for client
`chromecast-default-receiver`, so a playback grant minted for that client is receiver-bound and
receiver-resolved, and its route never exceeds H.264, 720 lines, stereo AAC and SDR, however
broad the sender's declared profile. The cap is 720 lines because the one client id covers every
Cast device and Google's Cast supported-media table lists Nest Hub and Nest Hub Max at 720p.

#### Scenario: Cast load to the Default Media Receiver

- **WHEN** a sender requests playback info with capability profile client `chromecast-default-receiver`
- **THEN** `appliedQuirks` contains `chromecast-default-receiver-floor`
- **AND** the stream's token grant is receiver-bound with `receiverProfileResolved` true

#### Scenario: A broad sender profile cannot route past the floor

- **WHEN** a sender declaring HEVC, E-AC-3 and AAC up to 2160 lines casts an H.264 720p source with
  stereo E-AC-3, or an HEVC 1080p source with 5.1 E-AC-3
- **THEN** neither is direct-played, and the HLS grant encodes AAC with at most two channels and caps
  the client height at 720

#### Scenario: Unknown receiver stays refused

- **WHEN** a sender requests playback info with client `chromecast-unknown`
- **THEN** the grant is receiver-bound and not receiver-resolved, and the byte routes refuse it

### Requirement: Core web offers casting

The core web player SHALL show its Cast control for video whenever the Cast SDK reports an
available device, with no build-time readiness flag.

#### Scenario: Cast device present

- **WHEN** a video plays in core web and the Cast SDK reports a device
- **THEN** the Cast control is rendered
