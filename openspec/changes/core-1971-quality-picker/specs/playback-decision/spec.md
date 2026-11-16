## ADDED Requirements

### Requirement: A viewer quality step is honoured below every cap

A viewer quality step is a `maxStreamingBitrate` equal to an advertised rendition's `bandwidth`.
Negotiation under such a ceiling SHALL advertise that rendition as the highest one, SHALL NOT
advertise any rendition above the lower of that ceiling and any admin remote cap that applies, and
a request without a ceiling SHALL negotiate exactly as it did before quality steps existed.

#### Scenario: Ladder step tops out at that step

- **WHEN** a source that transcodes to 1080p, 720p and 480p rungs is requested with the 720p
  rung's bandwidth as its ceiling
- **THEN** the highest advertised rendition is the 720p rung and none exceeds its bandwidth

#### Scenario: Admin cap tighter than the step

- **WHEN** a remote request picks the 1080p step while the profile's remote cap is 3 Mbit/s
- **THEN** no advertised rendition exceeds 3 Mbit/s

#### Scenario: Auto is today's negotiation

- **WHEN** the same request carries no ceiling
- **THEN** the decision, reason and renditions match a request made without the field
