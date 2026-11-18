## ADDED Requirements

### Requirement: The Default Media Receiver resolves from the shipped baseline

The release-embedded quirks database SHALL carry a selector for client
`chromecast-default-receiver`, so a playback grant minted for that client is receiver-bound and
receiver-resolved, and its route never exceeds H.264, 1080 lines, two audio channels and SDR.

#### Scenario: Cast load to the Default Media Receiver

- **WHEN** a sender requests playback info with capability profile client `chromecast-default-receiver`
- **THEN** `appliedQuirks` contains `chromecast-default-receiver-floor`
- **AND** the stream's token grant is receiver-bound with `receiverProfileResolved` true

#### Scenario: Unknown receiver stays refused

- **WHEN** a sender requests playback info with client `chromecast-unknown`
- **THEN** the grant is receiver-bound and not receiver-resolved, and the byte routes refuse it

### Requirement: Core web offers casting

The core web player SHALL show its Cast control for video whenever the Cast SDK reports an
available device, with no build-time readiness flag.

#### Scenario: Cast device present

- **WHEN** a video plays in core web and the Cast SDK reports a device
- **THEN** the Cast control is rendered
