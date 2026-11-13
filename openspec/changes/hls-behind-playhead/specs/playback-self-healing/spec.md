## ADDED Requirements

### Requirement: A segment request waits for a healthy encoder behind the playhead

The live HLS session manager SHALL keep a segment request waiting while the session's encoder
for that rendition is still the same run and has written at least one new segment during the
last latency-budget window. It SHALL fail the request when a window passes with no new segment,
or when another request replaced the encoder. It SHALL NOT restart an encoder that is making
progress.

#### Scenario: Encoder slower than real time

- **WHEN** a request waits for a segment past the encoder's frontier and the encoder keeps
  writing segments, but more slowly than one latency budget per segment
- **THEN** the request returns the segment once it is written, without a restart and without
  failing

#### Scenario: Encoder stalls

- **WHEN** a request waits for a segment and the encoder writes nothing for a whole window
- **THEN** the request fails after that window

### Requirement: The web player reports only presented progress

The web player SHALL report as playback progress only a position whose frame was presented.
While a seek is pending it SHALL report the last presented position. Before the load has
presented any frame it SHALL report nothing.

#### Scenario: A far seek stalls

- **WHEN** the viewer seeks to a position the stream has not produced yet and pauses or leaves
  before that frame is presented
- **THEN** the progress report carries the position presented before the seek
