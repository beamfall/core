## ADDED Requirements

### Requirement: A segment request waits for a healthy encoder behind the playhead

The live HLS session manager SHALL keep a segment request waiting while the encoder it waits on
is still the session's encoder for that rendition, started at or before the requested segment,
and wrote a segment during the last latency-budget window. Otherwise it SHALL fail the request
after that window. It SHALL NOT restart an encoder that is making progress.

#### Scenario: Encoder slower than real time

- **WHEN** a request waits for a segment past the encoder's frontier and the encoder keeps
  writing at least one segment per latency budget, but more slowly than real time
- **THEN** the request returns the segment once it is written, without a restart and without
  failing

#### Scenario: Segments cached past the encoder

- **WHEN** segments from an earlier encoder already exist past the one the encoder is writing
- **THEN** they do not count as progress, and the encoder's own writes do

#### Scenario: Encoder replaced past the segment

- **WHEN** a request waits for a segment and another request replaces the encoder with one
  started past that segment
- **THEN** the request fails after one window, however many segments the new encoder writes

#### Scenario: Encoder stalls

- **WHEN** a request waits for a segment and the encoder writes nothing for a whole window
- **THEN** the request fails after that window

### Requirement: The web player reports only presented progress

The web player SHALL report as playback progress only a position whose frame was presented.
While a seek is pending, or before the element has decoded the current frame, it SHALL report
the last presented position. Before the load has presented any frame it SHALL report nothing.

#### Scenario: A far seek stalls

- **WHEN** the viewer seeks to a position the stream has not produced yet and pauses or leaves
  before that frame is presented
- **THEN** the progress report carries the position presented before the seek
