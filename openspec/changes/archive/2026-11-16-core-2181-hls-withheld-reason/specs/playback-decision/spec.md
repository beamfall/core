## ADDED Requirements

### Requirement: A direct-play decision names a Profile gate that withheld the HLS stream

When the negotiation decides `direct-play` and withholds the HLS stream because the session
Profile's `transcodeAllowed` is false, the playback decision reason SHALL be `transcode-disallowed`.
A Dolby Vision P7 base-layer decision SHALL keep reason `dolby-vision-p7-base-layer`. When the HLS
stream is offered, or is withheld for any other cause, a `direct-play` decision SHALL carry no
reason.

#### Scenario: Profile disallows transcoding

- **WHEN** a direct-playable item is negotiated for a Profile whose `transcodeAllowed` is false
- **THEN** the decision is `direct-play` with reason `transcode-disallowed` and only the direct-play stream

#### Scenario: Profile allows transcoding

- **WHEN** a direct-playable item is negotiated for a Profile whose `transcodeAllowed` is true
- **THEN** the decision is `direct-play` with no reason and an HLS stream beside the direct-play stream
