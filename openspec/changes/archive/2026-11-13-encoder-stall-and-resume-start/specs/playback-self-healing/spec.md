## ADDED Requirements

### Requirement: A waiting request restarts a stalled encoder

The live HLS session manager SHALL track each live encoder's output progress. While a request
waits on an encoder, it SHALL kill the encoder if the encoder's output position has not advanced
for a bounded interval. It SHALL treat the kill as an unexpected exit: the waiting request
restarts the encoder within the existing restart bound and session budget, and fails promptly
once they are used up. It SHALL NOT kill an encoder that no request is waiting on.

#### Scenario: Encoder hangs without exiting

- **WHEN** a request waits on an encoder whose output position stops advancing
- **THEN** the encoder is killed after the stall interval and restarted at the requested segment

#### Scenario: Encoder keeps hanging

- **WHEN** every restarted encoder also stops advancing
- **THEN** the request fails with a classified encoder failure once the restart bound is used up,
  and the stall is logged

### Requirement: A resumed transcode starts its encoder once

When playback info resumes a viewer at a non-zero position, the live HLS session SHALL start
its first encoder at the segment that contains that position. A request for the init segment
SHALL reuse any encoder already running for its rendition.

#### Scenario: Resume at an offset

- **WHEN** a client resumes a transcode at a non-zero position and requests the init segment,
  then the segment at that position
- **THEN** the encoder is spawned once, at that segment
